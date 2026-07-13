package vnextconnector

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	// CurrentStateSchemaVersion is the only durable Connector state schema
	// understood by this adapter.
	CurrentStateSchemaVersion uint32 = 1

	// MaxStateJSONBytes bounds both persisted and loaded state, including the
	// trailing newline written by Save.
	MaxStateJSONBytes = 64 * 1024

	maxStateJSONSafeInteger  = uint64(9_007_199_254_740_991)
	maxStateConfigEntries    = 64
	maxStateConfigKeyBytes   = 64
	maxStateConfigValueBytes = 1024
)

var (
	ErrInvalidState            = errors.New("invalid connector state")
	ErrStateTooLarge           = errors.New("connector state exceeds 64 KiB")
	ErrStateSymlink            = errors.New("connector state target is a symlink")
	ErrStateIdentityMismatch   = errors.New("connector state identity mismatch")
	ErrStateGenerationMismatch = errors.New("connector state generation mismatch")
)

// DesiredState is the last durably applied server desired state.
type DesiredState string

const (
	DesiredStateRunning  DesiredState = "running"
	DesiredStateDraining DesiredState = "draining"
	DesiredStateStopped  DesiredState = "stopped"
)

// StateDigest keeps the in-memory API byte-oriented while persisting an
// auditable lowercase hexadecimal value rather than JSON base64.
type StateDigest []byte

func (digest StateDigest) MarshalJSON() ([]byte, error) {
	if len(digest) != 0 && len(digest) != 32 {
		return nil, invalidStatef("command digest has %d bytes, want 32", len(digest))
	}
	return json.Marshal(hex.EncodeToString(digest))
}

func (digest *StateDigest) UnmarshalJSON(encoded []byte) error {
	var value string
	if err := json.Unmarshal(encoded, &value); err != nil {
		return invalidStatef("command digest is not a JSON string")
	}
	if value == "" {
		*digest = nil
		return nil
	}
	if len(value) != 64 || strings.ToLower(value) != value {
		return invalidStatef("command digest is not 64 lowercase hexadecimal characters")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return invalidStatef("command digest is not an exact SHA-256 value")
	}
	*digest = StateDigest(decoded)
	return nil
}

// State is the durable, instance-scoped resume boundary for one Connector.
// The two command digests identify the exact command at the contiguous cursor.
type State struct {
	SchemaVersion            uint32            `json:"schema_version"`
	TenantID                 string            `json:"tenant_id"`
	ConnectorID              string            `json:"connector_id"`
	ConnectorGeneration      uint64            `json:"connector_generation"`
	AppliedConfigRevision    uint64            `json:"applied_config_revision"`
	AppliedCommandSequence   uint64            `json:"applied_command_sequence"`
	LastEncodedCommandDigest StateDigest       `json:"last_encoded_command_digest"`
	LastPayloadDigest        StateDigest       `json:"last_payload_digest"`
	DesiredState             DesiredState      `json:"desired_state"`
	AdapterConfig            map[string]string `json:"adapter_config"`
	RuntimeConfig            map[string]string `json:"runtime_config"`
	StableErrorCode          string            `json:"stable_error_code"`
}

