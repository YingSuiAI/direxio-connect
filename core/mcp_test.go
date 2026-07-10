package core

import (
	"context"
	"strings"
	"testing"
)

func TestParseMCPConfig_FromAgentToken(t *testing.T) {
	cfg := ParseMCPConfig(map[string]any{
		"mcp_server_name": "Dirextalk.D1",
		"mcp_url":         "https://d1.dirextalk.ai/mcp",
		"mcp_agent_token": "agent-token",
		"mcp_node_id":     "codex-d1",
	})

	if !cfg.Enabled() {
		t.Fatal("MCP config should be enabled")
	}
	if cfg.ServerName != "dirextalk_d1" {
		t.Fatalf("ServerName = %q", cfg.ServerName)
	}
	if cfg.Authorization != "Bearer agent-token" {
		t.Fatalf("Authorization = %q", cfg.Authorization)
	}
	if cfg.NodeID != "codex-d1" {
		t.Fatalf("NodeID = %q", cfg.NodeID)
	}
}

func TestValidateMCPOptions_RejectsPartialConfiguration(t *testing.T) {
	tests := []struct {
		name string
		opts map[string]any
		want []string
	}{
		{
			name: "url only",
			opts: map[string]any{"mcp_url": "https://d1.dirextalk.ai/mcp"},
			want: []string{"mcp_server_name", "mcp_agent_token"},
		},
		{
			name: "token only",
			opts: map[string]any{"mcp_agent_token": "agent-token"},
			want: []string{"mcp_server_name", "mcp_url"},
		},
		{
			name: "explicit enable without fields",
			opts: map[string]any{"mcp_enabled": true},
			want: []string{"mcp_server_name", "mcp_url", "mcp_agent_token"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMCPOptions(tt.opts)
			if err == nil {
				t.Fatal("ValidateMCPOptions() = nil, want partial-config error")
			}
			for _, fragment := range tt.want {
				if !strings.Contains(err.Error(), fragment) {
					t.Fatalf("ValidateMCPOptions() = %q, want mention of %q", err, fragment)
				}
			}
		})
	}
}

func TestValidateMCPOptions_AllowsAbsentOrExplicitlyDisabledConfiguration(t *testing.T) {
	for _, opts := range []map[string]any{
		nil,
		{},
		{"mcp_enabled": false, "mcp_url": "https://ignored.example/mcp"},
	} {
		if err := ValidateMCPOptions(opts); err != nil {
			t.Fatalf("ValidateMCPOptions(%#v) = %v", opts, err)
		}
	}
}

func TestValidateMCPOptions_RequiresCanonicalHTTPSAndBearerShapes(t *testing.T) {
	valid := []map[string]any{
		{
			"mcp_server_name":   "dirextalk-d1",
			"mcp_url":           "https://d1.dirextalk.ai/mcp",
			"mcp_authorization": "Bearer agent-token",
		},
		{
			"mcp_server_name": "dirextalk-d1",
			"mcp_domain":      "d1.dirextalk.ai",
			"mcp_agent_token": "agent-token",
		},
	}
	for i, opts := range valid {
		if err := ValidateMCPOptions(opts); err != nil {
			t.Fatalf("valid case %d: %v", i, err)
		}
	}

	tests := []struct {
		name          string
		url           string
		authorization string
	}{
		{name: "relative URL", url: "/mcp", authorization: "Bearer token"},
		{name: "HTTP URL", url: "http://example.com/mcp", authorization: "Bearer token"},
		{name: "empty host", url: "https:///mcp", authorization: "Bearer token"},
		{name: "port without host", url: "https://:443/mcp", authorization: "Bearer token"},
		{name: "userinfo", url: "https://user@example.com/mcp", authorization: "Bearer token"},
		{name: "wrong path", url: "https://example.com/other", authorization: "Bearer token"},
		{name: "query", url: "https://example.com/mcp?token=hidden", authorization: "Bearer token"},
		{name: "fragment", url: "https://example.com/mcp#hidden", authorization: "Bearer token"},
		{name: "basic authorization", url: "https://example.com/mcp", authorization: "Basic super-secret-token"},
		{name: "empty bearer", url: "https://example.com/mcp", authorization: "Bearer "},
		{name: "whitespace in token", url: "https://example.com/mcp", authorization: "Bearer super-secret-token extra"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMCPOptions(map[string]any{
				"mcp_server_name":   "dirextalk-d1",
				"mcp_url":           tt.url,
				"mcp_authorization": tt.authorization,
			})
			if err == nil {
				t.Fatal("ValidateMCPOptions() = nil, want malformed canonical MCP error")
			}
			if strings.Contains(err.Error(), "super-secret-token") || strings.Contains(err.Error(), "token=hidden") {
				t.Fatalf("validation error leaked a credential: %q", err)
			}
		})
	}
}

