// main.go
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"agentmon/internal/collector"
	"agentmon/internal/model"
	"agentmon/internal/procscan"
	"agentmon/internal/render"
	"agentmon/internal/sound"
)

func main() {
	noSound := flag.Bool("no-sound", false, "disable chimes")
	testSound := flag.Bool("test-sound", false, "play both chimes and report audio status, then exit")
	interval := flag.Duration("interval", time.Second, "poll interval")
	version := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *version {
		fmt.Println("amo", render.Version)
		return
	}

	if *testSound {
		runSoundTest()
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot resolve home dir:", err)
		os.Exit(1)
	}
	var player *sound.Player
	if !*noSound {
		player = sound.NewPlayer()
	}

	m := model.New(collector.NewDefault(home, procscan.NewProcFS()), player, *interval)
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "amo error:", err)
		os.Exit(1)
	}
}

// runSoundTest plays both chimes outside the alt-screen TUI and reports
// whether audio actually initialized — a quick way to tell "chimes are silent
// because audio is unavailable" apart from "the chime never triggered".
func runSoundTest() {
	p := sound.NewPlayer()
	fmt.Println("PULSE_SERVER =", os.Getenv("PULSE_SERVER"))
	fmt.Println("audio enabled =", p.Enabled())
	if !p.Enabled() {
		fmt.Println("→ No reachable PulseAudio server, so every chime is silent.")
		fmt.Println("  On WSL2, WSLg normally sets PULSE_SERVER; check it is present and audio works.")
		return
	}
	fmt.Println("playing DONE chime (rising E5→A5) …")
	p.Play(sound.ChimeDone)
	time.Sleep(1200 * time.Millisecond)
	fmt.Println("playing APPROVAL chime (A5–A5) …")
	p.Play(sound.ChimeApproval)
	time.Sleep(1200 * time.Millisecond)
	fmt.Println("done — did you hear two soft chimes?")
}
