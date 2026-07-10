package mcp_contract_test

import (
	"strings"
	"testing"

	_ "github.com/YingSuiAI/dirextalk-connect/agent/acp"
	_ "github.com/YingSuiAI/dirextalk-connect/agent/antigravity"
	_ "github.com/YingSuiAI/dirextalk-connect/agent/claudecode"
	_ "github.com/YingSuiAI/dirextalk-connect/agent/codex"
	_ "github.com/YingSuiAI/dirextalk-connect/agent/copilot"
	_ "github.com/YingSuiAI/dirextalk-connect/agent/cursor"
	_ "github.com/YingSuiAI/dirextalk-connect/agent/devin"
	_ "github.com/YingSuiAI/dirextalk-connect/agent/gemini"
	_ "github.com/YingSuiAI/dirextalk-connect/agent/iflow"
	_ "github.com/YingSuiAI/dirextalk-connect/agent/kimi"
	_ "github.com/YingSuiAI/dirextalk-connect/agent/opencode"
	_ "github.com/YingSuiAI/dirextalk-connect/agent/pi"
	_ "github.com/YingSuiAI/dirextalk-connect/agent/qoder"
	_ "github.com/YingSuiAI/dirextalk-connect/agent/reasonix"
	_ "github.com/YingSuiAI/dirextalk-connect/agent/tmux"
	"github.com/YingSuiAI/dirextalk-connect/core"
)

func TestEveryRegisteredBackendHasExplicitMCPCapability(t *testing.T) {
	want := map[string]core.MCPCapabilityKind{
		"acp":         core.MCPCapabilitySession,
		"antigravity": core.MCPCapabilityHostManaged,
		"claudecode":  core.MCPCapabilitySession,
		"codex":       core.MCPCapabilitySession,
		"copilot":     core.MCPCapabilitySession,
		"cursor":      core.MCPCapabilityHostManaged,
		"devin":       core.MCPCapabilityUnsupported,
		"gemini":      core.MCPCapabilitySession,
		"iflow":       core.MCPCapabilityHostManaged,
		"kimi":        core.MCPCapabilitySession,
		"opencode":    core.MCPCapabilitySession,
		"pi":          core.MCPCapabilityUnsupported,
		"qoder":       core.MCPCapabilitySession,
		"reasonix":    core.MCPCapabilityUnsupported,
		"tmux":        core.MCPCapabilityUnsupported,
	}

	registered := core.ListRegisteredAgents()
	if len(registered) != len(want) {
		t.Fatalf("registered backends = %v, want exactly %d production backends", registered, len(want))
	}
	for name, wantKind := range want {
		capability, ok := core.GetAgentMCPCapability(name)
		if !ok {
			t.Fatalf("backend %q has no MCP capability entry", name)
		}
		if capability.Kind != wantKind {
			t.Errorf("backend %q MCP capability = %q, want %q", name, capability.Kind, wantKind)
		}
		if strings.TrimSpace(capability.Reason) == "" {
			t.Errorf("backend %q has no actionable MCP capability reason", name)
		}
	}
}
