CREATE SCHEMA chat;

CREATE DOMAIN chat.short_text AS text CHECK (LENGTH(VALUE) <= 255);

CREATE TABLE chat.messages (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    channel short_text NOT NULL,
    sent_at timestamptz NOT NULL DEFAULT now(),
    score int8 NOT NULL DEFAULT 0,
    CONSTRAINT pk_messages PRIMARY KEY (id)
);

CREATE INDEX idx_messages_channel_sent ON chat.messages (channel, sent_at DESC);
CREATE INDEX idx_messages_plain ON chat.messages (channel);
CREATE INDEX idx_messages_score ON chat.messages (score DESC);

COMMENT ON TABLE chat.messages IS 'Chat messages';
