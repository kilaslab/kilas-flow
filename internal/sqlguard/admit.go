package sqlguard

import (
	"fmt"
	"strings"
)

// admit decides whether one lexed statement may be sent.
func admit(dialect Dialect, parsed statement) error {
	if err := refuseForbiddenPhrases(dialect, parsed); err != nil {
		return err
	}

	words := parsed.words
	// A parenthesised read is legal PostgreSQL — (SELECT 1) UNION (SELECT 2) —
	// so leading parentheses are skipped. They are skipped for every dialect
	// because a server that rejects the form will reject it itself, and this
	// guard's job is not to reimplement each server's syntax errors.
	for len(words) > 0 && words[0] == "(" {
		words = words[1:]
	}
	if len(words) == 0 {
		return fmt.Errorf("%w: there is no statement to run", ErrRefused)
	}

	keyword := words[0]
	if keyword == "WITH" {
		// A CTE does not decide what the statement does. Verified against a
		// live server: WITH x AS (SELECT …) DELETE FROM t … ran on the read
		// operation and deleted rows, because the first word was WITH. The
		// keyword that matters is the one after the last CTE.
		after, err := afterCommonTableExpressions(words)
		if err != nil {
			return err
		}
		words = after
		keyword = words[0]
	}

	if dialect.allowed[keyword] {
		return nil
	}
	if second, ok := dialect.twoWord[keyword]; ok {
		// Modifiers between the verb and the object kind are skipped, so the
		// second-word rule reads what is being created rather than how. CREATE
		// OR REPLACE VIEW, CREATE UNLOGGED TABLE and CREATE GLOBAL TEMPORARY
		// TABLE are all ordinary DDL that a rule looking only at words[1] read
		// as an unknown object and refused.
		for len(words) > 2 && createModifiers[words[1]] {
			words = append([]string{words[0]}, words[2:]...)
		}
		// CREATE, DROP and ALTER are too broad to admit on their own: CREATE
		// TRIGGER and CREATE FUNCTION carry statement bodies, CREATE EXTENSION
		// loads code, CREATE USER grants access. The second word is what says
		// which of those this is.
		if len(words) > 1 && second[words[1]] {
			return nil
		}
		if len(words) > 1 {
			return fmt.Errorf("%w: %s %s is not a statement this server will send",
				ErrRefused, keyword, words[1])
		}
	}
	return refusal(dialect, keyword)
}

func refusal(dialect Dialect, keyword string) error {
	return fmt.Errorf("%w: %s is not a statement this server will send on %s; it admits %s",
		ErrRefused, keyword, dialect.name, allowedList(dialect))
}

func allowedList(dialect Dialect) string {
	words := make([]string, 0, len(dialect.allowed)+len(dialect.twoWord))
	for word := range dialect.allowed {
		words = append(words, word)
	}
	for word := range dialect.twoWord {
		words = append(words, word+" TABLE")
	}
	sortStrings(words)
	return strings.Join(words, ", ")
}

// afterCommonTableExpressions returns the words from the statement's real
// keyword onward.
//
// It walks name [(columns)] AS [NOT] MATERIALIZED ( body ) lists, skipping
// balanced parentheses. The body's own parentheses are balanced by counting
// the tokens the lexer already emitted, so a parenthesis inside a string
// cannot unbalance it — the lexer never emitted one.
func afterCommonTableExpressions(words []string) ([]string, error) {
	index := 1
	if index < len(words) && words[index] == "RECURSIVE" {
		index++
	}
	for {
		// name, optionally followed by a column list.
		if index >= len(words) {
			return nil, fmt.Errorf("%w: the WITH clause names no statement", ErrRefused)
		}
		index++
		if index < len(words) && words[index] == "(" {
			var err error
			index, err = skipBalanced(words, index)
			if err != nil {
				return nil, err
			}
		}
		for index < len(words) && (words[index] == "AS" || words[index] == "NOT" || words[index] == "MATERIALIZED") {
			index++
		}
		if index >= len(words) || words[index] != "(" {
			return nil, fmt.Errorf("%w: the WITH clause could not be read", ErrRefused)
		}
		var err error
		index, err = skipBalanced(words, index)
		if err != nil {
			return nil, err
		}
		// PostgreSQL 14 lets a recursive CTE carry SEARCH and CYCLE clauses
		// between the body and the statement. They are skipped rather than
		// parsed: everything they contain is column names, and the keyword
		// that decides what the statement does is still the one after them.
		// The clause's own column list may carry commas — SEARCH DEPTH FIRST
		// BY a, b SET ord — so the skip must run to the statement keyword and
		// not stop at the first comma, which a CTE list also uses. Stopping
		// early made the walker re-enter the CTE loop on `b SET ord SELECT …`
		// and refuse a valid query.
		for index < len(words) && (words[index] == "SEARCH" || words[index] == "CYCLE") {
			for index < len(words) && !isStatementKeyword(words[index]) {
				index++
			}
		}
		if index < len(words) && words[index] == "," {
			index++
			continue
		}
		break
	}
	if index >= len(words) {
		return nil, fmt.Errorf("%w: the WITH clause names no statement", ErrRefused)
	}
	return words[index:], nil
}

