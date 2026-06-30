package runner

import (
	"context"
	"iter"
	"time"

	"github.com/bwmarrin/snowflake"

	"github.com/soasurs/adk/agent"
	snowflaker "github.com/soasurs/adk/internal/snowflake"
	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/session"
	sessionevent "github.com/soasurs/adk/session/event"
)

// Runner coordinates a stateless Agent with a SessionService. It loads
// conversation history from the session, forwards it to the agent, and
// persists every complete yielded event back to the session.
type Runner struct {
	agent      agent.Agent
	session    session.SessionService
	snowflaker *snowflake.Node
}

// New creates a Runner backed by the given agent and session service.
func New(a agent.Agent, s session.SessionService) (*Runner, error) {
	node, err := snowflaker.New()
	if err != nil {
		return nil, err
	}
	return &Runner{
		agent:      a,
		session:    s,
		snowflaker: node,
	}, nil
}

// Run handles one user turn. It fetches the session history, appends the user
// input, invokes the agent, and yields each produced Event to the caller.
// Complete events (Event.Partial=false) are persisted to the session; partial
// streaming fragments are forwarded to the caller for real-time display but
// are not persisted. The caller iterates the returned sequence and decides
// whether to continue the conversation by calling Run again.
//
// userInput must contain the user's input content (via Text or Parts).
// Its Role is always set to RoleUser by the runner.
func (r *Runner) Run(ctx context.Context, sessionID string, userInput model.Content) iter.Seq2[*model.Event, error] {
	return func(yield func(*model.Event, error) bool) {
		sess, unlock, err := r.getSessionForRun(ctx, sessionID)
		if err != nil {
			yield(nil, err)
			return
		}
		if unlock != nil {
			defer unlock()
		}

		// Load all previous active events from the session.
		persisted, err := sess.ListEvents(ctx)
		if err != nil {
			yield(nil, err)
			return
		}

		events := make([]model.Event, 0, len(persisted)+1)
		for _, ev := range persisted {
			events = append(events, ev.ToModel())
		}

		// Append and persist the incoming user event.
		userContent := userInput
		userContent.Role = model.RoleUser
		userEvent, err := r.persistEvent(ctx, sess, model.Event{
			Author:  "user",
			Content: userContent,
		})
		if err != nil {
			yield(nil, err)
			return
		}
		events = append(events, userEvent)

		// Run the agent, forwarding every event to the caller.
		// Only complete events (Partial=false) are persisted to the session.
		for event, err := range r.agent.Run(ctx, events) {
			if err != nil {
				yield(nil, err)
				return
			}
			// Persist only complete events; partial streaming fragments are
			// forwarded to the caller for real-time display only.
			if !event.Partial {
				persistedEvent, err := r.persistEvent(ctx, sess, *event)
				if err != nil {
					yield(nil, err)
					return
				}
				event = &persistedEvent
			}
			if !yield(event, nil) {
				return
			}
		}
	}
}

func (r *Runner) getSessionForRun(ctx context.Context, sessionID string) (session.Session, func(), error) {
	if locker, ok := r.session.(session.RunScopedLocker); ok {
		return r.getSessionWithScopedLock(ctx, sessionID, locker)
	}

	sess, err := r.session.GetSession(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	if sess == nil {
		return nil, nil, &SessionNotFoundError{SessionID: sessionID}
	}
	return sess, nil, nil
}

func (r *Runner) getSessionWithScopedLock(
	ctx context.Context,
	sessionID string,
	locker session.RunScopedLocker,
) (session.Session, func(), error) {
	for {
		sess, err := r.session.GetSession(ctx, sessionID)
		if err != nil {
			return nil, nil, err
		}
		if sess == nil {
			return nil, nil, &SessionNotFoundError{SessionID: sessionID}
		}

		key := runLockKey(sess)
		unlock, err := locker.LockRun(ctx, key)
		if err != nil {
			return nil, nil, err
		}

		lockedSess, err := r.session.GetSession(ctx, sessionID)
		if err != nil {
			unlock()
			return nil, nil, err
		}
		if lockedSess == nil {
			unlock()
			return nil, nil, &SessionNotFoundError{SessionID: sessionID}
		}
		if lockedKey := runLockKey(lockedSess); lockedKey != key {
			unlock()
			continue
		}
		return lockedSess, unlock, nil
	}
}

func runLockKey(sess session.Session) session.RunLockKey {
	return session.RunLockKey{
		AppID:     sess.GetAppID(),
		UserID:    sess.GetUserID(),
		SessionID: sess.GetSessionID(),
	}
}

// persistEvent assigns a snowflake ID and timestamps, then saves the event
// to the session.
func (r *Runner) persistEvent(ctx context.Context, sess session.Session, ev model.Event) (model.Event, error) {
	now := time.Now().UnixMilli()
	ev.ID = r.snowflaker.Generate().Int64()
	ev.SessionID = sess.GetSessionID()
	ev.CreatedAt = now
	ev.UpdatedAt = now
	stored := sessionevent.FromModel(ev)
	return ev, sess.CreateEvent(ctx, stored)
}
