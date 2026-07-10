package kimi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YingSuiAI/dirextalk-connect/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewKimiSession(t *testing.T) {
	ctx := context.Background()
	ks, err := newKimiSession(ctx, "kimi", nil, "/tmp", "kimi-k2", "default", "resume-123", nil, core.MCPConfig{}, 0)
	require.NoError(t, err)
	require.NotNil(t, ks)
	assert.True(t, ks.Alive())
	assert.Equal(t, "resume-123", ks.CurrentSessionID())

	err = ks.Close()
	assert.NoError(t, err)
	assert.False(t, ks.Alive())
}

func TestExtractResumeSessionID(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"To resume this session: kimi -r e3690555-60eb-4d50-874b-e3647e9cee5b", "e3690555-60eb-4d50-874b-e3647e9cee5b"},
		{"To resume this session: kimi --resume abc-def", ""},
		{"To resume this session: no-id-here", ""},
		{"random text", ""},
	}

	for _, c := range cases {
		assert.Equal(t, c.expected, extractResumeSessionID(c.input), "input: %s", c.input)
	}
}

func TestHandleAssistantWithText(t *testing.T) {
	ctx := context.Background()
	ks, _ := newKimiSession(ctx, "kimi", nil, "/tmp", "", "default", "", nil, core.MCPConfig{}, 0)
	defer ks.Close()

	ks.handleEvent(map[string]any{
		"role": "assistant",
		"content": []any{
			map[string]any{"type": "text", "text": "Hello!"},
		},
	})

	// pendingMsgs should buffer the text
	assert.Len(t, ks.pendingMsgs, 1)
	assert.Equal(t, "Hello!", ks.pendingMsgs[0])
}

func TestHandleAssistantWithThink(t *testing.T) {
	ctx := context.Background()
	ks, _ := newKimiSession(ctx, "kimi", nil, "/tmp", "", "default", "", nil, core.MCPConfig{}, 0)
	defer ks.Close()

	ks.handleEvent(map[string]any{
		"role": "assistant",
		"content": []any{
			map[string]any{"type": "think", "think": "Let me think..."},
			map[string]any{"type": "text", "text": "Done!"},
		},
	})

	events := drainEvents(ks.events, 2)
	require.Len(t, events, 1)
	assert.Equal(t, core.EventThinking, events[0].Type)
	assert.Equal(t, "Let me think...", events[0].Content)
	assert.Equal(t, "Done!", ks.pendingMsgs[0])
}

func TestHandleAssistantWithToolCalls(t *testing.T) {
	ctx := context.Background()
	ks, _ := newKimiSession(ctx, "kimi", nil, "/tmp", "", "default", "", nil, core.MCPConfig{}, 0)
	defer ks.Close()

	ks.handleEvent(map[string]any{
		"role": "assistant",
		"content": []any{
			map[string]any{"type": "text", "text": "I will run a command"},
		},
		"tool_calls": []any{
			map[string]any{
				"id": "tool_abc",
				"function": map[string]any{
					"name":      "Shell",
					"arguments": `{"command":"echo hello"}`,
				},
			},
		},
	})

	events := drainEvents(ks.events, 3)
	require.Len(t, events, 2)
	assert.Equal(t, core.EventThinking, events[0].Type)
	assert.Equal(t, "I will run a command", events[0].Content)
	assert.Equal(t, core.EventToolUse, events[1].Type)
	assert.Equal(t, "Shell", events[1].ToolName)
	assert.Equal(t, `{"command":"echo hello"}`, events[1].ToolInput)
	assert.Equal(t, "tool_abc", events[1].RequestID)
}

func TestHandleTool(t *testing.T) {
	ctx := context.Background()
	ks, _ := newKimiSession(ctx, "kimi", nil, "/tmp", "", "default", "", nil, core.MCPConfig{}, 0)
	defer ks.Close()

	ks.handleEvent(map[string]any{
		"role":         "tool",
		"tool_call_id": "tool_abc",
		"content": []any{
			map[string]any{"type": "text", "text": "hello\n"},
		},
	})

	events := drainEvents(ks.events, 1)
	require.Len(t, events, 1)
	assert.Equal(t, core.EventToolResult, events[0].Type)
	assert.Equal(t, "tool_abc", events[0].ToolName)
	assert.Contains(t, events[0].ToolResult, "hello")
}

func TestFlushPendingAsText(t *testing.T) {
	ctx := context.Background()
	ks, _ := newKimiSession(ctx, "kimi", nil, "/tmp", "", "default", "", nil, core.MCPConfig{}, 0)
	defer ks.Close()

	ks.pendingMsgs = []string{"Hello", " ", "world"}
	ks.flushPendingAsText()

	events := drainEvents(ks.events, 1)
	require.Len(t, events, 1)
	assert.Equal(t, core.EventText, events[0].Type)
	assert.Equal(t, "Hello world", events[0].Content)
	assert.Empty(t, ks.pendingMsgs)
}

