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
	"github.com/soasurs/adk/session/message"
)

// Runner coordinates a stateless Agent with a SessionService. It loads
// conversation history from the session, forwards it to the agent, and
// persists every yielded message back to the session.
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
func (r *Runner) Run(ctx context.Context, sessionID int64, userInput string) iter.Seq2[*model.Event, error] {
	return func(yield func(*model.Event, error) bool) {
		sess, err := r.session.GetSession(ctx, sessionID)
		if err != nil {
			yield(nil, err)
			return
		}

		// Load all previous active messages from the session.
		persisted, err := sess.ListMessages(ctx)
		if err != nil {
			yield(nil, err)
			return
		}

		messages := make([]model.Message, 0, len(persisted)+1)
		for _, m := range persisted {
			messages = append(messages, m.ToModel())
		}

		// Append and persist the incoming user message.
		userMsg := model.Message{
			Role:    model.RoleUser,
			Content: userInput,
		}
		if err := r.persistMessage(ctx, sess, userMsg); err != nil {
			yield(nil, err)
			return
		}
		messages = append(messages, userMsg)

		// Run the agent, forwarding every event to the caller.
		// Only complete events (Partial=false) are persisted to the session.
		for event, err := range r.agent.Run(ctx, messages) {
			if err != nil {
				yield(nil, err)
				return
			}
			// Persist only complete messages; partial streaming fragments are
			// forwarded to the caller for real-time display only.
			if !event.Partial {
				if err := r.persistMessage(ctx, sess, event.Message); err != nil {
					yield(nil, err)
					return
				}
			}
			if !yield(event, nil) {
				return
			}
		}
	}
}

// persistMessage assigns a snowflake ID and timestamps, then saves the message
// to the session.
func (r *Runner) persistMessage(ctx context.Context, sess session.Session, msg model.Message) error {
	now := time.Now().UnixMilli()
	m := message.FromModel(msg)
	m.MessageID = r.snowflaker.Generate().Int64()
	m.CreatedAt = now
	m.UpdatedAt = now
	return sess.CreateMessage(ctx, m)
}