func (state *State) UnmarshalJSON(encoded []byte) error {
	type wireState struct {
		SchemaVersion            *uint32            `json:"schema_version"`
		TenantID                 *string            `json:"tenant_id"`
		ConnectorID              *string            `json:"connector_id"`
		ConnectorGeneration      *uint64            `json:"connector_generation"`
		AppliedConfigRevision    *uint64            `json:"applied_config_revision"`
		AppliedCommandSequence   *uint64            `json:"applied_command_sequence"`
		LastEncodedCommandDigest *StateDigest       `json:"last_encoded_command_digest"`
		LastPayloadDigest        *StateDigest       `json:"last_payload_digest"`
		DesiredState             *DesiredState      `json:"desired_state"`
		AdapterConfig            *map[string]string `json:"adapter_config"`
		RuntimeConfig            *map[string]string `json:"runtime_config"`
		StableErrorCode          *string            `json:"stable_error_code"`
	}

	var wire wireState
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	if wire.SchemaVersion == nil || wire.TenantID == nil || wire.ConnectorID == nil ||
		wire.ConnectorGeneration == nil || wire.AppliedConfigRevision == nil ||
		wire.AppliedCommandSequence == nil || wire.LastEncodedCommandDigest == nil ||
		wire.LastPayloadDigest == nil || wire.DesiredState == nil || wire.AdapterConfig == nil ||
		wire.RuntimeConfig == nil || wire.StableErrorCode == nil {
		return invalidStatef("state JSON has a missing or null field")
	}
	*state = State{
		SchemaVersion:            *wire.SchemaVersion,
		TenantID:                 *wire.TenantID,
		ConnectorID:              *wire.ConnectorID,
		ConnectorGeneration:      *wire.ConnectorGeneration,
		AppliedConfigRevision:    *wire.AppliedConfigRevision,
		AppliedCommandSequence:   *wire.AppliedCommandSequence,
		LastEncodedCommandDigest: append(StateDigest(nil), (*wire.LastEncodedCommandDigest)...),
		LastPayloadDigest:        append(StateDigest(nil), (*wire.LastPayloadDigest)...),
		DesiredState:             *wire.DesiredState,
		AdapterConfig:            cloneStringMap(*wire.AdapterConfig),
		RuntimeConfig:            cloneStringMap(*wire.RuntimeConfig),
		StableErrorCode:          *wire.StableErrorCode,
	}
	return nil
}

// StateIdentity binds a state file and its command cursor to one immutable
// tenant/Connector generation.
type StateIdentity struct {
	TenantID            string
	ConnectorID         string
	ConnectorGeneration uint64
}

// StateStore serializes access to one durable state file. Callers should keep
// one store per Connector process.
type StateStore struct {
	path     string
	identity StateIdentity
	mu       sync.Mutex
}

func NewStateStore(path string, identity StateIdentity) (*StateStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, invalidStatef("state path is empty")
	}
	if err := validateStateIdentity(identity); err != nil {
		return nil, err
	}
	return &StateStore{path: filepath.Clean(path), identity: identity}, nil
}

func (store *StateStore) Load() (State, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	if err := validateStateTarget(store.path, true); err != nil {
		return State{}, err
	}
	file, err := openStateFile(store.path)
	if err != nil {
		return State{}, fmt.Errorf("open connector state: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return State{}, fmt.Errorf("stat opened connector state: %w", err)
	}
	if err := validateStateFileInfo(info); err != nil {
		return State{}, err
	}

	encoded, err := io.ReadAll(io.LimitReader(file, MaxStateJSONBytes+1))
	if err != nil {
		return State{}, fmt.Errorf("read connector state: %w", err)
	}
	if len(encoded) > MaxStateJSONBytes {
		return State{}, ErrStateTooLarge
	}
	if !utf8.Valid(encoded) {
		return State{}, invalidStatef("state JSON is not valid UTF-8")
	}
	if err := validateUniqueJSONFields(encoded); err != nil {
		return State{}, err
	}

	var state State
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return State{}, invalidStatef("decode state JSON: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return State{}, invalidStatef("state JSON contains a trailing value")
		}
		return State{}, invalidStatef("decode trailing state JSON: %v", err)
	}
	if err := validateState(state); err != nil {
		return State{}, err
	}
	if err := store.match(state); err != nil {
		return State{}, err
	}
	return state, nil
}

