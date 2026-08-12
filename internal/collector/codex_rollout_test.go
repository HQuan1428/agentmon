package collector

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexScannerReducesLifecycleAndModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeFile(t, path, strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"abc","cwd":"/work/p","parent_thread_id":"parent"}}`,
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-2"}}`,
		`{"type":"turn_context","payload":{"turn_id":"turn-1","model":"gpt-5.6-sol"}}`,
	}, "\n")+"\n")

	scanner := NewCodexScanner()
	if got := scanner.Scan(path); got.NativeID != "abc" || got.ParentID != "parent" || got.Cwd != "/work/p" || got.Model != "gpt-5.6-sol" || !got.Busy || got.Done {
		t.Fatalf("active snapshot=%+v", got)
	}

	appendFile(t, path, `{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}`+"\n")
	if got := scanner.Scan(path); !got.Busy || got.Done {
		t.Fatalf("partially completed snapshot=%+v", got)
	}

	appendFile(t, path, `{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-2"}}`+"\n")
	if got := scanner.Scan(path); got.Busy || !got.Done {
		t.Fatalf("completed snapshot=%+v", got)
	}
}

func TestCodexScannerAbortedTurnIsDone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeFile(t, path, strings.Join([]string{
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"type":"event_msg","payload":{"type":"task_aborted","turn_id":"turn-1"}}`,
	}, "\n")+"\n")

	if got := NewCodexScanner().Scan(path); got.Busy || !got.Done {
		t.Fatalf("aborted snapshot=%+v", got)
	}
}

func TestCodexScannerFailsClosedForMalformedAndPartialRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeFile(t, path, strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"abc","cwd":"/work/p","parent_thread_id":"parent"}}`,
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`not json`,
		`{"type":"turn_context","payload":{"turn_id":"turn-1","model":"gpt-5.6-sol"`,
	}, "\n"))

	scanner := NewCodexScanner()
	if got := scanner.Scan(path); got.NativeID != "abc" || got.Model != "" || !got.Busy || got.Done {
		t.Fatalf("snapshot before final newline=%+v", got)
	}

	appendFile(t, path, `}}`+"\n")
	if got := scanner.Scan(path); got.Model != "gpt-5.6-sol" || !got.Busy || got.Done {
		t.Fatalf("snapshot after final newline=%+v", got)
	}
}

func TestCodexScannerIgnoresTerminalEventsWithoutOpenTurn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeFile(t, path, `{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}`+"\n")

	if got := NewCodexScanner().Scan(path); got.Busy || got.Done {
		t.Fatalf("unmatched terminal snapshot=%+v", got)
	}
}

