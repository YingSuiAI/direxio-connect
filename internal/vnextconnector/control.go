package vnextconnector

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"math/rand/v2"
	"strconv"
	"time"

	controlv1 "github.com/YingSuiAI/dirextalk-connect/internal/vnextconnector/agentcontrolv1"
)

const (
	maxAgentControlMessageBytes = 262_144
	protocolMajor               = 1
	protocolMinor               = 1
	stablePayloadUnavailable    = "RUN_PAYLOAD_UNAVAILABLE"
)

var errConnectorStopped = errors.New("vnext connector stopped by desired state")

var errServerRequestedReconnect = errors.New("vnext connector reconnect requested by server")

// ControlStream is the small generated gRPC surface used by the connector.
// The interface keeps the state machine transport-testable without a second
// wire abstraction.
type ControlStream interface {
	Send(*controlv1.ClientFrame) error
	Recv() (*controlv1.ServerFrame, error)
}

type StreamOpener func(context.Context) (ControlStream, error)

// ClientConfig contains no private credential material. Credential loading and
// mTLS dialing happen at the process boundary and provide a StreamOpener.
type ClientConfig struct {
	TenantID              string
	ConnectorID           string
	HostID                string
	BootID                string
	ConnectorGeneration   uint64
	SpecRevision          uint64
	RuntimeKind           string
	RuntimeVersion        string
	Adapter               string
	Profile               string
	OfflinePolicy         string
	AdapterBuildDigest    [sha256.Size]byte
	MaximumConcurrentRuns uint32
	MaximumQueueDepth     uint32
	StateStore            *StateStore
}

// Client owns the connector-side lease, heartbeat, command cursor, and retry
// state machine. A schema-v2 process creates exactly one Client.
type Client struct {
	config ClientConfig
}

func NewClient(config ClientConfig) (*Client, error) {
	if !canonicalUUIDv7(config.TenantID) || !canonicalUUIDv7(config.ConnectorID) ||
		!canonicalUUIDv7(config.HostID) || !canonicalUUIDv7(config.BootID) {
		return nil, errors.New("vnext connector: client identity must use canonical UUIDv7 values")
	}
	if config.ConnectorGeneration == 0 || config.ConnectorGeneration > maxWireSafeInteger ||
		config.SpecRevision == 0 || config.SpecRevision > maxWireSafeInteger {
		return nil, errors.New("vnext connector: client fence is outside the JSON-safe range")
	}
	if !validRuntimeKind(config.RuntimeKind) || len(config.RuntimeVersion) == 0 || len(config.RuntimeVersion) > 128 ||
		containsC0C1(config.RuntimeVersion) || config.StateStore == nil {
		return nil, errors.New("vnext connector: invalid runtime claims")
	}
	if subtle.ConstantTimeCompare(config.AdapterBuildDigest[:], make([]byte, sha256.Size)) == 1 {
		return nil, errors.New("vnext connector: adapter build digest is empty")
	}
	if config.MaximumConcurrentRuns == 0 || config.MaximumConcurrentRuns > 4_096 {
		return nil, errors.New("vnext connector: invalid maximum concurrent runs")
	}
	if config.MaximumQueueDepth == 0 || config.MaximumQueueDepth > 1_000_000 {
		return nil, errors.New("vnext connector: invalid maximum queue depth")
	}
	if config.Adapter != runtimeAdapter(config.RuntimeKind) || config.Profile == "" ||
		(config.OfflinePolicy != "queue" && config.OfflinePolicy != "reject") {
		return nil, errors.New("vnext connector: invalid effective runtime configuration")
	}
	return &Client{config: config}, nil
}

// Run reconnects transient stream failures with bounded jitter. Authentication,
// protocol, and durable-state failures are permanent and returned immediately.
func (client *Client) Run(ctx context.Context, open StreamOpener) error {
	if open == nil {
		return errors.New("vnext connector: missing control stream opener")
	}
	state, err := client.loadOrInitializeState()
	if err != nil {
		return err
	}
	backoff := 500 * time.Millisecond
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		sessionCtx, cancel := context.WithCancel(ctx)
		stream, openErr := open(sessionCtx)
		if openErr == nil {
			state, openErr = client.runSession(sessionCtx, stream, state)
		} else {
			openErr = classifyTransportError("open control stream", openErr)
		}
		cancel()
		if errors.Is(openErr, errConnectorStopped) || ctx.Err() != nil {
			return nil
		}
		if isPermanentControlError(openErr) {
			return openErr
		}
		delay := jitter(backoff)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		if backoff < 15*time.Second {
			backoff *= 2
			if backoff > 15*time.Second {
				backoff = 15 * time.Second
			}
		}
	}
}

