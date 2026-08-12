package collector

import (
	"context"
	"crypto/sha256"
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
	before := snapshotOpenCodeFiles(t, path)

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
	if after := snapshotOpenCodeFiles(t, path); !reflect.DeepEqual(after, before) {
		t.Fatalf("database files changed:\nbefore=%+v\nafter=%+v", before, after)
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
	path := filepath.Join(t.TempDir(), "missing.db")
	store := NewSQLiteOpenCodeStore(path)
	if _, err := store.Candidates(context.Background(), []string{"/work/p"}, 0); err == nil {
		t.Fatal("Candidates error=nil for missing read-only database")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("missing database created: %v", err)
	}

	t.Run("schema", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "opencode.db")
		db := openOpenCodeFixture(t, path)
		if _, err := db.Exec(`INSERT INTO session VALUES ('ses_valid', NULL, 'valid', '/work/p', 'Valid', 'build', NULL, 2000000)`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO message VALUES ('msg_valid', 'ses_valid', 2000000, '{"role":"assistant","time":{"completed":2000001}}')`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`DROP TABLE part`); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if records, err := NewSQLiteOpenCodeStore(path).Candidates(context.Background(), []string{"/work/p"}, 0); err == nil || records != nil {
			t.Fatalf("records=%+v err=%v", records, err)
		}
	})

	t.Run("non-scannable session", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "opencode.db")
		db := openOpenCodeFixture(t, path)
		if _, err := db.Exec(`INSERT INTO session VALUES ('ses_valid', NULL, 'valid', '/work/p', 'Valid', 'build', NULL, 2000000)`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO session VALUES ('ses_bad', NULL, 'bad', '/work/p', 'Bad', 'build', NULL, 'not-an-integer')`); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if records, err := NewSQLiteOpenCodeStore(path).Candidates(context.Background(), []string{"/work/p"}, 0); err == nil || records != nil {
			t.Fatalf("partial records=%+v err=%v", records, err)
		}
	})
}

type openCodeFileSnapshot struct {
	Exists  bool
	Mode    os.FileMode
	ModTime int64
	Hash    [sha256.Size]byte
}

func snapshotOpenCodeFiles(t *testing.T, path string) map[string]openCodeFileSnapshot {
	t.Helper()
	result := make(map[string]openCodeFileSnapshot)
	for _, candidate := range []string{path, path + "-wal", path + "-shm", path + "-journal", filepath.Join(filepath.Dir(path), "auth.json")} {
		info, err := os.Stat(candidate)
		if os.IsNotExist(err) {
			result[candidate] = openCodeFileSnapshot{}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		var data []byte
		if filepath.Base(candidate) != "auth.json" {
			data, err = os.ReadFile(candidate)
			if err != nil {
				t.Fatal(err)
			}
		}
		result[candidate] = openCodeFileSnapshot{Exists: true, Mode: info.Mode(), ModTime: info.ModTime().UnixNano(), Hash: sha256.Sum256(data)}
	}
	return result
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
		`INSERT INTO message VALUES ('msg_parent_system', 'ses_parent', 2000002, '{"role":"system","time":{"created":2000002},"providerID":"wrong","modelID":"wrong-model"}')`,
		`INSERT INTO message VALUES ('msg_child', 'ses_child', 1999000, '{"role":"assistant","time":{"created":1999000,"completed":1999500},"providerID":"anthropic","modelID":"claude-sonnet-4-5"}')`,
		`INSERT INTO message VALUES ('msg_tool', 'ses_tool', 1900000, '{"role":"assistant","time":{"created":1900000,"completed":1900100}}')`,
		`INSERT INTO message VALUES ('msg_bad', 'ses_malformed', 1999000, 'not-json')`,
		`INSERT INTO message VALUES ('msg_other', 'ses_other', 2000000, '{"role":"assistant","time":{"created":2000000}}')`,
		`INSERT INTO part VALUES ('part_tool', 'msg_tool', 'ses_tool', 1900001, '{"type":"tool","state":{"status":"running"}}')`,
		`INSERT INTO part VALUES ('part_bad', 'msg_child', 'ses_child', 1999001, 'not-json')`,
		`INSERT INTO todo VALUES ('ses_parent', 'done', 'completed', 'high', 0, 1, 1)`,
		`INSERT INTO todo VALUES ('ses_parent', 'doing', 'in_progress', 'medium', 1, 1, 1)`,
		`INSERT INTO todo VALUES ('ses_parent', 'next', 'pending', 'low', 2, 1, 1)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}
