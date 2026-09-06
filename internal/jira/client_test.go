package jira

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchIssuesCloudUsesEnhancedEndpointAndBasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/search/jql" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte("user@example.com:secret"))
		if r.Header.Get("Authorization") != want {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["jql"] != "project = TEST" || body["nextPageToken"] != "next" {
			t.Fatalf("body = %#v", body)
		}
		_ = json.NewEncoder(w).Encode(SearchResult{Issues: []Issue{{Key: "TEST-1"}}, IsLast: true})
	}))
	defer server.Close()

	result, err := NewClient(Config{BaseURL: server.URL, Deployment: DeploymentCloud, Email: "user@example.com", APIToken: "secret"}).SearchIssues("project = TEST", 10, nil, "next")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Issues) != 1 || result.Issues[0].Key != "TEST-1" {
		t.Fatalf("result = %+v", result)
	}
}

func TestOAuthBearerTakesPrecedenceOverCloudBasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer oauth-token" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(SearchResult{})
	}))
	defer server.Close()
	_, err := NewClient(Config{BaseURL: server.URL, Deployment: DeploymentCloud, Email: "user@example.com", APIToken: "secret", BearerToken: "oauth-token"}).SearchIssues("project = TEST", 1, nil, "")
	if err != nil {
		t.Fatal(err)
	}
}

func TestAPIErrorPreservesStatusAndBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
	}))
	defer server.Close()
	_, err := NewClient(Config{BaseURL: server.URL, Deployment: DeploymentCloud, BearerToken: "token"}).SearchIssues("bad", 1, nil, "")
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.StatusCode != http.StatusBadRequest || apiErr.Body == "" {
		t.Fatalf("error = %#v", err)
	}
}
