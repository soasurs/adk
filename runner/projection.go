package runner

import (
	"context"
	"fmt"

	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/session"
	sessionevent "github.com/soasurs/adk/session/event"
)

// ProjectionInput contains durable Turn metadata and Event facts to project
// into provider-neutral model context. Events must be in conversation order.
type ProjectionInput struct {
	Turns  []*session.Turn
	Events []*sessionevent.Event
}

// Projector converts durable session facts into context safe to provide to an
// Agent. Implementations must not mutate or persist the input. Runner always
// applies ValidateToolProtocol to the returned context.
type Projector interface {
	Project(ctx context.Context, input ProjectionInput) ([]model.Event, error)
}

// ProjectorFunc adapts a function to Projector.
type ProjectorFunc func(context.Context, ProjectionInput) ([]model.Event, error)

// Project implements Projector.
func (f ProjectorFunc) Project(ctx context.Context, input ProjectionInput) ([]model.Event, error) {
	return f(ctx, input)
}

// NewDefaultProjector returns ADK's durable-Turn context projector. Completed
// and legacy Turns are replayed as recorded. Running Turns are omitted. Failed
// and interrupted Turns retain their safe prefix and gain an ephemeral status
// notice; an unmatched tool call and its suffix are omitted.
func NewDefaultProjector() Projector {
	return defaultProjector{}
}

type defaultProjector struct{}

func (defaultProjector) Project(_ context.Context, input ProjectionInput) ([]model.Event, error) {
	byID := make(map[string]*session.Turn, len(input.Turns))
	for _, turn := range input.Turns {
		if turn != nil {
			byID[turn.ID] = turn
		}
	}

	projected := make([]model.Event, 0, len(input.Events))
	for start := 0; start < len(input.Events); {
		if input.Events[start] == nil {
			start++
			continue
		}
		turnID := input.Events[start].TurnID
		end := start + 1
		for end < len(input.Events) && input.Events[end] != nil && input.Events[end].TurnID == turnID {
			end++
		}
		group := make([]model.Event, 0, end-start)
		for _, event := range input.Events[start:end] {
			if event != nil {
				group = append(group, event.ToModel())
			}
		}
		projected = append(projected, projectTurnEvents(byID[turnID], group)...)
		start = end
	}
	if err := ValidateToolProtocol(projected); err != nil {
		return nil, err
	}
	return projected, nil
}

func projectTurnEvents(turn *session.Turn, events []model.Event) []model.Event {
	// Empty Turn IDs and missing metadata remain compatible with legacy history.
	if turn == nil || turn.Status == session.TurnCompleted {
		return events
	}
	if turn.Status == session.TurnRunning {
		return nil
	}

	projected := events
	issues := InspectToolProtocol(events)
	if len(issues) > 0 {
		for i := range events {
			if events[i].ID == issues[0].EventID {
				projected = events[:i]
				break
			}
		}
	}

	notice := fmt.Sprintf(
		"Previous turn %s (%s). Its incomplete or uncertain output must not be treated as completed; inspect current state before retrying.",
		turn.Status,
		turn.Reason,
	)
	if turn.Failure != nil {
		notice += fmt.Sprintf(" Failure code: %s; stage: %s.", turn.Failure.Code, turn.Failure.Stage)
	}
	projected = append(projected, model.Event{
		SessionID: turn.SessionID,
		TurnID:    turn.ID,
		Author:    "runner",
		Content: model.Content{
			Role:    model.RoleUser,
			Content: notice,
		},
	})
	return projected
}
