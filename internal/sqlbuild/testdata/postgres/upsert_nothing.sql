INSERT INTO "public"."customers" ("id") VALUES ($1) ON CONFLICT ("id") DO NOTHING RETURNING *
-- parameters: [7]
-- returning
