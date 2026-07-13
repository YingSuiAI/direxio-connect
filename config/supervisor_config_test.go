package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestSupervisorConfigLegacySchemaRemainsCompatible(t *testing.T) {
	legacy := Config{Projects: []ProjectConfig{validProject("legacy")}}
	if legacy.SupervisorMode() {
		t.Fatal("schema_version 0 must not enable supervisor mode")
	}
	if err := legacy.validate(); err != nil {
		t.Fatalf("legacy config unexpectedly rejected: %v", err)
	}

	legacy.Projects[0].Platforms = nil
	assertErrContains(t, legacy.validate(), "needs at least one [[projects.platforms]]")

	var nilConfig *Config
	if nilConfig.SupervisorMode() {
		t.Fatal("nil config must not enable supervisor mode")
	}
}

func TestSupervisorConfigLoadsHappyPath(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	fixture := fmt.Sprintf(`
schema_version = 2
data_dir = %q

[instance]
id = "01890f47-5fd4-7cc2-8f8f-5f9476f4f001"
tenant_id = "01890f47-5fd4-7cc2-8f8f-5f9476f4f003"
host_id = "01890f47-5fd4-7cc2-8f8f-5f9476f4f002"
display_name = "Codex primary"
generation = 7
spec_revision = 12

[runtime]
kind = "codex"
adapter = "codex-app-server"
profile = "default"

[control]
node_url = "https://im.example.com"
credential_file = %q
runtime_dir = %q

[routing]
max_concurrent_runs = 2
offline_policy = "queue"

[workspace]
root = %q

[security]
policy_id = "coding-agent-standard"
secret_refs = ["secret://model/codex-main"]

[limits]
memory_mb = 4096
cpu_quota_percent = 200
processes = 128

[[projects]]
name = "primary"

[projects.agent]
type = "codex"

[projects.agent.options]
work_dir = %q
`, root+"/data", root+"/run/01890f47-5fd4-7cc2-8f8f-5f9476f4f001/credentials/control.credential", root+"/run/01890f47-5fd4-7cc2-8f8f-5f9476f4f001/worker", root+"/workspace", root+"/workspace/project")

	cfg, err := Load(writeConfigFixture(t, fixture))
	if err != nil {
		t.Fatalf("Load supervisor config: %v", err)
	}
	if !cfg.SupervisorMode() {
		t.Fatal("schema_version 2 must enable supervisor mode")
	}
	if cfg.Instance == nil || cfg.Instance.SpecRevision != 12 {
		t.Fatalf("instance block not decoded: %+v", cfg.Instance)
	}
	if len(cfg.Projects) != 1 || len(cfg.Projects[0].Platforms) != 0 {
		t.Fatalf("unexpected supervisor projects: %+v", cfg.Projects)
	}
}

func TestSupervisorConfigRequiresSingleExclusiveProjectAndBlocks(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "single project",
			mutate: func(cfg *Config) {
				cfg.Projects = append(cfg.Projects, cfg.Projects[0])
			},
			wantErr: "requires exactly one [[projects]] entry",
		},
		{
			name: "legacy Matrix consumer forbidden",
			mutate: func(cfg *Config) {
				cfg.Projects[0].Platforms = []PlatformConfig{{Type: "matrix"}}
			},
			wantErr: "must not configure a legacy Matrix platform",
		},
		{name: "instance block", mutate: func(cfg *Config) { cfg.Instance = nil }, wantErr: "requires [instance]"},
		{name: "runtime block", mutate: func(cfg *Config) { cfg.Runtime = nil }, wantErr: "requires [runtime]"},
		{name: "control block", mutate: func(cfg *Config) { cfg.Control = nil }, wantErr: "requires [control]"},
		{name: "routing block", mutate: func(cfg *Config) { cfg.Routing = nil }, wantErr: "requires [routing]"},
		{name: "workspace block", mutate: func(cfg *Config) { cfg.Workspace = nil }, wantErr: "requires [workspace]"},
		{name: "security block", mutate: func(cfg *Config) { cfg.Security = nil }, wantErr: "requires [security]"},
		{name: "limits block", mutate: func(cfg *Config) { cfg.Limits = nil }, wantErr: "requires [limits]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validSupervisorConfig(t)
			tt.mutate(&cfg)
			assertErrContains(t, cfg.validate(), tt.wantErr)
		})
	}
}

