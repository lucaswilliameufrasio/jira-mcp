// Package tools wires Jira operations up as MCP tools: it declares each
// tool's JSON schema and turns raw JSON arguments into calls against the
// jira.Client, formatting the results as plain text for the model to read.
package tools

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"jira-mcp/internal/jira"
	"jira-mcp/internal/mcp"
)

// maxInlineAttachmentBytes caps how large a file jira_get_attachment will
// embed inline (base64, in a single JSON-RPC message). Above this size, the
// tool returns metadata plus a download link instead of the raw bytes.
const maxInlineAttachmentBytes = 15 * 1024 * 1024 // 15 MB

// Register attaches every Jira tool to the given MCP server.
func Register(s *mcp.Server, client *jira.Client) {
	s.RegisterTool(mcp.Tool{
		Name:        "jira_search",
		Description: "Busca issues no Jira usando uma consulta JQL. Retorna chave, tipo, status, responsável e resumo de cada issue encontrada.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"jql": map[string]interface{}{
					"type":        "string",
					"description": "Consulta JQL, ex: 'project = ABC AND status = \"In Progress\" ORDER BY updated DESC'",
				},
				"max_results": map[string]interface{}{
					"type":        "integer",
					"description": "Número máximo de issues a retornar (padrão 25, máximo 100).",
				},
				"fields": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Campos específicos a retornar, ex: [\"summary\",\"status\",\"assignee\"]. Se omitido, usa um conjunto padrão útil.",
				},
				"page_token": map[string]interface{}{
					"type":        "string",
					"description": "Token de paginação retornado por uma chamada anterior (apenas Jira Cloud), para buscar a próxima página.",
				},
			},
			Required: []string{"jql"},
		},
	}, mcp.TextHandler(handleSearch(client)))

	s.RegisterTool(mcp.Tool{
		Name:        "jira_get_issue",
		Description: "Busca os detalhes completos de uma issue específica pelo seu identificador (ex: 'PROJ-123').",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"issue_key": map[string]interface{}{
					"type":        "string",
					"description": "Chave ou ID da issue, ex: 'PROJ-123'.",
				},
				"fields": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Campos específicos a retornar. Se omitido, retorna todos os campos, incluindo customfield_*.",
				},
			},
			Required: []string{"issue_key"},
		},
	}, mcp.TextHandler(handleGetIssue(client)))

	s.RegisterTool(mcp.Tool{
		Name:        "jira_get_field_metadata",
		Description: "Busca metadata dos campos editáveis de uma issue, incluindo nome, tipo, obrigatoriedade, operações e valores permitidos dos customfield_*.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"issue_key":   map[string]interface{}{"type": "string", "description": "Chave ou ID da issue."},
				"custom_only": map[string]interface{}{"type": "boolean", "description": "Se true (padrão), retorna apenas campos customfield_*. Use false para incluir campos padrão."},
			},
			Required: []string{"issue_key"},
		},
	}, mcp.TextHandler(handleGetFieldMetadata(client)))

	s.RegisterTool(mcp.Tool{
		Name:        "jira_set_issue_epic",
		Description: "Associa uma issue a um Epic usando parent ou o campo Epic Link legado detectado pela metadata Jira.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"issue_key": map[string]interface{}{"type": "string", "description": "Issue que será associada ao Epic."},
				"epic_key":  map[string]interface{}{"type": "string", "description": "Chave da issue cujo tipo deve ser Epic."},
			},
			Required: []string{"issue_key", "epic_key"},
		},
	}, mcp.TextHandler(handleSetIssueEpic(client)))

	s.RegisterTool(mcp.Tool{
		Name:        "jira_get_issue_hierarchy",
		Description: "Retorna o parent e o Epic atual de uma issue, incluindo o mecanismo usado pela instância Jira.",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]interface{}{"issue_key": map[string]interface{}{"type": "string", "description": "Chave ou ID da issue."}},
			Required:   []string{"issue_key"},
		},
	}, mcp.TextHandler(handleGetIssueHierarchy(client)))

	s.RegisterTool(mcp.Tool{
		Name:        "jira_create_issue",
		Description: "Cria uma nova issue no Jira.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"project_key": map[string]interface{}{
					"type":        "string",
					"description": "Chave do projeto, ex: 'PROJ'.",
				},
				"issue_type": map[string]interface{}{
					"type":        "string",
					"description": "Tipo da issue, ex: 'Task', 'Bug', 'Story'.",
				},
				"summary": map[string]interface{}{
					"type":        "string",
					"description": "Título/resumo da issue.",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "Descrição em texto simples (parágrafos separados por linha em branco).",
				},
				"extra_fields": map[string]interface{}{
					"type":        "object",
					"description": "Campos adicionais do Jira por nome de API, ex: {\"priority\":{\"name\":\"High\"},\"labels\":[\"backend\"]}.",
				},
			},
			Required: []string{"project_key", "issue_type", "summary"},
		},
	}, mcp.TextHandler(handleCreateIssue(client)))

	s.RegisterTool(mcp.Tool{
		Name:        "jira_update_issue",
		Description: "Atualiza campos de uma issue existente (atualização parcial).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"issue_key": map[string]interface{}{
					"type":        "string",
					"description": "Chave ou ID da issue a atualizar.",
				},
				"fields": map[string]interface{}{
					"type":        "object",
					"description": "Mapa de campos a atualizar por nome de API do Jira, incluindo customfield_*, ex: {\"summary\":\"Novo título\",\"priority\":{\"name\":\"High\"}}.",
				},
				"custom_fields": map[string]interface{}{
					"type":        "object",
					"description": "Mapa explícito de campos customizados, por exemplo {\"customfield_10001\":\"valor\"}. As chaves devem ser os nomes API customfield_*.",
				},
			},
			Required: []string{"issue_key"},
		},
	}, mcp.TextHandler(handleUpdateIssue(client)))

	s.RegisterTool(mcp.Tool{
		Name:        "jira_add_comment",
		Description: "Adiciona um comentário de texto simples a uma issue.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"issue_key": map[string]interface{}{"type": "string", "description": "Chave ou ID da issue."},
				"comment":   map[string]interface{}{"type": "string", "description": "Texto do comentário."},
			},
			Required: []string{"issue_key", "comment"},
		},
	}, mcp.TextHandler(handleAddComment(client)))

	s.RegisterTool(mcp.Tool{
		Name:        "jira_list_transitions",
		Description: "Lista as transições de workflow disponíveis para uma issue no momento (nome, status de destino e ID necessário para executá-la).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"issue_key": map[string]interface{}{"type": "string", "description": "Chave ou ID da issue."},
			},
			Required: []string{"issue_key"},
		},
	}, mcp.TextHandler(handleListTransitions(client)))

	s.RegisterTool(mcp.Tool{
		Name:        "jira_transition_issue",
		Description: "Move uma issue pelo workflow (ex: 'To Do' -> 'In Progress' -> 'Done'). Use jira_list_transitions primeiro para descobrir o ID ou o nome exato da transição.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"issue_key": map[string]interface{}{"type": "string", "description": "Chave ou ID da issue."},
				"transition_id": map[string]interface{}{
					"type":        "string",
					"description": "ID da transição (obtido via jira_list_transitions). Use isto ou transition_name.",
				},
				"transition_name": map[string]interface{}{
					"type":        "string",
					"description": "Nome da transição ou do status de destino (ex: 'Done'). Usado se transition_id não for informado.",
				},
				"comment": map[string]interface{}{
					"type":        "string",
					"description": "Comentário opcional a adicionar junto com a transição.",
				},
			},
			Required: []string{"issue_key"},
		},
	}, mcp.TextHandler(handleTransitionIssue(client)))

	// --- Issue links ---

	s.RegisterTool(mcp.Tool{
		Name:        "jira_list_issue_link_types",
		Description: "Lista os tipos formais de links disponíveis no Jira, incluindo as descrições inward e outward (por exemplo, Relates/relates to e Blocks/blocks/blocked by).",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]interface{}{}},
	}, mcp.TextHandler(handleListIssueLinkTypes(client)))

	s.RegisterTool(mcp.Tool{
		Name:        "jira_list_issue_links",
		Description: "Lê os links de uma issue, mostrando ID, tipo, direção e issue relacionada.",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]interface{}{"issue_key": map[string]interface{}{"type": "string", "description": "Chave ou ID da issue."}},
			Required:   []string{"issue_key"},
		},
	}, mcp.TextHandler(handleListIssueLinks(client)))

	s.RegisterTool(mcp.Tool{
		Name:        "jira_create_issue_link",
		Description: "Cria um link formal entre duas issues. Use jira_list_issue_link_types para consultar os nomes e as direções suportadas.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"link_type":     map[string]interface{}{"type": "string", "description": "Nome formal do tipo, ex: Relates ou Blocks. Também aceita aliases como 'relates to', 'blocks' e 'blocked by'."},
				"inward_issue":  map[string]interface{}{"type": "string", "description": "Issue no lado inward da relação."},
				"outward_issue": map[string]interface{}{"type": "string", "description": "Issue no lado outward da relação."},
				"comment":       map[string]interface{}{"type": "string", "description": "Comentário opcional adicionado à issue outward."},
			},
			Required: []string{"link_type", "inward_issue", "outward_issue"},
		},
	}, mcp.TextHandler(handleCreateIssueLink(client)))

	s.RegisterTool(mcp.Tool{
		Name:        "jira_update_issue_link",
		Description: "Atualiza um link formal. Como o Jira não oferece PUT para tipo/direção, a operação recria o link e o novo link recebe outro ID.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"link_id":       map[string]interface{}{"type": "string", "description": "ID do link retornado por jira_list_issue_links."},
				"link_type":     map[string]interface{}{"type": "string", "description": "Novo tipo formal ou alias."},
				"inward_issue":  map[string]interface{}{"type": "string", "description": "Nova issue inward, opcional."},
				"outward_issue": map[string]interface{}{"type": "string", "description": "Nova issue outward, opcional."},
				"comment":       map[string]interface{}{"type": "string", "description": "Novo comentário opcional."},
			},
			Required: []string{"link_id"},
		},
	}, mcp.TextHandler(handleUpdateIssueLink(client)))

	s.RegisterTool(mcp.Tool{
		Name:        "jira_delete_issue_link",
		Description: "Remove um link entre issues pelo ID retornado por jira_list_issue_links.",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]interface{}{"link_id": map[string]interface{}{"type": "string", "description": "ID do link."}},
			Required:   []string{"link_id"},
		},
	}, mcp.TextHandler(handleDeleteIssueLink(client)))

	s.RegisterTool(mcp.Tool{
		Name:        "jira_list_projects",
		Description: "Lista os projetos do Jira visíveis para o usuário autenticado.",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]interface{}{},
		},
	}, mcp.TextHandler(handleListProjects(client)))

	s.RegisterTool(mcp.Tool{
		Name:        "jira_assign_issue",
		Description: "Atribui (ou remove a atribuição de) uma issue a um usuário.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"issue_key": map[string]interface{}{"type": "string", "description": "Chave ou ID da issue."},
				"assignee": map[string]interface{}{
					"type":        "string",
					"description": "accountId (Cloud) ou username (Server/DC) do responsável. Use 'unassign' para remover a atribuição.",
				},
			},
			Required: []string{"issue_key", "assignee"},
		},
	}, mcp.TextHandler(handleAssignIssue(client)))

	// --- Agile: boards & sprints ---

	s.RegisterTool(mcp.Tool{
		Name:        "jira_list_boards",
		Description: "Lista os boards (Scrum/Kanban) do Jira Software visíveis para o usuário, opcionalmente filtrados por projeto.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"project_key": map[string]interface{}{
					"type":        "string",
					"description": "Filtra por chave do projeto, ex: 'PROJ'. Se omitido, lista todos os boards visíveis.",
				},
				"max_results": map[string]interface{}{
					"type":        "integer",
					"description": "Número máximo de boards a retornar após paginação (padrão 50).",
				},
			},
		},
	}, mcp.TextHandler(handleListBoards(client)))

	s.RegisterTool(mcp.Tool{
		Name:        "jira_get_board",
		Description: "Retorna detalhes de um board específico (nome, tipo Scrum/Kanban, projeto associado) pelo seu ID.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"board_id": map[string]interface{}{"type": "integer", "description": "ID numérico do board."},
			},
			Required: []string{"board_id"},
		},
	}, mcp.TextHandler(handleGetBoard(client)))

	s.RegisterTool(mcp.Tool{
		Name:        "jira_list_sprints",
		Description: "Lista as sprints de um board Scrum (com estado, datas e meta), opcionalmente filtradas por estado.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"board_id": map[string]interface{}{"type": "integer", "description": "ID numérico do board."},
				"state": map[string]interface{}{
					"type":        "string",
					"description": "Filtra por estado: 'active', 'closed', 'future', ou combinações separadas por vírgula.",
				},
			},
			Required: []string{"board_id"},
		},
	}, mcp.TextHandler(handleListSprints(client)))

	s.RegisterTool(mcp.Tool{
		Name:        "jira_board_issues",
		Description: "Lista as issues de um board (backlog + sprint ativa em Scrum, ou colunas em Kanban), opcionalmente filtradas por JQL adicional.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"board_id":    map[string]interface{}{"type": "integer", "description": "ID numérico do board."},
				"jql":         map[string]interface{}{"type": "string", "description": "Filtro JQL adicional, opcional."},
				"max_results": map[string]interface{}{"type": "integer", "description": "Número máximo de issues (padrão 50)."},
			},
			Required: []string{"board_id"},
		},
	}, mcp.TextHandler(handleBoardIssues(client)))

	s.RegisterTool(mcp.Tool{
		Name:        "jira_sprint_issues",
		Description: "Lista as issues de uma sprint específica pelo seu ID.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"sprint_id":   map[string]interface{}{"type": "integer", "description": "ID numérico da sprint."},
				"jql":         map[string]interface{}{"type": "string", "description": "Filtro JQL adicional, opcional."},
				"max_results": map[string]interface{}{"type": "integer", "description": "Número máximo de issues (padrão 50)."},
			},
			Required: []string{"sprint_id"},
		},
	}, mcp.TextHandler(handleSprintIssues(client)))

	// --- Attachments (imagens, vídeos e outros arquivos) ---

	s.RegisterTool(mcp.Tool{
		Name:        "jira_list_attachments",
		Description: "Lista os anexos (imagens, vídeos, documentos etc.) de uma issue, com nome, tamanho, tipo e um identificador para baixar cada um.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"issue_key": map[string]interface{}{"type": "string", "description": "Chave ou ID da issue."},
			},
			Required: []string{"issue_key"},
		},
	}, mcp.TextHandler(handleListAttachments(client)))

	s.RegisterTool(mcp.Tool{
		Name: "jira_get_attachment",
		Description: "Baixa um anexo de uma issue. Se for uma imagem (png/jpeg/gif/webp) e não for muito grande, o conteúdo é " +
			"retornado diretamente para a IA analisar. Para outros tipos (incluindo vídeo), o protocolo MCP não suporta " +
			"conteúdo binário embutido além de imagens, então retorna metadados e um link de download autenticado.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"issue_key": map[string]interface{}{"type": "string", "description": "Chave ou ID da issue."},
				"filename": map[string]interface{}{
					"type":        "string",
					"description": "Nome do arquivo a baixar, exatamente como aparece em jira_list_attachments. Use isto ou attachment_id.",
				},
				"attachment_id": map[string]interface{}{
					"type":        "string",
					"description": "ID do anexo (retornado por jira_list_attachments). Usado se filename não for informado.",
				},
			},
			Required: []string{"issue_key"},
		},
	}, handleGetAttachment(client))
}

