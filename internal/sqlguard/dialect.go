package sqlguard

// Dialect is the lexical grammar of one server's SQL.
//
// A value with closed package-level instances rather than an interface, for the
// same reason internal/sqlbuild's dialect is: an interface would let a caller
// outside this package supply its own rules for where a string ends, which is
// exactly the decision that must not be extensible.
type Dialect struct {
	name string
	// bracketQuotes marks a dialect where [ ... ] is an identifier. SQLite
	// only: turning it on for the others would let a bracket hide a semicolon
	// in text those servers read as code.
	bracketQuotes bool
	// backtickQuotes marks a dialect where ` ... ` is an identifier.
	backtickQuotes bool
	// dollarQuotes marks a dialect with $tag$ ... $tag$ strings. PostgreSQL
	// only, and it has to coexist with $1 placeholders — see dollarOpener.
	dollarQuotes bool
	// escapeStrings marks a dialect with E'...' , where a backslash always
	// escapes regardless of any server setting.
	escapeStrings bool
	// backslashMayEscape marks a dialect where a backslash inside an ordinary
	// string literal MIGHT be an escape, depending on a server setting this
	// process cannot see — standard_conforming_strings on PostgreSQL, sql_mode
	// on MySQL. Those are the dialects whose text must be lexed under both
	// readings.
	//
	// SQLite has no such setting: a backslash there is always a literal
	// character. Lexing it under the escaping reading as well would refuse
	// `SELECT 'C:\'`, an ordinary Windows path, for no gain — the pass exists
	// to catch a server that disagrees with the lexer, and this one cannot.
	backslashMayEscape bool
	// backslashAlwaysEscapes is the answer once a caller has asked the server,
	// and is meaningful only when backslashMayEscape is false.
	backslashAlwaysEscapes bool
	// doubleQuoteStrings marks a dialect where " delimits a string rather than
	// an identifier, and therefore where a backslash inside one can escape the
	// closing quote. MySQL, unless the server runs with ANSI_QUOTES.
	doubleQuoteStrings bool
	// nestedBlockComments marks a dialect where /* /* */ */ nests.
	//
	// Getting this backwards is a bypass in exactly one direction. Where the
	// server does not nest, a depth-counting lexer reads the tail of
	// `SELECT 1 /* /* */ ; DROP TABLE t` as comment and sees one statement,
	// while the server ends the comment at the first */ and runs the DROP. The
	// opposite mistake only ever refuses too much.
	nestedBlockComments bool
	// hashComments marks a dialect where # begins a comment. MySQL only;
	// SQLite rejects # as an unrecognised token.
	hashComments bool
	// lineCommentNeedsSpace marks a dialect where -- is a comment only when
	// followed by whitespace. MySQL requires it, so `SELECT 1 --x;DROP TABLE t`
	// is code there and must not be skipped as a comment.
	lineCommentNeedsSpace bool
	// executableComments marks a dialect where /*! ... */ is code rather than a
	// comment. MySQL's version-gated form: /*!50000 DROP TABLE t */ is a DROP.
	executableComments bool
	// allowed are the opening keywords a statement may begin with.
	//
	// One list rather than a read list and a write list. A split was tried and
	// removed: none of the operations that accept hand-written SQL promises to
	// be read-only — n8n's Execute Query runs DDL, and this server's own
	// "query" operation differs from "execute" in the shape of what comes
	// back, not in what it is permitted to do — so refusing a DELETE there
	// would reject legitimate work while preventing nothing the list below
	// does not already prevent. What the guard is for is that the text is one
	// statement and that its verb is not ATTACH, COPY, DO, LOAD or PRAGMA.
	allowed map[string]bool
	// twoWord are the opening keywords too broad to admit on their own, mapped
	// to the second words that are allowed after them. CREATE admits TABLE and
	// INDEX; it must not admit TRIGGER, FUNCTION, EXTENSION or USER.
	twoWord map[string]map[string]bool
	// forbiddenPhrases are token sequences refused anywhere in a statement,
	// not only at its start. An opening keyword is not enough on its own:
	// MySQL's SELECT ... INTO OUTFILE writes a server-side file and opens with
	// an allowlisted word.
	//
	// Matched against bare words only. A quoted identifier is a name, and a
	// name can never be the statement these phrases describe — `SELECT "grant"
	// FROM t` reads a column, it does not grant anything — so refusing it
	// would cost a legitimate query nothing is gained by refusing.
	forbiddenPhrases [][]string
	// forbiddenNames are single identifiers refused however they are spelled,
	// bare or quoted.
	//
	// Separate from the phrases because the reasoning inverts. These are
	// functions, and a quoted function name is still a call: PostgreSQL
	// resolves `"pg_read_file"` to exactly the function `pg_read_file` and
	// runs it. Checking only the bare spelling left the whole list open to a
	// pair of quotation marks — verified against a live server, which returned
	// the contents of a file on its own disk.
	forbiddenNames []string
}

