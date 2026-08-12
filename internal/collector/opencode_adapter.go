package collector

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"agentmon/internal/procscan"
)

const (
	openCodeTimeout        = 150 * time.Millisecond
	openCodeRecentDuration = 4 * time.Second
	openCodeBodyLimit      = 1 << 20
)

type OpenCodeStatusProbe interface {
	Statuses(context.Context, []procscan.Listener) (map[string]string, error)
}

type LoopbackOpenCodeProbe struct {
	Client *http.Client
}

func (p *LoopbackOpenCodeProbe) Statuses(ctx context.Context, listeners []procscan.Listener) (map[string]string, error) {
	client := http.Client{Timeout: openCodeTimeout}
	if p.Client != nil {
		client = *p.Client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for _, listener := range listeners {
		if listener.Network != "tcp" && listener.Network != "tcp6" {
			continue
		}
		address, err := netip.ParseAddr(listener.Address)
		if err != nil || !address.Unmap().IsLoopback() || listener.Port < 1 || listener.Port > 65535 {
			continue
		}
		requestContext, cancel := context.WithTimeout(ctx, openCodeTimeout)
		request, err := http.NewRequestWithContext(requestContext, http.MethodGet,
			"http://"+net.JoinHostPort(listener.Address, strconv.Itoa(listener.Port))+"/session/status", nil)
		if err != nil {
			cancel()
			continue
		}
		response, err := client.Do(request)
		if err != nil {
			cancel()
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, openCodeBodyLimit+1))
		closeErr := response.Body.Close()
		cancel()
		if response.StatusCode != http.StatusOK || readErr != nil || closeErr != nil || len(body) > openCodeBodyLimit {
			continue
		}
		var payload map[string]struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(body, &payload) != nil || len(payload) == 0 {
			continue
		}
		statuses := make(map[string]string, len(payload))
		valid := true
		for id, status := range payload {
			if strings.TrimSpace(id) == "" {
				valid = false
				break
			}
			if status.Type == "busy" || status.Type == "idle" {
				statuses[id] = status.Type
			}
		}
		if valid && len(statuses) > 0 {
			return statuses, nil
		}
	}
	return nil, errors.New("OpenCode status unavailable")
}

type OpenCodeAdapter struct {
	home             string
	probe            OpenCodeStatusProbe
	storeFor         func(string) OpenCodeStore
	now              func() time.Time
	recent           map[string]recentSession
	recentAttributed map[string]bool
}

func NewOpenCodeAdapter(home string) *OpenCodeAdapter {
	return &OpenCodeAdapter{
		home:  home,
		probe: &LoopbackOpenCodeProbe{Client: &http.Client{Timeout: openCodeTimeout}},
		storeFor: func(path string) OpenCodeStore {
			return NewSQLiteOpenCodeStore(path)
		},
		now:              time.Now,
		recent:           map[string]recentSession{},
		recentAttributed: map[string]bool{},
	}
}

func (a *OpenCodeAdapter) Agent() Agent { return AgentOpenCode }

