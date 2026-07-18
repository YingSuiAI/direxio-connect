package matrix

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/YingSuiAI/dirextalk-connect/core"
	"github.com/google/uuid"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"
)

func init() {
	core.RegisterPlatform("matrix", New)
}

type replyContext struct {
	roomID    id.RoomID
	messageID id.EventID
}

type Platform struct {
	homeserver            string
	accessToken           string
	userID                string
	allowFrom             string
	allowedRoomID         id.RoomID
	approvalOwnerID       id.UserID
	shareSessionInChannel bool
	groupReplyAll         bool
	autoJoin              bool
	autoVerify            bool
	proxyURL              string

	mu                   sync.RWMutex
	client               *mautrix.Client
	selfUserID           id.UserID
	handler              core.MessageHandler
	approvalHandler      core.ApprovalResponseHandler
	lifecycleHandler     core.PlatformLifecycleHandler
	cancel               context.CancelFunc
	statusRefresher      *agentRoomStatusRefresher
	stopping             bool
	generation           uint64
	everConnected        bool
	unavailableNotified  bool
	dedup                core.MessageDedup
	httpClient           *http.Client
	cryptoHelper         any //nolint:unused // *cryptohelper.CryptoHelper when built with goolm tag
	crossSigningPassword string
}

const (
	agentRoomStatusEventType = "io.dirextalk.agent.status"
	approvalRequestMsgType   = event.MessageType("io.dirextalk.agent.approval.request")
	approvalResponseMsgType  = event.MessageType("io.dirextalk.agent.approval.response")
	approvalResultMsgType    = event.MessageType("io.dirextalk.agent.approval.result")
	approvalEnvelopeKey      = "io.dirextalk.agent_approval"
	approvalRequestSchema    = "dirextalk.agent-approval-request/v1"
	approvalResponseSchema   = "dirextalk.agent-approval-response/v1"
	approvalResultSchema     = "dirextalk.agent-approval-result/v1"
	initialBackoff           = 2 * time.Second
	maxBackoff               = 60 * time.Second
	stableWindow             = 10 * time.Second
)

