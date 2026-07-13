package vnextconnector

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf8"

	controlv1 "github.com/YingSuiAI/dirextalk-connect/internal/vnextconnector/agentcontrolv1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

const (
	maxWireSafeInteger = uint64(9_007_199_254_740_991)
	maxCommandBytes    = 196_608

	commandPayloadDomain = "dirextalk.connector-command-payload.v1"
	encodedCommandDomain = "dirextalk.connector-encoded-command.v1"
)

// CommandIdentity is the immutable fence against which a durable command is
// checked before any local state is changed.
type CommandIdentity struct {
	ConnectorGeneration uint64
	SpecRevision        uint64
}

// CommandCursor contains the last locally committed command. The two digests
// make the one allowed crash replay (local commit before remote ACK) exact.
type CommandCursor struct {
	Sequence      uint64
	EncodedDigest [sha256.Size]byte
	PayloadDigest [sha256.Size]byte
}

type CommandKind uint8

const (
	CommandKindApplyConfig CommandKind = iota + 1
	CommandKindRotateCredential
	CommandKindCloseStream
)

// VerifiedCommand retains only authenticated, bounded command material. It is
// safe to use for a durable state transition; it intentionally has no String
// method because configuration values are redacted at logging boundaries.
type VerifiedCommand struct {
	Command       *controlv1.DurableCommand
	Kind          CommandKind
	EncodedDigest [sha256.Size]byte
	PayloadDigest [sha256.Size]byte
	Replay        bool
	AdapterConfig map[string]string
	RuntimeConfig map[string]string
}

// VerifyDurableCommand verifies the frozen exact-byte commitments, the
// generation/spec fence, the contiguous cursor, and the closed command value
// contract. Unknown wrapper fields remain covered by the encoded digest.
func VerifyDurableCommand(
	frame *controlv1.DurableCommandFrame,
	identity CommandIdentity,
	cursor CommandCursor,
) (*VerifiedCommand, error) {
	if frame == nil || len(frame.EncodedCommand) == 0 || len(frame.EncodedCommand) > maxCommandBytes {
		return nil, errors.New("vnext connector: invalid durable command size")
	}
	if identity.ConnectorGeneration == 0 || identity.ConnectorGeneration > maxWireSafeInteger ||
		identity.SpecRevision == 0 || identity.SpecRevision > maxWireSafeInteger {
		return nil, errors.New("vnext connector: invalid command identity")
	}

	encodedDigest := domainCommit(encodedCommandDomain, frame.EncodedCommand)
	if !equalDigest(frame.EncodedCommandDigest, encodedDigest) {
		return nil, errors.New("vnext connector: encoded command digest mismatch")
	}
	payloadBytes, err := exactCommandPayload(frame.EncodedCommand)
	if err != nil {
		return nil, err
	}
	payloadDigest := domainCommit(commandPayloadDomain, payloadBytes)

	command := &controlv1.DurableCommand{}
	if err := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(frame.EncodedCommand, command); err != nil {
		return nil, errors.New("vnext connector: invalid durable command encoding")
	}
	if !equalDigest(command.PayloadDigest, payloadDigest) {
		return nil, errors.New("vnext connector: command payload digest mismatch")
	}
	if !canonicalUUIDv7(command.OperationId) {
		return nil, errors.New("vnext connector: invalid command operation id")
	}
	if command.ConnectorGeneration != identity.ConnectorGeneration ||
		command.SpecRevision == 0 || command.SpecRevision > maxWireSafeInteger {
		return nil, errors.New("vnext connector: stale command fence")
	}
	if command.CommandSequence == 0 || command.CommandSequence > maxWireSafeInteger {
		return nil, errors.New("vnext connector: invalid command sequence")
	}

	replay := false
	switch {
	case cursor.Sequence < maxWireSafeInteger && command.CommandSequence == cursor.Sequence+1:
		// New contiguous command.
	case cursor.Sequence > 0 && command.CommandSequence == cursor.Sequence:
		if subtle.ConstantTimeCompare(cursor.EncodedDigest[:], encodedDigest[:]) != 1 ||
			subtle.ConstantTimeCompare(cursor.PayloadDigest[:], payloadDigest[:]) != 1 {
			return nil, errors.New("vnext connector: command replay digest mismatch")
		}
		replay = true
	default:
		return nil, errors.New("vnext connector: non-contiguous command sequence")
	}

	verified := &VerifiedCommand{
		Command:       command,
		EncodedDigest: encodedDigest,
		PayloadDigest: payloadDigest,
		Replay:        replay,
	}
	switch payload := command.Command.(type) {
	case *controlv1.DurableCommand_ApplyConfig:
		adapterConfig, runtimeConfig, err := validateApplyConfig(payload.ApplyConfig)
		if err != nil {
			return nil, err
		}
		verified.Kind = CommandKindApplyConfig
		verified.AdapterConfig = adapterConfig
		verified.RuntimeConfig = runtimeConfig
	case *controlv1.DurableCommand_RotateCredential:
		if err := validateRotateCredential(payload.RotateCredential); err != nil {
			return nil, err
		}
		verified.Kind = CommandKindRotateCredential
	case *controlv1.DurableCommand_CloseStream:
		if err := validateCloseStream(payload.CloseStream); err != nil {
			return nil, err
		}
		verified.Kind = CommandKindCloseStream
	default:
		return nil, errors.New("vnext connector: missing durable command payload")
	}
	if verified.Kind == CommandKindApplyConfig {
		apply := command.GetApplyConfig()
		if command.SpecRevision == maxWireSafeInteger || apply.ConfigRevision != command.SpecRevision+1 {
			return nil, errors.New("vnext connector: non-contiguous config revision")
		}
		if replay {
			if apply.ConfigRevision != identity.SpecRevision {
				return nil, errors.New("vnext connector: stale replayed command fence")
			}
		} else if command.SpecRevision != identity.SpecRevision {
			return nil, errors.New("vnext connector: stale command fence")
		}
	} else if command.SpecRevision != identity.SpecRevision {
		return nil, errors.New("vnext connector: stale command fence")
	}
	return verified, nil
}

