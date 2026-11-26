package jira

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (c *Client) UploadAttachment(key, filename string, data []byte) (*Attachment, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(data); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	path := c.apiPath("/issue/" + url.PathEscape(key) + "/attachments")
	req, err := http.NewRequestWithContext(c.ctx, http.MethodPost, c.cfg.BaseURL+path, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Atlassian-Token", "no-check")
	if err := c.authenticate(req); err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return nil, &APIError{StatusCode: resp.StatusCode, RetryAfter: resp.Header.Get("Retry-After"), Body: "envio do anexo recusado"}
	}
	var attachments []Attachment
	if err := json.NewDecoder(resp.Body).Decode(&attachments); err != nil || len(attachments) != 1 || attachments[0].ID == "" {
		return nil, fmt.Errorf("envio aceito sem ID confirmado; consulte jira_list_attachments antes de repetir")
	}
	return &attachments[0], nil
}

// MoveIssue uses sprint ID zero for the backlog. Read the Agile projection
// after writing, so a failed confirmation never triggers a second mutation.
func (c *Client) MoveIssue(key string, sprintID int) (string, error) {
	path := "/backlog/issue"
	if sprintID > 0 {
		path = "/sprint/" + strconv.Itoa(sprintID) + "/issue"
	}
	if err := c.doJSON(http.MethodPost, c.agilePath(path), nil, map[string][]string{"issues": {key}}, nil); err != nil {
		return "", err
	}
	var issue struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	err := c.doJSON(http.MethodGet, c.agilePath("/issue/"+url.PathEscape(key)), url.Values{"fields": {"sprint"}}, nil, &issue)
	if err != nil {
		return "", fmt.Errorf("movimentação aceita; falha ao confirmar destino: %w", err)
	}
	raw, ok := issue.Fields["sprint"]
	if ok && string(raw) == "null" && sprintID == 0 {
		return key + ": backlog confirmado.", nil
	}
	var sprint Sprint
	if ok && json.Unmarshal(raw, &sprint) == nil && sprintID > 0 && sprint.ID == sprintID {
		return fmt.Sprintf("%s: sprint %d (%s) confirmada.", key, sprint.ID, sprint.Name), nil
	}
	return "", fmt.Errorf("movimentação aceita, mas destino ainda não confirmado; consulte a issue antes de repetir")
}

type History struct {
	ID      string           `json:"id"`
	Created string           `json:"created"`
	Author  User             `json:"author"`
	Items   []map[string]any `json:"items"`
}

type HistoryPage struct {
	Values      []History `json:"values"`
	NextStartAt *int      `json:"next_start_at,omitempty"`
}

func (c *Client) Changelog(key, field string, start, limit int) (HistoryPage, error) {
	page := HistoryPage{Values: []History{}}
	path := c.apiPath("/issue/" + url.PathEscape(key))
	var histories []History
	var more bool
	if c.cfg.Deployment == DeploymentServer {
		var result struct {
			Changelog *struct {
				Histories []History `json:"histories"`
				Total     int       `json:"total"`
			} `json:"changelog"`
		}
		if err := c.doJSON(http.MethodGet, path, url.Values{"expand": {"changelog"}, "fields": {"summary"}}, nil, &result); err != nil {
			return page, err
		}
		if result.Changelog == nil || result.Changelog.Total > len(result.Changelog.Histories) {
			return page, fmt.Errorf("histórico incompleto na expansão changelog do Data Center")
		}
		all := result.Changelog.Histories
		if start < len(all) {
			count := min(limit, len(all)-start)
			histories = all[start : start+count]
			more = start+count < len(all)
		}
	} else {
		var result struct {
			Values []History `json:"values"`
			IsLast bool      `json:"isLast"`
			Total  int       `json:"total"`
		}
		q := url.Values{"startAt": {strconv.Itoa(start)}, "maxResults": {strconv.Itoa(limit)}}
		if err := c.doJSON(http.MethodGet, path+"/changelog", q, nil, &result); err != nil {
			return page, err
		}
		histories = result.Values
		more = !result.IsLast && len(histories) > 0 && (result.Total == 0 || start+len(histories) < result.Total)
	}
	if more {
		next := start + len(histories)
		page.NextStartAt = &next
	}
	for _, history := range histories {
		if field != "" {
			items := history.Items[:0]
			for _, item := range history.Items {
				name, _ := item["field"].(string)
				if strings.EqualFold(name, field) || item["fieldId"] == field {
					items = append(items, item)
				}
			}
			history.Items = items
			if len(items) == 0 {
				continue
			}
		}
		page.Values = append(page.Values, history)
	}
	return page, nil
}
