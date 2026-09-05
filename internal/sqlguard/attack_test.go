package sqlguard_test

import (
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/sqlguard"
)

// Everything an adversarial pass found once the guard was written.
//
// These are not hypotheses. Each was executed against a live server by an
// independent reviewer who reproduced both halves — the guard allowing the
// text, and the server doing something the guard existed to prevent. Two of
// them dropped a table and copied a secret out of another one.
func TestTheGuardRefusesWhatBrokeItOnce(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		technique string
		dialect   sqlguard.Dialect
		sql       string
	}{
		{
			// The trailing `e` of an ordinary word was read as an E'' escape
			// prefix, which forced the backslash-escaping reading in BOTH
			// passes — so the double-lex, whose whole purpose is to disagree
			// with itself here, agreed on the wrong answer. On a default
			// server the string closes at the second quote and the tail is
			// code. This dropped a real table.
			technique: "a word ending in e turns the next string into an escape string",
			dialect:   sqlguard.Postgres,
			sql:       `SELECT name'\';DROP TABLE zz_victim;--'`,
		},
		{
			technique: "the same trick used to copy a secret into a new table",
			dialect:   sqlguard.Postgres,
			sql:       `SELECT name'\';CREATE TABLE zz_loot AS SELECT token FROM zz_secret;--'`,
		},
		{
			technique: "the same trick with a leading column so the statement looks ordinary",
			dialect:   sqlguard.Postgres,
			sql:       `SELECT 1, name'\';DROP TABLE zz_victim;--'`,
		},
		{
			// PostgreSQL's CREATE SCHEMA takes a space-separated list of
			// element commands with no semicolons, so a GRANT rides through
			// on a statement whose first two words are allowed — and grants
			// on a table that already existed, not merely one the schema
			// creates. has_table_privilege returned true afterwards.
			technique: "CREATE SCHEMA carries a GRANT in its element list",
			dialect:   sqlguard.Postgres,
			sql:       `CREATE SCHEMA sb_evil GRANT SELECT ON sb_secret TO sb_attacker`,
		},
		{
			technique: "CREATE SCHEMA carries a CREATE TRIGGER in its element list",
			dialect:   sqlguard.Postgres,
			sql: `CREATE SCHEMA sb_ev CREATE TABLE ht2 (id int) ` +
				`CREATE TRIGGER trg AFTER INSERT ON ht2 FOR EACH ROW EXECUTE FUNCTION sb_hook()`,
		},
		{
			// The same driver serves MariaDB, which spells its version-gated
			// executable comment /*M! … */. A lexer that knew only /*! read
			// MariaDB-executed code as a comment.
			technique: "MariaDB's own executable comment hides a second statement",
			dialect:   sqlguard.MySQL,
			sql:       `SELECT 1 /*M!100000 ; DROP TABLE t */`,
		},
		{
			technique: "MariaDB's executable comment hides a server-side file write",
			dialect:   sqlguard.MySQL,
			sql:       `SELECT 0x6f776e6564 /*M!100000 INTO OUTFILE '/tmp/x' */`,
		},
		{
			// These run inside a plain SELECT and reach the server's own
			// filesystem. The credential's privileges are the primary control
			// and this list is the second one — see the dialect's comment.
			technique: "a server-side file read inside an allowed SELECT",
			dialect:   sqlguard.Postgres,
			sql:       `SELECT pg_read_file('/etc/hostname')`,
		},
		{
			technique: "a server-side directory listing inside an allowed SELECT",
			dialect:   sqlguard.Postgres,
			sql:       `SELECT pg_ls_dir('/')`,
		},
		{
			technique: "a large-object import, the read half of a file primitive",
			dialect:   sqlguard.Postgres,
			sql:       `SELECT lo_import('/etc/hostname')`,
		},
		{
			technique: "a large-object export, the write half",
			dialect:   sqlguard.Postgres,
			sql:       `SELECT lo_export(16384, '/tmp/whatever')`,
		},
		{
			// EXPLAIN ANALYZE executes the statement it claims to explain, so
			// admitting EXPLAIN without this would have handed back every
			// verb the allowlist refuses.
			technique: "EXPLAIN ANALYZE executes the statement it explains",
			dialect:   sqlguard.Postgres,
			sql:       `EXPLAIN ANALYZE DELETE FROM t`,
		},
		{
			technique: "the same on MySQL, where EXPLAIN ANALYZE also executes",
			dialect:   sqlguard.MySQL,
			sql:       `EXPLAIN ANALYZE SELECT * FROM t`,
		},
	} {
		t.Run(row.technique, func(t *testing.T) {
			if err := sqlguard.Check(row.dialect, row.sql); err == nil {
				t.Fatalf("allowed: %s", row.sql)
			}
		})
	}
}

