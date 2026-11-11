-- B-3 (bonus, owner decision 2026-11-11): content in the recipient's language. docs/contracts/api.md §9.9.
--   The copy is written in the campaign's language first (the 'primary' rows), then adapted to the other
--   locale in a separate step (the 'localized' rows).

-- A content version is one row per (variant, locale). Existing rows are the primary copy, in the campaign's
-- language (else tr). The default stays so a plain INSERT keeps meaning "primary, tr".
ALTER TABLE campaign_content
    ADD COLUMN locale       text NOT NULL DEFAULT 'tr' CHECK (locale IN ('tr', 'en')),
    ADD COLUMN adapted_from text;      -- NULL: a primary row. Else the hash of the primary copy it was adapted from
UPDATE campaign_content cc SET locale = c.language FROM campaigns c WHERE c.id = cc.campaign_id AND c.language IS NOT NULL;
ALTER TABLE campaign_content DROP CONSTRAINT campaign_content_pkey,
    ADD PRIMARY KEY (campaign_id, version, variant, locale);

-- The recipient's locale, frozen at enqueue like the audience (need.md N-ORC-016). Messages enqueued before
-- B-3 render in the campaign language whatever this says (language_mode 'campaign').
ALTER TABLE messages
    ADD COLUMN locale text NOT NULL DEFAULT 'tr' CHECK (locale IN ('tr', 'en'));
UPDATE messages m SET locale = u.locale FROM users u WHERE u.id = m.user_id;

-- 'campaign': every recipient gets the campaign-language copy (the behaviour before B-3).
-- 'recipient': every recipient gets the copy in their own locale.
ALTER TABLE campaigns
    ADD COLUMN language_mode text NOT NULL DEFAULT 'campaign' CHECK (language_mode IN ('campaign', 'recipient'));

ALTER TABLE llm_calls DROP CONSTRAINT llm_calls_purpose_check,
    ADD CONSTRAINT llm_calls_purpose_check CHECK (purpose IN ('segment', 'content', 'repair', 'refine', 'localize'));
