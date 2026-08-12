package collector

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"agentmon/internal/procscan"
)

const aiderRecentDuration = 4 * time.Second

type AiderAdapter struct {
	home    string
	history *AiderHistoryScanner
	now     func() time.Time
	recent  map[string]recentSession
}

func NewAiderAdapter(home string) *AiderAdapter {
	return &AiderAdapter{
		home: home, history: NewAiderHistoryScanner(), now: time.Now,
		recent: map[string]recentSession{},
	}
}

func (a *AiderAdapter) Agent() Agent { return AgentAider }

func (a *AiderAdapter) Discover(snapshot procscan.Snapshot) ([]Session, error) {
	now := a.now()
	owners := make(map[string]int)
	openOwners := make(map[string]int)
	instances := make(map[int]uint64)
	var processes []procscan.Process
	for _, process := range snapshot.Processes {
		if process.UID != snapshot.UID || !isAiderProcess(process) {
			continue
		}
		processes = append(processes, process)
		owners[filepath.Clean(process.Cwd)]++
		instances[process.PID] = process.StartTicks
	}
	for _, process := range processes {
		inputs, chats := aiderOpenHistoryPaths(process)
		for _, path := range append(inputs, chats...) {
			openOwners[path]++
		}
	}
	for id, recent := range a.recent {
		startTicks, alive := instances[recent.Row.PID]
		if !alive || startTicks != recent.StartTicks {
			delete(a.recent, id)
		}
	}

	rows := make([]Session, 0, len(processes))
	for _, process := range processes {
		row := aiderProcessObservation(process)
		inputPath, chatPath, attributed, ambiguous := aiderHistoryPaths(process, owners[filepath.Clean(process.Cwd)], openOwners)
		if !attributed {
			if ambiguous {
				a.history.Scan(inputPath, chatPath, false)
				delete(a.recent, row.ID)
			}
			if recent, ok := a.recent[row.ID]; ok && !now.After(recent.ExpiresAt) {
				rows = append(rows, recent.Row)
			} else {
				rows = append(rows, row)
			}
			continue
		}

		history := a.history.Scan(inputPath, chatPath, true)
		row.Model = ResolveAiderModel(ModelEvidence{
			RuntimeModel: history.RuntimeModel,
			ArgModel:     aiderOption(process.Args, "--model"),
			EnvModel:     process.Env["AIDER_MODEL"],
			ConfigModels: append(
				aiderConfigModels(filepath.Join(a.home, ".aider.conf.yml")),
				aiderConfigModels(filepath.Join(process.Cwd, ".aider.conf.yml"))...,
			),
			HistoryAttributed: true,
		})
		switch {
		case history.Busy:
			row.Status = "busy"
			delete(a.recent, row.ID)
		case history.Done:
			row.Status = "idle"
			recent, ok := a.recent[row.ID]
			if !ok || recent.StartTicks != process.StartTicks {
				recent.ExpiresAt = now.Add(aiderRecentDuration)
			}
			recent.Row, recent.StartTicks = row, process.StartTicks
			a.recent[row.ID] = recent
			if now.After(recent.ExpiresAt) {
				continue
			}
		default:
			delete(a.recent, row.ID)
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].PID < rows[j].PID })
	return rows, nil
}

func aiderProcessObservation(process procscan.Process) Session {
	nativeID := fmt.Sprintf("pid:%d", process.PID)
	return Session{
		ID: GlobalID(AgentAider, nativeID), NativeID: nativeID, Agent: AgentAider,
		Model: "unknown", Name: fmt.Sprintf("PID %d", process.PID), Project: filepath.Base(process.Cwd),
		Cwd: process.Cwd, PID: process.PID, Mode: Indeterminate,
	}
}

