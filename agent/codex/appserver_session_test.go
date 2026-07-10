package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YingSuiAI/dirextalk-connect/core"
)

func TestAppServerSession_ApplyThreadRuntimeState(t *testing.T) {
	s := &appServerSession{}
	effort := "xhigh"

	s.applyThreadRuntimeState("/tmp/project", "gpt-5.4", &effort)

	if got := s.GetWorkDir(); got != "/tmp/project" {
		t.Fatalf("GetWorkDir() = %q, want /tmp/project", got)
	}
	if got := s.GetModel(); got != "gpt-5.4" {
		t.Fatalf("GetModel() = %q, want gpt-5.4", got)
	}
	if got := s.GetReasoningEffort(); got != "xhigh" {
		t.Fatalf("GetReasoningEffort() = %q, want xhigh", got)
	}
}

func TestAppServerModeSettings_ReadOnlySkipsApproval(t *testing.T) {
	approval, sandbox := appServerModeSettings("read-only")
	if approval != "never" {
		t.Fatalf("approval = %q, want never", approval)
	}
	if sandbox != "read-only" {
		t.Fatalf("sandbox = %q, want read-only", sandbox)
	}
}

func TestAppServerSession_HandleRateLimitsUpdatedCachesUsage(t *testing.T) {
	s := &appServerSession{}
	raw, err := json.Marshal(appServerRateLimitsResponse{
		RateLimits: appServerRateLimitSnapshot{
			LimitID:   "codex",
			PlanType:  "pro",
			Primary:   &appServerRateLimitWindow{UsedPercent: 25, WindowDurationMins: 15, ResetsAt: 1730947200},
			Secondary: &appServerRateLimitWindow{UsedPercent: 42, WindowDurationMins: 60, ResetsAt: 1730950800},
			Credits:   &appServerCreditsSnapshot{HasCredits: true, Unlimited: false},
		},
	})
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}

	s.handleNotification("account/rateLimits/updated", raw)

	report, err := s.GetUsage(context.Background())
	if err != nil {
		t.Fatalf("GetUsage() returned error: %v", err)
	}
	if report.Provider != "codex" {
		t.Fatalf("provider = %q, want codex", report.Provider)
	}
	if report.Plan != "pro" {
		t.Fatalf("plan = %q, want pro", report.Plan)
	}
	if len(report.Buckets) != 1 {
		t.Fatalf("buckets = %d, want 1", len(report.Buckets))
	}
	if got := report.Buckets[0].Name; got != "codex" {
		t.Fatalf("bucket name = %q, want codex", got)
	}
	if got := report.Buckets[0].Windows[0].WindowSeconds; got != 15*60 {
		t.Fatalf("primary window seconds = %d, want %d", got, 15*60)
	}
	if got := report.Buckets[0].Windows[1].UsedPercent; got != 42 {
		t.Fatalf("secondary used percent = %d, want 42", got)
	}
	if report.Credits == nil || !report.Credits.HasCredits {
		t.Fatalf("credits = %#v, want has credits", report.Credits)
	}
}

func TestAppServerSession_HandleThreadTokenUsageUpdatedCachesContextUsage(t *testing.T) {
	s := &appServerSession{}
	raw, err := json.Marshal(appServerThreadTokenUsageNotification{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		TokenUsage: struct {
			Total              codexTokenUsage `json:"total"`
			Last               codexTokenUsage `json:"last"`
			ModelContextWindow int             `json:"modelContextWindow"`
		}{
			Total: codexTokenUsage{
				TotalTokens:           52011395,
				InputTokens:           51847383,
				CachedInputTokens:     48187904,
				OutputTokens:          164012,
				ReasoningOutputTokens: 78910,
			},
			Last: codexTokenUsage{
				TotalTokens:           41061,
				InputTokens:           40849,
				CachedInputTokens:     36864,
				OutputTokens:          212,
				ReasoningOutputTokens: 32,
			},
			ModelContextWindow: 258400,
		},
	})
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}

	s.handleNotification("thread/tokenUsage/updated", raw)

	usage := s.GetContextUsage()
	if usage == nil {
		t.Fatal("GetContextUsage() = nil, want cached context usage")
	}
	if usage.UsedTokens != 41061 {
		t.Fatalf("used tokens = %d, want 41061", usage.UsedTokens)
	}
	if usage.BaselineTokens != codexContextBaselineTokens {
		t.Fatalf("baseline tokens = %d, want %d", usage.BaselineTokens, codexContextBaselineTokens)
	}
	if usage.TotalTokens != 41061 {
		t.Fatalf("total tokens = %d, want 41061", usage.TotalTokens)
	}
	if usage.ContextWindow != 258400 {
		t.Fatalf("context window = %d, want 258400", usage.ContextWindow)
	}
	if usage.CachedInputTokens != 36864 {
		t.Fatalf("cached input tokens = %d, want 36864", usage.CachedInputTokens)
	}
	if usage.InputTokens != 40849 {
		t.Fatalf("input tokens = %d, want 40849", usage.InputTokens)
	}
}