// createModifiers sit between CREATE, DROP or ALTER and the kind of object.
//
// Only words that say *how*, never what: adding an object kind here would let
// it through the second-word check it exists to enforce.
var createModifiers = map[string]bool{
	"OR": true, "REPLACE": true, "UNLOGGED": true, "GLOBAL": true, "LOCAL": true,
	"IF": true, "NOT": true, "EXISTS": true, "CONCURRENTLY": true,
}

// isStatementKeyword reports whether a word can begin a statement.
//
// Used only to find where a CTE's trailing clauses stop. It lists every verb
// any dialect admits rather than the current dialect's, because stopping too
// early only ends the skip at a word that is then classified normally, while
// stopping too late would skip the statement itself.
func isStatementKeyword(word string) bool {
	switch word {
	case "SELECT", "INSERT", "UPDATE", "DELETE", "MERGE", "VALUES", "TABLE", "REPLACE":
		return true
	}
	return false
}

// skipBalanced returns the index just past the parenthesis group at start.
func skipBalanced(words []string, start int) (int, error) {
	depth := 0
	for index := start; index < len(words); index++ {
		switch words[index] {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				return index + 1, nil
			}
		}
	}
	return 0, fmt.Errorf("%w: a parenthesis in the WITH clause is never closed", ErrRefused)
}

// refuseForbiddenPhrases refuses a token sequence anywhere in the statement.
//
// The opening keyword cannot be the only check, because the dangerous thing is
// sometimes a clause rather than a verb: MySQL's SELECT … INTO OUTFILE writes
// a file on the server and opens with a word every read allowlist contains.
func refuseForbiddenPhrases(dialect Dialect, parsed statement) error {
	for _, phrase := range dialect.forbiddenPhrases {
		if containsPhrase(parsed.words, phrase) {
			return fmt.Errorf("%w: %s is not something this server will send",
				ErrRefused, strings.Join(phrase, " "))
		}
	}
	// Names are checked in both streams: a function reached through a quoted
	// identifier is the same function.
	for _, name := range dialect.forbiddenNames {
		if containsPhrase(parsed.words, []string{name}) || containsWord(parsed.names, name) {
			return fmt.Errorf("%w: %s reaches the server's own filesystem and is never sent",
				ErrRefused, name)
		}
	}
	return nil
}

func containsWord(words []string, want string) bool {
	for _, word := range words {
		if word == want {
			return true
		}
	}
	return false
}

func containsPhrase(words, phrase []string) bool {
	if len(phrase) == 0 || len(words) < len(phrase) {
		return false
	}
	for start := 0; start+len(phrase) <= len(words); start++ {
		matched := true
		for offset, want := range phrase {
			if words[start+offset] != want {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// sortStrings orders a small slice without pulling in a dependency the rest of
// the package does not need.
func sortStrings(words []string) {
	for i := 1; i < len(words); i++ {
		for j := i; j > 0 && words[j] < words[j-1]; j-- {
			words[j], words[j-1] = words[j-1], words[j]
		}
	}
}
