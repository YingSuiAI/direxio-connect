package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/YingSuiAI/dirextalk-connect/internal/vnextconnector"
)

func TestEnrollCommandRequiresExactRawTokenAndKeepsFailureStdoutEmpty(t *testing.T) {
	tests := []struct {
		name       string
		tokenBytes int
		execErr    error
		wantCalls  int
		wantOutput string
	}{
		{name: "31 bytes", tokenBytes: 31},
		{name: "32 bytes", tokenBytes: 32, wantCalls: 1, wantOutput: `{"credential":true}`},
		{name: "33 bytes", tokenBytes: 33},
		{name: "executor failure", tokenBytes: 32, execErr: errors.New("enrollment failed"), wantCalls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			executor := func(_ context.Context, _ vnextconnector.EnrollmentOptions, token []byte) ([]byte, error) {
				calls++
				if len(token) != 32 {
					t.Fatalf("executor received token length %d", len(token))
				}
				if test.execErr != nil {
					return nil, test.execErr
				}
				return []byte(`{"credential":true}`), nil
			}
			var stdout bytes.Buffer
			err := runEnrollCommandWithExecutor(
				enrollmentCommandArguments(),
				bytes.NewReader(bytes.Repeat([]byte{0x41}, test.tokenBytes)),
				&stdout,
				executor,
			)
			if test.wantOutput == "" {
				if err == nil {
					t.Fatal("command succeeded, want failure")
				}
			} else if err != nil {
				t.Fatalf("command failed: %v", err)
			}
			if calls != test.wantCalls {
				t.Fatalf("executor calls = %d, want %d", calls, test.wantCalls)
			}
			if stdout.String() != test.wantOutput {
				t.Fatalf("stdout = %q, want %q", stdout.String(), test.wantOutput)
			}
		})
	}
}

func TestParseEnrollInvocationRejectsUnknownDuplicateAndMismatchedTrustFlags(t *testing.T) {
	tests := [][]string{
		append(enrollmentCommandArguments(), "--unknown", "value"),
		append(enrollmentCommandArguments(), "--tenant-id", testSupervisorTenant),
		func() []string {
			args := enrollmentCommandArguments()
			args[15] = "other.example.test"
			return args
		}(),
	}
	for _, args := range tests {
		if _, err := parseEnrollInvocation(args); err == nil {
			t.Fatalf("accepted invalid arguments: %v", args)
		}
	}
}

func enrollmentCommandArguments() []string {
	return []string{
		"--tenant-id", testSupervisorTenant,
		"--host-id", testSupervisorHost,
		"--connector-id", testSupervisorConnector,
		"--generation", "1",
		"--spec-revision", "1",
		"--request-id", "0197f1f0-0000-7000-8000-000000000004",
		"--enrollment-url", "https://enroll.example.test:9443",
		"--enrollment-server-name", "enroll.example.test",
		"--enrollment-root-ca-file", "enrollment-ca.pem",
		"--control-url", "https://control.example.test:9444",
		"--control-server-name", "control.example.test",
		"--control-root-ca-file", "control-ca.pem",
	}
}
