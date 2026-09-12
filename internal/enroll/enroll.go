// Package enroll implements the client side of the OAuth 2.0 Device Authorization
// Grant (RFC 8628) that the bridge uses to authenticate against trencitos without
// the user ever entering a token (Requirements.md §5 "Authentication", §11
// "short-lived authorization codes", and Acceptance criterion 10).
//
// The flow is two steps:
//
//  1. RequestCode POSTs to the device-authorization endpoint and receives a
//     user_code + verification_uri to show the operator, plus a device_code and
//     polling interval (RFC 8628 §3.1–3.2).
//  2. Poll repeatedly POSTs the device_code to the token endpoint until the user
//     approves in the browser, honoring authorization_pending / slow_down and
//     failing on access_denied / expired_token (RFC 8628 §3.4–3.5).
//
// The concrete trencitos endpoint paths and any tenant-binding fields on the
// token response are a Phase 0 contract item (PhaseMapping.md); the defaults here
// follow the RFC and common OAuth conventions and are overridable via Config.
package enroll

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// deviceCodeGrantType is the RFC 8628 §3.4 grant_type value for token polling.
const deviceCodeGrantType = "urn:ietf:params:oauth:grant-type:device_code"

// defaultInterval is the poll interval used when the server omits one
// (RFC 8628 §3.2: "If no value is provided, clients MUST use 5 as the default").
const defaultInterval = 5 * time.Second

// Config configures the device-authorization client.
type Config struct {
	// DeviceAuthURL is the RFC 8628 device-authorization endpoint.
	DeviceAuthURL string
	// TokenURL is the OAuth token endpoint.
	TokenURL string
	// ClientID identifies this bridge installation (Requirements.md §5 step 1);
	// the installation ID is used.
	ClientID string
	// Scope is an optional space-delimited scope request.
	Scope string
	// HTTPClient overrides the default client (for tests or custom TLS/proxy).
	HTTPClient *http.Client
}

// DeviceAuth is the device-authorization response (RFC 8628 §3.2).
type DeviceAuth struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// interval returns the poll interval, applying the RFC default when unset.
func (d DeviceAuth) interval() time.Duration {
	if d.Interval <= 0 {
		return defaultInterval
	}
	return time.Duration(d.Interval) * time.Second
}

// activationURL is the URL to open in the browser: the pre-filled
// verification_uri_complete when the server provides it, else verification_uri.
func (d DeviceAuth) ActivationURL() string {
	if d.VerificationURIComplete != "" {
		return d.VerificationURIComplete
	}
	return d.VerificationURI
}

// Token is the successful token response (RFC 8628 §3.5 / RFC 6749 §5.1). The
// tenant_id / tenant_name fields are a trencitos extension binding the token to
// a tenant (Requirements.md §12) for display; they are optional.
type Token struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	TenantID    string `json:"tenant_id,omitempty"`
	TenantName  string `json:"tenant_name,omitempty"`
}

// tokenError is the OAuth error response body (RFC 6749 §5.2, RFC 8628 §3.5).
type tokenError struct {
	Code        string `json:"error"`
	Description string `json:"error_description"`
}

// Sentinel errors for the terminal outcomes of polling.
var (
	// ErrAccessDenied means the user (or an admin) declined the authorization.
	ErrAccessDenied = errors.New("authorization denied")
	// ErrExpired means the device code expired before the user approved it.
	ErrExpired = errors.New("device code expired before authorization")
)

// Client performs the device-authorization grant against one server.
type Client struct {
	cfg   Config
	hc    *http.Client
	sleep func(ctx context.Context, d time.Duration) error
}

// New constructs a Client from cfg.
func New(cfg Config) *Client {
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{cfg: cfg, hc: hc, sleep: sleepCtx}
}

// sleepCtx sleeps for d or returns early if ctx is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// RequestCode performs the device-authorization request (RFC 8628 §3.1–3.2).
func (c *Client) RequestCode(ctx context.Context) (DeviceAuth, error) {
	form := url.Values{"client_id": {c.cfg.ClientID}}
	if c.cfg.Scope != "" {
		form.Set("scope", c.cfg.Scope)
	}
	resp, err := c.postForm(ctx, c.cfg.DeviceAuthURL, form)
	if err != nil {
		return DeviceAuth{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return DeviceAuth{}, fmt.Errorf("device authorization request failed: %s: %s",
			resp.Status, describe(body))
	}
	var da DeviceAuth
	if err := json.Unmarshal(body, &da); err != nil {
		return DeviceAuth{}, fmt.Errorf("decode device authorization response: %w", err)
	}
	if da.DeviceCode == "" || da.UserCode == "" || da.VerificationURI == "" {
		return DeviceAuth{}, errors.New("device authorization response missing required fields")
	}
	return da, nil
}

// Poll polls the token endpoint until the user approves, the code expires, the
// server denies access, or ctx is cancelled. It honors the server's interval and
// slow_down responses (RFC 8628 §3.4–3.5).
func (c *Client) Poll(ctx context.Context, da DeviceAuth) (Token, error) {
	interval := da.interval()
	form := url.Values{
		"grant_type":  {deviceCodeGrantType},
		"device_code": {da.DeviceCode},
		"client_id":   {c.cfg.ClientID},
	}
	for {
		if err := c.sleep(ctx, interval); err != nil {
			return Token{}, err
		}
		tok, retry, wait, err := c.pollOnce(ctx, form)
		if err != nil {
			return Token{}, err
		}
		if !retry {
			return tok, nil
		}
		// slow_down (RFC 8628 §3.5): increase the interval by 5s and keep polling.
		if wait > 0 {
			interval = wait
		}
	}
}

// pollOnce performs one token request. It returns (token, retry=false) on
// success, (_, retry=true, newInterval) on authorization_pending/slow_down, or a
// terminal error otherwise.
func (c *Client) pollOnce(ctx context.Context, form url.Values) (Token, bool, time.Duration, error) {
	resp, err := c.postForm(ctx, c.cfg.TokenURL, form)
	if err != nil {
		return Token{}, false, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode == http.StatusOK {
		var tok Token
		if err := json.Unmarshal(body, &tok); err != nil {
			return Token{}, false, 0, fmt.Errorf("decode token response: %w", err)
		}
		if tok.AccessToken == "" {
			return Token{}, false, 0, errors.New("token response missing access_token")
		}
		return tok, false, 0, nil
	}

	// Non-200: parse the OAuth error code to decide continue vs. fail.
	var te tokenError
	_ = json.Unmarshal(body, &te)
	switch te.Code {
	case "authorization_pending":
		return Token{}, true, 0, nil
	case "slow_down":
		return Token{}, true, 5 * time.Second, nil
	case "access_denied":
		return Token{}, false, 0, ErrAccessDenied
	case "expired_token":
		return Token{}, false, 0, ErrExpired
	default:
		return Token{}, false, 0, fmt.Errorf("token request failed: %s: %s", resp.Status, describe(body))
	}
}

func (c *Client) postForm(ctx context.Context, endpoint string, form url.Values) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return c.hc.Do(req)
}

// describe renders a response body for an error message without dumping a huge
// or binary payload.
func describe(body []byte) string {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return "(empty response)"
	}
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
