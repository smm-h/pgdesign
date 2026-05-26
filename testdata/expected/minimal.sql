CREATE SCHEMA public;

CREATE TABLE public.users (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    email text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_users PRIMARY KEY (id)
);

COMMENT ON TABLE public.users IS 'User accounts';
