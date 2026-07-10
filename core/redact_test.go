package core

import (
	"strings"
	"testing"
)

func TestRedactArgs_FlagValue(t *testing.T) {
	args := []string{"--api-key", "sk-secret-123", "--verbose"}
	out := RedactArgs(args)

	if out[1] != "***" {
		t.Errorf("expected redacted, got %q", out[1])
	}
	if out[2] != "--verbose" {
		t.Errorf("non-sensitive arg modified: %q", out[2])
	}
}

func TestRedactArgs_EqualFormat(t *testing.T) {
	args := []string{"--api-key=sk-secret-123", "--verbose"}
	out := RedactArgs(args)

	if !strings.HasPrefix(out[0], "--api-key=") || !strings.HasSuffix(out[0], "***") {
		t.Errorf("expected --api-key=***, got %q", out[0])
	}
}

func TestRedactArgs_MultipleFlags(t *testing.T) {
	args := []string{"--token", "tok-123", "--secret", "s3cr3t", "--model", "gpt-4"}
	out := RedactArgs(args)

	if out[1] != "***" {
		t.Errorf("--token value not redacted: %q", out[1])
	}
	if out[3] != "***" {
		t.Errorf("--secret value not redacted: %q", out[3])
	}
	if out[5] != "gpt-4" {
		t.Errorf("--model value should not be redacted: %q", out[5])
	}
}

func TestRedactArgs_NoModifyOriginal(t *testing.T) {
	args := []string{"--api-key", "sk-secret"}
	_ = RedactArgs(args)
	if args[1] != "sk-secret" {
		t.Error("original args should not be modified")
	}
}

func TestRedactArgs_ShortFlag(t *testing.T) {
	args := []string{"-k", "my-key"}
	out := RedactArgs(args)
	if out[1] != "***" {
		t.Errorf("-k value not redacted: %q", out[1])
	}
}

func TestRedactArgs_Empty(t *testing.T) {
	out := RedactArgs(nil)
	if len(out) != 0 {
		t.Errorf("expected empty, got %v", out)
	}
}

func TestRedactArgs_MasksInlineMCPAuthorization(t *testing.T) {
	secret := `mcp_servers."dirextalk".http_headers={Authorization="Bearer agent-token"}`
	out := RedactArgs([]string{"-c", secret, "--model", "gpt-5"})

	if out[1] != "***" {
		t.Fatalf("inline MCP authorization not redacted: %q", out[1])
	}
	if strings.Contains(strings.Join(out, " "), "agent-token") {
		t.Fatalf("redacted args still contain bearer token: %#v", out)
	}
	if out[3] != "gpt-5" {
		t.Fatalf("non-sensitive arg modified: %#v", out)
	}
}
