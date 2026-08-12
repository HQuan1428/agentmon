// internal/collector/sessions_test.go
package collector

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanSessionsFiltersDeadPIDs(t *testing.T) {
	root := t.TempDir()
	alive := os.Getpid()
	writeFile(t, filepath.Join(root, "sessions", "1.json"), `{"pid":`+itoa(alive)+`,"sessionId":"aaa","cwd":"/home/u/proj","kind":"interactive","name":"live-one","status":"busy","updatedAt":10,"jobId":""}`)
	writeFile(t, filepath.Join(root, "sessions", "2.json"), `{"pid":2147480000,"sessionId":"bbb","cwd":"/home/u/proj","kind":"bg","name":"dead-one","status":"idle","updatedAt":11,"jobId":"job2"}`)

	got, err := ScanSessions(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 live session, got %d: %+v", len(got), got)
	}
	s := got[0]
	if s.Name != "live-one" || s.Project != "proj" || s.Kind != "interactive" || s.PID != alive {
		t.Errorf("unexpected session: %+v", s)
	}
}

func TestScanSessionsUsesProvidedLiveness(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "sessions", "1.json"), `{"pid":77,"sessionId":"live","cwd":"/home/u/proj","kind":"interactive","name":"live-one","status":"busy","updatedAt":10,"jobId":""}`)
	writeFile(t, filepath.Join(root, "sessions", "2.json"), `{"pid":88,"sessionId":"dead","cwd":"/home/u/proj","kind":"interactive","name":"dead-one","status":"busy","updatedAt":11,"jobId":""}`)

	got, err := scanSessions(root, func(pid int) bool { return pid == 77 })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "live" {
		t.Fatalf("sessions=%+v", got)
	}
}

func TestScanSessionsMissingDirIsEmpty(t *testing.T) {
	got, err := ScanSessions(t.TempDir())
	if err != nil {
		t.Fatalf("missing sessions dir should be empty not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty, got %d", len(got))
	}
}