// The legitimate statements the same pass found the guard wrongly refusing.
//
// These matter as much as the bypasses. A guard that refuses real work gets
// worked around, and the workaround is always worse than the guard.
func TestTheGuardStoppedRefusingLegitimateWork(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		about   string
		dialect sqlguard.Dialect
		sql     string
	}{
		{about: "a Windows path, where SQLite has no backslash escape at all",
			dialect: sqlguard.SQLite, sql: `SELECT 'C:\' AS p`},
		{about: "listing tables", dialect: sqlguard.MySQL, sql: `SHOW TABLES`},
		{about: "describing a table", dialect: sqlguard.MySQL, sql: `DESCRIBE information_schema.tables`},
		{about: "the short spelling", dialect: sqlguard.MySQL, sql: `DESC t`},
		{about: "reading a setting", dialect: sqlguard.Postgres, sql: `SHOW search_path`},
		{about: "collecting statistics", dialect: sqlguard.Postgres, sql: `ANALYZE`},
		{about: "a SQLite view", dialect: sqlguard.SQLite, sql: `CREATE VIEW v AS SELECT 1`},
		{about: "dropping a SQLite view", dialect: sqlguard.SQLite, sql: `DROP VIEW v`},
		{about: "replacing a view", dialect: sqlguard.Postgres, sql: `CREATE OR REPLACE VIEW sb_v AS SELECT 1 AS x`},
		{about: "restarting a sequence", dialect: sqlguard.Postgres, sql: `ALTER SEQUENCE s RESTART`},
		{about: "a recursive CTE with a SEARCH clause",
			dialect: sqlguard.Postgres,
			sql: `WITH RECURSIVE t(n) AS (VALUES (1) UNION ALL SELECT n+1 FROM t WHERE n < 5) ` +
				`SEARCH DEPTH FIRST BY n SET ord SELECT * FROM t`},
		{about: "a recursive CTE with a CYCLE clause",
			dialect: sqlguard.Postgres,
			sql: `WITH RECURSIVE t(n) AS (VALUES (1) UNION ALL SELECT n+1 FROM t WHERE n < 5) ` +
				`CYCLE n SET is_cycle USING path SELECT * FROM t`},
		{about: "a quoted CTE name",
			dialect: sqlguard.Postgres, sql: `WITH "recent" AS (SELECT 1 AS id) SELECT * FROM "recent"`},
	} {
		t.Run(row.about, func(t *testing.T) {
			if err := sqlguard.Check(row.dialect, row.sql); err != nil {
				t.Fatalf("refused legitimate SQL: %v\nSQL: %s", err, row.sql)
			}
		})
	}
}

// EXPLAIN is refused outright, and this test exists to record that the
// decision was reversed on evidence rather than never considered.
//
// It was admitted at first, because refusing an ordinary `EXPLAIN SELECT 1` is
// a real cost and the dangerous form looked like two adjacent words worth
// refusing. It is not two adjacent words. `EXPLAIN (ANALYZE) DELETE FROM t`
// puts a parenthesis between them, `EXPLAIN ANALYSE` spells the second one the
// British way, and both executed against a live server while the bare spelling
// was refused. Reading an option list correctly is parsing, and a node that
// reads rows does not need a query planner.
func TestExplainIsRefusedBecauseItsOptionListCanExecute(t *testing.T) {
	t.Parallel()

	for _, dialect := range []sqlguard.Dialect{sqlguard.Postgres, sqlguard.MySQL, sqlguard.SQLite} {
		if err := sqlguard.Check(dialect, `EXPLAIN SELECT 1`); err == nil {
			t.Errorf("%s admits EXPLAIN, whose option list can execute the statement", dialect.Name())
		}
	}
}

