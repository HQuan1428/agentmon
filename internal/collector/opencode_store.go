package collector

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	_ "modernc.org/sqlite"
)

type OpenCodeTodo struct {
	Content string
	Status  string
}

type OpenCodeRecord struct {
	ID, ParentID, Title, Directory, AgentMode string
	ProviderID, ModelID                       string
	UpdatedAt                                 int64
	Busy                                      bool
	Todos                                     []OpenCodeTodo
}

type OpenCodeStore interface {
	Candidates(context.Context, []string, int64) ([]OpenCodeRecord, error)
	ByIDs(context.Context, []string) ([]OpenCodeRecord, error)
}

type SQLiteOpenCodeStore struct {
	Path string
}

func NewSQLiteOpenCodeStore(path string) *SQLiteOpenCodeStore {
	return &SQLiteOpenCodeStore{Path: path}
}

func (s *SQLiteOpenCodeStore) Candidates(ctx context.Context, directories []string, recentAfter int64) ([]OpenCodeRecord, error) {
	if len(directories) == 0 {
		return nil, nil
	}
	records, err := s.records(ctx, "directory IN ("+placeholders(len(directories))+")", stringsToAny(directories))
	if err != nil {
		return nil, err
	}
	selected := records[:0]
	for _, record := range records {
		if record.Busy || record.UpdatedAt >= recentAfter {
			selected = append(selected, record)
		}
	}
	if err := s.loadTodos(ctx, selected); err != nil {
		return nil, err
	}
	return selected, nil
}

func (s *SQLiteOpenCodeStore) ByIDs(ctx context.Context, ids []string) ([]OpenCodeRecord, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	records, err := s.records(ctx, "id IN ("+placeholders(len(ids))+")", stringsToAny(ids))
	if err != nil {
		return nil, err
	}
	if err := s.loadTodos(ctx, records); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *SQLiteOpenCodeStore) records(ctx context.Context, where string, args []any) ([]OpenCodeRecord, error) {
	db, err := s.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `SELECT id, parent_id, title, directory, agent, model, time_updated
		FROM session WHERE `+where+` ORDER BY time_updated DESC, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []OpenCodeRecord
	for rows.Next() {
		var record OpenCodeRecord
		var parentID, agentMode, model sql.NullString
		if err := rows.Scan(&record.ID, &parentID, &record.Title, &record.Directory, &agentMode, &model, &record.UpdatedAt); err != nil {
			return nil, err
		}
		record.ParentID = parentID.String
		record.AgentMode = agentMode.String
		parseOpenCodeModel(model.String, &record.ProviderID, &record.ModelID)
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return records, nil
	}
	if err := loadOpenCodeState(ctx, db, records); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *SQLiteOpenCodeStore) loadTodos(ctx context.Context, records []OpenCodeRecord) error {
	if len(records) == 0 {
		return nil
	}
	db, err := s.open()
	if err != nil {
		return err
	}
	defer db.Close()

	args := make([]any, len(records))
	byID := make(map[string]int, len(records))
	for i := range records {
		args[i] = records[i].ID
		byID[records[i].ID] = i
	}
	rows, err := db.QueryContext(ctx, `SELECT session_id, content, status FROM todo
		WHERE session_id IN (`+placeholders(len(records))+`) ORDER BY session_id, position`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sessionID string
		var todo OpenCodeTodo
		if err := rows.Scan(&sessionID, &todo.Content, &todo.Status); err != nil {
			return err
		}
		if i, ok := byID[sessionID]; ok {
			records[i].Todos = append(records[i].Todos, todo)
		}
	}
	return rows.Err()
}

func (s *SQLiteOpenCodeStore) open() (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: s.Path, RawQuery: "mode=ro"}
	dsn := u.String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func loadOpenCodeState(ctx context.Context, db *sql.DB, records []OpenCodeRecord) error {
	args := make([]any, len(records))
	byID := make(map[string]int, len(records))
	for i := range records {
		args[i] = records[i].ID
		byID[records[i].ID] = i
	}

	rows, err := db.QueryContext(ctx, `SELECT session_id, data FROM message
		WHERE session_id IN (`+placeholders(len(records))+`)
		ORDER BY session_id, time_created DESC, id DESC`, args...)
	if err != nil {
		return err
	}
	seenLatest := make(map[string]bool, len(records))
	seenModel := make(map[string]bool, len(records))
	for rows.Next() {
		var sessionID string
		var data []byte
		if err := rows.Scan(&sessionID, &data); err != nil {
			rows.Close()
			return err
		}
		var message openCodeMessage
		if json.Unmarshal(data, &message) != nil {
			continue
		}
		i, ok := byID[sessionID]
		if !ok {
			continue
		}
		if message.Role == "assistant" && !seenLatest[sessionID] {
			seenLatest[sessionID] = true
			records[i].Busy = message.Role == "assistant" && message.Time.Completed == nil && !hasJSONValue(message.Error)
		}
		providerID, modelID := message.runtimeModel()
		if (message.Role == "assistant" || message.Role == "user") && !seenModel[sessionID] && providerID != "" && modelID != "" {
			seenModel[sessionID] = true
			records[i].ProviderID = providerID
			records[i].ModelID = modelID
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	parts, err := db.QueryContext(ctx, `SELECT session_id, data FROM part
		WHERE session_id IN (`+placeholders(len(records))+`)`, args...)
	if err != nil {
		return err
	}
	defer parts.Close()
	for parts.Next() {
		var sessionID string
		var data []byte
		if err := parts.Scan(&sessionID, &data); err != nil {
			return err
		}
		var part struct {
			Type  string `json:"type"`
			State struct {
				Status string `json:"status"`
			} `json:"state"`
		}
		if json.Unmarshal(data, &part) != nil || part.Type != "tool" {
			continue
		}
		if i, ok := byID[sessionID]; ok && (part.State.Status == "pending" || part.State.Status == "running") {
			records[i].Busy = true
		}
	}
	return parts.Err()
}

type openCodeMessage struct {
	Role       string          `json:"role"`
	ProviderID string          `json:"providerID"`
	ModelID    string          `json:"modelID"`
	Error      json.RawMessage `json:"error"`
	Time       struct {
		Completed *int64 `json:"completed"`
	} `json:"time"`
	Model struct {
		ProviderID string `json:"providerID"`
		ModelID    string `json:"modelID"`
		ID         string `json:"id"`
	} `json:"model"`
}

func (m openCodeMessage) runtimeModel() (string, string) {
	if m.ProviderID != "" && m.ModelID != "" {
		return m.ProviderID, m.ModelID
	}
	modelID := m.Model.ModelID
	if modelID == "" {
		modelID = m.Model.ID
	}
	return m.Model.ProviderID, modelID
}

func parseOpenCodeModel(data string, providerID, modelID *string) {
	var model struct {
		ProviderID string `json:"providerID"`
		ModelID    string `json:"modelID"`
		ID         string `json:"id"`
	}
	if json.Unmarshal([]byte(data), &model) != nil {
		return
	}
	*providerID = model.ProviderID
	*modelID = model.ModelID
	if *modelID == "" {
		*modelID = model.ID
	}
}

func hasJSONValue(value json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(value))
	return trimmed != "" && trimmed != "null"
}

func placeholders(count int) string {
	if count < 1 {
		panic(fmt.Sprintf("invalid placeholder count %d", count))
	}
	return strings.TrimRight(strings.Repeat("?,", count), ",")
}

func stringsToAny(values []string) []any {
	args := make([]any, len(values))
	for i := range values {
		args[i] = values[i]
	}
	return args
}
