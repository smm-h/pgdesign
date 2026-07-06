CREATE SCHEMA game;

CREATE SCHEMA IF NOT EXISTS partman;

CREATE EXTENSION pg_partman SCHEMA partman;

CREATE TYPE game.friendship_status AS ENUM ('pending', 'accepted', 'blocked');
CREATE TYPE game.generation_status AS ENUM ('pending', 'running', 'completed', 'failed');
CREATE TYPE game.session_status AS ENUM ('waiting', 'active', 'finished');
CREATE TYPE game.experiment_status AS ENUM ('draft', 'active', 'paused', 'completed');
CREATE TYPE game.report_status AS ENUM ('open', 'reviewing', 'resolved', 'dismissed');
CREATE TYPE game.moderation_action_type AS ENUM ('warn', 'mute', 'ban', 'unban', 'delete_content');
CREATE TYPE game.report_target_type AS ENUM ('player', 'game', 'comment', 'chat_message');
CREATE TYPE game.session_role AS ENUM ('host', 'player', 'spectator');
CREATE TYPE game.currency_transaction_type AS ENUM ('earn', 'spend', 'refund', 'grant', 'revoke');
CREATE TYPE game.notification_type AS ENUM ('friend_request', 'game_like', 'achievement', 'moderation', 'system');
CREATE TYPE game.server_type AS ENUM ('beta', 'stable');
CREATE TYPE game.server_status AS ENUM ('starting', 'ready', 'draining', 'stopped');

CREATE TABLE game.players (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    auth_id text,
    name text NOT NULL,
    display_name text NOT NULL,
    is_anonymous bool NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    is_ai bool NOT NULL DEFAULT false,
    ai_model text,
    CONSTRAINT pk_players PRIMARY KEY (id)
);

CREATE TABLE game.server_registry (
    id text NOT NULL,
    host text NOT NULL,
    port int4 NOT NULL,
    version text NOT NULL,
    server_type game.server_type NOT NULL,
    status game.server_status NOT NULL,
    max_sessions int4 NOT NULL,
    active_sessions int4 NOT NULL DEFAULT 0,
    last_heartbeat timestamptz NOT NULL,
    registered_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_server_registry PRIMARY KEY (id)
);

CREATE TABLE game.experiment (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    name text NOT NULL,
    description text,
    variants jsonb NOT NULL DEFAULT '{}'::jsonb,
    status game.experiment_status NOT NULL,
    allocation_pct int4 NOT NULL DEFAULT 100,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_experiment PRIMARY KEY (id)
);

CREATE TABLE game.achievement_def (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    name text NOT NULL,
    description text NOT NULL,
    icon text,
    criteria jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_achievement_def PRIMARY KEY (id)
);

CREATE TABLE game.friendships (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    player_a uuid NOT NULL,
    player_b uuid NOT NULL,
    status game.friendship_status NOT NULL,
    initiated_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_friendships PRIMARY KEY (id)
);

CREATE TABLE game.game_catalog (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    title text NOT NULL,
    description text,
    tags jsonb NOT NULL DEFAULT '[]'::jsonb,
    creator uuid,
    ai_generated bool NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    generation_model text,
    generation_prompt text,
    CONSTRAINT pk_game_catalog PRIMARY KEY (id)
);

CREATE TABLE game.chat_messages (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    sender_id uuid NOT NULL,
    channel text NOT NULL,
    body text NOT NULL,
    metadata jsonb,
    sent_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_chat_messages PRIMARY KEY (id, sent_at)
) PARTITION BY RANGE (sent_at);

CREATE TABLE game.player_privacy_settings (
    player_id uuid NOT NULL,
    public_profile bool NOT NULL DEFAULT false,
    chat_enabled bool NOT NULL DEFAULT false,
    friends_enabled bool NOT NULL DEFAULT false,
    leaderboard_visible bool NOT NULL DEFAULT true,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_player_privacy_settings PRIMARY KEY (player_id)
);

CREATE TABLE game.player_age_verification (
    player_id uuid NOT NULL,
    birth_date date NOT NULL,
    is_minor bool NOT NULL DEFAULT false,
    parental_consent_required bool NOT NULL DEFAULT false,
    parental_consent_given bool NOT NULL DEFAULT false,
    parent_email text,
    consent_token text,
    consent_token_expires_at timestamptz,
    consent_given_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_player_age_verification PRIMARY KEY (player_id)
);