func AvailableToolNames() []string {
	return []string{
		"jira_search", "jira_get_issue", "jira_get_field_metadata", "jira_set_issue_epic", "jira_get_issue_hierarchy", "jira_create_issue", "jira_update_issue",
		"jira_add_comment", "jira_list_transitions", "jira_transition_issue",
		"jira_list_issue_link_types", "jira_list_issue_links", "jira_create_issue_link",
		"jira_update_issue_link", "jira_delete_issue_link",
		"jira_list_projects", "jira_assign_issue", "jira_list_boards", "jira_get_board",
		"jira_list_sprints", "jira_board_issues", "jira_sprint_issues",
		"jira_list_attachments", "jira_get_attachment",
	}
}

// --- argument structs ---

type searchArgs struct {
	JQL        string   `json:"jql"`
	MaxResults int      `json:"max_results"`
	Fields     []string `json:"fields"`
	PageToken  string   `json:"page_token"`
}

type getIssueArgs struct {
	IssueKey string   `json:"issue_key"`
	Fields   []string `json:"fields"`
}

type getFieldMetadataArgs struct {
	IssueKey   string `json:"issue_key"`
	CustomOnly *bool  `json:"custom_only"`
}

type issueEpicArgs struct {
	IssueKey string `json:"issue_key"`
	EpicKey  string `json:"epic_key"`
}

