# Sessions and Event Archival

[简体中文](sessions_zh-CN.md) · [Documentation index](../README.md#documentation)

`SessionService` owns durable conversation history. Applications choose the
backend and provide session IDs plus application/user ownership metadata.

## Creating and listing sessions

Use the memory backend for tests and ephemeral runs:

```go
svc := memory.NewMemorySessionService()
sess, err := svc.CreateSession(ctx, session.CreateSessionRequest{
    SessionID: "session-123", AppID: "my-app", UserID: "user-123",
})
```

Session listing is scoped to an application and user. It supports offset
pagination and stable ordering; the default is 50 sessions by creation time,
descending.

```go
sessions, err := svc.ListSessions(ctx, session.ListSessionsRequest{
    AppID: "my-app", UserID: "user-123", Limit: 20,
    SortBy: session.SessionSortByCreatedAt, SortOrder: session.SortDescending,
})
```

`ListSessions` does not preload events. `GetCreatedAt` returns Unix milliseconds.

## SQL storage

The database backend accepts an application-owned `*sqlx.DB`. Applications
import and configure their chosen driver.

```go
// SQLite
db, err := sqlx.Connect("sqlite3", "sessions.db")
if err := database.InitSchema(ctx, db); err != nil { /* handle */ }
svc, err := database.NewDatabaseSessionService(db)

// PostgreSQL
db, err := sqlx.Connect("pgx", os.Getenv("ADK_POSTGRES_DSN"))
if err := database.InitSchema(ctx, db); err != nil { /* handle */ }
svc, err := database.NewDatabaseSessionService(db)
```

For SQLite `:memory:` tests, use `db.SetMaxOpenConns(1)` or a shared-cache DSN
so every operation reaches the same in-memory database.

Sessions expose event operations directly:

```go
events, _ := sess.ListEvents(ctx)
page, _ := sess.GetEvents(ctx, 50, 0)
_ = sess.DeleteEvent(ctx, events[0].EventID)
```

## Event archival

Archival marks old events inactive without summarizing or deleting them.
Applications own summary generation, storage, injection, and scheduling policy.

```go
events, _ := sess.ListEvents(ctx)
boundaryEventID := events[4].EventID // first event to keep active
_ = sess.ArchiveEventsBefore(ctx, boundaryEventID)
archived, _ := sess.ListArchivedEvents(ctx)
```

A zero boundary archives every active event. A non-zero boundary must refer to
an active event and remains active itself. Coordinate archival with concurrent
runs at the application level. Partial events are never persisted and therefore
never require archival.