CREATE TABLE game.player_profile (
    player_id uuid NOT NULL,
    bio text,
    avatar_url text,
    status_text text,
    level int4 NOT NULL DEFAULT 1,
    xp int8 NOT NULL DEFAULT 0,
    CONSTRAINT pk_player_profile PRIMARY KEY (player_id)
);

CREATE TABLE game.player_setting (
    player_id uuid NOT NULL,
    "key" text NOT NULL,
    value text NOT NULL,
    CONSTRAINT pk_player_setting PRIMARY KEY (player_id, "key")
);

CREATE TABLE game.follow (
    follower_id uuid NOT NULL,
    followed_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_follow PRIMARY KEY (follower_id, followed_id)
);

CREATE TABLE game.report (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    reporter_id uuid NOT NULL,
    target_type game.report_target_type NOT NULL,
    target_id uuid NOT NULL,
    reason text NOT NULL,
    status game.report_status NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_report PRIMARY KEY (id)
);

CREATE TABLE game.currency_balance (
    player_id uuid NOT NULL,
    balance int8 NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_currency_balance PRIMARY KEY (player_id)
);

CREATE TABLE game.currency_transaction (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    player_id uuid NOT NULL,
    amount int8 NOT NULL DEFAULT 0,
    "type" game.currency_transaction_type NOT NULL,
    reason text NOT NULL,
    reference_type text,
    reference_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_currency_transaction PRIMARY KEY (id)
);

CREATE TABLE game.notification (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    player_id uuid NOT NULL,
    "type" game.notification_type NOT NULL,
    title text NOT NULL,
    body text NOT NULL,
    data jsonb,
    read bool NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_notification PRIMARY KEY (id)
);

CREATE TABLE game.api_keys (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    player_id uuid NOT NULL,
    key_hash text NOT NULL,
    name text NOT NULL,
    scopes jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    revoked_at timestamptz,
    CONSTRAINT pk_api_keys PRIMARY KEY (id)
);

CREATE TABLE game.experiment_assignment (
    experiment_id uuid NOT NULL,
    player_id uuid NOT NULL,
    variant text NOT NULL,
    assigned_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_experiment_assignment PRIMARY KEY (experiment_id, player_id)
);

CREATE TABLE game.platform_leaderboards (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    game_id uuid NOT NULL,
    player_id uuid NOT NULL,
    score int8 NOT NULL DEFAULT 0,
    metadata jsonb,
    achieved_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_platform_leaderboards PRIMARY KEY (id)
);

CREATE TABLE game.game_version (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    game_id uuid NOT NULL,
    version_number text NOT NULL,
    bundle_path text NOT NULL,
    bundle_size int8 NOT NULL DEFAULT 0,
    changelog text,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_game_version PRIMARY KEY (id)
);

CREATE TABLE game.game_fork (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    parent_game_id uuid NOT NULL,
    child_game_id uuid NOT NULL,
    forked_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_game_fork PRIMARY KEY (id)
);

CREATE TABLE game.game_like (
    player_id uuid NOT NULL,
    game_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_game_like PRIMARY KEY (player_id, game_id)
);

CREATE TABLE game.game_comment (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    game_id uuid NOT NULL,
    player_id uuid NOT NULL,
    body text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_game_comment PRIMARY KEY (id)
);

CREATE TABLE game.generation_job (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    requested_by uuid NOT NULL,
    game_id uuid,
    prompt text NOT NULL,
    status game.generation_status NOT NULL,
    model text NOT NULL,
    cost_cents int4 NOT NULL DEFAULT 0,
    progress int4 NOT NULL DEFAULT 0,
    error_message text,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CONSTRAINT pk_generation_job PRIMARY KEY (id)
);

CREATE TABLE game.session (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    game_id uuid NOT NULL,
    status game.session_status NOT NULL,
    room_code text NOT NULL,
    max_players int4 NOT NULL,
    config jsonb NOT NULL DEFAULT '{}'::jsonb,
    result jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    ended_at timestamptz,
    server_id text,
    CONSTRAINT pk_session PRIMARY KEY (id)
);

