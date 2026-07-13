package main

import (
	"testing"

	"github.com/YingSuiAI/dirextalk-connect/config"
)

const (
	testSupervisorTenant    = "0197f1f0-0000-7000-8000-000000000001"
	testSupervisorHost      = "0197f1f0-0000-7000-8000-000000000002"
	testSupervisorConnector = "0197f1f0-0000-7000-8000-000000000003"
)

func TestParseSupervisorInvocationUsesFixedLayout(t *testing.T) {
	invocation, err := parseSupervisorInvocation(supervisorArguments())
	if err != nil {
		t.Fatalf("parse supervisor invocation: %v", err)
	}
	if invocation.configPath != "/etc/dirextalk/connect/instances/0197f1f0-0000-7000-8000-000000000003/config.toml" {
		t.Fatalf("unexpected config path: %s", invocation.configPath)
	}
	if got, err := supervisorUnixUser(testSupervisorConnector); err != nil || got != "dtx01jzrz0000e008000000000003" {
		t.Fatalf("unexpected deterministic user %q: %v", got, err)
	}

	malformed := supervisorArguments()
	malformed[7] = "/tmp/connector-config"
	if _, err := parseSupervisorInvocation(malformed); err == nil {
		t.Fatal("arbitrary config directory must be rejected")
	}
	if _, err := parseSupervisorInvocation(append(supervisorArguments(), "--force")); err == nil {
		t.Fatal("unknown supervisor flags must be rejected")
	}
}

func TestValidateSupervisorInvocationConfigBindsIdentityAndPaths(t *testing.T) {
	invocation, err := parseSupervisorInvocation(supervisorArguments())
	if err != nil {
		t.Fatalf("parse supervisor invocation: %v", err)
	}
	cfg := &config.Config{
		SchemaVersion: 2,
		DataDir:       invocation.dataDir,
		Instance: &config.InstanceConfig{
			ID:       invocation.instanceID,
			TenantID: invocation.tenantID,
			HostID:   invocation.hostID,
		},
		Control: &config.ControlConfig{
			CredentialFile: invocation.credentialFile,
			RuntimeDir:     invocation.runtimeDir,
		},
		Workspace: &config.WorkspaceConfig{Root: invocation.workspaceDir},
		Projects: []config.ProjectConfig{{
			Agent: config.AgentConfig{Options: map[string]any{"work_dir": invocation.workspaceDir + "/project"}},
		}},
	}
	if err := validateSupervisorInvocationConfig(invocation, cfg); err != nil {
		t.Fatalf("validate exact config: %v", err)
	}
	cfg.Control.RuntimeDir = "/run/dirextalk/connect/sibling/worker"
	if err := validateSupervisorInvocationConfig(invocation, cfg); err == nil {
		t.Fatal("sibling runtime directory must be rejected")
	}
}

func supervisorArguments() []string {
	base := "/var/lib/dirextalk/connect/instances/" + testSupervisorConnector
	runtime := "/run/dirextalk/connect/" + testSupervisorConnector
	return []string{
		"--instance-id", testSupervisorConnector,
		"--tenant-id", testSupervisorTenant,
		"--host-id", testSupervisorHost,
		"--config-dir", "/etc/dirextalk/connect/instances/" + testSupervisorConnector,
		"--data-dir", base + "/data",
		"--workspace-dir", base + "/workspace",
		"--runtime-dir", runtime + "/worker",
		"--credential-file", runtime + "/credentials/control.credential",
	}
}