func (store *StateStore) Save(state State) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	state = cloneState(state)
	if err := validateState(state); err != nil {
		return err
	}
	if err := store.match(state); err != nil {
		return err
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return invalidStatef("encode state JSON: %v", err)
	}
	encoded = append(encoded, '\n')
	if len(encoded) > MaxStateJSONBytes {
		return ErrStateTooLarge
	}
	if err := validateStateTarget(store.path, false); err != nil {
		return err
	}

	directory := filepath.Dir(store.path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(store.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary connector state: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("restrict temporary connector state permissions: %w", err)
	}
	if err := writeAll(temporary, encoded); err != nil {
		return fmt.Errorf("write temporary connector state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary connector state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary connector state: %w", err)
	}

	// Recheck immediately before replacement. Rename/MoveFileEx replaces the
	// link itself rather than following it, but an existing link is rejected so
	// configuration mistakes and hostile paths fail visibly.
	if err := validateStateTarget(store.path, false); err != nil {
		return err
	}
	if err := replaceStateFile(temporaryPath, store.path); err != nil {
		return fmt.Errorf("replace connector state: %w", err)
	}
	committed = true
	if err := syncStateParent(directory); err != nil {
		return fmt.Errorf("sync connector state directory: %w", err)
	}
	return nil
}

func (store *StateStore) match(state State) error {
	if state.TenantID != store.identity.TenantID || state.ConnectorID != store.identity.ConnectorID {
		return fmt.Errorf("%w: expected tenant %s connector %s", ErrStateIdentityMismatch, store.identity.TenantID, store.identity.ConnectorID)
	}
	if state.ConnectorGeneration != store.identity.ConnectorGeneration {
		return fmt.Errorf("%w: expected %d, got %d", ErrStateGenerationMismatch, store.identity.ConnectorGeneration, state.ConnectorGeneration)
	}
	return nil
}

func validateStateTarget(path string, mustExist bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !mustExist {
			return nil
		}
		return fmt.Errorf("inspect connector state: %w", err)
	}
	return validateStateFileInfo(info)
}

func validateStateFileInfo(info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrStateSymlink
	}
	if !info.Mode().IsRegular() {
		return invalidStatef("state target is not a regular file")
	}
	if info.Size() > MaxStateJSONBytes {
		return ErrStateTooLarge
	}
	return validateStateFilePermissions(info)
}

func validateStateIdentity(identity StateIdentity) error {
	if !isCanonicalUUIDv7(identity.TenantID) {
		return invalidStatef("tenant_id is not a canonical UUIDv7")
	}
	if !isCanonicalUUIDv7(identity.ConnectorID) {
		return invalidStatef("connector_id is not a canonical UUIDv7")
	}
	if identity.ConnectorGeneration == 0 || identity.ConnectorGeneration > maxStateJSONSafeInteger {
		return invalidStatef("connector_generation is outside the positive JSON-safe range")
	}
	return nil
}

func validateState(state State) error {
	if state.SchemaVersion != CurrentStateSchemaVersion {
		return invalidStatef("unsupported schema_version %d", state.SchemaVersion)
	}
	if err := validateStateIdentity(StateIdentity{
		TenantID:            state.TenantID,
		ConnectorID:         state.ConnectorID,
		ConnectorGeneration: state.ConnectorGeneration,
	}); err != nil {
		return err
	}
	if state.AppliedConfigRevision > maxStateJSONSafeInteger {
		return invalidStatef("applied_config_revision exceeds the JSON-safe range")
	}
	if state.AppliedCommandSequence > maxStateJSONSafeInteger {
		return invalidStatef("applied_command_sequence exceeds the JSON-safe range")
	}
	if state.AppliedCommandSequence == 0 {
		if len(state.LastEncodedCommandDigest) != 0 || len(state.LastPayloadDigest) != 0 {
			return invalidStatef("zero command cursor carries command digests")
		}
	} else if len(state.LastEncodedCommandDigest) != 32 || len(state.LastPayloadDigest) != 32 {
		return invalidStatef("positive command cursor requires exact 32-byte command digests")
	}
	if !validDesiredState(state.DesiredState) {
		return invalidStatef("desired_state %q is unsupported", state.DesiredState)
	}
	if err := validateStateConfig("adapter_config", state.AdapterConfig); err != nil {
		return err
	}
	if err := validateStateConfig("runtime_config", state.RuntimeConfig); err != nil {
		return err
	}
	if state.StableErrorCode != "" && !isStableErrorCode(state.StableErrorCode) {
		return invalidStatef("stable_error_code is malformed")
	}
	return nil
}

