// Package localconsole serves the optional read-only stdio console on loopback.
package localconsole

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"jira-mcp/internal/audit"
	"jira-mcp/internal/console"
)

type Server struct {
	URL    string
	server *http.Server
}

// Start binds only IPv4 loopback on an OS-selected port.
func Start(store audit.Store, report func(error)) (*Server, error) {
	if store == nil {
		return nil, errors.New("local audit store required")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	host := listener.Addr().String()
	server := &http.Server{Handler: newHandler(store, host), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	result := &Server{URL: "http://" + host + "/web/overview", server: server}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && report != nil {
			report(err)
		}
	}()
	return result, nil
}

func (s *Server) Close() error {
	return s.server.Close()
}

func newHandler(store audit.Store, host string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
		remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(remoteHost)
		origin := r.Header.Get("Origin")
		fetchSite := r.Header.Get("Sec-Fetch-Site")
		if err != nil || ip == nil || !ip.IsLoopback() || r.Host != host || (origin != "" && origin != "http://"+host) || (fetchSite != "" && fetchSite != "none" && fetchSite != "same-origin") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		section := ""
		switch r.URL.Path {
		case "/web/", "/web":
			http.Redirect(w, r, "/web/overview", http.StatusFound)
			return
		case "/web/overview":
			section = "overview"
		case "/web/monitoring":
			section = "monitoring"
		case "/web/audit":
			section = "audit"
		default:
			http.NotFound(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		events, err := store.List(ctx, "local", 200)
		page := console.NewPage(section, "", "", "", events, err != nil, time.Now())
		page.Local = true
		var buf bytes.Buffer
		if err := console.Render(&buf, page); err != nil {
			http.Error(w, "page unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = buf.WriteTo(w)
	})
}
