package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"brotherConnect/internal/bridge"
	"brotherConnect/internal/printer"
)

func testSnapshot() bridge.Snapshot {
	return bridge.Snapshot{
		Connected:      true,
		Server:         "wss://box.dev.trencitos.dev:8443/bridge",
		InstallationID: "bri_test",
		Version:        "0.1.0",
		Printers: []printer.Printer{
			{ID: "BROTHER_62", Model: "Brother QL-820NWB", Status: printer.StatusReady, LoadedWidthMM: 62},
		},
	}
}

func newTestServer() *Server {
	return NewServer(DefaultAddr, testSnapshot, nil)
}

func TestStatusJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/status", nil)
	newTestServer().Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var s bridge.Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	if !s.Connected || len(s.Printers) != 1 || s.Printers[0].ID != "BROTHER_62" {
		t.Fatalf("snapshot round-trip wrong: %+v", s)
	}
}

func TestStatusPageRendersPrinter(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	newTestServer().Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Brother QL-820NWB") || !strings.Contains(body, "Connected") {
		t.Fatalf("page missing expected content")
	}
}

// A non-loopback Host must be rejected (DNS-rebinding guard).
func TestRejectsNonLoopbackHost(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/status", nil)
	req.Host = "evil.example.com"
	newTestServer().Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for non-loopback host", rec.Code)
	}
}