func TestAppServerSession_MCPUsesConfiguredCommandAndEnvironment(t *testing.T) {
	workDir := t.TempDir()
	pathDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "helper-args.txt")
	envLogPath := filepath.Join(t.TempDir(), "helper-env.json")
	helperBin, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("resolve helper binary: %v", err)
	}
	if strings.ContainsAny(helperBin, " \t\r\n") {
		t.Skipf("test helper path contains whitespace unsupported by ParseCmdOpts: %q", helperBin)
	}
	t.Setenv("PATH", pathDir)
	t.Setenv("CC_CONNECT_APP_SERVER_HELPER", "1")
	t.Setenv("CC_CONNECT_APP_SERVER_HELPER_LOG", logPath)
	t.Setenv("CC_CONNECT_APP_SERVER_HELPER_ENV_LOG", envLogPath)

	agent, err := New(map[string]any{
		"backend":        "app_server",
		"app_server_url": "stdio",
		"cmd": strings.Join([]string{
			helperBin,
			"-test.run=TestAppServerSession_AppServerHelper",
			"--",
			"configured-extra",
		}, " "),
		"work_dir":        workDir,
		"mode":            "yolo",
		"mcp_server_name": "dirextalk-d1_dirextalk_ai",
		"mcp_url":         "https://d1.dirextalk.ai/mcp",
		"mcp_agent_token": "fake-agent-token",
		"mcp_node_id":     "codex-d1",
		"env": map[string]any{
			codexMCPAgentTokenEnv: "stale-token",
			codexMCPNodeIDEnv:     "stale-node",
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	session, err := agent.StartSession(context.Background(), "")
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	defer session.Close()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read helper args: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(args) < 5 {
		t.Fatalf("helper args = %#v, want configured extra args before app-server", args)
	}
	if args[0] != "-test.run=TestAppServerSession_AppServerHelper" {
		t.Fatalf("first helper arg = %q, want test run selector", args[0])
	}
	if args[1] != "--" || args[2] != "configured-extra" || args[3] != "app-server" {
		t.Fatalf("helper args = %#v, want -- configured-extra app-server prefix", args)
	}
	if !containsSequence(args, []string{"-c", `mcp_servers.dirextalk-d1_dirextalk_ai.url="https://d1.dirextalk.ai/mcp"`}) {
		t.Fatalf("helper args missing MCP url config flag: %#v", args)
	}
	if !containsSequence(args, []string{"-c", `mcp_servers.dirextalk-d1_dirextalk_ai.bearer_token_env_var="DIREXTALK_MCP_AGENT_TOKEN"`}) {
		t.Fatalf("helper args missing MCP bearer token env config flag: %#v", args)
	}
	if !containsSequence(args, []string{"-c", `mcp_servers.dirextalk-d1_dirextalk_ai.required=true`}) {
		t.Fatalf("helper args missing required MCP readiness flag: %#v", args)
	}
	if !containsSequence(args, []string{"-c", `mcp_servers.dirextalk-d1_dirextalk_ai.env_http_headers={"DIREXTALK-Agent-Node-Id"="DIREXTALK_MCP_NODE_ID"}`}) {
		t.Fatalf("helper args missing MCP environment-backed header config flag: %#v", args)
	}
	joined := strings.Join(args, "\n")
	for _, forbidden := range []string{"fake-agent-token", "Bearer fake-agent-token", "codex-d1", "Authorization=", ".http_headers="} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("app-server argv leaks static MCP value %q: %#v", forbidden, args)
		}
	}
	envData, err := os.ReadFile(envLogPath)
	if err != nil {
		t.Fatalf("read helper MCP env: %v", err)
	}
	var capturedEnv map[string]string
	if err := json.Unmarshal(envData, &capturedEnv); err != nil {
		t.Fatalf("decode helper MCP env: %v", err)
	}
	if capturedEnv[codexMCPAgentTokenEnv] != "fake-agent-token" || capturedEnv[codexMCPNodeIDEnv] != "codex-d1" {
		t.Fatalf("helper MCP env = %#v, want raw token and node id", capturedEnv)
	}
}