type createIssueArgs struct {
	ProjectKey  string                 `json:"project_key"`
	IssueType   string                 `json:"issue_type"`
	Summary     string                 `json:"summary"`
	Description string                 `json:"description"`
	ExtraFields map[string]interface{} `json:"extra_fields"`
}

type updateIssueArgs struct {
	IssueKey     string                 `json:"issue_key"`
	Fields       map[string]interface{} `json:"fields"`
	CustomFields map[string]interface{} `json:"custom_fields"`
}

type addCommentArgs struct {
	IssueKey string `json:"issue_key"`
	Comment  string `json:"comment"`
}

type listTransitionsArgs struct {
	IssueKey string `json:"issue_key"`
}

type transitionIssueArgs struct {
	IssueKey       string `json:"issue_key"`
	TransitionID   string `json:"transition_id"`
	TransitionName string `json:"transition_name"`
	Comment        string `json:"comment"`
}

type listIssueLinksArgs struct {
	IssueKey string `json:"issue_key"`
}

type issueLinkArgs struct {
	LinkID       string `json:"link_id"`
	LinkType     string `json:"link_type"`
	InwardIssue  string `json:"inward_issue"`
	OutwardIssue string `json:"outward_issue"`
	Comment      string `json:"comment"`
}

type assignIssueArgs struct {
	IssueKey string `json:"issue_key"`
	Assignee string `json:"assignee"`
}

