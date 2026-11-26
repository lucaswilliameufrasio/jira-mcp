package tools

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"jira-mcp/internal/jira"
	"jira-mcp/internal/mcp"
)

func registerPlanning(s *mcp.Server, client *jira.Client) {
	key := map[string]any{"type": "string", "minLength": 1, "description": "Chave ou ID da issue."}
	s.RegisterTool(mcp.Tool{Name: "jira_upload_attachment", Description: "Anexa um arquivo (Base64, até 15 MiB) e retorna ID, nome e tamanho.", InputSchema: mcp.InputSchema{Type: "object", Required: []string{"issue_key", "filename", "data"}, Properties: map[string]any{
		"issue_key": key, "filename": map[string]any{"type": "string", "minLength": 1}, "data": map[string]any{"type": "string", "maxLength": base64.StdEncoding.EncodedLen(maxInlineAttachmentBytes)},
	}}}, textTool(client, handleUpload))
	s.RegisterTool(mcp.Tool{Name: "jira_move_issue", Description: "Move uma issue para sprint ou backlog e consulta o destino após a escrita.", InputSchema: mcp.InputSchema{Type: "object", Required: []string{"issue_key", "destination"}, Properties: map[string]any{
		"issue_key": key, "destination": map[string]any{"type": "string", "enum": []string{"sprint", "backlog"}}, "sprint_id": map[string]any{"type": "integer", "minimum": 1, "description": "Obrigatório apenas para destino sprint."},
	}}}, textTool(client, handleMove))
	s.RegisterTool(mcp.Tool{Name: "jira_get_issue_changelog", Description: "Lista mudanças de uma issue com autor e data. field filtra pelo nome ou ID na página.", InputSchema: mcp.InputSchema{Type: "object", Required: []string{"issue_key"}, Properties: map[string]any{
		"issue_key": key, "field": map[string]any{"type": "string"}, "start_at": offsetSchema(), "max_results": map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
	}}}, textTool(client, handleHistory))
}

func handleUpload(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var args struct {
			Key      string  `json:"issue_key"`
			Filename string  `json:"filename"`
			Data     *string `json:"data"`
		}
		if err := unmarshal(raw, &args); err != nil {
			return "", err
		}
		if strings.TrimSpace(args.Key) == "" || strings.TrimSpace(args.Filename) == "" || strings.ContainsAny(args.Filename, `/\`) || strings.IndexFunc(args.Filename, unicode.IsControl) >= 0 {
			return "", fmt.Errorf("informe issue_key e filename sem caminho ou caracteres de controle")
		}
		if args.Data == nil || len(*args.Data) > base64.StdEncoding.EncodedLen(maxInlineAttachmentBytes) {
			return "", fmt.Errorf("informe data em Base64, até 15 MiB")
		}
		data, err := base64.StdEncoding.Strict().DecodeString(*args.Data)
		if err != nil {
			return "", fmt.Errorf("data não é Base64 válido")
		}
		attachment, err := client.UploadAttachment(args.Key, args.Filename, data)
		if err != nil {
			return "", friendlyError(err)
		}
		return fmt.Sprintf("ID: %s | Nome: %s | Tamanho: %d bytes", attachment.ID, attachment.Filename, attachment.Size), nil
	}
}

func handleMove(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var args struct {
			Key         string `json:"issue_key"`
			Destination string `json:"destination"`
			SprintID    *int   `json:"sprint_id"`
		}
		if err := unmarshal(raw, &args); err != nil {
			return "", err
		}
		if strings.TrimSpace(args.Key) == "" {
			return "", fmt.Errorf("informe issue_key")
		}
		switch args.Destination {
		case "sprint":
			if args.SprintID != nil && *args.SprintID > 0 {
				return client.MoveIssue(args.Key, *args.SprintID)
			}
		case "backlog":
			if args.SprintID == nil {
				return client.MoveIssue(args.Key, 0)
			}
		}
		return "", fmt.Errorf("use destination=sprint com sprint_id positivo ou destination=backlog sem sprint_id")
	}
}

func handleHistory(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var args struct {
			Key   string `json:"issue_key"`
			Field string `json:"field"`
			Start int    `json:"start_at"`
			Limit int    `json:"max_results"`
		}
		args.Limit = 25
		if err := unmarshal(raw, &args); err != nil {
			return "", err
		}
		if strings.TrimSpace(args.Key) == "" || args.Start < 0 || args.Limit < 1 || args.Limit > 100 {
			return "", fmt.Errorf("informe issue_key, start_at não negativo e max_results entre 1 e 100")
		}
		page, err := client.Changelog(args.Key, args.Field, args.Start, args.Limit)
		if err != nil {
			return "", friendlyError(err)
		}
		encoded, err := json.Marshal(page)
		return string(encoded), err
	}
}
