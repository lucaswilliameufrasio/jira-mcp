package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolHandler keeps the existing Jira handlers independent from the transport
// implementation. The MCP wire protocol is handled by the official SDK.
type ToolHandler func(arguments json.RawMessage) ([]ContentBlock, error)

type ContextHandler func(context.Context, json.RawMessage) ([]ContentBlock, error)

func TextHandler(fn func(arguments json.RawMessage) (string, error)) ToolHandler {
	return func(arguments json.RawMessage) ([]ContentBlock, error) {
		text, err := fn(arguments)
		if err != nil {
			return nil, err
		}
		return []ContentBlock{{Type: "text", Text: text}}, nil
	}
}

type Server struct {
	inner    *sdk.Server
	logger   *log.Logger
	enabled  map[string]bool
	observer func(ToolEvent)
}

// ToolEvent intentionally excludes arguments, results and error messages.
type ToolEvent struct {
	Category  string        `json:"category,omitempty"`
	Tool      string        `json:"tool"`
	StartedAt time.Time     `json:"started_at"`
	Duration  time.Duration `json:"duration_ns"`
	Success   bool          `json:"success"`
}

// SetObserver configures a metadata-only observer before tools are registered.
func (s *Server) SetObserver(observer func(ToolEvent)) {
	s.observer = observer
}

func NewServer(name, version string) *Server {
	return &Server{
		inner:  sdk.NewServer(&sdk.Implementation{Name: name, Version: version}, nil),
		logger: log.New(os.Stderr, "[jira-mcp] ", log.LstdFlags),
	}
}

func NewServerWithTools(name, version string, enabled map[string]bool) *Server {
	server := NewServer(name, version)
	server.enabled = enabled
	return server
}

func (s *Server) RegisterTool(def Tool, handler ContextHandler) {
	if s.enabled != nil && !s.enabled[def.Name] {
		return
	}
	s.inner.AddTool(&sdk.Tool{
		Name:        def.Name,
		Description: def.Description,
		InputSchema: InputSchema{Type: def.InputSchema.Type, Properties: def.InputSchema.Properties, Required: def.InputSchema.Required},
	}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		started := time.Now().UTC()
		success := false
		category := ""
		defer func() {
			if s.observer != nil {
				s.observer(ToolEvent{Tool: def.Name, StartedAt: started, Duration: time.Since(started), Success: success, Category: category})
			}
		}()
		content, err := handler(ctx, req.Params.Arguments)
		if err != nil {
			category = failureCategory(err)
			return &sdk.CallToolResult{
				Content: []sdk.Content{&sdk.TextContent{Text: err.Error()}},
				IsError: true,
			}, nil
		}

		var structured any
		converted := make([]sdk.Content, 0, len(content))
		for _, block := range content {
			if block.Structured != nil {
				structured = block.Structured
			}
			convertedBlock, convertErr := convertContent(block)
			if convertErr != nil {
				return nil, convertErr
			}
			converted = append(converted, convertedBlock)
		}
		success = true
		return &sdk.CallToolResult{Content: converted, StructuredContent: structured}, nil
	})
}

func convertContent(block ContentBlock) (sdk.Content, error) {
	switch block.Type {
	case "text":
		return &sdk.TextContent{Text: block.Text}, nil
	case "image":
		data, err := base64.StdEncoding.DecodeString(block.Data)
		if err != nil {
			return nil, fmt.Errorf("invalid image content: %w", err)
		}
		return &sdk.ImageContent{Data: data, MIMEType: block.MimeType}, nil
	case "resource":
		return &sdk.EmbeddedResource{Resource: &sdk.ResourceContents{
			URI: block.URI, MIMEType: block.MimeType, Blob: block.Blob,
		}}, nil
	default:
		return nil, fmt.Errorf("unsupported MCP content type %q", block.Type)
	}
}

func (s *Server) Run(ctx context.Context) error {
	return s.inner.Run(ctx, &sdk.StdioTransport{})
}

func NewHTTPHandler(factory func(*http.Request) *Server) http.Handler {
	return sdk.NewStreamableHTTPHandler(func(req *http.Request) *sdk.Server {
		server := factory(req)
		if server == nil {
			return nil
		}
		return server.inner
	}, &sdk.StreamableHTTPOptions{
		// 15 MiB upload encoded as Base64, plus room for the JSON-RPC envelope.
		MaxRequestBodyBytes: 21 << 20,
	})
}

func (s *Server) Logf(format string, args ...interface{}) {
	s.logger.Printf(format, args...)
}

func failureCategory(err error) string {
	var status interface{ HTTPStatus() int }
	if errors.As(err, &status) {
		switch code := status.HTTPStatus(); {
		case code == 401 || code == 403:
			return "permissão"
		case code == 429:
			return "limite de requisições"
		case code >= 500:
			return "Jira indisponível"
		}
	}
	var networkError net.Error
	if errors.As(err, &networkError) && !errors.Is(err, context.Canceled) {
		return "Jira indisponível"
	}
	return "outra"
}
