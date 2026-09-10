// Package jira is a small REST client for Atlassian Jira, covering the
// operations exposed as MCP tools: searching, reading, creating, updating,
// transitioning and commenting on issues, plus listing projects.
//
// It supports both Jira Cloud (api/3, Basic auth with email + API token) and
// Jira Server/Data Center (api/2, Bearer auth with a Personal Access Token).
package jira

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Deployment selects which flavor of the Jira REST API to talk to.
type Deployment string

const (
	DeploymentCloud  Deployment = "cloud"  // api/3, email + API token, Basic auth
	DeploymentServer Deployment = "server" // api/2, Personal Access Token, Bearer auth
)

// Config holds everything needed to reach and authenticate against a Jira
// instance. Populate it from environment variables (see main.go).
type Config struct {
	// BaseURL is the site root, e.g. "https://yourcompany.atlassian.net"
	// or "https://jira.yourcompany.com". No trailing slash.
	BaseURL string

	// Deployment picks the API family. Defaults to DeploymentCloud.
	Deployment Deployment

	// Cloud auth: Basic, base64(Email:APIToken).
	Email    string
	APIToken string

	// Server/Data Center auth: Bearer PersonalAccessToken.
	PersonalAccessToken string

	// Cloud OAuth access token. When set, it takes precedence over Basic auth.
	BearerToken string
}

func (c Config) apiVersion() string {
	if c.Deployment == DeploymentServer {
		return "2"
	}
	return "3"
}

// Client is a thin, synchronous wrapper around net/http for the Jira REST API.
type Client struct {
	cfg  Config
	http *http.Client
}

func NewClient(cfg Config) *Client {
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.Deployment == "" {
		cfg.Deployment = DeploymentCloud
	}
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// APIError represents a non-2xx response from Jira, with the response body
// (which Jira usually populates with errorMessages/errors) preserved so the
// caller can relay something actionable back to the model.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("jira API error (HTTP %d): %s", e.StatusCode, e.Body)
}

// doJSON issues an HTTP request with an optional JSON body and decodes a
// JSON response into out (if out is non-nil and the response has a body).
func (c *Client) doJSON(method, path string, query url.Values, body interface{}, out interface{}) error {
	if c.cfg.BaseURL == "" {
		return fmt.Errorf("jira base URL is not configured (set JIRA_BASE_URL)")
	}

	full := c.cfg.BaseURL + path
	if len(query) > 0 {
		full += "?" + query.Encode()
	}

	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, full, reader)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if err := c.authenticate(req); err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("calling jira: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading jira response: %w", err)
	}

	if resp.StatusCode >= 300 {
		return &APIError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decoding jira response: %w", err)
		}
	}
	return nil
}

func (c *Client) authenticate(req *http.Request) error {
	switch c.cfg.Deployment {
	case DeploymentServer:
		if c.cfg.PersonalAccessToken == "" {
			return fmt.Errorf("JIRA_PERSONAL_ACCESS_TOKEN is required for server/data-center deployment")
		}
		req.Header.Set("Authorization", "Bearer "+c.cfg.PersonalAccessToken)
	default:
		if c.cfg.BearerToken != "" {
			req.Header.Set("Authorization", "Bearer "+c.cfg.BearerToken)
			return nil
		}
		if c.cfg.Email == "" || c.cfg.APIToken == "" {
			return fmt.Errorf("JIRA_EMAIL and JIRA_API_TOKEN are required for cloud deployment")
		}
		token := base64.StdEncoding.EncodeToString([]byte(c.cfg.Email + ":" + c.cfg.APIToken))
		req.Header.Set("Authorization", "Basic "+token)
	}
	return nil
}

func (c *Client) apiPath(suffix string) string {
	return "/rest/api/" + c.cfg.apiVersion() + suffix
}

// BaseURL returns the configured Jira site root (no trailing slash), useful
// for building human-facing links such as issue browse URLs.
func (c *Client) BaseURL() string {
	return c.cfg.BaseURL
}

// BrowseURL returns the web UI link for a given issue key.
func (c *Client) BrowseURL(issueKey string) string {
	if c.cfg.BaseURL == "" {
		return issueKey
	}
	return c.cfg.BaseURL + "/browse/" + issueKey
}

// --- Data types ---

type Issue struct {
	ID     string                 `json:"id"`
	Key    string                 `json:"key"`
	Self   string                 `json:"self,omitempty"`
	Fields map[string]interface{} `json:"fields,omitempty"`
}