var defaultIssueFields = []string{"summary", "status", "issuetype", "assignee", "reporter", "priority", "updated"}

// --- handlers ---

func handleSearch(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var args searchArgs
		if err := unmarshal(raw, &args); err != nil {
			return "", err
		}
		if strings.TrimSpace(args.JQL) == "" {
			return "", fmt.Errorf("o parâmetro 'jql' é obrigatório")
		}
		fields := args.Fields
		if len(fields) == 0 {
			fields = []string{"*all"}
		}
		result, err := client.SearchIssues(args.JQL, args.MaxResults, fields, args.PageToken)
		if err != nil {
			return "", friendlyError(err)
		}
		return formatSearchResult(result), nil
	}
}

func handleGetIssue(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var args getIssueArgs
		if err := unmarshal(raw, &args); err != nil {
			return "", err
		}
		if strings.TrimSpace(args.IssueKey) == "" {
			return "", fmt.Errorf("o parâmetro 'issue_key' é obrigatório")
		}
		fields := args.Fields
		if len(fields) == 0 {
			fields = []string{"*all"}
		}
		issue, err := client.GetIssue(args.IssueKey, fields, nil)
		if err != nil {
			return "", friendlyError(err)
		}
		return formatIssue(issue), nil
	}
}

func handleGetFieldMetadata(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var args getFieldMetadataArgs
		if err := unmarshal(raw, &args); err != nil {
			return "", err
		}
		if strings.TrimSpace(args.IssueKey) == "" {
			return "", fmt.Errorf("o parâmetro 'issue_key' é obrigatório")
		}
		metadata, err := client.GetFieldMetadata(args.IssueKey)
		if err != nil {
			return "", friendlyError(err)
		}
		customOnly := args.CustomOnly == nil || *args.CustomOnly
		return formatFieldMetadata(args.IssueKey, metadata, customOnly), nil
	}
}

func handleSetIssueEpic(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var args issueEpicArgs
		if err := unmarshal(raw, &args); err != nil {
			return "", err
		}
		if strings.TrimSpace(args.IssueKey) == "" || strings.TrimSpace(args.EpicKey) == "" {
			return "", fmt.Errorf("'issue_key' e 'epic_key' são obrigatórios")
		}
		if err := client.SetIssueEpic(args.IssueKey, args.EpicKey); err != nil {
			return "", friendlyError(err)
		}
		return fmt.Sprintf("Issue %s associada ao Epic %s.", args.IssueKey, args.EpicKey), nil
	}
}

func handleGetIssueHierarchy(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var args listIssueLinksArgs
		if err := unmarshal(raw, &args); err != nil {
			return "", err
		}
		if strings.TrimSpace(args.IssueKey) == "" {
			return "", fmt.Errorf("o parâmetro 'issue_key' é obrigatório")
		}
		hierarchy, err := client.GetIssueHierarchy(args.IssueKey)
		if err != nil {
			return "", friendlyError(err)
		}
		parent := hierarchy.ParentKey
		if parent == "" {
			parent = "(nenhum)"
		}
		epic := hierarchy.EpicKey
		if epic == "" {
			epic = "(nenhum)"
		}
		mechanism := hierarchy.EpicField
		if mechanism == "" {
			mechanism = "parent"
		}
		return fmt.Sprintf("Hierarquia de %s:\n- parent: %s\n- epic: %s\n- mecanismo: %s", args.IssueKey, parent, epic, mechanism), nil
	}
}

func handleCreateIssue(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var args createIssueArgs
		if err := unmarshal(raw, &args); err != nil {
			return "", err
		}
		if args.ProjectKey == "" || args.IssueType == "" || args.Summary == "" {
			return "", fmt.Errorf("'project_key', 'issue_type' e 'summary' são obrigatórios")
		}
		createFields := make(map[string]interface{}, len(args.ExtraFields)+3)
		createFields["project"] = map[string]string{"key": args.ProjectKey}
		createFields["summary"] = args.Summary
		createFields["issuetype"] = map[string]string{"name": args.IssueType}
		for key, value := range args.ExtraFields {
			createFields[key] = value
		}
		if err := client.ValidateCreateFields(args.ProjectKey, args.IssueType, createFields); err != nil {
			return "", friendlyError(err)
		}
		issue, err := client.CreateIssue(jira.CreateIssueInput{
			ProjectKey:  args.ProjectKey,
			IssueType:   args.IssueType,
			Summary:     args.Summary,
			Description: args.Description,
			ExtraFields: args.ExtraFields,
		})
		if err != nil {
			return "", friendlyError(err)
		}
		return fmt.Sprintf("Issue criada com sucesso: %s (%s)", issue.Key, client.BrowseURL(issue.Key)), nil
	}
}

