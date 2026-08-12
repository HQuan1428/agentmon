package collector

import (
	"path/filepath"

	"agentmon/internal/procscan"
)

func NewDefault(home string, source procscan.Source) *Coordinator {
	return NewCoordinator(source,
		NewClaudeAdapter(filepath.Join(home, ".claude")),
		NewCodexAdapter(filepath.Join(home, ".codex")),
		NewOpenCodeAdapter(home),
		NewAiderAdapter(home),
	)
}
