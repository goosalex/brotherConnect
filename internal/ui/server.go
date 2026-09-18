// Package ui serves the bridge's local status/config UI on 127.0.0.1. It is a
// thin, read-only-for-now client over the daemon's Snapshot (see
// docs/ui-design.md). Pure stdlib: no cgo, no external assets.
package ui

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"time"

	"brotherConnect/internal/bridge"
)

// DefaultAddr is the loopback address the UI binds by default.
const DefaultAddr = "127.0.0.1:17600"

// Server serves the status UI and JSON API from a Snapshot provider.
type Server struct {
	addr     string
	snapshot func() bridge.Snapshot
	log      *slog.Logger
	tmpl     *template.Template
}

// NewServer returns a UI server bound to addr (e.g. DefaultAddr) that reads
// state from snapshot.
func NewServer(addr string, snapshot func() bridge.Snapshot, log *slog.Logger) *Server {
	if addr == "" {
		addr = DefaultAddr
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Server{
		addr:     addr,
		snapshot: snapshot,
		log:      log,
		tmpl:     template.Must(template.New("status").Parse(statusHTML)),
	}
}

// Handler returns the HTTP handler (exposed for tests).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.guard(s.handleStatusPage))
	mux.HandleFunc("/api/status", s.guard(s.handleStatusJSON))
	return mux
}

// Start runs the server until ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	srv := &http.Server{Addr: s.addr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	s.log.Info("status UI listening", "addr", "http://"+s.addr)
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// guard rejects requests whose Host is not loopback, defending against
// DNS-rebinding (an attacker page resolving a name to 127.0.0.1 to reach this
// server). Only literal loopback hosts are allowed.
func (s *Server) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			host = h
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func (s *Server) handleStatusJSON(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.snapshot())
}

func (s *Server) handleStatusPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.Execute(w, s.snapshot()); err != nil {
		s.log.Error("render status page", "err", err)
	}
}

const statusHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>trencitos bridge</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
 body{font:15px/1.5 system-ui,sans-serif;margin:2rem auto;max-width:760px;padding:0 1rem;color:#111}
 h1{font-size:1.3rem;margin:0 0 .25rem} .sub{color:#666;margin:0 0 1.5rem}
 .state{font-weight:600} .ok{color:#137333} .off{color:#b00020}
 table{border-collapse:collapse;width:100%;margin:.5rem 0 1.5rem}
 th,td{text-align:left;padding:.4rem .6rem;border-bottom:1px solid #eee;font-variant-numeric:tabular-nums}
 th{color:#666;font-weight:600;font-size:.85rem} code{background:#f4f4f4;padding:0 .3rem;border-radius:3px}
 .empty{color:#999}
</style></head><body>
<h1>trencitos print bridge</h1>
<p class="sub">
 <span class="state {{if .Connected}}ok{{else}}off{{end}}">{{if .Connected}}● Connected{{else}}○ Offline{{end}}</span>
 {{if .Tenant}}&nbsp;·&nbsp; tenant <code>{{.Tenant}}</code>{{end}}
 &nbsp;·&nbsp; server <code>{{.Server}}</code>
 &nbsp;·&nbsp; v{{.Version}}
</p>
<p class="sub">installation <code>{{.InstallationID}}</code></p>

<h2 style="font-size:1rem">Printers</h2>
{{if .Printers}}<table><tr><th>Model</th><th>ID</th><th>Status</th><th>Resolution</th><th>Media</th></tr>
{{range .Printers}}<tr><td>{{.Model}}</td><td><code>{{.ID}}</code></td><td>{{.Status}}</td>
<td>{{if .DPI}}{{.DPI}} dpi{{else}}—{{end}}</td><td>{{if .LoadedWidthMM}}{{.LoadedWidthMM}}mm{{if .LoadedHeightMM}}×{{.LoadedHeightMM}}mm{{else}} (continuous){{end}}{{else}}—{{end}}</td></tr>{{end}}
</table>{{else}}<p class="empty">No printers discovered.</p>{{end}}

<h2 style="font-size:1rem">Recent jobs</h2>
{{if .RecentJobs}}<table><tr><th>Job</th><th>State</th><th>When</th><th>Note</th></tr>
{{range .RecentJobs}}<tr><td><code>{{.JobID}}</code></td><td>{{.State}}</td><td>{{.At.Format "15:04:05"}}</td><td>{{.Reason}}</td></tr>{{end}}
</table>{{else}}<p class="empty">No jobs yet.</p>{{end}}
</body></html>`