func handleUpdateIssue(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var args updateIssueArgs
		if err := unmarshal(raw, &args); err != nil {
			return "", err
		}
		if args.IssueKey == "" || (len(args.Fields) == 0 && len(args.CustomFields) == 0) {
			return "", fmt.Errorf("'issue_key' e 'fields' ou 'custom_fields' são obrigatórios")
		}
		fields := make(map[string]interface{}, len(args.Fields)+len(args.CustomFields))
		for key, value := range args.Fields {
			fields[key] = value
		}
		for key, value := range args.CustomFields {
			if !strings.HasPrefix(key, "customfield_") {
				return "", fmt.Errorf("custom field inválido %q; use o nome API customfield_*", key)
			}
			fields[key] = value
		}
		if err := client.ValidateIssueFields(args.IssueKey, fields); err != nil {
			return "", friendlyError(err)
		}
		if err := client.UpdateIssue(args.IssueKey, fields); err != nil {
			return "", friendlyError(err)
		}
		return fmt.Sprintf("Issue %s atualizada com sucesso.", args.IssueKey), nil
	}
}

func handleAddComment(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var args addCommentArgs
		if err := unmarshal(raw, &args); err != nil {
			return "", err
		}
		if args.IssueKey == "" || strings.TrimSpace(args.Comment) == "" {
			return "", fmt.Errorf("'issue_key' e 'comment' são obrigatórios")
		}
		if err := client.AddComment(args.IssueKey, args.Comment); err != nil {
			return "", friendlyError(err)
		}
		return fmt.Sprintf("Comentário adicionado à issue %s.", args.IssueKey), nil
	}
}

func handleListTransitions(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var args listTransitionsArgs
		if err := unmarshal(raw, &args); err != nil {
			return "", err
		}
		if args.IssueKey == "" {
			return "", fmt.Errorf("o parâmetro 'issue_key' é obrigatório")
		}
		transitions, err := client.ListTransitions(args.IssueKey)
		if err != nil {
			return "", friendlyError(err)
		}
		if len(transitions) == 0 {
			return fmt.Sprintf("Nenhuma transição disponível para %s.", args.IssueKey), nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Transições disponíveis para %s:\n", args.IssueKey)
		for _, t := range transitions {
			fmt.Fprintf(&sb, "- id=%s  nome=%q  ->  status=%q\n", t.ID, t.Name, t.To.Name)
		}
		return sb.String(), nil
	}
}

func handleTransitionIssue(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var args transitionIssueArgs
		if err := unmarshal(raw, &args); err != nil {
			return "", err
		}
		if args.IssueKey == "" {
			return "", fmt.Errorf("o parâmetro 'issue_key' é obrigatório")
		}
		if args.TransitionID == "" && args.TransitionName == "" {
			return "", fmt.Errorf("informe 'transition_id' ou 'transition_name'")
		}

		transitionID := args.TransitionID
		if transitionID == "" {
			transitions, err := client.ListTransitions(args.IssueKey)
			if err != nil {
				return "", friendlyError(err)
			}
			match := findTransitionByName(transitions, args.TransitionName)
			if match == nil {
				return "", fmt.Errorf(
					"nenhuma transição chamada %q encontrada para %s; use jira_list_transitions para ver as opções válidas",
					args.TransitionName, args.IssueKey,
				)
			}
			transitionID = match.ID
		}

		if err := client.TransitionIssue(args.IssueKey, transitionID, args.Comment); err != nil {
			return "", friendlyError(err)
		}
		return fmt.Sprintf("Issue %s transicionada com sucesso.", args.IssueKey), nil
	}
}

func handleListIssueLinkTypes(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		types, err := client.ListIssueLinkTypes()
		if err != nil {
			return "", friendlyError(err)
		}
		if len(types) == 0 {
			return "Nenhum tipo de issue link disponível.", nil
		}
		var sb strings.Builder
		sb.WriteString("Tipos de issue link disponíveis:\n")
		for _, linkType := range types {
			fmt.Fprintf(&sb, "- %s (inward=%q, outward=%q, id=%s)\n", linkType.Name, linkType.Inward, linkType.Outward, linkType.ID)
		}
		return sb.String(), nil
	}
}

func handleListIssueLinks(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var args listIssueLinksArgs
		if err := unmarshal(raw, &args); err != nil {
			return "", err
		}
		if strings.TrimSpace(args.IssueKey) == "" {
			return "", fmt.Errorf("o parâmetro 'issue_key' é obrigatório")
		}
		links, err := client.ListIssueLinks(args.IssueKey)
		if err != nil {
			return "", friendlyError(err)
		}
		if len(links) == 0 {
			return fmt.Sprintf("A issue %s não possui links.", args.IssueKey), nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Links de %s:\n", args.IssueKey)
		for _, link := range links {
			fmt.Fprintf(&sb, "- id=%s  tipo=%q  inward=%s (%s)  outward=%s (%s)\n",
				link.ID, link.Type.Name, link.InwardIssue.Key, link.Type.Inward, link.OutwardIssue.Key, link.Type.Outward)
		}
		return sb.String(), nil
	}
}

func handleCreateIssueLink(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var args issueLinkArgs
		if err := unmarshal(raw, &args); err != nil {
			return "", err
		}
		if args.LinkType == "" || args.InwardIssue == "" || args.OutwardIssue == "" {
			return "", fmt.Errorf("'link_type', 'inward_issue' e 'outward_issue' são obrigatórios")
		}
		args.LinkType = normalizeLinkType(args.LinkType)
		resolvedType, err := client.ResolveIssueLinkType(args.LinkType)
		if err != nil {
			return "", friendlyError(err)
		}
		args.LinkType = resolvedType
		if err := client.CreateIssueLink(jira.CreateIssueLinkInput{
			LinkType: args.LinkType, InwardIssue: args.InwardIssue, OutwardIssue: args.OutwardIssue, Comment: args.Comment,
		}); err != nil {
			return "", friendlyError(err)
		}
		return fmt.Sprintf("Link %s criado: %s %s %s.", args.LinkType, args.InwardIssue, args.LinkType, args.OutwardIssue), nil
	}
}

