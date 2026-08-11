// internal/model/model.go
package model

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"agentmon/internal/collector"
	"agentmon/internal/render"
	"agentmon/internal/sound"
)

const barWidth = 16

// graceDuration is how long a session that just completed stays on screen
// (rendered faint) before it disappears from the view.
const graceDuration = 3 * time.Second

type pollMsg []collector.Session
type animMsg struct{}

type Model struct {
	root     string
	player   *sound.Player
	scanner  *collector.Scanner
	interval time.Duration
	sessions []collector.Session
	doneAt   map[string]time.Time // session/subagent ID -> when it first became done
	nowFn    func() time.Time     // injectable clock (tests override)
	phase    int
	scroll   int
	height   int
	soundOn  bool
	collapse bool
	seeded   bool
}

func New(root string, player *sound.Player, interval time.Duration) Model {
	return Model{
		root:     root,
		player:   player,
		scanner:  collector.NewScanner(),
		interval: interval,
		soundOn:  true,
		doneAt:   map[string]time.Time{},
		nowFn:    time.Now,
	}
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
	firstPoll := !m.seeded
	var evs []Event
	if firstPoll {
		m.seeded = true
	} else {
		evs = DiffEvents(m.sessions, sessions)
	}
	m.sessions = sessions
	m.updateDoneTimes(sessions, firstPoll)
	return m, evs
}

// updateDoneTimes stamps the moment each session/subagent first becomes done
// (so the view can grant it a grace window), clears the stamp when it goes
// active again, and prunes stamps for IDs no longer present. Sessions already
// done on the very first poll are stamped as already-expired so they are
// hidden immediately rather than granted a grace window at startup.
func (m Model) updateDoneTimes(sessions []collector.Session, firstPoll bool) {
	now := m.nowFn()
	seen := map[string]bool{}
	var walk func([]collector.Session)
	walk = func(ss []collector.Session) {
		for _, s := range ss {
			seen[s.ID] = true
			if s.IsDone() {
				if _, ok := m.doneAt[s.ID]; !ok {
					if firstPoll {
						m.doneAt[s.ID] = now.Add(-graceDuration - time.Second)
					} else {
						m.doneAt[s.ID] = now
					}
				}
			} else {
				delete(m.doneAt, s.ID)
			}
			walk(s.Children)
		}
	}
	walk(sessions)
	for id := range m.doneAt {
		if !seen[id] {
			delete(m.doneAt, id)
		}
	}
}

// displayView returns the sessions to render — dropping any done session or
// subagent whose grace window has elapsed — plus the set of IDs still within
// their grace window, which the renderer draws faint.
func (m Model) displayView() ([]collector.Session, map[string]bool) {
	now := m.nowFn()
	dim := map[string]bool{}
	var filter func([]collector.Session) []collector.Session
	filter = func(ss []collector.Session) []collector.Session {
		var out []collector.Session
		for _, s := range ss {
			if s.IsDone() {
				if t, ok := m.doneAt[s.ID]; ok && now.Sub(t) >= graceDuration {
					continue // grace elapsed → hide
				}
				dim[s.ID] = true // still within grace → show faint
			}
			s.Children = filter(s.Children)
			out = append(out, s)
		}
		return out
	}
	return filter(m.sessions), dim
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
			if m.scroll < m.maxScroll() {
				m.scroll++
			}
			return m, nil
		}
	case tea.WindowSizeMsg:
		m.height = msg.Height
		return m, nil
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

// renderLines produces the full header+body output (respecting m.collapse)
// split into individual lines, with no trailing empty line. View and
// maxScroll both build on this so their notion of "lines" always agrees.
func (m Model) renderLines() []string {
	view, dim := m.displayView()
	if m.collapse {
		view = stripChildren(view)
	}
	header := "agentmon — q quit · s sound · c tree · ↑/↓ scroll\n\n"
	var full string
	if len(view) == 0 {
		full = header + "  (no active sessions)\n"
	} else {
		full = header + render.RenderView(view, barWidth, m.phase, dim)
	}
	full = strings.TrimSuffix(full, "\n")
	return strings.Split(full, "\n")
}

// maxScroll returns the largest scroll offset that still shows a full
// viewport of content, based on the same line count View renders.
func (m Model) maxScroll() int {
	if m.height <= 0 {
		return 0
	}
	if ms := len(m.renderLines()) - m.height; ms > 0 {
		return ms
	}
	return 0
}

func (m Model) View() string {
	lines := m.renderLines()
	if m.height <= 0 {
		return strings.Join(lines, "\n") + "\n"
	}

	maxScroll := len(lines) - m.height
	if maxScroll < 0 {
		maxScroll = 0
	}
	eff := m.scroll
	if eff < 0 {
		eff = 0
	} else if eff > maxScroll {
		eff = maxScroll
	}
	end := eff + m.height
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[eff:end], "\n") + "\n"
}

func stripChildren(sessions []collector.Session) []collector.Session {
	out := make([]collector.Session, len(sessions))
	copy(out, sessions)
	for i := range out {
		out[i].Children = nil
	}
	return out
}
