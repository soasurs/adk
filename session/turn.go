package session

import "github.com/soasurs/adk/session/event"

// Turn groups the events produced during a single Runner.Run call.
type Turn struct {
	// TurnID is the identifier assigned to the turn by the runner.
	TurnID string
	// Events holds the events in this turn, ordered by created_at ASC with
	// event_id as a stable tiebreaker.
	Events []*event.Event
}

// GroupEventsByTurn groups a pre-sorted slice of events by TurnID, preserving
// the input order within each turn and across turns.
func GroupEventsByTurn(events []*event.Event) []*Turn {
	turns := make([]*Turn, 0)
	var current *Turn
	for _, ev := range events {
		if current == nil || current.TurnID != ev.TurnID {
			current = &Turn{TurnID: ev.TurnID}
			turns = append(turns, current)
		}
		current.Events = append(current.Events, ev)
	}
	return turns
}
