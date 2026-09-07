package remote

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(clone)
}

type memoryStore struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (s *memoryStore) Put(_ context.Context, key string, value any, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := json.Marshal(value)
	if err == nil {
		s.values[key] = raw
	}
	return err
}
func (s *memoryStore) Get(_ context.Context, key string, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, ok := s.values[key]
	if !ok {
		return ErrNotFound
	}
	return json.Unmarshal(raw, value)
}
func (s *memoryStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, key)
	return nil
}
func (s *memoryStore) Close() {}

func TestHealthEndpointIsPublic(t *testing.T) {
	store := &memoryStore{values: make(map[string][]byte)}
	server, err := New(Config{PublicURL: "https://mcp.example.test", AtlassianID: "atlassian-client", AtlassianKey: "secret", Store: store, EncryptionKey: make([]byte, 32)})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", res.StatusCode)
	}
	var payload map[string]string
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "ok" {
		t.Fatalf("health payload = %#v", payload)
	}
}

func TestOAuthMetadataAndRegistration(t *testing.T) {
	store := &memoryStore{values: make(map[string][]byte)}
	server, err := New(Config{PublicURL: "https://mcp.example.test", AtlassianID: "atlassian-client", AtlassianKey: "secret", Store: store, EncryptionKey: make([]byte, 32)})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("metadata status = %d", res.StatusCode)
	}

	body := `{"redirect_uris":["https://client.example/callback"]}`
	res, err = http.Post(ts.URL+"/oauth/register", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("registration status = %d", res.StatusCode)
	}
	var registration map[string]any
	if err := json.NewDecoder(res.Body).Decode(&registration); err != nil {
		t.Fatal(err)
	}
	if registration["client_id"] == nil {
		t.Fatal("registration did not return client_id")
	}
}

func TestOAuthTokenRequiresPKCE(t *testing.T) {
	store := &memoryStore{values: make(map[string][]byte)}
	server, err := New(Config{PublicURL: "https://mcp.example.test", AtlassianID: "atlassian-client", AtlassianKey: "secret", Store: store, EncryptionKey: make([]byte, 32)})
	if err != nil {
		t.Fatal(err)
	}
	challenge := "challenge"
	_ = store.Put(context.Background(), "oauth:code:test", authorizationCode{UserID: "user", ClientID: "client", RedirectURI: "https://client/cb", CodeChallenge: challenge}, time.Minute)
	form := url.Values{"code": {"test"}, "client_id": {"client"}, "redirect_uri": {"https://client/cb"}, "code_verifier": {"wrong"}}
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("token status = %d", rec.Code)
	}
}

func TestMCPCallsJiraThroughConfiguredAPIURL(t *testing.T) {
	var hits int64
	jiraHits := &hits
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/rest/api/3/search/jql") {
			atomic.AddInt64(jiraHits, 1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"issues": []map[string]any{{"id": "10001", "key": "TEST-1"}}})
			return
		}
		http.Error(w, "unexpected", http.StatusNotFound)
	}))
	defer fake.Close()

	key := make([]byte, 32)
	store := &memoryStore{values: make(map[string][]byte)}
	accessToken, err := seal(key, "atlassian-access-token")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "mcp:user:user-1", userRecord{AccountID: "account-1", CloudID: "cloud-1", Tools: []string{"jira_search"}, AccessToken: accessToken, ExpiresAt: time.Now().Add(time.Hour)}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "mcp:token:mcp-token", accessSession{UserID: "user-1"}, time.Hour); err != nil {
		t.Fatal(err)
	}
	server, err := New(Config{PublicURL: "https://mcp.example", AtlassianID: "client", AtlassianKey: "secret", AtlassianAPIURL: fake.URL, Store: store, EncryptionKey: key})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server)
	defer ts.Close()

	client := sdk.NewClient(&sdk.Implementation{Name: "unit-client", Version: "1.0.0"}, nil)
	transport := &sdk.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{token: "mcp-token", base: http.DefaultTransport}}, DisableStandaloneSSE: true}
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	result, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: "jira_search", Arguments: map[string]any{"jql": "project = TEST"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("tool result is error: %v", result.Content)
	}
	if atomic.LoadInt64(jiraHits) == 0 {
		t.Fatal("jira_search did not reach the configured Atlassian API URL")
	}
}

func newHardeningTestServer(t *testing.T, store *memoryStore) *httptest.Server {
	t.Helper()
	server, err := New(Config{PublicURL: "https://mcp.example.test", AtlassianID: "atlassian-client", AtlassianKey: "secret", Store: store, EncryptionKey: make([]byte, 32)})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	return ts
}