CREATE TABLE game.achievement (
    player_id uuid NOT NULL,
    achievement_def_id uuid NOT NULL,
    game_id uuid,
    earned_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_achievement PRIMARY KEY (player_id, achievement_def_id)
);

CREATE TABLE game.moderation_action (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    report_id uuid NOT NULL,
    action_type game.moderation_action_type NOT NULL,
    moderator_note text,
    acted_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_moderation_action PRIMARY KEY (id)
);

CREATE TABLE game.game_play (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    game_id uuid NOT NULL,
    player_id uuid NOT NULL,
    version_id uuid,
    duration_seconds int4,
    completed bool NOT NULL DEFAULT false,
    score int8 DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_game_play PRIMARY KEY (id)
);

CREATE TABLE game.session_participant (
    session_id uuid NOT NULL,
    player_id uuid NOT NULL,
    role game.session_role NOT NULL,
    joined_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_session_participant PRIMARY KEY (session_id, player_id)
);

CREATE TABLE game.action_log (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL,
    player_id uuid NOT NULL,
    action jsonb NOT NULL DEFAULT '{}'::jsonb,
    sequence_num int8 NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_action_log PRIMARY KEY (id)
);

CREATE TABLE game.state_snapshot (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL,
    state jsonb NOT NULL DEFAULT '{}'::jsonb,
    action_sequence_num int8 NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pk_state_snapshot PRIMARY KEY (id)
);

SELECT partman.create_parent(
  p_parent_table := 'game.chat_messages',
  p_control := 'sent_at',
  p_interval := '1 month',
  p_premake := 3
);

UPDATE partman.part_config
SET retention = '90 days',
    retention_keep_table = false
WHERE parent_table = 'game.chat_messages';

