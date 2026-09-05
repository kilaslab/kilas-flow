package sqlbuild

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// Dialect is one database's spelling of the same statements.
//
// A value with a closed set of package-level instances, not an interface. The
// difference matters here more than it usually does: this is the one code path
// where user input reaches statement text, and an interface would make the
// quoting rule something a caller outside this package could supply. A closed
// set cannot be extended from outside, which is the same argument KnownOperators
// already makes about the comparison set.
//
// It is also not a `switch driver` inside each builder. That scatters the
// difference across five functions, and the next dialect has to find every
// site. Here every divergence is one field, listed in one place, and the golden
// files are rendered per dialect from the same table — so a dialect difference
// shows up as a diff in one directory and a change to the shared shape shows up
// as a diff in both.
type Dialect struct {
	name string
	// quote renders one identifier part, already checked.
	quote func(part string) string
	// checkPart refuses a part this server cannot address.
	checkPart func(part string) error
	// placeholder renders the nth bound parameter, counting from one.
	placeholder func(position int) string
	// qualifies is whether a target's schema is part of the name.
	qualifies bool
	// comparison renders one WHERE term, or reports that this dialect has no
	// spelling for the operator.
	comparison func(column, operator, bind string) (string, bool)
	// upsertTail renders everything after the VALUES list.
	upsertTail func(quoted, matching, updates []string) string
	// excluded renders one column's assignment from the row that would have
	// been inserted.
	excluded func(quoted string) string
	// returningAll is appended to a write that hands its row back, or empty
	// where the dialect has no such clause.
	returningAll string
}

// Name identifies the dialect, and names its golden directory.
func (dialect Dialect) Name() string { return dialect.name }

// Returns reports whether a write hands its row back.
func (dialect Dialect) Returns() bool { return dialect.returningAll != "" }

// Identifier quotes one or more identifier parts into a qualified name.
func (dialect Dialect) Identifier(parts ...string) (string, error) {
	if len(parts) == 0 {
		return "", fmt.Errorf("an identifier needs at least one part")
	}
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		if err := dialect.checkPart(part); err != nil {
			return "", err
		}
		quoted = append(quoted, dialect.quote(part))
	}
	return strings.Join(quoted, "."), nil
}

// Postgres is the dialect the operation set was written against.
var Postgres = Dialect{
	name: "postgres",
	// pgx's own quoting, wrapped rather than called: see checkPostgresPart for
	// the two things Sanitize does that this must not.
	quote:       func(part string) string { return pgx.Identifier{part}.Sanitize() },
	checkPart:   checkPostgresPart,
	placeholder: func(position int) string { return "$" + strconv.Itoa(position) },
	qualifies:   true,
	comparison:  standardComparison,
	excluded:    func(quoted string) string { return quoted + " = EXCLUDED." + quoted },
	upsertTail: func(quoted, matching, updates []string) string {
		tail := " ON CONFLICT (" + strings.Join(matching, ", ") + ")"
		if len(updates) == 0 {
			// DO UPDATE SET with an empty list is a syntax error, and there is
			// genuinely nothing to change about this row.
			return tail + " DO NOTHING"
		}
		return tail + " DO UPDATE SET " + strings.Join(updates, ", ")
	},
	returningAll: " RETURNING *",
}

