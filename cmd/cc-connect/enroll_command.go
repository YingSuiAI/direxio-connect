package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/YingSuiAI/dirextalk-connect/internal/vnextconnector"
)

const enrollmentCommandTimeout = 30 * time.Second

type enrollCommandExecutor func(
	context.Context,
	vnextconnector.EnrollmentOptions,
	[]byte,
) ([]byte, error)

func runEnrollCommand(args []string) error {
	return runEnrollCommandWithExecutor(args, os.Stdin, os.Stdout, vnextconnector.EnrollConnector)
}

func runEnrollCommandWithExecutor(
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	executor enrollCommandExecutor,
) error {
	options, err := parseEnrollInvocation(args)
	if err != nil {
		return err
	}
	if stdin == nil || stdout == nil || executor == nil {
		return errors.New("invalid closed enrollment invocation")
	}
	token, err := io.ReadAll(io.LimitReader(stdin, 33))
	if err != nil {
		clear(token)
		return errors.New("cannot read enrollment token from stdin")
	}
	defer clear(token)
	if len(token) != 32 {
		return errors.New("enrollment stdin must contain exactly 32 raw token bytes with no newline")
	}
	ctx, cancel := context.WithTimeout(context.Background(), enrollmentCommandTimeout)
	defer cancel()
	credential, err := executor(ctx, options, token)
	if err != nil {
		clear(credential)
		return err
	}
	defer clear(credential)
	if len(credential) == 0 || len(credential) > vnextconnector.MaxControlCredentialBytes {
		return errors.New("enrollment returned an invalid credential document")
	}
	if _, err := io.Copy(stdout, bytes.NewReader(credential)); err != nil {
		return errors.New("cannot write enrollment credential to stdout")
	}
	return nil
}

func parseEnrollInvocation(args []string) (vnextconnector.EnrollmentOptions, error) {
	if err := rejectDuplicateEnrollFlags(args); err != nil {
		return vnextconnector.EnrollmentOptions{}, err
	}
	var options vnextconnector.EnrollmentOptions
	flags := flag.NewFlagSet("enroll", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.TenantID, "tenant-id", "", "")
	flags.StringVar(&options.HostID, "host-id", "", "")
	flags.StringVar(&options.ConnectorID, "connector-id", "", "")
	flags.Uint64Var(&options.Generation, "generation", 0, "")
	flags.Uint64Var(&options.SpecRevision, "spec-revision", 0, "")
	flags.StringVar(&options.RequestID, "request-id", "", "")
	flags.StringVar(&options.EnrollmentURL, "enrollment-url", "", "")
	flags.StringVar(&options.EnrollmentServerName, "enrollment-server-name", "", "")
	flags.StringVar(&options.EnrollmentRootCAFile, "enrollment-root-ca-file", "", "")
	flags.StringVar(&options.ControlURL, "control-url", "", "")
	flags.StringVar(&options.ControlServerName, "control-server-name", "", "")
	flags.StringVar(&options.ControlRootCAFile, "control-root-ca-file", "", "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return vnextconnector.EnrollmentOptions{}, errors.New("invalid closed enrollment arguments")
	}
	if err := vnextconnector.ValidateEnrollmentOptions(options); err != nil {
		return vnextconnector.EnrollmentOptions{}, err
	}
	return options, nil
}

func rejectDuplicateEnrollFlags(args []string) error {
	seen := make(map[string]struct{}, len(args)/2)
	for _, argument := range args {
		if !strings.HasPrefix(argument, "-") {
			continue
		}
		name := strings.TrimLeft(argument, "-")
		if separator := strings.IndexByte(name, '='); separator >= 0 {
			name = name[:separator]
		}
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("duplicate enrollment flag --%s", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}
