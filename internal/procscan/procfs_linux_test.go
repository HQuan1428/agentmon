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

func TestProcFSSnapshotExcludesInactiveProcessStates(t *testing.T) {
	root := t.TempDir()
	for i, state := range []string{"T", "t", "Z", "X", "x"} {
		pid := 101 + i
		writeProc(t, root, pid, 1000, 1000, "codex", []string{"codex"}, "/work/a", nil)
		setProcState(t, root, pid, state)
	}

	snap, err := (&ProcFS{Root: root, UID: 1000}).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Processes) != 0 {
		t.Fatalf("processes=%+v", snap.Processes)
	}
}

func TestProcFSSnapshotRetainsActiveAndWaitingStates(t *testing.T) {
	root := t.TempDir()
	for i, state := range []string{"R", "S", "D", "I"} {
		pid := 101 + i
		writeProc(t, root, pid, 1000, 1000, "codex", []string{"codex"}, "/work/a", nil)
		setProcState(t, root, pid, state)
	}

	snap, err := (&ProcFS{Root: root, UID: 1000}).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Processes) != 4 {
		t.Fatalf("processes=%+v", snap.Processes)
	}
}

func TestProcFSRejectsOtherUIDBeforeProcessMetadata(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, 101, 1000, 1000, "codex", []string{"codex"}, "/work/a", nil)
	foreignDir := filepath.Join(root, "202")
	if err := os.Mkdir(foreignDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreignDir, "status"), []byte("Name:\taider\nUid:\t2000\t2000\t2000\t2000\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	proc := &ProcFS{Root: root, UID: 1000}
	if _, matched, err := proc.readOwnedProcess(202); err != nil || matched {
		t.Fatalf("foreign process result: matched=%t err=%v", matched, err)
	}
	snap, err := proc.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Processes) != 1 || snap.Processes[0].PID != 101 {
		t.Fatalf("processes=%+v", snap.Processes)
	}
}

func TestProcFSOpenFiles(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, 101, 1000, 1000, "codex", []string{"codex"}, "/work/a", nil)

	fdDir := filepath.Join(root, "101", "fd")
	if err := os.Symlink("/home/u/.codex/sessions/2026/08/12/rollout-a.jsonl", filepath.Join(fdDir, "7")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/tmp/other.jsonl", filepath.Join(fdDir, "10")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("socket:[34567]", filepath.Join(fdDir, "8")); err != nil {
		t.Fatal(err)
	}

	snap, err := (&ProcFS{Root: root, UID: 1000}).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	p := snap.Processes[0]
	if len(p.Files) != 2 || p.Files[0].FD != 7 || p.Files[0].Path != "/home/u/.codex/sessions/2026/08/12/rollout-a.jsonl" || p.Files[1].FD != 10 || p.Files[1].Path != "/tmp/other.jsonl" {
		t.Fatalf("files=%+v", p.Files)
	}
}

