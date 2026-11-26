-- Who acted through an embed session, and who queued a run.
--
-- An embed session is minted by a host backend for one of its own users, and
-- the host may now name that user in the token (actor.id, actor.displayName).
-- Revisions and publish events already record an actor's kind and label; the
-- kind gains 'embed', and actor_id holds the host's identifier for its user,
-- which is neither a KilasFlow user nor an API key and so never reuses
-- actor_key_id. It is host-asserted: KilasFlow signs it into the token but
-- never verifies it.
--
-- Executions had no actor at all. A manual run or a retry now records who
-- queued it in the same vocabulary; a trigger-started run records nobody.
--
-- There is no backfill. Nothing ever recorded who queued an existing run, or
-- which host user stood behind an existing revision, so empty (NULL on
-- workflow_versions, whose other actor columns are nullable for the reason
-- 000019 gives) is the honest answer for every row written before this.

ALTER TABLE `workflow_versions` ADD COLUMN `actor_id` text;

ALTER TABLE `workflow_publish_events` ADD COLUMN `actor_id` text NOT NULL DEFAULT "";

ALTER TABLE `executions` ADD COLUMN `actor_kind` text NOT NULL DEFAULT "";

ALTER TABLE `executions` ADD COLUMN `actor_label` text NOT NULL DEFAULT "";

ALTER TABLE `executions` ADD COLUMN `actor_key_id` text NOT NULL DEFAULT "";

ALTER TABLE `executions` ADD COLUMN `actor_id` text NOT NULL DEFAULT "";
