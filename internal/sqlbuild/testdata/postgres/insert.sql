INSERT INTO "public"."customers" ("email", "id", "tier") VALUES ($1, $2, $3) RETURNING *
-- parameters: ["ada@example.test",7,"gold"]
-- returning
