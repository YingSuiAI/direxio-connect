package matrix

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/YingSuiAI/dirextalk-connect/core"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// --- Config validation (New) ---

func TestNew_MissingHomeserver(t *testing.T) {
	_, err := New(map[string]any{
		"access_token": "syt_test",
	})
	if err == nil || !strings.Contains(err.Error(), "homeserver is required") {
		t.Fatalf("expected homeserver error, got %v", err)
	}
}

func TestNew_MissingAccessToken(t *testing.T) {
	_, err := New(map[string]any{
		"homeserver": "https://matrix.org",
	})
	if err == nil || !strings.Contains(err.Error(), "access_token is required") {
		t.Fatalf("expected access_token error, got %v", err)
	}
}

func TestNew_MissingRoomID(t *testing.T) {
	_, err := New(map[string]any{
		"homeserver":   "https://matrix.org",
		"access_token": "syt_test",
	})
	if err == nil || !strings.Contains(err.Error(), "room_id is required") {
		t.Fatalf("expected room_id required error, got %v", err)
	}
}

func TestNew_DoesNotFallbackToLegacyAllowedRoomID(t *testing.T) {
	_, err := New(map[string]any{
		"homeserver":      "https://matrix.org",
		"access_token":    "syt_test",
		"allowed_room_id": "!room:matrix.org",
	})
	if err == nil || !strings.Contains(err.Error(), "room_id is required") {
		t.Fatalf("expected room_id required error, got %v", err)
	}
}