func domainCommit(domain string, parts ...[]byte) [sha256.Size]byte {
	digest := sha256.New()
	writeLP := func(value []byte) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = digest.Write(length[:])
		_, _ = digest.Write(value)
	}
	writeLP([]byte(domain))
	for _, part := range parts {
		writeLP(part)
	}
	var result [sha256.Size]byte
	copy(result[:], digest.Sum(nil))
	return result
}

func equalDigest(value []byte, expected [sha256.Size]byte) bool {
	return len(value) == sha256.Size && subtle.ConstantTimeCompare(value, expected[:]) == 1
}

func exactCommandPayload(encoded []byte) ([]byte, error) {
	remaining := encoded
	var payload []byte
	commandFields := 0
	for len(remaining) > 0 {
		number, wireType, tagLength := protowire.ConsumeTag(remaining)
		if tagLength < 0 {
			return nil, errors.New("vnext connector: malformed durable command tag")
		}
		remaining = remaining[tagLength:]
		if number >= 10 && number <= 12 {
			if wireType != protowire.BytesType {
				return nil, errors.New("vnext connector: malformed durable command payload")
			}
			value, valueLength := protowire.ConsumeBytes(remaining)
			if valueLength < 0 || len(value) == 0 {
				return nil, errors.New("vnext connector: malformed durable command payload")
			}
			commandFields++
			if commandFields != 1 {
				return nil, errors.New("vnext connector: multiple durable command payloads")
			}
			payload = append([]byte(nil), value...)
			remaining = remaining[valueLength:]
			continue
		}
		valueLength := protowire.ConsumeFieldValue(number, wireType, remaining)
		if valueLength < 0 {
			return nil, errors.New("vnext connector: malformed durable command field")
		}
		remaining = remaining[valueLength:]
	}
	if commandFields != 1 {
		return nil, errors.New("vnext connector: missing durable command payload")
	}
	return payload, nil
}

func validateApplyConfig(value *controlv1.ApplyConfig) (map[string]string, map[string]string, error) {
	if value == nil || value.ConfigRevision == 0 || value.ConfigRevision > maxWireSafeInteger {
		return nil, nil, errors.New("vnext connector: invalid config revision")
	}
	switch value.DesiredState {
	case controlv1.DesiredConnectorState_DESIRED_CONNECTOR_STATE_RUNNING,
		controlv1.DesiredConnectorState_DESIRED_CONNECTOR_STATE_DRAINING,
		controlv1.DesiredConnectorState_DESIRED_CONNECTOR_STATE_STOPPED:
	default:
		return nil, nil, errors.New("vnext connector: invalid desired state")
	}
	adapter, err := validateConfigEntries(value.AdapterConfig)
	if err != nil {
		return nil, nil, err
	}
	runtime, err := validateConfigEntries(value.RuntimeConfig)
	if err != nil {
		return nil, nil, err
	}
	return adapter, runtime, nil
}

