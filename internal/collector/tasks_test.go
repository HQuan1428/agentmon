package collector

import (
	"path/filepath"
	"testing"
)

func TestParseTasksDir(t *testing.T) {
	root := t.TempDir()
	sid := "sess1"
	dir := filepath.Join(root, "tasks", sid)
	writeFile(t, filepath.Join(dir, "1.json"), `{"id":"1","status":"completed"}`)
	writeFile(t, filepath.Join(dir, "2.json"), `{"id":"2","status":"in_progress"}`)
	writeFile(t, filepath.Join(dir, "3.json"), `{"id":"3","status":"pending"}`)
	writeFile(t, filepath.Join(dir, "4.json"), `{"id":"4","status":"deleted"}`) // excluded
	writeFile(t, filepath.Join(dir, ".lock"), ``)                               // ignored (not .json)

	done, total, ok := ParseTasksDir(root, sid)
	if !ok || total != 3 || done != 1 {
		t.Fatalf("ParseTasksDir=(%d,%d,%v) want (1,3,true)", done, total, ok)
	}

	if _, _, ok := ParseTasksDir(root, "missing"); ok {
		t.Error("missing dir should be found=false")
	}
}
