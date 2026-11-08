-- Initial schema (KICKOFF §5.4). Later migrations use timestamp names so parallel additions never collide.

CREATE TABLE users (
    id                 bigserial PRIMARY KEY,
    seed_key           text NOT NULL UNIQUE,            -- deterministic seed identity; makes the seed idempotent
    first_name         text NOT NULL DEFAULT '',
    last_name          text NOT NULL DEFAULT '',
    email              text NOT NULL,
    phone              text NOT NULL,                   -- E.164, except seeded invalid edge cases
    city               text NOT NULL,
    locale             text NOT NULL CHECK (locale IN ('tr', 'en')),
    birth_date         date NOT NULL,
    signup_at          timestamptz NOT NULL,
    last_login_at      timestamptz,
    last_purchase_at   timestamptz,
    purchase_count     integer NOT NULL DEFAULT 0 CHECK (purchase_count >= 0),
    total_spent        numeric(12, 2) NOT NULL DEFAULT 0 CHECK (total_spent >= 0),
    last_cart_add_at   timestamptz,
    cart_abandoned     boolean GENERATED ALWAYS AS (
                           last_cart_add_at IS NOT NULL
                           AND (last_purchase_at IS NULL OR last_purchase_at < last_cart_add_at)) STORED,
    favorite_category  text,
    loyalty_tier       text NOT NULL,
    sms_consent        boolean NOT NULL,
    email_consent      boolean NOT NULL,
    consent_updated_at timestamptz
);
CREATE INDEX users_city_idx ON users (city);
CREATE INDEX users_last_cart_add_at_idx ON users (last_cart_add_at);
CREATE INDEX users_last_purchase_at_idx ON users (last_purchase_at);

CREATE TABLE campaigns (
    id                       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    brief                    text NOT NULL,
    status                   text NOT NULL DEFAULT 'draft' CHECK (status IN
                                 ('draft', 'segmented', 'content_ready', 'sending', 'completed',
                                  'completed_with_failures', 'cancelled')),
    segment_rule             jsonb,                     -- validated rule only
    segment_version          integer NOT NULL DEFAULT 0,
    segment_meta             jsonb,                     -- source, rendering, unsupported_criteria, assumptions, explanation, warnings
    audience                 jsonb,                     -- last dry-run counts
    language                 text CHECK (language IN ('tr', 'en')),
    channels                 text[] NOT NULL DEFAULT '{}',
    approved_content_version integer,
    approve_idempotency_key  text UNIQUE,
    approve_response         jsonb,                     -- replayed for a repeated Idempotency-Key
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),
    approved_at              timestamptz,
    completed_at             timestamptz,
    scheduled_at             timestamptz                -- B-1 (bonus)
);
CREATE INDEX campaigns_created_at_idx ON campaigns (created_at DESC);

CREATE TABLE campaign_content (
    campaign_id   uuid NOT NULL REFERENCES campaigns (id),
    version       integer NOT NULL CHECK (version > 0),
    sms_text      text NOT NULL,
    email_subject text NOT NULL,
    email_body    text NOT NULL,
    source        text NOT NULL CHECK (source IN ('llm', 'edited')),
    validation    jsonb NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (campaign_id, version)
);

CREATE TABLE messages (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),   -- also the provider Idempotency-Key
    campaign_id         uuid NOT NULL REFERENCES campaigns (id),
    user_id             bigint NOT NULL REFERENCES users (id),
    channel             text NOT NULL CHECK (channel IN ('sms', 'email')),
    content_version     integer NOT NULL,
    status              text NOT NULL DEFAULT 'queued' CHECK (status IN
                            ('queued', 'sending', 'sent', 'retry', 'failed', 'skipped_no_consent')),
    attempts            integer NOT NULL DEFAULT 0,
    throttled_count     integer NOT NULL DEFAULT 0,
    next_attempt_at     timestamptz NOT NULL DEFAULT now(),
    lease_until         timestamptz,
    last_error          text,
    provider_message_id text,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    sent_at             timestamptz,
    UNIQUE (campaign_id, user_id, channel)                             -- R-SND-3 layer 1
);
CREATE INDEX messages_ready_idx ON messages (channel, next_attempt_at) WHERE status IN ('queued', 'retry');
CREATE INDEX messages_lease_idx ON messages (channel, lease_until) WHERE status = 'sending';
CREATE INDEX messages_campaign_status_idx ON messages (campaign_id, channel, status);

CREATE TABLE delivery_attempts (
    id          bigserial PRIMARY KEY,
    message_id  uuid NOT NULL REFERENCES messages (id),
    attempt     integer NOT NULL,
    http_status integer,                               -- null for timeouts and connection errors
    error_class text NOT NULL,                         -- success|permanent|config|throttled|transient|ambiguous
    latency_ms  integer NOT NULL,
    at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX delivery_attempts_message_idx ON delivery_attempts (message_id);

CREATE TABLE llm_calls (
    id                 bigserial PRIMARY KEY,
    campaign_id        uuid REFERENCES campaigns (id),
    purpose            text NOT NULL CHECK (purpose IN ('segment', 'content', 'repair')),
    provider           text NOT NULL,
    model              text NOT NULL,
    prompt_version     text NOT NULL,
    input_tokens       integer NOT NULL DEFAULT 0,
    output_tokens      integer NOT NULL DEFAULT 0,
    cache_read_tokens  integer NOT NULL DEFAULT 0,
    cache_write_tokens integer NOT NULL DEFAULT 0,
    cost_usd           numeric(12, 6) NOT NULL DEFAULT 0,
    latency_ms         integer NOT NULL DEFAULT 0,
    stop_reason        text,
    outcome            text NOT NULL CHECK (outcome IN ('valid', 'repaired', 'invalid', 'refused', 'error')),
    error              text,
    created_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX llm_calls_campaign_idx ON llm_calls (campaign_id);

CREATE TABLE worker_heartbeats (
    worker_id    text NOT NULL,
    channel      text NOT NULL CHECK (channel IN ('sms', 'email')),
    holds_lock   boolean NOT NULL,
    paused_until timestamptz,
    at           timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (worker_id, channel)
);
