package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/oschina/mothx/internal/dao"
	database "github.com/oschina/mothx/internal/db"
)

const knowledgeBaseDatabaseDirectoryName = "knowledge-bases"

// KnowledgeBaseDatabasePath derives the private SQLite file for exactly one
// knowledge base. The file lives beside sessions.db but is never attached to
// or queried through the session database.
func KnowledgeBaseDatabasePath(sessionDir, knowledgeBaseID string) (string, error) {
	id, err := normalizeKnowledgeBaseDatabaseID(knowledgeBaseID)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(rootDBPath(sessionDir)), knowledgeBaseDatabaseDirectoryName, id+".db"), nil
}

func normalizeKnowledgeBaseDatabaseID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ErrKnowledgeBaseNotFound
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return "", fmt.Errorf("invalid knowledge base database identity")
		}
	}
	return value, nil
}

func openKnowledgeBaseDatabase(sessionDir, knowledgeBaseID string, create bool) (*dao.Database, string, error) {
	path, err := KnowledgeBaseDatabasePath(sessionDir, knowledgeBaseID)
	if err != nil {
		return nil, "", err
	}
	if !create {
		info, statErr := os.Lstat(path)
		if errors.Is(statErr, os.ErrNotExist) {
			return nil, path, ErrKnowledgeBaseNotFound
		}
		if statErr != nil {
			return nil, path, fmt.Errorf("stat knowledge base database: %w", statErr)
		}
		if !info.Mode().IsRegular() {
			return nil, path, fmt.Errorf("knowledge base database is not a regular file")
		}
	}
	// The per-knowledge-base graph/FTS database is a private, rebuildable
	// derived store whose snapshot lifecycle prunes child rows (chunks, nodes,
	// evidence, FTS) through ON DELETE CASCADE. It therefore opts into SQLite
	// foreign key enforcement, unlike the canonical session database which
	// keeps integrity in the repository layer. See internal/db.Options.
	connection, err := database.OpenWithOptions(path, EnsureKnowledgeBaseSchema, database.Options{ForeignKeys: true})
	if err != nil {
		return nil, path, err
	}
	return dao.WrapDatabase(connection), path, nil
}

func queryKnowledgeBaseDatabase(sessionDir, knowledgeBaseID string, fn func(*dao.Database) error) error {
	db, _, err := openKnowledgeBaseDatabase(sessionDir, knowledgeBaseID, false)
	if err != nil {
		return err
	}
	return fn(db)
}

// readKnowledgeBaseDatabase keeps a multi-query graph projection on one
// SQLite read transaction. Snapshot publication prunes old rows atomically,
// so callers that need a coherent projection must not release the connection
// between reading active_snapshot_id and its graph rows.
func readKnowledgeBaseDatabase(ctx context.Context, sessionDir, knowledgeBaseID string, fn func(*dao.Tx) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	db, _, err := openKnowledgeBaseDatabase(sessionDir, knowledgeBaseID, false)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, &dao.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func writeKnowledgeBaseDatabase(ctx context.Context, sessionDir, knowledgeBaseID string, create bool, fn func(*dao.Tx) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	db, _, err := openKnowledgeBaseDatabase(sessionDir, knowledgeBaseID, create)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func listKnowledgeBaseDatabaseIDs(sessionDir string) ([]string, error) {
	dir := filepath.Join(filepath.Dir(rootDBPath(sessionDir)), knowledgeBaseDatabaseDirectoryName)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list knowledge base databases: %w", err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".db")
		if _, err := normalizeKnowledgeBaseDatabaseID(id); err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func deleteKnowledgeBaseDatabase(sessionDir, knowledgeBaseID string) error {
	path, err := KnowledgeBaseDatabasePath(sessionDir, knowledgeBaseID)
	if err != nil {
		return err
	}
	if err := database.Close(path); err != nil {
		return err
	}
	for _, target := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove knowledge base database: %w", err)
		}
	}
	return nil
}
