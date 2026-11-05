package remote

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func browserTestServer(t *testing.T) (*Server, *memoryStore) {
	t.Helper()
	store := &memoryStore{values: make(map[string][]byte)}
	server, err := New(Config{PublicURL: "https://mcp.example.test", AtlassianID: "client", AtlassianKey: "fixture", Store: store, EncryptionKey: make([]byte, 32)})
	if err != nil {
		t.Fatal(err)
	}
	return server, store
}

func beginBrowserLogin(t *testing.T, server *Server, store *memoryStore, userID string) (*http.Cookie, string) {
	t.Helper()
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/web/login", nil))
	if response.Code != http.StatusFound || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("login status = %d", response.Code)
	}
	redirect, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := redirect.Query().Get("state")
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != webFlowCookie || cookies[0].Value != state || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatal("incorrect login cookie")
	}
	var flow webFlow
	if err := store.Get(context.Background(), webKey("web:flow:", state), &flow); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(flow.Verifier))
	code := "synthetic-code-" + userID
	auth := authorizationCode{UserID: userID, ClientID: flow.ClientID, RedirectURI: "https://mcp.example.test/web/callback", CodeChallenge: base64.RawURLEncoding.EncodeToString(sum[:])}
	if err := store.Put(context.Background(), "oauth:code:"+code, auth, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "mcp:user:"+userID, userRecord{AccountID: "account-" + userID, CloudID: "cloud-" + userID}, time.Hour); err != nil {
		t.Fatal(err)
	}
	return cookies[0], "/web/callback?" + url.Values{"state": {state}, "code": {code}}.Encode()
}

func completeBrowserLogin(t *testing.T, server *Server, cookie *http.Cookie, target string) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, req)
	if response.Code != http.StatusFound {
		t.Fatalf("callback status = %d", response.Code)
	}
	for _, item := range response.Result().Cookies() {
		if item.Name == webSessionCookie {
			if !item.Secure || !item.HttpOnly || item.SameSite != http.SameSiteLaxMode {
				t.Fatal("incorrect session cookie flags")
			}
			return item
		}
	}
	t.Fatal("missing session cookie")
	return nil
}

func TestAuthenticatedConsoleAllowsSameOriginFormOrigin(t *testing.T) {
	server, store := browserTestServer(t)
	flow, callback := beginBrowserLogin(t, server, store, "policy")
	session := completeBrowserLogin(t, server, flow, callback)
	for _, path := range []string{"/web/overview", "/web/monitoring", "/web/audit"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(session)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, req)
		if got := response.Header().Get("Referrer-Policy"); got != "same-origin" {
			t.Errorf("%s Referrer-Policy = %q, want same-origin", path, got)
		}
	}
}

func TestBrowserSessionIsolationLogoutAndReplay(t *testing.T) {
	server, store := browserTestServer(t)
	flowA, callbackA := beginBrowserLogin(t, server, store, "a")
	flowB, callbackB := beginBrowserLogin(t, server, store, "b")
	wrong := httptest.NewRequest(http.MethodGet, callbackA, nil)
	wrong.AddCookie(flowB)
	denied := httptest.NewRecorder()
	server.ServeHTTP(denied, wrong)
	if denied.Code != http.StatusBadRequest {
		t.Fatalf("cross-browser callback = %d", denied.Code)
	}
	sessionA := completeBrowserLogin(t, server, flowA, callbackA)
	sessionB := completeBrowserLogin(t, server, flowB, callbackB)
	store.mu.Lock()
	_, rawTokenKeyExists := store.values["web:session:"+sessionA.Value]
	_, hashedTokenKeyExists := store.values[webKey("web:session:", sessionA.Value)]
	store.mu.Unlock()
	if rawTokenKeyExists || !hashedTokenKeyExists {
		t.Fatal("session token must be stored only by its hash")
	}
	replay := httptest.NewRequest(http.MethodGet, callbackA, nil)
	replay.AddCookie(flowA)
	replayed := httptest.NewRecorder()
	server.ServeHTTP(replayed, replay)
	if replayed.Code != http.StatusBadRequest {
		t.Fatalf("replayed callback = %d", replayed.Code)
	}
	var csrf string
	for _, test := range []struct {
		cookie  *http.Cookie
		account string
	}{{sessionA, "account-a"}, {sessionB, "account-b"}} {
		req := httptest.NewRequest(http.MethodGet, "/web/session", nil)
		req.AddCookie(test.cookie)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, req)
		var data map[string]string
		if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil || response.Code != http.StatusOK || data["account_id"] != test.account {
			t.Fatalf("session identity = %#v, status = %d, err = %v", data, response.Code, err)
		}
		if test.cookie == sessionA {
			csrf = data["csrf_token"]
		}
	}
	logout := func(origin, token string) int {
		req := httptest.NewRequest(http.MethodPost, "/web/logout", strings.NewReader(url.Values{"csrf_token": {token}}.Encode()))
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(sessionA)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, req)
		return response.Code
	}
	if logout("https://evil.example", csrf) != http.StatusForbidden || logout("https://mcp.example.test", "wrong") != http.StatusForbidden {
		t.Fatal("logout CSRF accepted")
	}
	if logout("https://mcp.example.test", csrf) != http.StatusNoContent {
		t.Fatal("logout failed")
	}
	req := httptest.NewRequest(http.MethodGet, "/web/session", nil)
	req.AddCookie(sessionA)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, req)
	if response.Code != http.StatusUnauthorized {
		t.Fatal("logged-out session remained valid")
	}
	req = httptest.NewRequest(http.MethodGet, "/web/session", nil)
	req.AddCookie(sessionB)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatal("logout affected another user")
	}
}

func TestBrowserCallbackRejectsWrongPKCE(t *testing.T) {
	server, store := browserTestServer(t)
	flow, callback := beginBrowserLogin(t, server, store, "bad-pkce")
	code := "synthetic-code-bad-pkce"
	var auth authorizationCode
	if err := store.Get(context.Background(), "oauth:code:"+code, &auth); err != nil {
		t.Fatal(err)
	}
	auth.CodeChallenge = "wrong"
	if err := store.Put(context.Background(), "oauth:code:"+code, auth, time.Minute); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, callback, nil)
	req.AddCookie(flow)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, req)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("wrong PKCE status = %d", response.Code)
	}
}

func TestBrowserCallbackSingleUseConcurrently(t *testing.T) {
	server, store := browserTestServer(t)
	flow, callback := beginBrowserLogin(t, server, store, "race")
	results := make(chan int, 12)
	var wg sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, callback, nil)
			req.AddCookie(flow)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, req)
			results <- response.Code
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for status := range results {
		if status == http.StatusFound {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful callbacks = %d", successes)
	}
}

func TestBrowserSessionExpiredAndInsecureOrigin(t *testing.T) {
	server, store := browserTestServer(t)
	if err := store.Put(context.Background(), webKey("web:session:", "expired"), webSession{UserID: "x", CSRF: "x", ExpiresAt: time.Now().Add(-time.Second)}, time.Hour); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/web/session", nil)
	req.AddCookie(&http.Cookie{Name: webSessionCookie, Value: "expired"})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, req)
	if response.Code != http.StatusUnauthorized {
		t.Fatal("expired session accepted")
	}
	server.cfg.PublicURL = "http://remote.example.test"
	response = httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/web/login", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatal("non-loopback HTTP login accepted")
	}
}