type LinkedIssue struct {
	ID     string                 `json:"id"`
	Key    string                 `json:"key"`
	Fields map[string]interface{} `json:"fields,omitempty"`
}

type IssueLinkType struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Inward  string `json:"inward"`
	Outward string `json:"outward"`
}

type IssueLink struct {
	ID           string        `json:"id"`
	Type         IssueLinkType `json:"type"`
	InwardIssue  LinkedIssue   `json:"inwardIssue"`
	OutwardIssue LinkedIssue   `json:"outwardIssue"`
}

type issueLinkTypesResponse struct {
	IssueLinkTypes []IssueLinkType `json:"issueLinkTypes"`
}

type CreateIssueLinkInput struct {
	LinkType     string
	InwardIssue  string
	OutwardIssue string
	Comment      string
}

type SearchResult struct {
	// Populated on Jira Server/Data Center (api/2 classic search).
	StartAt    int `json:"startAt,omitempty"`
	MaxResults int `json:"maxResults,omitempty"`
	Total      int `json:"total,omitempty"`

	// Populated on Jira Cloud (api/3 enhanced JQL search).
	NextPageToken string `json:"nextPageToken,omitempty"`
	IsLast        bool   `json:"isLast,omitempty"`

	Issues []Issue `json:"issues"`
}

type Project struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

type Transition struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	To   struct {
		Name string `json:"name"`
	} `json:"to"`
}

type transitionsResponse struct {
	Transitions []Transition `json:"transitions"`
}

// --- Operations ---

