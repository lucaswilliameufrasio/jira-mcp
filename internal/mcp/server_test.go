package mcp

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTextHandlerReturnsTextContent(t *testing.T) {
	content, err := TextHandler(func(arguments json.RawMessage) (string, error) { return string(arguments), nil })(json.RawMessage(`{"ok":true}`))
	if err != nil || len(content) != 1 || content[0].Type != "text" {
		t.Fatalf("content = %#v, err = %v", content, err)
	}
}

func TestConvertContentDecodesImages(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("image"))
	content, err := convertContent(ContentBlock{Type: "image", Data: encoded, MimeType: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	image, ok := content.(*sdk.ImageContent)
	if !ok || string(image.Data) != "image" || image.MIMEType != "image/png" {
		t.Fatalf("content = %#v", content)
	}
}

func TestConvertContentRejectsUnknownType(t *testing.T) {
	if _, err := convertContent(ContentBlock{Type: "audio"}); err == nil {
		t.Fatal("expected unsupported content error")
	}
}