ALTER TABLE game.players ADD CONSTRAINT fk_players_auth FOREIGN KEY (auth_id) REFERENCES auth.users (id) ON DELETE SET NULL;
ALTER TABLE game.friendships ADD CONSTRAINT fk_friendships_initiated_by FOREIGN KEY (initiated_by) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.friendships ADD CONSTRAINT fk_friendships_player_a FOREIGN KEY (player_a) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.friendships ADD CONSTRAINT fk_friendships_player_b FOREIGN KEY (player_b) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.game_catalog ADD CONSTRAINT fk_game_catalog_creator FOREIGN KEY (creator) REFERENCES game.players (id) ON DELETE SET NULL;
ALTER TABLE game.chat_messages ADD CONSTRAINT fk_chat_messages_sender FOREIGN KEY (sender_id) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.player_privacy_settings ADD CONSTRAINT fk_privacy_settings_player FOREIGN KEY (player_id) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.player_age_verification ADD CONSTRAINT fk_age_verification_player FOREIGN KEY (player_id) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.player_profile ADD CONSTRAINT fk_player_profile_player FOREIGN KEY (player_id) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.player_setting ADD CONSTRAINT fk_player_setting_player FOREIGN KEY (player_id) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.follow ADD CONSTRAINT fk_follow_followed FOREIGN KEY (followed_id) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.follow ADD CONSTRAINT fk_follow_follower FOREIGN KEY (follower_id) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.report ADD CONSTRAINT fk_report_reporter FOREIGN KEY (reporter_id) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.currency_balance ADD CONSTRAINT fk_currency_balance_player FOREIGN KEY (player_id) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.currency_transaction ADD CONSTRAINT fk_currency_transaction_player FOREIGN KEY (player_id) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.notification ADD CONSTRAINT fk_notification_player FOREIGN KEY (player_id) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.api_keys ADD CONSTRAINT fk_api_keys_player FOREIGN KEY (player_id) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.experiment_assignment ADD CONSTRAINT fk_experiment_assignment_experiment FOREIGN KEY (experiment_id) REFERENCES game.experiment (id) ON DELETE CASCADE;
ALTER TABLE game.experiment_assignment ADD CONSTRAINT fk_experiment_assignment_player FOREIGN KEY (player_id) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.platform_leaderboards ADD CONSTRAINT fk_leaderboards_game FOREIGN KEY (game_id) REFERENCES game.game_catalog (id) ON DELETE CASCADE;
ALTER TABLE game.platform_leaderboards ADD CONSTRAINT fk_leaderboards_player FOREIGN KEY (player_id) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.game_version ADD CONSTRAINT fk_game_version_game FOREIGN KEY (game_id) REFERENCES game.game_catalog (id) ON DELETE CASCADE;
ALTER TABLE game.game_fork ADD CONSTRAINT fk_game_fork_child FOREIGN KEY (child_game_id) REFERENCES game.game_catalog (id) ON DELETE CASCADE;
ALTER TABLE game.game_fork ADD CONSTRAINT fk_game_fork_parent FOREIGN KEY (parent_game_id) REFERENCES game.game_catalog (id) ON DELETE CASCADE;
ALTER TABLE game.game_fork ADD CONSTRAINT fk_game_fork_player FOREIGN KEY (forked_by) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.game_like ADD CONSTRAINT fk_game_like_game FOREIGN KEY (game_id) REFERENCES game.game_catalog (id) ON DELETE CASCADE;
ALTER TABLE game.game_like ADD CONSTRAINT fk_game_like_player FOREIGN KEY (player_id) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.game_comment ADD CONSTRAINT fk_game_comment_game FOREIGN KEY (game_id) REFERENCES game.game_catalog (id) ON DELETE CASCADE;
ALTER TABLE game.game_comment ADD CONSTRAINT fk_game_comment_player FOREIGN KEY (player_id) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.generation_job ADD CONSTRAINT fk_generation_job_game FOREIGN KEY (game_id) REFERENCES game.game_catalog (id) ON DELETE SET NULL;
ALTER TABLE game.generation_job ADD CONSTRAINT fk_generation_job_player FOREIGN KEY (requested_by) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.session ADD CONSTRAINT fk_session_game FOREIGN KEY (game_id) REFERENCES game.game_catalog (id) ON DELETE CASCADE;
ALTER TABLE game.session ADD CONSTRAINT fk_session_server FOREIGN KEY (server_id) REFERENCES game.server_registry (id) ON DELETE SET NULL;
ALTER TABLE game.achievement ADD CONSTRAINT fk_achievement_def FOREIGN KEY (achievement_def_id) REFERENCES game.achievement_def (id) ON DELETE CASCADE;
ALTER TABLE game.achievement ADD CONSTRAINT fk_achievement_game FOREIGN KEY (game_id) REFERENCES game.game_catalog (id) ON DELETE SET NULL;
ALTER TABLE game.achievement ADD CONSTRAINT fk_achievement_player FOREIGN KEY (player_id) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.moderation_action ADD CONSTRAINT fk_moderation_action_report FOREIGN KEY (report_id) REFERENCES game.report (id) ON DELETE CASCADE;
ALTER TABLE game.game_play ADD CONSTRAINT fk_game_play_game FOREIGN KEY (game_id) REFERENCES game.game_catalog (id) ON DELETE CASCADE;
ALTER TABLE game.game_play ADD CONSTRAINT fk_game_play_player FOREIGN KEY (player_id) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.game_play ADD CONSTRAINT fk_game_play_version FOREIGN KEY (version_id) REFERENCES game.game_version (id) ON DELETE SET NULL;
ALTER TABLE game.session_participant ADD CONSTRAINT fk_session_participant_player FOREIGN KEY (player_id) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.session_participant ADD CONSTRAINT fk_session_participant_session FOREIGN KEY (session_id) REFERENCES game.session (id) ON DELETE CASCADE;
ALTER TABLE game.action_log ADD CONSTRAINT fk_action_log_player FOREIGN KEY (player_id) REFERENCES game.players (id) ON DELETE CASCADE;
ALTER TABLE game.action_log ADD CONSTRAINT fk_action_log_session FOREIGN KEY (session_id) REFERENCES game.session (id) ON DELETE CASCADE;
ALTER TABLE game.state_snapshot ADD CONSTRAINT fk_state_snapshot_session FOREIGN KEY (session_id) REFERENCES game.session (id) ON DELETE CASCADE;

ALTER TABLE game.players ADD CONSTRAINT uq_players_auth_id UNIQUE (auth_id);
ALTER TABLE game.experiment ADD CONSTRAINT uq_experiment_name UNIQUE (name);
ALTER TABLE game.achievement_def ADD CONSTRAINT uq_achievement_def_name UNIQUE (name);