func validateStateConfig(field string, entries map[string]string) error {
	if entries == nil {
		return invalidStatef("%s is missing", field)
	}
	if len(entries) > maxStateConfigEntries {
		return invalidStatef("%s has more than %d entries", field, maxStateConfigEntries)
	}
	for key, value := range entries {
		if !isStableConfigKey(key) {
			return invalidStatef("%s contains malformed key", field)
		}
		if len(value) > maxStateConfigValueBytes || !utf8.ValidString(value) || containsControl(value) {
			return invalidStatef("%s contains malformed value", field)
		}
	}
	return nil
}

func validDesiredState(state DesiredState) bool {
	switch state {
	case DesiredStateRunning, DesiredStateDraining, DesiredStateStopped:
		return true
	default:
		return false
	}
}

func isCanonicalUUIDv7(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || value[14] != '7' {
		return false
	}
	if !strings.ContainsRune("89ab", rune(value[19])) {
		return false
	}
	compact := strings.ReplaceAll(value, "-", "")
	if len(compact) != 32 || strings.ToLower(compact) != compact {
		return false
	}
	decoded, err := hex.DecodeString(compact)
	return err == nil && len(decoded) == 16
}

func isStableConfigKey(value string) bool {
	if len(value) == 0 || len(value) > maxStateConfigKeyBytes ||
		!((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= '0' && value[0] <= '9')) {
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

func isStableErrorCode(value string) bool {
	if len(value) < 3 || len(value) > 64 || value[0] < 'A' || value[0] > 'Z' {
		return false
	}
	previousUnderscore := false
	for _, character := range []byte(value) {
		isUpper := character >= 'A' && character <= 'Z'
		isDigit := character >= '0' && character <= '9'
		if isUpper || isDigit {
			previousUnderscore = false
			continue
		}
		if character != '_' || previousUnderscore {
			return false
		}
		previousUnderscore = true
	}
	return !previousUnderscore
}

func containsControl(value string) bool {
	for _, character := range value {
		if character <= 0x1f || (character >= 0x7f && character <= 0x9f) {
			return true
		}
	}
	return false
}

func writeAll(file *os.File, value []byte) error {
	for len(value) != 0 {
		written, err := file.Write(value)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		value = value[written:]
	}
	return nil
}

func cloneState(state State) State {
	state.LastEncodedCommandDigest = append(StateDigest(nil), state.LastEncodedCommandDigest...)
	state.LastPayloadDigest = append(StateDigest(nil), state.LastPayloadDigest...)
	state.AdapterConfig = cloneStringMap(state.AdapterConfig)
	state.RuntimeConfig = cloneStringMap(state.RuntimeConfig)
	return state
}

func cloneStringMap(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func validateUniqueJSONFields(encoded []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if err := validateUniqueJSONValue(decoder); err != nil {
		return invalidStatef("state JSON is not strict: %v", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return invalidStatef("state JSON contains a trailing value")
		}
		return invalidStatef("state JSON has invalid trailing data: %v", err)
	}
	return nil
}

func validateUniqueJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate object key %q", key)
			}
			seen[key] = struct{}{}
			if err := validateUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return errors.New("object is not closed")
		}
	case '[':
		for decoder.More() {
			if err := validateUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return errors.New("array is not closed")
		}
	default:
		return errors.New("unexpected closing delimiter")
	}
	return nil
}

func invalidStatef(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidState, fmt.Sprintf(format, arguments...))
}
