package runner

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"time"

	"github.com/bwmarrin/snowflake"

	"github.com/soasurs/adk/agent"
	snowflaker "github.com/soasurs/adk/internal/snowflake"
	"github.com/soasurs/adk/model"
	"github.com/soasurs/adk/session"
	sessionevent "github.com/soasurs/adk/session/event"
	adktrace "github.com/soasurs/adk/trace"
)

// Runner coordinates a stateless Agent with a SessionService. It loads
// conversation history from the session, forwards it to the agent, and
// persists complete yielded events back to the session. Sessions implementing
// session.TurnStore preserve failed and interrupted turns durably; other
// implementations retain the legacy rollback behavior.
type Runner struct {
	agent             agent.Agent
	session           session.SessionService
	snowflaker        *snowflake.Node
	tracer            adktrace.Tracer
	projector         Projector
	failureClassifier FailureClassifier
}

// New creates a Runner backed by the given agent and session service.
func New(a agent.Agent, s session.SessionService, opts ...Option) (*Runner, error) {
	node, err := snowflaker.New()
	if err != nil {
		return nil, err
	}
	r := &Runner{
		agent:             a,
		session:           s,
		snowflaker:        node,
		tracer:            adktrace.DiscardTracer{},
		projector:         NewDefaultProjector(),
		failureClassifier: DefaultFailureClassifier,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r, nil
}

// Run handles one user turn. It fetches the session history, appends the user
// input, invokes the agent, and yields each produced Event to the caller.
// Complete events (Event.Partial=false) are persisted to the session; partial
// streaming fragments are forwarded to the caller for real-time display but
// are not persisted. Durable Turn stores retain events when execution fails or
// iteration stops early and use a safe projection when constructing later LLM
// context. Other stores remove events created by an incomplete turn.
// The caller iterates the returned sequence and decides whether to continue the
// conversation by calling Run again.
//
// If active history contains assistant tool calls without matching durable
// results, Run yields ErrToolExecutionUnknown before persisting the user input
// or invoking the agent.
//
// userInput must contain the user's input content (via Text or Parts).
// Its Role is always set to RoleUser by the runner.
func (r *Runner) Run(ctx context.Context, sessionID string, userInput model.Content) iter.Seq2[*model.Event, error] {
	return func(yield func(*model.Event, error) bool) {
		info := adktrace.RunInfo{
			RunID:     r.snowflaker.Generate().String(),
			TurnID:    r.snowflaker.Generate().String(),
			SessionID: sessionID,
		}
		ctx = adktrace.ContextWithTracer(ctx, r.tracer)
		ctx = adktrace.ContextWithRunInfo(ctx, info)
		ctx, runSpan := adktrace.Start(ctx, adktrace.Event{
			Kind:      adktrace.KindRunnerRun,
			SessionID: sessionID,
		})
		runEnd := adktrace.Event{Kind: adktrace.KindRunnerRun}.WithRunInfo(info)
		defer func() {
			runSpan.End(ctx, runEnd.WithRunInfo(info))
		}()

		sess, unlock, err := r.getSessionForRun(ctx, sessionID)
		if err != nil {
			runEnd.Err = err
			yield(nil, err)
			return
		}
		if unlock != nil {
			defer unlock()
		}
		info.AppID = sess.GetAppID()
		info.UserID = sess.GetUserID()
		ctx = adktrace.ContextWithRunInfo(ctx, info)
		runEnd = runEnd.WithRunInfo(info)

		turnEventIDs := make([]int64, 0)
		turns, durableTurns := sess.(session.TurnStore)
		if durableTurns && unlock != nil {
			if err := withTurnCleanupContext(ctx, func(cleanupCtx context.Context) error {
				return turns.InterruptRunningTurns(cleanupCtx, session.TurnReasonAbandoned)
			}); err != nil {
				runEnd.Err = err
				yield(nil, err)
				return
			}
		}
		if durableTurns {
			err := turns.BeginTurn(ctx, session.Turn{
				ID:        info.TurnID,
				SessionID: sessionID,
				Status:    session.TurnRunning,
				StartedAt: time.Now().UnixMilli(),
			})
			if err != nil {
				runEnd.Err = err
				yield(nil, err)
				return
			}
		}
		failTurn := func(cause error, stage session.TurnFailureStage) error {
			if durableTurns {
				finalizeErr := withTurnCleanupContext(ctx, func(cleanupCtx context.Context) error {
					return finalizeFailedTurn(
						cleanupCtx,
						turns,
						info.TurnID,
						cause,
						stage,
						r.failureClassifier,
					)
				})
				return errors.Join(cause, finalizeErr)
			}
			rollbackErr := rollbackTurnEvents(ctx, sess, turnEventIDs)
			turnEventIDs = nil
			return errors.Join(cause, rollbackErr)
		}
		interruptTurn := func() error {
			if durableTurns {
				return withTurnCleanupContext(ctx, func(cleanupCtx context.Context) error {
					return turns.FinalizeTurn(cleanupCtx, info.TurnID, session.TurnOutcome{
						Status: session.TurnInterrupted,
						Reason: session.TurnReasonConsumerStopped,
					})
				})
			}
			return rollbackTurnEvents(ctx, sess, turnEventIDs)
		}

		// Load all previous active events from the session.
		loadCtx, loadSpan := adktrace.Start(ctx, adktrace.Event{
			Kind: adktrace.KindSessionLoad,
		})
		persisted, err := sess.ListEvents(loadCtx)
		loadSpan.End(loadCtx, adktrace.Event{
			Kind:       adktrace.KindSessionLoad,
			EventCount: len(persisted),
			Err:        err,
		})
		if err != nil {
			err = failTurn(err, session.TurnFailureStagePersistence)
			runEnd.Err = err
			yield(nil, err)
			return
		}

		var listedTurns []*session.Turn
		if durableTurns {
			listedTurns, err = turns.ListTurns(ctx)
			if err != nil {
				err = failTurn(
					fmt.Errorf("runner: list turns: %w", err),
					session.TurnFailureStagePersistence,
				)
				runEnd.Err = err
				yield(nil, err)
				return
			}
		}
		events, err := r.projector.Project(ctx, ProjectionInput{
			Turns:  listedTurns,
			Events: persisted,
		})
		if err != nil {
			err = failTurn(err, session.TurnFailureStageAgent)
			runEnd.Err = err
			yield(nil, err)
			return
		}
		if protocolErr := ValidateToolProtocol(events); protocolErr != nil {
			err = failTurn(protocolErr, session.TurnFailureStageTool)
			runEnd.Err = err
			yield(nil, err)
			return
		}

		// Append and persist the incoming user event.
		userContent := userInput
		userContent.Role = model.RoleUser
		userEvent, err := r.persistEvent(ctx, sess, model.Event{
			TurnID:  info.TurnID,
			Author:  "user",
			Content: userContent,
		})
		if err != nil {
			err = failTurn(err, session.TurnFailureStagePersistence)
			runEnd.Err = err
			yield(nil, err)
			return
		}
		turnEventIDs = append(turnEventIDs, userEvent.ID)
		events = append(events, userEvent)

		// Run the agent, forwarding every event to the caller.
		// Only complete events (Partial=false) are persisted to the session.
		agentCtx, agentSpan := adktrace.Start(ctx, adktrace.Event{
			Kind:      adktrace.KindAgentRun,
			AgentName: r.agent.Name(),
		})
		agentEnd := adktrace.Event{
			Kind:      adktrace.KindAgentRun,
			AgentName: r.agent.Name(),
		}
		defer func() {
			agentSpan.End(agentCtx, agentEnd)
		}()

		for event, err := range r.agent.Run(agentCtx, events) {
			if err != nil {
				err = failTurn(err, session.TurnFailureStageAgent)
				agentEnd.Err = err
				runEnd.Err = err
				yield(nil, err)
				return
			}
			event.SessionID = sess.GetSessionID()
			event.TurnID = info.TurnID
			// Persist only complete events; partial streaming fragments are
			// forwarded to the caller for real-time display only.
			if !event.Partial {
				persistedEvent, err := r.persistEvent(ctx, sess, *event)
				if err != nil {
					err = failTurn(err, session.TurnFailureStagePersistence)
					agentEnd.Err = err
					runEnd.Err = err
					yield(nil, err)
					return
				}
				turnEventIDs = append(turnEventIDs, persistedEvent.ID)
				event = &persistedEvent
			}
			if !yield(event, nil) {
				agentEnd.StoppedEarly = true
				runEnd.StoppedEarly = true
				if err := interruptTurn(); err != nil {
					agentEnd.Err = err
					runEnd.Err = err
				}
				return
			}
		}
		if durableTurns {
			if err := withTurnCleanupContext(ctx, func(cleanupCtx context.Context) error {
				return turns.FinalizeTurn(cleanupCtx, info.TurnID, session.TurnOutcome{
					Status: session.TurnCompleted,
				})
			}); err != nil {
				agentEnd.Err = err
				runEnd.Err = err
				yield(nil, err)
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
		lockCtx, lockSpan := adktrace.Start(ctx, adktrace.Event{
			Kind:      adktrace.KindRunnerLock,
			SessionID: key.SessionID,
			AppID:     key.AppID,
			UserID:    key.UserID,
		})
		unlock, err := locker.LockRun(lockCtx, key)
		lockSpan.End(lockCtx, adktrace.Event{
			Kind:      adktrace.KindRunnerLock,
			SessionID: key.SessionID,
			AppID:     key.AppID,
			UserID:    key.UserID,
			Err:       err,
		})
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

func rollbackTurnEvents(ctx context.Context, sess session.Session, eventIDs []int64) error {
	ctx = context.WithoutCancel(ctx)
	var rollbackErr error
	for i := len(eventIDs) - 1; i >= 0; i-- {
		if err := sess.DeleteEvent(ctx, eventIDs[i]); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("runner: rollback event %d: %w", eventIDs[i], err))
		}
	}
	return rollbackErr
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
	persistCtx, persistSpan := adktrace.Start(ctx, adktrace.Event{
		Kind:        adktrace.KindEventPersist,
		EventID:     ev.ID,
		EventAuthor: ev.Author,
		EventRole:   ev.Content.Role,
		Partial:     ev.Partial,
	})
	err := sess.CreateEvent(persistCtx, stored)
	persistSpan.End(persistCtx, adktrace.Event{
		Kind:        adktrace.KindEventPersist,
		EventID:     ev.ID,
		EventAuthor: ev.Author,
		EventRole:   ev.Content.Role,
		Partial:     ev.Partial,
		Err:         err,
	})
	return ev, err
}