// A column really named after a forbidden word is still readable.
//
// The name list and the phrase list are checked differently on purpose. A
// quoted *function* name is a call and must be caught; a quoted *column* name
// is a name and must not be. Getting this backwards in either direction is a
// defect — one lets a file read through, the other refuses an ordinary query.
func TestAColumnNamedAfterAForbiddenWordIsStillReadable(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		dialect sqlguard.Dialect
		sql     string
	}{
		{sqlguard.Postgres, `SELECT "grant" FROM t`},
		{sqlguard.Postgres, `SELECT "trigger", "function" FROM t`},
		{sqlguard.MySQL, "SELECT `into outfile` FROM t"},
		{sqlguard.SQLite, `SELECT [grant] FROM t`},
	} {
		if err := sqlguard.Check(row.dialect, row.sql); err != nil {
			t.Errorf("refused an ordinary column read: %v\nSQL: %s", err, row.sql)
		}
	}
}

// A statement whose meaning depends on a server setting is refused, and that
// is the design rather than a gap.
//
// On MySQL, `SELECT "a\";b"` is one statement under the default sql_mode and
// two under NO_BACKSLASH_ESCAPES, because the mode decides whether the
// backslash escapes the closing quote. This process cannot see that setting,
// and a credential field could reach it before the DSN was hardened — so the
// guard refuses rather than picking the reading that happens to be safe today.
// The cost is real and small: the same string written with single quotes is
// accepted.
func TestAStatementWhoseMeaningDependsOnAServerSettingIsRefused(t *testing.T) {
	t.Parallel()

	if err := sqlguard.Check(sqlguard.MySQL, `SELECT "a\";b" AS c`); err == nil {
		t.Error("a string whose length depends on sql_mode was allowed")
	}
	// The unambiguous spelling of the same thing goes through.
	if err := sqlguard.Check(sqlguard.MySQL, `SELECT 'a";b' AS c`); err != nil {
		t.Errorf("the unambiguous spelling was refused too: %v", err)
	}
}

// The refusal has to name the parameter or the verb, not just say no.
func TestARefusalSaysWhatWasWrong(t *testing.T) {
	t.Parallel()

	err := sqlguard.Check(sqlguard.SQLite, `SELECT 1; DROP TABLE t`)
	if err == nil {
		t.Fatal("two statements were allowed")
	}
	if !strings.Contains(err.Error(), "2 statements") {
		t.Errorf("error = %v, want it to say how many statements it found", err)
	}
}