func TestFlushPendingAsThinking(t *testing.T) {
	ctx := context.Background()
	ks, _ := newKimiSession(ctx, "kimi", nil, "/tmp", "", "default", "", nil, core.MCPConfig{}, 0)
	defer ks.Close()

	ks.pendingMsgs = []string{"Thinking..."}
	ks.flushPendingAsThinking()

	events := drainEvents(ks.events, 1)
	require.Len(t, events, 1)
	assert.Equal(t, core.EventThinking, events[0].Type)
	assert.Equal(t, "Thinking...", events[0].Content)
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "hello", truncate("hello", 10))
	assert.Equal(t, "hello world", truncate("hello world", 11))
	assert.Equal(t, "hello worl...", truncate("hello world", 10))
}

func TestWriteKimiMCPConfigFile(t *testing.T) {
	cfg := core.MCPConfigFromAgentToken("Dirextalk.D1", "https://d1.dirextalk.ai/mcp", "agent-token", "node-1")
	root := t.TempDir()
	path, cleanup, err := writeKimiMCPConfigFile(root, cfg)
	require.NoError(t, err)
	defer cleanup()

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var parsed struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(data, &parsed))
	server := parsed.MCPServers["dirextalk_d1"]
	assert.Equal(t, "http", server.Type)
	assert.Equal(t, "https://d1.dirextalk.ai/mcp", server.URL)
	assert.Equal(t, "Bearer agent-token", server.Headers["Authorization"])
	assert.Equal(t, "node-1", server.Headers["DIREXTALK-Agent-Node-Id"])
}

func TestKimiMCPConfigCleanupAfterNormalExit(t *testing.T) {
	root := isolateKimiTempRoot(t)
	cfg := core.MCPConfigFromAgentToken("Dirextalk.D1", "https://d1.dirextalk.ai/mcp", "agent-token", "node-1")
	ks, err := newKimiSession(
		context.Background(),
		os.Args[0],
		[]string{"-test.run=TestKimiHelperProcess", "--"},
		t.TempDir(),
		"",
		"default",
		"",
		[]string{"KIMI_HELPER_PROCESS=1"},
		cfg,
		0,
	)
	require.NoError(t, err)
	defer ks.Close()

	require.NoError(t, ks.Send("hello", nil, nil))
	ks.wg.Wait()
	assertNoKimiMCPTempDirs(t, root)
}

func TestKimiMCPConfigCleanupAfterStartFailure(t *testing.T) {
	root := isolateKimiTempRoot(t)
	cfg := core.MCPConfigFromAgentToken("Dirextalk.D1", "https://d1.dirextalk.ai/mcp", "agent-token", "node-1")
	ks, err := newKimiSession(
		context.Background(),
		filepath.Join(t.TempDir(), "missing-kimi"),
		nil,
		t.TempDir(),
		"",
		"default",
		"",
		nil,
		cfg,
		0,
	)
	require.NoError(t, err)
	defer ks.Close()

	require.Error(t, ks.Send("hello", nil, nil))
	assertNoKimiMCPTempDirs(t, root)
}

func TestKimiHelperProcess(t *testing.T) {
	if os.Getenv("KIMI_HELPER_PROCESS") != "1" {
		return
	}
	os.Exit(0)
}

func isolateKimiTempRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("TEMP", root)
	t.Setenv("TMP", root)
	t.Setenv("TMPDIR", root)
	return root
}

func assertNoKimiMCPTempDirs(t *testing.T, root string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "dirextalk-kimi-mcp-*"))
	require.NoError(t, err)
	assert.Empty(t, matches, "temporary MCP directories survived cleanup")
}

func TestBuildKimiArgsIncludesMCPConfigWithoutToken(t *testing.T) {
	ks := &kimiSession{workDir: "/repo", model: "kimi-k2", mode: "plan"}
	args := ks.buildKimiArgs("hello", "session-1", "/tmp/kimi-mcp.json")
	if !containsKimiArgPair(args, "--mcp-config-file", "/tmp/kimi-mcp.json") {
		t.Fatalf("missing --mcp-config-file arg pair: %v", args)
	}
	if strings.Contains(strings.Join(args, "\x00"), "agent-token") {
		t.Fatalf("argv should not include token: %v", args)
	}
}

func containsKimiArgPair(args []string, key, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}

func drainEvents(ch <-chan core.Event, max int) []core.Event {
	var events []core.Event
	timeout := time.After(500 * time.Millisecond)
	for i := 0; i < max; i++ {
		select {
		case evt := <-ch:
			events = append(events, evt)
		case <-timeout:
			return events
		}
	}
	return events
}