func TestCodexScannerResetsStateWhenRolloutTruncates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeFile(t, path, strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"old","cwd":"/work/old"}}`,
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
	}, "\n")+"\n")

	scanner := NewCodexScanner()
	if got := scanner.Scan(path); got.NativeID != "old" || !got.Busy {
		t.Fatalf("snapshot before truncate=%+v", got)
	}
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	appendFile(t, path, `{"type":"session_meta","payload":{"id":"new","cwd":"/work/new"}}`+"\n")

	if got := scanner.Scan(path); got.NativeID != "new" || got.Cwd != "/work/new" || got.Busy || got.Done {
		t.Fatalf("snapshot after truncate=%+v", got)
	}
}

func TestCodexScannerResetsStateWhenRolloutIsReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeFile(t, path, strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"old","cwd":"/work/old"}}`,
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
	}, "\n")+"\n")

	scanner := NewCodexScanner()
	if got := scanner.Scan(path); got.NativeID != "old" || !got.Busy {
		t.Fatalf("snapshot before replacement=%+v", got)
	}

	replacement := filepath.Join(filepath.Dir(path), "replacement.jsonl")
	writeFile(t, replacement, strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"new","cwd":"/work/new"}}`,
		`{"type":"ignored","payload":{}}`,
		`{"type":"ignored","payload":{}}`,
		`{"type":"ignored","payload":{}}`,
		`{"type":"ignored","payload":{}}`,
		`{"type":"ignored","payload":{}}`,
	}, "\n")+"\n")
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}

	if got := scanner.Scan(path); got.NativeID != "new" || got.Cwd != "/work/new" || got.Busy || got.Done {
		t.Fatalf("snapshot after replacement=%+v", got)
	}
}

func TestCodexScannerResetsStateWhenSameInodeRolloutIsRewritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeFile(t, path, strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"old","cwd":"/work/old"}}`,
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
	}, "\n")+"\n")

	scanner := NewCodexScanner()
	if got := scanner.Scan(path); got.NativeID != "old" || !got.Busy {
		t.Fatalf("snapshot before rewrite=%+v", got)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"new","cwd":"/work/new"}}`,
		`{"type":"ignored","payload":{}}`,
		`{"type":"ignored","payload":{}}`,
		`{"type":"ignored","payload":{}}`,
		`{"type":"ignored","payload":{}}`,
		`{"type":"ignored","payload":{}}`,
	}, "\n")+"\n")
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("test setup replaced the rollout inode")
	}

	if got := scanner.Scan(path); got.NativeID != "new" || got.Cwd != "/work/new" || got.Busy || got.Done {
		t.Fatalf("snapshot after rewrite=%+v", got)
	}
}

func TestCodexScannerResetsSameInodeRewriteWithPreservedPrefix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	prefix := strings.Repeat(`{"type":"ignored","payload":{}}`+"\n", 200)
	old := prefix + `{"type":"session_meta","payload":{"id":"old","cwd":"/work/old"}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}` + "\n"
	new := prefix + `{"type":"session_meta","payload":{"id":"new","cwd":"/work/new"}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}` + "\n"
	if len(old) != len(new) {
		t.Fatalf("fixture sizes old=%d new=%d", len(old), len(new))
	}
	writeFile(t, path, old)

	scanner := NewCodexScanner()
	if got := scanner.Scan(path); got.NativeID != "old" || !got.Busy {
		t.Fatalf("snapshot before rewrite=%+v", got)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, new)
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("test setup replaced the rollout inode")
	}

	if got := scanner.Scan(path); got.NativeID != "new" || got.Cwd != "/work/new" || !got.Busy || got.Done {
		t.Fatalf("snapshot after preserved-prefix rewrite=%+v", got)
	}
}

func TestCodexScannerResetsRewriteBeforeTrailingPartialRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	prefix := strings.Repeat(`{"type":"ignored","payload":{}}`+"\n", 200)
	writeFile(t, path, prefix+`{"type":"session_meta","payload":{"id":"old","cwd":"/work/old"}}`+"\n")

	scanner := NewCodexScanner()
	if got := scanner.Scan(path); got.NativeID != "old" {
		t.Fatalf("snapshot before rewrite=%+v", got)
	}
	writeFile(t, path, prefix+`{"type":"session_meta","payload":{"id":"new","cwd":"/work/new"}}`+"\n"+
		`{"type":"turn_context","payload":{"turn_id":"turn-1","model":"gpt-5.7-codex"`)

	if got := scanner.Scan(path); got.NativeID != "new" || got.Model != "" {
		t.Fatalf("snapshot before final newline=%+v", got)
	}
	appendFile(t, path, `}}`+"\n")
	if got := scanner.Scan(path); got.NativeID != "new" || got.Model != "gpt-5.7-codex" {
		t.Fatalf("snapshot after final newline=%+v", got)
	}
}

func TestCodexScannerCapturesSourceAndTimestamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeFile(t, path, `{"type":"session_meta","payload":{"id":"abc","cwd":"/work/p","source":{"subagent":{"depth":1}},"timestamp":"2026-08-12T05:23:13.436Z"}}`+"\n")

	got := NewCodexScanner().Scan(path)
	if got.Source != "subagent" || got.UpdatedAt != 1786512193436 {
		t.Fatalf("snapshot=%+v", got)
	}
}

func TestCodexScannerParsesRFC3339TimestampVariants(t *testing.T) {
	for _, tc := range []struct {
		name      string
		timestamp string
		want      int64
	}{
		{name: "plain UTC", timestamp: "2026-08-12T05:23:13Z", want: 1786512193000},
		{name: "offset", timestamp: "2026-08-12T12:23:13+07:00", want: 1786512193000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rollout.jsonl")
			writeFile(t, path, `{"type":"session_meta","payload":{"id":"abc","cwd":"/work/p","source":"cli","timestamp":"`+tc.timestamp+`"}}`+"\n")

			if got := NewCodexScanner().Scan(path); got.UpdatedAt != tc.want {
				t.Fatalf("updatedAt=%d snapshot=%+v", got.UpdatedAt, got)
			}
		})
	}
}

func TestCodexScannerRejectsMalformedSessionMetaSourceOrTimestamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeFile(t, path, `{"type":"session_meta","payload":{"id":"original","cwd":"/work/original","source":"cli","timestamp":"2026-08-12T05:23:13.436Z"}}`+"\n")

	scanner := NewCodexScanner()
	if got := scanner.Scan(path); got.NativeID != "original" || got.Source != "cli" || got.UpdatedAt != 1786512193436 {
		t.Fatalf("original snapshot=%+v", got)
	}
	appendFile(t, path, strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"bad-timestamp","cwd":"/work/bad-timestamp","source":"cli","timestamp":"not-a-time"}}`,
		`{"type":"session_meta","payload":{"id":"bad-source","cwd":"/work/bad-source","source":false,"timestamp":"2026-08-12T06:23:13.436Z"}}`,
	}, "\n")+"\n")

	if got := scanner.Scan(path); got.NativeID != "original" || got.Cwd != "/work/original" || got.Source != "cli" || got.UpdatedAt != 1786512193436 {
		t.Fatalf("snapshot after malformed metadata=%+v", got)
	}
}