type approvalEnvelope struct {
	Schema     string `json:"schema"`
	ApprovalID string `json:"approval_id"`
	Kind       string `json:"kind,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Decision   string `json:"decision,omitempty"`
	Outcome    string `json:"outcome,omitempty"`
	Code       string `json:"code,omitempty"`
}

// approvalTimelineContent keeps structured data in the one agreed custom
// envelope; clients must not infer approval state from body text.
type approvalTimelineContent struct {
	MsgType  event.MessageType `json:"msgtype"`
	Body     string            `json:"body"`
	Approval approvalEnvelope  `json:"io.dirextalk.agent_approval"`
}

var (
	agentRoomStatusMatrixEventType = event.Type{Type: agentRoomStatusEventType, Class: event.StateEventType}
	agentRoomStatusRefreshInterval = 30 * time.Second
)

type agentRoomStatusRefresher struct {
	stopOnce sync.Once
	cancel   context.CancelFunc
	stopped  chan struct{}
}

func (r *agentRoomStatusRefresher) Stop() {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() {
		r.cancel()
		<-r.stopped
	})
}

func New(opts map[string]any) (core.Platform, error) {
	homeserver, _ := opts["homeserver"].(string)
	if homeserver == "" {
		return nil, fmt.Errorf("matrix: homeserver is required")
	}
	accessToken, _ := opts["access_token"].(string)
	if accessToken == "" {
		return nil, fmt.Errorf("matrix: access_token is required")
	}
	userID, _ := opts["user_id"].(string)
	allowFrom, _ := opts["allow_from"].(string)
	core.CheckAllowFrom("matrix", allowFrom)
	roomID, _ := opts["room_id"].(string)
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return nil, fmt.Errorf("matrix: room_id is required")
	}
	if err := validateRoomID(roomID); err != nil {
		return nil, err
	}
	approvalOwnerID, _ := opts["approval_owner_id"].(string)
	if rawOwnerID, configured := opts["approval_owner_id"]; configured && rawOwnerID != nil {
		if _, ok := rawOwnerID.(string); !ok {
			return nil, fmt.Errorf("matrix: approval_owner_id must be a Matrix user ID")
		}
	}
	approvalOwnerID = strings.TrimSpace(approvalOwnerID)
	if approvalOwnerID != "" {
		if _, _, err := id.UserID(approvalOwnerID).ParseAndValidateRelaxed(); err != nil {
			return nil, fmt.Errorf("matrix: approval_owner_id must be a Matrix user ID")
		}
	}

	groupReplyAll, _ := opts["group_reply_all"].(bool)
	shareSession, _ := opts["share_session_in_channel"].(bool)
	autoJoin, _ := opts["auto_join"].(bool)
	if !autoJoin {
		_, hasKey := opts["auto_join"]
		if !hasKey {
			autoJoin = true // default true
		}
	}
	autoVerify, _ := opts["auto_verify"].(bool)
	if !autoVerify {
		_, hasKey := opts["auto_verify"]
		if !hasKey {
			autoVerify = true // default true
		}
	}
	proxyURL, _ := opts["proxy"].(string)
	crossSigningPassword, _ := opts["cross_signing_password"].(string)
	if env := os.Getenv("MATRIX_CROSS_SIGNING_PASSWORD"); env != "" {
		crossSigningPassword = env
	}

	httpClient := &http.Client{Timeout: 120 * time.Second}
	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("matrix: invalid proxy URL %q: %w", proxyURL, err)
		}
		httpClient.Transport = &http.Transport{Proxy: http.ProxyURL(u)}
		slog.Info("matrix: using proxy", "proxy", u.Host)
	}

	return &Platform{
		homeserver:            homeserver,
		accessToken:           accessToken,
		userID:                userID,
		allowFrom:             allowFrom,
		allowedRoomID:         id.RoomID(roomID),
		approvalOwnerID:       id.UserID(approvalOwnerID),
		groupReplyAll:         groupReplyAll,
		shareSessionInChannel: shareSession,
		autoJoin:              autoJoin,
		proxyURL:              proxyURL,
		autoVerify:            autoVerify,
		crossSigningPassword:  crossSigningPassword,
		httpClient:            httpClient,
		dedup:                 core.MessageDedup{},
	}, nil
}

func (p *Platform) Name() string { return "matrix" }

func (p *Platform) Start(handler core.MessageHandler) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.stopping {
		return fmt.Errorf("matrix: platform stopped")
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.handler = handler
	p.cancel = cancel

	go p.connectLoop(ctx)
	return nil
}

func (p *Platform) SetLifecycleHandler(h core.PlatformLifecycleHandler) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lifecycleHandler = h
}

// SetApprovalResponseHandler installs the engine callback for already
// room- and owner-authorized approval responses.
func (p *Platform) SetApprovalResponseHandler(handler core.ApprovalResponseHandler) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.approvalHandler = handler
}

// SendApprovalRequest emits the safe public projection of a pending agent
// permission. It is disabled until an exact Matrix owner is configured.
func (p *Platform) SendApprovalRequest(ctx context.Context, rctx any, request core.ApprovalRequest) error {
	if p.approvalOwnerID == "" {
		return core.ErrApprovalBridgeUnavailable
	}
	rc, ok := rctx.(replyContext)
	if !ok {
		return fmt.Errorf("matrix: invalid reply context type %T", rctx)
	}
	if !p.roomAllowed(rc.roomID) {
		return fmt.Errorf("matrix: approval request room is not allowed")
	}
	if !validApprovalID(request.ApprovalID) || request.Kind != "tool" ||
		strings.TrimSpace(request.ToolName) == "" || len(request.ToolName) > 64 ||
		strings.TrimSpace(request.Summary) == "" || len(request.Summary) > 160 {
		return fmt.Errorf("matrix: invalid approval request projection")
	}

	content := approvalTimelineContent{
		MsgType: approvalRequestMsgType,
		Body:    "Approval requested. Use a compatible client to allow or deny.",
		Approval: approvalEnvelope{
			Schema:     approvalRequestSchema,
			ApprovalID: request.ApprovalID,
			Kind:       request.Kind,
			ToolName:   request.ToolName,
			Summary:    request.Summary,
		},
	}
	return p.sendRoomEvent(ctx, rc.roomID, event.EventMessage, &content)
}

// SendApprovalResult emits the safe terminal projection for an owner-scoped
// approval that the engine resolved without a client response.
func (p *Platform) SendApprovalResult(ctx context.Context, rctx any, result core.ApprovalResult) error {
	rc, ok := rctx.(replyContext)
	if !ok {
		return fmt.Errorf("matrix: invalid reply context type %T", rctx)
	}
	return p.sendApprovalResult(ctx, rc, result)
}

func (p *Platform) sendApprovalResult(ctx context.Context, rc replyContext, result core.ApprovalResult) error {
	if !p.roomAllowed(rc.roomID) {
		return fmt.Errorf("matrix: approval result room is not allowed")
	}
	if !validApprovalID(result.ApprovalID) || !validApprovalResult(result) {
		return fmt.Errorf("matrix: invalid approval result")
	}

	body := "Approval result is available."
	switch result.Outcome {
	case "allowed":
		body = "Approval allowed."
	case "denied":
		body = "Approval denied."
	case "expired":
		body = "Approval expired."
	case "failed":
		body = "Approval failed."
	}
	content := approvalTimelineContent{
		MsgType: approvalResultMsgType,
		Body:    body,
		Approval: approvalEnvelope{
			Schema:     approvalResultSchema,
			ApprovalID: result.ApprovalID,
			Outcome:    result.Outcome,
			Code:       result.Code,
		},
	}
	return p.sendRoomEvent(ctx, rc.roomID, event.EventMessage, &content)
}

func validApprovalID(approvalID string) bool {
	parsed, err := uuid.Parse(approvalID)
	return err == nil && parsed.String() == approvalID
}

func validApprovalResult(result core.ApprovalResult) bool {
	switch result.Outcome {
	case "allowed", "denied", "expired":
		return result.Code == ""
	case "failed":
		switch result.Code {
		case "backend_response_failed", "session_unavailable", "invalid_response":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func parseApprovalResponse(content *event.MessageEventContent, raw map[string]interface{}) (core.ApprovalResponse, bool) {
	if content == nil || content.MsgType != approvalResponseMsgType || len(raw) != 3 {
		return core.ApprovalResponse{}, false
	}
	msgType, ok := raw["msgtype"].(string)
	if !ok || event.MessageType(msgType) != approvalResponseMsgType {
		return core.ApprovalResponse{}, false
	}
	body, ok := raw["body"].(string)
	if !ok || strings.TrimSpace(body) == "" {
		return core.ApprovalResponse{}, false
	}
	envelope, ok := raw[approvalEnvelopeKey].(map[string]interface{})
	if !ok || len(envelope) != 3 {
		return core.ApprovalResponse{}, false
	}
	schema, schemaOK := envelope["schema"].(string)
	approvalID, idOK := envelope["approval_id"].(string)
	decision, decisionOK := envelope["decision"].(string)
	if !schemaOK || !idOK || !decisionOK || schema != approvalResponseSchema ||
		!validApprovalID(approvalID) || (decision != "allow" && decision != "deny") {
		return core.ApprovalResponse{}, false
	}
	return core.ApprovalResponse{ApprovalID: approvalID, Decision: decision}, true
}

func (p *Platform) handleApprovalResponse(ctx context.Context, evt *event.Event, content *event.MessageEventContent) {
	if p.approvalOwnerID == "" || evt.Sender != p.approvalOwnerID {
		slog.Debug("matrix: ignoring approval response from non-owner", "sender", evt.Sender)
		return
	}
	response, ok := parseApprovalResponse(content, evt.Content.Raw)
	if !ok {
		slog.Debug("matrix: ignoring malformed approval response", "event_id", evt.ID)
		return
	}
	handler := p.getApprovalResponseHandler()
	if handler == nil {
		slog.Debug("matrix: approval response received before engine handler was registered", "event_id", evt.ID)
		return
	}

	result := handler(response)
	if result.ApprovalID != response.ApprovalID {
		slog.Error("matrix: approval handler returned mismatched approval ID")
		return
	}
	if err := p.sendApprovalResult(ctx, replyContext{roomID: evt.RoomID, messageID: evt.ID}, result); err != nil {
		slog.Warn("matrix: failed to send approval result", "outcome", result.Outcome, "error", err)
	}
}

func (p *Platform) connectLoop(ctx context.Context) {
	backoff := initialBackoff

	for {
		if ctx.Err() != nil || p.isStopping() {
			return
		}

		startedAt := time.Now()
		err := p.runConnection(ctx)
		if ctx.Err() != nil || p.isStopping() {
			return
		}

		wait := backoff
		if time.Since(startedAt) >= stableWindow {
			wait = initialBackoff
			backoff = initialBackoff
		} else if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}

		if err != nil {
			slog.Warn("matrix: connection error, retrying", "error", core.RedactToken(err.Error(), p.accessToken), "backoff", wait)
			p.notifyUnavailable(err)
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (p *Platform) runConnection(ctx context.Context) error {
	client, err := mautrix.NewClient(p.homeserver, "", p.accessToken)
	if err != nil {
		return fmt.Errorf("matrix: create client: %w", err)
	}
	client.Client = p.httpClient

	// Always call Whoami to validate token and get device ID (needed for E2EE)
	selfUserID := id.UserID(p.userID)
	var deviceID id.DeviceID
	resp, err := client.Whoami(ctx)
	if err != nil {
		return fmt.Errorf("matrix: whoami: %w", err)
	}
	if selfUserID == "" {
		selfUserID = resp.UserID
	}
	deviceID = resp.DeviceID
	client.UserID = selfUserID
	client.DeviceID = deviceID

	if ctx.Err() != nil || p.isStopping() {
		return nil
	}

	gen, ok := p.publishClient(client, selfUserID)
	if !ok {
		return nil
	}

	// Initialize E2EE crypto helper
	p.initE2EE(ctx, client)

	if err := p.publishAgentRoomStatus(ctx, true); err != nil {
		slog.Warn("matrix: publish agent online status failed", "error", core.RedactToken(err.Error(), p.accessToken))
	}
	statusRefresher := p.startAgentRoomStatusRefresher(ctx)
	p.setAgentRoomStatusRefresher(gen, statusRefresher)

	slog.Info("matrix: connected", "user_id", selfUserID)
	p.emitReady(gen)

	// Register event handlers.
	// Note: EventEncrypted is handled by cryptohelper which decrypts and
	// re-dispatches as EventMessage, so we only need EventMessage here.
	syncer := client.Syncer.(*mautrix.DefaultSyncer)
	syncer.OnEventType(event.EventMessage, func(ctx context.Context, evt *event.Event) {
		p.handleMessage(ctx, evt)
	})
	syncer.OnEventType(event.StateMember, func(ctx context.Context, evt *event.Event) {
		p.handleMemberState(ctx, evt)
	})

	// Blocks until ctx cancelled or fatal error
	err = client.SyncWithContext(ctx)
	p.stopAgentRoomStatusRefresher(gen, statusRefresher)

	// Cleanup
	if ctx.Err() == nil {
		statusCtx, cancelStatus := context.WithTimeout(context.Background(), 5*time.Second)
		if statusErr := p.publishAgentRoomStatus(statusCtx, false); statusErr != nil {
			slog.Warn("matrix: publish agent offline status failed", "error", core.RedactToken(statusErr.Error(), p.accessToken))
		}
		cancelStatus()
	}
	p.closeCryptoHelper()
	p.clearClient(gen, client)
	if ctx.Err() != nil {
		return nil
	}
	return fmt.Errorf("matrix: sync ended: %w", err)
}

func (p *Platform) handleMessage(ctx context.Context, evt *event.Event) {
	if !p.roomAllowed(evt.RoomID) {
		return
	}

	content, ok := evt.Content.Parsed.(*event.MessageEventContent)
	if !ok || content == nil {
		return
	}

	// Skip own messages
	selfID := p.getSelfUserID()
	if evt.Sender == selfID {
		return
	}

	// Dedup
	if p.dedup.IsDuplicate(evt.ID.String()) {
		return
	}

	// Old message check
	msgTime := time.UnixMilli(evt.Timestamp)
	if core.IsOldMessage(msgTime) {
		slog.Debug("matrix: ignoring old message", "event_id", evt.ID, "time", msgTime)
		return
	}

	// Approval responses are a separate, owner-scoped control path. They must
	// not pass through allow_from, group mention handling, or the ordinary
	// agent prompt dispatcher.
	if content.MsgType == approvalResponseMsgType {
		p.handleApprovalResponse(ctx, evt, content)
		return
	}
	if content.MsgType == approvalRequestMsgType || content.MsgType == approvalResultMsgType {
		slog.Debug("matrix: ignoring approval timeline event not addressed to the bridge", "type", content.MsgType)
		return
	}

	// Allow-from check
	senderStr := evt.Sender.String()
	if !core.AllowList(p.allowFrom, senderStr) {
		slog.Debug("matrix: message from unauthorized user", "user", senderStr)
		return
	}

	roomID := evt.RoomID
	isDM := p.isDMRoom(ctx, roomID)

	// Group mention check
	if !isDM && !p.groupReplyAll {
		if !p.isDirectedAtBot(content, selfID) {
			return
		}
	}

	userName := displayName(evt.Sender)
	sessionKey := p.buildSessionKey(roomID, evt.Sender)
	channelKey := roomID.String()

	rctx := replyContext{roomID: roomID, messageID: evt.ID}

	// Handle different message types
	msgType := content.MsgType
	switch msgType {
	case event.MsgText, event.MsgNotice, event.MsgEmote:
		text := stripBotMention(content.Body, selfID)
		p.dispatch(&core.Message{
			SessionKey: sessionKey, Platform: "matrix",
			UserID: senderStr, UserName: userName,
			Content: text, MessageID: evt.ID.String(),
			ChannelKey: channelKey, ReplyCtx: rctx,
		})
	case event.MsgImage:
		img, err := p.downloadMedia(ctx, content)
		if err != nil {
			slog.Error("matrix: download image failed", "error", err)
			return
		}
		caption := stripBotMention(content.Body, selfID)
		p.dispatch(&core.Message{
			SessionKey: sessionKey, Platform: "matrix",
			UserID: senderStr, UserName: userName,
			Content: caption, MessageID: evt.ID.String(),
			ChannelKey: channelKey, ReplyCtx: rctx,
			Images: []core.ImageAttachment{*img},
		})
	case event.MsgFile:
		file, err := p.downloadFileMedia(ctx, content)
		if err != nil {
			slog.Error("matrix: download file failed", "error", err)
			return
		}
		caption := stripBotMention(content.Body, selfID)
		p.dispatch(&core.Message{
			SessionKey: sessionKey, Platform: "matrix",
			UserID: senderStr, UserName: userName,
			Content: caption, MessageID: evt.ID.String(),
			ChannelKey: channelKey, ReplyCtx: rctx,
			Files: []core.FileAttachment{*file},
		})
	case event.MsgAudio:
		audio, err := p.downloadAudioMedia(ctx, content)
		if err != nil {
			slog.Error("matrix: download audio failed", "error", err)
			return
		}
		p.dispatch(&core.Message{
			SessionKey: sessionKey, Platform: "matrix",
			UserID: senderStr, UserName: userName,
			MessageID:  evt.ID.String(),
			ChannelKey: channelKey, ReplyCtx: rctx,
			Audio: audio,
		})
	default:
		slog.Debug("matrix: ignoring unsupported message type", "type", msgType)
	}
}

func (p *Platform) handleMemberState(ctx context.Context, evt *event.Event) {
	if !p.autoJoin {
		return
	}
	if !p.roomAllowed(evt.RoomID) {
		return
	}
	content, ok := evt.Content.Parsed.(*event.MemberEventContent)
	if !ok {
		return
	}
	selfID := p.getSelfUserID()
	if content.Membership == event.MembershipInvite && evt.StateKey != nil && id.UserID(*evt.StateKey) == selfID {
		client := p.getClient()
		if client == nil {
			return
		}
		_, err := client.JoinRoomByID(ctx, evt.RoomID)
		if err != nil {
			slog.Error("matrix: auto-join failed", "room", evt.RoomID, "error", err)
		} else {
			slog.Info("matrix: auto-joined room", "room", evt.RoomID)
		}
	}
}

func (p *Platform) dispatch(msg *core.Message) {
	handler := p.getHandler()
	if handler == nil {
		return
	}
	handler(p, msg)
}

// sendRoomEvent sends an event to a room, encrypting it if E2EE is available and the room is encrypted.
func (p *Platform) sendRoomEvent(ctx context.Context, roomID id.RoomID, evtType event.Type, content any) error {
	client := p.getClient()
	if client == nil {
		return fmt.Errorf("matrix: not connected")
	}

	// Try E2EE path first (only available when built with goolm tag)
	if handled, err := p.tryEncryptAndSend(ctx, client, roomID, evtType, content); handled {
		return err
	}

	_, err := client.SendMessageEvent(ctx, roomID, evtType, content)
	if err != nil {
		return fmt.Errorf("matrix: send: %w", err)
	}
	return nil
}

func (p *Platform) Reply(ctx context.Context, rctx any, content string) error {
	rc, ok := rctx.(replyContext)
	if !ok {
		return fmt.Errorf("matrix: invalid reply context type %T", rctx)
	}

	parsed := format.RenderMarkdown(content, true, false)
	parsed.Body = content
	if content != "" {
		parsed.RelatesTo = &event.RelatesTo{}
		parsed.RelatesTo.SetReplyTo(rc.messageID)
	}

	return p.sendRoomEvent(ctx, rc.roomID, event.EventMessage, &parsed)
}

func (p *Platform) Send(ctx context.Context, rctx any, content string) error {
	rc, ok := rctx.(replyContext)
	if !ok {
		return fmt.Errorf("matrix: invalid reply context type %T", rctx)
	}

	parsed := format.RenderMarkdown(content, true, false)
	parsed.Body = content

	return p.sendRoomEvent(ctx, rc.roomID, event.EventMessage, &parsed)
}

func (p *Platform) Stop() error {
	p.mu.Lock()
	if p.stopping {
		p.mu.Unlock()
		return nil
	}
	p.stopping = true
	cancel := p.cancel
	p.cancel = nil
	statusRefresher := p.statusRefresher
	p.statusRefresher = nil
	p.mu.Unlock()

	statusRefresher.Stop()

	statusCtx, cancelStatus := context.WithTimeout(context.Background(), 5*time.Second)
	if err := p.publishAgentRoomStatus(statusCtx, false); err != nil {
		slog.Warn("matrix: publish agent offline status failed", "error", core.RedactToken(err.Error(), p.accessToken))
	}
	cancelStatus()

	p.mu.Lock()
	p.client = nil
	p.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	return nil
}

func (p *Platform) startAgentRoomStatusRefresher(ctx context.Context) *agentRoomStatusRefresher {
	if agentRoomStatusRefreshInterval <= 0 {
		return nil
	}

	refreshCtx, cancel := context.WithCancel(ctx)
	refresher := &agentRoomStatusRefresher{
		cancel:  cancel,
		stopped: make(chan struct{}),
	}
	go func() {
		defer close(refresher.stopped)

		ticker := time.NewTicker(agentRoomStatusRefreshInterval)
		defer ticker.Stop()

		for {
			select {
			case <-refreshCtx.Done():
				return
			case <-ticker.C:
				if refreshCtx.Err() != nil || p.isStopping() {
					return
				}

				statusCtx, cancelStatus := context.WithTimeout(refreshCtx, 5*time.Second)
				if err := p.publishAgentRoomStatus(statusCtx, true); err != nil {
					slog.Warn("matrix: refresh agent online status failed", "error", core.RedactToken(err.Error(), p.accessToken))
				}
				cancelStatus()
			}
		}
	}()
	return refresher
}

func (p *Platform) setAgentRoomStatusRefresher(gen uint64, refresher *agentRoomStatusRefresher) {
	if refresher == nil {
		return
	}

	p.mu.Lock()
	if p.stopping || p.generation != gen {
		p.mu.Unlock()
		refresher.Stop()
		return
	}
	p.statusRefresher = refresher
	p.mu.Unlock()
}

func (p *Platform) stopAgentRoomStatusRefresher(gen uint64, refresher *agentRoomStatusRefresher) {
	if refresher == nil {
		return
	}

	p.mu.Lock()
	if p.generation == gen && p.statusRefresher == refresher {
		p.statusRefresher = nil
	}
	p.mu.Unlock()

	refresher.Stop()
}

func (p *Platform) publishAgentRoomStatus(ctx context.Context, online bool) error {
	p.mu.RLock()
	client := p.client
	roomID := p.allowedRoomID
	userID := p.selfUserID
	if userID == "" {
		userID = id.UserID(p.userID)
	}
	p.mu.RUnlock()

	if client == nil || roomID == "" || userID == "" {
		return nil
	}

	content := map[string]bool{"online": online}
	_, err := client.SendStateEvent(ctx, roomID, agentRoomStatusMatrixEventType, userID.String(), content)
	if err != nil {
		return fmt.Errorf("send %s state event: %w", agentRoomStatusEventType, err)
	}
	return nil
}

// --- Optional interfaces ---

func (p *Platform) SendImage(ctx context.Context, rctx any, img core.ImageAttachment) error {
	rc, ok := rctx.(replyContext)
	if !ok {
		return fmt.Errorf("matrix: invalid reply context type %T", rctx)
	}
	client := p.getClient()
	if client == nil {
		return fmt.Errorf("matrix: not connected")
	}

	mime := img.MimeType
	if mime == "" {
		mime = "image/png"
	}
	name := img.FileName
	if name == "" {
		name = "image"
	}

	uri, err := client.UploadMedia(ctx, mautrix.ReqUploadMedia{
		ContentBytes: img.Data,
		ContentType:  mime,
		FileName:     name,
	})
	if err != nil {
		return fmt.Errorf("matrix: upload image: %w", err)
	}

	content := &event.MessageEventContent{
		MsgType: event.MsgImage,
		Body:    name,
		Info: &event.FileInfo{
			MimeType: mime,
			Size:     len(img.Data),
		},
	}
	if !uri.ContentURI.IsEmpty() {
		content.URL = uri.ContentURI.CUString()
	} else {
		content.File = &event.EncryptedFileInfo{
			URL: uri.ContentURI.CUString(),
		}
	}

	return p.sendRoomEvent(ctx, rc.roomID, event.EventMessage, content)
}

func (p *Platform) SendFile(ctx context.Context, rctx any, file core.FileAttachment) error {
	rc, ok := rctx.(replyContext)
	if !ok {
		return fmt.Errorf("matrix: invalid reply context type %T", rctx)
	}
	client := p.getClient()
	if client == nil {
		return fmt.Errorf("matrix: not connected")
	}

	mime := file.MimeType
	if mime == "" {
		mime = "application/octet-stream"
	}
	name := file.FileName
	if name == "" {
		name = "attachment"
	}

	uri, err := client.UploadMedia(ctx, mautrix.ReqUploadMedia{
		ContentBytes: file.Data,
		ContentType:  mime,
		FileName:     name,
	})
	if err != nil {
		return fmt.Errorf("matrix: upload file: %w", err)
	}

	content := &event.MessageEventContent{
		MsgType: event.MsgFile,
		Body:    name,
		Info: &event.FileInfo{
			MimeType: mime,
			Size:     len(file.Data),
		},
	}
	if !uri.ContentURI.IsEmpty() {
		content.URL = uri.ContentURI.CUString()
	} else {
		content.File = &event.EncryptedFileInfo{
			URL: uri.ContentURI.CUString(),
		}
	}

	return p.sendRoomEvent(ctx, rc.roomID, event.EventMessage, content)
}

func (p *Platform) StartTyping(ctx context.Context, rctx any) (stop func()) {
	rc, ok := rctx.(replyContext)
	if !ok {
		return func() {}
	}

	client := p.getClient()
	if client == nil {
		return func() {}
	}

	// Set typing with 30s timeout, refresh every 25s
	_, _ = client.UserTyping(ctx, rc.roomID, true, 30*time.Second)

	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				c := p.getClient()
				if c != nil {
					_, _ = c.UserTyping(context.Background(), rc.roomID, false, 0)
				}
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				c := p.getClient()
				if c == nil {
					return
				}
				_, _ = c.UserTyping(ctx, rc.roomID, true, 30*time.Second)
			}
		}
	}()

	return func() { close(done) }
}

func (p *Platform) UpdateMessage(ctx context.Context, previewHandle any, content string) error {
	rc, ok := previewHandle.(replyContext)
	if !ok {
		return fmt.Errorf("matrix: invalid preview handle type %T", previewHandle)
	}

	parsed := format.RenderMarkdown(content, true, false)
	parsed.Body = content

	// Copy the new content for m.replace relation
	newContent := parsed
	newContent.Mentions = nil

	parsed.NewContent = &newContent
	parsed.RelatesTo = &event.RelatesTo{
		Type:    event.RelReplace,
		EventID: rc.messageID,
	}
	parsed.Body = "* " + content

	return p.sendRoomEvent(ctx, rc.roomID, event.EventMessage, &parsed)
}

func (p *Platform) ReconstructReplyCtx(sessionKey string) (any, error) {
	// Formats:
	//   matrix:{roomID}:{userID}   - per-user session
	//   matrix:{roomID}            - shared session
	// Room IDs contain a colon (!localpart:server), so we can't simply split on colons.
	if !strings.HasPrefix(sessionKey, "matrix:") {
		return nil, fmt.Errorf("matrix: invalid session key %q", sessionKey)
	}
	rest := sessionKey[len("matrix:"):]
	if rest == "" {
		return nil, fmt.Errorf("matrix: invalid session key %q", sessionKey)
	}

	// Find boundary between room ID and optional user ID.
	// User IDs start with @, so ":@" only appears at the roomID:userID boundary.
	var roomIDStr string
	if idx := strings.Index(rest, ":@"); idx >= 0 {
		roomIDStr = rest[:idx]
	} else {
		roomIDStr = rest
	}

	if !strings.HasPrefix(roomIDStr, "!") {
		return nil, fmt.Errorf("matrix: invalid room ID in %q", sessionKey)
	}
	if !p.roomAllowed(id.RoomID(roomIDStr)) {
		return nil, fmt.Errorf("matrix: room %q is not allowed", roomIDStr)
	}
	return replyContext{roomID: id.RoomID(roomIDStr)}, nil
}

// --- Internal helpers ---

func (p *Platform) roomAllowed(roomID id.RoomID) bool {
	return p.allowedRoomID != "" && roomID == p.allowedRoomID
}

func validateRoomID(roomID string) error {
	if strings.HasPrefix(roomID, "!agent:") {
		return fmt.Errorf("matrix: room_id must be the real Matrix room ID, not legacy !agent:<server>")
	}

	serverSep := strings.LastIndexByte(roomID, ':')
	if !strings.HasPrefix(roomID, "!") || serverSep <= 1 || serverSep == len(roomID)-1 {
		return fmt.Errorf("matrix: room_id must be a Matrix room ID")
	}
	return nil
}

func (p *Platform) buildSessionKey(roomID id.RoomID, sender id.UserID) string {
	if p.shareSessionInChannel {
		return fmt.Sprintf("matrix:%s", roomID)
	}
	return fmt.Sprintf("matrix:%s:%s", roomID, sender)
}

func (p *Platform) isDMRoom(ctx context.Context, roomID id.RoomID) bool {
	client := p.getClient()
	if client == nil {
		return false
	}
	members, err := client.Members(ctx, roomID)
	if err != nil {
		slog.Debug("matrix: failed to get room members, assuming group", "error", err)
		return false
	}
	return len(members.Chunk) <= 2
}

func (p *Platform) isDirectedAtBot(content *event.MessageEventContent, selfID id.UserID) bool {
	// Check formatted body for matrix.to link
	if content.FormattedBody != "" {
		mention := fmt.Sprintf("https://matrix.to/#/%s", selfID)
		if strings.Contains(content.FormattedBody, mention) {
			return true
		}
	}
	// Check plain body for @user:server mention
	if strings.Contains(content.Body, selfID.String()) {
		return true
	}
	return false
}

func (p *Platform) downloadMediaContent(ctx context.Context, contentURL id.ContentURIString) ([]byte, error) {
	client := p.getClient()
	if client == nil {
		return nil, fmt.Errorf("not connected")
	}
	parsed, err := contentURL.Parse()
	if err != nil {
		return nil, fmt.Errorf("parse content URI: %w", err)
	}
	resp, err := client.Download(ctx, parsed)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return io.ReadAll(resp.Body)
}

func (p *Platform) downloadMedia(ctx context.Context, content *event.MessageEventContent) (*core.ImageAttachment, error) {
	data, err := p.downloadMediaContent(ctx, content.URL)
	if err != nil {
		return nil, err
	}
	mime := ""
	if content.Info != nil {
		mime = content.Info.MimeType
	}
	if mime == "" {
		mime = "image/png"
	}
	name := content.Body
	return &core.ImageAttachment{
		MimeType: mime,
		Data:     data,
		FileName: name,
	}, nil
}

func (p *Platform) downloadFileMedia(ctx context.Context, content *event.MessageEventContent) (*core.FileAttachment, error) {
	data, err := p.downloadMediaContent(ctx, content.URL)
	if err != nil {
		return nil, err
	}
	mime := ""
	if content.Info != nil {
		mime = content.Info.MimeType
	}
	if mime == "" {
		mime = "application/octet-stream"
	}
	return &core.FileAttachment{
		MimeType: mime,
		Data:     data,
		FileName: content.Body,
	}, nil
}

func (p *Platform) downloadAudioMedia(ctx context.Context, content *event.MessageEventContent) (*core.AudioAttachment, error) {
	data, err := p.downloadMediaContent(ctx, content.URL)
	if err != nil {
		return nil, err
	}
	mime := ""
	audiFmt := ""
	duration := 0
	if content.Info != nil {
		mime = content.Info.MimeType
		duration = content.Info.Duration / 1000
	}
	if mime == "" {
		mime = "audio/ogg"
	}
	if parts := strings.SplitN(mime, "/", 2); len(parts) == 2 {
		audiFmt = parts[1]
	}
	if audiFmt == "" {
		audiFmt = "ogg"
	}
	return &core.AudioAttachment{
		MimeType: mime,
		Data:     data,
		Format:   audiFmt,
		Duration: duration,
	}, nil
}

func stripBotMention(text string, selfID id.UserID) string {
	if selfID == "" {
		return text
	}
	// Strip matrix.to links first (longer pattern), then plain user ID
	text = strings.ReplaceAll(text, fmt.Sprintf("https://matrix.to/#/%s", selfID), "")
	text = strings.ReplaceAll(text, selfID.String(), "")
	return strings.TrimSpace(text)
}

func displayName(userID id.UserID) string {
	// Use the localpart as a fallback display name
	localpart, _, _ := strings.Cut(userID.String(), ":")
	return strings.TrimPrefix(localpart, "@")
}

// --- Concurrency-safe accessors ---

func (p *Platform) isStopping() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.stopping
}

func (p *Platform) getClient() *mautrix.Client {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.client
}

func (p *Platform) getSelfUserID() id.UserID {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.selfUserID
}

func (p *Platform) getHandler() core.MessageHandler {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.handler
}

func (p *Platform) getApprovalResponseHandler() core.ApprovalResponseHandler {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.approvalHandler
}

func (p *Platform) publishClient(client *mautrix.Client, selfUserID id.UserID) (uint64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopping {
		return 0, false
	}
	p.generation++
	p.client = client
	p.selfUserID = selfUserID
	return p.generation, true
}

func (p *Platform) emitReady(gen uint64) {
	p.mu.RLock()
	if p.stopping || p.generation != gen || p.client == nil {
		p.mu.RUnlock()
		return
	}
	handler := p.lifecycleHandler
	p.mu.RUnlock()

	p.mu.Lock()
	p.everConnected = true
	p.unavailableNotified = false
	p.mu.Unlock()

	if handler != nil {
		handler.OnPlatformReady(p)
	}
}

func (p *Platform) clearClient(gen uint64, client *mautrix.Client) {
	notify := false
	p.mu.Lock()
	if p.client == client && p.generation == gen {
		p.client = nil
		notify = !p.stopping
	}
	p.mu.Unlock()

	if notify {
		p.notifyUnavailable(fmt.Errorf("matrix: connection lost"))
	}
}

func (p *Platform) notifyUnavailable(err error) {
	var handler core.PlatformLifecycleHandler

	p.mu.Lock()
	if p.stopping || err == nil || p.unavailableNotified {
		p.mu.Unlock()
		return
	}
	p.unavailableNotified = true
	handler = p.lifecycleHandler
	p.mu.Unlock()

	if handler != nil {
		handler.OnPlatformUnavailable(p, err)
	}
}

// Interface compliance checks
var (
	_ core.Platform                  = (*Platform)(nil)
	_ core.AsyncRecoverablePlatform  = (*Platform)(nil)
	_ core.ReplyContextReconstructor = (*Platform)(nil)
	_ core.ImageSender               = (*Platform)(nil)
	_ core.FileSender                = (*Platform)(nil)
	_ core.MessageUpdater            = (*Platform)(nil)
	_ core.TypingIndicator           = (*Platform)(nil)
)
