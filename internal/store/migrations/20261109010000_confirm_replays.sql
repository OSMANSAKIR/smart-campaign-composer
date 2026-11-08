-- need.md N-DEL-009 (owner CHANGE → B): a message with an unanswered try (timeout, connection dropped after it
-- was made, lease expired mid-send) may have been delivered. When its attempts run out it gets up to
-- MaxConfirmReplays more tries with the same Idempotency-Key before it is marked failed, so a delivery the
-- provider recorded is confirmed by the provider's stored answer instead of being reported as failed.
ALTER TABLE messages
    ADD COLUMN unanswered_tries integer NOT NULL DEFAULT 0,
    ADD COLUMN confirm_replays  integer NOT NULL DEFAULT 0;
