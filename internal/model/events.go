package model

import "agentmon/internal/collector"

type EventKind int

const (
	DoneEvent EventKind = iota
	ApprovalEvent
)

type Event struct {
	Kind      EventKind
	SessionID string
}

type flatState struct {
	done    bool
	blocked bool
}

func flatten(sessions []collector.Session, into map[string]flatState) {
	for _, s := range sessions {
		into[sessionID(s)] = flatState{done: s.IsDone(), blocked: s.Blocked}
		if len(s.Children) > 0 {
			flatten(s.Children, into)
		}
	}
}

func DiffEvents(prev, cur []collector.Session) []Event {
	prevMap := map[string]flatState{}
	curMap := map[string]flatState{}
	flatten(prev, prevMap)
	flatten(cur, curMap)

	var evs []Event
	for _, s := range flattenOrder(cur) {
		p := prevMap[s] // zero value if absent: {false,false}
		c := curMap[s]
		if !p.done && c.done {
			evs = append(evs, Event{Kind: DoneEvent, SessionID: s})
		}
		if !p.blocked && c.blocked {
			evs = append(evs, Event{Kind: ApprovalEvent, SessionID: s})
		}
	}
	return evs
}

func sessionID(s collector.Session) string {
	if s.Agent == "" {
		return s.ID
	}
	native := s.NativeID
	if native == "" {
		native = s.ID
	}
	return collector.GlobalID(s.Agent, native)
}

// flattenOrder returns IDs in a stable top-down, children-after-parent order.
func flattenOrder(sessions []collector.Session) []string {
	var ids []string
	var walk func([]collector.Session)
	walk = func(ss []collector.Session) {
		for _, s := range ss {
			ids = append(ids, sessionID(s))
			walk(s.Children)
		}
	}
	walk(sessions)
	return ids
}