func (client *Client) loadOrInitializeState() (State, error) {
	state, err := client.config.StateStore.Load()
	if err == nil {
		if state.AppliedConfigRevision == 0 {
			return State{}, permanentError(errors.New("vnext connector: durable config revision is zero"))
		}
		if state.AppliedConfigRevision < client.config.SpecRevision {
			return State{}, permanentError(errors.New("vnext connector: durable config revision is behind the configured fence"))
		}
		if err := client.validateAppliedConfiguration(state.AdapterConfig, state.RuntimeConfig); err != nil {
			return State{}, permanentError(err)
		}
		return state, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return State{}, permanentError(err)
	}
	state = State{
		SchemaVersion:         CurrentStateSchemaVersion,
		TenantID:              client.config.TenantID,
		ConnectorID:           client.config.ConnectorID,
		ConnectorGeneration:   client.config.ConnectorGeneration,
		AppliedConfigRevision: client.config.SpecRevision,
		DesiredState:          DesiredStateRunning,
		StableErrorCode:       stablePayloadUnavailable,
		AdapterConfig: map[string]string{
			"adapter": client.config.Adapter,
			"profile": client.config.Profile,
		},
		RuntimeConfig: map[string]string{
			"max-concurrent-runs": strconv.FormatUint(uint64(client.config.MaximumConcurrentRuns), 10),
			"offline-policy":      client.config.OfflinePolicy,
		},
	}
	if err := client.config.StateStore.Save(state); err != nil {
		return State{}, permanentError(err)
	}
	return state, nil
}

