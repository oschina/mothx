package dao_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/session"
)

func TestChannelToolsGeneration(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "sessions")

	bound, err := session.CreateBound(root, sessionDir, "wechat", "test-dao")
	if err != nil {
		t.Fatalf("CreateBound: %v", err)
	}

	db, err := session.OpenRootDB(sessionDir)
	if err != nil {
		t.Fatalf("OpenRootDB: %v", err)
	}
	t.Cleanup(func() { _ = session.CloseDatabases() })

	ctx := context.Background()
	sessionID := bound.GetHeader().ID

	// Check initial generation
	gen, err := dao.NewBindingDAO(db.Bun()).ChannelToolGeneration(ctx, sessionID)
	if err != nil {
		t.Fatalf("ChannelToolGeneration: %v", err)
	}
	if gen != 0 {
		t.Fatalf("initial generation = %d, want 0", gen)
	}

	// Set channel tools
	tools := []dao.ChannelToolRecord{
		{ToolName: "read", Enabled: true},
		{ToolName: "bash", Enabled: true},
	}
	daoInst := dao.NewBindingDAO(db.Bun())
	if err := daoInst.SetChannelTools(ctx, sessionID, tools); err != nil {
		t.Fatalf("SetChannelTools: %v", err)
	}

	if _, err := daoInst.ListChannelTools(ctx, sessionID); err != nil {
		t.Fatalf("ListChannelTools: %v", err)
	}

	// Check generation after set
	gen, err = dao.NewBindingDAO(db.Bun()).ChannelToolGeneration(ctx, sessionID)
	if err != nil {
		t.Fatalf("ChannelToolGeneration: %v", err)
	}
	if gen != 1 {
		t.Fatalf("generation after set = %d, want 1", gen)
	}

	// Set tools again - should increment generation
	if err := dao.NewBindingDAO(db.Bun()).SetChannelTools(ctx, sessionID, tools); err != nil {
		t.Fatalf("SetChannelTools second time: %v", err)
	}

	gen, err = dao.NewBindingDAO(db.Bun()).ChannelToolGeneration(ctx, sessionID)
	if err != nil {
		t.Fatalf("ChannelToolGeneration: %v", err)
	}
	if gen != 2 {
		t.Fatalf("generation after second set = %d, want 2", gen)
	}
}
