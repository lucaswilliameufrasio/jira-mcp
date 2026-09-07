package remote

import (
	"context"
	"encoding/json"
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