func (client *Client) runSession(ctx context.Context, stream ControlStream, state State) (State, error) {
	if stream == nil {
		return state, transientError(errors.New("vnext connector: nil control stream"))
	}
	if err := stream.Send(client.helloFrame(state)); err != nil {
		return state, classifyTransportError("send connector hello", err)
	}
	first, err := stream.Recv()
	if err != nil {
		return state, classifyTransportError("receive connect lease", err)
	}
	lease := first.GetConnectLease()
	if lease == nil || first.GetKind() == nil {
		return state, permanentError(errors.New("vnext connector: ConnectLease must be the first server frame"))
	}
	if err := client.validateLease(lease, state); err != nil {
		return state, permanentError(err)
	}
	// A command may have reached stable storage immediately before the prior
	// stream died, leaving the server cursor exactly one step behind. Reconcile
	// that replay before Ready so Ready never advertises a config revision the
	// server has not yet committed. Terminal replays are acknowledged and exit
	// without Ready because their ACK revokes the newly issued lease.
	if lease.AcknowledgedCommandSequence+1 == state.AppliedCommandSequence {
		replayFrame, err := stream.Recv()
		if err != nil {
			return state, classifyTransportError("receive durable command replay", err)
		}
		replay := replayFrame.GetDurableCommand()
		if replay == nil || replayFrame.GetKind() == nil {
			return state, permanentError(errors.New("vnext connector: durable command replay must follow a recoverable lease"))
		}
		state, err = client.applyDurableCommand(stream, lease.Fence, state, replay)
		if err != nil {
			if errors.Is(err, errServerRequestedReconnect) {
				return state, transientError(err)
			}
			return state, err
		}
	}
	if err := stream.Send(&controlv1.ClientFrame{Kind: &controlv1.ClientFrame_Ready{
		Ready: &controlv1.Ready{
			Fence:                  lease.Fence,
			AppliedConfigRevision:  state.AppliedConfigRevision,
			AppliedCommandSequence: state.AppliedCommandSequence,
		},
	}}); err != nil {
		return state, classifyTransportError("send connector ready", err)
	}
	if state.DesiredState == DesiredStateStopped && lease.AcknowledgedCommandSequence == state.AppliedCommandSequence {
		return state, errConnectorStopped
	}

	receive := make(chan receivedFrame, 1)
	go receiveControlFrames(ctx, stream, receive)
	heartbeatTicker := time.NewTicker(time.Duration(lease.HeartbeatIntervalMillis) * time.Millisecond)
	defer heartbeatTicker.Stop()
	leaseTimer := time.NewTimer(time.Duration(lease.ExpiresAtMillis-lease.IssuedAtMillis) * time.Millisecond)
	defer leaseTimer.Stop()
	heartbeatSequence := uint64(0)
	acknowledgedHeartbeat := uint64(0)

	for {
		select {
		case <-ctx.Done():
			return state, nil
		case <-leaseTimer.C:
			return state, transientError(errors.New("vnext connector: control lease expired"))
		case <-heartbeatTicker.C:
			if heartbeatSequence == maxWireSafeInteger {
				return state, permanentError(errors.New("vnext connector: heartbeat sequence exhausted"))
			}
			heartbeatSequence++
			if err := stream.Send(client.heartbeatFrame(lease.Fence, heartbeatSequence, state)); err != nil {
				return state, classifyTransportError("send connector heartbeat", err)
			}
		case received := <-receive:
			if received.err != nil {
				return state, classifyTransportError("receive control frame", received.err)
			}
			frame := received.frame
			if frame == nil || frame.GetKind() == nil {
				return state, permanentError(errors.New("vnext connector: empty server frame"))
			}
			switch kind := frame.Kind.(type) {
			case *controlv1.ServerFrame_HeartbeatAcknowledgement:
				ack := kind.HeartbeatAcknowledgement
				if ack == nil || ack.HeartbeatSequence != acknowledgedHeartbeat+1 || ack.HeartbeatSequence > heartbeatSequence ||
					ack.ObservedAtMillis > maxWireSafeInteger || ack.LeaseExpiresAtMillis > maxWireSafeInteger ||
					ack.LeaseExpiresAtMillis <= ack.ObservedAtMillis ||
					ack.LeaseExpiresAtMillis-ack.ObservedAtMillis > uint64(lease.HeartbeatTtlMillis) {
					return state, permanentError(errors.New("vnext connector: invalid heartbeat acknowledgement"))
				}
				acknowledgedHeartbeat = ack.HeartbeatSequence
				resetTimer(leaseTimer, time.Duration(ack.LeaseExpiresAtMillis-ack.ObservedAtMillis)*time.Millisecond)
			case *controlv1.ServerFrame_DurableCommand:
				state, err = client.applyDurableCommand(stream, lease.Fence, state, kind.DurableCommand)
				if err != nil {
					if errors.Is(err, errServerRequestedReconnect) {
						return state, transientError(err)
					}
					return state, err
				}
			case *controlv1.ServerFrame_RunAvailable:
				// Capacity is deliberately zero until AR3 freezes prompt/result
				// frames. An offer is not execution authority and is not claimed.
				if kind.RunAvailable == nil {
					return state, permanentError(errors.New("vnext connector: empty run offer"))
				}
			case *controlv1.ServerFrame_ConnectLease,
				*controlv1.ServerFrame_CredentialRotationResult,
				*controlv1.ServerFrame_RunLeaseGranted:
				return state, permanentError(errors.New("vnext connector: unexpected server frame"))
			default:
				return state, permanentError(errors.New("vnext connector: unsupported server frame"))
			}
		}
	}
}

func (client *Client) helloFrame(state State) *controlv1.ClientFrame {
	return &controlv1.ClientFrame{Kind: &controlv1.ClientFrame_Hello{Hello: &controlv1.Hello{
		TenantId:            client.config.TenantID,
		ConnectorId:         client.config.ConnectorID,
		HostId:              client.config.HostID,
		BootId:              client.config.BootID,
		ConnectorGeneration: client.config.ConnectorGeneration,
		SpecRevision:        state.AppliedConfigRevision,
		Protocol: &controlv1.ProtocolRange{
			MinimumMajor: protocolMajor,
			MinimumMinor: protocolMinor,
			MaximumMajor: protocolMajor,
			MaximumMinor: protocolMinor,
		},
		RuntimeClaims:              client.runtimeClaims(state),
		Capacity:                   client.capacity(state),
		LastAppliedCommandSequence: state.AppliedCommandSequence,
		RequiredServerCapabilities: []string{"run-routing"},
	}}}
}

