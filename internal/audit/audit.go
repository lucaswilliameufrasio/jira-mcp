// Package audit stores metadata about MCP tool execution without payloads.
package audit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"jira-mcp/internal/mcp"
)

const DefaultRetention = 30 * 24 * time.Hour
const MaxRetention = 365 * 24 * time.Hour

// Store scopes audit records to a caller-supplied identity, not a request body.
type Store interface {
	Append(context.Context, string, mcp.ToolEvent) error
	List(context.Context, string, int) ([]mcp.ToolEvent, error)
}

func ParseRetention(value string) (time.Duration, error) {
	if value == "" {
		return DefaultRetention, nil
	}
	retention, err := time.ParseDuration(value)
	if err != nil || retention < time.Second || retention > MaxRetention {
		return 0, errors.New("JIRA_MCP_AUDIT_RETENTION must be a duration between 1s and 8760h")
	}
	return retention, nil
}

func DefaultDirectory() (string, error) {
	if root := os.Getenv("XDG_STATE_HOME"); root != "" {
		if !filepath.IsAbs(root) {
			return "", errors.New("XDG_STATE_HOME must be absolute")
		}
		return filepath.Join(root, "jira-mcp", "audit"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "jira-mcp", "audit"), nil
}
