// internal/model/model.go
package model

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"agentmon/internal/collector"
	"agentmon/internal/render"
	"agentmon/internal/sound"
)

// graceDuration is how long a session that just completed stays on screen
// (rendered faint) before it disappears from the view.
const graceDuration = 3 * time.Second

type pollMsg []collector.Session
type animMsg struct{}

type SessionSource interface {
	Collect() collector.Collection
}

type Model struct {
	source   SessionSource
	player   *sound.Player
	interval time.Duration
	sessions []collector.Session
	doneAt   map[string]time.Time // session/subagent ID -> when it first became done
	nowFn    func() time.Time     // injectable clock (tests override)
	phase    int
	scroll   int
	width    int
	height   int
	soundOn  bool
	collapse bool
	seeded   bool
}

func New(source SessionSource, player *sound.Player, interval time.Duration) Model {
	return Model{
		source:   source,
		player:   player,
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
		return pollMsg(m.source.Collect().Sessions)
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
		m.width = msg.Width
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

// bodyLines returns the display session tree (collapse applied) and the
// rendered table body lines. View and maxScroll share it so their line
// counts agree. An empty tree yields nil lines (the empty-state hero).
func (m Model) bodyLines() ([]collector.Session, []string) {
	view, dim := m.displayView()
	if m.collapse {
		view = stripChildren(view)
	}
	if len(view) == 0 {
		return view, nil
	}
	return view, render.BodyLines(view, m.phase, dim)
}

// maxScroll is the largest scroll offset that still fills the body viewport.
func (m Model) maxScroll() int {
	_, lines := m.bodyLines()
	budget := render.BodyBudget(m.height)
	if budget <= 0 {
		return 0
	}
	if ms := len(lines) - budget; ms > 0 {
		return ms
	}
	return 0
}

func (m Model) View() string {
	view, lines := m.bodyLines()
	counts := render.CountSessions(view)
	return render.Compose(m.width, m.height, counts, m.soundOn, lines, m.scroll, len(view) == 0)
}

func stripChildren(sessions []collector.Session) []collector.Session {
	out := make([]collector.Session, len(sessions))
	copy(out, sessions)
	for i := range out {
		out[i].Children = nil
	}
	return out
}
