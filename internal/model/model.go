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
	source    SessionSource
	player    *sound.Player
	interval  time.Duration
	sessions  []collector.Session
	exiting   map[string]exitEntry // global ID -> last snapshot of a session whose process went away
	childDone map[string]time.Time // subagent global ID -> when it finished (fades out after grace)
	nowFn     func() time.Time     // injectable clock (tests override)
	phase     int
	scroll    int
	width     int
	height    int
	soundOn   bool
	collapse  bool
	seeded    bool
}

func New(source SessionSource, player *sound.Player, interval time.Duration) Model {
	return Model{
		source:    source,
		player:    player,
		interval:  interval,
		soundOn:   true,
		exiting:   map[string]exitEntry{},
		childDone: map[string]time.Time{},
		nowFn:     time.Now,
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

type exitEntry struct {
	sess collector.Session
	at   time.Time
}

func (m Model) applyPoll(sessions []collector.Session) (Model, []Event) {
	now := m.nowFn()
	curIDs := topLevelIDs(sessions)
	var evs []Event
	if !m.seeded {
		m.seeded = true // first poll only seeds; no chime, no exit ghosts
	} else {
		evs = DiffEvents(m.sessions, sessions)
		// A top-level session present last poll but gone now has exited.
		for _, ps := range m.sessions {
			id := sessionID(ps, "")
			if !curIDs[id] {
				if _, ok := m.exiting[id]; !ok {
					m.exiting[id] = exitEntry{sess: ps, at: now}
				}
			}
		}
	}
	// Drop ghosts that reappeared or whose 3s window elapsed.
	for id, e := range m.exiting {
		if curIDs[id] || now.Sub(e.at) >= graceDuration {
			delete(m.exiting, id)
		}
	}
	m.updateChildDone(sessions, now)
	m.sessions = sessions
	return m, evs
}

// childFinished reports a subagent that has stopped running (its task result
// arrived) — the trigger to fade it out.
func childFinished(c collector.Session) bool {
	st := render.StateOf(c)
	return st == render.StateIdle || st == render.StateDone
}

// updateChildDone stamps when each subagent first finished, clears the stamp if
// it goes active again, and prunes subagents no longer present.
func (m Model) updateChildDone(sessions []collector.Session, now time.Time) {
	seen := map[string]bool{}
	for _, s := range sessions {
		for _, c := range s.Children {
			id := sessionID(c, s.Agent)
			seen[id] = true
			if childFinished(c) {
				if _, ok := m.childDone[id]; !ok {
					m.childDone[id] = now
				}
			} else {
				delete(m.childDone, id)
			}
		}
	}
	for id := range m.childDone {
		if !seen[id] {
			delete(m.childDone, id)
		}
	}
}

func topLevelIDs(ss []collector.Session) map[string]bool {
	ids := map[string]bool{}
	for _, s := range ss {
		ids[sessionID(s, "")] = true
	}
	return ids
}

// displayView returns everything to render: all live sessions (never hidden)
// plus EXIT ghosts still inside their 3s window, marked Exited and drawn faint.
func (m Model) displayView() ([]collector.Session, map[string]bool) {
	now := m.nowFn()
	dim := map[string]bool{}
	var out []collector.Session
	for _, s := range m.sessions {
		s.Children = m.filterChildren(s, now, dim)
		out = append(out, s)
	}
	for _, e := range m.exiting {
		if now.Sub(e.at) >= graceDuration {
			continue
		}
		g := e.sess
		g.Exited = true
		g.Children = nil
		out = append(out, g)
		dim[g.ID] = true
	}
	return out, dim
}

// filterChildren drops subagents whose fade-out window elapsed and dims those
// still fading; running subagents pass through untouched.
func (m Model) filterChildren(s collector.Session, now time.Time, dim map[string]bool) []collector.Session {
	var out []collector.Session
	for _, c := range s.Children {
		if childFinished(c) {
			if t, ok := m.childDone[sessionID(c, s.Agent)]; ok && now.Sub(t) >= graceDuration {
				continue // faded out
			}
			dim[c.ID] = true
		}
		out = append(out, c)
	}
	return out
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
	return view, render.BodyLines(view, m.phase, dim, m.width)
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
