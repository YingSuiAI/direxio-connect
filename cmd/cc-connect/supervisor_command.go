package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"path"
	"regexp"

	"github.com/YingSuiAI/dirextalk-connect/config"
	"github.com/google/uuid"
)

const supervisorConfigFilename = "config.toml"

var supervisorUUIDv7Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type supervisorInvocation struct {
	instanceID     string
	tenantID       string
	hostID         string
	configDir      string
	configPath     string
	dataDir        string
	workspaceDir   string
	runtimeDir     string
	credentialFile string
}

func runSupervisorCommand(args []string) error {
	invocation, err := parseSupervisorInvocation(args)
	if err != nil {
		return err
	}
	return executeSupervisorInvocation(invocation)
}

func parseSupervisorInvocation(args []string) (supervisorInvocation, error) {
	var invocation supervisorInvocation
	flags := flag.NewFlagSet("supervisor", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&invocation.instanceID, "instance-id", "", "")
	flags.StringVar(&invocation.tenantID, "tenant-id", "", "")
	flags.StringVar(&invocation.hostID, "host-id", "", "")
	flags.StringVar(&invocation.configDir, "config-dir", "", "")
	flags.StringVar(&invocation.dataDir, "data-dir", "", "")
	flags.StringVar(&invocation.workspaceDir, "workspace-dir", "", "")
	flags.StringVar(&invocation.runtimeDir, "runtime-dir", "", "")
	flags.StringVar(&invocation.credentialFile, "credential-file", "", "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return supervisorInvocation{}, errors.New("invalid closed supervisor arguments")
	}
	if !supervisorUUIDv7Pattern.MatchString(invocation.instanceID) ||
		!supervisorUUIDv7Pattern.MatchString(invocation.tenantID) ||
		!supervisorUUIDv7Pattern.MatchString(invocation.hostID) {
		return supervisorInvocation{}, errors.New("instance, tenant, and host IDs must be canonical UUIDv7 values")
	}

	base := path.Join("/var/lib/dirextalk/connect/instances", invocation.instanceID)
	expected := supervisorInvocation{
		instanceID:     invocation.instanceID,
		tenantID:       invocation.tenantID,
		hostID:         invocation.hostID,
		configDir:      path.Join("/etc/dirextalk/connect/instances", invocation.instanceID),
		dataDir:        path.Join(base, "data"),
		workspaceDir:   path.Join(base, "workspace"),
		runtimeDir:     path.Join("/run/dirextalk/connect", invocation.instanceID, "worker"),
		credentialFile: path.Join("/run/dirextalk/connect", invocation.instanceID, "credentials", "control.credential"),
	}
	expected.configPath = path.Join(expected.configDir, supervisorConfigFilename)
	if invocation.configDir != expected.configDir || invocation.dataDir != expected.dataDir ||
		invocation.workspaceDir != expected.workspaceDir || invocation.runtimeDir != expected.runtimeDir ||
		invocation.credentialFile != expected.credentialFile {
		return supervisorInvocation{}, errors.New("supervisor paths do not match the registered instance layout")
	}
	return expected, nil
}

func validateSupervisorInvocationConfig(invocation supervisorInvocation, cfg *config.Config) error {
	if cfg == nil || !cfg.SupervisorMode() || cfg.Instance == nil || cfg.Control == nil || cfg.Workspace == nil {
		return errors.New("configuration is not a complete supervisor contract")
	}
	if cfg.Instance.ID != invocation.instanceID || cfg.Instance.TenantID != invocation.tenantID ||
		cfg.Instance.HostID != invocation.hostID {
		return errors.New("configuration identity does not match the host invocation")
	}
	if cfg.DataDir != invocation.dataDir || cfg.Workspace.Root != invocation.workspaceDir ||
		cfg.Control.RuntimeDir != invocation.runtimeDir || cfg.Control.CredentialFile != invocation.credentialFile {
		return errors.New("configuration paths do not match the host invocation")
	}
	if len(cfg.Projects) != 1 {
		return errors.New("configuration must contain exactly one project")
	}
	workDir, ok := cfg.Projects[0].Agent.Options["work_dir"].(string)
	if !ok || workDir != path.Join(invocation.workspaceDir, "project") {
		return errors.New("project workspace does not match the registered instance layout")
	}
	return nil
}

func supervisorUnixUser(instanceID string) (string, error) {
	parsed, err := uuid.Parse(instanceID)
	if err != nil || !supervisorUUIDv7Pattern.MatchString(instanceID) {
		return "", errors.New("invalid instance identity")
	}
	value := new(big.Int).SetBytes(parsed[:])
	alphabet := "0123456789abcdefghjkmnpqrstvwxyz"
	encoded := make([]byte, 26)
	mask := big.NewInt(31)
	for index := len(encoded) - 1; index >= 0; index-- {
		encoded[index] = alphabet[new(big.Int).And(value, mask).Int64()]
		value.Rsh(value, 5)
	}
	return fmt.Sprintf("dtx%s", encoded), nil
}
