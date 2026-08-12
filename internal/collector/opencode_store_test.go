package collector

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSQLiteOpenCodeStoreCandidates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.db")
	db := openOpenCodeFixture(t, path)
	insertOpenCodeFixture(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// A sibling credential file must be irrelevant to the store.
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "auth.json"), []byte("not json"), 0); err != nil {
		t.Fatal(err)
	}

	store := NewSQLiteOpenCodeStore(path)
	records, err := store.Candidates(context.Background(), []string{"/work/p"}, 1_995_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 {
		t.Fatalf("records=%+v", records)
	}

	byID := make(map[string]OpenCodeRecord, len(records))
	for _, record := range records {
		byID[record.ID] = record
	}
	parent := byID["ses_parent"]
	if parent.ProviderID != "openai" || parent.ModelID != "gpt-5.6-sol" || !parent.Busy {
		t.Fatalf("parent=%+v", parent)
	}
	if parent.AgentMode != "build" || parent.Title != "Parent" || parent.Directory != "/work/p" {
		t.Fatalf("parent metadata=%+v", parent)
	}
	wantTodos := []OpenCodeTodo{
		{Content: "done", Status: "completed"},
		{Content: "doing", Status: "in_progress"},
		{Content: "next", Status: "pending"},
	}
	if !reflect.DeepEqual(parent.Todos, wantTodos) {
		t.Fatalf("todos=%+v", parent.Todos)
	}

	child := byID["ses_child"]
	if child.ParentID != "ses_parent" || child.ProviderID != "anthropic" || child.ModelID != "claude-sonnet-4-5" || child.Busy {
		t.Fatalf("child=%+v", child)
	}
	if tool := byID["ses_tool"]; !tool.Busy {
		t.Fatalf("running tool not busy: %+v", tool)
	}
	if malformed := byID["ses_malformed"]; malformed.ProviderID != "fallback" || malformed.ModelID != "fallback-model" || malformed.Busy {
		t.Fatalf("malformed fallback=%+v", malformed)
	}
	if _, found := byID["ses_other"]; found {
		t.Fatal("session from unrelated directory returned")
	}

	if _, err := os.Stat(path + "-wal"); !os.IsNotExist(err) {
		t.Fatalf("read-only store created WAL: %v", err)
	}
}

func TestSQLiteOpenCodeStoreByIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.db")
	db := openOpenCodeFixture(t, path)
	insertOpenCodeFixture(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store := NewSQLiteOpenCodeStore(path)
	records, err := store.ByIDs(context.Background(), []string{"ses_child", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ID != "ses_child" {
		t.Fatalf("records=%+v", records)
	}
	empty, err := store.ByIDs(context.Background(), nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty=%+v err=%v", empty, err)
	}
}

func TestSQLiteOpenCodeStoreReportsQueryErrors(t *testing.T) {
	store := NewSQLiteOpenCodeStore(filepath.Join(t.TempDir(), "missing.db"))
	if _, err := store.Candidates(context.Background(), []string{"/work/p"}, 0); err == nil {
		t.Fatal("Candidates error=nil for missing read-only database")
	}
}

func openOpenCodeFixture(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE session (
			id TEXT PRIMARY KEY, parent_id TEXT, slug TEXT NOT NULL,
			directory TEXT NOT NULL, title TEXT NOT NULL, agent TEXT,
			model TEXT, time_updated INTEGER NOT NULL
		)`,
		`CREATE TABLE message (
			id TEXT PRIMARY KEY, session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL, data TEXT NOT NULL
		)`,
		`CREATE TABLE part (
			id TEXT PRIMARY KEY, message_id TEXT NOT NULL,
			session_id TEXT NOT NULL, time_created INTEGER NOT NULL,
			data TEXT NOT NULL
		)`,
		`CREATE TABLE todo (
			session_id TEXT NOT NULL, content TEXT NOT NULL,
			status TEXT NOT NULL, priority TEXT NOT NULL,
			position INTEGER NOT NULL, time_created INTEGER NOT NULL,
			time_updated INTEGER NOT NULL,
			PRIMARY KEY (session_id, position)
		)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func insertOpenCodeFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, statement := range []string{
		`INSERT INTO session VALUES ('ses_parent', NULL, 'parent', '/work/p', 'Parent', 'build', '{"id":"fallback-model","providerID":"fallback"}', 1900000)`,
		`INSERT INTO session VALUES ('ses_child', 'ses_parent', 'child', '/work/p', 'Child', 'general', NULL, 1999000)`,
		`INSERT INTO session VALUES ('ses_tool', NULL, 'tool', '/work/p', 'Tool', 'build', NULL, 1900000)`,
		`INSERT INTO session VALUES ('ses_malformed', NULL, 'bad', '/work/p', 'Malformed', 'build', '{"id":"fallback-model","providerID":"fallback"}', 1999000)`,
		`INSERT INTO session VALUES ('ses_other', NULL, 'other', '/work/other', 'Other', 'build', NULL, 2000000)`,
		`INSERT INTO message VALUES ('msg_parent', 'ses_parent', 2000000, '{"role":"assistant","time":{"created":2000000},"providerID":"openai","modelID":"gpt-5.6-sol"}')`,
		`INSERT INTO message VALUES ('msg_parent_user', 'ses_parent', 2000001, '{"role":"user","time":{"created":2000001}}')`,
		`INSERT INTO message VALUES ('msg_child', 'ses_child', 1999000, '{"role":"assistant","time":{"created":1999000,"completed":1999500},"providerID":"anthropic","modelID":"claude-sonnet-4-5"}')`,
		`INSERT INTO message VALUES ('msg_tool', 'ses_tool', 1900000, '{"role":"assistant","time":{"created":1900000,"completed":1900100}}')`,
		`INSERT INTO message VALUES ('msg_bad', 'ses_malformed', 1999000, 'not-json')`,
		`INSERT INTO message VALUES ('msg_other', 'ses_other', 2000000, '{"role":"assistant","time":{"created":2000000}}')`,
		`INSERT INTO part VALUES ('part_tool', 'msg_tool', 'ses_tool', 1900001, '{"type":"tool","state":{"status":"running"}}')`,
		`INSERT INTO todo VALUES ('ses_parent', 'done', 'completed', 'high', 0, 1, 1)`,
		`INSERT INTO todo VALUES ('ses_parent', 'doing', 'in_progress', 'medium', 1, 1, 1)`,
		`INSERT INTO todo VALUES ('ses_parent', 'next', 'pending', 'low', 2, 1, 1)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}