func (client *Client) heartbeatFrame(fence *controlv1.LeaseFence, sequence uint64, state State) *controlv1.ClientFrame {
	return &controlv1.ClientFrame{Kind: &controlv1.ClientFrame_Heartbeat{Heartbeat: &controlv1.Heartbeat{
		Fence:                  fence,
		HeartbeatSequence:      sequence,
		AppliedConfigRevision:  state.AppliedConfigRevision,
		AppliedCommandSequence: state.AppliedCommandSequence,
		RuntimeClaims:          client.runtimeClaims(state),
		Capacity:               client.capacity(state),
	}}}
}

func (client *Client) runtimeClaims(state State) *controlv1.RuntimeClaims {
	stableError := state.StableErrorCode
	if stableError == "" {
		stableError = stablePayloadUnavailable
	}
	return &controlv1.RuntimeClaims{
		RuntimeKind:        client.config.RuntimeKind,
		RuntimeVersion:     client.config.RuntimeVersion,
		AdapterBuildDigest: append([]byte(nil), client.config.AdapterBuildDigest[:]...),
		Capabilities:       nil,
		QueueDepth:         0,
		ActiveRunIds:       nil,
		StableErrorCode:    stableError,
	}
}

func (client *Client) capacity(state State) *controlv1.Capacity {
	maximumConcurrentRuns := client.config.MaximumConcurrentRuns
	if configured, ok := state.RuntimeConfig["max-concurrent-runs"]; ok {
		parsed, err := strconv.ParseUint(configured, 10, 32)
		if err == nil && parsed >= 1 && parsed <= 4_096 {
			maximumConcurrentRuns = uint32(parsed)
		}
	}
	return &controlv1.Capacity{
		MaximumConcurrentRuns:   maximumConcurrentRuns,
		AvailableConcurrentRuns: 0,
		MaximumQueueDepth:       client.config.MaximumQueueDepth,
	}
}

func (client *Client) validateLease(lease *controlv1.ConnectLease, state State) error {
	if lease == nil || lease.Fence == nil || lease.ProtocolMajor != protocolMajor || lease.ProtocolMinor != protocolMinor ||
		lease.IssuedAtMillis == 0 || lease.ExpiresAtMillis <= lease.IssuedAtMillis || lease.ExpiresAtMillis > maxWireSafeInteger ||
		lease.ExpiresAtMillis-lease.IssuedAtMillis > uint64(lease.HeartbeatTtlMillis) ||
		lease.HeartbeatIntervalMillis < 1_000 || lease.HeartbeatIntervalMillis > 60_000 ||
		lease.HeartbeatTtlMillis <= lease.HeartbeatIntervalMillis || lease.HeartbeatTtlMillis > 300_000 {
		return errors.New("vnext connector: invalid ConnectLease")
	}
	fence := lease.Fence
	if fence.TenantId != client.config.TenantID || fence.ConnectorId != client.config.ConnectorID ||
		fence.BootId != client.config.BootID || fence.ConnectorGeneration != client.config.ConnectorGeneration ||
		!canonicalUUIDv7(fence.LeaseId) || fence.LeaseEpoch == 0 || fence.LeaseEpoch > maxWireSafeInteger {
		return errors.New("vnext connector: ConnectLease fence mismatch")
	}
	serverCursor := lease.AcknowledgedCommandSequence
	if serverCursor > maxWireSafeInteger {
		return errors.New("vnext connector: invalid server command cursor")
	}
	if serverCursor != state.AppliedCommandSequence &&
		!(state.AppliedCommandSequence > 0 && serverCursor+1 == state.AppliedCommandSequence) {
		return errors.New("vnext connector: server/local command cursors cannot be reconciled")
	}
	return nil
}

