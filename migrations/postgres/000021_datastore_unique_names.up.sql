-- A data table's name is unique within its tenant, compared without case.
--
-- A name is how the By-Name locator addresses a table, and the locator has
-- always compared names without regard to case. Nothing stopped a tenant from
-- holding "Leads" beside "leads" — not even two tables both called "Leads" — and
-- the locator then acted on whichever the catalogue listed first: a run that
-- asked for the empty "leads" read the rows of "Leads", and an Update, Delete or
-- Clear would have written to it (BUG-e7dwpk). n8n keeps names unique within a
-- project, which is the rule this restores.
--
-- The engine checks the rule before every create and rename, in Go, and that
-- check is the authority: Go folds case the same way on every install, while
-- lower() here folds by the database's locale. The index is the backstop for
-- two writers that pass the check at the same moment, and it has to be built
-- over the same expression the duplicates below are grouped by, or a pair the
-- grouping missed would stop the index from being created at all.
--
-- The duplicates a database already holds are renamed rather than refused or
-- deleted: the tables and their rows are the tenant's, and a migration that
-- cannot finish would take the whole install down with it. The oldest table of
-- each name keeps it — by created_at, then by id so a tie still has one answer —
-- because that is the one a workflow written against the name most likely
-- meant. Every other gets its own id appended, " (datastore_…)", which is unique
-- by construction and tells its owner which table it is. From here on the name
-- resolves to the table that kept it and to no other; a workflow that meant one
-- of the renamed tables finds it under its new name, from the list or by id.
--
-- The name column is varchar(255), so the original name is cut short enough for
-- the suffix to fit whole: the id is the part that makes the new name unique,
-- and a suffix cut in half would not be.
--
-- Table and index names are quoted so database.table_prefix rewrites them.

UPDATE "datastores"
    SET "name" = substr("name", 1, 255 - length(' (' || "id" || ')')) || ' (' || "id" || ')'
    WHERE "id" IN (
        SELECT "id" FROM (
            SELECT "id", ROW_NUMBER() OVER (
                PARTITION BY "tenant_id", lower("name")
                ORDER BY "created_at", "id"
            ) AS "seniority"
            FROM "datastores"
        ) AS "ranked"
        WHERE "seniority" > 1
    );

CREATE UNIQUE INDEX "uidx_datastores_tenant_lower_name" ON "datastores" ("tenant_id", lower("name"));
