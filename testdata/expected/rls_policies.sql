CREATE SCHEMA secure;

CREATE EXTENSION pgcrypto;

CREATE DOMAIN secure.short_text AS text CHECK (LENGTH(VALUE) <= 255);

CREATE TABLE secure.documents (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    owner_id uuid NOT NULL,
    title secure.short_text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_documents PRIMARY KEY (id)
);

COMMENT ON TABLE secure.documents IS 'User documents with row-level security';

ALTER TABLE secure.documents ENABLE ROW LEVEL SECURITY;

CREATE POLICY owners_delete ON secure.documents FOR DELETE TO app_user USING (owner_id = current_setting('app.user_id')::uuid);
CREATE POLICY owners_insert ON secure.documents FOR INSERT TO app_user WITH CHECK (owner_id = current_setting('app.user_id')::uuid);
CREATE POLICY owners_read ON secure.documents FOR SELECT TO app_user USING (owner_id = current_setting('app.user_id')::uuid);
CREATE POLICY owners_update ON secure.documents FOR UPDATE TO app_user USING (owner_id = current_setting('app.user_id')::uuid) WITH CHECK (owner_id = current_setting('app.user_id')::uuid);
