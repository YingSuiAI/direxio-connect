package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/YingSuiAI/dirextalk-connect/config"
	"github.com/YingSuiAI/dirextalk-connect/internal/vnextconnector"
	controlv1 "github.com/YingSuiAI/dirextalk-connect/internal/vnextconnector/agentcontrolv1"
	"github.com/google/uuid"
)

// runVNextSupervisor is the exclusive schema-v2 process path. It constructs no
// Matrix platform, local API, webhook, bridge, scheduler, or legacy Engine; one
// config file therefore owns exactly one outbound Connector control stream.
func runVNextSupervisor(ctx context.Context, cfg *config.Config) error {
	if cfg == nil || !cfg.SupervisorMode() || cfg.Instance == nil || cfg.Runtime == nil ||
		cfg.Control == nil || cfg.Routing == nil || len(cfg.Projects) != 1 {
		return errors.New("vnext supervisor: incomplete schema-v2 configuration")
	}

	credential, err := vnextconnector.LoadControlCredential(
		cfg.Control.CredentialFile,
		cfg.Instance.TenantID,
		cfg.Instance.ID,
		cfg.Instance.Generation,
		cfg.Control.NodeURL,
	)
	if err != nil {
		return fmt.Errorf("vnext supervisor: load control credential: %w", err)
	}
	bootID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("vnext supervisor: create boot id: %w", err)
	}
	buildDigest, runtimeLauncher, err := adapterBuildDigest(cfg.Runtime.Kind)
	if err != nil {
		return fmt.Errorf("vnext supervisor: verify registered runtime launcher: %w", err)
	}

	stateDirectory := filepath.Join(cfg.DataDir, "vnext")
	if err := os.MkdirAll(stateDirectory, 0o700); err != nil {
		return fmt.Errorf("vnext supervisor: create state directory: %w", err)
	}
	stateDirectoryInfo, err := os.Lstat(stateDirectory)
	if err != nil || !stateDirectoryInfo.IsDir() || stateDirectoryInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("vnext supervisor: state directory is not a regular directory")
	}
	if err := os.Chmod(stateDirectory, 0o700); err != nil {
		return fmt.Errorf("vnext supervisor: restrict state directory: %w", err)
	}
	runtimeDirectoryInfo, err := os.Lstat(cfg.Control.RuntimeDir)
	if err != nil || !runtimeDirectoryInfo.IsDir() || runtimeDirectoryInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("vnext supervisor: Host Supervisor runtime directory is not a regular directory")
	}
	if err := os.Chmod(cfg.Control.RuntimeDir, 0o700); err != nil {
		return fmt.Errorf("vnext supervisor: restrict Host Supervisor runtime directory: %w", err)
	}
	identityLockPath, err := supervisorIdentityLockPath(cfg.Control.RuntimeDir)
	if err != nil {
		return fmt.Errorf("vnext supervisor: prepare identity lock: %w", err)
	}
	identityLock, err := AcquireInstanceLock(identityLockPath)
	if err != nil {
		return fmt.Errorf("vnext supervisor: acquire exclusive Connector identity lock: %w", err)
	}
	defer identityLock.Release()
	statePath := filepath.Join(stateDirectory, "connector-state.json")
	stateLock, err := AcquireInstanceLock(statePath)
	if err != nil {
		return fmt.Errorf("vnext supervisor: acquire exclusive state lock: %w", err)
	}
	defer stateLock.Release()
	stateStore, err := vnextconnector.NewStateStore(
		statePath,
		vnextconnector.StateIdentity{
			TenantID:            cfg.Instance.TenantID,
			ConnectorID:         cfg.Instance.ID,
			ConnectorGeneration: cfg.Instance.Generation,
		},
	)
	if err != nil {
		return fmt.Errorf("vnext supervisor: create state store: %w", err)
	}

	maximumRuns := uint32(cfg.Routing.MaxConcurrentRuns)
	maximumQueueDepth := maximumRuns * 4
	if maximumQueueDepth < maximumRuns || maximumQueueDepth > 1_000_000 {
		maximumQueueDepth = 1_000_000
	}
	client, err := vnextconnector.NewClient(vnextconnector.ClientConfig{
		TenantID:              cfg.Instance.TenantID,
		ConnectorID:           cfg.Instance.ID,
		HostID:                cfg.Instance.HostID,
		BootID:                bootID.String(),
		ConnectorGeneration:   cfg.Instance.Generation,
		SpecRevision:          cfg.Instance.SpecRevision,
		RuntimeKind:           cfg.Runtime.Kind,
		RuntimeVersion:        version,
		Adapter:               cfg.Runtime.Adapter,
		Profile:               cfg.Runtime.Profile,
		OfflinePolicy:         cfg.Routing.OfflinePolicy,
		AdapterBuildDigest:    buildDigest,
		MaximumConcurrentRuns: maximumRuns,
		MaximumQueueDepth:     maximumQueueDepth,
		StateStore:            stateStore,
	})
	if err != nil {
		return fmt.Errorf("vnext supervisor: create control client: %w", err)
	}
	connection, err := vnextconnector.NewControlConnection(cfg.Control.NodeURL, credential)
	if err != nil {
		return fmt.Errorf("vnext supervisor: create control connection: %w", err)
	}
	defer connection.Close()

	slog.Info(
		"vnext connector starting",
		"tenant_id", cfg.Instance.TenantID,
		"connector_id", cfg.Instance.ID,
		"generation", cfg.Instance.Generation,
		"runtime_kind", cfg.Runtime.Kind,
		"runtime_launcher", filepath.Base(runtimeLauncher),
		"execution_state", "protocol_foundation",
	)
	return client.Run(
		ctx,
		vnextconnector.GRPCStreamOpener(controlv1.NewConnectorControlClient(connection)),
	)
}

