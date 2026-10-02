//go:build darwin || linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func healthFixture(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeTestAuth(t, dir, "fixture-000001", "private-fixture-token", "www.codebuddy.cn", "")
	return dir
}

func TestAuthHealthStrictResponse(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body, code string
	}{
		{"healthy", 200, `{"code":0,"data":{"Response":{"Data":{"Accounts":[]}}}}`, "ok"},
		{"rejected", 401, `{"code":12153,"msg":"private-fixture-token"}`, "authorization_rejected"},
		{"WAF", 403, `<html>private-fixture-token</html>`, "service_unavailable"},
		{"rate", 429, `private-fixture-token`, "rate_limited"},
		{"server", 503, `private-fixture-token`, "service_unavailable"},
		{"missing", 200, `{}`, "service_unavailable"},
		{"null", 200, `{"code":0,"data":{"Response":{"Data":{"Accounts":null}}}}`, "service_unavailable"},
		{"wrong-type", 200, `{"code":0,"data":{"Response":{"Data":{"Accounts":{}}}}}`, "service_unavailable"},
		{"nested-error", 200, `{"code":0,"data":{"Response":{"Error":{"Code":"private"},"Data":{"Accounts":[]}}}}`, "service_unavailable"},
		{"null-row", 200, `{"code":0,"data":{"Response":{"Data":{"Accounts":[null]}}}}`, "service_unavailable"},
		{"malformed-row", 200, `{"code":0,"data":{"Response":{"Data":{"Accounts":[{"CycleCapacitySize":"bad"}]}}}}`, "service_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := healthFixture(t)
			path := filepath.Join(dir, "workbuddy-fixture-.json")
			before, _ := os.ReadFile(path)
			calls := 0
			up := fakeUpstreamCredit(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != "/v2/billing/meter/get-user-resource" {
					t.Fatal("unexpected operation")
				}
				response := creditResp(tc.body)
				response.StatusCode = tc.status
				return response, nil
			})
			out := checkAuthHealth(dir, up)
			if out.Code != tc.code || (out.Status == "healthy") != (tc.code == "ok") || calls != 1 {
				t.Fatalf("unexpected safe result: %+v calls=%d", out, calls)
			}
			raw, _ := json.Marshal(out)
			if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "fixture-") {
				t.Fatal("account leaked")
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("auth mutated")
			}
		})
	}
}

func TestAuthHealthAllAccountsAndRecovery(t *testing.T) {
	dir := healthFixture(t)
	writeTestAuth(t, dir, "secondaa-000002", "second-token", "www.workbuddy.ai", "global")
	mode := "failed"
	calls := 0
	up := fakeUpstreamCredit(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if mode == "failed" && strings.Contains(r.Header.Get("Authorization"), "second-token") {
			response := creditResp(`{"code":12153}`)
			response.StatusCode = 401
			return response, nil
		}
		return creditResp(`{"code":0,"data":{"Response":{"Data":{"Accounts":[]}}}}`), nil
	})
	for _, step := range []string{"failed", "failed", "healthy", "failed"} {
		mode = step
		out := checkAuthHealth(dir, up)
		if out.Status != step || out.Accounts != 2 {
			t.Fatalf("unexpected result: %+v", out)
		}
	}
	if calls != 8 {
		t.Fatal("partial healthy pool hid an account")
	}
}

