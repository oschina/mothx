package session

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/provider"
)

const sqliteProcessHelperEnv = "MOTHX_SQLITE_ROBUSTNESS_HELPER"

func TestSQLiteRobustnessProcessHelper(t *testing.T) {
	if os.Getenv(sqliteProcessHelperEnv) != "1" {
		return
	}

	switch os.Getenv("MOTHX_SQLITE_HELPER_MODE") {
	case "distinct":
		runDistinctSessionProcess(t)
	case "stale-writer":
		runStaleWriterProcess(t)
	case "crash-transaction":
		runCrashTransactionProcess(t)
	default:
		t.Fatalf("unknown helper mode %q", os.Getenv("MOTHX_SQLITE_HELPER_MODE"))
	}
}

func runCrashTransactionProcess(t *testing.T) {
	db, err := OpenStandaloneDB(os.Getenv("MOTHX_SQLITE_DB_PATH"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO sessions
		(id, cwd, timestamp, parent_session, version) VALUES (?, ?, ?, ?, ?)`,
		"uncommitted-crash", "/tmp/crash", time.Now().Format(time.RFC3339Nano), nil, CurrentVersion); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("MOTHX_SQLITE_READY_FILE"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	for {
		time.Sleep(time.Second)
	}
}

func runDistinctSessionProcess(t *testing.T) {
	sessionDir := os.Getenv("MOTHX_SQLITE_SESSION_DIR")
	sessionID := os.Getenv("MOTHX_SQLITE_SESSION_ID")
	messageCount, err := strconv.Atoi(os.Getenv("MOTHX_SQLITE_MESSAGE_COUNT"))
	if err != nil {
		t.Fatal(err)
	}
	m := New("/tmp/sqlite-stress", sessionDir)
	if err := m.InitWithID(sessionID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < messageCount; i++ {
		if _, err := m.AppendMessage(provider.NewUserMessage(fmt.Sprintf("%s-message-%d", sessionID, i))); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 16; i++ {
		if err := m.RecordUsage("test", "multiprocess", "model", i, i+1, i*2+1, i); err != nil {
			t.Fatal(err)
		}
	}
}

func runStaleWriterProcess(t *testing.T) {
	m, err := Open(os.Getenv("MOTHX_SQLITE_SESSION_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("MOTHX_SQLITE_READY_FILE"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	waitForTestFile(t, os.Getenv("MOTHX_SQLITE_START_FILE"), 10*time.Second)

	result := "success"
	if _, err := m.AppendMessage(provider.NewUserMessage(os.Getenv("MOTHX_SQLITE_WRITER_ID"))); err != nil {
		if !errors.Is(err, ErrSessionModified) {
			t.Fatal(err)
		}
		result = "conflict"
	}
	if err := os.WriteFile(os.Getenv("MOTHX_SQLITE_RESULT_FILE"), []byte(result), 0600); err != nil {
		t.Fatal(err)
	}
}

type sqliteTestProcess struct {
	cmd    *exec.Cmd
	output *bytes.Buffer
}

func startSQLiteTestProcess(t *testing.T, env ...string) sqliteTestProcess {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSQLiteRobustnessProcessHelper$")
	cmd.Env = append(os.Environ(), append([]string{sqliteProcessHelperEnv + "=1"}, env...)...)
	output := &bytes.Buffer{}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return sqliteTestProcess{cmd: cmd, output: output}
}

func waitSQLiteTestProcess(t *testing.T, process sqliteTestProcess) {
	t.Helper()
	if err := process.cmd.Wait(); err != nil {
		t.Fatalf("SQLite helper process failed: %v\n%s", err, process.output.String())
	}
}

func waitForTestFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertDatabaseIntegrity(t *testing.T, dbPath string) {
	t.Helper()
	db, err := OpenStandaloneDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var result string
	if err := db.Bun().QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		t.Fatal(err)
	}
	if result != "ok" {
		t.Fatalf("integrity_check = %q, want ok", result)
	}
}

func TestSQLiteManyProcessesWriteDistinctSessions(t *testing.T) {
	sessionDir := t.TempDir()
	const processCount = 8
	const messagesPerProcess = 64
	processes := make([]sqliteTestProcess, 0, processCount)
	for i := 0; i < processCount; i++ {
		processes = append(processes, startSQLiteTestProcess(t,
			"MOTHX_SQLITE_HELPER_MODE=distinct",
			"MOTHX_SQLITE_SESSION_DIR="+sessionDir,
			fmt.Sprintf("MOTHX_SQLITE_SESSION_ID=stress-%d", i),
			fmt.Sprintf("MOTHX_SQLITE_MESSAGE_COUNT=%d", messagesPerProcess),
		))
	}
	for _, process := range processes {
		waitSQLiteTestProcess(t, process)
	}

	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	var sessionCount, entryCount, usageCount int
	if err := db.Bun().QueryRow("SELECT COUNT(*) FROM sessions WHERE cwd = ?", "/tmp/sqlite-stress").Scan(&sessionCount); err != nil {
		t.Fatal(err)
	}
	if err := db.Bun().QueryRow("SELECT COUNT(*) FROM entries").Scan(&entryCount); err != nil {
		t.Fatal(err)
	}
	if err := db.Bun().QueryRow("SELECT COUNT(*) FROM request_stats").Scan(&usageCount); err != nil {
		t.Fatal(err)
	}
	if sessionCount != processCount {
		t.Fatalf("sessions = %d, want %d", sessionCount, processCount)
	}
	if entryCount != processCount*(messagesPerProcess+1) {
		t.Fatalf("entries = %d, want %d", entryCount, processCount*(messagesPerProcess+1))
	}
	if usageCount != processCount*16 {
		t.Fatalf("request_stats = %d, want %d", usageCount, processCount*16)
	}
	assertDatabaseIntegrity(t, filepath.Join(sessionDir, "sessions.db"))
}

func TestSQLiteTwoProcessesCompetingForSameSession(t *testing.T) {
	sessionDir := t.TempDir()
	m := New("/tmp/same-session", sessionDir)
	if err := m.InitWithID("shared-session"); err != nil {
		t.Fatal(err)
	}

	startFile := filepath.Join(t.TempDir(), "start")
	processes := make([]sqliteTestProcess, 0, 2)
	readyFiles := make([]string, 0, 2)
	resultFiles := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		dir := t.TempDir()
		readyFile := filepath.Join(dir, "ready")
		resultFile := filepath.Join(dir, "result")
		readyFiles = append(readyFiles, readyFile)
		resultFiles = append(resultFiles, resultFile)
		processes = append(processes, startSQLiteTestProcess(t,
			"MOTHX_SQLITE_HELPER_MODE=stale-writer",
			"MOTHX_SQLITE_SESSION_FILE="+m.GetFile(),
			"MOTHX_SQLITE_READY_FILE="+readyFile,
			"MOTHX_SQLITE_START_FILE="+startFile,
			"MOTHX_SQLITE_RESULT_FILE="+resultFile,
			fmt.Sprintf("MOTHX_SQLITE_WRITER_ID=writer-%d", i),
		))
	}
	for _, readyFile := range readyFiles {
		waitForTestFile(t, readyFile, 10*time.Second)
	}
	if err := os.WriteFile(startFile, []byte("start"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, process := range processes {
		waitSQLiteTestProcess(t, process)
	}

	results := map[string]int{}
	for _, resultFile := range resultFiles {
		data, err := os.ReadFile(resultFile)
		if err != nil {
			t.Fatal(err)
		}
		results[string(data)]++
	}
	if results["success"] != 1 || results["conflict"] != 1 {
		t.Fatalf("writer results = %#v, want one success and one conflict", results)
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	var messages int
	if err := db.Bun().QueryRow("SELECT COUNT(*) FROM entries WHERE session_id = ? AND type = ?", "shared-session", string(EntryMessage)).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if messages != 1 {
		t.Fatalf("persisted messages = %d, want 1", messages)
	}
	assertDatabaseIntegrity(t, filepath.Join(sessionDir, "sessions.db"))
}

func TestSQLiteConcurrentGoroutineAppendsPreserveParentChain(t *testing.T) {
	sessionDir := t.TempDir()
	m := New("/tmp/goroutines", sessionDir)
	if err := m.InitWithID("goroutine-session"); err != nil {
		t.Fatal(err)
	}

	const goroutineCount = 100
	errCh := make(chan error, goroutineCount)
	var wg sync.WaitGroup
	for i := 0; i < goroutineCount; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := m.AppendMessage(provider.NewUserMessage(fmt.Sprintf("message-%d", i)))
			errCh <- err
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.Bun().Query(`SELECT id, parent_id FROM entries
		WHERE session_id = ? AND type != ? ORDER BY seq`, "goroutine-session", string(EntrySession))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	previousID := ""
	count := 0
	for rows.Next() {
		var id string
		var parentID *string
		if err := rows.Scan(&id, &parentID); err != nil {
			t.Fatal(err)
		}
		if previousID == "" {
			if parentID != nil {
				t.Fatalf("first parent = %q, want nil", *parentID)
			}
		} else if parentID == nil || *parentID != previousID {
			t.Fatalf("entry %s parent = %v, want %s", id, parentID, previousID)
		}
		previousID = id
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != goroutineCount {
		t.Fatalf("entries = %d, want %d", count, goroutineCount)
	}
}

func TestSQLiteHeaderWriteRollsBackWhenEntryInsertFails(t *testing.T) {
	sessionDir := t.TempDir()
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Bun().Exec(`CREATE TRIGGER reject_header_entry BEFORE INSERT ON entries
		BEGIN SELECT RAISE(ABORT, 'reject entry'); END`); err != nil {
		t.Fatal(err)
	}

	m := New("/tmp/header-rollback", sessionDir)
	if err := m.InitWithID("rollback-header"); err == nil {
		t.Fatal("InitWithID succeeded despite rejecting entry insert")
	}
	var sessions, entries int
	if err := db.Bun().QueryRow("SELECT COUNT(*) FROM sessions WHERE id = ?", "rollback-header").Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err := db.Bun().QueryRow("SELECT COUNT(*) FROM entries WHERE session_id = ?", "rollback-header").Scan(&entries); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 || entries != 0 {
		t.Fatalf("sessions=%d entries=%d, want both 0 after rollback", sessions, entries)
	}
}

func TestSQLiteAppendRollbackPreservesLeafAndCanRecover(t *testing.T) {
	sessionDir := t.TempDir()
	m := New("/tmp/append-rollback", sessionDir)
	if err := m.InitWithID("append-rollback"); err != nil {
		t.Fatal(err)
	}
	firstID, err := m.AppendMessage(provider.NewUserMessage("first"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Bun().Exec(`CREATE TRIGGER reject_message BEFORE INSERT ON entries
		WHEN NEW.type = 'message' BEGIN SELECT RAISE(ABORT, 'reject message'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AppendMessage(provider.NewUserMessage("rejected")); err == nil {
		t.Fatal("AppendMessage succeeded despite rejecting trigger")
	}
	if leaf := m.GetLeafID(); leaf == nil || *leaf != firstID {
		t.Fatalf("leaf = %v, want %s", leaf, firstID)
	}
	if len(m.entries) != 1 {
		t.Fatalf("in-memory entries = %d, want 1", len(m.entries))
	}
	if _, err := db.Bun().Exec("DROP TRIGGER reject_message"); err != nil {
		t.Fatal(err)
	}
	secondID, err := m.AppendMessage(provider.NewUserMessage("accepted"))
	if err != nil {
		t.Fatal(err)
	}
	var parentID string
	if err := db.Bun().QueryRow("SELECT parent_id FROM entries WHERE id = ?", secondID).Scan(&parentID); err != nil {
		t.Fatal(err)
	}
	if parentID != firstID {
		t.Fatalf("recovered entry parent = %s, want %s", parentID, firstID)
	}
}

func TestSQLiteDeleteSessionRollsBackOnFailure(t *testing.T) {
	sessionDir := t.TempDir()
	m := New("/tmp/delete-rollback", sessionDir)
	if err := m.InitWithID("delete-rollback"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AppendMessage(provider.NewUserMessage("keep me")); err != nil {
		t.Fatal(err)
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Bun().Exec(`INSERT INTO session_capabilities
		(session_id, mode, updated_at) VALUES (?, ?, ?)`, "delete-rollback", "agent", time.Now().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Bun().Exec(`CREATE TRIGGER reject_entry_delete BEFORE DELETE ON entries
		BEGIN SELECT RAISE(ABORT, 'reject delete'); END`); err != nil {
		t.Fatal(err)
	}
	if err := DeleteSession(m.GetFile(), sessionDir); err == nil {
		t.Fatal("DeleteSession succeeded despite rejecting trigger")
	}
	var sessionCount int
	if err := db.Bun().QueryRow("SELECT COUNT(*) FROM sessions WHERE id = ?", "delete-rollback").Scan(&sessionCount); err != nil {
		t.Fatal(err)
	}
	if sessionCount != 1 {
		t.Fatalf("sessions count = %d, want 1", sessionCount)
	}
	for table, want := range map[string]int{"entries": 2, "session_capabilities": 1} {
		var count int
		if err := db.Bun().QueryRow("SELECT COUNT(*) FROM "+table+" WHERE session_id = ?", "delete-rollback").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("%s count = %d, want %d", table, count, want)
		}
	}
}

func TestSQLiteBusyTimeoutWaitsForWriter(t *testing.T) {
	sessionDir := t.TempDir()
	dbPath := filepath.Join(sessionDir, "sessions.db")
	first, err := OpenStandaloneDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := OpenStandaloneDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	tx, err := first.Begin()
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	started := time.Now()
	go func() {
		_, err := second.Bun().Exec(`INSERT INTO request_stats
			(timestamp, session_id, provider, protocol, model, input_tokens, output_tokens, total_tokens, duration_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			time.Now().Format(time.RFC3339Nano), "", "test", "busy", "model", 1, 1, 2, 1)
		result <- err
	}()
	time.Sleep(150 * time.Millisecond)
	select {
	case err := <-result:
		_ = tx.Rollback()
		t.Fatalf("contending write returned before lock release: %v", err)
	default:
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("contending write did not finish after lock release")
	}
	if elapsed := time.Since(started); elapsed < 150*time.Millisecond {
		t.Fatalf("contending write waited only %s", elapsed)
	}
}

func TestSQLiteKilledWriterRollsBackAndDatabaseRemainsValid(t *testing.T) {
	sessionDir := t.TempDir()
	dbPath := filepath.Join(sessionDir, "sessions.db")
	if _, err := OpenRootDB(sessionDir); err != nil {
		t.Fatal(err)
	}
	readyFile := filepath.Join(t.TempDir(), "ready")
	process := startSQLiteTestProcess(t,
		"MOTHX_SQLITE_HELPER_MODE=crash-transaction",
		"MOTHX_SQLITE_DB_PATH="+dbPath,
		"MOTHX_SQLITE_READY_FILE="+readyFile,
	)
	waitForTestFile(t, readyFile, 10*time.Second)
	if err := process.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := process.cmd.Wait(); err == nil {
		t.Fatal("killed helper process exited successfully")
	}

	db, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.Bun().QueryRow("SELECT COUNT(*) FROM sessions WHERE id = ?", "uncommitted-crash").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("uncommitted session count = %d, want 0", count)
	}
	m := New("/tmp/after-crash", sessionDir)
	if err := m.InitWithID("after-crash"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AppendMessage(provider.NewUserMessage("database recovered")); err != nil {
		t.Fatal(err)
	}
	assertDatabaseIntegrity(t, dbPath)
}

func TestSQLitePathWithReservedCharacters(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "sessions with spaces # and ?")
	m := New("/tmp/special-path", sessionDir)
	if err := m.InitWithID("special-path"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AppendMessage(provider.NewUserMessage("stored")); err != nil {
		t.Fatal(err)
	}
	if err := CloseDatabases(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(m.GetFile())
	if err != nil {
		t.Fatal(err)
	}
	messages := reopened.GetMessages()
	if len(messages) != 1 || messages[0].Content != "stored" {
		t.Fatalf("messages = %#v", messages)
	}
	assertDatabaseIntegrity(t, filepath.Join(sessionDir, "sessions.db"))
}

func TestSQLiteMalformedDatabaseIsRejected(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	if err := os.WriteFile(dbPath, []byte("this is not a sqlite database"), 0600); err != nil {
		t.Fatal(err)
	}
	if db, err := OpenStandaloneDB(dbPath); err == nil {
		db.Close()
		t.Fatal("OpenStandaloneDB succeeded for malformed database")
	}
}

func TestSQLiteCanonicalPathsShareCachedConnection(t *testing.T) {
	sessionDir := t.TempDir()
	first, err := OpenRootDB(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenRootDB(filepath.Join(sessionDir, "."))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("equivalent database paths created separate cached connections")
	}
}
