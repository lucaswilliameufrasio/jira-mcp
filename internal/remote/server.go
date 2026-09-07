package remote

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"jira-mcp/internal/jira"
	"jira-mcp/internal/mcp"
	"jira-mcp/internal/tools"
)

const (
	stateTTL = 10 * time.Minute
	codeTTL  = 60 * time.Second
	tokenTTL = 30 * time.Minute
)

type Config struct {
	PublicURL        string
	AtlassianID      string
	AtlassianKey     string
	AtlassianAuthURL string
	AtlassianAPIURL  string
	Store            Store
	EncryptionKey    []byte
	// RateLimitRPS caps /mcp requests per client per second (burst is 2x).
	// Zero disables limiting; production wiring defaults to 20.
	RateLimitRPS float64
}

type Server struct {
	cfg           Config
	mux           *http.ServeMux
	oauthFlow     *ipLimiter
	oauthRegister *ipLimiter
	mcp           *ipLimiter
}

// oauthMaxBody bounds OAuth request bodies (registration JSON, form posts).
const oauthMaxBody = 16 << 10

// DefaultRateLimitRPS is the /mcp per-client rate limit applied by the
// production wiring when JIRA_MCP_RATE_LIMIT_RPS is unset.
const DefaultRateLimitRPS = 20

type authorizationRequest struct {
	ClientID        string `json:"client_id"`
	RedirectURI     string `json:"redirect_uri"`
	State           string `json:"state"`
	CodeChallenge   string `json:"code_challenge"`
	ChallengeMethod string `json:"code_challenge_method"`
}

type pendingAuthorization struct {
	authorizationRequest
	AtlassianAccessToken  string     `json:"atlassian_access_token"`
	AtlassianRefreshToken string     `json:"atlassian_refresh_token"`
	ExpiresAt             time.Time  `json:"expires_at"`
	AccountID             string     `json:"account_id"`
	Resources             []resource `json:"resources"`
}

type resource struct {
	ID   string `json:"id"`
	URL  string `json:"url"`
	Name string `json:"name"`
}

