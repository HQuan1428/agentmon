package collector

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAiderHistoryReducesLifecycleAndRuntimeModel(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, ".aider.input.history")
	chat := filepath.Join(dir, ".aider.chat.history.md")
	writeFile(t, input, "# 2026-08-12 10:00:00\n+write the tests\n\n")
	writeFile(t, chat, "#### user\nold request\n\n#### assistant\nold answer\n")
	old := time.Unix(1, 0)
	newer := time.Unix(2, 0)
	if err := os.Chtimes(input, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(chat, newer, newer); err != nil {
		t.Fatal(err)
	}

	scanner := NewAiderHistoryScanner()
	if got := scanner.Scan(input, chat, true); got.Busy || !got.Done {
		t.Fatalf("completed initial turn=%+v", got)
	}

	appendFile(t, input, "# 2026-08-12 10:01:00\n+fix the parser\n\n")
	if got := scanner.Scan(input, chat, true); !got.Busy || got.Done {
		t.Fatalf("submitted turn=%+v", got)
	}

	appendFile(t, chat, "\n#### user\nfix the parser\n\n#### assistant\nfixed\n")
	if got := scanner.Scan(input, chat, true); got.Busy || !got.Done {
		t.Fatalf("assistant completion=%+v", got)
	}

	appendFile(t, input, "# 2026-08-12 10:02:00\n+/model openrouter/deepseek/deepseek-chat\n\n")
	got := scanner.Scan(input, chat, true)
	if got.Busy || !got.Done || got.RuntimeModel != "openrouter/deepseek/deepseek-chat" {
		t.Fatalf("model command=%+v", got)
	}

	appendFile(t, input, "# 2026-08-12 10:03:00\n+/help\n\n")
	if got := scanner.Scan(input, chat, true); got.Busy || !got.Done || got.RuntimeModel != "openrouter/deepseek/deepseek-chat" {
		t.Fatalf("help command=%+v", got)
	}
}

func TestAiderHistoryWaitsForCompleteEntriesAndLines(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.history")
	chat := filepath.Join(dir, "chat.md")
	writeFile(t, input, "# 2026-08-12 10:00:00\n+unfinished")
	writeFile(t, chat, "")

	scanner := NewAiderHistoryScanner()
	if got := scanner.Scan(input, chat, true); got.Busy || got.Done {
		t.Fatalf("partial input=%+v", got)
	}
	appendFile(t, input, " request\n\n")
	if got := scanner.Scan(input, chat, true); !got.Busy || got.Done {
		t.Fatalf("complete input=%+v", got)
	}

	appendFile(t, chat, "#### assistant")
	if got := scanner.Scan(input, chat, true); !got.Busy || got.Done {
		t.Fatalf("partial assistant heading=%+v", got)
	}
	appendFile(t, chat, "\nanswer\n")
	if got := scanner.Scan(input, chat, true); got.Busy || !got.Done {
		t.Fatalf("complete assistant heading=%+v", got)
	}
}

