CREATE SCHEMA app;

CREATE SCHEMA IF NOT EXISTS partman;

CREATE EXTENSION pgcrypto;
CREATE EXTENSION pg_partman SCHEMA partman;

CREATE TYPE app.priority AS ENUM ('low', 'medium', 'high', 'critical');

CREATE DOMAIN app.short_text AS text CHECK (LENGTH(VALUE) <= 255);

CREATE TABLE app.projects (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    owner_id uuid NOT NULL,
    name app.short_text NOT NULL,
    description app.short_text,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_projects PRIMARY KEY (id)
);

CREATE TABLE app.tasks (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL,
    title app.short_text NOT NULL,
    priority app.priority NOT NULL DEFAULT 'medium',
    estimated_hours int8 NOT NULL DEFAULT 0,
    hourly_rate int8 NOT NULL DEFAULT 0,
    estimated_cost int8 NOT NULL GENERATED ALWAYS AS (estimated_hours * hourly_rate) STORED,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_tasks PRIMARY KEY (id)
);

CREATE TABLE app.audit_log (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    action app.short_text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_audit_log PRIMARY KEY (id)
);

CREATE TABLE app.events (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL,
    event_type app.short_text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_events PRIMARY KEY (id, occurred_at)
) PARTITION BY RANGE (occurred_at);

CREATE TABLE app.comments (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    task_id uuid NOT NULL,
    author_id uuid NOT NULL,
    body app.short_text NOT NULL,
    tags app.short_text[] NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_comments PRIMARY KEY (id)
);

CREATE TABLE app.events_2024_q1 PARTITION OF app.events
  FOR VALUES FROM ('2024-01-01') TO ('2024-04-01');
CREATE TABLE app.events_2024_q2 PARTITION OF app.events
  FOR VALUES FROM ('2024-04-01') TO ('2024-07-01');

SELECT partman.create_parent(
  p_parent_table := 'app.events',
  p_control := 'occurred_at',
  p_interval := '1 month',
  p_premake := 3
);

UPDATE partman.part_config
SET retention = '90 days',
    retention_keep_table = false
WHERE parent_table = 'app.events';

ALTER TABLE app.tasks ADD CONSTRAINT fk_tasks_project FOREIGN KEY (project_id) REFERENCES app.projects (id) ON DELETE CASCADE;
ALTER TABLE app.audit_log ADD CONSTRAINT fk_audit_log_project FOREIGN KEY (project_id) REFERENCES app.projects (id) ON DELETE CASCADE;
ALTER TABLE app.events ADD CONSTRAINT fk_events_project FOREIGN KEY (project_id) REFERENCES app.projects (id) ON DELETE CASCADE;
ALTER TABLE app.comments ADD CONSTRAINT fk_comments_task FOREIGN KEY (task_id) REFERENCES app.tasks (id) ON DELETE CASCADE;

ALTER TABLE app.tasks ADD CONSTRAINT uq_tasks_project_title UNIQUE (project_id, title);

ALTER TABLE app.tasks ADD CONSTRAINT ck_tasks_positive_hours CHECK (estimated_hours >= 0);
ALTER TABLE app.audit_log ADD CONSTRAINT ck_metadata_title_type CHECK (metadata ? 'title' AND jsonb_typeof(metadata->'title') = 'string');
ALTER TABLE app.audit_log ADD CONSTRAINT ck_metadata_value_type CHECK (metadata ? 'value' AND jsonb_typeof(metadata->'value') = 'number');

CREATE INDEX idx_tasks_project_id ON app.tasks (project_id);
CREATE INDEX idx_tasks_title ON app.tasks (title text_pattern_ops);
CREATE INDEX idx_audit_log_project ON app.audit_log (project_id, created_at DESC);
CREATE INDEX idx_events_project_time ON app.events (project_id, occurred_at DESC);
CREATE INDEX idx_comments_task ON app.comments (task_id, created_at DESC);

CREATE OR REPLACE FUNCTION app.pgdesign_deny_mutation() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'table % is append-only: UPDATE and DELETE are not allowed', TG_TABLE_NAME;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER deny_mutation BEFORE UPDATE OR DELETE ON app.audit_log FOR EACH ROW EXECUTE FUNCTION app.pgdesign_deny_mutation();

COMMENT ON TABLE app.projects IS 'Top-level projects with row-level security.';
COMMENT ON COLUMN app.projects.owner_id IS 'User who owns this project';
COMMENT ON TABLE app.tasks IS 'Individual tasks within a project.';
COMMENT ON TABLE app.audit_log IS 'Immutable audit trail of project actions.';
COMMENT ON TABLE app.events IS 'Time-series project events, partitioned by quarter.';
COMMENT ON TABLE app.comments IS 'Comments on tasks with tagging support.';

ALTER TABLE app.projects ENABLE ROW LEVEL SECURITY;

CREATE POLICY owner_delete ON app.projects FOR DELETE TO app_user USING (owner_id = current_setting('app.user_id')::uuid);
CREATE POLICY owner_insert ON app.projects FOR INSERT TO app_user WITH CHECK (owner_id = current_setting('app.user_id')::uuid);
CREATE POLICY owner_select ON app.projects FOR SELECT TO app_user USING (owner_id = current_setting('app.user_id')::uuid);
CREATE POLICY owner_update ON app.projects FOR UPDATE TO app_user USING (owner_id = current_setting('app.user_id')::uuid) WITH CHECK (owner_id = current_setting('app.user_id')::uuid);