type userRecord struct {
	AccountID    string    `json:"account_id"`
	CloudID      string    `json:"cloud_id"`
	BaseURL      string    `json:"base_url"`
	Tools        []string  `json:"tools"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type authorizationCode struct {
	UserID        string `json:"user_id"`
	ClientID      string `json:"client_id"`
	RedirectURI   string `json:"redirect_uri"`
	CodeChallenge string `json:"code_challenge"`
}

type accessSession struct {
	UserID string `json:"user_id"`
}

func New(cfg Config) (*Server, error) {
	if cfg.AtlassianAuthURL == "" {
		cfg.AtlassianAuthURL = "https://auth.atlassian.com"
	}
	if cfg.AtlassianAPIURL == "" {
		cfg.AtlassianAPIURL = "https://api.atlassian.com"
	}
	if cfg.PublicURL == "" || cfg.AtlassianID == "" || cfg.AtlassianKey == "" || cfg.Store == nil || len(cfg.EncryptionKey) != 32 {
		return nil, errors.New("remote server requires public URL, Atlassian OAuth credentials, store and a 32-byte encryption key")
	}
	s := &Server{
		cfg:           cfg,
		mux:           http.NewServeMux(),
		oauthFlow:     newIPLimiter(2, 20),
		oauthRegister: newIPLimiter(1.0/6.0, 10),
		mcp:           newIPLimiter(cfg.RateLimitRPS, 2*cfg.RateLimitRPS),
	}
	s.mux.HandleFunc("/.well-known/oauth-protected-resource", s.protectedResource)
	s.mux.HandleFunc("/.well-known/oauth-authorization-server", s.authorizationServer)
	s.mux.Handle("/oauth/register", s.limit(s.oauthRegister, http.HandlerFunc(s.register)))
	s.mux.Handle("/oauth/authorize", s.limit(s.oauthFlow, http.HandlerFunc(s.authorize)))
	s.mux.Handle("/oauth/callback", s.limit(s.oauthFlow, http.HandlerFunc(s.callback)))
	s.mux.Handle("/oauth/complete", s.limit(s.oauthFlow, http.HandlerFunc(s.complete)))
	s.mux.Handle("/oauth/token", s.limit(s.oauthFlow, http.HandlerFunc(s.token)))
	s.mux.Handle("/oauth/revoke", s.limit(s.oauthFlow, http.HandlerFunc(s.revoke)))
	mcpHandler := mcp.NewHTTPHandler(func(req *http.Request) *mcp.Server {
		user, ok := s.userFromRequest(req)
		if !ok {
			return nil
		}
		client := jira.NewClient(jira.Config{
			BaseURL:     strings.TrimRight(cfg.AtlassianAPIURL, "/") + "/ex/jira/" + user.CloudID,
			Deployment:  jira.DeploymentCloud,
			BearerToken: user.AccessToken,
		})
		enabled := make(map[string]bool, len(user.Tools))
		for _, name := range user.Tools {
			enabled[name] = true
		}
		server := mcp.NewServerWithTools("jira-mcp-remote", "1.0.0", enabled)
		tools.Register(server, client)
		return server
	})
	s.mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]string{"status": "ok"})
	})
	s.mux.Handle("/mcp", s.requireAccessToken(s.limit(s.mcp, mcpHandler)))
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) limit(l *ipLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(clientKey(r)) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireAccessToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.userFromRequest(r); !ok {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+strings.TrimRight(s.cfg.PublicURL, "/")+`/.well-known/oauth-protected-resource"`)
			http.Error(w, "missing or invalid bearer token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) userFromRequest(r *http.Request) (userRecord, bool) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return userRecord{}, false
	}
	var session accessSession
	if s.cfg.Store.Get(r.Context(), "mcp:token:"+parts[1], &session) != nil {
		return userRecord{}, false
	}
	var user userRecord
	if s.cfg.Store.Get(r.Context(), "mcp:user:"+session.UserID, &user) != nil {
		return userRecord{}, false
	}
	accessToken, err := open(s.cfg.EncryptionKey, user.AccessToken)
	if err != nil {
		return userRecord{}, false
	}
	user.AccessToken = accessToken
	if user.RefreshToken != "" {
		refreshToken, err := open(s.cfg.EncryptionKey, user.RefreshToken)
		if err != nil {
			return userRecord{}, false
		}
		user.RefreshToken = refreshToken
	}
	if time.Now().After(user.ExpiresAt) {
		if user.RefreshToken == "" {
			return userRecord{}, false
		}
		refreshed, err := s.refreshUser(r.Context(), session.UserID, user)
		if err != nil {
			return userRecord{}, false
		}
		user = refreshed
	}
	return user, true
}

func (s *Server) refreshUser(ctx context.Context, userID string, user userRecord) (userRecord, error) {
	form := url.Values{"grant_type": {"refresh_token"}, "client_id": {s.cfg.AtlassianID}, "client_secret": {s.cfg.AtlassianKey}, "refresh_token": {user.RefreshToken}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.cfg.AtlassianAuthURL, "/")+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return userRecord{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return userRecord{}, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode/100 != 2 {
		return userRecord{}, fmt.Errorf("atlassian refresh failed with HTTP %d", res.StatusCode)
	}
	var token atlassianToken
	if err := json.NewDecoder(res.Body).Decode(&token); err != nil {
		return userRecord{}, err
	}
	accessToken, err := seal(s.cfg.EncryptionKey, token.AccessToken)
	if err != nil {
		return userRecord{}, err
	}
	user.AccessToken = token.AccessToken
	user.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	stored := user
	stored.AccessToken = accessToken
	if token.RefreshToken != "" {
		stored.RefreshToken, err = seal(s.cfg.EncryptionKey, token.RefreshToken)
		if err != nil {
			return userRecord{}, err
		}
		user.RefreshToken = token.RefreshToken
	}
	if err := s.cfg.Store.Put(ctx, "mcp:user:"+userID, stored, 90*24*time.Hour); err != nil {
		return userRecord{}, err
	}
	return user, nil
}

func (s *Server) protectedResource(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"resource": strings.TrimRight(s.cfg.PublicURL, "/") + "/mcp", "authorization_servers": []string{strings.TrimRight(s.cfg.PublicURL, "/")}})
}

