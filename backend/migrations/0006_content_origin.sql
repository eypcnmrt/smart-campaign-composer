-- What the composer screen needs to be honest with the marketer:
--   * content_origin       'generated' (written by the model) or 'edited' (saved through PUT /content). Regenerating
--                          edited text throws the marketer's work away, so the screen asks before it does;
--   * segment_updated_at   when the rule was last (re)generated;
--   * content_updated_at   when the content was last saved, by either path.
-- A segment newer than the content means the text was written for an audience that no longer exists; the API reports it as
-- segment_changed_after_content so the screen can offer to regenerate. Saving the content in any way clears the hint.

ALTER TABLE campaigns
    ADD COLUMN content_origin     TEXT        CHECK (content_origin IN ('generated', 'edited')),
    ADD COLUMN segment_updated_at TIMESTAMPTZ,
    ADD COLUMN content_updated_at TIMESTAMPTZ;

-- Rows from before this migration: the content was generated (there was no other way to write it), and both timestamps
-- are the last change, so no old campaign shows the hint.
UPDATE campaigns SET segment_updated_at = updated_at WHERE segment_rule IS NOT NULL;
UPDATE campaigns SET content_origin = 'generated', content_updated_at = updated_at WHERE content IS NOT NULL;

-- No constraint ties these columns to content and segment_rule on purpose: rows written by other code (the seed, the load
-- test, fixtures) may set content without them. A missing origin reads as 'generated', a missing time as "unknown", and
-- unknown never shows the hint.
