# jira-mcp

Servidor **MCP (Model Context Protocol)** para o Atlassian Jira, escrito em Go.
Expõe operações comuns do
Jira como *tools* que qualquer cliente MCP (Claude Desktop, Claude Code, etc.)
pode chamar.

## Ferramentas (tools) disponíveis

| Tool | Descrição |
|---|---|
| `jira_search` | Busca issues via JQL |
| `jira_get_issue` | Detalhes completos de uma issue |
| `jira_create_issue` | Cria uma nova issue |
| `jira_update_issue` | Atualiza campos de uma issue existente |
| `jira_add_comment` | Adiciona comentário a uma issue |
| `jira_list_transitions` | Lista transições de workflow disponíveis |
| `jira_transition_issue` | Move a issue no workflow (por ID ou nome) |
| `jira_list_projects` | Lista projetos visíveis |
| `jira_assign_issue` | Atribui/remove atribuição de uma issue |
| `jira_list_boards` | Lista boards Scrum/Kanban (Jira Software) |
| `jira_get_board` | Detalhes de um board |
| `jira_list_sprints` | Lista sprints de um board |
| `jira_board_issues` | Issues de um board |
| `jira_sprint_issues` | Issues de uma sprint |
| `jira_list_attachments` | Lista anexos (imagens, vídeos, docs) de uma issue |
| `jira_get_attachment` | Baixa um anexo — imagens são retornadas inline para a IA analisar |

### Sobre boards e anexos

- **Boards/sprints** usam uma API separada do Jira (`/rest/agile/1.0`), diferente
  da API núcleo usada pelas demais tools. Precisa do Jira Software habilitado
  no projeto para existir um board.
- **Anexos**: o protocolo MCP só tem um tipo de conteúdo binário embutível —
  **imagem** (base64). Não existe um bloco nativo de "vídeo". Então:
  - Imagens (`png`, `jpeg`, `gif`, `webp`) até 15&nbsp;MB são devolvidas
    inline em `jira_get_attachment`, e a IA consegue "ver" e analisar o
    conteúdo diretamente.
  - Vídeos e outros binários (PDF, zip etc.) **não são embutidos** — a tool
    retorna metadados (nome, tipo, tamanho) e um link de download
    autenticado, que você abre manualmente. Não há como uma IA "assistir" a
    um vídeo através do MCP hoje; isso é uma limitação do protocolo, não
    deste servidor.
  - Arquivos de imagem acima de 15&nbsp;MB também caem nesse fallback de
    link, para não estourar o limite de mensagem da maioria dos clientes MCP.

Suporta tanto **Jira Cloud** (API v3, ADF para descrições/comentários, paginação
via `nextPageToken` — a Atlassian removeu o endpoint de busca clássico em 2025)
quanto **Jira Server/Data Center** (API v2, texto simples, autenticação via
Personal Access Token).

## Build

Requer Go 1.21+ instalado (não foi possível compilar neste ambiente por falta
de acesso à rede — confira o build localmente antes de usar):

```bash
cd jira-mcp
go build -o jira-mcp .
```

Isso gera um binário único `jira-mcp` (ou `jira-mcp.exe` no Windows).

## Configuração

O servidor lê tudo de variáveis de ambiente:

Para configuração interativa local, execute `jira-mcp setup`. O arquivo é salvo
com permissão restrita em `XDG_CONFIG_HOME/jira-mcp/config.json`.

### Jira Cloud (padrão)

```bash
export JIRA_BASE_URL="https://suaempresa.atlassian.net"
export JIRA_EMAIL="voce@suaempresa.com"
export JIRA_API_TOKEN="seu-token-de-api"
```

Gere o token em: https://id.atlassian.com/manage-profile/security/api-tokens

### Jira Server / Data Center

```bash
export JIRA_BASE_URL="https://jira.suaempresa.com"
export JIRA_DEPLOYMENT="server"
export JIRA_PERSONAL_ACCESS_TOKEN="seu-pat"
```

### Servidor remoto multiusuário

O modo remoto usa apenas OAuth Atlassian e Valkey. Não configure API token Jira
nesse modo:

```bash
export JIRA_MCP_PUBLIC_URL="https://mcp.exemplo.com"
export JIRA_MCP_ATLASSIAN_CLIENT_ID="..."
export JIRA_MCP_ATLASSIAN_CLIENT_SECRET="..."
export VALKEY_URL="rediss://:senha@host:6379"
export JIRA_MCP_ENCRYPTION_KEY="$(openssl rand -base64 32 | tr -d '=\n')"
./jira-mcp remote
```

O servidor expõe MCP em `/mcp`, descoberta OAuth em `/.well-known/` e o fluxo
OAuth em `/oauth/*`. Tokens Atlassian são criptografados antes de serem salvos
no Valkey; cada usuário recebe sua própria seleção de tools e credenciais.

## Integração com clientes MCP

Todos os clientes abaixo usam o mesmo binário `jira-mcp` — a única diferença é
onde/como cada um lê a configuração (comando + variáveis de ambiente). Ajuste
`/caminho/absoluto/para/jira-mcp` para o caminho real do binário compilado.

### Claude Desktop

Edite (ou crie) o arquivo de config:
- macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`
- Windows: `%APPDATA%\Claude\claude_desktop_config.json`

```json
{
  "mcpServers": {
    "jira": {
      "command": "/caminho/absoluto/para/jira-mcp",
      "env": {
        "JIRA_BASE_URL": "https://suaempresa.atlassian.net",
        "JIRA_EMAIL": "voce@suaempresa.com",
        "JIRA_API_TOKEN": "seu-token-de-api"
      }
    }
  }
}
```

Reinicie o app.

### Claude Code (CLI)

Mais simples via linha de comando (escopo `user` deixa disponível em todos os
projetos; use `project` para restringir ao repositório atual):

```bash
claude mcp add jira \
  --scope user \
  --env JIRA_BASE_URL=https://suaempresa.atlassian.net \
  --env JIRA_EMAIL=voce@suaempresa.com \
  --env JIRA_API_TOKEN=seu-token-de-api \
  -- /caminho/absoluto/para/jira-mcp
