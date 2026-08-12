//go:build linux

package procscan

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var retainedEnvironmentKeys = map[string]struct{}{
	"XDG_DATA_HOME": {},
	"AIDER_MODEL":   {},
}

func (p *ProcFS) Snapshot() (Snapshot, error) {
	entries, err := os.ReadDir(p.Root)
	if err != nil {
		return Snapshot{}, err
	}

	snapshot := Snapshot{UID: p.UID}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid < 0 {
			continue
		}
		process, err := p.readProcess(pid)
		if err != nil || process.UID != p.UID {
			continue
		}
		snapshot.Processes = append(snapshot.Processes, process)
	}
	return snapshot, nil
}

func (p *ProcFS) readProcess(pid int) (Process, error) {
	dir := filepath.Join(p.Root, strconv.Itoa(pid))
	uid, err := readEffectiveUID(filepath.Join(dir, "status"))
	if err != nil {
		return Process{}, err
	}
	ppid, startTicks, err := readStat(filepath.Join(dir, "stat"))
	if err != nil {
		return Process{}, err
	}
	comm, err := readTrimmed(filepath.Join(dir, "comm"))
	if err != nil {
		return Process{}, err
	}
	args, err := readNULSeparated(filepath.Join(dir, "cmdline"))
	if err != nil {
		return Process{}, err
	}
	env, err := readEnvironment(filepath.Join(dir, "environ"))
	if err != nil {
		return Process{}, err
	}
	cwd, err := os.Readlink(filepath.Join(dir, "cwd"))
	if err != nil {
		return Process{}, err
	}
	exe, err := os.Readlink(filepath.Join(dir, "exe"))
	if err != nil {
		return Process{}, err
	}

	return Process{
		PID:        pid,
		PPID:       ppid,
		UID:        uid,
		StartTicks: startTicks,
		Comm:       comm,
		Exe:        exe,
		Cwd:        cwd,
		Args:       args,
		Env:        env,
	}, nil
}

func readEffectiveUID(path string) (uint32, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "Uid:" {
			uid, err := strconv.ParseUint(fields[2], 10, 32)
			if err != nil {
				return 0, err
			}
			return uint32(uid), nil
		}
	}
	return 0, fmt.Errorf("effective UID missing from %s", path)
}

func readStat(path string) (int, uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	stat := strings.TrimSpace(string(data))
	closeComm := strings.LastIndex(stat, ")")
	if closeComm == -1 || closeComm+1 >= len(stat) {
		return 0, 0, fmt.Errorf("malformed stat %s", path)
	}
	fields := strings.Fields(stat[closeComm+1:])
	if len(fields) < 20 {
		return 0, 0, fmt.Errorf("malformed stat %s", path)
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, err
	}
	startTicks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	return ppid, startTicks, nil
}

func readTrimmed(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func readNULSeparated(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	values := strings.Split(string(data), "\x00")
	if len(values) > 0 && values[len(values)-1] == "" {
		values = values[:len(values)-1]
	}
	return values, nil
}

func readEnvironment(path string) (map[string]string, error) {
	values, err := readNULSeparated(path)
	if err != nil {
		return nil, err
	}
	env := make(map[string]string)
	for _, value := range values {
		key, value, found := strings.Cut(value, "=")
		if !found {
			continue
		}
		if _, retain := retainedEnvironmentKeys[key]; retain {
			env[key] = value
		}
	}
	return env, nil
}
