CREATE SCHEMA app;

CREATE EXTENSION pgcrypto;
CREATE EXTENSION pg_partman;

CREATE TYPE app.priority AS ENUM ('low', 'medium', 'high', 'critical');

CREATE TABLE app.projects (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    owner_id uuid NOT NULL,
    name text NOT NULL,
    description text,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_projects PRIMARY KEY (id)
);

CREATE TABLE app.tasks (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL,
    title text NOT NULL,
    priority app.priority NOT NULL DEFAULT 'medium',
    estimated_hours bigint NOT NULL DEFAULT 0,
    hourly_rate bigint NOT NULL DEFAULT 0,
    estimated_cost bigint NOT NULL GENERATED ALWAYS AS (estimated_hours * hourly_rate) STORED,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_tasks PRIMARY KEY (id)
);

CREATE TABLE app.audit_log (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    action text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_audit_log PRIMARY KEY (id)
);

CREATE TABLE app.events (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_events PRIMARY KEY (id, occurred_at)
) PARTITION BY RANGE (occurred_at);

CREATE TABLE app.comments (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    task_id uuid NOT NULL,
    author_id uuid NOT NULL,
    body text NOT NULL,
    tags text[] NOT NULL,
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
  p_interval := '90 days',
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

ALTER TABLE app.projects ADD CONSTRAINT chk_projects_description CHECK (LENGTH(description) <= 255);
ALTER TABLE app.projects ADD CONSTRAINT chk_projects_name CHECK (LENGTH(name) <= 255);
ALTER TABLE app.tasks ADD CONSTRAINT chk_tasks_title CHECK (LENGTH(title) <= 255);
ALTER TABLE app.tasks ADD CONSTRAINT ck_tasks_positive_hours CHECK (estimated_hours >= 0);
ALTER TABLE app.audit_log ADD CONSTRAINT chk_audit_log_action CHECK (LENGTH(action) <= 255);
ALTER TABLE app.audit_log ADD CONSTRAINT ck_metadata_title_type CHECK (metadata ? 'title' AND jsonb_typeof(metadata->'title') = 'string');
ALTER TABLE app.audit_log ADD CONSTRAINT ck_metadata_value_type CHECK (metadata ? 'value' AND jsonb_typeof(metadata->'value') = 'number');
ALTER TABLE app.events ADD CONSTRAINT chk_events_event_type CHECK (LENGTH(event_type) <= 255);
ALTER TABLE app.comments ADD CONSTRAINT chk_comments_body CHECK (LENGTH(body) <= 255);
ALTER TABLE app.comments ADD CONSTRAINT chk_comments_tags CHECK (LENGTH(tags) <= 255);

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
