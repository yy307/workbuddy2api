package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"workbuddy2api/internal/auth"
	"workbuddy2api/internal/upstream"
)

const authHealthID = "workbuddy_account_authorization"

type authHealth struct {
	CheckID   string `json:"check_id"`
	Status    string `json:"status"`
	Code      string `json:"code"`
	CheckedAt int64  `json:"checked_at"`
	Accounts  int    `json:"accounts"`
	Verified  int    `json:"verified"`
}

type deadlineTransport struct {
	ctx  context.Context
	base http.RoundTripper
}

func (t deadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := t.ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(t.ctx, cancel)
	defer stop()
	defer cancel()
	response, err := t.base.RoundTrip(r.Clone(ctx))
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	invalid := errors.New("authorization response unavailable")
	if response.StatusCode < 400 && response.StatusCode != http.StatusOK {
		return nil, invalid
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, invalid
	}
	if response.StatusCode == http.StatusOK {
		var envelope struct {
			Code *int `json:"code"`
		}
		if json.Unmarshal(raw, &envelope) != nil || envelope.Code == nil {
			return nil, invalid
		}
	}
	response.Body = io.NopCloser(bytes.NewReader(raw))
	return response, nil
}

func runAuthHealth() int {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	up := upstream.New()
	up.GlobalEnabled = true // same realm routing as the existing credit CLI
	up.HTTP.Timeout = 10 * time.Second
	up.HTTP.Transport = deadlineTransport{ctx, up.HTTP.Transport}
	up.HTTP.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }
	out := checkAuthHealth(os.Getenv("WB2A_AUTH_DIR"), up)
	_ = json.NewEncoder(os.Stdout).Encode(out)
	if out.Status != "healthy" {
		return 1
	}
	return 0
}

func checkAuthHealth(dir string, up *upstream.Client) authHealth {
	out := authHealth{CheckID: authHealthID, Status: "failed", Code: "configuration_invalid", CheckedAt: time.Now().Unix()}
	if dir == "" || !authHealthDirSafe(dir) {
		return out
	}
	files, err := auth.LoadAuthFiles(dir)
	if err != nil || len(files) == 0 || len(files) > 32 {
		return out
	}
	out.Accounts = len(files)
	seen := make(map[string]bool)
	// Parse every file before the first request. Unlike ordinary credit mode,
	// malformed accounts cannot disappear silently from a healthy aggregate.
	accounts := make([]*auth.Auth, 0, len(files))
	for _, path := range files {
		raw, err := readHealthAuth(path)
		if err != nil {
			return out
		}
		a, err := auth.Parse(raw)
		if err != nil || a.UID == "" || seen[a.UID] {
			return out
		}
		seen[a.UID] = true
		accounts = append(accounts, a)
	}
	out.Code = "ok"
	for _, a := range accounts {
		err := up.CheckAuthorization(a)
		if err == nil {
			out.Verified++
			continue
		}
		code := "service_unavailable"
		var ue *upstream.Error
		if errors.As(err, &ue) {
			if ue.Status == http.StatusUnauthorized || ue.Kind == upstream.ErrSessionDead {
				code = "authorization_rejected"
			}
			if ue.Kind == upstream.ErrSoftRate {
				code = "rate_limited"
			}
		}
		// Rejection takes precedence, but all configured accounts are checked.
		if out.Code == "ok" || code == "authorization_rejected" {
			out.Code = code
		}
	}
	if out.Verified == out.Accounts {
		out.Status = "healthy"
	}
	out.CheckedAt = time.Now().Unix()
	return out
}
