//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"os/user"
	"syscall"

	"github.com/YingSuiAI/dirextalk-connect/config"
)

const maxSupervisorConfigBytes = 1024 * 1024

func executeSupervisorInvocation(invocation supervisorInvocation) error {
	if err := verifySupervisorProcessBoundary(invocation); err != nil {
		return err
	}
	cfg, err := config.Load(invocation.configPath)
	if err != nil {
		return fmt.Errorf("load fixed configuration: %w", err)
	}
	if err := validateSupervisorInvocationConfig(invocation, cfg); err != nil {
		return err
	}
	config.ConfigPath = invocation.configPath

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return runVNextSupervisor(ctx, cfg)
}

func verifySupervisorProcessBoundary(invocation supervisorInvocation) error {
	expectedUser, err := supervisorUnixUser(invocation.instanceID)
	if err != nil {
		return err
	}
	current, err := user.Current()
	if err != nil || current.Username != expectedUser {
		return errors.New("process user does not match the registered instance identity")
	}
	if err := verifySupervisorDirectory(invocation.configDir); err != nil {
		return err
	}
	metadata, err := os.Lstat(invocation.configPath)
	if err != nil || !metadata.Mode().IsRegular() || metadata.Mode()&os.ModeSymlink != 0 ||
		metadata.Size() <= 0 || metadata.Size() > maxSupervisorConfigBytes || metadata.Mode().Perm() != 0o440 {
		return errors.New("fixed supervisor configuration is not a bounded root-owned 0440 file")
	}
	stat, ok := metadata.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || stat.Gid != uint32(os.Getegid()) {
		return errors.New("fixed supervisor configuration ownership is invalid")
	}
	return nil
}

func verifySupervisorDirectory(directory string) error {
	metadata, err := os.Lstat(directory)
	if err != nil || !metadata.IsDir() || metadata.Mode()&os.ModeSymlink != 0 || metadata.Mode().Perm() != 0o750 {
		return errors.New("fixed supervisor configuration directory is not root-owned 0750")
	}
	stat, ok := metadata.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || stat.Gid != uint32(os.Getegid()) {
		return errors.New("fixed supervisor configuration directory ownership is invalid")
	}
	return nil
}