func isAiderProcess(process procscan.Process) bool {
	if aiderExecutable(process.Comm) || aiderExecutable(process.Exe) {
		return true
	}
	if !pythonExecutable(process.Exe) && !pythonExecutable(process.Comm) {
		return false
	}
	for i := 0; i+1 < len(process.Args); i++ {
		if process.Args[i] == "-m" && process.Args[i+1] == "aider" {
			return true
		}
	}
	for _, index := range []int{0, 1} {
		if index < len(process.Args) && aiderExecutable(process.Args[index]) {
			return true
		}
	}
	return false
}

func aiderExecutable(value string) bool {
	return strings.ToLower(filepath.Base(strings.TrimSpace(value))) == "aider"
}

func pythonExecutable(value string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(value)))
	return base == "python" || base == "python2" || base == "python3" ||
		strings.HasPrefix(base, "python2.") || strings.HasPrefix(base, "python3.")
}

func aiderHistoryPaths(process procscan.Process, cwdOwners int, openOwners map[string]int) (inputPath, chatPath string, attributed, ambiguous bool) {
	defaultInput := filepath.Join(process.Cwd, ".aider.input.history")
	defaultChat := filepath.Join(process.Cwd, ".aider.chat.history.md")
	openInputs, openChats := aiderOpenHistoryPaths(process)
	if len(openInputs) == 1 && len(openChats) == 1 {
		inputPath, chatPath = openInputs[0], openChats[0]
		if openOwners[inputPath] > 1 || openOwners[chatPath] > 1 {
			return inputPath, chatPath, false, true
		}
		if aiderHistoryReadable(inputPath, chatPath) {
			return inputPath, chatPath, true, false
		}
	}
	if len(openInputs) > 1 || len(openChats) > 1 {
		return defaultInput, defaultChat, false, true
	}
	if cwdOwners == 1 && aiderHistoryReadable(defaultInput, defaultChat) {
		return defaultInput, defaultChat, true, false
	}
	return defaultInput, defaultChat, false, cwdOwners > 1
}

func aiderOpenHistoryPaths(process procscan.Process) ([]string, []string) {
	defaultInput := filepath.Join(process.Cwd, ".aider.input.history")
	defaultChat := filepath.Join(process.Cwd, ".aider.chat.history.md")
	inputPaths := []string{defaultInput}
	chatPaths := []string{defaultChat}
	if path := aiderOption(process.Args, "--input-history-file"); path != "" {
		inputPaths = append(inputPaths, resolveAiderPath(process.Cwd, path))
	}
	if path := aiderOption(process.Args, "--chat-history-file"); path != "" {
		chatPaths = append(chatPaths, resolveAiderPath(process.Cwd, path))
	}
	return matchingOpenPaths(process.Files, inputPaths), matchingOpenPaths(process.Files, chatPaths)
}

func matchingOpenPaths(files []procscan.OpenFile, candidates []string) []string {
	wanted := make(map[string]bool, len(candidates))
	for _, path := range candidates {
		wanted[filepath.Clean(path)] = true
	}
	found := make(map[string]bool)
	for _, file := range files {
		path := filepath.Clean(file.Path)
		if wanted[path] {
			found[path] = true
		}
	}
	paths := make([]string, 0, len(found))
	for path := range found {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func aiderHistoryReadable(inputPath, chatPath string) bool {
	for _, path := range []string{inputPath, chatPath} {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			return false
		}
	}
	return true
}

func resolveAiderPath(cwd, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(cwd, path)
}

func aiderOption(args []string, name string) string {
	var value string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == name && i+1 < len(args):
			i++
			value = strings.TrimSpace(args[i])
		case strings.HasPrefix(args[i], name+"="):
			value = strings.TrimSpace(strings.TrimPrefix(args[i], name+"="))
		}
	}
	return value
}

func aiderConfigModels(path string) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	var models []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "model" {
			continue
		}
		value = strings.TrimSpace(value)
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		} else if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
			value = value[1 : len(value)-1]
		}
		if value != "" {
			models = append(models, value)
		}
	}
	return models
}
