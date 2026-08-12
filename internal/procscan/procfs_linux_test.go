package procscan

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProcFSSnapshotFiltersUIDAndSecrets(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, 101, 2000, 1000, "codex", []string{"codex"}, "/work/a",
		[]string{"XDG_DATA_HOME=/data/u", "AIDER_MODEL=sonnet", "OPENAI_API_KEY=secret"})
	writeProc(t, root, 202, 1000, 2000, "aider", []string{"python", "-m", "aider"}, "/work/b", nil)

	snap, err := (&ProcFS{Root: root, UID: 1000}).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Processes) != 1 || snap.Processes[0].PID != 101 {
		t.Fatalf("processes=%+v", snap.Processes)
	}
	if snap.Processes[0].Env["XDG_DATA_HOME"] != "/data/u" || snap.Processes[0].Env["AIDER_MODEL"] != "sonnet" {
		t.Fatalf("whitelist missing: %#v", snap.Processes[0].Env)
	}
	if _, leaked := snap.Processes[0].Env["OPENAI_API_KEY"]; leaked {
		t.Fatal("secret environment value retained")
	}
	process := snap.Processes[0]
	if process.UID != 1000 {
		t.Fatalf("UID=%d", process.UID)
	}
	if process.PPID != 1 || process.StartTicks != 123 {
		t.Fatalf("metadata PPID=%d StartTicks=%d", process.PPID, process.StartTicks)
	}
	if len(process.Args) != 1 || process.Args[0] != "codex" {
		t.Fatalf("args=%q", process.Args)
	}
	if process.Cwd != "/work/a" || process.Exe != "/usr/bin/codex" {
		t.Fatalf("cwd=%q exe=%q", process.Cwd, process.Exe)
	}
}

func TestProcFSSnapshotSkipsMalformedProcess(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, 101, 1000, 1000, "codex", []string{"codex"}, "/work/a", nil)
	writeProc(t, root, 202, 1000, 1000, "broken", []string{"broken"}, "/work/b", nil)
	if err := os.WriteFile(filepath.Join(root, "202", "stat"), []byte("not a stat file\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	snap, err := (&ProcFS{Root: root, UID: 1000}).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Processes) != 1 || !snap.HasPID(101) || snap.HasPID(202) {
		t.Fatalf("processes=%+v", snap.Processes)
	}
}

func TestProcFSSnapshotReturnsNoArgsForEmptyCmdline(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, 101, 1000, 1000, "kernel-thread", nil, "/work/a", nil)

	snap, err := (&ProcFS{Root: root, UID: 1000}).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Processes) != 1 {
		t.Fatalf("processes=%+v", snap.Processes)
	}
	if len(snap.Processes[0].Args) != 0 {
		t.Fatalf("args=%q", snap.Processes[0].Args)
	}
}

func writeProc(t *testing.T, root string, pid int, realUID, effectiveUID uint32, comm string, args []string, cwd string, env []string) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(filepath.Join(dir, "fd"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, value string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("status", fmt.Sprintf("Name:\t%s\nUid:\t%d\t%d\t%d\t%d\n", comm, realUID, effectiveUID, realUID, realUID))
	statFields := []string{"S", "1"}
	for len(statFields) < 19 {
		statFields = append(statFields, "0")
	}
	statFields = append(statFields, "123")
	write("stat", fmt.Sprintf("%d (%s) %s\n", pid, comm, strings.Join(statFields, " ")))
	write("comm", comm+"\n")
	cmdline := strings.Join(args, "\x00")
	if len(args) > 0 {
		cmdline += "\x00"
	}
	write("cmdline", cmdline)
	write("environ", strings.Join(env, "\x00")+"\x00")
	if err := os.Symlink(cwd, filepath.Join(dir, "cwd")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("/usr/bin", comm), filepath.Join(dir, "exe")); err != nil {
		t.Fatal(err)
	}
}
