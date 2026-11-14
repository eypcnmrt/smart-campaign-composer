-- What the campaign API needs on top of the first schema:
--   * version          for optimistic concurrency: approval must name the version the marketer reviewed, so a
--                      campaign that changed meanwhile cannot be approved by mistake;
--   * audience_*       the counts shown with the segment (matched users, those who consented to sms and to e-mail,
--                      and the number of users there were, which tells "everyone" from "most people");
--   * approved_*       the snapshot taken at approval: the rule, the content and the channels as they were approved,
--                      and the warnings that were acknowledged. The worker reads the snapshot, never the editable
--                      columns, so nothing edited later can change what is sent;
--   * an index for the stats and completion queries.
-- The state machine itself stays in Go (docs: DECISIONS.md, "The state machine is a CHECK on status plus data invariants").

ALTER TABLE campaigns
    ADD COLUMN version              INTEGER     NOT NULL DEFAULT 1 CHECK (version >= 1),
    ADD COLUMN audience_matched     INTEGER     CHECK (audience_matched >= 0),
    ADD COLUMN audience_sms         INTEGER     CHECK (audience_sms >= 0),
    ADD COLUMN audience_email       INTEGER     CHECK (audience_email >= 0),
    ADD COLUMN audience_total_users INTEGER     CHECK (audience_total_users >= 0),
    ADD COLUMN audience_counted_at  TIMESTAMPTZ,
    ADD COLUMN approved_rule        JSONB,
    ADD COLUMN approved_content     JSONB,
    ADD COLUMN approved_channels    TEXT[],
    ADD COLUMN approved_warnings    JSONB;

-- The counts are written together or not at all.
ALTER TABLE campaigns ADD CONSTRAINT campaigns_audience_all_or_nothing
    CHECK (num_nonnulls(audience_matched, audience_sms, audience_email, audience_total_users, audience_counted_at) IN (0, 5));

-- From approval on, the snapshot is complete, and its channels are as valid as the editable ones.
ALTER TABLE campaigns ADD CONSTRAINT campaigns_approved_has_snapshot
    CHECK (status IN ('draft', 'audience_ready', 'content_ready')
           OR (approved_rule IS NOT NULL AND approved_content IS NOT NULL AND approved_channels IS NOT NULL));
ALTER TABLE campaigns ADD CONSTRAINT campaigns_approved_channels_valid
    CHECK (approved_channels IS NULL
           OR (cardinality(approved_channels) > 0 AND approved_channels <@ ARRAY['sms', 'email']));

-- Stats (counts per channel and state of one campaign) and the completion check (is anything still queued or sending)
-- both start from a campaign and a status. With (campaign_id, status) as the prefix the completion check is covered, and
-- with the two other columns the stats query is an index-only scan. It makes the old (campaign_id, status) index
-- redundant: that one is a prefix of this one.
CREATE INDEX deliveries_campaign_status_channel_idx ON deliveries (campaign_id, status, channel, attempts);
DROP INDEX deliveries_campaign_status_idx;