func handleUpdateIssueLink(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var args issueLinkArgs
		if err := unmarshal(raw, &args); err != nil {
			return "", err
		}
		if args.LinkID == "" {
			return "", fmt.Errorf("o parâmetro 'link_id' é obrigatório")
		}
		if args.LinkType == "" && args.InwardIssue == "" && args.OutwardIssue == "" && args.Comment == "" {
			return "", fmt.Errorf("informe ao menos um campo para atualizar")
		}
		if args.LinkType != "" {
			args.LinkType = normalizeLinkType(args.LinkType)
			resolvedType, err := client.ResolveIssueLinkType(args.LinkType)
			if err != nil {
				return "", friendlyError(err)
			}
			args.LinkType = resolvedType
		}
		if err := client.UpdateIssueLink(args.LinkID, jira.CreateIssueLinkInput{
			LinkType: args.LinkType, InwardIssue: args.InwardIssue, OutwardIssue: args.OutwardIssue, Comment: args.Comment,
		}); err != nil {
			return "", friendlyError(err)
		}
		return fmt.Sprintf("Link %s atualizado; o Jira recriou o link com um novo ID.", args.LinkID), nil
	}
}

func handleDeleteIssueLink(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var args issueLinkArgs
		if err := unmarshal(raw, &args); err != nil {
			return "", err
		}
		if args.LinkID == "" {
			return "", fmt.Errorf("o parâmetro 'link_id' é obrigatório")
		}
		if err := client.DeleteIssueLink(args.LinkID); err != nil {
			return "", friendlyError(err)
		}
		return fmt.Sprintf("Link %s removido com sucesso.", args.LinkID), nil
	}
}

func normalizeLinkType(value string) string {
	compact := strings.ToLower(strings.TrimSpace(value))
	aliases := map[string]string{
		"relates":       "Relates",
		"relates to":    "Relates",
		"blocks":        "Blocks",
		"blocked by":    "Blocks",
		"duplicates":    "Duplicate",
		"duplicated by": "Duplicate",
		"clones":        "Cloners",
		"cloned by":     "Cloners",
		"causes":        "Causes",
		"caused by":     "Causes",
	}
	if normalized, ok := aliases[compact]; ok {
		return normalized
	}
	return strings.TrimSpace(value)
}

func handleListProjects(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		projects, err := client.ListProjects()
		if err != nil {
			return "", friendlyError(err)
		}
		if len(projects) == 0 {
			return "Nenhum projeto visível para o usuário autenticado.", nil
		}
		var sb strings.Builder
		sb.WriteString("Projetos:\n")
		for _, p := range projects {
			fmt.Fprintf(&sb, "- %s: %s (id=%s)\n", p.Key, p.Name, p.ID)
		}
		return sb.String(), nil
	}
}

func handleAssignIssue(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var args assignIssueArgs
		if err := unmarshal(raw, &args); err != nil {
			return "", err
		}
		if args.IssueKey == "" || args.Assignee == "" {
			return "", fmt.Errorf("'issue_key' e 'assignee' são obrigatórios")
		}
		if err := client.AssignIssue(args.IssueKey, args.Assignee); err != nil {
			return "", friendlyError(err)
		}
		if args.Assignee == "unassign" {
			return fmt.Sprintf("Issue %s teve sua atribuição removida.", args.IssueKey), nil
		}
		return fmt.Sprintf("Issue %s atribuída a %s.", args.IssueKey, args.Assignee), nil
	}
}

// --- board / sprint argument structs & handlers ---

type listBoardsArgs struct {
	ProjectKey string `json:"project_key"`
	MaxResults int    `json:"max_results"`
}

type getBoardArgs struct {
	BoardID int `json:"board_id"`
}

type listSprintsArgs struct {
	BoardID int    `json:"board_id"`
	State   string `json:"state"`
}

type boardIssuesArgs struct {
	BoardID    int    `json:"board_id"`
	JQL        string `json:"jql"`
	MaxResults int    `json:"max_results"`
}

type sprintIssuesArgs struct {
	SprintID   int    `json:"sprint_id"`
	JQL        string `json:"jql"`
	MaxResults int    `json:"max_results"`
}

func handleListBoards(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var args listBoardsArgs
		if err := unmarshal(raw, &args); err != nil {
			return "", err
		}
		boards, err := client.ListBoards(args.ProjectKey, args.MaxResults)
		if err != nil {
			return "", friendlyError(err)
		}
		if len(boards) == 0 {
			return "Nenhum board encontrado (verifique se o Jira Software está habilitado e se o projeto tem um board).", nil
		}
		var sb strings.Builder
		sb.WriteString("Boards:\n")
		for _, b := range boards {
			fmt.Fprintf(&sb, "- id=%d  nome=%q  tipo=%s  projeto=%s\n", b.ID, b.Name, b.Type, b.Location.ProjectKey)
		}
		return sb.String(), nil
	}
}

func handleGetBoard(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var args getBoardArgs
		if err := unmarshal(raw, &args); err != nil {
			return "", err
		}
		if args.BoardID == 0 {
			return "", fmt.Errorf("o parâmetro 'board_id' é obrigatório")
		}
		board, err := client.GetBoard(args.BoardID)
		if err != nil {
			return "", friendlyError(err)
		}
		return fmt.Sprintf("Board %d: %q (tipo=%s, projeto=%s)", board.ID, board.Name, board.Type, board.Location.ProjectKey), nil
	}
}

func handleListSprints(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var args listSprintsArgs
		if err := unmarshal(raw, &args); err != nil {
			return "", err
		}
		if args.BoardID == 0 {
			return "", fmt.Errorf("o parâmetro 'board_id' é obrigatório")
		}
		sprints, err := client.ListSprints(args.BoardID, args.State, 0)
		if err != nil {
			return "", friendlyError(err)
		}
		if len(sprints) == 0 {
			return fmt.Sprintf("Nenhuma sprint encontrada para o board %d (é um board Scrum?).", args.BoardID), nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Sprints do board %d:\n", args.BoardID)
		for _, sp := range sprints {
			fmt.Fprintf(&sb, "- id=%d  nome=%q  estado=%s  início=%s  fim=%s\n", sp.ID, sp.Name, sp.State, sp.StartDate, sp.EndDate)
		}
		return sb.String(), nil
	}
}

