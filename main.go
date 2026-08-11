// main.go
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"agentmon/internal/model"
	"agentmon/internal/sound"
)

func main() {
	noSound := flag.Bool("no-sound", false, "disable chimes")
	interval := flag.Duration("interval", time.Second, "poll interval")
	flag.Parse()

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot resolve home dir:", err)
		os.Exit(1)
	}
	root := filepath.Join(home, ".claude")

	var player *sound.Player
	if !*noSound {
		player = sound.NewPlayer()
	}

	m := model.New(root, player, *interval)
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "agentmon error:", err)
		os.Exit(1)
	}
}
