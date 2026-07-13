package vnextconnector

import (
	"bytes"
	"testing"

	controlv1 "github.com/YingSuiAI/dirextalk-connect/internal/vnextconnector/agentcontrolv1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestVerifyDurableCommandAcceptsNewAndExactCrashReplay(t *testing.T) {
	frame := applyConfigFrame(t, 1)
	identity := CommandIdentity{ConnectorGeneration: 7, SpecRevision: 12}

	verified, err := VerifyDurableCommand(frame, identity, CommandCursor{})
	if err != nil {
		t.Fatalf("verify new command: %v", err)
	}
	if verified.Kind != CommandKindApplyConfig || verified.Replay {
		t.Fatalf("new command = kind %d replay %t", verified.Kind, verified.Replay)
	}
	if got := verified.AdapterConfig["adapter"]; got != "codex-app-server" {
		t.Fatalf("adapter = %q", got)
	}

	cursor := CommandCursor{
		Sequence:      1,
		EncodedDigest: verified.EncodedDigest,
		PayloadDigest: verified.PayloadDigest,
	}
	replayed, err := VerifyDurableCommand(frame, CommandIdentity{ConnectorGeneration: 7, SpecRevision: 13}, cursor)
	if err != nil {
		t.Fatalf("verify crash replay: %v", err)
	}
	if !replayed.Replay {
		t.Fatal("committed command was not recognized as an exact replay")
	}
	nextFrame := applyConfigFrameAtRevision(t, 2, 13, controlv1.DesiredConnectorState_DESIRED_CONNECTOR_STATE_DRAINING)
	next, err := VerifyDurableCommand(nextFrame, CommandIdentity{ConnectorGeneration: 7, SpecRevision: 13}, cursor)
	if err != nil {
		t.Fatalf("verify next revision: %v", err)
	}
	if next.Replay || next.Command.SpecRevision != 13 || next.Command.GetApplyConfig().ConfigRevision != 14 {
		t.Fatalf("next command = %+v", next.Command)
	}

	mutated := proto.Clone(frame).(*controlv1.DurableCommandFrame)
	mutated.EncodedCommand = append([]byte(nil), frame.EncodedCommand...)
	mutated.EncodedCommand[len(mutated.EncodedCommand)-1] ^= 1
	if _, err := VerifyDurableCommand(mutated, identity, cursor); err == nil {
		t.Fatal("mutated command unexpectedly verified")
	}
}

func TestVerifyDurableCommandRejectsMultipleOneofPayloads(t *testing.T) {
	frame := applyConfigFrame(t, 1)
	closePayload, err := proto.Marshal(&controlv1.CloseStream{
		Reason:     controlv1.CloseStreamReason_CLOSE_STREAM_REASON_RECONNECT,
		StableCode: "RECONNECT",
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded := append([]byte(nil), frame.EncodedCommand...)
	encoded = protowire.AppendTag(encoded, 12, protowire.BytesType)
	encoded = protowire.AppendBytes(encoded, closePayload)
	digest := domainCommit(encodedCommandDomain, encoded)
	frame.EncodedCommand = encoded
	frame.EncodedCommandDigest = digest[:]

	if _, err := VerifyDurableCommand(frame, CommandIdentity{
		ConnectorGeneration: 7,
		SpecRevision:        12,
	}, CommandCursor{}); err == nil {
		t.Fatal("multiple oneof payloads unexpectedly verified")
	}
}

func TestDomainCommitUsesFrozenLengthPrefixFraming(t *testing.T) {
	left := domainCommit("ab", []byte("c"))
	right := domainCommit("a", []byte("bc"))
	if bytes.Equal(left[:], right[:]) {
		t.Fatal("length-prefixed transcripts collided")
	}
}

func applyConfigFrame(t *testing.T, sequence uint64) *controlv1.DurableCommandFrame {
	return applyConfigFrameForState(t, sequence, controlv1.DesiredConnectorState_DESIRED_CONNECTOR_STATE_RUNNING)
}

func applyConfigFrameForState(t *testing.T, sequence uint64, desired controlv1.DesiredConnectorState) *controlv1.DurableCommandFrame {
	return applyConfigFrameAtRevision(t, sequence, 12, desired)
}

func applyConfigFrameAtRevision(t *testing.T, sequence, specRevision uint64, desired controlv1.DesiredConnectorState) *controlv1.DurableCommandFrame {
	t.Helper()
	payload := &controlv1.ApplyConfig{
		ConfigRevision: specRevision + 1,
		DesiredState:   desired,
		AdapterConfig: []*controlv1.ConfigEntry{
			{Key: "adapter", Value: "codex-app-server"},
			{Key: "profile", Value: "default"},
		},
		RuntimeConfig: []*controlv1.ConfigEntry{
			{Key: "log-level", Value: "info"},
			{Key: "max-concurrent-runs", Value: "3"},
			{Key: "workspace-mode", Value: "workspace-write"},
		},
	}
	payloadBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	payloadDigest := domainCommit(commandPayloadDomain, payloadBytes)
	command := &controlv1.DurableCommand{
		CommandSequence:     sequence,
		OperationId:         "01890f00-0000-7000-8000-000000000001",
		ConnectorGeneration: 7,
		SpecRevision:        specRevision,
		PayloadDigest:       payloadDigest[:],
		Command: &controlv1.DurableCommand_ApplyConfig{
			ApplyConfig: payload,
		},
	}
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	encodedDigest := domainCommit(encodedCommandDomain, encoded)
	return &controlv1.DurableCommandFrame{
		EncodedCommand:       encoded,
		EncodedCommandDigest: encodedDigest[:],
	}
}
