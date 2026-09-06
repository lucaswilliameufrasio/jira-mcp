package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

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
	defer res.Body.Close()
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
