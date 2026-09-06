package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolHandler keeps the existing Jira handlers independent from the transport
// implementation. The MCP wire protocol is handled by the official SDK.
type ToolHandler func(arguments json.RawMessage) ([]ContentBlock, error)

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
	inner   *sdk.Server
	logger  *log.Logger
	enabled map[string]bool
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

func (s *Server) RegisterTool(def Tool, handler ToolHandler) {
	if s.enabled != nil && !s.enabled[def.Name] {
		return
	}
	s.inner.AddTool(&sdk.Tool{
		Name:        def.Name,
		Description: def.Description,
		InputSchema: InputSchema{Type: def.InputSchema.Type, Properties: def.InputSchema.Properties, Required: def.InputSchema.Required},
	}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		content, err := handler(req.Params.Arguments)
		if err != nil {
			return &sdk.CallToolResult{
				Content: []sdk.Content{&sdk.TextContent{Text: err.Error()}},
				IsError: true,
			}, nil
		}

		converted := make([]sdk.Content, 0, len(content))
		for _, block := range content {
			convertedBlock, convertErr := convertContent(block)
			if convertErr != nil {
				return nil, convertErr
			}
			converted = append(converted, convertedBlock)
		}
		return &sdk.CallToolResult{Content: converted}, nil
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
	}, nil)
}

func (s *Server) Logf(format string, args ...interface{}) {
	s.logger.Printf(format, args...)
}
