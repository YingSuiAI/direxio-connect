package acp

import (
	"testing"

	"github.com/YingSuiAI/dirextalk-connect/core"
)

func TestNew_DisplayNameDefault(t *testing.T) {
	a, err := New(map[string]any{"command": "go"})
	if err != nil {
		t.Fatal(err)
	}
	agent := a.(*Agent)
	if got := agent.CLIDisplayName(); got != "ACP" {
		t.Fatalf("CLIDisplayName = %q, want ACP", got)
	}
}

func TestNew_DisplayNameCustom(t *testing.T) {
	a, err := New(map[string]any{
		"command":      "go",
		"display_name": "Copilot ACP",
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := a.(*Agent)
	if got := agent.CLIDisplayName(); got != "Copilot ACP" {
		t.Fatalf("CLIDisplayName = %q, want Copilot ACP", got)
	}
}

func TestNew_ParsesMCPConfig(t *testing.T) {
	a, err := New(map[string]any{
		"command":         "go",
		"mcp_server_name": "dirextalk-d1",
		"mcp_url":         "https://d1.dirextalk.ai/mcp",
		"mcp_agent_token": "agent-token",
		"mcp_node_id":     "node-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := a.(*Agent)
	if !agent.mcpConfig.Enabled() {
		t.Fatal("MCP config should be enabled")
	}
	params := acpSessionParams("/tmp/work", agent.mcpConfig)
	servers, _ := params["mcpServers"].([]any)
	if len(servers) != 1 {
		t.Fatalf("mcpServers = %#v", params["mcpServers"])
	}
	snakeServers, _ := params["mcp_servers"].([]any)
	if len(snakeServers) != 1 {
		t.Fatalf("mcp_servers = %#v", params["mcp_servers"])
	}
	server := servers[0].(map[string]any)
	if server["name"] != "dirextalk-d1" || server["url"] != "https://d1.dirextalk.ai/mcp" {
		t.Fatalf("server = %#v", server)
	}
	if server["type"] != "http" {
		t.Fatalf("server type = %#v, want http", server["type"])
	}
	if _, ok := server["command"]; ok {
		t.Fatalf("http MCP server must not include stdio command: %#v", server)
	}
	if _, ok := server["args"]; ok {
		t.Fatalf("http MCP server must not include stdio args: %#v", server)
	}
	if _, ok := server["env"]; ok {
		t.Fatalf("http MCP server must not include stdio env: %#v", server)
	}
	headers, ok := server["headers"].([]map[string]string)
	if !ok {
		t.Fatalf("headers type = %T", server["headers"])
	}
	gotHeaders := map[string]string{}
	for _, header := range headers {
		gotHeaders[header["name"]] = header["value"]
	}
	if gotHeaders["Authorization"] != "Bearer agent-token" {
		t.Fatalf("Authorization header = %q", gotHeaders["Authorization"])
	}
	if gotHeaders["DIREXTALK-Agent-Node-Id"] != "node-1" {
		t.Fatalf("node header = %q", gotHeaders["DIREXTALK-Agent-Node-Id"])
	}
	snakeServer := snakeServers[0].(map[string]any)
	if snakeServer["name"] != server["name"] || snakeServer["url"] != server["url"] || snakeServer["type"] != server["type"] {
		t.Fatalf("mcp_servers[0] = %#v, want same as mcpServers[0] %#v", snakeServer, server)
	}
}

func TestWorkspaceAgentOptions(t *testing.T) {
	a, err := New(map[string]any{
		"command":      "go",
		"args":         []any{"--acp", "--stdio"},
		"env":          map[string]any{"FOO": "bar", "COPILOT_VALUE": "a=b"},
		"auth_method":  "cursor_login",
		"display_name": "Copilot ACP",
	})
	if err != nil {
		t.Fatal(err)
	}

	agent := a.(*Agent)
	agent.SetSessionEnv([]string{"SESSION_ONLY=1"})

	snapshotter, ok := a.(core.WorkspaceAgentOptionSnapshotter)
	if !ok {
		t.Fatalf("agent does not implement WorkspaceAgentOptionSnapshotter")
	}
	opts := snapshotter.WorkspaceAgentOptions()

	if got, _ := opts["cmd"].(string); got != "go" {
		t.Fatalf("cmd = %q, want go", got)
	}
	gotArgs, _ := opts["args"].([]string)
	if len(gotArgs) != 2 || gotArgs[0] != "--acp" || gotArgs[1] != "--stdio" {
		t.Fatalf("args = %#v, want [--acp --stdio]", gotArgs)
	}
	gotEnv, _ := opts["env"].(map[string]string)
	if len(gotEnv) != 2 || gotEnv["FOO"] != "bar" || gotEnv["COPILOT_VALUE"] != "a=b" {
		t.Fatalf("env = %#v, want config env only", gotEnv)
	}
	if got, _ := opts["auth_method"].(string); got != "cursor_login" {
		t.Fatalf("auth_method = %q, want cursor_login", got)
	}
	if got, _ := opts["display_name"].(string); got != "Copilot ACP" {
		t.Fatalf("display_name = %q, want Copilot ACP", got)
	}
}
