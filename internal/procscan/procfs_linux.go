//go:build linux

package procscan

import (
	"encoding/hex"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
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
	files, socketInodes := readFDs(dir)

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
		Files:      files,
		Listeners:  readListeners(filepath.Join(p.Root, "net"), socketInodes),
	}, nil
}

func readFDs(procDir string) (files []OpenFile, socketInodes map[string]bool) {
	socketInodes = map[string]bool{}
	entries, err := os.ReadDir(filepath.Join(procDir, "fd"))
	if err != nil {
		return nil, socketInodes
	}
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join(procDir, "fd", entry.Name()))
		if err != nil {
			continue
		}
		if strings.HasPrefix(target, "socket:[") && strings.HasSuffix(target, "]") {
			socketInodes[strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")] = true
			continue
		}
		fd, err := strconv.Atoi(entry.Name())
		if err == nil && filepath.IsAbs(target) {
			files = append(files, OpenFile{FD: fd, Path: target})
		}
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].FD == files[j].FD {
			return files[i].Path < files[j].Path
		}
		return files[i].FD < files[j].FD
	})
	return files, socketInodes
}

func readListeners(netDir string, socketInodes map[string]bool) []Listener {
	var listeners []Listener
	listeners = append(listeners, readTCPListeners(filepath.Join(netDir, "tcp"), "tcp", socketInodes)...)
	listeners = append(listeners, readTCPListeners(filepath.Join(netDir, "tcp6"), "tcp6", socketInodes)...)
	sort.Slice(listeners, func(i, j int) bool {
		if listeners[i].Network != listeners[j].Network {
			return listeners[i].Network < listeners[j].Network
		}
		if listeners[i].Address != listeners[j].Address {
			return listeners[i].Address < listeners[j].Address
		}
		return listeners[i].Port < listeners[j].Port
	})
	return listeners
}

func readTCPListeners(path, network string, socketInodes map[string]bool) []Listener {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var listeners []Listener
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || fields[3] != "0A" || !socketInodes[fields[9]] {
			continue
		}
		address, port, ok := parseTCPAddress(fields[1], network)
		if !ok {
			continue
		}
		listeners = append(listeners, Listener{Network: network, Address: address, Port: port})
	}
	return listeners
}

func parseTCPAddress(localAddress, network string) (string, int, bool) {
	hexAddress, hexPort, found := strings.Cut(localAddress, ":")
	if !found {
		return "", 0, false
	}
	port, err := strconv.ParseUint(hexPort, 16, 16)
	if err != nil {
		return "", 0, false
	}
	address, ok := decodeTCPAddress(hexAddress, network)
	if !ok || !(address.IsLoopback() || address.Unmap().IsLoopback()) {
		return "", 0, false
	}
	return address.String(), int(port), true
}

func decodeTCPAddress(hexAddress, network string) (netip.Addr, bool) {
	bytes, err := hex.DecodeString(hexAddress)
	if err != nil {
		return netip.Addr{}, false
	}
	switch network {
	case "tcp":
		if len(bytes) != 4 {
			return netip.Addr{}, false
		}
		return netip.AddrFrom4([4]byte{bytes[3], bytes[2], bytes[1], bytes[0]}), true
	case "tcp6":
		if len(bytes) != 16 {
			return netip.Addr{}, false
		}
		for i := 0; i < len(bytes); i += 4 {
			bytes[i], bytes[i+3] = bytes[i+3], bytes[i]
			bytes[i+1], bytes[i+2] = bytes[i+2], bytes[i+1]
		}
		var address [16]byte
		copy(address[:], bytes)
		return netip.AddrFrom16(address), true
	default:
		return netip.Addr{}, false
	}
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