func TestNew_ValidConfig(t *testing.T) {
	p, err := New(map[string]any{
		"homeserver":   "https://matrix.org",
		"access_token": "syt_test",
		"room_id":      "!room:matrix.org",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plat := p.(*Platform)
	if plat.homeserver != "https://matrix.org" {
		t.Errorf("homeserver = %q, want https://matrix.org", plat.homeserver)
	}
	if plat.accessToken != "syt_test" {
		t.Errorf("accessToken = %q, want syt_test", plat.accessToken)
	}
	if plat.allowedRoomID != "!room:matrix.org" {
		t.Errorf("allowedRoomID = %q, want !room:matrix.org", plat.allowedRoomID)
	}
	if plat.autoJoin != true {
		t.Error("autoJoin should default to true")
	}
}

func TestNew_ApprovalOwnerID(t *testing.T) {
	for _, invalid := range []any{"not-a-matrix-user", 7} {
		_, err := New(map[string]any{
			"homeserver":        "https://matrix.org",
			"access_token":      "syt_test",
			"room_id":           "!room:matrix.org",
			"approval_owner_id": invalid,
		})
		if err == nil || !strings.Contains(err.Error(), "approval_owner_id must be a Matrix user ID") {
			t.Fatalf("expected approval_owner_id validation error for %#v, got %v", invalid, err)
		}
	}

	p, err := New(map[string]any{
		"homeserver":        "https://matrix.org",
		"access_token":      "syt_test",
		"room_id":           "!room:matrix.org",
		"approval_owner_id": "@owner:matrix.org",
	})
	if err != nil {
		t.Fatalf("valid approval owner rejected: %v", err)
	}
	if got := p.(*Platform).approvalOwnerID; got != "@owner:matrix.org" {
		t.Fatalf("approvalOwnerID = %q", got)
	}
}

func TestNew_AutoJoinDefault(t *testing.T) {
	p, _ := New(map[string]any{
		"homeserver":   "https://matrix.org",
		"access_token": "tok",
		"room_id":      "!room:matrix.org",
	})
	plat := p.(*Platform)
	if !plat.autoJoin {
		t.Error("autoJoin should default to true when not specified")
	}
}

func TestNew_AutoJoinExplicitFalse(t *testing.T) {
	p, _ := New(map[string]any{
		"homeserver":   "https://matrix.org",
		"access_token": "tok",
		"room_id":      "!room:matrix.org",
		"auto_join":    false,
	})
	plat := p.(*Platform)
	if plat.autoJoin {
		t.Error("autoJoin should be false when explicitly set to false")
	}
}

func TestNew_AutoVerifyDefault(t *testing.T) {
	p, _ := New(map[string]any{
		"homeserver":   "https://matrix.org",
		"access_token": "tok",
		"room_id":      "!room:matrix.org",
	})
	plat := p.(*Platform)
	if !plat.autoVerify {
		t.Error("autoVerify should default to true when not specified")
	}
}

func TestNew_AutoVerifyExplicitFalse(t *testing.T) {
	p, _ := New(map[string]any{
		"homeserver":   "https://matrix.org",
		"access_token": "tok",
		"room_id":      "!room:matrix.org",
		"auto_verify":  false,
	})
	plat := p.(*Platform)
	if plat.autoVerify {
		t.Error("autoVerify should be false when explicitly set to false")
	}
}

func TestNew_ProxyInvalidURL(t *testing.T) {
	_, err := New(map[string]any{
		"homeserver":   "https://matrix.org",
		"access_token": "tok",
		"room_id":      "!room:matrix.org",
		"proxy":        "://bad",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid proxy URL") {
		t.Fatalf("expected proxy error, got %v", err)
	}
}

func TestNew_ProxyValidURL(t *testing.T) {
	p, err := New(map[string]any{
		"homeserver":   "https://matrix.org",
		"access_token": "tok",
		"room_id":      "!room:matrix.org",
		"proxy":        "http://proxy:8080",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plat := p.(*Platform)
	if plat.proxyURL != "http://proxy:8080" {
		t.Errorf("proxyURL = %q", plat.proxyURL)
	}
}

func TestNew_AllOptions(t *testing.T) {
	p, err := New(map[string]any{
		"homeserver":               "https://matrix.org",
		"access_token":             "tok",
		"user_id":                  "@bot:matrix.org",
		"allow_from":               "@alice:matrix.org",
		"room_id":                  "!room:matrix.org",
		"auto_join":                false,
		"share_session_in_channel": true,
		"group_reply_all":          true,
		"proxy":                    "socks5://proxy:1080",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plat := p.(*Platform)
	if plat.userID != "@bot:matrix.org" {
		t.Errorf("userID = %q", plat.userID)
	}
	if plat.allowFrom != "@alice:matrix.org" {
		t.Errorf("allowFrom = %q", plat.allowFrom)
	}
	if plat.allowedRoomID != "!room:matrix.org" {
		t.Errorf("allowedRoomID = %q", plat.allowedRoomID)
	}
	if plat.shareSessionInChannel != true {
		t.Error("shareSessionInChannel should be true")
	}
	if plat.groupReplyAll != true {
		t.Error("groupReplyAll should be true")
	}
}

func TestNew_InvalidRoomID(t *testing.T) {
	for _, roomID := range []string{"not-a-room", "!room"} {
		t.Run(roomID, func(t *testing.T) {
			_, err := New(map[string]any{
				"homeserver":   "https://matrix.org",
				"access_token": "tok",
				"room_id":      roomID,
			})
			if err == nil || !strings.Contains(err.Error(), "room_id must be a Matrix room ID") {
				t.Fatalf("expected room_id error, got %v", err)
			}
		})
	}
}

func TestNew_RejectsLegacyPseudoAgentRoomID(t *testing.T) {
	_, err := New(map[string]any{
		"homeserver":   "https://matrix.org",
		"access_token": "tok",
		"room_id":      "!agent:matrix.org",
	})
	if err == nil || !strings.Contains(err.Error(), "legacy !agent:<server>") {
		t.Fatalf("expected legacy room_id error, got %v", err)
	}
}

// --- Name ---

func TestPlatform_Name(t *testing.T) {
	p, _ := New(map[string]any{
		"homeserver":   "https://matrix.org",
		"access_token": "tok",
		"room_id":      "!room:matrix.org",
	})
	if p.Name() != "matrix" {
		t.Errorf("Name() = %q, want matrix", p.Name())
	}
}

// --- Helper functions ---

func TestStripBotMention(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		selfID string
		want   string
	}{
		{"empty selfID", "hello", "", "hello"},
		{"no mention", "hello world", "@bot:matrix.org", "hello world"},
		{"plain mention", "hello @bot:matrix.org how are you", "@bot:matrix.org", "hello  how are you"},
		{"matrix.to link", "https://matrix.to/#/@bot:matrix.org hello", "@bot:matrix.org", "hello"},
		{"mention at start", "@bot:matrix.org do something", "@bot:matrix.org", "do something"},
		{"mention at end", "do something @bot:matrix.org", "@bot:matrix.org", "do something"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripBotMention(tt.text, id.UserID(tt.selfID))
			if got != tt.want {
				t.Errorf("stripBotMention(%q, %q) = %q, want %q", tt.text, tt.selfID, got, tt.want)
			}
		})
	}
}

func TestDisplayName(t *testing.T) {
	tests := []struct {
		userID id.UserID
		want   string
	}{
		{"@alice:matrix.org", "alice"},
		{"@bob:synapse.example.com", "bob"},
		{"@user_name:server.org", "user_name"},
	}
	for _, tt := range tests {
		t.Run(string(tt.userID), func(t *testing.T) {
			got := displayName(tt.userID)
			if got != tt.want {
				t.Errorf("displayName(%q) = %q, want %q", tt.userID, got, tt.want)
			}
		})
	}
}

func TestBuildSessionKey(t *testing.T) {
	p := &Platform{}

	// Per-user (default)
	key := p.buildSessionKey("!room:server", "@user:server")
	want := "matrix:!room:server:@user:server"
	if key != want {
		t.Errorf("per-user key = %q, want %q", key, want)
	}

	// Shared session
	p.shareSessionInChannel = true
	key = p.buildSessionKey("!room:server", "@user:server")
	want = "matrix:!room:server"
	if key != want {
		t.Errorf("shared key = %q, want %q", key, want)
	}
}

func TestIsDirectedAtBot(t *testing.T) {
	p := &Platform{}
	selfID := id.UserID("@bot:matrix.org")

	tests := []struct {
		name    string
		content *event.MessageEventContent
		want    bool
	}{
		{
			"plain mention",
			&event.MessageEventContent{Body: "hey @bot:matrix.org help"},
			true,
		},
		{
			"matrix.to link",
			&event.MessageEventContent{
				Body:          "help",
				FormattedBody: `<a href="https://matrix.to/#/@bot:matrix.org">bot</a> help`,
			},
			true,
		},
		{
			"no mention",
			&event.MessageEventContent{Body: "just chatting"},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.isDirectedAtBot(tt.content, selfID)
			if got != tt.want {
				t.Errorf("isDirectedAtBot() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRoomAllowedRejectsWhenUnconfigured(t *testing.T) {
	p := &Platform{}

	if p.roomAllowed("!room:server") {
		t.Fatal("roomAllowed should reject all rooms when room_id is not configured")
	}
}

// --- ReconstructReplyCtx ---

func TestReconstructReplyCtx(t *testing.T) {
	p := &Platform{allowedRoomID: "!abc:server"}

	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{"valid per-user", "matrix:!abc:server:@user:server", false},
		{"valid shared", "matrix:!abc:server", false},
		{"missing prefix", "telegram:!abc:server", true},
		{"too short", "matrix:", true},
		{"empty", "", true},
		{"invalid room ID", "matrix:notaroom", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rctx, err := p.ReconstructReplyCtx(tt.key)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			rc, ok := rctx.(replyContext)
			if !ok {
				t.Fatal("expected replyContext type")
			}
			if tt.key == "matrix:!abc:server:@user:server" {
				if rc.roomID != "!abc:server" {
					t.Errorf("roomID = %q, want !abc:server", rc.roomID)
				}
			}
		})
	}
}

func TestReconstructReplyCtxRejectsDisallowedRoom(t *testing.T) {
	p := &Platform{allowedRoomID: "!allowed:server"}

	if _, err := p.ReconstructReplyCtx("matrix:!other:server"); err == nil {
		t.Fatal("expected disallowed room error")
	}
	rctx, err := p.ReconstructReplyCtx("matrix:!allowed:server")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rc, ok := rctx.(replyContext)
	if !ok {
		t.Fatal("expected replyContext")
	}
	if rc.roomID != "!allowed:server" {
		t.Fatalf("roomID = %q, want !allowed:server", rc.roomID)
	}
}

// --- Reply/Send error paths ---

func TestReply_NotConnected(t *testing.T) {
	p := &Platform{}
	err := p.Reply(context.Background(), replyContext{roomID: "!room:s", messageID: "$evt"}, "hello")
	if err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("expected not connected error, got %v", err)
	}
}

func TestReply_InvalidContext(t *testing.T) {
	p := &Platform{}
	err := p.Reply(context.Background(), "not-a-replyContext", "hello")
	if err == nil || !strings.Contains(err.Error(), "invalid reply context") {
		t.Fatalf("expected invalid context error, got %v", err)
	}
}

func TestSend_NotConnected(t *testing.T) {
	p := &Platform{}
	err := p.Send(context.Background(), replyContext{roomID: "!room:s", messageID: "$evt"}, "hello")
	if err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("expected not connected error, got %v", err)
	}
}

func TestSendImage_NotConnected(t *testing.T) {
	p := &Platform{}
	err := p.SendImage(context.Background(), replyContext{roomID: "!room:s"}, core.ImageAttachment{Data: []byte("x")})
	if err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("expected not connected error, got %v", err)
	}
}

func TestSendFile_NotConnected(t *testing.T) {
	p := &Platform{}
	err := p.SendFile(context.Background(), replyContext{roomID: "!room:s"}, core.FileAttachment{Data: []byte("x")})
	if err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("expected not connected error, got %v", err)
	}
}

func TestUpdateMessage_NotConnected(t *testing.T) {
	p := &Platform{}
	err := p.UpdateMessage(context.Background(), replyContext{roomID: "!room:s", messageID: "$evt"}, "edited")
	if err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("expected not connected error, got %v", err)
	}
}

func TestStartTyping_NotConnected(t *testing.T) {
	p := &Platform{}
	stop := p.StartTyping(context.Background(), replyContext{roomID: "!room:s"})
	stop()
}

func TestStartTyping_InvalidContext(t *testing.T) {
	p := &Platform{}
	stop := p.StartTyping(context.Background(), "bad")
	stop()
}

// --- Lifecycle: Start / Stop ---

func TestPlatform_StopWithoutStart(t *testing.T) {
	p, _ := New(map[string]any{
		"homeserver":   "https://matrix.org",
		"access_token": "tok",
		"room_id":      "!room:matrix.org",
	})
	err := p.Stop()
	if err != nil {
		t.Errorf("Stop() returned error: %v", err)
	}
}

func TestPlatform_StartStopIdempotent(t *testing.T) {
	p, _ := New(map[string]any{
		"homeserver":   "https://matrix.org",
		"access_token": "tok",
		"room_id":      "!room:matrix.org",
	})
	plat := p.(*Platform)

	_ = plat.Start(func(_ core.Platform, _ *core.Message) {})
	_ = plat.Stop()
	_ = plat.Stop()

	err := plat.Start(func(_ core.Platform, _ *core.Message) {})
	if err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Errorf("expected stopped error, got %v", err)
	}
}

func TestPlatform_StopPublishesAgentRoomOfflineStatus(t *testing.T) {
	statusEvents := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		wantPath := "/_matrix/client/v3/rooms/!room:matrix.org/state/io.dirextalk.agent.status/@agent:matrix.org"
		if r.URL.Path != wantPath {
			t.Errorf("path = %s, want %s", r.URL.Path, wantPath)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		statusEvents <- body
		_, _ = w.Write([]byte(`{"event_id":"$offline"}`))
	}))
	defer server.Close()

	client, err := mautrix.NewClient(server.URL, "", "tok")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	client.Client = server.Client()

	p := &Platform{
		client:        client,
		selfUserID:    "@agent:matrix.org",
		allowedRoomID: "!room:matrix.org",
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop() returned error: %v", err)
	}

	select {
	case got := <-statusEvents:
		if got["online"] != false {
			t.Fatalf("online = %v, want false", got["online"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not publish offline status")
	}
}

func TestPlatform_RunConnectionPublishesAgentRoomOnlineStatus(t *testing.T) {
	statusEvents := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/_matrix/client/v3/account/whoami":
			_, _ = w.Write([]byte(`{"user_id":"@agent:matrix.org","device_id":"DEVICE"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/_matrix/client/v3/keys/query":
			http.Error(w, "e2ee disabled in status test", http.StatusServiceUnavailable)
		case r.Method == http.MethodPut && r.URL.Path == "/_matrix/client/v3/rooms/!room:matrix.org/state/io.dirextalk.agent.status/@agent:matrix.org":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode request body: %v", err)
			}
			statusEvents <- body
			_, _ = w.Write([]byte(`{"event_id":"$online"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/_matrix/client/v3/sync":
			<-r.Context().Done()
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := &Platform{
		homeserver:    server.URL,
		accessToken:   "tok",
		userID:        "@agent:matrix.org",
		allowedRoomID: "!room:matrix.org",
		httpClient:    server.Client(),
		dedup:         core.MessageDedup{},
	}
	done := make(chan error, 1)
	go func() {
		done <- p.runConnection(ctx)
	}()

	select {
	case got := <-statusEvents:
		if got["online"] != true {
			t.Fatalf("online = %v, want true", got["online"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runConnection() did not publish online status")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runConnection() returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runConnection() did not stop after context cancellation")
	}
}

func TestPlatform_RunConnectionRefreshesAgentRoomOnlineStatus(t *testing.T) {
	oldRefreshInterval := agentRoomStatusRefreshInterval
	agentRoomStatusRefreshInterval = 20 * time.Millisecond
	t.Cleanup(func() {
		agentRoomStatusRefreshInterval = oldRefreshInterval
	})

	statusEvents := make(chan map[string]any, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/_matrix/client/v3/account/whoami":
			_, _ = w.Write([]byte(`{"user_id":"@agent:matrix.org","device_id":"DEVICE"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/_matrix/client/v3/keys/query":
			http.Error(w, "e2ee disabled in status test", http.StatusServiceUnavailable)
		case r.Method == http.MethodPost && r.URL.Path == "/_matrix/client/v3/user/@agent:matrix.org/filter":
			_, _ = w.Write([]byte(`{"filter_id":"test-filter"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/_matrix/client/v3/rooms/!room:matrix.org/state/io.dirextalk.agent.status/@agent:matrix.org":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode request body: %v", err)
			}
			select {
			case statusEvents <- body:
			default:
			}
			_, _ = w.Write([]byte(`{"event_id":"$online"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/_matrix/client/v3/sync":
			<-r.Context().Done()
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := &Platform{
		homeserver:    server.URL,
		accessToken:   "tok",
		userID:        "@agent:matrix.org",
		allowedRoomID: "!room:matrix.org",
		httpClient:    server.Client(),
		dedup:         core.MessageDedup{},
	}
	done := make(chan error, 1)
	go func() {
		done <- p.runConnection(ctx)
	}()

	onlineEvents := 0
	deadline := time.After(500 * time.Millisecond)
	for onlineEvents < 2 {
		select {
		case got := <-statusEvents:
			if got["online"] != true {
				t.Fatalf("online = %v, want true", got["online"])
			}
			onlineEvents++
		case <-deadline:
			t.Fatalf("published %d online status events, want at least 2", onlineEvents)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runConnection() returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runConnection() did not stop after context cancellation")
	}
}

func TestPlatform_SetLifecycleHandler(t *testing.T) {
	p, _ := New(map[string]any{
		"homeserver":   "https://matrix.org",
		"access_token": "tok",
		"room_id":      "!room:matrix.org",
	})
	plat := p.(*Platform)

	plat.SetLifecycleHandler(&testLifecycleHandler{
		onUnavailable: func(_ core.Platform, _ error) {},
	})
	if plat.lifecycleHandler == nil {
		t.Error("lifecycleHandler should be set")
	}
}

func newApprovalTimelineTestPlatform(t *testing.T) (*Platform, <-chan map[string]any) {
	t.Helper()
	sent := make(chan map[string]any, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || !strings.Contains(r.URL.Path, "/send/m.room.message/") {
			t.Errorf("unexpected approval timeline request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode approval timeline body: %v", err)
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		sent <- body
		_, _ = w.Write([]byte(`{"event_id":"$approval"}`))
	}))
	t.Cleanup(server.Close)

	client, err := mautrix.NewClient(server.URL, "", "tok")
	if err != nil {
		t.Fatalf("create Matrix client: %v", err)
	}
	client.Client = server.Client()
	return &Platform{
		client:          client,
		selfUserID:      "@agent:matrix.org",
		allowedRoomID:   "!room:matrix.org",
		approvalOwnerID: "@owner:matrix.org",
		dedup:           core.MessageDedup{},
		groupReplyAll:   true,
	}, sent
}

func takeApprovalTimelineEvent(t *testing.T, sent <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case body := <-sent:
		return body
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval timeline event")
		return nil
	}
}

func assertNoApprovalTimelineEvent(t *testing.T, sent <-chan map[string]any) {
	t.Helper()
	select {
	case body := <-sent:
		t.Fatalf("unexpected approval timeline event: %#v", body)
	default:
	}
}

func TestSendApprovalRequest_RequiresConfiguredOwner(t *testing.T) {
	p := &Platform{allowedRoomID: "!room:matrix.org"}
	err := p.SendApprovalRequest(context.Background(), replyContext{roomID: "!room:matrix.org"}, core.ApprovalRequest{
		ApprovalID: "de305d54-75b4-431b-adb2-eb6b9e546014",
		Kind:       "tool",
		ToolName:   "Bash",
		Summary:    "A local agent requests permission.",
	})
	if err != core.ErrApprovalBridgeUnavailable {
		t.Fatalf("SendApprovalRequest without approval_owner_id = %v, want ErrApprovalBridgeUnavailable", err)
	}
}

func TestSendApprovalRequest_UsesStrictSafeEnvelope(t *testing.T) {
	p, sent := newApprovalTimelineTestPlatform(t)
	const approvalID = "de305d54-75b4-431b-adb2-eb6b9e546014"
	if err := p.SendApprovalRequest(context.Background(), replyContext{roomID: "!room:matrix.org"}, core.ApprovalRequest{
		ApprovalID: approvalID,
		Kind:       "tool",
		ToolName:   "Bash",
		Summary:    "A local agent requests permission.",
	}); err != nil {
		t.Fatalf("SendApprovalRequest: %v", err)
	}

	body := takeApprovalTimelineEvent(t, sent)
	if len(body) != 3 || body["msgtype"] != string(approvalRequestMsgType) {
		t.Fatalf("request content = %#v, want only custom msgtype/body/envelope", body)
	}
	if body["body"] != "Approval requested. Use a compatible client to allow or deny." {
		t.Fatalf("request body = %#v", body["body"])
	}
	envelope, ok := body[approvalEnvelopeKey].(map[string]any)
	if !ok || len(envelope) != 5 {
		t.Fatalf("request envelope = %#v", body[approvalEnvelopeKey])
	}
	if envelope["schema"] != approvalRequestSchema || envelope["approval_id"] != approvalID ||
		envelope["kind"] != "tool" || envelope["tool_name"] != "Bash" ||
		envelope["summary"] != "A local agent requests permission." {
		t.Fatalf("request envelope = %#v", envelope)
	}
	for _, forbidden := range []string{"command", "request_id", "session_key", "api_key"} {
		if _, exists := body[forbidden]; exists {
			t.Fatalf("request leaked forbidden top-level field %q: %#v", forbidden, body)
		}
	}
}

func TestSendApprovalResult_AllowsCodeOnlyForFailedOutcome(t *testing.T) {
	p, sent := newApprovalTimelineTestPlatform(t)
	const approvalID = "de305d54-75b4-431b-adb2-eb6b9e546014"
	if err := p.sendApprovalResult(context.Background(), replyContext{roomID: "!room:matrix.org"}, core.ApprovalResult{
		ApprovalID: approvalID,
		Outcome:    "expired",
		Code:       "backend_response_failed",
	}); err == nil {
		t.Fatal("non-failed approval result with code was accepted")
	}
	assertNoApprovalTimelineEvent(t, sent)

	if err := p.sendApprovalResult(context.Background(), replyContext{roomID: "!room:matrix.org"}, core.ApprovalResult{
		ApprovalID: approvalID,
		Outcome:    "failed",
		Code:       "backend_response_failed",
	}); err != nil {
		t.Fatalf("failed approval result: %v", err)
	}
	body := takeApprovalTimelineEvent(t, sent)
	envelope, ok := body[approvalEnvelopeKey].(map[string]any)
	if !ok || len(envelope) != 4 || envelope["schema"] != approvalResultSchema ||
		envelope["approval_id"] != approvalID || envelope["outcome"] != "failed" ||
		envelope["code"] != "backend_response_failed" {
		t.Fatalf("failed result envelope = %#v", body[approvalEnvelopeKey])
	}
}

func approvalResponseEvent(eventID id.EventID, roomID id.RoomID, sender id.UserID, approvalID string, extra map[string]any) *event.Event {
	raw := map[string]any{
		"msgtype": string(approvalResponseMsgType),
		"body":    "Approval response submitted.",
		approvalEnvelopeKey: map[string]any{
			"schema":      approvalResponseSchema,
			"approval_id": approvalID,
			"decision":    "allow",
		},
	}
	for key, value := range extra {
		raw[key] = value
	}
	return &event.Event{
		RoomID:    roomID,
		Sender:    sender,
		ID:        eventID,
		Type:      event.EventMessage,
		Timestamp: time.Now().UnixMilli(),
		Content: event.Content{
			Raw: raw,
			Parsed: &event.MessageEventContent{
				MsgType: approvalResponseMsgType,
				Body:    "Approval response submitted.",
			},
		},
	}
}

func TestHandleMessage_ApprovalResponseRequiresExactRoomOwnerAndSchema(t *testing.T) {
	p, sent := newApprovalTimelineTestPlatform(t)
	const approvalID = "de305d54-75b4-431b-adb2-eb6b9e546014"
	var ownerResponses []core.ApprovalResponse
	var ordinaryMessages int
	p.handler = func(_ core.Platform, _ *core.Message) { ordinaryMessages++ }
	p.SetApprovalResponseHandler(func(response core.ApprovalResponse) core.ApprovalResult {
		ownerResponses = append(ownerResponses, response)
		return core.ApprovalResult{ApprovalID: response.ApprovalID, Outcome: "expired"}
	})

	p.handleMessage(context.Background(), approvalResponseEvent("$foreign", "!room:matrix.org", "@other:matrix.org", approvalID, nil))
	p.handleMessage(context.Background(), approvalResponseEvent("$other-room", "!other:matrix.org", "@owner:matrix.org", approvalID, nil))
	p.handleMessage(context.Background(), approvalResponseEvent("$malformed", "!room:matrix.org", "@owner:matrix.org", approvalID, map[string]any{"unexpected": true}))
	if len(ownerResponses) != 0 || ordinaryMessages != 0 {
		t.Fatalf("non-owner, cross-room, or malformed responses had side effects: owner=%#v ordinary=%d", ownerResponses, ordinaryMessages)
	}
	assertNoApprovalTimelineEvent(t, sent)

	valid := approvalResponseEvent("$valid", "!room:matrix.org", "@owner:matrix.org", approvalID, nil)
	p.handleMessage(context.Background(), valid)
	p.handleMessage(context.Background(), valid) // duplicate event ID is one-shot at Matrix ingress too.
	if len(ownerResponses) != 1 || ownerResponses[0] != (core.ApprovalResponse{ApprovalID: approvalID, Decision: "allow"}) {
		t.Fatalf("owner responses = %#v", ownerResponses)
	}
	if ordinaryMessages != 0 {
		t.Fatalf("approval response entered ordinary message dispatch %d times", ordinaryMessages)
	}

	result := takeApprovalTimelineEvent(t, sent)
	if len(result) != 3 || result["msgtype"] != string(approvalResultMsgType) || result["body"] != "Approval expired." {
		t.Fatalf("result content = %#v", result)
	}
	envelope, ok := result[approvalEnvelopeKey].(map[string]any)
	if !ok || len(envelope) != 3 || envelope["schema"] != approvalResultSchema ||
		envelope["approval_id"] != approvalID || envelope["outcome"] != "expired" {
		t.Fatalf("result envelope = %#v", result[approvalEnvelopeKey])
	}
	assertNoApprovalTimelineEvent(t, sent)
}

// --- Dedup in handleMessage ---

func TestHandleMessage_SkipsOwnMessages(t *testing.T) {
	p := &Platform{
		selfUserID:    "@bot:matrix.org",
		allowedRoomID: "!room:server",
		dedup:         core.MessageDedup{},
		groupReplyAll: true,
	}

	var dispatched []*core.Message
	p.handler = func(_ core.Platform, msg *core.Message) {
		dispatched = append(dispatched, msg)
	}

	evt := &event.Event{
		RoomID: "!room:server",
		Sender: "@bot:matrix.org",
		ID:     "$own_msg",
		Type:   event.EventMessage,
		Content: event.Content{
			Parsed: &event.MessageEventContent{
				MsgType: event.MsgText,
				Body:    "my own message",
			},
		},
	}
	p.handleMessage(context.Background(), evt)

	if len(dispatched) != 0 {
		t.Error("should not dispatch own message")
	}
}

func TestHandleMessage_SkipsDuplicates(t *testing.T) {
	p := &Platform{
		selfUserID:    "@bot:matrix.org",
		allowedRoomID: "!room:server",
		dedup:         core.MessageDedup{},
		groupReplyAll: true,
	}

	var count int
	p.handler = func(_ core.Platform, msg *core.Message) {
		count++
	}

	evt := &event.Event{
		RoomID:    "!room:server",
		Sender:    "@user:matrix.org",
		ID:        "$dup_msg",
		Type:      event.EventMessage,
		Timestamp: time.Now().UnixMilli(),
		Content: event.Content{
			Parsed: &event.MessageEventContent{
				MsgType: event.MsgText,
				Body:    "hello",
			},
		},
	}

	p.handleMessage(context.Background(), evt)
	p.handleMessage(context.Background(), evt)

	if count != 1 {
		t.Errorf("dispatched %d times, want 1", count)
	}
}

func TestHandleMessage_SkipsOldMessages(t *testing.T) {
	orig := core.StartTime
	core.StartTime = time.Now()
	defer func() { core.StartTime = orig }()

	p := &Platform{
		selfUserID:    "@bot:matrix.org",
		allowedRoomID: "!room:server",
		dedup:         core.MessageDedup{},
		groupReplyAll: true,
	}

	var count int
	p.handler = func(_ core.Platform, msg *core.Message) {
		count++
	}

	evt := &event.Event{
		RoomID:    "!room:server",
		Sender:    "@user:matrix.org",
		ID:        "$old_msg",
		Type:      event.EventMessage,
		Timestamp: time.Now().Add(-1 * time.Hour).UnixMilli(),
		Content: event.Content{
			Parsed: &event.MessageEventContent{
				MsgType: event.MsgText,
				Body:    "old",
			},
		},
	}
	p.handleMessage(context.Background(), evt)

	if count != 0 {
		t.Error("should not dispatch old message")
	}
}

func TestHandleMessage_DispatchesText(t *testing.T) {
	p := &Platform{
		selfUserID:    "@bot:matrix.org",
		allowedRoomID: "!room:server",
		dedup:         core.MessageDedup{},
		groupReplyAll: true,
	}

	var received *core.Message
	p.handler = func(_ core.Platform, msg *core.Message) {
		received = msg
	}

	evt := &event.Event{
		RoomID:    "!room:server",
		Sender:    "@alice:matrix.org",
		ID:        "$text_msg",
		Type:      event.EventMessage,
		Timestamp: time.Now().UnixMilli(),
		Content: event.Content{
			Parsed: &event.MessageEventContent{
				MsgType: event.MsgText,
				Body:    "hello bot",
			},
		},
	}
	p.handleMessage(context.Background(), evt)

	if received == nil {
		t.Fatal("expected message to be dispatched")
	}
	if received.Content != "hello bot" {
		t.Errorf("Content = %q, want hello bot", received.Content)
	}
	if received.UserID != "@alice:matrix.org" {
		t.Errorf("UserID = %q", received.UserID)
	}
	if received.UserName != "alice" {
		t.Errorf("UserName = %q", received.UserName)
	}
	if received.Platform != "matrix" {
		t.Errorf("Platform = %q", received.Platform)
	}
	if received.ChannelKey != "!room:server" {
		t.Errorf("ChannelKey = %q", received.ChannelKey)
	}
}

func TestHandleMessage_SkipsDisallowedRoom(t *testing.T) {
	p := &Platform{
		selfUserID:    "@bot:matrix.org",
		allowedRoomID: "!room:server",
		dedup:         core.MessageDedup{},
		groupReplyAll: true,
	}

	var count int
	p.handler = func(_ core.Platform, msg *core.Message) {
		count++
	}

	evt := &event.Event{
		RoomID:    "!other:server",
		Sender:    "@alice:matrix.org",
		ID:        "$text_msg",
		Type:      event.EventMessage,
		Timestamp: time.Now().UnixMilli(),
		Content: event.Content{
			Parsed: &event.MessageEventContent{
				MsgType: event.MsgText,
				Body:    "hello bot",
			},
		},
	}
	p.handleMessage(context.Background(), evt)

	if count != 0 {
		t.Fatalf("dispatched %d messages from a disallowed room", count)
	}
}

func TestHandleMessage_DispatchesAllowedRoom(t *testing.T) {
	p := &Platform{
		selfUserID:    "@bot:matrix.org",
		allowedRoomID: "!room:server",
		dedup:         core.MessageDedup{},
		groupReplyAll: true,
	}

	var received *core.Message
	p.handler = func(_ core.Platform, msg *core.Message) {
		received = msg
	}

	evt := &event.Event{
		RoomID:    "!room:server",
		Sender:    "@alice:matrix.org",
		ID:        "$text_msg",
		Type:      event.EventMessage,
		Timestamp: time.Now().UnixMilli(),
		Content: event.Content{
			Parsed: &event.MessageEventContent{
				MsgType: event.MsgText,
				Body:    "hello agent",
			},
		},
	}
	p.handleMessage(context.Background(), evt)

	if received == nil {
		t.Fatal("expected message from allowed room")
	}
	if received.ChannelKey != "!room:server" {
		t.Fatalf("ChannelKey = %q, want !room:server", received.ChannelKey)
	}
}

func TestHandleMessage_NoticeAndEmote(t *testing.T) {
	for _, msgType := range []event.MessageType{event.MsgNotice, event.MsgEmote} {
		t.Run(string(msgType), func(t *testing.T) {
			p := &Platform{
				selfUserID:    "@bot:matrix.org",
				allowedRoomID: "!room:s",
				dedup:         core.MessageDedup{},
				groupReplyAll: true,
			}
			var received *core.Message
			p.handler = func(_ core.Platform, msg *core.Message) {
				received = msg
			}
			evt := &event.Event{
				RoomID:    "!room:s",
				Sender:    "@user:s",
				ID:        id.EventID("$" + string(msgType)),
				Type:      event.EventMessage,
				Timestamp: time.Now().UnixMilli(),
				Content: event.Content{
					Parsed: &event.MessageEventContent{
						MsgType: msgType,
						Body:    "test",
					},
				},
			}
			p.handleMessage(context.Background(), evt)
			if received == nil {
				t.Fatalf("%s not dispatched", msgType)
			}
		})
	}
}

// --- handleMemberState (auto-join) ---

func TestHandleMemberState_AutoJoinDisabled(t *testing.T) {
	p := &Platform{
		autoJoin:   false,
		selfUserID: "@bot:matrix.org",
	}
	stateKey := "@bot:matrix.org"
	evt := &event.Event{
		RoomID:   "!room:s",
		StateKey: &stateKey,
		Content: event.Content{
			Parsed: &event.MemberEventContent{
				Membership: event.MembershipInvite,
			},
		},
	}
	p.handleMemberState(context.Background(), evt)
}

func TestHandleMemberState_NotForSelf(t *testing.T) {
	p := &Platform{
		autoJoin:      true,
		selfUserID:    "@bot:matrix.org",
		allowedRoomID: "!room:s",
	}
	stateKey := "@other:matrix.org"
	evt := &event.Event{
		RoomID:   "!room:s",
		StateKey: &stateKey,
		Content: event.Content{
			Parsed: &event.MemberEventContent{
				Membership: event.MembershipInvite,
			},
		},
	}
	p.handleMemberState(context.Background(), evt)
}

// --- Concurrency-safe accessors ---

func TestConcurrentAccess(t *testing.T) {
	p := &Platform{}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = p.getClient()
			_ = p.getSelfUserID()
			_ = p.getHandler()
			_ = p.isStopping()
		}()
	}
	wg.Wait()
}

// --- Interface compliance ---

func TestInterfaceCompliance(t *testing.T) {
	p, err := New(map[string]any{
		"homeserver":   "https://matrix.org",
		"access_token": "tok",
		"room_id":      "!room:matrix.org",
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Verify Platform interface is satisfied at compile time
	var _ core.Platform = (*Platform)(nil)

	if _, ok := p.(core.AsyncRecoverablePlatform); !ok {
		t.Error("should implement AsyncRecoverablePlatform")
	}
	if _, ok := p.(core.ReplyContextReconstructor); !ok {
		t.Error("should implement ReplyContextReconstructor")
	}
	if _, ok := p.(core.ImageSender); !ok {
		t.Error("should implement ImageSender")
	}
	if _, ok := p.(core.FileSender); !ok {
		t.Error("should implement FileSender")
	}
	if _, ok := p.(core.MessageUpdater); !ok {
		t.Error("should implement MessageUpdater")
	}
	if _, ok := p.(core.TypingIndicator); !ok {
		t.Error("should implement TypingIndicator")
	}
}

// --- test helpers ---

type testLifecycleHandler struct {
	onReady       func(core.Platform)
	onUnavailable func(core.Platform, error)
}

func (h testLifecycleHandler) OnPlatformReady(p core.Platform) {
	if h.onReady != nil {
		h.onReady(p)
	}
}

func (h testLifecycleHandler) OnPlatformUnavailable(p core.Platform, err error) {
	if h.onUnavailable != nil {
		h.onUnavailable(p, err)
	}
}
