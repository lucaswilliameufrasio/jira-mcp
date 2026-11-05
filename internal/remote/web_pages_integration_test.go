//go:build integration

package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"jira-mcp/internal/mcp"
)

type unavailableAudit struct{}

func (unavailableAudit) Append(context.Context, string, mcp.ToolEvent) error {
	return errors.New("unavailable")
}

func (unavailableAudit) List(context.Context, string, int) ([]mcp.ToolEvent, error) {
	return nil, errors.New("unavailable")
}

func TestConsolePagesUseOnlyAuthenticatedUsersAudit(t *testing.T) {
	rawURL := os.Getenv("VALKEY_URL")
	if rawURL == "" {
		t.Skip("VALKEY_URL required")
	}
	store, err := NewValkeyStore(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	audits, err := NewValkeyAuditStore(store, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(Config{PublicURL: "https://mcp.example.test", AtlassianID: "client", AtlassianKey: "fixture", Store: store, Audit: audits, EncryptionKey: make([]byte, 32)})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	ctx := context.Background()
	unique := fmt.Sprintf("console-%d", time.Now().UnixNano())
	userA := userRecord{AccountID: unique + "-account-a", CloudID: unique + "-site"}
	userB := userRecord{AccountID: unique + "-account-b", CloudID: unique + "-site"}
	for _, fixture := range []struct {
		id    string
		user  userRecord
		token string
		tool  string
	}{{unique + "-a", userA, unique + "-session-a", "jira_search"}, {unique + "-b", userB, unique + "-session-b", "jira_create_issue"}} {
		if err := store.Put(ctx, "mcp:user:"+fixture.id, fixture.user, time.Minute); err != nil {
			t.Fatal(err)
		}
		defer store.Delete(ctx, "mcp:user:"+fixture.id)
		if err := store.Put(ctx, webKey("web:session:", fixture.token), webSession{UserID: fixture.id, CSRF: unique + "-csrf", ExpiresAt: time.Now().Add(time.Minute)}, time.Minute); err != nil {
			t.Fatal(err)
		}
		defer store.Delete(ctx, webKey("web:session:", fixture.token))
		if err := audits.Append(ctx, auditIdentity(fixture.user), mcp.ToolEvent{Tool: fixture.tool, StartedAt: time.Now(), Duration: 42 * time.Millisecond, Success: true}); err != nil {
			t.Fatal(err)
		}
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, path := range []string{"/web/overview", "/web/monitoring", "/web/audit"} {
		req, _ := http.NewRequest(http.MethodGet, httpServer.URL+path, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/web/login" {
			t.Fatalf("unauthenticated %s status = %d", path, resp.StatusCode)
		}
		req, _ = http.NewRequest(http.MethodGet, httpServer.URL+path, nil)
		req.AddCookie(&http.Cookie{Name: webSessionCookie, Value: unique + "-session-a"})
		resp, err = client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "no-store" || !strings.Contains(string(body), userA.AccountID) || strings.Contains(string(body), userB.AccountID) {
			t.Fatalf("%s did not return isolated authenticated page", path)
		}
		if path == "/web/audit" && (!strings.Contains(string(body), "jira_search") || strings.Contains(string(body), "jira_create_issue")) {
			t.Fatal("audit contains another account's event")
		}
		if path == "/web/monitoring" && !strings.Contains(string(body), "42ms") {
			t.Fatal("monitoring did not use actual event duration")
		}
	}
	secondReq, _ := http.NewRequest(http.MethodGet, httpServer.URL+"/web/audit", nil)
	secondReq.AddCookie(&http.Cookie{Name: webSessionCookie, Value: unique + "-session-b"})
	secondResp, err := client.Do(secondReq)
	if err != nil {
		t.Fatal(err)
	}
	secondBody, err := io.ReadAll(secondResp.Body)
	secondResp.Body.Close()
	if err != nil || secondResp.StatusCode != http.StatusOK || !strings.Contains(string(secondBody), "jira_create_issue") || strings.Contains(string(secondBody), "jira_search") {
		t.Fatal("second user's audit was not isolated")
	}
	degraded, err := New(Config{PublicURL: "https://mcp.example.test", AtlassianID: "client", AtlassianKey: "fixture", Store: store, Audit: unavailableAudit{}, EncryptionKey: make([]byte, 32)})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/web/overview", nil)
	req.AddCookie(&http.Cookie{Name: webSessionCookie, Value: unique + "-session-a"})
	recorder := httptest.NewRecorder()
	degraded.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Dados de auditoria indisponíveis") || strings.Contains(recorder.Body.String(), "Chamadas na amostra") {
		t.Fatal("audit outage was not presented accurately")
	}
	req, _ = http.NewRequest(http.MethodPost, httpServer.URL+"/web/logout", strings.NewReader(url.Values{"csrf_token": {unique + "-csrf"}, "return": {"web"}}.Encode()))
	req.Header.Set("Origin", "https://mcp.example.test")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: webSessionCookie, Value: unique + "-session-a"})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/web/signed-out" {
		t.Fatal("console logout did not complete")
	}
}
