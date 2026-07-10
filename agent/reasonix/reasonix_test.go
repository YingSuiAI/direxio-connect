package reasonix

import (
	"strings"
	"testing"
)

func TestNewRejectsRemoteMCPConfiguration(t *testing.T) {
	_, err := New(map[string]any{
		"serve_url":       "https://reasonix.example",
		"work_dir":        t.TempDir(),
		"mcp_server_name": "dirextalk-d1",
		"mcp_url":         "https://d1.dirextalk.ai/mcp",
		"mcp_agent_token": "agent-token",
	})
	if err == nil || !strings.Contains(err.Error(), "remote serve_url cannot consume") {
		t.Fatalf("New() error = %v, want remote MCP unsupported error", err)
	}
}
