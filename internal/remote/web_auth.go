package remote

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	webFlowCookie    = "jira_mcp_web_flow"
	webSessionCookie = "jira_mcp_web_session"
	webSessionTTL    = 8 * time.Hour
)

type webFlow struct {
	ClientID  string    `json:"client_id"`
	Verifier  string    `json:"verifier"`
	ExpiresAt time.Time `json:"expires_at"`
}

type webSession struct {
	UserID    string    `json:"user_id"`
	CSRF      string    `json:"csrf"`
	ExpiresAt time.Time `json:"expires_at"`
}

func webKey(prefix, token string) string {
	hash := sha256.Sum256([]byte(token))
	return prefix + hex.EncodeToString(hash[:])
}

func (s *Server) webOrigin() (string, bool, bool) {
	u, err := url.Parse(s.cfg.PublicURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", false, false
	}
	secure := u.Scheme == "https"
	if !secure {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || (!strings.EqualFold(u.Hostname(), "localhost") && (ip == nil || !ip.IsLoopback())) {
			return "", false, false
		}
	}
	return u.Scheme + "://" + u.Host, secure, true
}

func webHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
}

func setWebCookie(w http.ResponseWriter, name, value string, secure bool, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
}

func (s *Server) webLogin(w http.ResponseWriter, r *http.Request) {
	webHeaders(w)
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	origin, secure, ok := s.webOrigin()
	if !ok {
		http.Error(w, "browser login requires HTTPS or loopback HTTP", http.StatusServiceUnavailable)
		return
	}
	if _, ok := s.cfg.Store.(AtomicTakeStore); !ok {
		http.Error(w, "browser login unavailable", http.StatusServiceUnavailable)
		return
	}
	state, err := randomToken(32)
	if err != nil {
		http.Error(w, "browser login unavailable", http.StatusServiceUnavailable)
		return
	}
	verifier, err := randomToken(32)
	if err != nil {
		http.Error(w, "browser login unavailable", http.StatusServiceUnavailable)
		return
	}
	clientID, err := randomToken(18)
	if err != nil {
		http.Error(w, "browser login unavailable", http.StatusServiceUnavailable)
		return
	}
	redirectURI := origin + "/web/callback"
	flow := webFlow{ClientID: clientID, Verifier: verifier, ExpiresAt: time.Now().Add(stateTTL)}
	if err := s.cfg.Store.Put(r.Context(), webKey("web:flow:", state), flow, stateTTL); err != nil {
		http.Error(w, "browser login unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := s.cfg.Store.Put(r.Context(), "mcp:client:"+clientID, struct {
		RedirectURIs []string `json:"redirect_uris"`
		Browser      bool     `json:"browser"`
	}{RedirectURIs: []string{redirectURI}, Browser: true}, stateTTL); err != nil {
		http.Error(w, "browser login unavailable", http.StatusServiceUnavailable)
		return
	}
	sum := sha256.Sum256([]byte(verifier))
	query := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirectURI}, "state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}}
	setWebCookie(w, webFlowCookie, state, secure, int(stateTTL.Seconds()))
	http.Redirect(w, r, origin+"/oauth/authorize?"+query.Encode(), http.StatusFound)
}

func (s *Server) webCallback(w http.ResponseWriter, r *http.Request) {
	webHeaders(w)
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	origin, secure, ok := s.webOrigin()
	take, atomic := s.cfg.Store.(AtomicTakeStore)
	cookie, err := r.Cookie(webFlowCookie)
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if !ok || !atomic || err != nil || state == "" || code == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(state)) != 1 {
		http.Error(w, "invalid browser authorization", http.StatusBadRequest)
		return
	}
	var flow webFlow
	if err := take.Take(r.Context(), webKey("web:flow:", state), &flow); err != nil || time.Now().After(flow.ExpiresAt) {
		http.Error(w, "invalid browser authorization", http.StatusBadRequest)
		return
	}
	setWebCookie(w, webFlowCookie, "", secure, -1)
	var auth authorizationCode
	if err := s.cfg.Store.Get(r.Context(), "oauth:code:"+code, &auth); err != nil || auth.ClientID != flow.ClientID || auth.RedirectURI != origin+"/web/callback" || !verifyPKCE(auth.CodeChallenge, flow.Verifier) {
		http.Error(w, "invalid browser authorization", http.StatusBadRequest)
		return
	}
	var consumed authorizationCode
	if err := take.Take(r.Context(), "oauth:code:"+code, &consumed); err != nil || consumed != auth || auth.UserID == "" {
		http.Error(w, "invalid browser authorization", http.StatusBadRequest)
		return
	}
	token, err := randomToken(32)
	if err != nil {
		http.Error(w, "browser login unavailable", http.StatusServiceUnavailable)
		return
	}
	csrf, err := randomToken(32)
	if err != nil {
		http.Error(w, "browser login unavailable", http.StatusServiceUnavailable)
		return
	}
	session := webSession{UserID: auth.UserID, CSRF: csrf, ExpiresAt: time.Now().Add(webSessionTTL)}
	if err := s.cfg.Store.Put(r.Context(), webKey("web:session:", token), session, webSessionTTL); err != nil {
		http.Error(w, "browser login unavailable", http.StatusServiceUnavailable)
		return
	}
	setWebCookie(w, webSessionCookie, token, secure, int(webSessionTTL.Seconds()))
	http.Redirect(w, r, origin+"/web/overview", http.StatusFound)
}

func (s *Server) currentWebSession(r *http.Request) (webSession, userRecord, bool) {
	cookie, err := r.Cookie(webSessionCookie)
	if err != nil || cookie.Value == "" {
		return webSession{}, userRecord{}, false
	}
	var session webSession
	if s.cfg.Store.Get(r.Context(), webKey("web:session:", cookie.Value), &session) != nil || session.UserID == "" || session.CSRF == "" || !time.Now().Before(session.ExpiresAt) {
		return webSession{}, userRecord{}, false
	}
	var user userRecord
	if s.cfg.Store.Get(r.Context(), "mcp:user:"+session.UserID, &user) != nil || user.AccountID == "" || user.CloudID == "" {
		return webSession{}, userRecord{}, false
	}
	return session, user, true
}

func (s *Server) webSessionInfo(w http.ResponseWriter, r *http.Request) {
	webHeaders(w)
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	session, user, ok := s.currentWebSession(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"account_id": user.AccountID, "cloud_id": user.CloudID, "csrf_token": session.CSRF, "expires_at": session.ExpiresAt.UTC().Format(time.RFC3339)})
}

func (s *Server) webLogout(w http.ResponseWriter, r *http.Request) {
	webHeaders(w)
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	origin, secure, ok := s.webOrigin()
	if !ok || r.Header.Get("Origin") != origin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	session, _, ok := s.currentWebSession(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, oauthMaxBody)
	if err := r.ParseForm(); err != nil || subtle.ConstantTimeCompare([]byte(r.FormValue("csrf_token")), []byte(session.CSRF)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	cookie, _ := r.Cookie(webSessionCookie)
	if err := s.cfg.Store.Delete(r.Context(), webKey("web:session:", cookie.Value)); err != nil {
		http.Error(w, "logout unavailable", http.StatusServiceUnavailable)
		return
	}
	setWebCookie(w, webSessionCookie, "", secure, -1)
	if r.FormValue("return") == "web" {
		http.Redirect(w, r, "/web/signed-out", http.StatusSeeOther)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