// Name identifies the dialect in a refusal message.
func (dialect Dialect) Name() string { return dialect.name }

// WithKnownBackslashEscapes returns the dialect with the ambiguity resolved.
//
// The two-reading rule exists because a server setting decides whether a
// backslash escapes inside a string literal and this process cannot see it. A
// caller that has *asked* the server can say so, and then the guard holds the
// text to the one reading that server actually uses instead of to both.
//
// This matters more than it sounds. `SELECT 'O\'Brien'` is valid and
// unambiguous under the default sql_mode of every MySQL and MariaDB this
// server talks to, and holding it to both readings refused it — along with
// every apostrophe in every hand-written string, which is a great many of
// them.
func (dialect Dialect) WithKnownBackslashEscapes(escapes bool) Dialect {
	dialect.backslashMayEscape = false
	dialect.backslashAlwaysEscapes = escapes
	return dialect
}

// backslashReadings are the interpretations of a backslash the text must be
// safe under.
//
// Two where a server setting decides it, one where the language does. Every
// reading must yield exactly one allowed statement, so a dialect whose setting
// is unknowable is held to the stricter of the two answers rather than to a
// guess about which one the server is using.
func (dialect Dialect) backslashReadings() []bool {
	if dialect.backslashMayEscape {
		return []bool{true, false}
	}
	return []bool{dialect.backslashAlwaysEscapes}
}

// SQLite is the grammar of the embedded database this server also runs on.
var SQLite = Dialect{
	name:           "sqlite",
	bracketQuotes:  true,
	backtickQuotes: true,
	allowed: keywordSet("SELECT", "VALUES", "TABLE", "WITH",
		"INSERT", "UPDATE", "DELETE", "REPLACE"),
	twoWord: map[string]map[string]bool{
		"CREATE": keywordSet("TABLE", "INDEX", "UNIQUE", "TEMP", "TEMPORARY", "VIRTUAL", "VIEW"),
		"DROP":   keywordSet("TABLE", "INDEX", "VIEW"),
		"ALTER":  keywordSet("TABLE"),
	},
	// ATTACH reaches another database file on the same connection, which is
	// how a workflow read this installation's credentials table. VACUUM INTO
	// writes a complete copy of the connected database to a path the statement
	// names, which is the same exfiltration in one word and no semicolon.
	// PRAGMA reconfigures the connection, including writable_schema. All three
	// are refused by the allowlist rather than named here.
	//
	// EXPLAIN is deliberately absent from the list above. See the PostgreSQL
	// dialect for why admitting it could not be made safe.
	forbiddenPhrases: nil,
}