func (a *OpenCodeAdapter) Discover(snapshot procscan.Snapshot) ([]Session, error) {
	now := a.now()
	instances := make(map[int]uint64)
	observed := make(map[string]openCodeObservedSession)
	fallbackOwners := make(map[string]int)
	var processes []procscan.Process
	for _, process := range snapshot.Processes {
		if isOpenCodeProcess(process) {
			processes = append(processes, process)
			instances[process.PID] = process.StartTicks
			fallbackOwners[openCodeFallbackKey(a.home, process)]++
		}
	}
	succeeded := false
	var firstError error

	for _, process := range processes {
		store := a.storeFor(filepath.Join(openCodeDataRoot(a.home, process), "opencode.db"))

		var statuses map[string]string
		if len(process.Listeners) > 0 {
			ctx, cancel := context.WithTimeout(context.Background(), openCodeTimeout)
			statuses, _ = a.probe.Statuses(ctx, process.Listeners)
			cancel()
		}
		if len(statuses) > 0 {
			ids := make([]string, 0, len(statuses))
			for id := range statuses {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			ctx, cancel := context.WithTimeout(context.Background(), openCodeTimeout)
			records, err := store.ByIDs(ctx, ids)
			cancel()
			if err != nil {
				if firstError == nil {
					firstError = err
				}
			} else {
				succeeded = true
				a.observe(process, records, statuses, true, observed)
			}
		}

		if fallbackOwners[openCodeFallbackKey(a.home, process)] == 1 {
			ctx, cancel := context.WithTimeout(context.Background(), openCodeTimeout)
			records, err := store.Candidates(ctx, []string{process.Cwd}, now.Add(-openCodeRecentDuration).UnixMilli())
			cancel()
			if err != nil {
				if firstError == nil {
					firstError = err
				}
			} else {
				succeeded = true
				a.observe(process, records, statuses, false, observed)
			}
		}
	}

	if len(processes) > 0 && !succeeded && firstError != nil {
		return nil, firstError
	}
	for nativeID, entry := range observed {
		row := entry.Row
		switch row.Status {
		case "busy":
			delete(a.recent, row.ID)
			delete(a.recentAttributed, row.ID)
		case "idle":
			recent, ok := a.recent[row.ID]
			if !ok || recent.StartTicks != entry.StartTicks {
				recent.ExpiresAt = now.Add(openCodeRecentDuration)
			}
			recent.Row, recent.ParentID, recent.StartTicks = row, entry.ParentID, entry.StartTicks
			a.recent[row.ID] = recent
			a.recentAttributed[row.ID] = entry.Attributed
			if now.After(recent.ExpiresAt) {
				delete(observed, nativeID)
			}
		}
	}
	for id, recent := range a.recent {
		startTicks, alive := instances[recent.Row.PID]
		process, processAlive := processByPID(processes, recent.Row.PID)
		ambiguousFallback := processAlive && fallbackOwners[openCodeFallbackKey(a.home, process)] > 1 && !a.recentAttributed[id]
		if !alive || startTicks != recent.StartTicks || now.After(recent.ExpiresAt) || ambiguousFallback {
			delete(a.recent, id)
			delete(a.recentAttributed, id)
			continue
		}
		if _, exists := observed[recent.Row.NativeID]; !exists {
			observed[recent.Row.NativeID] = openCodeObservedSession{
				Row: recent.Row, ParentID: recent.ParentID, StartTicks: recent.StartTicks,
			}
		}
	}
	return buildOpenCodeTree(observed), nil
}

type openCodeObservedSession struct {
	Row        Session
	ParentID   string
	StartTicks uint64
	Attributed bool
}

func (a *OpenCodeAdapter) observe(process procscan.Process, records []OpenCodeRecord, statuses map[string]string, attributed bool, observed map[string]openCodeObservedSession) {
	for _, record := range records {
		if strings.TrimSpace(record.ID) == "" {
			continue
		}
		status := statuses[record.ID]
		if status != "busy" && status != "idle" {
			status = ""
		}
		if status == "" {
			if record.Busy {
				status = "busy"
			} else {
				status = "idle"
			}
		} else if status != "idle" {
			status = "busy"
		}
		row := openCodeSession(process, record, status)
		recordAttributed := attributed
		if previous, ok := observed[record.ID]; ok && previous.Attributed {
			recordAttributed = true
		}
		if statuses[record.ID] == "busy" || statuses[record.ID] == "idle" {
			recordAttributed = true
		}
		observed[record.ID] = openCodeObservedSession{Row: row, ParentID: record.ParentID, StartTicks: process.StartTicks, Attributed: recordAttributed}
	}
}

func openCodeSession(process procscan.Process, record OpenCodeRecord, status string) Session {
	name := strings.TrimSpace(record.Title)
	if name == "" {
		name = strings.TrimSpace(record.Slug)
		if name == "" {
			name = record.ID
		}
	}
	model := ""
	if record.ProviderID != "" && record.ModelID != "" {
		model = record.ProviderID + "/" + record.ModelID
	}
	row := Session{
		ID: GlobalID(AgentOpenCode, record.ID), NativeID: record.ID, Agent: AgentOpenCode,
		Model: ModelOrUnknown(model), Name: name, Project: filepath.Base(record.Directory), Cwd: record.Directory,
		Kind: record.AgentMode, Status: status, PID: process.PID, Mode: Indeterminate, UpdatedAt: record.UpdatedAt,
	}
	if strings.TrimSpace(record.ParentID) != "" {
		row.Model = ""
	}
	if len(record.Todos) > 0 {
		row.Mode, row.Total = Determinate, len(record.Todos)
		for _, todo := range record.Todos {
			if todo.Status == "completed" {
				row.Done++
			}
		}
	}
	return row
}

func buildOpenCodeTree(observed map[string]openCodeObservedSession) []Session {
	children := make(map[string][]string)
	var roots []string
	for nativeID, entry := range observed {
		if entry.ParentID != "" && entry.ParentID != nativeID {
			if _, ok := observed[entry.ParentID]; ok {
				children[entry.ParentID] = append(children[entry.ParentID], nativeID)
				continue
			}
		}
		roots = append(roots, nativeID)
	}
	sort.Strings(roots)
	for parentID := range children {
		sort.Strings(children[parentID])
	}
	visited := make(map[string]bool)
	var build func(string, string) Session
	build = func(nativeID, parentGlobalID string) Session {
		visited[nativeID] = true
		row := observed[nativeID].Row
		if parentGlobalID != "" {
			row.ID = parentGlobalID + "/" + nativeID
			row.Model = ""
		}
		for _, childID := range children[nativeID] {
			if !visited[childID] {
				row.Children = append(row.Children, build(childID, row.ID))
			}
		}
		return row
	}
	rows := make([]Session, 0, len(roots))
	for _, nativeID := range roots {
		rows = append(rows, build(nativeID, ""))
	}
	for nativeID := range observed {
		if !visited[nativeID] {
			rows = append(rows, build(nativeID, ""))
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

func isOpenCodeProcess(process procscan.Process) bool {
	if openCodeExecutable(process.Comm) || openCodeExecutable(process.Exe) {
		return true
	}
	wrapper := strings.ToLower(filepath.Base(process.Exe))
	if wrapper == "." || wrapper == "" {
		wrapper = strings.ToLower(filepath.Base(process.Comm))
	}
	if wrapper != "node" && wrapper != "nodejs" {
		return false
	}
	for _, arg := range process.Args {
		path := strings.ToLower(filepath.ToSlash(arg))
		if strings.HasSuffix(path, "/node_modules/opencode-ai/bin/opencode") ||
			strings.HasSuffix(path, "/node_modules/opencode-ai/bin/opencode.js") {
			return true
		}
	}
	return false
}

func openCodeExecutable(name string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(name)))
	return base == "opencode" || base == "opencode-cli"
}

func openCodeDataRoot(home string, process procscan.Process) string {
	if root := strings.TrimSpace(process.Env["XDG_DATA_HOME"]); root != "" {
		return filepath.Join(root, "opencode")
	}
	return filepath.Join(home, ".local", "share", "opencode")
}

func openCodeFallbackKey(home string, process procscan.Process) string {
	return openCodeDataRoot(home, process) + "\x00" + process.Cwd
}

func processByPID(processes []procscan.Process, pid int) (procscan.Process, bool) {
	for _, process := range processes {
		if process.PID == pid {
			return process, true
		}
	}
	return procscan.Process{}, false
}