func (s *Server) authorizationServer(w http.ResponseWriter, r *http.Request) {
	base := strings.TrimRight(s.cfg.PublicURL, "/")
	writeJSON(w, map[string]any{
		"issuer": base, "authorization_endpoint": base + "/oauth/authorize", "token_endpoint": base + "/oauth/token",
		"registration_endpoint": base + "/oauth/register", "code_challenge_methods_supported": []string{"S256"},
	})
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, oauthMaxBody)
	clientID, err := randomToken(18)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	var req struct {
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid registration request", 400)
		return
	}
	if err := s.cfg.Store.Put(r.Context(), "mcp:client:"+clientID, req, 24*time.Hour); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{"client_id": clientID, "client_name": "jira-mcp", "redirect_uris": req.RedirectURIs, "token_endpoint_auth_method": "none"})
}

// revoke implements RFC 7009 semantics for MCP access tokens: it always
// answers 200 so the endpoint cannot be probed, and silently ignores
// unknown tokens. Knowing the token is the only requirement, which matches
// bearer-token trust: whoever holds it could already use it.
func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, oauthMaxBody)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid request body", 400)
		return
	}
	if token := r.FormValue("token"); token != "" {
		_ = s.cfg.Store.Delete(r.Context(), "mcp:token:"+token)
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := authorizationRequest{ClientID: q.Get("client_id"), RedirectURI: q.Get("redirect_uri"), State: q.Get("state"), CodeChallenge: q.Get("code_challenge"), ChallengeMethod: q.Get("code_challenge_method")}
	if q.Get("response_type") != "code" || req.RedirectURI == "" || req.CodeChallenge == "" || req.ChallengeMethod != "S256" {
		http.Error(w, "invalid authorization request", 400)
		return
	}
	if !s.validClient(r.Context(), req.ClientID, req.RedirectURI) {
		http.Error(w, "unregistered client", 400)
		return
	}
	externalState, err := randomToken(32)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := s.cfg.Store.Put(r.Context(), "oauth:state:"+externalState, req, stateTTL); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	params := url.Values{"audience": {"api.atlassian.com"}, "client_id": {s.cfg.AtlassianID}, "scope": {"read:jira-work write:jira-work read:jira-user offline_access"}, "redirect_uri": {strings.TrimRight(s.cfg.PublicURL, "/") + "/oauth/callback"}, "state": {externalState}, "response_type": {"code"}, "prompt": {"consent"}}
	http.Redirect(w, r, strings.TrimRight(s.cfg.AtlassianAuthURL, "/")+"/authorize?"+params.Encode(), http.StatusFound)
}