func TestAiderHistoryKeepsNewerSubmittedInputBusyOnColdStart(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.history")
	chat := filepath.Join(dir, "chat.md")
	writeFile(t, input, "# new\n+new request\n\n")
	writeFile(t, chat, "#### user\nold request\n\n#### assistant\nold answer\n")
	old := time.Unix(1, 0)
	newer := time.Unix(2, 0)
	if err := os.Chtimes(chat, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(input, newer, newer); err != nil {
		t.Fatal(err)
	}

	if got := NewAiderHistoryScanner().Scan(input, chat, true); !got.Busy || got.Done {
		t.Fatalf("cold-start snapshot=%+v", got)
	}
}

func TestAiderHistoryOpensTurnForPromptBearingChatCommand(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.history")
	chat := filepath.Join(dir, "chat.md")
	writeFile(t, input, "# now\n+/ask explain this code\n\n")
	writeFile(t, chat, "")

	if got := NewAiderHistoryScanner().Scan(input, chat, true); !got.Busy || got.Done {
		t.Fatalf("ask command=%+v", got)
	}
}

func TestAiderHistoryIgnoresArgumentBearingLocalCommands(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.history")
	chat := filepath.Join(dir, "chat.md")
	writeFile(t, input, "# now\n+/help explain config\n\n# now\n+/context src/main.go\n\n")
	writeFile(t, chat, "")

	if got := NewAiderHistoryScanner().Scan(input, chat, true); got.Busy || got.Done {
		t.Fatalf("local commands=%+v", got)
	}
}

func TestAiderHistoryResetsWhenInputHistoryRotates(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.history")
	chat := filepath.Join(dir, "chat.md")
	writeFile(t, input, "# old\n+old request\n\n")
	writeFile(t, chat, "")

	scanner := NewAiderHistoryScanner()
	if got := scanner.Scan(input, chat, true); !got.Busy {
		t.Fatalf("before rotation=%+v", got)
	}

	replacement := filepath.Join(dir, "replacement.history")
	writeFile(t, replacement, "# new\n+/model sonnet\n\n")
	if err := os.Rename(replacement, input); err != nil {
		t.Fatal(err)
	}
	if got := scanner.Scan(input, chat, true); got.Busy || got.Done || got.RuntimeModel != "sonnet" {
		t.Fatalf("after rotation=%+v", got)
	}
}

func TestAiderHistoryResetsWhenInputHistoryTruncates(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.history")
	chat := filepath.Join(dir, "chat.md")
	writeFile(t, input, "# old\n+/model old-model\n\n# old\n+old request\n\n")
	writeFile(t, chat, "")

	scanner := NewAiderHistoryScanner()
	if got := scanner.Scan(input, chat, true); !got.Busy || got.RuntimeModel != "old-model" {
		t.Fatalf("before truncate=%+v", got)
	}
	if err := os.Truncate(input, 0); err != nil {
		t.Fatal(err)
	}
	if got := scanner.Scan(input, chat, true); got.Busy || got.Done || got.RuntimeModel != "" {
		t.Fatalf("after truncate=%+v", got)
	}
}

func TestAiderHistoryFailsClosedWhenRequiredHistoryDisappears(t *testing.T) {
	for _, missing := range []string{"input", "chat"} {
		t.Run(missing, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "input.history")
			chat := filepath.Join(dir, "chat.md")
			writeFile(t, input, "# old\n+/model old-model\n\n# old\n+old request\n\n")
			writeFile(t, chat, "")

			scanner := NewAiderHistoryScanner()
			if got := scanner.Scan(input, chat, true); !got.Busy || got.RuntimeModel != "old-model" {
				t.Fatalf("initial snapshot=%+v", got)
			}
			path := input
			if missing == "chat" {
				path = chat
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if got := scanner.Scan(input, chat, true); got != (AiderHistorySnapshot{}) {
				t.Fatalf("missing history snapshot=%+v", got)
			}

			writeFile(t, input, "# new\n+/model new-model\n\n# new\n+new request\n\n")
			writeFile(t, chat, "")
			if got := scanner.Scan(input, chat, true); !got.Busy || got.Done || got.RuntimeModel != "new-model" {
				t.Fatalf("recreated history snapshot=%+v", got)
			}
		})
	}
}

func TestAiderHistoryFailsClosedWhenHistoryIsAmbiguous(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.history")
	chat := filepath.Join(dir, "chat.md")
	writeFile(t, input, "# now\n+/model sonnet\n\n# now\n+write code\n\n")
	writeFile(t, chat, "#### assistant\ndone\n")

	scanner := NewAiderHistoryScanner()
	if got := scanner.Scan(input, chat, false); got.Busy || got.Done || got.RuntimeModel != "" {
		t.Fatalf("ambiguous snapshot=%+v", got)
	}
}

func TestResolveAiderModelUsesOnlyAttributableEvidence(t *testing.T) {
	tests := []struct {
		name     string
		evidence ModelEvidence
		want     string
	}{
		{
			name: "runtime model wins",
			evidence: ModelEvidence{
				RuntimeModel: "openrouter/deepseek/deepseek-chat", ArgModel: "sonnet",
				HistoryAttributed: true,
			},
			want: "openrouter/deepseek/deepseek-chat",
		},
		{
			name:     "explicit argument wins launch precedence",
			evidence: ModelEvidence{ArgModel: "sonnet", EnvModel: "env-model", ConfigModels: []string{"config-model"}, HistoryAttributed: true},
			want:     "sonnet",
		},
		{
			name:     "argument needs attributable history",
			evidence: ModelEvidence{ArgModel: "sonnet"},
			want:     "unknown",
		},
		{
			name:     "runtime needs attributable history",
			evidence: ModelEvidence{RuntimeModel: "sonnet"},
			want:     "unknown",
		},
		{
			name:     "one effective setting",
			evidence: ModelEvidence{EnvModel: "sonnet", ConfigModels: []string{"sonnet", " sonnet "}, HistoryAttributed: true},
			want:     "sonnet",
		},
		{
			name:     "conflicting effective settings",
			evidence: ModelEvidence{EnvModel: "sonnet", ConfigModels: []string{"deepseek"}, HistoryAttributed: true},
			want:     "unknown",
		},
		{
			name:     "conflicting values without attribution",
			evidence: ModelEvidence{ArgModel: "sonnet", EnvModel: "deepseek", ConfigModels: []string{"opus"}},
			want:     "unknown",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ResolveAiderModel(test.evidence); got != test.want {
				t.Fatalf("model=%q want %q", got, test.want)
			}
		})
	}
}