// SearchIssues runs a JQL query. On Cloud it uses the enhanced JQL search
// endpoint (POST /search/jql) with token-based pagination, since the classic
// /search endpoint was removed by Atlassian in 2025. On Server/Data Center it
// uses the still-current classic GET /search endpoint with startAt paging.
func (c *Client) SearchIssues(jql string, maxResults int, fields []string, pageToken string) (*SearchResult, error) {
	if maxResults <= 0 {
		maxResults = 25
	}

	if c.cfg.Deployment == DeploymentServer {
		q := url.Values{}
		q.Set("jql", jql)
		q.Set("maxResults", strconv.Itoa(maxResults))
		if len(fields) > 0 {
			q.Set("fields", strings.Join(fields, ","))
		}
		var out SearchResult
		if err := c.doJSON(http.MethodGet, c.apiPath("/search"), q, nil, &out); err != nil {
			return nil, err
		}
		return &out, nil
	}

	body := map[string]interface{}{
		"jql":        jql,
		"maxResults": maxResults,
	}
	if len(fields) > 0 {
		body["fields"] = fields
	}
	if pageToken != "" {
		body["nextPageToken"] = pageToken
	}
	var out SearchResult
	if err := c.doJSON(http.MethodPost, c.apiPath("/search/jql"), nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetIssue fetches a single issue by key or ID (e.g. "PROJ-123").
func (c *Client) GetIssue(issueKey string, fields []string, expand []string) (*Issue, error) {
	q := url.Values{}
	if len(fields) > 0 {
		q.Set("fields", strings.Join(fields, ","))
	}
	if len(expand) > 0 {
		q.Set("expand", strings.Join(expand, ","))
	}
	var out Issue
	if err := c.doJSON(http.MethodGet, c.apiPath("/issue/"+url.PathEscape(issueKey)), q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListIssueLinks returns all links attached to an issue.
func (c *Client) ListIssueLinks(issueKey string) ([]IssueLink, error) {
	issue, err := c.GetIssue(issueKey, []string{"issuelinks"}, nil)
	if err != nil {
		return nil, err
	}
	raw, ok := issue.Fields["issuelinks"]
	if !ok || raw == nil {
		return nil, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("re-encoding issue links: %w", err)
	}
	var links []IssueLink
	if err := json.Unmarshal(b, &links); err != nil {
		return nil, fmt.Errorf("decoding issue links: %w", err)
	}
	return links, nil
}

// ListIssueLinkTypes returns the formal link types available in this Jira
// instance, including their inward and outward descriptions.
func (c *Client) ListIssueLinkTypes() ([]IssueLinkType, error) {
	var out issueLinkTypesResponse
	if err := c.doJSON(http.MethodGet, c.apiPath("/issueLinkType"), nil, nil, &out); err != nil {
		return nil, err
	}
	return out.IssueLinkTypes, nil
}

// CreateIssueLink creates a directional link between two issues.
func (c *Client) CreateIssueLink(in CreateIssueLinkInput) error {
	body := map[string]interface{}{
		"type":         map[string]string{"name": in.LinkType},
		"inwardIssue":  map[string]string{"key": in.InwardIssue},
		"outwardIssue": map[string]string{"key": in.OutwardIssue},
	}
	if strings.TrimSpace(in.Comment) != "" {
		body["comment"] = map[string]interface{}{"body": c.encodeDescription(in.Comment)}
	}
	return c.doJSON(http.MethodPost, c.apiPath("/issueLink"), nil, body, nil)
}

// GetIssueLink returns one link by its Jira link ID.
func (c *Client) GetIssueLink(linkID string) (*IssueLink, error) {
	var out IssueLink
	if err := c.doJSON(http.MethodGet, c.apiPath("/issueLink/"+url.PathEscape(linkID)), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteIssueLink deletes a link by its Jira link ID.
func (c *Client) DeleteIssueLink(linkID string) error {
	return c.doJSON(http.MethodDelete, c.apiPath("/issueLink/"+url.PathEscape(linkID)), nil, nil, nil)
}

// UpdateIssueLink replaces a link because Jira has no update endpoint for its
// type or direction. The replacement receives a new link ID.
func (c *Client) UpdateIssueLink(linkID string, in CreateIssueLinkInput) error {
	current, err := c.GetIssueLink(linkID)
	if err != nil {
		return err
	}
	if in.LinkType == "" {
		in.LinkType = current.Type.Name
	}
	if in.InwardIssue == "" {
		in.InwardIssue = current.InwardIssue.Key
	}
	if in.OutwardIssue == "" {
		in.OutwardIssue = current.OutwardIssue.Key
	}
	if err := c.DeleteIssueLink(linkID); err != nil {
		return err
	}
	return c.CreateIssueLink(in)
}

// CreateIssueInput describes the fields needed to create a new issue.
// Description is treated as a plain-text string and converted to Atlassian
// Document Format automatically when talking to Cloud (api/3).
type CreateIssueInput struct {
	ProjectKey  string
	IssueType   string
	Summary     string
	Description string
	// ExtraFields lets callers set any additional Jira field by its API
	// name (e.g. "priority", "labels", "assignee", "customfield_10010").
	ExtraFields map[string]interface{}
}

func (c *Client) CreateIssue(in CreateIssueInput) (*Issue, error) {
	fields := map[string]interface{}{
		"project":   map[string]string{"key": in.ProjectKey},
		"summary":   in.Summary,
		"issuetype": map[string]string{"name": in.IssueType},
	}
	if in.Description != "" {
		fields["description"] = c.encodeDescription(in.Description)
	}
	for k, v := range in.ExtraFields {
		fields[k] = v
	}

	body := map[string]interface{}{"fields": fields}
	var out Issue
	if err := c.doJSON(http.MethodPost, c.apiPath("/issue"), nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateIssue applies a partial field update to an existing issue.
func (c *Client) UpdateIssue(issueKey string, fields map[string]interface{}) error {
	body := map[string]interface{}{"fields": fields}
	return c.doJSON(http.MethodPut, c.apiPath("/issue/"+url.PathEscape(issueKey)), nil, body, nil)
}

// AddComment posts a plain-text comment to an issue.
func (c *Client) AddComment(issueKey, comment string) error {
	body := map[string]interface{}{"body": c.encodeDescription(comment)}
	return c.doJSON(http.MethodPost, c.apiPath("/issue/"+url.PathEscape(issueKey)+"/comment"), nil, body, nil)
}

// ListTransitions returns the workflow transitions currently available for
// an issue (the "to" status names and the IDs needed to perform them).
func (c *Client) ListTransitions(issueKey string) ([]Transition, error) {
	var out transitionsResponse
	if err := c.doJSON(http.MethodGet, c.apiPath("/issue/"+url.PathEscape(issueKey)+"/transitions"), nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Transitions, nil
}

// TransitionIssue moves an issue through its workflow using a transition ID
// obtained from ListTransitions, optionally leaving a comment at the same time.
func (c *Client) TransitionIssue(issueKey, transitionID, comment string) error {
	body := map[string]interface{}{
		"transition": map[string]string{"id": transitionID},
	}
	if comment != "" {
		body["update"] = map[string]interface{}{
			"comment": []map[string]interface{}{
				{"add": map[string]interface{}{"body": c.encodeDescription(comment)}},
			},
		}
	}
	return c.doJSON(http.MethodPost, c.apiPath("/issue/"+url.PathEscape(issueKey)+"/transitions"), nil, body, nil)
}

// ListProjects returns all projects visible to the authenticated user.
func (c *Client) ListProjects() ([]Project, error) {
	var out []Project
	if err := c.doJSON(http.MethodGet, c.apiPath("/project"), nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// AssignIssue assigns (or unassigns, with accountIDOrNil == "unassign") an
// issue. For Cloud, pass an accountId; for Server/DC, pass a username.
func (c *Client) AssignIssue(issueKey, assignee string) error {
	var body map[string]interface{}
	if assignee == "" || assignee == "unassign" {
		body = map[string]interface{}{"accountId": nil}
	} else if c.cfg.Deployment == DeploymentServer {
		body = map[string]interface{}{"name": assignee}
	} else {
		body = map[string]interface{}{"accountId": assignee}
	}
	return c.doJSON(http.MethodPut, c.apiPath("/issue/"+url.PathEscape(issueKey)+"/assignee"), nil, body, nil)
}

// --- Agile (boards & sprints) ---
//
// Boards and sprints live under a separate REST API family
// (/rest/agile/1.0/...) that exists on both Cloud and Server/Data Center
// installs that have Jira Software enabled — it is not part of the core
// /rest/api/{2,3} family used above.

type Board struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"` // "scrum" or "kanban"
	Location struct {
		ProjectKey string `json:"projectKey"`
		Name       string `json:"name"`
	} `json:"location"`
}

type boardsResponse struct {
	Values     []Board `json:"values"`
	IsLast     bool    `json:"isLast"`
	StartAt    int     `json:"startAt"`
	MaxResults int     `json:"maxResults"`
}

type Sprint struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	State         string `json:"state"` // "active", "closed", "future"
	StartDate     string `json:"startDate,omitempty"`
	EndDate       string `json:"endDate,omitempty"`
	CompleteDate  string `json:"completeDate,omitempty"`
	OriginBoardID int    `json:"originBoardId,omitempty"`
	Goal          string `json:"goal,omitempty"`
}

type sprintsResponse struct {
	Values     []Sprint `json:"values"`
	IsLast     bool     `json:"isLast"`
	StartAt    int      `json:"startAt"`
	MaxResults int      `json:"maxResults"`
}

func (c *Client) agilePath(suffix string) string {
	return "/rest/agile/1.0" + suffix
}

// ListBoards returns boards visible to the user, optionally filtered by
// project key. Jira paginates this endpoint at up to 50 per page; this
// method returns the first page (increase maxResults to raise that cap).
func (c *Client) ListBoards(projectKey string, maxResults int) ([]Board, error) {
	if maxResults <= 0 {
		maxResults = 50
	}
	q := url.Values{}
	q.Set("maxResults", strconv.Itoa(maxResults))
	if projectKey != "" {
		q.Set("projectKeyOrId", projectKey)
	}
	var out boardsResponse
	if err := c.doJSON(http.MethodGet, c.agilePath("/board"), q, nil, &out); err != nil {
		return nil, err
	}
	return out.Values, nil
}

// GetBoard fetches a single board's details by ID.
func (c *Client) GetBoard(boardID int) (*Board, error) {
	var out Board
	if err := c.doJSON(http.MethodGet, c.agilePath("/board/"+strconv.Itoa(boardID)), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListSprints returns the sprints for a Scrum board, optionally filtered by
// state ("active", "closed", "future" — comma-separated to combine).
func (c *Client) ListSprints(boardID int, state string, maxResults int) ([]Sprint, error) {
	if maxResults <= 0 {
		maxResults = 50
	}
	q := url.Values{}
	q.Set("maxResults", strconv.Itoa(maxResults))
	if state != "" {
		q.Set("state", state)
	}
	var out sprintsResponse
	if err := c.doJSON(http.MethodGet, c.agilePath("/board/"+strconv.Itoa(boardID)+"/sprint"), q, nil, &out); err != nil {
		return nil, err
	}
	return out.Values, nil
}

// BoardIssues returns the issues on a board (backlog + active sprint items
// for Scrum boards, or the column contents for Kanban boards), optionally
// narrowed with an extra JQL filter.
func (c *Client) BoardIssues(boardID int, jql string, maxResults int, fields []string) (*SearchResult, error) {
	if maxResults <= 0 {
		maxResults = 50
	}
	q := url.Values{}
	q.Set("maxResults", strconv.Itoa(maxResults))
	if jql != "" {
		q.Set("jql", jql)
	}
	if len(fields) > 0 {
		q.Set("fields", strings.Join(fields, ","))
	}
	var out SearchResult
	if err := c.doJSON(http.MethodGet, c.agilePath("/board/"+strconv.Itoa(boardID)+"/issue"), q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SprintIssues returns the issues in a given sprint.
func (c *Client) SprintIssues(sprintID int, jql string, maxResults int, fields []string) (*SearchResult, error) {
	if maxResults <= 0 {
		maxResults = 50
	}
	q := url.Values{}
	q.Set("maxResults", strconv.Itoa(maxResults))
	if jql != "" {
		q.Set("jql", jql)
	}
	if len(fields) > 0 {
		q.Set("fields", strings.Join(fields, ","))
	}
	var out SearchResult
	if err := c.doJSON(http.MethodGet, c.agilePath("/sprint/"+strconv.Itoa(sprintID)+"/issue"), q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// --- Attachments ---

// Attachment mirrors the metadata Jira embeds in issue.fields.attachment.
type Attachment struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	MimeType string `json:"mimeType"`
	Content  string `json:"content"` // download URL
	Created  string `json:"created"`
	Author   struct {
		DisplayName string `json:"displayName"`
	} `json:"author"`
}

// ListAttachments returns attachment metadata for an issue. Jira exposes
// attachments as part of the issue's fields rather than a dedicated list
// endpoint, so this fetches the issue with just that field.
func (c *Client) ListAttachments(issueKey string) ([]Attachment, error) {
	issue, err := c.GetIssue(issueKey, []string{"attachment"}, nil)
	if err != nil {
		return nil, err
	}
	raw, ok := issue.Fields["attachment"]
	if !ok || raw == nil {
		return nil, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("re-encoding attachment metadata: %w", err)
	}
	var out []Attachment
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("decoding attachment metadata: %w", err)
	}
	return out, nil
}

// DownloadAttachmentByID fetches the raw bytes and content-type of an
// attachment by its ID, authenticating the same way as every other call.
func (c *Client) DownloadAttachmentByID(attachmentID string) (data []byte, contentType string, err error) {
	// The attachment "content" URL returned in metadata already points at
	// the right place; this reconstructs it from the ID so callers only
	// need to have listed attachments once.
	path := c.apiPath("/attachment/content/" + url.PathEscape(attachmentID))
	return c.downloadRaw(path)
}

// DownloadAttachmentURL fetches raw bytes from an absolute attachment
// content URL, e.g. the "content" field returned by ListAttachments.
func (c *Client) DownloadAttachmentURL(rawURL string) (data []byte, contentType string, err error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("building download request: %w", err)
	}
	if err := c.authenticate(req); err != nil {
		return nil, "", err
	}
	return c.doDownload(req)
}

func (c *Client) downloadRaw(path string) ([]byte, string, error) {
	req, err := http.NewRequest(http.MethodGet, c.cfg.BaseURL+path, nil)
	if err != nil {
		return nil, "", fmt.Errorf("building download request: %w", err)
	}
	if err := c.authenticate(req); err != nil {
		return nil, "", err
	}
	return c.doDownload(req)
}

func (c *Client) doDownload(req *http.Request) ([]byte, string, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("calling jira: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("reading attachment: %w", err)
	}
	if resp.StatusCode >= 300 {
		return nil, "", &APIError{StatusCode: resp.StatusCode, Body: string(data)}
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// encodeDescription returns plain text as-is for Server/DC (api/2, which
// still takes wiki-markup strings) or as a minimal Atlassian Document Format
// document for Cloud (api/3, which requires ADF for rich-text fields).
func (c *Client) encodeDescription(text string) interface{} {
	if c.cfg.Deployment == DeploymentServer {
		return text
	}
	paragraphs := strings.Split(text, "\n\n")
	content := make([]map[string]interface{}, 0, len(paragraphs))
	for _, p := range paragraphs {
		if p == "" {
			continue
		}
		content = append(content, map[string]interface{}{
			"type": "paragraph",
			"content": []map[string]interface{}{
				{"type": "text", "text": p},
			},
		})
	}
	if len(content) == 0 {
		content = append(content, map[string]interface{}{
			"type":    "paragraph",
			"content": []map[string]interface{}{},
		})
	}
	return map[string]interface{}{
		"type":    "doc",
		"version": 1,
		"content": content,
	}
}