func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	var req authorizationRequest
	if s.cfg.Store.Get(r.Context(), "oauth:state:"+state, &req) != nil {
		http.Error(w, "invalid OAuth state", 400)
		return
	}
	_ = s.cfg.Store.Delete(r.Context(), "oauth:state:"+state)
	if r.URL.Query().Get("error") != "" {
		http.Error(w, r.URL.Query().Get("error_description"), 400)
		return
	}
	token, resources, accountID, err := s.exchangeAtlassian(r.Context(), r.URL.Query().Get("code"), r.URL.Query().Get("state"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	accessToken, err := seal(s.cfg.EncryptionKey, token.AccessToken)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	refreshToken := ""
	if token.RefreshToken != "" {
		refreshToken, err = seal(s.cfg.EncryptionKey, token.RefreshToken)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	pending := pendingAuthorization{authorizationRequest: req, AtlassianAccessToken: accessToken, AtlassianRefreshToken: refreshToken, ExpiresAt: time.Now().Add(time.Duration(token.ExpiresIn) * time.Second), AccountID: accountID, Resources: resources}
	if err := s.cfg.Store.Put(r.Context(), "oauth:pending:"+r.URL.Query().Get("state"), pending, stateTTL); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = template.Must(template.New("select").Parse(selectPage)).Execute(w, map[string]any{"State": r.URL.Query().Get("state"), "Resources": resources, "Tools": tools.AvailableToolNames()})
}

func (s *Server) complete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, oauthMaxBody)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid request body", 400)
		return
	}
	state := r.FormValue("state")
	var pending pendingAuthorization
	if s.cfg.Store.Get(r.Context(), "oauth:pending:"+state, &pending) != nil {
		http.Error(w, "invalid pending OAuth state", 400)
		return
	}
	cloudID := r.FormValue("cloud_id")
	var selected []string
	for _, name := range r.Form["tool"] {
		if contains(tools.AvailableToolNames(), name) {
			selected = append(selected, name)
		}
	}
	if len(selected) == 0 {
		selected = tools.AvailableToolNames()
	}
	resourceURL := ""
	for _, item := range pending.Resources {
		if item.ID == cloudID {
			resourceURL = item.URL
			break
		}
	}
	if resourceURL == "" {
		http.Error(w, "invalid Jira site", 400)
		return
	}
	// Single-use: consume the pending state before issuing anything so the
	// site/tool selection form cannot be replayed within its TTL.
	if err := s.cfg.Store.Delete(r.Context(), "oauth:pending:"+state); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	userID := pending.AccountID + ":" + cloudID
	user := userRecord{AccountID: pending.AccountID, CloudID: cloudID, BaseURL: resourceURL, Tools: selected, AccessToken: pending.AtlassianAccessToken, RefreshToken: pending.AtlassianRefreshToken, ExpiresAt: pending.ExpiresAt}
	if err := s.cfg.Store.Put(r.Context(), "mcp:user:"+userID, user, 90*24*time.Hour); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	code, err := randomToken(32)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := s.cfg.Store.Put(r.Context(), "oauth:code:"+code, authorizationCode{UserID: userID, ClientID: pending.ClientID, RedirectURI: pending.RedirectURI, CodeChallenge: pending.CodeChallenge}, codeTTL); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	redirect, _ := url.Parse(pending.RedirectURI)
	values := redirect.Query()
	values.Set("code", code)
	values.Set("state", pending.State)
	redirect.RawQuery = values.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

type atlassianToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

func (s *Server) exchangeAtlassian(ctx context.Context, code, state string) (atlassianToken, []resource, string, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {s.cfg.AtlassianID}, "client_secret": {s.cfg.AtlassianKey}, "code": {code}, "redirect_uri": {strings.TrimRight(s.cfg.PublicURL, "/") + "/oauth/callback"}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.cfg.AtlassianAuthURL, "/")+"/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return atlassianToken{}, nil, "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return atlassianToken{}, nil, "", fmt.Errorf("atlassian token exchange failed: %s", b)
	}
	var token atlassianToken
	if err := json.NewDecoder(res.Body).Decode(&token); err != nil {
		return token, nil, "", err
	}
	resources, err := s.getResources(ctx, token.AccessToken)
	if err != nil {
		return token, nil, "", err
	}
	accountID, err := s.getAccountID(ctx, token.AccessToken)
	return token, resources, accountID, err
}

func (s *Server) getResources(ctx context.Context, accessToken string) ([]resource, error) {
	return getJSON[[]resource](ctx, strings.TrimRight(s.cfg.AtlassianAPIURL, "/")+"/oauth/token/accessible-resources", accessToken)
}
func (s *Server) getAccountID(ctx context.Context, accessToken string) (string, error) {
	var v struct {
		AccountID string `json:"account_id"`
	}
	v, err := getJSON[struct {
		AccountID string `json:"account_id"`
	}](ctx, strings.TrimRight(s.cfg.AtlassianAPIURL, "/")+"/me", accessToken)
	return v.AccountID, err
}
func getJSON[T any](ctx context.Context, endpoint, accessToken string) (T, error) {
	var value T
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return value, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode/100 != 2 {
		return value, fmt.Errorf("atlassian API returned HTTP %d", res.StatusCode)
	}
	err = json.NewDecoder(res.Body).Decode(&value)
	return value, err
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, oauthMaxBody)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid request body", 400)
		return
	}
	var auth authorizationCode
	if s.cfg.Store.Get(r.Context(), "oauth:code:"+r.FormValue("code"), &auth) != nil {
		http.Error(w, "invalid authorization code", 400)
		return
	}
	if auth.ClientID != r.FormValue("client_id") || auth.RedirectURI != r.FormValue("redirect_uri") || !verifyPKCE(auth.CodeChallenge, r.FormValue("code_verifier")) {
		http.Error(w, "invalid authorization code", 400)
		return
	}
	_ = s.cfg.Store.Delete(r.Context(), "oauth:code:"+r.FormValue("code"))
	accessToken, err := randomToken(32)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := s.cfg.Store.Put(r.Context(), "mcp:token:"+accessToken, accessSession{UserID: auth.UserID}, tokenTTL); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{"access_token": accessToken, "token_type": "Bearer", "expires_in": int(tokenTTL.Seconds()), "scope": "mcp"})
}

