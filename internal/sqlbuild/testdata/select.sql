SELECT * FROM "public"."customers" WHERE "tier" = $1 AND "email" LIKE $2 ORDER BY "email" ASC LIMIT 50
-- parameters: ["gold","%@example.test"]
-- returning