func (client *Client) applyDurableCommand(
	stream ControlStream,
	fence *controlv1.LeaseFence,
	state State,
	frame *controlv1.DurableCommandFrame,
) (State, error) {
	cursor, err := stateCommandCursor(state)
	if err != nil {
		return state, permanentError(err)
	}
	verified, err := VerifyDurableCommand(frame, CommandIdentity{
		ConnectorGeneration: client.config.ConnectorGeneration,
		SpecRevision:        state.AppliedConfigRevision,
	}, cursor)
	if err != nil {
		return state, permanentError(err)
	}
	if verified.Kind == CommandKindRotateCredential {
		return state, permanentError(errors.New("vnext connector: credential rotation is not enabled in this protocol slice"))
	}
	var sessionResult error
	if !verified.Replay {
		switch verified.Kind {
		case CommandKindApplyConfig:
			apply := verified.Command.GetApplyConfig()
			if apply.ConfigRevision <= state.AppliedConfigRevision {
				return state, permanentError(errors.New("vnext connector: config revision is not monotonic"))
			}
			if err := client.validateAppliedConfiguration(verified.AdapterConfig, verified.RuntimeConfig); err != nil {
				return state, permanentError(err)
			}
			state.AppliedConfigRevision = apply.ConfigRevision
			state.DesiredState = desiredStateFromProto(apply.DesiredState)
			state.AdapterConfig = cloneStringMap(verified.AdapterConfig)
			state.RuntimeConfig = cloneStringMap(verified.RuntimeConfig)
			state.StableErrorCode = stablePayloadUnavailable
			if state.DesiredState == DesiredStateStopped {
				sessionResult = errConnectorStopped
			}
		case CommandKindCloseStream:
			closeCommand := verified.Command.GetCloseStream()
			state.StableErrorCode = closeCommand.StableCode
			switch closeCommand.Reason {
			case controlv1.CloseStreamReason_CLOSE_STREAM_REASON_DRAINED,
				controlv1.CloseStreamReason_CLOSE_STREAM_REASON_REVOKED:
				state.DesiredState = DesiredStateStopped
				sessionResult = errConnectorStopped
			default:
				sessionResult = errServerRequestedReconnect
			}
		default:
			return state, permanentError(errors.New("vnext connector: unsupported durable command"))
		}
		state.AppliedCommandSequence = verified.Command.CommandSequence
		state.LastEncodedCommandDigest = append(StateDigest(nil), verified.EncodedDigest[:]...)
		state.LastPayloadDigest = append(StateDigest(nil), verified.PayloadDigest[:]...)
		if err := client.config.StateStore.Save(state); err != nil {
			return state, permanentError(err)
		}
	} else {
		switch verified.Kind {
		case CommandKindApplyConfig:
			apply := verified.Command.GetApplyConfig()
			if err := client.validateAppliedConfiguration(verified.AdapterConfig, verified.RuntimeConfig); err != nil {
				return state, permanentError(err)
			}
			if desiredStateFromProto(apply.DesiredState) != state.DesiredState ||
				!maps.Equal(verified.AdapterConfig, state.AdapterConfig) ||
				!maps.Equal(verified.RuntimeConfig, state.RuntimeConfig) {
				return state, permanentError(errors.New("vnext connector: replayed config does not match durable state"))
			}
			if apply.DesiredState == controlv1.DesiredConnectorState_DESIRED_CONNECTOR_STATE_STOPPED {
				sessionResult = errConnectorStopped
			}
		case CommandKindCloseStream:
			closeCommand := verified.Command.GetCloseStream()
			if closeCommand.StableCode != state.StableErrorCode {
				return state, permanentError(errors.New("vnext connector: replayed close command does not match durable state"))
			}
			switch closeCommand.Reason {
			case controlv1.CloseStreamReason_CLOSE_STREAM_REASON_DRAINED,
				controlv1.CloseStreamReason_CLOSE_STREAM_REASON_REVOKED:
				sessionResult = errConnectorStopped
			default:
				sessionResult = errServerRequestedReconnect
			}
		}
	}
	ack := &controlv1.CommandAcknowledgement{
		Fence:                fence,
		CommandSequence:      verified.Command.CommandSequence,
		PayloadDigest:        append([]byte(nil), verified.PayloadDigest[:]...),
		EncodedCommandDigest: append([]byte(nil), verified.EncodedDigest[:]...),
	}
	if err := stream.Send(&controlv1.ClientFrame{Kind: &controlv1.ClientFrame_CommandAcknowledgement{
		CommandAcknowledgement: ack,
	}}); err != nil {
		return state, classifyTransportError("acknowledge durable command", err)
	}
	return state, sessionResult
}