func TestAppServerSession_AppServerHelper(t *testing.T) {
	if os.Getenv("CC_CONNECT_APP_SERVER_HELPER") != "1" {
		return
	}
	logPath := os.Getenv("CC_CONNECT_APP_SERVER_HELPER_LOG")
	if logPath == "" {
		os.Exit(2)
	}
	if err := os.WriteFile(logPath, []byte(strings.Join(os.Args[1:], "\n")), 0o600); err != nil {
		os.Exit(2)
	}
	envLogPath := os.Getenv("CC_CONNECT_APP_SERVER_HELPER_ENV_LOG")
	envData, err := json.Marshal(map[string]string{
		codexMCPAgentTokenEnv: os.Getenv(codexMCPAgentTokenEnv),
		codexMCPNodeIDEnv:     os.Getenv(codexMCPNodeIDEnv),
	})
	if err != nil || envLogPath == "" || os.WriteFile(envLogPath, envData, 0o600) != nil {
		os.Exit(2)
	}

	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for {
		var envelope map[string]json.RawMessage
		if err := decoder.Decode(&envelope); err != nil {
			if err == io.EOF {
				return
			}
			os.Exit(2)
		}
		id, hasID := envelope["id"]
		if !hasID {
			continue
		}
		var method string
		if err := json.Unmarshal(envelope["method"], &method); err != nil {
			os.Exit(2)
		}
		result := map[string]any{}
		switch method {
		case "initialize":
			result["protocolVersion"] = "2026-06-26"
		case "thread/start", "thread/resume":
			result["cwd"] = ""
			result["model"] = ""
			result["thread"] = map[string]any{"id": "thread-configured-command"}
		case "account/rateLimits/read":
			result["rateLimits"] = map[string]any{"limitId": "codex"}
		default:
			result["ok"] = true
		}
		if err := encoder.Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(id),
			"result":  result,
		}); err != nil {
			os.Exit(2)
		}
	}
}

func TestMapAppServerRateLimits_PrefersMultiBucketView(t *testing.T) {
	report := mapAppServerRateLimits(appServerRateLimitsResponse{
		RateLimits: appServerRateLimitSnapshot{
			LimitID:  "legacy",
			PlanType: "team",
			Primary:  &appServerRateLimitWindow{UsedPercent: 99, WindowDurationMins: 15},
		},
		RateLimitsByLimitID: map[string]appServerRateLimitSnapshot{
			"codex": {
				LimitID:   "codex",
				LimitName: "Codex",
				PlanType:  "team",
				Primary:   &appServerRateLimitWindow{UsedPercent: 10, WindowDurationMins: 15},
			},
			"codex_other": {
				LimitID:  "codex_other",
				PlanType: "team",
				Primary:  &appServerRateLimitWindow{UsedPercent: 20, WindowDurationMins: 60},
			},
		},
	})

	if report.Plan != "team" {
		t.Fatalf("plan = %q, want team", report.Plan)
	}
	if len(report.Buckets) != 2 {
		t.Fatalf("buckets = %d, want 2", len(report.Buckets))
	}
	if report.Buckets[0].Name != "Codex" {
		t.Fatalf("first bucket = %q, want Codex", report.Buckets[0].Name)
	}
	if report.Buckets[1].Name != "codex_other" {
		t.Fatalf("second bucket = %q, want codex_other", report.Buckets[1].Name)
	}
}

func TestAppServerSession_HandleRequestUserInputEmitsAskQuestion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stdin := &lockedWriteCloser{}
	s := &appServerSession{
		events:           make(chan core.Event, 4),
		ctx:              ctx,
		pendingApprovals: make(map[string]chan core.PermissionResult),
		stdin:            stdin,
	}

	s.handleServerRequest(serverRequestProbe(t, `"rui-1"`, "item/tool/requestUserInput", map[string]any{
		"threadId": "thread-1",
		"turnId":   "turn-1",
		"itemId":   "call-1",
		"questions": []any{
			map[string]any{
				"id":       "database",
				"header":   "Database",
				"question": "Which database should we use?",
				"isOther":  true,
				"isSecret": false,
				"options": []any{
					map[string]any{"label": "Postgres", "description": "Use the existing relational database"},
					map[string]any{"label": "SQLite", "description": "Keep it embedded"},
				},
			},
		},
	}))

	var event core.Event
	select {
	case event = <-s.events:
	case <-time.After(time.Second):
		t.Fatal("expected AskUserQuestion event")
	}
	if event.Type != core.EventPermissionRequest {
		t.Fatalf("event type = %s, want %s", event.Type, core.EventPermissionRequest)
	}
	if event.ToolName != "AskUserQuestion" {
		t.Fatalf("tool name = %q, want AskUserQuestion", event.ToolName)
	}
	if event.RequestID != `"rui-1"` {
		t.Fatalf("request id = %q, want raw JSON id", event.RequestID)
	}
	if len(event.Questions) != 1 {
		t.Fatalf("questions = %d, want 1", len(event.Questions))
	}
	q := event.Questions[0]
	if q.Question != "Which database should we use?" || q.Header != "Database" {
		t.Fatalf("question = %#v", q)
	}
	if len(q.Options) != 2 || q.Options[0].Label != "Postgres" || q.Options[1].Description != "Keep it embedded" {
		t.Fatalf("options = %#v", q.Options)
	}
	if stdin.String() != "" {
		t.Fatalf("request_user_input should not write before the answer, got %q", stdin.String())
	}
}