func validateConfigEntries(entries []*controlv1.ConfigEntry) (map[string]string, error) {
	if len(entries) > 64 {
		return nil, errors.New("vnext connector: too many config entries")
	}
	result := make(map[string]string, len(entries))
	previous := ""
	for _, entry := range entries {
		if entry == nil || !validLowerStableName(entry.Key, 64) || len(entry.Value) > 1024 ||
			!utf8.ValidString(entry.Value) || containsC0C1(entry.Value) ||
			!registeredConfigValue(entry.Key, entry.Value) ||
			(previous != "" && previous >= entry.Key) {
			return nil, errors.New("vnext connector: invalid config entry")
		}
		previous = entry.Key
		result[entry.Key] = entry.Value
	}
	return result, nil
}

func validateRotateCredential(value *controlv1.RotateCredential) error {
	if value == nil || len(value.RotationNonce) != sha256.Size ||
		value.SuccessorRevision == 0 || value.SuccessorRevision > maxWireSafeInteger ||
		value.DeadlineMillis == 0 || value.DeadlineMillis > maxWireSafeInteger {
		return errors.New("vnext connector: invalid credential rotation command")
	}
	return nil
}

func validateCloseStream(value *controlv1.CloseStream) error {
	if value == nil {
		return errors.New("vnext connector: invalid close stream command")
	}
	switch value.Reason {
	case controlv1.CloseStreamReason_CLOSE_STREAM_REASON_RECONNECT,
		controlv1.CloseStreamReason_CLOSE_STREAM_REASON_DRAINED,
		controlv1.CloseStreamReason_CLOSE_STREAM_REASON_REVOKED,
		controlv1.CloseStreamReason_CLOSE_STREAM_REASON_PROTOCOL_UPGRADE:
	default:
		return errors.New("vnext connector: invalid close stream reason")
	}
	if !validUpperSnakeCode(value.StableCode, 64) || len(value.RedactedDetail) > 512 ||
		!utf8.ValidString(value.RedactedDetail) || containsC0C1(value.RedactedDetail) {
		return errors.New("vnext connector: invalid close stream metadata")
	}
	return nil
}

func canonicalUUIDv7(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id.Version() == 7 && id.Variant() == uuid.RFC4122 && id.String() == value
}

func validLowerStableName(value string, maximum int) bool {
	if len(value) == 0 || len(value) > maximum || !((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= '0' && value[0] <= '9')) {
		return false
	}
	for _, character := range []byte(value) {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') {
			continue
		}
		switch character {
		case '.', '_', '-', '/', ':':
		default:
			return false
		}
	}
	return true
}

func validUpperSnakeCode(value string, maximum int) bool {
	if len(value) < 3 || len(value) > maximum || value[0] < 'A' || value[0] > 'Z' {
		return false
	}
	partLength := 0
	for _, character := range []byte(value) {
		if character == '_' {
			if partLength == 0 {
				return false
			}
			partLength = 0
			continue
		}
		if !((character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9')) {
			return false
		}
		partLength++
	}
	return partLength > 0
}

func containsC0C1(value string) bool {
	for _, character := range value {
		if character <= 0x1f || (character >= 0x7f && character <= 0x9f) {
			return true
		}
	}
	return false
}

func registeredConfigValue(key, value string) bool {
	switch key {
	case "adapter":
		switch value {
		case "codex-app-server", "openclaw-acp", "eino", "rig", "claude-code", "vendor-v1":
			return true
		}
	case "endpoint", "endpoint-profile":
		return value == "local" || value == "private" || value == "public"
	case "log-level":
		return value == "trace" || value == "debug" || value == "info" || value == "warn" || value == "error"
	case "max-concurrent-runs":
		var maximum uint32
		if _, err := fmt.Sscan(value, &maximum); err == nil {
			return maximum >= 1 && maximum <= 4096 && fmt.Sprint(maximum) == value
		}
	case "model":
		return value == "agent-v1"
	case "offline-policy":
		return value == "queue" || value == "reject"
	case "policy-id":
		return value == "policy-v1"
	case "profile":
		return value == "safe" || value == "default"
	case "shutdown":
		return value == "graceful" || value == "immediate"
	case "workspace-mode":
		return value == "read-only" || value == "workspace-write"
	}
	return false
}