func adapterBuildDigest(runtimeKind string) ([sha256.Size]byte, string, error) {
	var digest [sha256.Size]byte
	launcherName := registeredRuntimeLauncher(runtimeKind)
	if launcherName == "" {
		return digest, "", errors.New("runtime kind has no registered launcher")
	}
	launcherPath, err := exec.LookPath(launcherName)
	if err != nil {
		return digest, "", fmt.Errorf("runtime launcher %q was not found: %w", launcherName, err)
	}
	connectorPath, err := os.Executable()
	if err != nil {
		return digest, "", err
	}
	connectorDigest, err := artifactDigest(connectorPath)
	if err != nil {
		return digest, "", fmt.Errorf("hash Connector executable: %w", err)
	}
	launcherDigest, err := artifactDigest(launcherPath)
	if err != nil {
		return digest, "", fmt.Errorf("hash runtime launcher: %w", err)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("dirextalk.connector-adapter-build.v1\x00"))
	_, _ = hash.Write(connectorDigest[:])
	_, _ = hash.Write(launcherDigest[:])
	copy(digest[:], hash.Sum(nil))
	return digest, launcherPath, nil
}

func artifactDigest(path string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	file, err := os.Open(path)
	if err != nil {
		return digest, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return digest, errors.New("runtime artifact is not a regular file")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return digest, err
	}
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

func registeredRuntimeLauncher(runtimeKind string) string {
	switch runtimeKind {
	case "codex":
		return "codex"
	case "openclaw_acp":
		return "openclaw-acp"
	case "eino":
		return "eino"
	case "rig":
		return "rig"
	case "claude_code":
		return "claude"
	case "custom_acp":
		return "vendor-v1"
	default:
		return ""
	}
}

func supervisorIdentityLockPath(runtimeDirectory string) (string, error) {
	info, err := os.Lstat(runtimeDirectory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("Host Supervisor runtime directory is not a regular directory")
	}
	return filepath.Join(runtimeDirectory, "connector.identity"), nil
}