func handleBoardIssues(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var args boardIssuesArgs
		if err := unmarshal(raw, &args); err != nil {
			return "", err
		}
		if args.BoardID == 0 {
			return "", fmt.Errorf("o parâmetro 'board_id' é obrigatório")
		}
		result, err := client.BoardIssues(args.BoardID, args.JQL, args.MaxResults, defaultIssueFields)
		if err != nil {
			return "", friendlyError(err)
		}
		return formatSearchResult(result), nil
	}
}

func handleSprintIssues(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var args sprintIssuesArgs
		if err := unmarshal(raw, &args); err != nil {
			return "", err
		}
		if args.SprintID == 0 {
			return "", fmt.Errorf("o parâmetro 'sprint_id' é obrigatório")
		}
		result, err := client.SprintIssues(args.SprintID, args.JQL, args.MaxResults, defaultIssueFields)
		if err != nil {
			return "", friendlyError(err)
		}
		return formatSearchResult(result), nil
	}
}

// --- attachment argument structs & handlers ---

type listAttachmentsArgs struct {
	IssueKey string `json:"issue_key"`
}

type getAttachmentArgs struct {
	IssueKey     string `json:"issue_key"`
	Filename     string `json:"filename"`
	AttachmentID string `json:"attachment_id"`
}

func handleListAttachments(client *jira.Client) func(json.RawMessage) (string, error) {
	return func(raw json.RawMessage) (string, error) {
		var args listAttachmentsArgs
		if err := unmarshal(raw, &args); err != nil {
			return "", err
		}
		if args.IssueKey == "" {
			return "", fmt.Errorf("o parâmetro 'issue_key' é obrigatório")
		}
		attachments, err := client.ListAttachments(args.IssueKey)
		if err != nil {
			return "", friendlyError(err)
		}
		if len(attachments) == 0 {
			return fmt.Sprintf("A issue %s não tem anexos.", args.IssueKey), nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Anexos de %s:\n", args.IssueKey)
		for _, a := range attachments {
			fmt.Fprintf(&sb, "- id=%s  nome=%q  tipo=%s  tamanho=%s  enviado por=%s\n",
				a.ID, a.Filename, a.MimeType, humanSize(a.Size), a.Author.DisplayName)
		}
		sb.WriteString("\nUse jira_get_attachment com 'filename' ou 'attachment_id' para baixar um deles.")
		return sb.String(), nil
	}
}

// handleGetAttachment is registered directly as an mcp.ToolHandler (rather
// than wrapped with mcp.TextHandler) because it needs to return an image
// content block, not just text.
func handleGetAttachment(client *jira.Client) mcp.ToolHandler {
	return func(raw json.RawMessage) ([]mcp.ContentBlock, error) {
		var args getAttachmentArgs
		if err := unmarshal(raw, &args); err != nil {
			return nil, err
		}
		if args.IssueKey == "" {
			return nil, fmt.Errorf("o parâmetro 'issue_key' é obrigatório")
		}
		if args.Filename == "" && args.AttachmentID == "" {
			return nil, fmt.Errorf("informe 'filename' ou 'attachment_id'")
		}

		attachments, err := client.ListAttachments(args.IssueKey)
		if err != nil {
			return nil, friendlyError(err)
		}
		var match *jira.Attachment
		for i := range attachments {
			if args.AttachmentID != "" && attachments[i].ID == args.AttachmentID {
				match = &attachments[i]
				break
			}
			if args.Filename != "" && attachments[i].Filename == args.Filename {
				match = &attachments[i]
				break
			}
		}
		if match == nil {
			return nil, fmt.Errorf("nenhum anexo correspondente encontrado em %s; use jira_list_attachments para ver os disponíveis", args.IssueKey)
		}

		if match.Size > maxInlineAttachmentBytes {
			return []mcp.ContentBlock{{
				Type: "text",
				Text: fmt.Sprintf(
					"O anexo %q (%s, %s) é grande demais para ser embutido inline. Link de download: %s",
					match.Filename, match.MimeType, humanSize(match.Size), match.Content,
				),
			}}, nil
		}

		data, contentType, err := client.DownloadAttachmentURL(match.Content)
		if err != nil {
			return nil, friendlyError(err)
		}
		mimeType := contentType
		if mimeType == "" {
			mimeType = match.MimeType
		}

		if strings.HasPrefix(mimeType, "image/") {
			return []mcp.ContentBlock{
				{Type: "text", Text: fmt.Sprintf("Anexo %q (%s, %s):", match.Filename, mimeType, humanSize(match.Size))},
				{Type: "image", Data: base64.StdEncoding.EncodeToString(data), MimeType: mimeType},
			}, nil
		}

		// Not an image (e.g. video, PDF, zip): MCP has no generic binary
		// content block, so hand back metadata and an authenticated link
		// instead of raw bytes the model couldn't do anything with anyway.
		return []mcp.ContentBlock{{
			Type: "text",
			Text: fmt.Sprintf(
				"O anexo %q é do tipo %s (%s). O protocolo MCP não suporta embutir vídeo ou outros binários genéricos "+
					"inline — apenas imagens. Baixe manualmente em: %s",
				match.Filename, mimeType, humanSize(match.Size), match.Content,
			),
		}}, nil
	}
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	units := "KMGTPE"
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), units[exp])
}

// --- formatting & small helpers ---

func unmarshal(raw json.RawMessage, v interface{}) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("argumentos inválidos: %w", err)
	}
	return nil
}

func friendlyError(err error) error {
	if apiErr, ok := err.(*jira.APIError); ok {
		return fmt.Errorf("jira retornou HTTP %d: %s", apiErr.StatusCode, compact(apiErr.Body))
	}
	return err
}