func (client *Client) validateAppliedConfiguration(adapterConfig, runtimeConfig map[string]string) error {
	for key, value := range adapterConfig {
		allowed := false
		switch client.config.RuntimeKind {
		case "codex", "claude_code":
			allowed = key == "adapter" || key == "endpoint-profile" || key == "model" || key == "profile"
		case "openclaw_acp":
			allowed = key == "adapter" || key == "endpoint" || key == "profile"
		case "eino", "rig":
			allowed = key == "adapter" || key == "endpoint" || key == "model" || key == "profile"
		case "custom_acp":
			allowed = key == "adapter" || key == "endpoint" || key == "profile"
		}
		if !allowed || !registeredConfigValue(key, value) || (key == "adapter" && value != client.config.Adapter) {
			return errors.New("vnext connector: config is outside the registered adapter scope")
		}
	}
	for key, value := range runtimeConfig {
		if !registeredConfigValue(key, value) {
			return errors.New("vnext connector: config contains an unregistered runtime value")
		}
		switch key {
		case "log-level", "max-concurrent-runs", "offline-policy", "policy-id", "shutdown", "workspace-mode":
		default:
			return errors.New("vnext connector: config is outside the registered runtime scope")
		}
	}
	return nil
}

func stateCommandCursor(state State) (CommandCursor, error) {
	var cursor CommandCursor
	cursor.Sequence = state.AppliedCommandSequence
	if cursor.Sequence == 0 {
		return cursor, nil
	}
	if len(state.LastEncodedCommandDigest) != sha256.Size || len(state.LastPayloadDigest) != sha256.Size {
		return CommandCursor{}, errors.New("vnext connector: durable cursor digests are invalid")
	}
	copy(cursor.EncodedDigest[:], state.LastEncodedCommandDigest)
	copy(cursor.PayloadDigest[:], state.LastPayloadDigest)
	return cursor, nil
}

func desiredStateFromProto(value controlv1.DesiredConnectorState) DesiredState {
	switch value {
	case controlv1.DesiredConnectorState_DESIRED_CONNECTOR_STATE_DRAINING:
		return DesiredStateDraining
	case controlv1.DesiredConnectorState_DESIRED_CONNECTOR_STATE_STOPPED:
		return DesiredStateStopped
	default:
		return DesiredStateRunning
	}
}

type receivedFrame struct {
	frame *controlv1.ServerFrame
	err   error
}

func receiveControlFrames(ctx context.Context, stream ControlStream, output chan<- receivedFrame) {
	for {
		frame, err := stream.Recv()
		select {
		case output <- receivedFrame{frame: frame, err: err}:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(duration)
}

func validRuntimeKind(value string) bool {
	switch value {
	case "codex", "openclaw_acp", "eino", "rig", "claude_code", "custom_acp":
		return true
	default:
		return false
	}
}

func runtimeAdapter(runtimeKind string) string {
	switch runtimeKind {
	case "codex":
		return "codex-app-server"
	case "openclaw_acp":
		return "openclaw-acp"
	case "eino":
		return "eino"
	case "rig":
		return "rig"
	case "claude_code":
		return "claude-code"
	case "custom_acp":
		return "vendor-v1"
	default:
		return ""
	}
}

func jitter(base time.Duration) time.Duration {
	if base <= 1 {
		return base
	}
	// Equal jitter keeps retries bounded in [base/2, base).
	half := base / 2
	return half + time.Duration(rand.Int64N(int64(base-half)))
}

type classifiedControlError struct {
	err       error
	permanent bool
}

func (err *classifiedControlError) Error() string { return err.err.Error() }
func (err *classifiedControlError) Unwrap() error { return err.err }

func permanentError(err error) error { return &classifiedControlError{err: err, permanent: true} }
func transientError(err error) error { return &classifiedControlError{err: err} }

func isPermanentControlError(err error) bool {
	var classified *classifiedControlError
	return errors.As(err, &classified) && classified.permanent
}

func classifyTransportError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return transientError(fmt.Errorf("%s: %w", operation, err))
	}
	// gRPC status classification is kept in grpc_transport.go so this state
	// machine remains free of transport construction concerns.
	if grpcStatusIsPermanent(err) {
		return permanentError(fmt.Errorf("%s: %w", operation, err))
	}
	return transientError(fmt.Errorf("%s: %w", operation, err))
}
