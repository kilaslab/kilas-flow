INSERT INTO "public"."customers" ("email", "id", "tier") VALUES ($1, $2, $3) ON CONFLICT ("id") DO UPDATE SET "email" = EXCLUDED."email", "tier" = EXCLUDED."tier" RETURNING *
-- parameters: ["ada@example.test",7,"gold"]
-- returning