// MySQL is the same statements in MySQL's and MariaDB's spelling.
var MySQL = Dialect{
	name:        "mysql",
	quote:       func(part string) string { return "`" + strings.ReplaceAll(part, "`", "``") + "`" },
	checkPart:   checkMySQLPart,
	placeholder: func(int) string { return "?" },
	// MySQL has no schemas separate from databases, so `a`.`b` names
	// *database* a's table b. Qualifying a target here would silently address
	// the wrong database on every statement the node builds.
	qualifies:  false,
	comparison: mysqlComparison,
	// VALUES(col), not the MySQL 8.0.19 row alias `AS new … new.col`. The
	// alias form is unavailable on MySQL 5.7 and on MariaDB entirely, and this
	// credential is documented as serving both — so the alias would break every
	// MariaDB user to silence a deprecation warning. Revisit when a MySQL
	// release actually removes VALUES().
	excluded: func(quoted string) string { return quoted + " = VALUES(" + quoted + ")" },
	upsertTail: func(quoted, matching, updates []string) string {
		if len(updates) == 0 {
			// A no-op assignment rather than INSERT IGNORE, which also
			// downgrades type errors, foreign-key violations and truncations
			// to warnings — a far wider silence than "this row already exists".
			return " ON DUPLICATE KEY UPDATE " + matching[0] + " = " + matching[0]
		}
		return " ON DUPLICATE KEY UPDATE " + strings.Join(updates, ", ")
	},
	// MySQL has no RETURNING. MariaDB has one for INSERT since 10.5, and using
	// it would split the dialect on a runtime version probe — so neither gets
	// it, and an insert reports rows affected and the driver's own last insert
	// id instead. See sqlnode.Result.LastInsertID for why that is the honest
	// answer rather than SELECT LAST_INSERT_ID().
	returningAll: "",
}

// MaxIdentifierBytes is PostgreSQL's own limit.
//
// The server truncates a longer name silently, so two columns whose first
// sixty-three bytes match would collide into one — a statement that runs and
// writes the wrong column. Refused here rather than discovered there.
const MaxIdentifierBytes = 63

// MaxIdentifierRunes is MySQL's, which counts characters rather than bytes.
//
// MySQL rejects an over-long name outright, so this refusal buys a better
// message rather than preventing silent damage — which is the opposite of what
// the PostgreSQL limit is for, and the reason the two are separate constants
// with separate wording.
const MaxIdentifierRunes = 64

func checkPostgresPart(part string) error {
	if strings.TrimSpace(part) == "" {
		return fmt.Errorf("an identifier part cannot be empty")
	}
	if strings.ContainsRune(part, 0) {
		// pgx strips it, which would silently rename the thing being addressed.
		return fmt.Errorf("an identifier cannot contain a NUL byte")
	}
	if len(part) > MaxIdentifierBytes {
		return fmt.Errorf("the identifier %q is %d bytes, and PostgreSQL truncates anything over %d — "+
			"two names that long can silently become one", part, len(part), MaxIdentifierBytes)
	}
	return nil
}

func checkMySQLPart(part string) error {
	if strings.TrimSpace(part) == "" {
		return fmt.Errorf("an identifier part cannot be empty")
	}
	if strings.ContainsRune(part, 0) {
		return fmt.Errorf("an identifier cannot contain a NUL byte")
	}
	if strings.HasSuffix(part, " ") {
		// MySQL refuses a trailing space in a table name even when it is
		// quoted, so a name that reaches the server is a run-time error rather
		// than a build-time one unless it is caught here.
		return fmt.Errorf("the identifier %q ends in a space, which MySQL refuses even when quoted", part)
	}
	if utf8.RuneCountInString(part) > MaxIdentifierRunes {
		return fmt.Errorf("the identifier %q is %d characters, and MySQL allows at most %d",
			part, utf8.RuneCountInString(part), MaxIdentifierRunes)
	}
	return nil
}

var comparisonSQL = map[string]string{
	"equals": "=", "notEquals": "<>", "gt": ">", "gte": ">=", "lt": "<", "lte": "<=",
	"like": "LIKE", "ilike": "ILIKE",
}

func standardComparison(column, operator, bind string) (string, bool) {
	symbol, known := comparisonSQL[operator]
	if !known {
		return "", false
	}
	return column + " " + symbol + " " + bind, true
}

func mysqlComparison(column, operator, bind string) (string, bool) {
	if operator == "ilike" {
		// MySQL has no ILIKE. Its default collations are already
		// case-insensitive, but a column under a _bin or _cs collation is not,
		// so the comparison is folded explicitly rather than relying on the
		// collation the table happens to carry. It defeats an index on that
		// column, which is the cost of meaning what the operator says.
		return "LOWER(" + column + ") LIKE LOWER(" + bind + ")", true
	}
	return standardComparison(column, operator, bind)
}
