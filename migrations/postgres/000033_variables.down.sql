-- Reverse of variables.up.sql. Every tenant loses its variables, and `$vars`
-- reads as empty again.

DROP TABLE IF EXISTS "variables";
