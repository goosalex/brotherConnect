package enroll

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestClient builds a Client pointed at srv with instant, non-blocking sleeps
// so polling loops run without real delays.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c := New(Config{
		DeviceAuthURL: srv.URL + "/oauth/device_authorization",
		TokenURL:      srv.URL + "/oauth/token",
		ClientID:      "bri_test",
		HTTPClient:    srv.Client(),
	})
	c.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	return c
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeOAuthErr(w http.ResponseWriter, code string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": code})
}

func TestRequestCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if got := r.FormValue("client_id"); got != "bri_test" {
			t.Errorf("client_id = %q, want bri_test", got)
		}
		writeJSON(w, http.StatusOK, DeviceAuth{
			DeviceCode:              "devcode123",
			UserCode:                "WDJB-MJHT",
			VerificationURI:         "https://box.dev.trencitos.dev/activate",
			VerificationURIComplete: "https://box.dev.trencitos.dev/activate?code=WDJB-MJHT",
			ExpiresIn:               900,
			Interval:                5,
		})
	}))
	defer srv.Close()

	da, err := newTestClient(t, srv).RequestCode(context.Background())
	if err != nil {
		t.Fatalf("RequestCode: %v", err)
	}
	if da.DeviceCode != "devcode123" || da.UserCode != "WDJB-MJHT" {
		t.Fatalf("unexpected device auth: %+v", da)
	}
	if got := da.ActivationURL(); got != da.VerificationURIComplete {
		t.Fatalf("ActivationURL = %q, want the complete URI", got)
	}
}

func TestRequestCodeServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := newTestClient(t, srv).RequestCode(context.Background()); err == nil {
		t.Fatal("expected error on 500 response")
	}
}

func TestPollPendingThenSuccess(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.FormValue("grant_type"); got != deviceCodeGrantType {
			t.Errorf("grant_type = %q", got)
		}
		if got := r.FormValue("device_code"); got != "devcode123" {
			t.Errorf("device_code = %q", got)
		}
		calls++
		if calls < 3 {
			writeOAuthErr(w, "authorization_pending")
			return
		}
		writeJSON(w, http.StatusOK, Token{
			AccessToken: "final-token",
			TokenType:   "Bearer",
			ExpiresIn:   3600,
			TenantID:    "tenant_7",
			TenantName:  "Layout Club",
		})
	}))
	defer srv.Close()

	tok, err := newTestClient(t, srv).Poll(context.Background(), DeviceAuth{DeviceCode: "devcode123", Interval: 1})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if tok.AccessToken != "final-token" || tok.TenantName != "Layout Club" {
		t.Fatalf("unexpected token: %+v", tok)
	}
	if calls != 3 {
		t.Fatalf("expected 3 polls (2 pending + success), got %d", calls)
	}
}

func TestPollSlowDown(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		switch calls {
		case 1:
			writeOAuthErr(w, "slow_down")
		default:
			writeJSON(w, http.StatusOK, Token{AccessToken: "t"})
		}
	}))
	defer srv.Close()

	if _, err := newTestClient(t, srv).Poll(context.Background(), DeviceAuth{Interval: 1}); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 polls after slow_down, got %d", calls)
	}
}

func TestPollTerminalErrors(t *testing.T) {
	cases := []struct {
		oauthCode string
		want      error
	}{
		{"access_denied", ErrAccessDenied},
		{"expired_token", ErrExpired},
	}
	for _, tc := range cases {
		t.Run(tc.oauthCode, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeOAuthErr(w, tc.oauthCode)
			}))
			defer srv.Close()
			_, err := newTestClient(t, srv).Poll(context.Background(), DeviceAuth{Interval: 1})
			if !errors.Is(err, tc.want) {
				t.Fatalf("Poll err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestPollContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeOAuthErr(w, "authorization_pending")
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := newTestClient(t, srv).Poll(ctx, DeviceAuth{Interval: 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Poll err = %v, want context.Canceled", err)
	}
}

func TestConfigFromServer(t *testing.T) {
	cases := []struct {
		name, server, wantDevice, wantToken string
	}{
		{
			name:       "wss with path and port",
			server:     "wss://box.dev.trencitos.dev:8443/bridge",
			wantDevice: "https://box.dev.trencitos.dev:8443/oauth/device_authorization",
			wantToken:  "https://box.dev.trencitos.dev:8443/oauth/token",
		},
		{
			name:       "ws maps to http",
			server:     "ws://localhost:9000/bridge",
			wantDevice: "http://localhost:9000/oauth/device_authorization",
			wantToken:  "http://localhost:9000/oauth/token",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ConfigFromServer(tc.server, "bri_9")
			if err != nil {
				t.Fatalf("ConfigFromServer: %v", err)
			}
			if cfg.DeviceAuthURL != tc.wantDevice {
				t.Errorf("DeviceAuthURL = %q, want %q", cfg.DeviceAuthURL, tc.wantDevice)
			}
			if cfg.TokenURL != tc.wantToken {
				t.Errorf("TokenURL = %q, want %q", cfg.TokenURL, tc.wantToken)
			}
			if cfg.ClientID != "bri_9" {
				t.Errorf("ClientID = %q, want bri_9", cfg.ClientID)
			}
		})
	}
}

func TestConfigFromServerRejectsBadScheme(t *testing.T) {
	if _, err := ConfigFromServer("ftp://example.com", "bri_1"); err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
}