```

Confira com `claude mcp list`. Alternativamente, edite `.mcp.json` na raiz do
projeto (mesmo formato JSON do Claude Desktop, chave `mcpServers`).

### VSCode (GitHub Copilot Chat / modo agente)

No VSCode 1.99+, crie `.vscode/mcp.json` na raiz do workspace (note que aqui a
chave é `servers`, não `mcpServers`, e variáveis usam `type: "stdio"`):

```json
{
  "servers": {
    "jira": {
      "type": "stdio",
      "command": "/caminho/absoluto/para/jira-mcp",
      "env": {
        "JIRA_BASE_URL": "https://suaempresa.atlassian.net",
        "JIRA_EMAIL": "voce@suaempresa.com",
        "JIRA_API_TOKEN": "seu-token-de-api"
      }
    }
  }
}
```

Depois abra o Command Palette → "MCP: List Servers" para confirmar que
conectou, ou clique em "Start" no CodeLens que aparece acima do bloco no
próprio `mcp.json`.

### Zed

No `settings.json` do Zed (Command Palette → "zed: open settings"):

```json
{
  "context_servers": {
    "jira": {
      "command": {
        "path": "/caminho/absoluto/para/jira-mcp",
        "args": [],
        "env": {
          "JIRA_BASE_URL": "https://suaempresa.atlassian.net",
          "JIRA_EMAIL": "voce@suaempresa.com",
          "JIRA_API_TOKEN": "seu-token-de-api"
        }
      }
    }
  }
}
```

(Zed vem mudando o nome dessa chave entre versões — se `context_servers` não
funcionar, procure por `experimental.context_servers` nas configurações da
sua versão.)

### OpenAI Codex CLI

No `~/.codex/config.toml`:

```toml
[mcp_servers.jira]
command = "/caminho/absoluto/para/jira-mcp"
args = []

[mcp_servers.jira.env]
JIRA_BASE_URL = "https://suaempresa.atlassian.net"
JIRA_EMAIL = "voce@suaempresa.com"
JIRA_API_TOKEN = "seu-token-de-api"
```

### opencode

No `opencode.json` (raiz do projeto ou `~/.config/opencode/opencode.json`
para global):

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "jira": {
      "type": "local",
      "command": ["/caminho/absoluto/para/jira-mcp"],
      "environment": {
        "JIRA_BASE_URL": "https://suaempresa.atlassian.net",
        "JIRA_EMAIL": "voce@suaempresa.com",
        "JIRA_API_TOKEN": "seu-token-de-api"
      },
      "enabled": true
    }
  }
}
```

> Esses formatos (nome de chave, `stdio` vs `local`, `env` vs `environment`)
> mudam com alguma frequência entre versões dessas ferramentas. Se algo não
> conectar, vale checar a doc oficial mais recente de cada uma — a mecânica
> de fundo (rodar `/caminho/absoluto/para/jira-mcp` como subprocesso e falar
> JSON-RPC por stdio) é sempre a mesma.

## Como funciona por baixo dos panos

- Transporte: JSON-RPC 2.0 delimitado por linha sobre stdin/stdout (o
  transporte "stdio" padrão do MCP). Todo log de diagnóstico vai para stderr,
  nunca para stdout, para não corromper o protocolo.
- `internal/mcp`: implementação mínima do protocolo (handshake `initialize`,
  `tools/list`, `tools/call`) sem depender de nenhum SDK externo.
- `internal/jira`: cliente REST do Jira (autenticação, busca, CRUD de issues,
  transições, comentários, projetos).
- `internal/tools`: liga cada tool MCP a uma chamada do cliente Jira e formata
  a resposta em texto legível para o modelo.

## Estrutura do projeto

```
jira-mcp/
├── go.mod
├── main.go                       # carrega config e inicia o servidor
├── internal/
│   ├── mcp/
│   │   ├── types.go               # tipos JSON-RPC / MCP
│   │   └── server.go              # loop stdio + dispatch
│   ├── jira/
│   │   └── client.go              # cliente REST do Jira
│   └── tools/
│       └── tools.go                # definição e handlers das tools
└── README.md
```

## Extensão

Para adicionar uma nova operação do Jira:

1. Adicione o método correspondente em `internal/jira/client.go`.
2. Registre uma nova tool (schema + handler) em `internal/tools/tools.go`,
   dentro da função `Register`.

Nenhuma outra mudança é necessária — o servidor MCP genérico em
`internal/mcp` não precisa saber nada sobre Jira.

## Limitações conhecidas

- **Vídeo não é analisável pela IA via MCP.** O protocolo só embute imagens
  como conteúdo binário; vídeos anexados ao Jira só podem ser referenciados
  por link de download, não "vistos" pelo modelo na conversa.
- **Boards/sprints exigem Jira Software** habilitado no projeto — projetos só
  de "Business"/Service Management não têm boards Agile.
- Sem testes automatizados nem CI configurados.
- Não há suporte a Webhooks (push de eventos do Jira) — todas as tools são
  *pull*, chamadas sob demanda pelo modelo.
- Paginação: `jira_search` (Cloud) usa `page_token`; as tools de board/sprint
  retornam só a primeira página (até `max_results`), sem paginação encadeada.
