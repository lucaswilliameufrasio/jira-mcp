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

func TestGetIssueRequestsAllFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/issue/TEST-1" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("fields"); got != "*all" {
			t.Fatalf("fields = %q", got)
		}
		_ = json.NewEncoder(w).Encode(Issue{Key: "TEST-1", Fields: map[string]interface{}{"customfield_10001": "value"}})
	}))
	defer server.Close()

	issue, err := NewClient(Config{BaseURL: server.URL, Deployment: DeploymentCloud, BearerToken: "token"}).GetIssue("TEST-1", []string{"*all"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if issue.Fields["customfield_10001"] != "value" {
		t.Fatalf("fields = %#v", issue.Fields)
	}
}

func TestUpdateIssueSendsCustomFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/rest/api/3/issue/TEST-1" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["fields"]["customfield_10001"] != "value" {
			t.Fatalf("body = %#v", body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	err := NewClient(Config{BaseURL: server.URL, Deployment: DeploymentCloud, BearerToken: "token"}).UpdateIssue("TEST-1", map[string]interface{}{"customfield_10001": "value"})
	if err != nil {
		t.Fatal(err)
	}
}