func TestCodexScannerRejectsNullSessionMetaTimestamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeFile(t, path, `{"type":"session_meta","payload":{"id":"original","cwd":"/work/original","source":"cli","timestamp":"2026-08-12T05:23:13.436Z"}}`+"\n")

	scanner := NewCodexScanner()
	if got := scanner.Scan(path); got.NativeID != "original" || got.Cwd != "/work/original" || got.Source != "cli" || got.UpdatedAt != 1786512193436 {
		t.Fatalf("original snapshot=%+v", got)
	}
	appendFile(t, path, `{"type":"session_meta","payload":{"id":"changed","cwd":"/work/changed","source":"subagent","timestamp":null}}`+"\n")

	if got := scanner.Scan(path); got.NativeID != "original" || got.Cwd != "/work/original" || got.Source != "cli" || got.UpdatedAt != 1786512193436 {
		t.Fatalf("snapshot after null timestamp=%+v", got)
	}
}

func TestCodexScannerRetainsModelAfterBlankOrMalformedEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeFile(t, path, strings.Join([]string{
		`{"type":"turn_context","payload":{"turn_id":"turn-1","model":"gpt-5.6-sol"}}`,
		`{"type":"turn_context","payload":{"turn_id":"turn-2","model":"gpt-5.7-codex"}}`,
		`{"type":"turn_context","payload":{"turn_id":"turn-3","model":" \t "}}`,
		`{"type":"turn_context","payload":{"turn_id":"turn-4","model":false}}`,
	}, "\n")+"\n")

	if got := NewCodexScanner().Scan(path); got.Model != "gpt-5.7-codex" {
		t.Fatalf("model=%q snapshot=%+v", got.Model, got)
	}
}

func TestCodexScannerKeepsPathsIndependent(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first.jsonl")
	second := filepath.Join(root, "second.jsonl")
	writeFile(t, first, strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"first","cwd":"/work/first"}}`,
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
	}, "\n")+"\n")
	writeFile(t, second, strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"second","cwd":"/work/second"}}`,
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-2"}}`,
		`{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-2"}}`,
	}, "\n")+"\n")

	scanner := NewCodexScanner()
	firstSnapshot := scanner.Scan(first)
	secondSnapshot := scanner.Scan(second)
	if firstSnapshot.NativeID != "first" || !firstSnapshot.Busy || firstSnapshot.Done {
		t.Fatalf("first snapshot=%+v", firstSnapshot)
	}
	if secondSnapshot.NativeID != "second" || secondSnapshot.Busy || !secondSnapshot.Done {
		t.Fatalf("second snapshot=%+v", secondSnapshot)
	}
}
