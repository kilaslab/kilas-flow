SELECT * FROM "public"."customers" WHERE "tier" IS NULL OR "email" = $1
-- parameters: ["ada@example.test"]
-- returning