// Postgres is the grammar pgx speaks.
var Postgres = Dialect{
	name:                "postgres",
	dollarQuotes:        true,
	escapeStrings:       true,
	backslashMayEscape:  true,
	nestedBlockComments: true,
	allowed: keywordSet("SELECT", "VALUES", "TABLE", "WITH",
		"INSERT", "UPDATE", "DELETE", "MERGE", "TRUNCATE", "SHOW", "ANALYZE"),
	twoWord: map[string]map[string]bool{
		"CREATE": keywordSet("TABLE", "INDEX", "UNIQUE", "TEMP", "TEMPORARY", "VIEW",
			"MATERIALIZED", "SCHEMA", "TYPE", "SEQUENCE"),
		// SCHEMA is a namespace in PostgreSQL, not a database, so managing one
		// is ordinary work for a node whose credential owns it. MySQL spells
		// DROP DATABASE that way, which is a different blast radius, and is
		// deliberately not on its list below.
		"DROP":  keywordSet("TABLE", "INDEX", "VIEW", "MATERIALIZED", "SCHEMA", "TYPE", "SEQUENCE"),
		"ALTER": keywordSet("TABLE", "INDEX", "VIEW", "SEQUENCE", "MATERIALIZED"),
	},
	// COPY … TO PROGRAM runs a shell command as the server's own user, and DO
	// runs an anonymous procedural block — the multi-statement equivalent that
	// survives pgx's extended protocol, where a literal semicolon does not.
	// Both are refused by the allowlist.
	//
	// The rest are refused anywhere in the statement, because the opening
	// keyword is not where the danger sits:
	//
	//   EXPLAIN ANALYZE executes the statement it claims to explain, so a
	//   DELETE reaches the table through a verb that reads as diagnostic.
	//
	//   CREATE SCHEMA takes a space-separated list of element commands with no
	//   semicolons between them, so `CREATE SCHEMA x GRANT SELECT ON secret TO
	//   attacker` passes an opening-keyword check and grants on a table that
	//   already existed. Verified against a live server: has_table_privilege
	//   returned true afterwards. The same list carries CREATE TRIGGER, which
	//   attaches a function to a table the statement never appears to touch.
	//
	//   The file functions run inside a plain SELECT and read or write the
	//   server's own filesystem. They are gated by the credential's privileges
	//   too, and that gate is the primary control — this list is the second
	//   one, and it is not a claim that arbitrary SQL is safe.
	// EXPLAIN is not on the allowlist above, and the attempt to admit it is
	// worth recording so nobody repeats it. EXPLAIN ANALYZE executes the
	// statement it claims to explain, so the plan was to admit the verb and
	// refuse that pair of words. The pair is not a reliable thing to refuse:
	// `EXPLAIN (ANALYZE) DELETE FROM t` puts a parenthesis between them and
	// deleted every row on a live server, and `EXPLAIN ANALYSE DELETE FROM t`
	// spells it the British way, which PostgreSQL accepts as a synonym, and
	// did the same. Both passed a guard that refused the bare spelling.
	// Reading an option list correctly is parsing, and a node that reads rows
	// does not need a query planner — so the verb goes, and the cost is that
	// somebody wanting a plan runs it in a database client.
	forbiddenPhrases: [][]string{
		{"GRANT"}, {"REVOKE"},
		// Paired with the verb rather than named alone. A bare TRIGGER also
		// appears in `ALTER TABLE t DISABLE TRIGGER ALL`, which is ordinary
		// maintenance — what must not get through is one being *created*,
		// which is how a CREATE SCHEMA element list smuggled one in.
		{"CREATE", "TRIGGER"}, {"CREATE", "FUNCTION"}, {"CREATE", "PROCEDURE"},
		{"CREATE", "OR", "REPLACE", "FUNCTION"}, {"CREATE", "OR", "REPLACE", "PROCEDURE"},
		{"CREATE", "OR", "REPLACE", "TRIGGER"},
	},
	forbiddenNames: []string{
		"PG_READ_FILE", "PG_READ_BINARY_FILE", "PG_LS_DIR", "PG_STAT_FILE",
		"LO_IMPORT", "LO_EXPORT", "DBLINK",
	},
}

// MySQL is the grammar go-sql-driver speaks, and MariaDB's.
var MySQL = Dialect{
	name:                  "mysql",
	backtickQuotes:        true,
	backslashMayEscape:    true,
	doubleQuoteStrings:    true,
	hashComments:          true,
	lineCommentNeedsSpace: true,
	executableComments:    true,
	allowed: keywordSet("SELECT", "VALUES", "WITH",
		"INSERT", "UPDATE", "DELETE", "REPLACE", "TRUNCATE",
		"SHOW", "DESCRIBE", "DESC"),
	twoWord: map[string]map[string]bool{
		"CREATE": keywordSet("TABLE", "INDEX", "UNIQUE", "TEMPORARY", "VIEW"),
		"DROP":   keywordSet("TABLE", "INDEX", "VIEW"),
		"ALTER":  keywordSet("TABLE"),
	},
	// These open with an allowlisted keyword, which is why the opening-keyword
	// check cannot be the only one. INTO OUTFILE and INTO DUMPFILE write a
	// server-side file from inside a SELECT; LOAD_FILE reads one. All three are
	// additionally gated by the FILE privilege and secure_file_priv on the
	// server, so this is defence in depth rather than the only control.
	forbiddenPhrases: [][]string{
		{"INTO", "OUTFILE"},
		{"INTO", "DUMPFILE"},
		{"GRANT"}, {"REVOKE"},
		{"CREATE", "TRIGGER"}, {"CREATE", "FUNCTION"}, {"CREATE", "PROCEDURE"},
	},
	forbiddenNames: []string{"LOAD_FILE"},
}

func keywordSet(words ...string) map[string]bool {
	set := make(map[string]bool, len(words))
	for _, word := range words {
		set[word] = true
	}
	return set
}
