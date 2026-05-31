package mongo

import (
	"fmt"
	"iter"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/session"
)

type mongoSession struct {
	agentID   string
	appName   string
	userID    string
	sessionID string

	mu        sync.RWMutex
	events    []*session.Event
	state     map[string]any
	updatedAt time.Time
}

func (s *mongoSession) ID() string { return s.sessionID }

func (s *mongoSession) AppName() string { return s.appName }

func (s *mongoSession) UserID() string { return s.userID }

func (s *mongoSession) AgentID() string { return s.agentID }

func (s *mongoSession) LastUpdateTime() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.updatedAt
}

func (s *mongoSession) State() session.State {
	return &state{mu: &s.mu, state: s.state}
}

func (s *mongoSession) Events() session.Events {
	return events(s.events)
}

func (s *mongoSession) appendEvent(event *session.Event) error {
	if event.Partial {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := updateSessionState(s, event); err != nil {
		return fmt.Errorf("failed to update mongoSession state: %w", err)
	}

	processedEvent := trimTempDeltaState(event)
	s.events = append(s.events, processedEvent)
	s.updatedAt = event.Timestamp
	return nil
}

// State 和 Events 适配器与之前版本一致，略
type state struct {
	mu    *sync.RWMutex
	state map[string]any
}

func (s *state) Get(key string) (any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	val, ok := s.state[key]
	if !ok {
		return nil, session.ErrStateKeyNotExist
	}

	return val, nil
}

func (s *state) Set(key string, val any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.state[key] = val
	return nil
}

func (s *state) All() iter.Seq2[string, any] {
	s.mu.RLock()
	// Create a copy of the state to iterate over it without holding the lock.
	stateCopy := make(map[string]any, len(s.state))
	for k, v := range s.state {
		stateCopy[k] = v
	}
	s.mu.RUnlock()

	return func(yield func(key string, val any) bool) {
		for k, v := range stateCopy {
			if !yield(k, v) {
				return
			}
		}
	}
}

type events []*session.Event

func (e events) All() iter.Seq[*session.Event] {
	return func(yield func(*session.Event) bool) {
		for _, event := range e {
			if !yield(event) {
				return
			}
		}
	}
}

func (e events) Len() int { return len(e) }

func (e events) At(i int) *session.Event {
	if i >= 0 && i < len(e) {
		return e[i]
	}
	return nil
}

// TrimTempDeltaState removes temporary state delta keys from the event.
func trimTempDeltaState(event *session.Event) *session.Event {
	if len(event.Actions.StateDelta) == 0 {
		return event
	}

	// Iterate over the map and build a new one with the keys we want to keep.
	filteredStateDelta := make(map[string]any)
	for key, value := range event.Actions.StateDelta {
		if !strings.HasPrefix(key, session.KeyPrefixTemp) {
			filteredStateDelta[key] = value
		}
	}

	// Replace the old map with the newly filtered one.
	event.Actions.StateDelta = filteredStateDelta
	return event
}

// updateSessionState updates the session state based on the event state delta.
func updateSessionState(sess *mongoSession, event *session.Event) error {
	if event.Actions.StateDelta == nil {
		return nil // Nothing to do
	}

	// Ensure the session state map is initialized
	if sess.state == nil {
		sess.state = make(map[string]any)
	}

	for key, value := range event.Actions.StateDelta {
		if strings.HasPrefix(key, session.KeyPrefixTemp) {
			continue
		}
		sess.state[key] = value
	}
	return nil
}

var (
	_ session.Session = (*mongoSession)(nil)
	_ session.Events  = (*events)(nil)
	_ session.State   = (*state)(nil)
)
