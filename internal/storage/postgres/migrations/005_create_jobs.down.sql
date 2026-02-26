-- +migrate Down
DROP TABLE IF EXISTS jobs;
DROP TYPE IF EXISTS job_status;
