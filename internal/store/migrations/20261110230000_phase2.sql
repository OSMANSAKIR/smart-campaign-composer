-- Phase 2 (owner decisions 2026-11-10; docs/contracts/api.md §9):
--   F1 A/B variants, F2 manual rule edits (kept in campaigns.segment_meta, no column), F3 refine,
--   F4 cancel, F5 scheduled send.

-- F1: a content version is one row per variant. Every version has variant 'a'; a two-variant version also
-- has 'b'. Existing rows become variant 'a' of their version.
ALTER TABLE campaign_content
    ADD COLUMN variant     text NOT NULL DEFAULT 'a' CHECK (variant IN ('a', 'b')),
    ADD COLUMN label       text NOT NULL DEFAULT '',   -- short angle, e.g. "Friendly reminder"
    ADD COLUMN instruction text;                       -- F3: the marketer's refine instruction for this row
ALTER TABLE campaign_content DROP CONSTRAINT campaign_content_pkey,
    ADD PRIMARY KEY (campaign_id, version, variant);
ALTER TABLE campaign_content DROP CONSTRAINT campaign_content_source_check,
    ADD CONSTRAINT campaign_content_source_check CHECK (source IN ('llm', 'edited', 'refined'));

-- F1: which variant(s) the approve sends: everyone gets A, everyone gets B, or a 50/50 split.
-- F4: cancelled_at. F5: status 'scheduled' (the scheduled_at column exists since the initial schema).
ALTER TABLE campaigns
    ADD COLUMN send_variant text CHECK (send_variant IN ('a', 'b', 'ab')),
    ADD COLUMN cancelled_at timestamptz;
ALTER TABLE campaigns DROP CONSTRAINT campaigns_status_check,
    ADD CONSTRAINT campaigns_status_check CHECK (status IN
        ('draft', 'segmented', 'content_ready', 'scheduled', 'sending', 'completed',
         'completed_with_failures', 'cancelled'));
CREATE INDEX campaigns_scheduled_idx ON campaigns (scheduled_at) WHERE status = 'scheduled';

-- F1: the variant each message renders. F4: 'cancelled' is terminal.
ALTER TABLE messages
    ADD COLUMN variant text NOT NULL DEFAULT 'a' CHECK (variant IN ('a', 'b'));
ALTER TABLE messages DROP CONSTRAINT messages_status_check,
    ADD CONSTRAINT messages_status_check CHECK (status IN
        ('queued', 'sending', 'sent', 'retry', 'failed', 'skipped_no_consent', 'cancelled'));

-- F3: refine calls are accounted separately (their repairs stay 'repair').
ALTER TABLE llm_calls DROP CONSTRAINT llm_calls_purpose_check,
    ADD CONSTRAINT llm_calls_purpose_check CHECK (purpose IN ('segment', 'content', 'repair', 'refine'));
