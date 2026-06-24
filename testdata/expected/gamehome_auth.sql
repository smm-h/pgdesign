CREATE SCHEMA auth;

CREATE EXTENSION pgcrypto;

CREATE TABLE auth.users (
    id text NOT NULL,
    username text,
    display_name text NOT NULL DEFAULT '',
    email text,
    email_verified bool NOT NULL DEFAULT false,
    created_at float8 NOT NULL,
    updated_at float8 NOT NULL,
    CONSTRAINT pk_users PRIMARY KEY (id)
);

CREATE TABLE auth.magic_links (
    token text NOT NULL,
    email text NOT NULL,
    expires_at float8 NOT NULL,
    used bool NOT NULL DEFAULT false,
    CONSTRAINT pk_magic_links PRIMARY KEY (token)
);

CREATE TABLE auth.credentials (
    user_id text NOT NULL,
    password_hash text NOT NULL,
    created_at float8 NOT NULL,
    updated_at float8 NOT NULL,
    CONSTRAINT pk_credentials PRIMARY KEY (user_id)
);

CREATE TABLE auth.sessions (
    id text NOT NULL,
    user_id text NOT NULL,
    created_at float8 NOT NULL,
    last_active float8 NOT NULL,
    expires_at float8 NOT NULL,
    ip text,
    user_agent text,
    CONSTRAINT pk_sessions PRIMARY KEY (id)
);

CREATE TABLE auth.provider_links (
    user_id text NOT NULL,
    provider text NOT NULL,
    provider_sub text NOT NULL,
    provider_email text,
    linked_at float8 NOT NULL,
    CONSTRAINT pk_provider_links PRIMARY KEY (provider, provider_sub)
);

CREATE TABLE auth.email_verifications (
    token text NOT NULL,
    user_id text NOT NULL,
    email text NOT NULL,
    expires_at float8 NOT NULL,
    used bool NOT NULL DEFAULT false,
    CONSTRAINT pk_email_verifications PRIMARY KEY (token)
);

CREATE TABLE auth.password_resets (
    token text NOT NULL,
    user_id text NOT NULL,
    expires_at float8 NOT NULL,
    used bool NOT NULL DEFAULT false,
    CONSTRAINT pk_password_resets PRIMARY KEY (token)
);

CREATE TABLE auth.totp_secrets (
    user_id text NOT NULL,
    secret text NOT NULL,
    verified bool NOT NULL DEFAULT false,
    created_at float8 NOT NULL,
    CONSTRAINT pk_totp_secrets PRIMARY KEY (user_id)
);

CREATE TABLE auth.recovery_codes (
    id int8 NOT NULL,
    user_id text NOT NULL,
    code_hash text NOT NULL,
    used bool NOT NULL DEFAULT false,
    CONSTRAINT pk_recovery_codes PRIMARY KEY (id)
);

CREATE TABLE auth.webauthn_credentials (
    id text NOT NULL,
    user_id text NOT NULL,
    public_key bytea NOT NULL,
    attestation_type text NOT NULL,
    transport text,
    sign_count int4 NOT NULL DEFAULT 0,
    created_at float8 NOT NULL,
    last_used float8,
    CONSTRAINT pk_webauthn_credentials PRIMARY KEY (id)
);

ALTER TABLE auth.credentials ADD CONSTRAINT fk_credentials_user FOREIGN KEY (user_id) REFERENCES auth.users (id) ON DELETE CASCADE;
ALTER TABLE auth.sessions ADD CONSTRAINT fk_sessions_user FOREIGN KEY (user_id) REFERENCES auth.users (id) ON DELETE CASCADE;
ALTER TABLE auth.provider_links ADD CONSTRAINT fk_provider_links_user FOREIGN KEY (user_id) REFERENCES auth.users (id) ON DELETE CASCADE;
ALTER TABLE auth.email_verifications ADD CONSTRAINT fk_email_verifications_user FOREIGN KEY (user_id) REFERENCES auth.users (id) ON DELETE CASCADE;
ALTER TABLE auth.password_resets ADD CONSTRAINT fk_password_resets_user FOREIGN KEY (user_id) REFERENCES auth.users (id) ON DELETE CASCADE;
ALTER TABLE auth.totp_secrets ADD CONSTRAINT fk_totp_secrets_user FOREIGN KEY (user_id) REFERENCES auth.users (id) ON DELETE CASCADE;
ALTER TABLE auth.recovery_codes ADD CONSTRAINT fk_recovery_codes_user FOREIGN KEY (user_id) REFERENCES auth.users (id) ON DELETE CASCADE;
ALTER TABLE auth.webauthn_credentials ADD CONSTRAINT fk_webauthn_credentials_user FOREIGN KEY (user_id) REFERENCES auth.users (id) ON DELETE CASCADE;

CREATE INDEX idx_users_email ON auth.users (email) WHERE email IS NOT NULL;
CREATE UNIQUE INDEX idx_users_username ON auth.users (username) WHERE username IS NOT NULL;
CREATE INDEX idx_magic_links_email ON auth.magic_links (email);
CREATE INDEX idx_credentials_user_id ON auth.credentials (user_id);
CREATE INDEX idx_sessions_expires_at ON auth.sessions (expires_at);
CREATE INDEX idx_sessions_user_id ON auth.sessions (user_id);
CREATE INDEX idx_provider_links_user_id ON auth.provider_links (user_id);
CREATE INDEX idx_email_verifications_user_id ON auth.email_verifications (user_id);
CREATE INDEX idx_password_resets_user_id ON auth.password_resets (user_id);
CREATE INDEX idx_totp_secrets_user_id ON auth.totp_secrets (user_id);
CREATE INDEX idx_recovery_codes_user_id ON auth.recovery_codes (user_id);
CREATE INDEX idx_webauthn_credentials_user_id ON auth.webauthn_credentials (user_id);

COMMENT ON TABLE auth.users IS 'Core user accounts. All authentication methods link back here.';
COMMENT ON COLUMN auth.users.id IS 'Opaque user identifier';
COMMENT ON TABLE auth.magic_links IS 'Passwordless login tokens sent via email.';
COMMENT ON TABLE auth.credentials IS 'Password credentials. One row per user with a password.';
COMMENT ON TABLE auth.sessions IS 'Active login sessions with expiry and device metadata.';
COMMENT ON COLUMN auth.sessions.id IS 'Session token';
COMMENT ON TABLE auth.provider_links IS 'OAuth/OIDC provider links. Composite PK on (provider, provider_sub) to prevent duplicates.';
COMMENT ON TABLE auth.email_verifications IS 'Pending email verification tokens with expiry.';
COMMENT ON TABLE auth.password_resets IS 'Password reset tokens with expiry.';
COMMENT ON TABLE auth.totp_secrets IS 'TOTP (authenticator app) secrets for two-factor authentication.';
COMMENT ON TABLE auth.recovery_codes IS 'One-time-use backup codes for MFA recovery.';
COMMENT ON TABLE auth.webauthn_credentials IS 'FIDO2/WebAuthn passkey credentials.';
COMMENT ON COLUMN auth.webauthn_credentials.id IS 'Credential ID from the authenticator';
