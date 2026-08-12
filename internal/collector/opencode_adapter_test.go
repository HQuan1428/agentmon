package collector

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"agentmon/internal/procscan"
)

func TestLoopbackOpenCodeProbeGetsSessionStatuses(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/session/status" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ses_parent":{"type":"busy"},"ses_child":{"type":"idle"}}`))
	}))
	t.Cleanup(server.Close)

	_, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 150 * time.Millisecond}
	probe := LoopbackOpenCodeProbe{Client: client}
	got, err := probe.Statuses(context.Background(), []procscan.Listener{
		{Network: "tcp", Address: "0.0.0.0", Port: port},
		{Network: "tcp", Address: "127.0.0.1", Port: port},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"ses_parent": "busy", "ses_child": "idle"}
	if !reflect.DeepEqual(got, want) || requests != 1 {
		t.Fatalf("statuses=%v requests=%d", got, requests)
	}
}

func TestLoopbackOpenCodeProbeDoesNotFollowRedirects(t *testing.T) {
	var redirected int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirected++
		_, _ = w.Write([]byte(`{"ses_other":{"type":"busy"}}`))
	}))
	t.Cleanup(target.Close)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/session/status", http.StatusFound)
	}))
	t.Cleanup(server.Close)
	_, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portText)

	probe := LoopbackOpenCodeProbe{Client: &http.Client{Timeout: 150 * time.Millisecond}}
	statuses, err := probe.Statuses(context.Background(), []procscan.Listener{{Network: "tcp", Address: "127.0.0.1", Port: port}})
	if err == nil || statuses != nil || redirected != 0 {
		t.Fatalf("statuses=%v err=%v redirected=%d", statuses, err, redirected)
	}
}

func TestOpenCodeAdapterUsesSQLiteFallbackAndBuildsHierarchy(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	store := &fakeOpenCodeStore{candidates: []OpenCodeRecord{
		{
			ID: "ses_parent", Title: "Parent work", Directory: "/work/project", AgentMode: "build",
			ProviderID: "openai", ModelID: "gpt-5.6-sol", UpdatedAt: now.UnixMilli(), Busy: true,
			Todos: []OpenCodeTodo{{Status: "completed"}, {Status: "in_progress"}, {Status: "pending"}},
		},
		{ID: "ses_child", ParentID: "ses_parent", Directory: "/work/project", ProviderID: "openai", ModelID: "gpt-5.6-mini", UpdatedAt: now.UnixMilli()},
	}}
	home := t.TempDir()
	adapter := NewOpenCodeAdapter(home)
	adapter.now = func() time.Time { return now }
	var databasePath string
	adapter.storeFor = func(path string) OpenCodeStore {
		databasePath = path
		return store
	}

	rows, err := adapter.Discover(procscan.Snapshot{Processes: []procscan.Process{{
		PID: 42, StartTicks: 100, Comm: "opencode", Cwd: "/work/project",
		Listeners: []procscan.Listener{{Network: "tcp", Address: "127.0.0.1", Port: 1}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if databasePath != filepath.Join(home, ".local", "share", "opencode", "opencode.db") {
		t.Fatalf("database path=%q", databasePath)
	}
	if len(rows) != 1 {
		t.Fatalf("sessions=%+v", rows)
	}
	parent := rows[0]
	if parent.ID != "opencode:ses_parent" || parent.Agent != AgentOpenCode || parent.Model != "openai/gpt-5.6-sol" || parent.Status != "busy" || parent.Name != "Parent work" || parent.Kind != "build" {
		t.Fatalf("parent=%+v", parent)
	}
	if len(parent.Children) != 1 || parent.Children[0].ID != "opencode:ses_parent/ses_child" || parent.Children[0].Model != "" || parent.Children[0].Name != "ses_child" {
		t.Fatalf("children=%+v", parent.Children)
	}
	if parent.Mode != Determinate || parent.Done != 1 || parent.Total != 3 {
		t.Fatalf("progress=%+v", parent)
	}
}

func TestOpenCodeAdapterQueriesStatusIDsAndUsesXDGRoot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ses_parent":{"type":"busy"},"ses_child":{"type":"idle"}}`))
	}))
	t.Cleanup(server.Close)
	_, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portText)
	store := &fakeOpenCodeStore{byIDs: []OpenCodeRecord{
		{ID: "ses_parent", Directory: "/work/p", ProviderID: "anthropic", ModelID: "claude-sonnet-4-5"},
		{ID: "ses_child", ParentID: "ses_parent", Directory: "/work/p"},
	}}
	adapter := NewOpenCodeAdapter(t.TempDir())
	var databasePath string
	adapter.storeFor = func(path string) OpenCodeStore {
		databasePath = path
		return store
	}
	rows, err := adapter.Discover(procscan.Snapshot{Processes: []procscan.Process{{
		PID: 42, StartTicks: 100, Comm: "node", Exe: "/usr/bin/node",
		Args: []string{"node", "/opt/node_modules/opencode-ai/bin/opencode"},
		Cwd:  "/work/p", Env: map[string]string{"XDG_DATA_HOME": "/data/user"},
		Listeners: []procscan.Listener{{Network: "tcp", Address: "127.0.0.1", Port: port}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if databasePath != "/data/user/opencode/opencode.db" {
		t.Fatalf("database path=%q", databasePath)
	}
	if !reflect.DeepEqual(store.ids, []string{"ses_child", "ses_parent"}) {
		t.Fatalf("ByIDs=%v", store.ids)
	}
	if len(rows) != 1 || rows[0].Status != "busy" || len(rows[0].Children) != 1 || rows[0].Children[0].Status != "idle" {
		t.Fatalf("sessions=%+v", rows)
	}
	probe := adapter.probe.(*LoopbackOpenCodeProbe)
	if probe.Client.Timeout != 150*time.Millisecond {
		t.Fatalf("timeout=%s", probe.Client.Timeout)
	}
}

func TestOpenCodeAdapterRetainsTerminalRowsForFourSeconds(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	store := &fakeOpenCodeStore{candidates: []OpenCodeRecord{{ID: "ses_done", Directory: "/work/p", UpdatedAt: now.UnixMilli()}}}
	adapter := NewOpenCodeAdapter(t.TempDir())
	adapter.now = func() time.Time { return now }
	adapter.probe = fakeOpenCodeProbe{}
	adapter.storeFor = func(string) OpenCodeStore { return store }
	snapshot := procscan.Snapshot{Processes: []procscan.Process{{PID: 42, StartTicks: 100, Comm: "opencode-cli", Cwd: "/work/p"}}}

	rows, err := adapter.Discover(snapshot)
	if err != nil || len(rows) != 1 || rows[0].Status != "idle" {
		t.Fatalf("initial rows=%+v err=%v", rows, err)
	}
	store.candidates = nil
	now = now.Add(4 * time.Second)
	rows, err = adapter.Discover(snapshot)
	if err != nil || len(rows) != 1 || rows[0].ID != "opencode:ses_done" {
		t.Fatalf("at four seconds rows=%+v err=%v", rows, err)
	}
	now = now.Add(time.Nanosecond)
	rows, err = adapter.Discover(snapshot)
	if err != nil || len(rows) != 0 {
		t.Fatalf("after expiry rows=%+v err=%v", rows, err)
	}
}

type fakeOpenCodeProbe struct{}

func (fakeOpenCodeProbe) Statuses(context.Context, []procscan.Listener) (map[string]string, error) {
	return nil, nil
}

type fakeOpenCodeStore struct {
	candidates []OpenCodeRecord
	byIDs      []OpenCodeRecord
	ids        []string
}

func (s *fakeOpenCodeStore) Candidates(context.Context, []string, int64) ([]OpenCodeRecord, error) {
	return append([]OpenCodeRecord(nil), s.candidates...), nil
}

func (s *fakeOpenCodeStore) ByIDs(_ context.Context, ids []string) ([]OpenCodeRecord, error) {
	s.ids = append([]string(nil), ids...)
	return append([]OpenCodeRecord(nil), s.byIDs...), nil
}
