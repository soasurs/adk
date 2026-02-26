-- +migrate Up
CREATE TYPE job_status AS ENUM ('pending', 'processing', 'completed', 'failed');

CREATE TABLE jobs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    queue_name VARCHAR(100) NOT NULL,
    payload JSONB NOT NULL,
    status job_status NOT NULL DEFAULT 'pending',
    priority INTEGER DEFAULT 0,
    max_attempts INTEGER DEFAULT 3,
    attempts INTEGER DEFAULT 0,
    error TEXT,
    scheduled_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_jobs_queue_status ON jobs(queue_name, status);
CREATE INDEX idx_jobs_scheduled_at ON jobs(scheduled_at);
CREATE INDEX idx_jobs_priority ON jobs(priority DESC);

-- +migrate Down
DROP TABLE IF EXISTS jobs;
DROP TYPE IF EXISTS job_status;
