-- What a trigger's registration answered with, kept on its route.
--
-- A pack trigger's lifecycle `set` may capture values from the service's
-- answer: the id of the subscription it made, or a secret the service generated
-- and will sign every delivery with. The `check` and `remove` that come later
-- are addressed by that id, and the secret is what verifies a delivery, so the
-- values are kept until the registration they belong to is removed.
--
-- The route row rather than the binding, because the route outlives it:
-- bindings are deleted and re-inserted on every activation and deleted on
-- deactivation, while the subscription is still there when the workflow is
-- activated again, and its id has to be too.
--
-- bytea, sealed with the credential cipher like credentials.payload, since a
-- captured value may be a secret. It is nullable because a route whose trigger
-- captures nothing — every route minted before this migration among them — has
-- no state, and NULL is that answer. Nothing is backfilled: nothing was ever
-- captured before this column existed.

ALTER TABLE "webhook_routes" ADD COLUMN "lifecycle_state" bytea;
