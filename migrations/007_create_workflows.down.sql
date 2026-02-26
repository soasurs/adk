-- +migrate Down
DROP TABLE IF EXISTS agent_messages;
DROP TABLE IF EXISTS workflow_runs;
DROP TABLE IF EXISTS workflows;