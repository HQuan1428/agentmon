// internal/model/model.go
package model

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"agentmon/internal/collector"
	"agentmon/internal/render"
	"agentmon/internal/sound"
)

const barWidth = 16

type pollMsg []collector.Session
type animMsg struct{}

type Model struct {
	root     string
	player   *sound.Player
	scanner  *collector.Scanner
	interval time.Duration
	sessions []collector.Session
	phase    int
	scroll   int
	soundOn  bool
	collapse bool
}

func New(root string, player *sound.Player, interval time.Duration) Model {
	return Model{root: root, player: player, scanner: collector.NewScanner(), interval: interval, soundOn: true}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.pollCmd(), m.animCmd())
}

func (m Model) pollCmd() tea.Cmd {
	return func() tea.Msg {
		sessions, _ := collector.Collect(m.root, m.scanner)
		return pollMsg(sessions)
	}
}

func (m Model) animCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return animMsg{} })
}

func (m Model) scheduleNextPoll() tea.Cmd {
	return tea.Tick(m.interval, func(time.Time) tea.Msg { return pollTickMsg{} })
}

type pollTickMsg struct{}

func (m Model) applyPoll(sessions []collector.Session) (Model, []Event) {
	evs := DiffEvents(m.sessions, sessions)
	m.sessions = sessions
	return m, evs
}

func (m Model) toggleSound() Model { m.soundOn = !m.soundOn; return m }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "s":
			return m.toggleSound(), nil
		case "c":
			m.collapse = !m.collapse
			return m, nil
		case "up":
			if m.scroll > 0 {
				m.scroll--
			}
			return m, nil
		case "down":
			m.scroll++
			return m, nil
		}
	case pollMsg:
		m2, evs := m.applyPoll([]collector.Session(msg))
		if m2.soundOn && m2.player != nil {
			for _, e := range evs {
				switch e.Kind {
				case DoneEvent:
					m2.player.Play(sound.ChimeDone)
				case ApprovalEvent:
					m2.player.Play(sound.ChimeApproval)
				}
			}
		}
		return m2, m2.scheduleNextPoll()
	case pollTickMsg:
		return m, m.pollCmd()
	case animMsg:
		m.phase++
		return m, m.animCmd()
	}
	return m, nil
}

func (m Model) View() string {
	view := m.sessions
	if m.collapse {
		view = stripChildren(view)
	}
	header := "agentmon — q quit · s sound · c tree · ↑/↓ scroll\n\n"
	if len(view) == 0 {
		return header + "  (no live sessions)\n"
	}
	return header + render.RenderView(view, barWidth, m.phase)
}

func stripChildren(sessions []collector.Session) []collector.Session {
	out := make([]collector.Session, len(sessions))
	copy(out, sessions)
	for i := range out {
		out[i].Children = nil
	}
	return out
}
