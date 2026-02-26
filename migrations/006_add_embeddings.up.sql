-- +migrate Up
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE embeddings (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    message_id UUID,
    content TEXT NOT NULL,
    vector vector(1536),
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_embeddings_session_id ON embeddings(session_id);
CREATE INDEX idx_embeddings_vector ON embeddings USING ivfflat (vector vector_cosine_ops) WITH (lists = 100);
CREATE INDEX idx_embeddings_message_id ON embeddings(message_id);

CREATE TABLE summaries (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    content TEXT NOT NULL,
    token_count INTEGER DEFAULT 0,
    start_message_idx INTEGER,
    end_message_idx INTEGER,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_summaries_session_id ON summaries(session_id);
CREATE INDEX idx_summaries_created_at ON summaries(created_at);

-- +migrate Down
DROP TABLE IF EXISTS summaries;
DROP TABLE IF EXISTS embeddings;
