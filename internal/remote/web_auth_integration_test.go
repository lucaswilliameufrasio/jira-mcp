//go:build integration

package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"jira-mcp/internal/mcp"
)

func TestBrowserOAuthWithValkey(t *testing.T) {
	rawURL := os.Getenv("VALKEY_URL")
	if rawURL == "" {
		t.Skip("VALKEY_URL required")
	}
	store, err := NewValkeyStore(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	identity := fmt.Sprintf("test-%d", time.Now().UnixNano())
	account, cloud := identity+"-account", identity+"-cloud"
	audits, err := NewValkeyAuditStore(store, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := audits.Append(ctx, auditIdentity(userRecord{AccountID: account, CloudID: cloud}), mcp.ToolEvent{Tool: "jira_search", StartedAt: time.Now(), Duration: 42 * time.Millisecond, Success: true}); err != nil {
		t.Fatal(err)
	}
	existing := userRecord{AccountID: account, CloudID: cloud, Tools: []string{"jira_search"}, AccessToken: "synthetic-old-ciphertext"}
	if err := store.Put(ctx, "mcp:user:"+account+":"+cloud, existing, time.Minute); err != nil {
		t.Fatal(err)
	}
	defer store.Delete(ctx, "mcp:user:"+account+":"+cloud)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token/accessible-resources":
			_ = json.NewEncoder(w).Encode([]resource{{ID: cloud, URL: "https://jira.example.test", Name: "Test"}})
		case "/me":
			_ = json.NewEncoder(w).Encode(map[string]string{"account_id": account})
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/authorize":
			redirect, _ := url.Parse(r.URL.Query().Get("redirect_uri"))
			q := redirect.Query()
			q.Set("state", r.URL.Query().Get("state"))
			q.Set("code", "synthetic-atlassian-code")
			redirect.RawQuery = q.Encode()
			http.Redirect(w, r, redirect.String(), http.StatusFound)
		case "/oauth/token":
			_ = json.NewEncoder(w).Encode(atlassianToken{AccessToken: "synthetic-access", ExpiresIn: 3600})
		default:
			http.NotFound(w, r)
		}
	}))
	defer auth.Close()
	app := httptest.NewUnstartedServer(nil)
	origin := "http://" + app.Listener.Addr().String()
	server, err := New(Config{PublicURL: origin, AtlassianID: "synthetic-client", AtlassianKey: "synthetic-key", AtlassianAuthURL: auth.URL, AtlassianAPIURL: api.URL, Store: store, Audit: audits, EncryptionKey: make([]byte, 32)})
	if err != nil {
		t.Fatal(err)
	}
	app.Config.Handler = server
	app.Start()
	defer app.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(target string) *http.Response {
		t.Helper()
		resp, err := client.Get(target)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := get(app.URL + "/web/login")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	authorizeURL := resp.Header.Get("Location")
	resp.Body.Close()
	resp = get(authorizeURL)
	providerURL := resp.Header.Get("Location")
	resp.Body.Close()
	resp = get(providerURL)
	atlassianCallback := resp.Header.Get("Location")
	resp.Body.Close()
	callbackURL, err := url.Parse(atlassianCallback)
	if err != nil {
		t.Fatal(err)
	}
	resp = get(atlassianCallback)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("selection status = %d", resp.StatusCode)
	}
	resp.Body.Close()
	form := url.Values{"state": {callbackURL.Query().Get("state")}, "cloud_id": {cloud}, "tool": {"jira_search"}}
	resp, err = client.PostForm(app.URL+"/oauth/complete", form)
	if err != nil {
		t.Fatal(err)
	}
	webCallback := resp.Header.Get("Location")
	resp.Body.Close()
	resp = get(webCallback)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("web callback status = %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = get(app.URL + "/web/session")
	var session map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || session["account_id"] != account || session["cloud_id"] != cloud || session["csrf_token"] == "" {
		t.Fatal("incorrect authenticated identity")
	}
	for _, check := range []struct {
		path, expected string
	}{{"/web/overview", "Visão geral"}, {"/web/monitoring", "42ms"}, {"/web/audit", "jira_search"}} {
		resp = get(app.URL + check.path)
		page, err := io.ReadAll(resp.Body)
		if err != nil {
			resp.Body.Close()
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(page), check.expected) {
			t.Fatalf("authenticated route %s did not display expected content", check.path)
		}
	}
	req, _ := http.NewRequest(http.MethodPost, app.URL+"/web/logout", strings.NewReader(url.Values{"csrf_token": {session["csrf_token"]}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", origin)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d", resp.StatusCode)
	}
	var retained userRecord
	if err := store.Get(ctx, "mcp:user:"+account+":"+cloud, &retained); err != nil || retained.AccessToken != existing.AccessToken || len(retained.Tools) != 1 || retained.Tools[0] != "jira_search" {
		t.Fatal("browser login changed the existing MCP user's credentials or tool grants")
	}
	resp = get(app.URL + "/web/session")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("session after logout status = %d", resp.StatusCode)
	}
}

func TestBrowserValkeyAtomicTakeSingleWinner(t *testing.T) {
	rawURL := os.Getenv("VALKEY_URL")
	if rawURL == "" {
		t.Skip("VALKEY_URL required")
	}
	store, err := NewValkeyStore(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	key := "browser-take-test:" + time.Now().Format("20060102150405.000000000")
	if err := store.Put(ctx, key, webFlow{ClientID: "synthetic"}, time.Minute); err != nil {
		t.Fatal(err)
	}
	defer store.Delete(ctx, key)
	var wg sync.WaitGroup
	results := make(chan bool, 20)
	for i := 0; i < cap(results); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var flow webFlow
			err := store.Take(ctx, key, &flow)
			results <- err == nil && flow.ClientID == "synthetic"
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for value := range results {
		if value {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("atomic consume winners = %d", success)
	}
}
