package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/session"
)

// deliveryProviderFixture is deliberately outside SQLite. It models the
// provider-side asset/message identity that survives a MothX process crash.
// The test runs the writer processes sequentially, so a small JSON file is
// sufficient and keeps the fixture independent of UDP and localhost sockets.
type deliveryProviderFixture struct {
	UploadCalls int    `json:"uploadCalls"`
	SendCalls   int    `json:"sendCalls"`
	AssetID     string `json:"assetID"`
	ClientID    string `json:"clientID"`
}

func TestDeliveryRecoveryAcrossProcessesReusesProviderState(t *testing.T) {
	sessionDir := t.TempDir()
	providerState := t.TempDir() + "/provider-state.json"
	marker := t.TempDir() + "/delivery-ready"
	mgr := session.New(t.TempDir(), sessionDir)
	if err := mgr.InitWithID("delivery-process-session"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := session.CreateSessionRun(sessionDir, session.SessionRun{
		ID: "delivery-process-run", SessionID: mgr.GetHeader().ID, Status: "completed",
		StartedAt: now, UpdatedAt: now, FinishedAt: &now,
	}); err != nil {
		t.Fatal(err)
	}
	plan := session.DeliveryPlan{
		Intent: session.DeliveryIntent{
			ID: "delivery-process-intent", SessionID: mgr.GetHeader().ID, RunID: "delivery-process-run",
			Platform: "wechat", TargetID: "target-user", ReplyMessageID: "reply-1",
			TransportContext: json.RawMessage(`{"replyContext":"ctx-1"}`), Status: "pending",
			CreatedAt: now, UpdatedAt: now,
		},
		Operations: []session.DeliveryOperation{
			{ID: "delivery-process-upload", OperationKey: "artifact-upload", OperationKind: "upload_artifact", Sequence: 1,
				IdempotencyKey: "upload-idempotency", PayloadDigest: "sha256:artifact", Status: "pending", CreatedAt: now, UpdatedAt: now},
			{ID: "delivery-process-send", OperationKey: "artifact-send", OperationKind: "send_artifact", Sequence: 2,
				DependsOn: "delivery-process-upload", IdempotencyKey: "send-idempotency", PayloadDigest: "sha256:artifact-kind", Status: "pending", CreatedAt: now, UpdatedAt: now},
		},
	}
	if err := session.CreateDeliveryPlan(context.Background(), sessionDir, plan); err != nil {
		t.Fatal(err)
	}

	crash := startDeliveryProcessHelper(t, "crash_after_upload", sessionDir, providerState, marker)
	crashExited := false
	defer func() {
		if !crashExited && crash.Process != nil {
			_ = crash.Process.Kill()
			_ = crash.Wait()
		}
	}()
	waitForDeliveryMarker(t, marker)
	if operation, err := session.GetDeliveryOperation(t.Context(), sessionDir, "delivery-process-upload"); err != nil {
		t.Fatal(err)
	} else if operation.Status != "uploading" || operation.ProviderAssetID != "provider-asset-1" {
		t.Fatalf("crashed upload checkpoint = %#v", operation)
	}
	if err := crash.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := crash.Wait(); err == nil {
		t.Fatal("crashed delivery helper exited cleanly")
	}
	crashExited = true
	if err := expireDeliveryLease(sessionDir, "delivery-process-upload"); err != nil {
		t.Fatal(err)
	}

	recover := startDeliveryProcessHelper(t, "recover", sessionDir, providerState, "")
	if err := recover.Wait(); err != nil {
		t.Fatalf("delivery recovery helper: %v", err)
	}
	upload, err := session.GetDeliveryOperation(t.Context(), sessionDir, "delivery-process-upload")
	if err != nil {
		t.Fatal(err)
	}
	send, err := session.GetDeliveryOperation(t.Context(), sessionDir, "delivery-process-send")
	if err != nil {
		t.Fatal(err)
	}
	if upload.Status != "uploaded" || upload.ProviderAssetID != "provider-asset-1" {
		t.Fatalf("recovered upload = %#v", upload)
	}
	if string(upload.ProviderState) != `{"encryptedParam":"encrypted-param-1"}` {
		t.Fatalf("recovered upload provider state = %s", upload.ProviderState)
	}
	wantClientID := fixtureClientID(send.IdempotencyKey)
	if send.Status != "delivered" || send.ProviderMessageID != wantClientID {
		t.Fatalf("recovered send = %#v, want delivered client %q", send, wantClientID)
	}
	intent, err := session.GetDeliveryPlan(t.Context(), sessionDir, plan.Intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if intent.Intent.Status != "delivered" {
		t.Fatalf("delivery intent status = %q, want delivered", intent.Intent.Status)
	}
	fixture := readDeliveryProviderFixture(t, providerState)
	if fixture.UploadCalls != 1 || fixture.SendCalls != 1 || fixture.AssetID != "provider-asset-1" || fixture.ClientID != wantClientID {
		t.Fatalf("provider fixture = %#v, want one upload/send and stable IDs", fixture)
	}
}

func startDeliveryProcessHelper(t *testing.T, action, sessionDir, providerState, marker string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestDeliveryProcessHelper$")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		"MOTHX_DELIVERY_PROCESS_HELPER=1",
		"MOTHX_DELIVERY_PROCESS_ACTION="+action,
		"MOTHX_DELIVERY_PROCESS_SESSION_DIR="+sessionDir,
		"MOTHX_DELIVERY_PROCESS_PROVIDER_STATE="+providerState,
		"MOTHX_DELIVERY_PROCESS_MARKER="+marker,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start delivery process helper: %v", err)
	}
	return cmd
}

func TestDeliveryProcessHelper(t *testing.T) {
	if os.Getenv("MOTHX_DELIVERY_PROCESS_HELPER") != "1" {
		return
	}
	sessionDir := os.Getenv("MOTHX_DELIVERY_PROCESS_SESSION_DIR")
	providerStatePath := os.Getenv("MOTHX_DELIVERY_PROCESS_PROVIDER_STATE")
	marker := os.Getenv("MOTHX_DELIVERY_PROCESS_MARKER")
	action := os.Getenv("MOTHX_DELIVERY_PROCESS_ACTION")
	coordinator := NewDeliveryCoordinator(sessionDir, "delivery-process-"+action)
	if action == "crash_after_upload" {
		operation, err := coordinator.Claim(context.Background(), "delivery-process-upload", time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		state := json.RawMessage(`{"encryptedParam":"encrypted-param-1"}`)
		if err := coordinator.Progress(context.Background(), operation, DeliveryResult{
			Status: "uploading", ProviderAssetID: "provider-asset-1", ProviderState: state,
		}); err != nil {
			t.Fatal(err)
		}
		if err := updateDeliveryProviderFixture(providerStatePath, func(fixture *deliveryProviderFixture) {
			fixture.UploadCalls++
			fixture.AssetID = "provider-asset-1"
		}); err != nil {
			t.Fatal(err)
		}
		if marker != "" {
			if err := os.WriteFile(marker, []byte("ready"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		select {}
	}
	if action != "recover" {
		t.Fatalf("unknown delivery process action %q", action)
	}
	upload, err := coordinator.Claim(context.Background(), "delivery-process-upload", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	fixture := readDeliveryProviderFixture(t, providerStatePath)
	if fixture.AssetID == "" {
		t.Fatal("provider fixture lost uploaded asset")
	}
	if err := coordinator.Complete(context.Background(), upload, DeliveryResult{
		Status: "uploaded", ProviderAssetID: fixture.AssetID,
		ProviderState: json.RawMessage(`{"encryptedParam":"encrypted-param-1"}`),
	}); err != nil {
		t.Fatal(err)
	}
	send, err := coordinator.Claim(context.Background(), "delivery-process-send", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	clientID := fixtureClientID(send.IdempotencyKey)
	if err := updateDeliveryProviderFixture(providerStatePath, func(fixture *deliveryProviderFixture) {
		fixture.SendCalls++
		fixture.ClientID = clientID
	}); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Complete(context.Background(), send, DeliveryResult{Status: "delivered", ProviderMessageID: clientID}); err != nil {
		t.Fatal(err)
	}
}

func expireDeliveryLease(sessionDir, operationID string) error {
	db, err := session.OpenRootDB(sessionDir)
	if err != nil {
		return err
	}
	_, err = db.Bun().Exec(`UPDATE delivery_operations SET lease_expires_at = ? WHERE id = ?`, time.Now().UTC().Add(-time.Second).UnixMilli(), operationID)
	return err
}

// updateDeliveryProviderFixture serializes the fixture through an exclusive
// create/rename cycle. The test invokes writers sequentially after process
// boundaries, so no platform-specific file-lock primitive is required.
func updateDeliveryProviderFixture(path string, update func(*deliveryProviderFixture)) error {
	fixture := deliveryProviderFixture{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &fixture); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	update(&fixture)
	data, err := json.Marshal(fixture)
	if err != nil {
		return err
	}
	temporary := path + ".tmp-" + fmt.Sprint(os.Getpid())
	if err := os.WriteFile(temporary, data, 0600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func readDeliveryProviderFixture(t *testing.T, path string) deliveryProviderFixture {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture deliveryProviderFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func fixtureClientID(idempotencyKey string) string {
	digest := sha256.Sum256([]byte(idempotencyKey))
	return "client-" + hex.EncodeToString(digest[:16])
}

func waitForDeliveryMarker(t *testing.T, marker string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("delivery process marker %q was not created", marker)
}
