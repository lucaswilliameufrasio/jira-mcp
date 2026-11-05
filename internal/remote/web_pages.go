package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"jira-mcp/internal/console"
	"jira-mcp/internal/mcp"
)

// auditIdentity is shared by the writer and the authenticated console reader.
func auditIdentity(user userRecord) string {
	identity, _ := json.Marshal([]string{user.AccountID, user.CloudID})
	return string(identity)
}

func (s *Server) webRoot(w http.ResponseWriter, r *http.Request) {
	webHeaders(w)
	if r.URL.Path != "/web/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	http.Redirect(w, r, "/web/overview", http.StatusFound)
}

func (s *Server) webOverview(w http.ResponseWriter, r *http.Request) {
	s.webPage(w, r, "overview")
}

func (s *Server) webMonitoring(w http.ResponseWriter, r *http.Request) {
	s.webPage(w, r, "monitoring")
}

func (s *Server) webAudit(w http.ResponseWriter, r *http.Request) {
	s.webPage(w, r, "audit")
}

func (s *Server) webPage(w http.ResponseWriter, r *http.Request, section string) {
	webHeaders(w)
	// Firefox sends Origin: null for form submissions from a no-referrer page.
	// Keep cross-origin referrers private while preserving the same-origin logout.
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	session, user, ok := s.currentWebSession(r)
	if !ok {
		http.Redirect(w, r, "/web/login", http.StatusFound)
		return
	}
	var events []mcp.ToolEvent
	unavailable := s.cfg.Audit == nil
	if !unavailable {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		var err error
		events, err = s.cfg.Audit.List(ctx, auditIdentity(user), 200)
		unavailable = err != nil
	}
	page := console.NewPage(section, user.AccountID, user.CloudID, session.CSRF, events, unavailable, time.Now())
	var buf bytes.Buffer
	if err := console.Render(&buf, page); err != nil {
		http.Error(w, "page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

func (s *Server) webSignedOut(w http.ResponseWriter, r *http.Request) {
	webHeaders(w)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><html lang="pt-BR"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Sessão encerrada</title><main><h1>Sessão encerrada</h1><p>A sessão deste navegador foi encerrada.</p><a href="/web/login">Entrar novamente</a></main></html>`))
}