func TestProcFSLoopbackListeners(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, 101, 1000, 1000, "codex", []string{"codex"}, "/work/a", nil)
	if err := os.Symlink("socket:[34567]", filepath.Join(root, "101", "fd", "8")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("socket:[34568]", filepath.Join(root, "101", "fd", "9")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("socket:[34569]", filepath.Join(root, "101", "fd", "10")); err != nil {
		t.Fatal(err)
	}
	writeTCPFixture(t, root, "tcp", []string{
		"   0: 0100007F:1002 00000000:0000 0A 00000000:00000000 00:00000000 00000000   1000        0 34569 1 0000000000000000 100 0 0 10 0",
		"   0: 0100007F:1000 00000000:0000 0A 00000000:00000000 00:00000000 00000000   1000        0 34567 1 0000000000000000 100 0 0 10 0",
		"   1: 00000000:1001 00000000:0000 0A 00000000:00000000 00:00000000 00000000   1000        0 34568 1 0000000000000000 100 0 0 10 0",
	})

	snap, err := (&ProcFS{Root: root, UID: 1000}).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	p := snap.Processes[0]
	if len(p.Listeners) != 2 || p.Listeners[0].Network != "tcp" || p.Listeners[0].Address != "127.0.0.1" || p.Listeners[0].Port != 4096 || p.Listeners[1].Network != "tcp" || p.Listeners[1].Address != "127.0.0.1" || p.Listeners[1].Port != 4098 {
		t.Fatalf("listeners=%+v", p.Listeners)
	}
}

func TestProcFSRetainsOnlyValidOwnedListenEntries(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, 101, 1000, 1000, "codex", []string{"codex"}, "/work/a", nil)
	fdDir := filepath.Join(root, "101", "fd")
	for name, target := range map[string]string{
		"8":        "socket:[34567]",
		"9":        "socket:[34568]",
		"10":       "socket:[not-an-inode]",
		"11":       "socket:[34571]",
		"-1":       "socket:[34572]",
		"not-a-fd": "socket:[34570]",
	} {
		if err := os.Symlink(target, filepath.Join(fdDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	writeTCPFixture(t, root, "tcp", []string{
		"   0: 0100007F:1000 00000000:0000 0A 00000000:00000000 00:00000000 00000000   1000        0 34567 1 0000000000000000 100 0 0 10 0",
		"   1: 0100007F:1001 00000000:0000 01 00000000:00000000 00:00000000 00000000   1000        0 34568 1 0000000000000000 100 0 0 10 0",
		"   2: 0100007F:1002 00000000:0000 0A 00000000:00000000 00:00000000 00000000   1000        0 34569 1 0000000000000000 100 0 0 10 0",
		"   3: 0100007F:1003 00000000:0000 0A 00000000:00000000 00:00000000 00000000   1000        0 not-an-inode 1 0000000000000000 100 0 0 10 0",
		"   4: 0100007F:1004 00000000:0000 0A 00000000:00000000 00:00000000 00000000   1000        0 34570 1 0000000000000000 100 0 0 10 0",
		"   5: 0100007F:1005 00000000:0000 0A 00000000:00000000 00:00000000 00000000   1000        0 34572 1 0000000000000000 100 0 0 10 0",
		"   6: malformed",
		"   7: not-hex:1007 00000000:0000 0A 00000000:00000000 00:00000000 00000000   1000        0 34571 1 0000000000000000 100 0 0 10 0",
	})

	snap, err := (&ProcFS{Root: root, UID: 1000}).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	p := snap.Processes[0]
	if len(p.Listeners) != 1 || p.Listeners[0].Network != "tcp" || p.Listeners[0].Address != "127.0.0.1" || p.Listeners[0].Port != 4096 {
		t.Fatalf("listeners=%+v", p.Listeners)
	}
}

func TestProcFSIPv6LoopbackListeners(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, 101, 1000, 1000, "codex", []string{"codex"}, "/work/a", nil)
	if err := os.Symlink("socket:[45678]", filepath.Join(root, "101", "fd", "8")); err != nil {
		t.Fatal(err)
	}
	writeTCPFixture(t, root, "tcp6", []string{
		"   0: 00000000000000000000000001000000:1001 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000   1000        0 45678 1 0000000000000000 100 0 0 10 0",
	})

	snap, err := (&ProcFS{Root: root, UID: 1000}).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	p := snap.Processes[0]
	if len(p.Listeners) != 1 || p.Listeners[0].Network != "tcp6" || p.Listeners[0].Address != "::1" || p.Listeners[0].Port != 4097 {
		t.Fatalf("listeners=%+v", p.Listeners)
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

func setProcState(t *testing.T, root string, pid int, state string) {
	t.Helper()
	if state == "S" {
		return
	}
	path := filepath.Join(root, strconv.Itoa(pid), "stat")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), ") S ", ") "+state+" ", 1)
	if updated == string(data) {
		t.Fatalf("stat fixture missing state: %q", data)
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTCPFixture(t *testing.T, root, network string, rows []string) {
	t.Helper()
	netDir := filepath.Join(root, "net")
	if err := os.MkdirAll(netDir, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" + strings.Join(rows, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(netDir, network), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
