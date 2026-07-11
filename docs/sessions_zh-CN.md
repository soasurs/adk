# 会话与 Event 归档

[English](sessions.md) · [文档索引](../README_zh-CN.md#文档)

`SessionService` 持有持久化会话历史。应用负责选择后端，并提供 session ID 以及
application/user 归属信息。

## 创建和列出 Session

测试和临时运行可以使用内存后端：

```go
svc := memory.NewMemorySessionService()
sess, err := svc.CreateSession(ctx, session.CreateSessionRequest{
    SessionID: "session-123", AppID: "my-app", UserID: "user-123",
})
```

Session 列表按 application 和 user 隔离，支持 offset 分页和稳定排序。默认按创建时间
倒序返回 50 条。

```go
sessions, err := svc.ListSessions(ctx, session.ListSessionsRequest{
    AppID: "my-app", UserID: "user-123", Limit: 20,
    SortBy: session.SessionSortByCreatedAt, SortOrder: session.SortDescending,
})
```

`ListSessions` 不会预加载 Event；`GetCreatedAt` 返回 Unix 毫秒时间戳。

## SQL 存储

Database 后端接收应用持有的 `*sqlx.DB`；应用自行 import 和配置所需 driver。

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

SQLite `:memory:` 测试应使用 `db.SetMaxOpenConns(1)` 或 shared-cache DSN，确保所有
操作访问同一个内存数据库。

Session 直接提供 Event 操作：

```go
events, _ := sess.ListEvents(ctx)
page, _ := sess.GetEvents(ctx, 50, 0)
_ = sess.DeleteEvent(ctx, events[0].EventID)
```

## Event 归档

归档只把旧 Event 标记为 inactive，不会生成摘要或删除数据。摘要的生成、存储、注入
和调度策略由应用负责。

```go
events, _ := sess.ListEvents(ctx)
boundaryEventID := events[4].EventID // 第一个继续保持 active 的 Event
_ = sess.ArchiveEventsBefore(ctx, boundaryEventID)
archived, _ := sess.ListArchivedEvents(ctx)
```

零边界会归档全部 active events；非零边界必须指向 active event，且边界本身仍保持
active。应用需要协调归档与并发运行。Partial Event 不会持久化，因此无需归档。