// The third adversarial round: one bypass, and a list of ordinary SQL the
// guard was refusing.
//
// The false refusals matter as much as the bypass. A guard that rejects
// `SELECT 'O'Brien'` rejects most hand-written SQL containing an apostrophe,
// and a control people route around is worse than one that was never there.
func TestTheThirdRoundOfAttacks(t *testing.T) {
	t.Parallel()

	t.Run("refused", func(t *testing.T) {
		for _, row := range []struct {
			technique string
			dialect   sqlguard.Dialect
			sql       string
		}{
			{
				// The guard compares an identifier's spelling against a list
				// of function names. This form's spelling is not what the
				// server resolves — \0061 is an `a` — so a single escape hid
				// the name and read a file off the server's disk.
				technique: "a unicode-escaped identifier spells a forbidden name differently",
				dialect:   sqlguard.Postgres,
				sql:       `SELECT U&"pg_re\0061d_file"('/etc/hostname')`,
			},
			{
				technique: "the same for a binary file read",
				dialect:   sqlguard.Postgres,
				sql:       `SELECT U&"pg_read_bi\006Eary_file"('/etc/hostname')`,
			},
			{
				technique: "the same for a directory listing",
				dialect:   sqlguard.Postgres,
				sql:       `SELECT U&"pg_ls_di\0072"('.')`,
			},
			{
				technique: "the same for a large-object import",
				dialect:   sqlguard.Postgres,
				sql:       `SELECT U&"lo_impor\0074"('/etc/hostname')`,
			},
			{
				technique: "a create-trigger still cannot ride inside a schema element list",
				dialect:   sqlguard.Postgres,
				sql:       `CREATE SCHEMA s CREATE TABLE t (id int) CREATE TRIGGER g AFTER INSERT ON t EXECUTE FUNCTION f()`,
			},
		} {
			t.Run(row.technique, func(t *testing.T) {
				if err := sqlguard.Check(row.dialect, row.sql); err == nil {
					t.Fatalf("allowed: %s", row.sql)
				}
			})
		}
	})

	t.Run("no longer refused", func(t *testing.T) {
		for _, row := range []struct {
			about   string
			dialect sqlguard.Dialect
			sql     string
		}{
			{about: "an unlogged table", dialect: sqlguard.Postgres,
				sql: `CREATE UNLOGGED TABLE zz (id int)`},
			{about: "the SQL-standard temporary table spelling", dialect: sqlguard.Postgres,
				sql: `CREATE GLOBAL TEMPORARY TABLE zz (id int)`},
			{about: "a local temporary table", dialect: sqlguard.Postgres,
				sql: `CREATE LOCAL TEMPORARY TABLE zz (id int)`},
			{about: "an index built without locking the table", dialect: sqlguard.Postgres,
				sql: `CREATE INDEX CONCURRENTLY idx ON t (a)`},
			{about: "disabling triggers, which is maintenance rather than creation",
				dialect: sqlguard.Postgres, sql: `ALTER TABLE t DISABLE TRIGGER ALL`},
			{about: "enabling them again",
				dialect: sqlguard.Postgres, sql: `ALTER TABLE t ENABLE TRIGGER ALL`},
			{about: "a multi-column SEARCH clause", dialect: sqlguard.Postgres,
				sql: `WITH RECURSIVE t(a,b) AS (VALUES(1,1) UNION ALL SELECT a+1,b+1 FROM t WHERE a<3) ` +
					`SEARCH DEPTH FIRST BY a, b SET ord SELECT * FROM t`},
			{about: "a multi-column CYCLE clause", dialect: sqlguard.Postgres,
				sql: `WITH RECURSIVE t(a,b) AS (VALUES(1,1) UNION ALL SELECT a+1,b+1 FROM t WHERE a<3) ` +
					`CYCLE a, b SET is_cyc USING pth SELECT * FROM t`},
		} {
			t.Run(row.about, func(t *testing.T) {
				if err := sqlguard.Check(row.dialect, row.sql); err != nil {
					t.Fatalf("refused ordinary SQL: %v\nSQL: %s", err, row.sql)
				}
			})
		}
	})

	// The apostrophe case, which is only answerable once the server has been
	// asked. Both readings still apply until it is.
	t.Run("an escaped apostrophe, once the server has said which rule it uses", func(t *testing.T) {
		ambiguous := `SELECT 'O\'Brien' AS n`
		if err := sqlguard.Check(sqlguard.MySQL, ambiguous); err == nil {
			t.Error("the ambiguous dialect allowed a statement whose length depends on sql_mode")
		}
		escaping := sqlguard.MySQL.WithKnownBackslashEscapes(true)
		if err := sqlguard.Check(escaping, ambiguous); err != nil {
			t.Errorf("a server that escapes backslashes still refused it: %v", err)
		}
		// And where the server does not escape, the same text really is two
		// statements, so it must still be refused.
		literal := sqlguard.MySQL.WithKnownBackslashEscapes(false)
		if err := sqlguard.Check(literal, `SELECT 'a\'; DROP TABLE t; --'`); err == nil {
			t.Error("a server that treats a backslash literally allowed two statements")
		}
	})
}