func TestAppServerSession_ReadOnlyAutomaticallyApprovesPermissionRequests(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stdin := &lockedWriteCloser{}
	s := &appServerSession{
		mode:             "read-only",
		events:           make(chan core.Event, 1),
		ctx:              ctx,
		pendingApprovals: make(map[string]chan core.PermissionResult),
		stdin:            stdin,
	}
	s.handleServerRequest(serverRequestProbe(t, `"mcp-permission-1"`, "item/permissions/requestApproval", map[string]any{
		"permissions": map[string]any{"network": map[string]any{"hosts": []string{"a4.dirextalk.ai"}}},
	}))

	select {
	case event := <-s.events:
		t.Fatalf("read-only mode emitted approval request: %#v", event)
	case <-time.After(100 * time.Millisecond):
	}

	line := waitForWrittenJSONLine(t, stdin)
	var envelope struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Result  struct {
			Permissions map[string]any `json:"permissions"`
			Scope       string         `json:"scope"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		t.Fatalf("decode response %q: %v", line, err)
	}
	if envelope.JSONRPC != "2.0" || envelope.ID != "mcp-permission-1" {
		t.Fatalf("envelope = %#v", envelope)
	}
	if envelope.Result.Scope != "turn" || envelope.Result.Permissions["network"] == nil {
		t.Fatalf("permission response = %#v, want granted turn permissions", envelope.Result)
	}
}

func TestAppServerSession_HandleRequestUserInputWritesCodexResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stdin := &lockedWriteCloser{}
	s := &appServerSession{
		events:           make(chan core.Event, 4),
		ctx:              ctx,
		pendingApprovals: make(map[string]chan core.PermissionResult),
		stdin:            stdin,
	}

	s.handleServerRequest(serverRequestProbe(t, `"rui-2"`, "item/tool/requestUserInput", map[string]any{
		"threadId": "thread-1",
		"turnId":   "turn-1",
		"itemId":   "call-2",
		"questions": []any{
			map[string]any{
				"id":       "database",
				"header":   "Database",
				"question": "Which database should we use?",
				"options": []any{
					map[string]any{"label": "Postgres", "description": "Use the existing relational database"},
					map[string]any{"label": "SQLite", "description": "Keep it embedded"},
				},
			},
		},
	}))

	var event core.Event
	select {
	case event = <-s.events:
	case <-time.After(time.Second):
		t.Fatal("expected AskUserQuestion event")
	}
	if err := s.RespondPermission(event.RequestID, core.PermissionResult{
		Behavior: "allow",
		UpdatedInput: map[string]any{
			"answers": map[string]any{
				"Which database should we use?": "Postgres",
			},
		},
	}); err != nil {
		t.Fatalf("RespondPermission() error = %v", err)
	}

	line := waitForWrittenJSONLine(t, stdin)
	var envelope struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Result  struct {
			Answers map[string]struct {
				Answers []string `json:"answers"`
			} `json:"answers"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		t.Fatalf("decode response %q: %v", line, err)
	}
	if envelope.JSONRPC != "2.0" || envelope.ID != "rui-2" {
		t.Fatalf("envelope = %#v", envelope)
	}
	got := envelope.Result.Answers["database"].Answers
	if len(got) != 1 || got[0] != "Postgres" {
		t.Fatalf("answers[database] = %#v, want [Postgres]", got)
	}
}

var _ interface {
	GetUsage(context.Context) (*core.UsageReport, error)
} = (*appServerSession)(nil)

var _ interface {
	GetContextUsage() *core.ContextUsage
} = (*appServerSession)(nil)

type lockedWriteCloser struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *lockedWriteCloser) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *lockedWriteCloser) Close() error { return nil }

func (w *lockedWriteCloser) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

var _ io.WriteCloser = (*lockedWriteCloser)(nil)

func serverRequestProbe(t *testing.T, idJSON, method string, params any) map[string]json.RawMessage {
	t.Helper()
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	methodJSON, err := json.Marshal(method)
	if err != nil {
		t.Fatalf("marshal method: %v", err)
	}
	return map[string]json.RawMessage{
		"id":     json.RawMessage(idJSON),
		"method": methodJSON,
		"params": paramsJSON,
	}
}

func waitForWrittenJSONLine(t *testing.T, w *lockedWriteCloser) string {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for JSON response, buffer=%q", w.String())
		case <-ticker.C:
			for _, line := range strings.Split(w.String(), "\n") {
				line = strings.TrimSpace(line)
				if line != "" {
					return line
				}
			}
		}
	}
}
