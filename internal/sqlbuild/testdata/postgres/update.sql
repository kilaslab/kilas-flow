UPDATE "public"."customers" SET "email" = $1, "tier" = $2 WHERE "id" = $3 RETURNING *
-- parameters: ["ada@example.test","gold",7]
-- returning
