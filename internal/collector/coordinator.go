package collector

import (
	"fmt"
	"sort"
	"strings"

	"agentmon/internal/procscan"
)

type Adapter interface {
	Agent() Agent
	Discover(procscan.Snapshot) ([]Session, error)
}

type AdapterError struct {
	Agent Agent
	Err   error
}

type Collection struct {
	Sessions []Session
	Errors   []AdapterError
}

type Coordinator struct {
	source   procscan.Source
	adapters []Adapter
}

func NewCoordinator(source procscan.Source, adapters ...Adapter) *Coordinator {
	return &Coordinator{source: source, adapters: adapters}
}

func (c *Coordinator) Collect() Collection {
	snapshot, err := c.source.Snapshot()
	if err != nil {
		return Collection{Errors: []AdapterError{{Err: err}}}
	}

	collection := Collection{}
	seen := make(map[string]struct{})
	for _, adapter := range c.adapters {
		agent, rows, err := safeDiscover(adapter, snapshot)
		if err != nil {
			collection.Errors = append(collection.Errors, AdapterError{Agent: agent, Err: err})
			continue
		}
		for _, row := range rows {
			row, errors, ok := filterSession(agent, row, seen)
			for _, err := range errors {
				collection.Errors = append(collection.Errors, AdapterError{Agent: agent, Err: err})
			}
			if ok {
				collection.Sessions = append(collection.Sessions, row)
			}
		}
	}

	sort.SliceStable(collection.Sessions, func(i, j int) bool {
		left, right := collection.Sessions[i], collection.Sessions[j]
		if left.Project != right.Project {
			return left.Project < right.Project
		}
		if left.Agent.Rank() != right.Agent.Rank() {
			return left.Agent.Rank() < right.Agent.Rank()
		}
		if left.IsDone() != right.IsDone() {
			return !left.IsDone()
		}
		return left.Name < right.Name
	})

	return collection
}

func filterSession(agent Agent, session Session, seen map[string]struct{}) (Session, []error, bool) {
	if err := validateSession(agent, session); err != nil {
		return Session{}, []error{err}, false
	}
	if _, ok := seen[session.ID]; ok {
		return Session{}, []error{fmt.Errorf("duplicate session ID %q", session.ID)}, false
	}
	seen[session.ID] = struct{}{}

	children := make([]Session, 0, len(session.Children))
	var errors []error
	for _, child := range session.Children {
		child, childErrors, ok := filterSession(agent, child, seen)
		errors = append(errors, childErrors...)
		if ok {
			children = append(children, child)
		}
	}
	session.Children = children
	return session, errors, true
}

func safeDiscover(adapter Adapter, snapshot procscan.Snapshot) (agent Agent, rows []Session, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			rows = nil
			err = fmt.Errorf("adapter panic: %v", recovered)
		}
	}()
	agent = adapter.Agent()
	rows, err = adapter.Discover(snapshot)
	return agent, rows, err
}

func validateSession(agent Agent, session Session) error {
	namespace := strings.ToLower(string(agent)) + ":"
	if session.Agent != agent {
		return fmt.Errorf("session %q has agent %q, want %q", session.ID, session.Agent, agent)
	}
	if !strings.HasPrefix(session.ID, namespace) || len(session.ID) == len(namespace) {
		return fmt.Errorf("session ID %q is outside %q namespace", session.ID, namespace)
	}
	return nil
}
