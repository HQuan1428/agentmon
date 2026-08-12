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