// compact trims long/noisy JSON error bodies down to something readable.
func compact(body string) string {
	body = strings.TrimSpace(body)
	if len(body) > 500 {
		return body[:500] + "…"
	}
	return body
}

func findTransitionByName(transitions []jira.Transition, name string) *jira.Transition {
	name = strings.ToLower(strings.TrimSpace(name))
	for i := range transitions {
		if strings.ToLower(transitions[i].Name) == name || strings.ToLower(transitions[i].To.Name) == name {
			return &transitions[i]
		}
	}
	return nil
}

func fieldString(fields map[string]interface{}, key string) string {
	v, ok := fields[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if m, ok := v.(map[string]interface{}); ok {
		if name, ok := m["displayName"].(string); ok {
			return name
		}
		if name, ok := m["name"].(string); ok {
			return name
		}
	}
	return ""
}

func formatIssue(issue *jira.Issue) string {
	f := issue.Fields
	var sb strings.Builder
	fmt.Fprintf(&sb, "Issue: %s\n", issue.Key)
	if summary := fieldString(f, "summary"); summary != "" {
		fmt.Fprintf(&sb, "Resumo: %s\n", summary)
	}
	if status := fieldString(f, "status"); status != "" {
		fmt.Fprintf(&sb, "Status: %s\n", status)
	}
	if issueType := fieldString(f, "issuetype"); issueType != "" {
		fmt.Fprintf(&sb, "Tipo: %s\n", issueType)
	}
	if priority := fieldString(f, "priority"); priority != "" {
		fmt.Fprintf(&sb, "Prioridade: %s\n", priority)
	}
	if assignee := fieldString(f, "assignee"); assignee != "" {
		fmt.Fprintf(&sb, "Responsável: %s\n", assignee)
	} else {
		sb.WriteString("Responsável: (não atribuído)\n")
	}
	if reporter := fieldString(f, "reporter"); reporter != "" {
		fmt.Fprintf(&sb, "Relator: %s\n", reporter)
	}
	if updated := fieldString(f, "updated"); updated != "" {
		fmt.Fprintf(&sb, "Atualizado em: %s\n", updated)
	}
	var customFields []string
	for key := range f {
		if strings.HasPrefix(key, "customfield_") {
			customFields = append(customFields, key)
		}
	}
	sort.Strings(customFields)
	for _, key := range customFields {
		value, err := json.Marshal(f[key])
		if err == nil {
			fmt.Fprintf(&sb, "%s: %s\n", key, string(value))
		}
	}
	if desc, ok := f["description"]; ok && desc != nil {
		fmt.Fprintf(&sb, "Descrição: %s\n", extractPlainText(desc))
	}
	return sb.String()
}

func formatFieldMetadata(issueKey string, metadata *jira.EditMetadata, customOnly bool) string {
	var keys []string
	for key := range metadata.Fields {
		if !customOnly || strings.HasPrefix(key, "customfield_") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		if customOnly {
			return fmt.Sprintf("Nenhum campo customizado editável encontrado para %s.", issueKey)
		}
		return fmt.Sprintf("Nenhum campo editável encontrado para %s.", issueKey)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Metadata dos campos editáveis de %s:\n", issueKey)
	for _, key := range keys {
		field := metadata.Fields[key]
		name := field.Name
		if name == "" {
			name = key
		}
		fmt.Fprintf(&sb, "- %s: %s\n", key, name)
		fmt.Fprintf(&sb, "  obrigatório: %t\n", field.Required)
		if fieldType, ok := field.Schema["type"].(string); ok && fieldType != "" {
			fmt.Fprintf(&sb, "  tipo: %s\n", fieldType)
		}
		if len(field.Operations) > 0 {
			fmt.Fprintf(&sb, "  operações: %s\n", strings.Join(field.Operations, ", "))
		}
		if len(field.AllowedValues) > 0 {
			if encoded, err := json.Marshal(field.AllowedValues); err == nil {
				fmt.Fprintf(&sb, "  valores permitidos: %s\n", encoded)
			}
		}
	}
	return sb.String()
}

// extractPlainText handles both plain-string descriptions (Server/DC, api/2)
// and Atlassian Document Format objects (Cloud, api/3), pulling out just the
// text runs so the model gets readable content either way.
func extractPlainText(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]interface{}:
		var sb strings.Builder
		var walk func(node interface{})
		walk = func(node interface{}) {
			m, ok := node.(map[string]interface{})
			if !ok {
				return
			}
			if text, ok := m["text"].(string); ok {
				sb.WriteString(text)
			}
			if content, ok := m["content"].([]interface{}); ok {
				for _, child := range content {
					walk(child)
				}
				sb.WriteString(" ")
			}
		}
		walk(t)
		return strings.TrimSpace(sb.String())
	default:
		return ""
	}
}

func formatSearchResult(result *jira.SearchResult) string {
	var sb strings.Builder
	if len(result.Issues) == 0 {
		return "Nenhuma issue encontrada para essa consulta JQL."
	}
	fmt.Fprintf(&sb, "%d issue(s) encontrada(s):\n", len(result.Issues))
	for _, issue := range result.Issues {
		summary := fieldString(issue.Fields, "summary")
		status := fieldString(issue.Fields, "status")
		assignee := fieldString(issue.Fields, "assignee")
		if assignee == "" {
			assignee = "não atribuído"
		}
		fmt.Fprintf(&sb, "- %s [%s] %s (responsável: %s)\n", issue.Key, status, summary, assignee)
	}
	if result.NextPageToken != "" {
		fmt.Fprintf(&sb, "\nHá mais resultados. Use page_token=%q para buscar a próxima página.\n", result.NextPageToken)
	} else if result.Total > 0 && len(result.Issues) < result.Total {
		fmt.Fprintf(&sb, "\nMostrando %d de %d resultados totais. Aumente max_results ou refine a consulta JQL para ver mais.\n", len(result.Issues), result.Total)
	}
	return sb.String()
}