func TestWithMCPEnvOptions_MergesEnv(t *testing.T) {
	opts := WithMCPEnvOptions(map[string]any{
		"mcp_server_name": "dirextalk-d1",
		"mcp_domain":      "d1.dirextalk.ai",
		"mcp_agent_token": "agent-token",
		"mcp_node_id":     "node-1",
		"env": map[string]any{
			"EXISTING": "keep",
		},
	})

	env, ok := opts["env"].(map[string]any)
	if !ok {
		t.Fatalf("env type = %T", opts["env"])
	}
	if env["EXISTING"] != "keep" {
		t.Fatalf("EXISTING env lost: %#v", env)
	}
	if env["DIREXTALK_MCP_URL"] != "https://d1.dirextalk.ai/mcp" {
		t.Fatalf("DIREXTALK_MCP_URL = %#v", env["DIREXTALK_MCP_URL"])
	}
	if env["DIREXTALK_MCP_AGENT_TOKEN"] != "agent-token" {
		t.Fatalf("DIREXTALK_MCP_AGENT_TOKEN missing")
	}
	if env["DIREXTALK_MCP_NODE_ID"] != "node-1" {
		t.Fatalf("DIREXTALK_MCP_NODE_ID = %#v", env["DIREXTALK_MCP_NODE_ID"])
	}
}

func TestMCPConfig_ACPServers(t *testing.T) {
	cfg := MCPConfigFromAgentToken("dirextalk-d1", "https://d1.dirextalk.ai/mcp", "agent-token", "node-1")
	servers := cfg.ACPServers()
	if len(servers) != 1 {
		t.Fatalf("servers len = %d", len(servers))
	}
	server, ok := servers[0].(map[string]any)
	if !ok {
		t.Fatalf("server type = %T", servers[0])
	}
	if server["name"] != "dirextalk-d1" || server["url"] != "https://d1.dirextalk.ai/mcp" {
		t.Fatalf("server = %#v", server)
	}
	if server["type"] != "http" {
		t.Fatalf("type = %#v, want http", server["type"])
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
}

func TestMCPConfig_MCPServersConfig(t *testing.T) {
	cfg := MCPConfigFromAgentToken("Dirextalk.D1", "https://d1.dirextalk.ai/mcp", "agent-token", "node-1")
	got := cfg.MCPServersConfig()
	servers, ok := got["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers type = %T", got["mcpServers"])
	}
	server, ok := servers["dirextalk_d1"].(map[string]any)
	if !ok {
		t.Fatalf("server missing: %#v", servers)
	}
	if server["type"] != "http" || server["url"] != "https://d1.dirextalk.ai/mcp" {
		t.Fatalf("server = %#v", server)
	}
	headers, ok := server["headers"].(map[string]string)
	if !ok {
		t.Fatalf("headers type = %T", server["headers"])
	}
	if headers["Authorization"] != "Bearer agent-token" {
		t.Fatalf("Authorization = %q", headers["Authorization"])
	}
	if headers["DIREXTALK-Agent-Node-Id"] != "node-1" {
		t.Fatalf("node header = %q", headers["DIREXTALK-Agent-Node-Id"])
	}
}

func TestCreateAgent_AddsMCPEnvForDeclaredConditionalFactory(t *testing.T) {
	agentName := "mcp-test-agent"
	var captured map[string]any
	RegisterAgent(agentName, func(opts map[string]any) (Agent, error) {
		captured = opts
		return stubMCPAgent{}, nil
	}, MCPBackendCapability{Kind: MCPCapabilityConditional, ConditionalOption: "mcp_wrapper", Reason: "test wrapper"})

	_, err := CreateAgent(agentName, map[string]any{
		"mcp_server_name": "dirextalk-d1",
		"mcp_url":         "https://d1.dirextalk.ai/mcp",
		"mcp_agent_token": "agent-token",
		"mcp_wrapper":     true,
	})
	if err != nil {
		t.Fatalf("CreateAgent() error = %v", err)
	}
	env, ok := captured["env"].(map[string]any)
	if !ok {
		t.Fatalf("env type = %T", captured["env"])
	}
	if env["DIREXTALK_MCP_SERVER_NAME"] != "dirextalk-d1" {
		t.Fatalf("MCP env not injected: %#v", env)
	}
}

type stubMCPAgent struct{}

func (stubMCPAgent) Name() string { return "mcp-test-agent" }
func (stubMCPAgent) StartSession(context.Context, string) (AgentSession, error) {
	return nil, nil
}
func (stubMCPAgent) ListSessions(context.Context) ([]AgentSessionInfo, error) {
	return nil, nil
}
func (stubMCPAgent) Stop() error { return nil }