func (s *Server) validClient(ctx context.Context, id, redirectURI string) bool {
	if id == "" {
		return false
	}
	var value struct {
		RedirectURIs []string `json:"redirect_uris"`
	}
	if s.cfg.Store.Get(ctx, "mcp:client:"+id, &value) == nil {
		return contains(value.RedirectURIs, redirectURI)
	}
	return false
}

func randomToken(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func verifyPKCE(challenge, verifier string) bool {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:]) == challenge
}
func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

// ipLimiter is a per-client token bucket. A nil limiter or a non-positive
// rate disables limiting entirely (used by tests and benchmarks).
type ipLimiter struct {
	mu      sync.Mutex
	rate    float64
	burst   float64
	buckets map[string]*rateBucket
}

type rateBucket struct {
	tokens float64
	last   time.Time
}

func newIPLimiter(rate, burst float64) *ipLimiter {
	return &ipLimiter{rate: rate, burst: burst, buckets: make(map[string]*rateBucket)}
}

func (l *ipLimiter) allow(key string) bool {
	if l == nil || l.rate <= 0 {
		return true
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buckets) > 10000 {
		l.evictLocked(now)
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &rateBucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	} else {
		b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// evictLocked bounds memory under address-spoofing floods; if eviction cannot
// keep up, the whole table is dropped, which only costs a brief refill window.
func (l *ipLimiter) evictLocked(now time.Time) {
	for key, bucket := range l.buckets {
		if now.Sub(bucket.last) > time.Hour {
			delete(l.buckets, key)
		}
	}
	if len(l.buckets) > 20000 {
		l.buckets = make(map[string]*rateBucket)
	}
}

// clientKey picks the best available client identity: the leftmost
// X-Forwarded-For entry when behind a proxy, the remote host otherwise.
func clientKey(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		if i := strings.IndexByte(forwarded, ','); i >= 0 {
			return strings.TrimSpace(forwarded[:i])
		}
		return strings.TrimSpace(forwarded)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

var selectPage = `<!doctype html><html><body><h1>Connect Jira</h1><form method="post" action="/oauth/complete"><input type="hidden" name="state" value="{{.State}}"><label>Jira site <select name="cloud_id">{{range .Resources}}<option value="{{.ID}}">{{.Name}}</option>{{end}}</select></label><h2>Tools</h2>{{range .Tools}}<label><input type="checkbox" name="tool" value="{{.}}" checked>{{.}}</label><br>{{end}}<button type="submit">Continue</button></form></body></html>`

func EncryptionKeyFromEnv() ([]byte, error) {
	raw := os.Getenv("JIRA_MCP_ENCRYPTION_KEY")
	if raw == "" {
		return nil, errors.New("JIRA_MCP_ENCRYPTION_KEY is required")
	}
	key, err := base64.RawStdEncoding.DecodeString(raw)
	if err != nil || len(key) != 32 {
		return nil, errors.New("JIRA_MCP_ENCRYPTION_KEY must be base64 for 32 bytes")
	}
	return key, nil
}

func seal(key []byte, plaintext string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plaintext), nil)), nil
}
func open(key []byte, encoded string) (string, error) {
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(raw) < gcm.NonceSize() {
		return "", errors.New("invalid encrypted token")
	}
	plaintext, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	return string(plaintext), err
}
