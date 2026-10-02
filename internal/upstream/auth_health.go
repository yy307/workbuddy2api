package upstream

import (
	"encoding/json"
	"errors"

	"workbuddy2api/internal/auth"
)

// CheckAuthorization reuses the authenticated, read-only billing request.
// An empty package list is valid; a missing response is not evidence of auth.
// No token refresh, sign-in, pool transition or model call happens here.
func (c *Client) CheckAuthorization(a *auth.Auth) error {
	data, err := c.getUserResourceData(a)
	if err != nil {
		return err
	}
	var shape struct {
		Response *struct {
			Error json.RawMessage
			Data  *struct{ Accounts json.RawMessage }
		}
	}
	if json.Unmarshal(data, &shape) != nil || shape.Response == nil ||
		(len(shape.Response.Error) != 0 && string(shape.Response.Error) != "null") ||
		shape.Response.Data == nil {
		return errors.New("authorization response unavailable")
	}
	var accounts []json.RawMessage
	raw := shape.Response.Data.Accounts
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &accounts) != nil {
		return errors.New("authorization response unavailable")
	}
	// Keep the original typed parser's validation of account fields.
	var parsed userResourceResp
	if json.Unmarshal(data, &parsed) != nil {
		return errors.New("authorization response unavailable")
	}
	for _, account := range accounts {
		var row map[string]json.RawMessage
		if json.Unmarshal(account, &row) != nil || row == nil {
			return errors.New("authorization response unavailable")
		}
	}
	return nil
}
