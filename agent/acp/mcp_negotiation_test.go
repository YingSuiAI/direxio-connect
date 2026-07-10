package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YingSuiAI/dirextalk-connect/core"
	"github.com/YingSuiAI/dirextalk-connect/internal/testutil"
)

func TestACPSendsStandardMCPServersAfterHTTPAdvertisement(t *testing.T) {
	testutil.IsolatedHome(t)
	capturePath := filepath.Join(t.TempDir(), "session-new.json")
	session, err := newACPMCPFakeSession(t, true, capturePath)
	if err != nil {
		t.Fatalf("newACPSession() error = %v", err)
	}
	defer session.Close()

	data, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read captured session/new: %v", err)
	}
	if bytes.Contains(data, []byte("mcp_servers")) {
		t.Fatalf("session/new contains non-standard mcp_servers field: %s", data)
	}
}

func TestACPRejectsConfiguredMCPWithoutHTTPAdvertisement(t *testing.T) {
	testutil.IsolatedHome(t)
	_, err := newACPMCPFakeSession(t, false, "")
	if err == nil {
		t.Fatal("newACPSession() = nil error, want fail-closed capability error")
	}
	if !strings.Contains(err.Error(), "does not advertise HTTP MCP") {
		t.Fatalf("newACPSession() error = %q, want actionable HTTP capability error", err)
	}
}

func newACPMCPFakeSession(t *testing.T, advertiseHTTP bool, capturePath string) (*acpSession, error) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return newACPSession(ctx, acpSessionConfig{
		command:  executable,
		args:     []string{"-test.run=TestACPMCPStrictFakeAgent", "--"},
		workDir:  t.TempDir(),
		extraEnv: []string{fmt.Sprintf("GO_WANT_ACP_MCP_FAKE=1"), fmt.Sprintf("ACP_MCP_HTTP=%t", advertiseHTTP), "ACP_MCP_CAPTURE=" + capturePath},
		mcpConfig: core.MCPConfigFromAgentToken(
			"dirextalk-d1",
			"https://d1.dirextalk.ai/mcp",
			"agent-token",
			"node-1",
		),
	})
}

func TestACPMCPStrictFakeAgent(t *testing.T) {
	if os.Getenv("GO_WANT_ACP_MCP_FAKE") != "1" {
		return
	}

	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		switch request.Method {
		case "initialize":
			httpCap := os.Getenv("ACP_MCP_HTTP") == "true"
			writeACPFakeResult(t, encoder, request.ID, map[string]any{
				"protocolVersion": 1,
				"agentCapabilities": map[string]any{
					"loadSession":     false,
					"mcpCapabilities": map[string]any{"http": httpCap, "sse": false},
				},
			})
		case "session/new":
			var params struct {
				CWD        string `json:"cwd"`
				MCPServers []struct {
					Name    string `json:"name"`
					Type    string `json:"type"`
					URL     string `json:"url"`
					Headers []struct {
						Name  string `json:"name"`
						Value string `json:"value"`
					} `json:"headers"`
				} `json:"mcpServers"`
			}
			decoder := json.NewDecoder(bytes.NewReader(request.Params))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&params); err != nil {
				t.Fatalf("strict decode session/new: %v; params=%s", err, request.Params)
			}
			if os.Getenv("ACP_MCP_HTTP") != "true" {
				t.Fatalf("session/new sent despite missing HTTP capability: %s", request.Params)
			}
			if len(params.MCPServers) != 1 || params.MCPServers[0].Type != "http" {
				t.Fatalf("mcpServers = %#v", params.MCPServers)
			}
			server := params.MCPServers[0]
			if server.Name != "dirextalk-d1" || server.URL != "https://d1.dirextalk.ai/mcp" {
				t.Fatalf("MCP server identity/URL = %#v", server)
			}
			headers := map[string]string{}
			for _, header := range server.Headers {
				headers[header.Name] = header.Value
			}
			if headers["Authorization"] != "Bearer agent-token" || headers["DIREXTALK-Agent-Node-Id"] != "node-1" {
				t.Fatalf("MCP headers = %#v", headers)
			}
			if capturePath := os.Getenv("ACP_MCP_CAPTURE"); capturePath != "" {
				if err := os.WriteFile(capturePath, request.Params, 0o600); err != nil {
					t.Fatalf("capture session/new: %v", err)
				}
			}
			writeACPFakeResult(t, encoder, request.ID, map[string]any{"sessionId": "strict-mcp-session"})
		default:
			t.Fatalf("unexpected method %q", request.Method)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan requests: %v", err)
	}
}

func writeACPFakeResult(t *testing.T, encoder *json.Encoder, id json.RawMessage, result any) {
	t.Helper()
	if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result}); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