func TestOAuthPendingIsSingleUse(t *testing.T) {
	store := &memoryStore{values: make(map[string][]byte)}
	ts := newHardeningTestServer(t, store)
	pending := pendingAuthorization{
		authorizationRequest:  authorizationRequest{ClientID: "client", RedirectURI: "https://client.example/cb", State: "client-state", CodeChallenge: "challenge", ChallengeMethod: "S256"},
		AtlassianAccessToken:  "atlassian-access-token",
		AtlassianRefreshToken: "",
		ExpiresAt:             time.Now().Add(time.Hour),
		AccountID:             "account-1",
		Resources:             []resource{{ID: "cloud-1", URL: "https://site.example", Name: "Site"}},
	}
	if err := store.Put(context.Background(), "oauth:pending:test-state", pending, time.Minute); err != nil {
		t.Fatal(err)
	}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	form := strings.NewReader("state=test-state&cloud_id=cloud-1&tool=jira_search")
	res, err := noRedirect.Post(ts.URL+"/oauth/complete", "application/x-www-form-urlencoded", form)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("first complete status = %d", res.StatusCode)
	}
	form = strings.NewReader("state=test-state&cloud_id=cloud-1&tool=jira_search")
	res, err = noRedirect.Post(ts.URL+"/oauth/complete", "application/x-www-form-urlencoded", form)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("replayed complete status = %d, want 400", res.StatusCode)
	}
}

func TestOAuthStateIsConsumedByCallback(t *testing.T) {
	store := &memoryStore{values: make(map[string][]byte)}
	ts := newHardeningTestServer(t, store)
	req := authorizationRequest{ClientID: "client", RedirectURI: "https://client.example/cb", CodeChallenge: "challenge", ChallengeMethod: "S256"}
	if err := store.Put(context.Background(), "oauth:state:abc", req, time.Minute); err != nil {
		t.Fatal(err)
	}
	res, err := http.Get(ts.URL + "/oauth/callback?state=abc&error=access_denied")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("callback with error status = %d, want 400", res.StatusCode)
	}
	res, err = http.Get(ts.URL + "/oauth/callback?state=abc")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "invalid OAuth state") {
		t.Fatalf("replayed callback status = %d body = %q", res.StatusCode, body)
	}
}

func TestRevokeInvalidatesToken(t *testing.T) {
	store := &memoryStore{values: make(map[string][]byte)}
	ts := newHardeningTestServer(t, store)
	key := make([]byte, 32)
	accessToken, err := seal(key, "atlassian-access-token")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "mcp:user:user-1", userRecord{AccountID: "account-1", CloudID: "cloud-1", Tools: []string{"jira_search"}, AccessToken: accessToken, ExpiresAt: time.Now().Add(time.Hour)}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "mcp:token:mcp-token", accessSession{UserID: "user-1"}, time.Hour); err != nil {
		t.Fatal(err)
	}
	res, err := http.Post(ts.URL+"/oauth/revoke", "application/x-www-form-urlencoded", strings.NewReader("token=mcp-token"))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("revoke status = %d", res.StatusCode)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer mcp-token")
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("post-revoke /mcp status = %d, want 401", res.StatusCode)
	}
}

func TestRegisterRejectsOversizedAndInvalidBodies(t *testing.T) {
	store := &memoryStore{values: make(map[string][]byte)}
	ts := newHardeningTestServer(t, store)

	res, err := http.Post(ts.URL+"/oauth/register", "application/json", strings.NewReader(`{"redirect_uris":"`+strings.Repeat("a", oauthMaxBody+1024)+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized register status = %d, want 413", res.StatusCode)
	}

	res, err = http.Post(ts.URL+"/oauth/register", "application/json", strings.NewReader("not-json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid register status = %d, want 400", res.StatusCode)
	}
}

func TestRateLimitReturns429(t *testing.T) {
	store := &memoryStore{values: make(map[string][]byte)}
	ts := newHardeningTestServer(t, store)
	var sawLimited bool
	for i := 0; i < 20; i++ {
		res, err := http.Post(ts.URL+"/oauth/register", "application/json", strings.NewReader(`{"redirect_uris":["https://client.example/cb"]}`))
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode == http.StatusTooManyRequests {
			sawLimited = true
			break
		}
	}
	if !sawLimited {
		t.Fatal("register endpoint never returned 429 under flood")
	}
}

func TestMCPRateLimitCanBeDisabled(t *testing.T) {
	store := &memoryStore{values: make(map[string][]byte)}
	server, err := New(Config{PublicURL: "https://mcp.example.test", AtlassianID: "atlassian-client", AtlassianKey: "secret", Store: store, EncryptionKey: make([]byte, 32), RateLimitRPS: 0})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if !server.mcp.allow("1.2.3.4") {
			t.Fatal("mcp limiter with rate 0 must always allow")
		}
	}
}