func TestAuthHealthInvalidFileNeverSkipped(t *testing.T) {
	for _, mode := range []string{"malformed", "permissions", "symlink", "hardlink", "oversized", "duplicate", "no-uid", "missing-token", "empty-dir", "missing-dir", "relative", "too-many"} {
		t.Run(mode, func(t *testing.T) {
			dir := healthFixture(t)
			files, _ := filepath.Glob(filepath.Join(dir, "workbuddy*.json"))
			path := files[0]
			switch mode {
			case "malformed":
				os.WriteFile(path, []byte("private-malformed"), 0600)
			case "permissions":
				os.Chmod(path, 0644)
			case "symlink":
				os.Symlink(path, filepath.Join(dir, "workbuddy-link.json"))
			case "hardlink":
				os.Link(path, filepath.Join(dir, "workbuddy-link.json"))
			case "oversized":
				os.WriteFile(path, []byte(strings.Repeat("x", 65537)), 0600)
			case "duplicate":
				raw, _ := os.ReadFile(path)
				os.WriteFile(filepath.Join(dir, "workbuddy-copy.json"), raw, 0600)
			case "no-uid":
				os.WriteFile(path, []byte(`{"accessToken":"private"}`), 0600)
			case "missing-token":
				os.WriteFile(path, []byte(`{"uid":"private"}`), 0600)
			case "empty-dir":
				os.Remove(path)
			case "missing-dir":
				dir = filepath.Join(dir, "missing")
			case "relative":
				dir = "auths"
			case "too-many":
				for i := 0; i < 33; i++ {
					os.WriteFile(filepath.Join(dir, "workbuddy-"+strings.Repeat("x", i)+".json"), []byte(`{}`), 0600)
				}
			}
			up := fakeUpstreamCredit(t, func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid configuration requested provider")
				return nil, nil
			})
			out := checkAuthHealth(dir, up)
			if out.Status != "failed" || out.Code != "configuration_invalid" {
				t.Fatalf("bad file ignored: %+v", out)
			}
		})
	}
}

func TestAuthHealthTransportDeadlineAndRedirect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	transport := deadlineTransport{ctx, roundTripFn(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })}
	r, _ := http.NewRequest("POST", "https://example.invalid", nil)
	if _, err := transport.RoundTrip(r); !errors.Is(err, context.Canceled) {
		t.Fatal("deadline lost")
	}
	for _, status := range []int{201, 302, 307} {
		transport = deadlineTransport{context.Background(), roundTripFn(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("private"))}, nil
		})}
		if _, err := transport.RoundTrip(r); err == nil {
			t.Fatal("unexpected HTTP response accepted")
		}
	}
	if time.Now().Unix() == 0 {
		t.Fatal("clock unavailable")
	}
}

func TestAuthHealthTransportEnvelope(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{"code":null}`, `{"code":true}`, `{"code":"0"}`, strings.Repeat("x", (1<<20)+1)} {
		transport := deadlineTransport{context.Background(), roundTripFn(func(*http.Request) (*http.Response, error) { return creditResp(body), nil })}
		request, _ := http.NewRequest("POST", "https://example.invalid", nil)
		if _, err := transport.RoundTrip(request); err == nil {
			t.Fatal("invalid envelope accepted")
		}
	}
	transport := deadlineTransport{context.Background(), roundTripFn(func(*http.Request) (*http.Response, error) { return creditResp(`{"code":0,"data":{}}`), nil })}
	request, _ := http.NewRequest("POST", "https://example.invalid", nil)
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal("valid envelope refused")
	}
	raw, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(raw) != `{"code":0,"data":{}}` {
		t.Fatal("body lost")
	}
}

// The ordinary suite skips this explicit native guard. Release verification
// runs the compiled test under sandbox-exec and requires it to pass, not skip.
func TestAuthHealthNativeSandbox(t *testing.T) {
	outside := os.Getenv("WB2A_AUTH_HEALTH_OUTSIDE_PROBE")
	if outside == "" {
		t.Skip("native sandbox verification only")
	}
	conn, err := net.DialTimeout("tcp", "127.0.0.1:9", time.Second)
	if conn != nil {
		conn.Close()
	}
	if !errors.Is(err, syscall.EPERM) {
		t.Fatal("sandbox network guard not proved")
	}
	if err := os.WriteFile(outside, []byte("probe"), 0600); !errors.Is(err, syscall.EPERM) {
		t.Fatal("sandbox write guard not proved")
	}
	dir := healthFixture(t)
	up := fakeUpstreamCredit(t, func(*http.Request) (*http.Response, error) {
		return creditResp(`{"code":0,"data":{"Response":{"Data":{"Accounts":[]}}}}`), nil
	})
	out := checkAuthHealth(dir, up)
	if out.Status != "healthy" {
		t.Fatalf("isolated check failed: %+v", out)
	}
}
