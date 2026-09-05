INSERT INTO "public"."customers" ("email", "id", "tier") VALUES ($1, $2, $3) ON CONFLICT DO NOTHING RETURNING *
-- parameters: ["ada@example.test",7,"gold"]
-- returning
