//go:build integration

package remote

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestOAuthFlowIssuesMCPTokenAndSession(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token/accessible-resources" {
			_ = json.NewEncoder(w).Encode([]resource{{ID: "cloud-1", URL: "https://jira.example", Name: "Test Jira"}})
			return
		}
		if r.URL.Path == "/me" {
			_ = json.NewEncoder(w).Encode(map[string]string{"account_id": "account-1"})
			return
		}
		http.NotFound(w, r)
	}))
	defer api.Close()

	var callbackBase string
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/authorize" {
			callbackBase = r.URL.Query().Get("redirect_uri")
			redirect, _ := url.Parse(callbackBase)
			values := redirect.Query()
			values.Set("code", "atlassian-code")
			values.Set("state", r.URL.Query().Get("state"))
			redirect.RawQuery = values.Encode()
			http.Redirect(w, r, redirect.String(), http.StatusFound)
			return
		}
		if r.URL.Path == "/oauth/token" {
			_ = json.NewEncoder(w).Encode(atlassianToken{AccessToken: "atlassian-access", RefreshToken: "atlassian-refresh", ExpiresIn: 3600})
			return
		}
		http.NotFound(w, r)
	}))
	defer auth.Close()

	store := &memoryStore{values: make(map[string][]byte)}
	app := httptest.NewUnstartedServer(nil)
	publicURL := "http://" + app.Listener.Addr().String()
	server, err := New(Config{PublicURL: publicURL, AtlassianID: "atl-client", AtlassianKey: "atl-secret", AtlassianAuthURL: auth.URL, AtlassianAPIURL: api.URL, Store: store, EncryptionKey: make([]byte, 32)})
	if err != nil {
		t.Fatal(err)
	}
	app.Config.Handler = server
	app.Start()
	defer app.Close()

	clientHTTP := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	registrationBody := strings.NewReader(`{"redirect_uris":["https://client.example/callback"]}`)
	response, err := clientHTTP.Post(app.URL+"/oauth/register", "application/json", registrationBody)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var registration struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&registration); err != nil {
		t.Fatal(err)
	}
	verifier := "integration-verifier"
	challenge := base64URLSHA256(verifier)
	query := url.Values{"response_type": {"code"}, "client_id": {registration.ClientID}, "redirect_uri": {"https://client.example/callback"}, "state": {"client-state"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	response, err = clientHTTP.Get(app.URL + "/oauth/authorize?" + query.Encode())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	providerRedirect := response.Header.Get("Location")
	if providerRedirect == "" {
		t.Fatal("authorization did not redirect to Atlassian")
	}
	response, err = clientHTTP.Get(providerRedirect)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	callback := response.Header.Get("Location")
	if callback == "" {
		t.Fatal("fake Atlassian did not redirect to callback")
	}
	callbackURL, _ := url.Parse(callback)
	response, err = clientHTTP.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(string(body), "cloud-1") {
		t.Fatalf("site selection page = %s", body)
	}
	form := url.Values{"state": {callbackURL.Query().Get("state")}, "cloud_id": {"cloud-1"}, "tool": {"jira_search"}}
	response, err = clientHTTP.PostForm(app.URL+"/oauth/complete", form)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	codeRedirect, _ := url.Parse(response.Header.Get("Location"))
	codeForm := url.Values{"grant_type": {"authorization_code"}, "code": {codeRedirect.Query().Get("code")}, "client_id": {registration.ClientID}, "redirect_uri": {"https://client.example/callback"}, "code_verifier": {verifier}}
	response, err = clientHTTP.PostForm(app.URL+"/oauth/token", codeForm)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&token); err != nil {
		t.Fatal(err)
	}
	if token.AccessToken == "" {
		t.Fatal("MCP access token is empty")
	}

	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "oauth-integration", Version: "1.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: app.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{token: token.AccessToken, base: http.DefaultTransport}}, DisableStandaloneSSE: true}
	session, err := mcpClient.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 1 || listed.Tools[0].Name != "jira_search" {
		t.Fatalf("tools = %#v", listed.Tools)
	}
}

func base64URLSHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