func TestSupervisorConfigRejectsUnsafeValues(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{name: "non canonical UUIDv7", mutate: func(cfg *Config) { cfg.Instance.ID = strings.ToUpper(cfg.Instance.ID) }, wantErr: "canonical lowercase UUIDv7"},
		{name: "missing tenant UUIDv7", mutate: func(cfg *Config) { cfg.Instance.TenantID = "" }, wantErr: "instance.tenant_id"},
		{name: "zero generation", mutate: func(cfg *Config) { cfg.Instance.Generation = 0 }, wantErr: "generation must be a positive JSON-safe integer"},
		{name: "unsafe spec revision", mutate: func(cfg *Config) { cfg.Instance.SpecRevision = maxJSONSafeInteger + 1 }, wantErr: "spec_revision must be a positive JSON-safe integer"},
		{name: "unregistered runtime", mutate: func(cfg *Config) { cfg.Runtime.Kind = "shell" }, wantErr: "runtime.kind"},
		{name: "unregistered adapter", mutate: func(cfg *Config) { cfg.Runtime.Adapter = "arbitrary-command" }, wantErr: "runtime.adapter must be"},
		{name: "unregistered profile", mutate: func(cfg *Config) { cfg.Runtime.Profile = "operator-defined" }, wantErr: "runtime.profile must be a registered profile"},
		{name: "insecure node URL", mutate: func(cfg *Config) { cfg.Control.NodeURL = "http://im.example.com" }, wantErr: "origin HTTPS URL"},
		{name: "relative credential", mutate: func(cfg *Config) { cfg.Control.CredentialFile = "control.credential" }, wantErr: "credential_file must be an absolute path"},
		{name: "missing runtime directory", mutate: func(cfg *Config) { cfg.Control.RuntimeDir = "" }, wantErr: "runtime_dir must be an absolute"},
		{name: "runtime identity mismatch", mutate: func(cfg *Config) {
			base := filepath.ToSlash(t.TempDir()) + "/other"
			cfg.Control.RuntimeDir = base + "/worker"
			cfg.Control.CredentialFile = base + "/credentials/control.credential"
		}, wantErr: "runtime_dir must be scoped by instance.id"},
		{name: "zero capacity", mutate: func(cfg *Config) { cfg.Routing.MaxConcurrentRuns = 0 }, wantErr: "max_concurrent_runs must be between"},
		{name: "relative data directory", mutate: func(cfg *Config) { cfg.DataDir = "data" }, wantErr: "data_dir must be an explicit absolute path"},
		{name: "overlapping data and workspace", mutate: func(cfg *Config) { cfg.DataDir = cfg.Workspace.Root + "/data" }, wantErr: "must be independent paths"},
		{name: "work directory escape", mutate: func(cfg *Config) { cfg.Projects[0].Agent.Options["work_dir"] = cfg.DataDir }, wantErr: "must equal or be inside workspace.root"},
		{name: "agent command override", mutate: func(cfg *Config) { cfg.Projects[0].Agent.Options["command"] = "arbitrary" }, wantErr: "agent.options.command is not allowed"},
		{name: "direct provider credential", mutate: func(cfg *Config) { cfg.Projects[0].Agent.Providers = []ProviderConfig{{Name: "unsafe"}} }, wantErr: "only through security.secret_refs"},
		{name: "execution identity override", mutate: func(cfg *Config) { cfg.Projects[0].RunAsUser = "another-user" }, wantErr: "controlled by security.policy_id"},
		{name: "missing secret refs", mutate: func(cfg *Config) { cfg.Security.SecretRefs = nil }, wantErr: "must contain at least one"},
		{name: "memory below bound", mutate: func(cfg *Config) { cfg.Limits.MemoryMB = 127 }, wantErr: "memory_mb must be between"},
		{name: "CPU above bound", mutate: func(cfg *Config) { cfg.Limits.CPUQuotaPercent = 10_001 }, wantErr: "cpu_quota_percent must be between"},
		{name: "processes below bound", mutate: func(cfg *Config) { cfg.Limits.Processes = 15 }, wantErr: "processes must be between"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validSupervisorConfig(t)
			tt.mutate(&cfg)
			assertErrContains(t, cfg.validate(), tt.wantErr)
		})
	}
}

func validSupervisorConfig(t *testing.T) Config {
	t.Helper()
	root := filepath.ToSlash(t.TempDir())
	workspaceRoot := root + "/workspace"
	runtimeRoot := root + "/run/01890f47-5fd4-7cc2-8f8f-5f9476f4f001"
	return Config{
		SchemaVersion: 2,
		DataDir:       root + "/data",
		Instance: &InstanceConfig{
			ID:           "01890f47-5fd4-7cc2-8f8f-5f9476f4f001",
			TenantID:     "01890f47-5fd4-7cc2-8f8f-5f9476f4f003",
			HostID:       "01890f47-5fd4-7cc2-8f8f-5f9476f4f002",
			DisplayName:  "Codex primary",
			Generation:   7,
			SpecRevision: 12,
		},
		Runtime: &RuntimeConfig{Kind: "codex", Adapter: "codex-app-server", Profile: "default"},
		Control: &ControlConfig{
			NodeURL:        "https://im.example.com",
			CredentialFile: runtimeRoot + "/credentials/control.credential",
			RuntimeDir:     runtimeRoot + "/worker",
		},
		Routing:   &RoutingConfig{MaxConcurrentRuns: 2, OfflinePolicy: "queue"},
		Workspace: &WorkspaceConfig{Root: workspaceRoot},
		Security:  &SecurityConfig{PolicyID: "coding-agent-standard", SecretRefs: []string{"secret://model/codex-main"}},
		Limits:    &LimitsConfig{MemoryMB: 4096, CPUQuotaPercent: 200, Processes: 128},
		Projects: []ProjectConfig{{
			Name: "primary",
			Agent: AgentConfig{
				Type:    "codex",
				Options: map[string]any{"work_dir": workspaceRoot + "/project"},
			},
		}},
	}
}
