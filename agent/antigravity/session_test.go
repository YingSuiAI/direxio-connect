package antigravity

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YingSuiAI/dirextalk-connect/core"
)

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"cc-connect", "cc-connect"},
		{"Daily", "daily"},
		{"My Project", "my-project"},
		{"hello_world", "hello-world"},
		{"Test.123", "test-123"},
		{"---weird---", "weird"},
		{"", "project"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := slugify(tt.input)
			if got != tt.want {
				t.Errorf("slugify(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeMode(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"default", "default"},
		{"yolo", "yolo"},
		{"auto", "yolo"},
		{"force", "yolo"},
		{"plan", "plan"},
		{"sandbox", "plan"},
		{"invalid", "default"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeMode(tt.input)
			if got != tt.want {
				t.Errorf("normalizeMode(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSession_ContinueSessionTreatedAsFresh(t *testing.T) {
	s, err := newAntigravitySession(context.Background(), "echo", nil, "/tmp", "", "default", core.ContinueSession, nil, 0, core.MCPConfig{})
	if err != nil {
		t.Fatalf("newAntigravitySession: %v", err)
	}
	defer func() { _ = s.Close() }()

	if got := s.CurrentSessionID(); got != "" {
		t.Errorf("ContinueSession should be treated as fresh: chatID = %q, want empty", got)
	}
}

func TestBuildAntigravityArgs_PromptAtEnd(t *testing.T) {
	s, _ := newAntigravitySession(context.Background(), "echo", nil, "/tmp", "", "default", "", nil, 0, core.MCPConfig{})
	args := s.buildAntigravityArgs("sid-1", true, "plan", "What is 1+1?")
	if len(args) < 2 {
		t.Fatalf("args too short: %v", args)
	}
	if args[len(args)-2] != "-p" || args[len(args)-1] != "What is 1+1?" {
		t.Fatalf("expected prompt to be final '-p <prompt>', got: %v", args)
	}
	if !contains(args, "--sandbox") {
		t.Fatalf("expected --sandbox in args, got: %v", args)
	}
	if contains(args, "-m") || contains(args, "--model") {
		t.Fatalf("did not expect model flags in args, got: %v", args)
	}
}

func TestUsesInteractivePermission(t *testing.T) {
	if !usesInteractivePermission("default") {
		t.Fatal("default mode should use interactive permission stdin")
	}
	if usesInteractivePermission("yolo") {
		t.Fatal("yolo mode should not use interactive permission stdin")
	}
	if usesInteractivePermission("plan") {
		t.Fatal("plan mode should not use interactive permission stdin")
	}
}

func TestRespondPermission_WritesTerminalAnswer(t *testing.T) {
	s, err := newAntigravitySession(context.Background(), "echo", nil, "/tmp", "", "default", "", nil, 0, core.MCPConfig{})
	if err != nil {
		t.Fatalf("newAntigravitySession: %v", err)
	}
	defer func() { _ = s.Close() }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()
	s.stdin = w

	s.permReqID.Store("req")
	if err := s.RespondPermission("req", core.PermissionResult{Behavior: "allow"}); err != nil {
		t.Fatalf("RespondPermission allow: %v", err)
	}
	buf := make([]byte, 8)
	n, err := r.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("read allow response: %v", err)
	}
	if got := string(buf[:n]); got != "y\n" {
		t.Fatalf("allow response = %q, want %q", got, "y\n")
	}

	s.permReqID.Store("req")
	if err := s.RespondPermission("req", core.PermissionResult{Behavior: "deny"}); err != nil {
		t.Fatalf("RespondPermission deny: %v", err)
	}
	n, err = r.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("read deny response: %v", err)
	}
	if got := string(buf[:n]); got != "n\n" {
		t.Fatalf("deny response = %q, want %q", got, "n\n")
	}
}

func TestEnsureAntigravityMCPConfigWritesGlobalConfigs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := core.MCPConfigFromAgentToken("Dirextalk.D1", "https://d1.dirextalk.ai/mcp", "agent-token", "node-1")
	if err := ensureAntigravityMCPConfig(cfg); err != nil {
		t.Fatalf("ensureAntigravityMCPConfig: %v", err)
	}

	for _, path := range antigravityMCPConfigPaths(home) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", path, err)
		}

		var parsed struct {
			MCPServers map[string]struct {
				ServerURL string            `json:"serverUrl"`
				Headers   map[string]string `json:"headers"`
				Type      string            `json:"type"`
				URL       string            `json:"url"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Fatalf("Unmarshal(%s): %v", path, err)
		}

		server := parsed.MCPServers["dirextalk_d1"]
		if server.ServerURL != "https://d1.dirextalk.ai/mcp" {
			t.Fatalf("%s serverUrl = %q", path, server.ServerURL)
		}
		if server.Headers["Authorization"] != "Bearer agent-token" {
			t.Fatalf("%s Authorization header = %q", path, server.Headers["Authorization"])
		}
		if server.Headers["DIREXTALK-Agent-Node-Id"] != "node-1" {
			t.Fatalf("%s node header = %q", path, server.Headers["DIREXTALK-Agent-Node-Id"])
		}
		if server.Type != "" || server.URL != "" {
			t.Fatalf("%s should not write legacy type/url fields: %#v", path, server)
		}
	}
}

func TestEnsureAntigravityMCPConfigPreservesExistingServers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	existingPath := filepath.Join(home, ".gemini", "config", "mcp_config.json")
	if err := os.MkdirAll(filepath.Dir(existingPath), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(existingPath, []byte(`{"mcpServers":{"existing":{"serverUrl":"https://example.com/mcp"}},"other":true}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := core.MCPConfigFromAgentToken("dirextalk-d1", "https://d1.dirextalk.ai/mcp", "agent-token", "")
	if err := ensureAntigravityMCPConfig(cfg); err != nil {
		t.Fatalf("ensureAntigravityMCPConfig: %v", err)
	}

	data, err := os.ReadFile(existingPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed["other"] != true {
		t.Fatalf("existing top-level keys not preserved: %#v", parsed)
	}
	servers, ok := parsed["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers type = %T", parsed["mcpServers"])
	}
	if _, ok := servers["existing"]; !ok {
		t.Fatalf("existing server not preserved: %#v", servers)
	}
	if _, ok := servers["dirextalk-d1"]; !ok {
		t.Fatalf("dirextalk server missing: %#v", servers)
	}
}

func TestExtractPermissionPrompt(t *testing.T) {
	text := "Tool wants to run command. Allow this action? (y/N)"
	got, ok := extractPermissionPrompt(text)
	if !ok {
		t.Fatalf("expected permission prompt to be detected")
	}
	if got == "" {
		t.Fatalf("detected prompt should not be empty")
	}
}

func TestExtractPermissionPrompt_SplitChunksDetectedInWindow(t *testing.T) {
	part1 := "Tool wants to run command. Allow this"
	part2 := " action? (y/N)"
	got, ok := extractPermissionPrompt(part1 + part2)
	if !ok || got == "" {
		t.Fatalf("expected split prompt to be detected, got ok=%v prompt=%q", ok, got)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if strings.TrimSpace(x) == want {
			return true
		}
	}
	return false
}