ALTER TABLE game.players ADD CONSTRAINT ck_players_ai_model_consistency CHECK ((is_ai = true AND ai_model IS NOT NULL) OR (is_ai = false AND ai_model IS NULL));
ALTER TABLE game.friendships ADD CONSTRAINT ck_friendships_no_self CHECK (player_a <> player_b);
ALTER TABLE game.friendships ADD CONSTRAINT ck_friendships_ordered CHECK (player_a < player_b);
ALTER TABLE game.game_catalog ADD CONSTRAINT ck_game_catalog_generation_consistency CHECK ((ai_generated = true AND generation_model IS NOT NULL) OR (ai_generated = false AND generation_model IS NULL AND generation_prompt IS NULL));
ALTER TABLE game.follow ADD CONSTRAINT ck_follow_no_self CHECK (follower_id <> followed_id);

CREATE INDEX idx_players_auth_id ON game.players (auth_id) WHERE auth_id IS NOT NULL;
CREATE INDEX idx_players_is_ai ON game.players (is_ai);
CREATE INDEX idx_players_name ON game.players (name);
CREATE INDEX idx_server_registry_server_type ON game.server_registry (server_type);
CREATE INDEX idx_server_registry_status ON game.server_registry (status);
CREATE INDEX idx_friendships_initiated_by ON game.friendships (initiated_by);
CREATE UNIQUE INDEX idx_friendships_pair ON game.friendships (player_a, player_b);
CREATE INDEX idx_friendships_player_b ON game.friendships (player_b);
CREATE INDEX idx_game_catalog_created_at ON game.game_catalog (created_at DESC);
CREATE INDEX idx_game_catalog_creator ON game.game_catalog (creator) WHERE creator IS NOT NULL;
CREATE INDEX idx_game_catalog_tags ON game.game_catalog USING gin (tags);
CREATE INDEX idx_chat_messages_channel_sent ON game.chat_messages (channel, sent_at DESC);
CREATE INDEX idx_chat_messages_sender ON game.chat_messages (sender_id, sent_at DESC);
CREATE INDEX idx_player_privacy_settings_player_id ON game.player_privacy_settings (player_id);
CREATE INDEX idx_age_verification_consent_token ON game.player_age_verification (consent_token) WHERE consent_token IS NOT NULL;
CREATE INDEX idx_player_age_verification_player_id ON game.player_age_verification (player_id);
CREATE INDEX idx_player_profile_player_id ON game.player_profile (player_id);
CREATE INDEX idx_player_setting_player_id ON game.player_setting (player_id);
CREATE INDEX idx_follow_followed ON game.follow (followed_id);
CREATE INDEX idx_follow_follower_id ON game.follow (follower_id);
CREATE INDEX idx_report_reporter ON game.report (reporter_id);
CREATE INDEX idx_report_status ON game.report (status);
CREATE INDEX idx_report_target ON game.report (target_type, target_id);
CREATE INDEX idx_currency_balance_player_id ON game.currency_balance (player_id);
CREATE INDEX idx_currency_transaction_player ON game.currency_transaction (player_id, created_at);
CREATE INDEX idx_currency_transaction_type ON game.currency_transaction ("type");
CREATE INDEX idx_notification_player ON game.notification (player_id, created_at);
CREATE INDEX idx_notification_player_unread ON game.notification (player_id, created_at) WHERE read = false;
CREATE UNIQUE INDEX idx_api_keys_hash ON game.api_keys (key_hash);
CREATE INDEX idx_api_keys_player ON game.api_keys (player_id);
CREATE INDEX idx_experiment_assignment_experiment_id ON game.experiment_assignment (experiment_id);
CREATE INDEX idx_experiment_assignment_player_id ON game.experiment_assignment (player_id);
CREATE INDEX idx_leaderboards_achieved_at ON game.platform_leaderboards (achieved_at DESC);
CREATE INDEX idx_leaderboards_game_score ON game.platform_leaderboards (game_id, score DESC);
CREATE INDEX idx_leaderboards_player ON game.platform_leaderboards (player_id);
CREATE INDEX idx_game_version_game_id ON game.game_version (game_id, created_at);
CREATE INDEX idx_game_fork_child ON game.game_fork (child_game_id);
CREATE INDEX idx_game_fork_forked_by ON game.game_fork (forked_by);
CREATE INDEX idx_game_fork_parent ON game.game_fork (parent_game_id);
CREATE INDEX idx_game_like_game ON game.game_like (game_id);
CREATE INDEX idx_game_like_player_id ON game.game_like (player_id);
CREATE INDEX idx_game_comment_game ON game.game_comment (game_id, created_at);
CREATE INDEX idx_game_comment_player ON game.game_comment (player_id);
CREATE INDEX idx_generation_job_game_id ON game.generation_job (game_id);
CREATE INDEX idx_generation_job_player ON game.generation_job (requested_by, created_at);
CREATE INDEX idx_generation_job_status ON game.generation_job (status);
CREATE INDEX idx_session_game ON game.session (game_id);
CREATE INDEX idx_session_room_code ON game.session (room_code);
CREATE INDEX idx_session_server_id ON game.session (server_id);
CREATE INDEX idx_session_status ON game.session (status);
CREATE INDEX idx_achievement_achievement_def_id ON game.achievement (achievement_def_id);
CREATE INDEX idx_achievement_game ON game.achievement (game_id) WHERE game_id IS NOT NULL;
CREATE INDEX idx_achievement_player_id ON game.achievement (player_id);
CREATE INDEX idx_moderation_action_report ON game.moderation_action (report_id);
CREATE INDEX idx_game_play_game ON game.game_play (game_id, created_at);
CREATE INDEX idx_game_play_player ON game.game_play (player_id, created_at);
CREATE INDEX idx_game_play_version_id ON game.game_play (version_id);
CREATE INDEX idx_session_participant_player_id ON game.session_participant (player_id);
CREATE INDEX idx_session_participant_session_id ON game.session_participant (session_id);
CREATE INDEX idx_action_log_player ON game.action_log (player_id);
CREATE INDEX idx_action_log_session_seq ON game.action_log (session_id, sequence_num);
CREATE INDEX idx_state_snapshot_session_seq ON game.state_snapshot (session_id, action_sequence_num);

