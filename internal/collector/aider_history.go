package collector

import (
	"bufio"
	"io"
	"os"
	"strings"
)

type AiderHistorySnapshot struct {
	Busy         bool
	Done         bool
	RuntimeModel string
}

type ModelEvidence struct {
	RuntimeModel      string
	ArgModel          string
	EnvModel          string
	ConfigModels      []string
	HistoryAttributed bool
}

type aiderHistoryTail struct {
	offset   int64
	fileInfo os.FileInfo
}

type aiderHistoryState struct {
	input            aiderHistoryTail
	chat             aiderHistoryTail
	chatPromptOpen   bool
	chatPromptEnded  bool
	chatResponseSeen bool
	snapshot         AiderHistorySnapshot
}

type AiderHistoryScanner struct {
	states map[string]*aiderHistoryState
}

func NewAiderHistoryScanner() *AiderHistoryScanner {
	return &AiderHistoryScanner{states: map[string]*aiderHistoryState{}}
}

func (scanner *AiderHistoryScanner) Scan(inputPath, chatPath string, attributed bool) AiderHistorySnapshot {
	key := inputPath + "\x00" + chatPath
	if !attributed {
		delete(scanner.states, key)
		return AiderHistorySnapshot{}
	}
	inputInfo, inputErr := os.Stat(inputPath)
	chatInfo, chatErr := os.Stat(chatPath)
	if inputErr != nil || chatErr != nil {
		delete(scanner.states, key)
		return AiderHistorySnapshot{}
	}

	state := scanner.states[key]
	if state == nil {
		state = &aiderHistoryState{}
		scanner.states[key] = state
	}
	if aiderHistoryChanged(inputInfo, state.input) || aiderHistoryChanged(chatInfo, state.chat) {
		*state = aiderHistoryState{}
	}
	var ok bool
	if chatInfo.ModTime().After(inputInfo.ModTime()) {
		ok = state.scanInput(inputPath) && state.scanChat(chatPath)
	} else {
		ok = state.scanChat(chatPath) && state.scanInput(inputPath)
	}
	if !ok {
		delete(scanner.states, key)
		return AiderHistorySnapshot{}
	}
	return state.snapshot
}

func aiderHistoryChanged(info os.FileInfo, tail aiderHistoryTail) bool {
	if tail.fileInfo == nil {
		return false
	}
	return info.Size() < tail.offset || !os.SameFile(tail.fileInfo, info)
}

func (state *aiderHistoryState) scanInput(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return false
	}
	state.input.fileInfo = info
	if _, err := file.Seek(state.input.offset, io.SeekStart); err != nil {
		return false
	}

	reader := bufio.NewReader(file)
	var entry []string
	var entryBytes int64
	valid := true
	for {
		raw, err := reader.ReadString('\n')
		if err != nil {
			return err == io.EOF
		}
		entryBytes += int64(len(raw))
		line := strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
		switch {
		case line == "":
			if valid {
				state.applyInput(entry)
			}
			state.input.offset += entryBytes
			entry, entryBytes, valid = nil, 0, true
		case strings.HasPrefix(line, "+"):
			entry = append(entry, strings.TrimPrefix(line, "+"))
		case strings.HasPrefix(line, "#"):
		default:
			valid = false
		}
	}
}

func (state *aiderHistoryState) applyInput(lines []string) {
	input := strings.TrimSpace(strings.Join(lines, "\n"))
	if input == "" {
		return
	}
	fields := strings.Fields(input)
	if strings.HasPrefix(fields[0], "/") {
		if fields[0] == "/model" && len(fields) == 2 {
			state.snapshot.RuntimeModel = fields[1]
		}
		if !aiderSlashStartsTurn(fields) {
			return
		}
	}
	state.snapshot.Busy = true
	state.snapshot.Done = false
}

func aiderSlashStartsTurn(fields []string) bool {
	if fields[0] == "/ok" {
		return true
	}
	switch fields[0] {
	case "/ask", "/code", "/architect":
		return len(fields) > 1
	default:
		return false
	}
}

func (state *aiderHistoryState) scanChat(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return false
	}
	state.chat.fileInfo = info
	if _, err := file.Seek(state.chat.offset, io.SeekStart); err != nil {
		return false
	}

	reader := bufio.NewReader(file)
	for {
		raw, err := reader.ReadString('\n')
		if err != nil {
			return err == io.EOF
		}
		state.chat.offset += int64(len(raw))
		line := strings.TrimSpace(raw)
		switch {
		case state.snapshot.Busy && strings.HasPrefix(line, "#### ") && strings.TrimSpace(strings.TrimPrefix(line, "#### ")) != "":
			state.chatPromptOpen = true
			state.chatPromptEnded = false
			state.chatResponseSeen = false
			state.snapshot.Busy = true
			state.snapshot.Done = false
		case line == "":
			if state.chatPromptOpen && !state.chatPromptEnded {
				state.chatPromptEnded = true
			} else if state.chatPromptOpen && state.chatResponseSeen {
				state.chatPromptOpen = false
				state.snapshot.Busy = false
				state.snapshot.Done = true
			}
		case strings.HasPrefix(line, "#") || strings.HasPrefix(line, "<!--"):
		default:
			if state.chatPromptOpen && state.chatPromptEnded {
				state.chatResponseSeen = true
			}
		}
	}
}

func ResolveAiderModel(evidence ModelEvidence) string {
	if !evidence.HistoryAttributed {
		return "unknown"
	}
	if model := strings.TrimSpace(evidence.RuntimeModel); model != "" {
		return model
	}
	if model := strings.TrimSpace(evidence.ArgModel); model != "" {
		return model
	}

	models := map[string]struct{}{}
	for _, model := range append([]string{evidence.EnvModel}, evidence.ConfigModels...) {
		if model = strings.TrimSpace(model); model != "" {
			models[model] = struct{}{}
		}
	}
	if len(models) != 1 {
		return "unknown"
	}
	for model := range models {
		return model
	}
	return "unknown"
}
