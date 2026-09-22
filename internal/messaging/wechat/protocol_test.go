package wechat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/messaging"
)

func TestClientLifecycleAndGetUpdatesProtocolContract(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("AuthorizationType"); got != "ilink_bot_token" {
			t.Errorf("AuthorizationType = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("iLink-App-Id"); got != "bot" {
			t.Errorf("iLink-App-Id = %q", got)
		}
		if got := r.Header.Get("iLink-App-ClientVersion"); got == "" || got == "0" {
			t.Errorf("iLink-App-ClientVersion = %q, want non-empty non-zero", got)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		baseInfo, ok := body["base_info"].(map[string]any)
		if !ok {
			t.Errorf("base_info = %#v, want object", body["base_info"])
		} else if baseInfo["channel_version"] == "" || baseInfo["bot_agent"] == "" {
			t.Errorf("base_info = %#v, want channel_version and bot_agent", baseInfo)
		}
		paths = append(paths, r.URL.Path)

		switch r.URL.Path {
		case "/ilink/bot/msg/notifystart", "/ilink/bot/msg/notifystop":
			_, _ = io.WriteString(w, `{"ret":0}`)
		case "/ilink/bot/getupdates":
			if got := body["get_updates_buf"]; got != "prior-cursor" {
				t.Errorf("get_updates_buf = %#v, want prior-cursor", got)
			}
			_, _ = io.WriteString(w, `{"ret":0,"msgs":[],"get_updates_buf":"next-cursor","longpolling_timeout_ms":42000}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient()
	client.HTTP = server.Client()
	if err := client.NotifyStart(context.Background(), server.URL, "token"); err != nil {
		t.Fatalf("NotifyStart: %v", err)
	}
	updates, err := client.GetUpdatesWithTimeout(context.Background(), server.URL, "token", "prior-cursor", 3*time.Second)
	if err != nil {
		t.Fatalf("GetUpdatesWithTimeout: %v", err)
	}
	if updates.GetUpdatesBuf != "next-cursor" || updates.LongPollingTimeoutMS != 42000 {
		t.Fatalf("updates = %#v", updates)
	}
	if err := client.NotifyStop(context.Background(), server.URL, "token"); err != nil {
		t.Fatalf("NotifyStop: %v", err)
	}
	if want := []string{"/ilink/bot/msg/notifystart", "/ilink/bot/getupdates", "/ilink/bot/msg/notifystop"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %#v, want %#v", paths, want)
	}
}

func TestGetUpdatesTimeoutIsAnEmptyPoll(t *testing.T) {
	client := NewClient()
	client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}

	updates, err := client.GetUpdatesWithTimeout(context.Background(), "https://ilink.test", "token", "cursor", time.Millisecond)
	if err != nil {
		t.Fatalf("GetUpdatesWithTimeout: %v", err)
	}
	if updates.Ret != 0 || len(updates.Msgs) != 0 || updates.GetUpdatesBuf != "cursor" {
		t.Fatalf("timeout response = %#v", updates)
	}
}

func TestBotStartReportsHealthyOnlyAfterSuccessfulPollAndPersistsCursor(t *testing.T) {
	var (
		mu               sync.Mutex
		paths            []string
		once             sync.Once
		releaseFirstPoll = make(chan struct{})
		releaseLongPoll  = make(chan struct{})
		// secondPollArrived is closed once the post-message long-poll request has
		// reached the server (and its path has been recorded). The test waits for
		// it before cancelling so the shutdown notifystop is deterministically the
		// last recorded lifecycle path. Without this, cancellation can race the
		// in-flight poll and the server may record notifystop before getupdates.
		secondPollArrived = make(chan struct{})
		onceSecondPoll    sync.Once
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()

		switch r.URL.Path {
		case "/ilink/bot/msg/notifystart", "/ilink/bot/msg/notifystop":
			_, _ = io.WriteString(w, `{"ret":0}`)
		case "/ilink/bot/getupdates":
			first := false
			once.Do(func() { first = true })
			if first {
				<-releaseFirstPoll
				_, _ = io.WriteString(w, `{"ret":0,"get_updates_buf":"saved-cursor","longpolling_timeout_ms":1200,"msgs":[{"message_id":7,"from_user_id":"wx-user","create_time_ms":1,"message_type":1,"context_token":"ctx","item_list":[{"type":1,"text_item":{"text":"hello"}}]}]}`)
				return
			}
			// The path is already recorded above; signal arrival before blocking
			// so the test can sequence cancellation after this poll is in flight.
			onceSecondPoll.Do(func() { close(secondPollArrived) })
			<-releaseLongPoll
			_, _ = io.WriteString(w, `{"ret":0,"msgs":[]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	credPath := filepath.Join(t.TempDir(), "wechat-credentials.json")
	if err := SaveCredentials(&Credentials{Token: "token", BaseURL: server.URL, AccountID: "bot", UserID: "owner"}, credPath); err != nil {
		t.Fatal(err)
	}
	bot := NewBot(BotOptions{CredPath: credPath})
	bot.client.HTTP = server.Client()
	status := make(chan bool, 4)
	bot.SetStatusCallback(func(connected bool) { status <- connected })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	received := make(chan messaging.InboundMessage, 1)
	go func() {
		done <- bot.Start(ctx, func(_ context.Context, message messaging.InboundMessage) (messaging.MessageResponse, error) {
			received <- message
			return messaging.MessageResponse{}, nil
		})
	}()

	select {
	case err := <-bot.Ready():
		if err != nil {
			t.Fatalf("ready error: %v", err)
		}
		if bot.IsConnected() {
			t.Fatal("bot reported connected before getupdates succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bot readiness")
	}
	close(releaseFirstPoll)

	select {
	case inbound := <-received:
		if inbound.Text != "hello" || inbound.UserID != "wx-user" {
			t.Fatalf("inbound = %#v", inbound)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for inbound message")
	}
	if !bot.IsConnected() {
		t.Fatal("bot did not report connected after successful getupdates")
	}
	loaded, err := LoadCredentials(credPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.GetUpdatesBuf != "saved-cursor" {
		t.Fatalf("persisted cursor = %q, want saved-cursor", loaded.GetUpdatesBuf)
	}

	// Wait until the post-message long-poll is in flight (its path recorded)
	// before cancelling. This makes the shutdown notifystop deterministically
	// the last lifecycle path instead of racing the in-flight getupdates.
	select {
	case <-secondPollArrived:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the post-message long-poll to reach the server")
	}

	cancel()
	close(releaseLongPoll)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out stopping bot")
	}
	if bot.IsConnected() {
		t.Fatal("bot remained connected after stop")
	}

	mu.Lock()
	gotPaths := append([]string(nil), paths...)
	mu.Unlock()
	if len(gotPaths) < 3 || gotPaths[0] != "/ilink/bot/msg/notifystart" || gotPaths[len(gotPaths)-1] != "/ilink/bot/msg/notifystop" {
		t.Fatalf("lifecycle paths = %#v", gotPaths)
	}
	var gotConnected, gotDisconnected bool
	for len(status) > 0 {
		if <-status {
			gotConnected = true
		} else {
			gotDisconnected = true
		}
	}
	if !gotConnected || !gotDisconnected {
		t.Fatalf("connection transitions connected=%t disconnected=%t", gotConnected, gotDisconnected)
	}
}

func TestSemverComponentsAndLongPollTimeout(t *testing.T) {
	if got, ok := semverComponents("v1.2.95-rc.1"); !ok || got != [3]int{1, 2, 95} {
		t.Fatalf("semverComponents = %#v, %t", got, ok)
	}
	if _, ok := semverComponents("revision-dirty"); ok {
		t.Fatal("source revision was parsed as semver")
	}
	if got, ok := longPollTimeout(35000); !ok || got != 35*time.Second {
		t.Fatalf("longPollTimeout = %v, %t", got, ok)
	}
	if _, ok := longPollTimeout(0); ok {
		t.Fatal("zero poll timeout was accepted")
	}
}