COMMENT ON TABLE game.players IS 'Game-layer player identity. Links to auth.users via auth_id; anonymous players have auth_id NULL and is_anonymous true.';
COMMENT ON COLUMN game.players.auth_id IS 'FK to auth.users; NULL for anonymous players';
COMMENT ON COLUMN game.players.is_ai IS 'Whether this player is an AI agent. Disclosed per EU AI Act Art. 52.';
COMMENT ON COLUMN game.players.ai_model IS 'AI model identifier (e.g. claude-opus-4-6). NULL for human players.';
COMMENT ON TABLE game.server_registry IS 'Game server fleet registry. Servers register on startup and heartbeat periodically. Used for room routing and load balancing.';
COMMENT ON COLUMN game.server_registry.id IS 'Human-readable server identifier (e.g. python-1, zig-1)';
COMMENT ON COLUMN game.server_registry.host IS 'Hostname or IP address';
COMMENT ON COLUMN game.server_registry.version IS 'Server version string';
COMMENT ON TABLE game.experiment IS 'A/B experiments with variant definitions and allocation percentage.';
COMMENT ON COLUMN game.experiment.variants IS 'JSON array of variant definitions';
COMMENT ON COLUMN game.experiment.allocation_pct IS 'Percentage of eligible players to enroll (0-100)';
COMMENT ON TABLE game.achievement_def IS 'Achievement templates with criteria. Defines what achievements exist.';
COMMENT ON COLUMN game.achievement_def.criteria IS 'JSON object describing the unlock criteria';
COMMENT ON TABLE game.friendships IS 'Bidirectional friend relationships. player_a < player_b enforces canonical ordering to prevent duplicates.';
COMMENT ON TABLE game.game_catalog IS 'Game metadata registry. Game bundles live on filesystem; this stores discovery metadata only.';
COMMENT ON COLUMN game.game_catalog.generation_model IS 'AI model that generated this game. NULL for human-created. Disclosed per EU AI Act Art. 50.';
COMMENT ON COLUMN game.game_catalog.generation_prompt IS 'Original prompt used to generate this game. Stored for transparency.';
COMMENT ON TABLE game.chat_messages IS 'Player chat messages, partitioned by month. Retention: 90 days (drop old partitions via cron).';
COMMENT ON TABLE game.player_privacy_settings IS 'Per-player privacy controls. Created at registration with all social features disabled (privacy-by-default).';
COMMENT ON TABLE game.player_age_verification IS 'Age verification and parental consent state. is_minor is true when age < 16. Minors require parental consent before enabling social features.';
COMMENT ON COLUMN game.player_age_verification.is_minor IS 'True when age < 16';
COMMENT ON TABLE game.player_profile IS 'Extended player profile with bio, avatar, and progression data.';
COMMENT ON TABLE game.player_setting IS 'Key-value player settings. Composite PK on (player_id, key).';
COMMENT ON TABLE game.follow IS 'Player-to-player follows. Composite PK prevents duplicate follows.';
COMMENT ON TABLE game.report IS 'Player-submitted reports against other players, games, comments, or chat messages.';
COMMENT ON COLUMN game.report.target_id IS 'UUID of the reported entity';
COMMENT ON TABLE game.currency_balance IS 'Current virtual currency balance per player. Single row per player.';
COMMENT ON TABLE game.currency_transaction IS 'Immutable ledger of virtual currency transactions.';
COMMENT ON COLUMN game.currency_transaction.amount IS 'Signed amount (positive = credit, negative = debit)';
COMMENT ON COLUMN game.currency_transaction.reference_type IS 'Entity type that triggered the transaction (e.g. game_play, achievement)';
COMMENT ON COLUMN game.currency_transaction.reference_id IS 'UUID of the triggering entity';
COMMENT ON TABLE game.notification IS 'Player notifications for friend requests, likes, achievements, moderation, and system events.';
COMMENT ON COLUMN game.notification.data IS 'Arbitrary payload (e.g. friend request ID, game ID)';
COMMENT ON TABLE game.api_keys IS 'API keys for AI agent and service authentication. Keys are hashed; the plaintext is shown only once at creation.';
COMMENT ON TABLE game.experiment_assignment IS 'Tracks which variant a player is assigned to in an experiment.';
COMMENT ON TABLE game.platform_leaderboards IS 'Cross-game leaderboard entries. Each row is a score submission for a specific game.';
COMMENT ON TABLE game.game_version IS 'Versioned game bundles. Each game can have multiple published versions.';
COMMENT ON COLUMN game.game_version.bundle_size IS 'Bundle size in bytes';
COMMENT ON TABLE game.game_fork IS 'Tracks when a game is forked (remixed) from another. Links parent and child games.';
COMMENT ON TABLE game.game_like IS 'Player likes on games. Composite PK prevents duplicate likes.';
COMMENT ON TABLE game.game_comment IS 'Player comments on games.';
COMMENT ON TABLE game.generation_job IS 'AI game generation jobs. Tracks prompt, model, cost, and progress.';
COMMENT ON COLUMN game.generation_job.game_id IS 'Set after game is created';
COMMENT ON COLUMN game.generation_job.progress IS '0-100 percentage';
COMMENT ON TABLE game.session IS 'Multiplayer game sessions (lobbies). Tracks room code, player cap, and lifecycle.';
COMMENT ON COLUMN game.session.config IS 'Game-specific configuration for this session';
COMMENT ON COLUMN game.session.result IS 'Game result data set when session ends';
COMMENT ON COLUMN game.session.server_id IS 'ID of the server hosting this session. NULL for sessions created before fleet routing.';
COMMENT ON TABLE game.achievement IS 'Earned achievements per player. Composite PK prevents duplicate awards.';
COMMENT ON COLUMN game.achievement.game_id IS 'Game that triggered the unlock, if applicable';
COMMENT ON TABLE game.moderation_action IS 'Actions taken by moderators in response to reports.';
COMMENT ON TABLE game.game_play IS 'Individual play sessions. Tracks when a player plays a game, duration, and score.';
COMMENT ON TABLE game.session_participant IS 'Players in a game session with their role.';
COMMENT ON TABLE game.action_log IS 'Append-only game action log for replay and reconnection. Each action has a monotonically increasing sequence number within its session.';
COMMENT ON TABLE game.state_snapshot IS 'Periodic game state snapshots for fast reconnection. Load latest snapshot, replay actions from its sequence_num forward.';
