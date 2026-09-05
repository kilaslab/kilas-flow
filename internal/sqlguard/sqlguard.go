package sqlguard

import (
	"fmt"
	"strings"
)

// ErrRefused is the error every refusal wraps, so a caller can tell a guard
// refusal from a driver error without matching on message text.
var ErrRefused = fmt.Errorf("statement refused")

// Check reports whether text is one statement this operation may send.
//
// The text is lexed twice, under both readings of a backslash inside a string
// literal, and both readings must yield exactly one allowlisted statement.
// Which reading a server actually uses depends on settings this process cannot
// see — standard_conforming_strings on PostgreSQL, sql_mode on MySQL, one of
// which is reachable through a credential field — so agreeing with both is the
// only answer that does not depend on guessing right.
func Check(dialect Dialect, text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("%w: there is no statement to run", ErrRefused)
	}
	for _, backslashEscapes := range dialect.backslashReadings() {
		if err := check(dialect, text, backslashEscapes); err != nil {
			return err
		}
	}
	return nil
}

// CheckAll applies Check to every statement in a transaction's list.
//
// Each element is its own statement and is checked on its own: a list is not a
// licence to put two statements in one element, because the driver would run
// both and the count the caller reasoned about would be wrong.
func CheckAll(dialect Dialect, texts []string) error {
	for index, text := range texts {
		if err := Check(dialect, text); err != nil {
			return fmt.Errorf("statement %d: %w", index+1, err)
		}
	}
	return nil
}

func check(dialect Dialect, text string, backslashEscapes bool) error {
	statements, err := split(dialect, text, backslashEscapes)
	if err != nil {
		return err
	}
	if len(statements) == 0 {
		return fmt.Errorf("%w: there is no statement to run", ErrRefused)
	}
	if len(statements) > 1 {
		// The count is the finding, and the message says so plainly: this is
		// the shape an injected semicolon takes, and a user who genuinely
		// wanted two statements has the transaction operation for it.
		return fmt.Errorf("%w: this is %d statements and only one may be sent; "+
			"use the transaction operation to run several, and bind values with parameters "+
			"rather than writing them into the text", ErrRefused, len(statements))
	}
	return admit(dialect, statements[0])
}

// quotedToken stands in for any string or quoted name.
//
// A single quote character, which no bare word can contain, so it can never be
// mistaken for a keyword the allowlist or a forbidden phrase names.
const quotedToken = "'"

// statement is one lexed statement: its words, with strings and comments gone.
type statement struct {
	// names are the contents of quoted identifiers, upper-cased.
	//
	// Kept apart from words because the two answer different questions. A
	// quoted identifier can never be a statement's verb, so it must not reach
	// the keyword logic — but it IS resolved by the server, so
	// `SELECT "pg_read_file"(…)` calls the same function the bare spelling
	// would. Recording only the placeholder hid that call from the denylist
	// entirely, which is how this guard was broken after the placeholder was
	// added for an unrelated reason.
	names []string
	// words are the bare keywords and identifiers, upper-cased. Anything that
	// was inside a string, a comment or quotes is not here — a keyword the
	// server would not execute must not be classified as one.
	words []string
}

// split lexes the text into statements, honouring the dialect's quoting and
// comment rules.
func split(dialect Dialect, text string, backslashEscapes bool) ([]statement, error) {
	lexer := &lexer{dialect: dialect, src: text, backslashEscapes: backslashEscapes}
	return lexer.run()
}

type lexer struct {
	dialect          Dialect
	src              string
	pos              int
	backslashEscapes bool

	statements []statement
	current    statement
	word       strings.Builder
	// lastQuotedName carries a quoted identifier's contents out of
	// consumeQuoted, which is where the delimiter is known.
	lastQuotedName string
}

func (l *lexer) run() ([]statement, error) {
	for l.pos < len(l.src) {
		switch {
		case l.skipComment():
			// Comments contribute nothing, and a word cannot span one:
			// `SELE/**/CT` is not SELECT to any of these servers.
			l.endWord()
		case l.atStringOrIdentifier():
			if err := l.consumeQuoted(); err != nil {
				return nil, err
			}
			// A quoted region is a value or a name, never a keyword, so it
			// breaks the word without contributing to it — a table called
			// "grant" must not read as the verb. It still emits a placeholder,
			// because the classifier counts positions: without one, the walk
			// over a WITH clause whose name is quoted would step past the
			// opening parenthesis and lose the statement.
			l.endWord()
			l.current.words = append(l.current.words, quotedToken)
			if l.lastQuotedName != "" {
				l.current.names = append(l.current.names, l.lastQuotedName)
				l.lastQuotedName = ""
			}
		case l.src[l.pos] == ';':
			l.endWord()
			l.endStatement()
			l.pos++
		default:
			l.consumeBare()
		}
	}
	l.endWord()
	l.endStatement()
	return l.statements, nil
}

// skipComment consumes a comment and reports whether it did.
func (l *lexer) skipComment() bool {
	rest := l.src[l.pos:]
	switch {
	case strings.HasPrefix(rest, "--"):
		// MySQL requires whitespace after the dashes, so `--x` is code there.
		// Skipping it as a comment would hide from the lexer text the server
		// executes.
		if l.dialect.lineCommentNeedsSpace && !startsWithSpace(rest[2:]) {
			return false
		}
		l.pos += lineLength(rest)
		return true
	case l.dialect.hashComments && rest[0] == '#':
		l.pos += lineLength(rest)
		return true
	case strings.HasPrefix(rest, "/*"):
		// MySQL's /*! is not a comment at all: the server parses its contents
		// as SQL, gated on a version number it ignores when the server is
		// newer. Unwrapping it and lexing the contents as code is what makes
		// /*!50000 DROP TABLE t */ read as the DROP it is.
		// MariaDB spells its own version-gated comment /*M! …  */ and the same
		// driver serves both servers, so a lexer that knew only /*! would read
		// MariaDB-executed code as a comment. Verified: /*M!100000 … */ runs.
		if l.dialect.executableComments &&
			(strings.HasPrefix(rest, "/*!") || strings.HasPrefix(rest, "/*M!")) {
			l.pos += 3
			if l.pos < len(l.src) && l.src[l.pos] == '!' {
				l.pos++
			}
			l.skipVersionDigits()
			return true
		}
		l.skipBlockComment()
		return true
	}
	return false
}

// skipVersionDigits consumes the Mmmrr version an executable comment may carry.
func (l *lexer) skipVersionDigits() {
	for l.pos < len(l.src) && l.src[l.pos] >= '0' && l.src[l.pos] <= '9' {
		l.pos++
	}
}

// skipBlockComment consumes /* … */, nesting only where the dialect nests.
//
// An unterminated comment is not an error here: SQLite runs `SELECT 1 /* oops`
// as a statement, so refusing would differ from the server for no safety gain.
// What matters is that the text inside is never read as code.
func (l *lexer) skipBlockComment() {
	l.pos += 2
	depth := 1
	for l.pos < len(l.src) {
		rest := l.src[l.pos:]
		switch {
		case l.dialect.nestedBlockComments && strings.HasPrefix(rest, "/*"):
			depth++
			l.pos += 2
		case strings.HasPrefix(rest, "*/"):
			depth--
			l.pos += 2
			if depth == 0 {
				return
			}
		default:
			l.pos++
		}
	}
}

// atStringOrIdentifier reports whether a quoted region opens here.
func (l *lexer) atStringOrIdentifier() bool {
	switch c := l.src[l.pos]; {
	case c == '\'' || c == '"':
		return true
	case c == '`' && l.dialect.backtickQuotes:
		return true
	case c == '[' && l.dialect.bracketQuotes:
		return true
	case c == '$' && l.dialect.dollarQuotes:
		return l.dollarTag() != ""
	case (c == 'U' || c == 'u') && l.dialect.escapeStrings && l.atTokenStart():
		// PostgreSQL's U&'…' and U&"…" forms, which decode \XXXX escapes into
		// characters. They are refused rather than decoded: the guard records
		// an identifier's spelling to compare against a list of function
		// names, and a form whose spelling is not what the server resolves
		// defeats that comparison — U&"pg_re\0061d_file" read a file off the
		// server's disk while the two ordinary spellings were refused.
		// Decoding them correctly is possible; deciding that a workflow node
		// never needs one is simpler and cannot be subtly wrong.
		return l.pos+2 < len(l.src) && l.src[l.pos+1] == '&' &&
			(l.src[l.pos+2] == '\'' || l.src[l.pos+2] == '"')
	case (c == 'E' || c == 'e') && l.dialect.escapeStrings:
		// Only when the letter begins a token. Without this the trailing `e`
		// of an ordinary word is read as an escape-string prefix, and
		// `SELECT name'\';DROP TABLE t;--'` becomes one statement to the
		// lexer and two to the server — which is how this guard was broken
		// once, dropping a table and copying a secret out of another.
		return l.atTokenStart() && l.pos+1 < len(l.src) && l.src[l.pos+1] == '\''
	case (c == 'x' || c == 'X') && l.pos+1 < len(l.src) && l.src[l.pos+1] == '\'':
		return l.atTokenStart()
	}
	return false
}

// atTokenStart reports whether the cursor begins a word rather than sitting
// inside one.
func (l *lexer) atTokenStart() bool {
	return l.pos == 0 || !isWordByte(l.src[l.pos-1])
}

// dollarTag returns the opening $tag$ at the cursor, or empty when this is not
// one.
//
// A dollar followed by a digit is pgx's own placeholder, never a quote opener.
// Treating $1 as one would swallow everything after it and report a single
// statement for text that is two — and this server emits $1 itself, so the case
// is not hypothetical.
func (l *lexer) dollarTag() string {
	rest := l.src[l.pos:]
	if len(rest) < 2 {
		return ""
	}
	end := 1
	for end < len(rest) {
		c := rest[end]
		if c == '$' {
			return rest[:end+1]
		}
		if !isTagByte(c, end == 1) {
			return ""
		}
		end++
	}
	return ""
}

// consumeQuoted consumes one string or quoted identifier.
func (l *lexer) consumeQuoted() error {
	if l.dialect.dollarQuotes && l.src[l.pos] == '$' {
		return l.consumeDollarQuoted()
	}
	if c := l.src[l.pos]; (c == 'U' || c == 'u') && l.dialect.escapeStrings &&
		l.pos+1 < len(l.src) && l.src[l.pos+1] == '&' {
		return fmt.Errorf("%w: a unicode-escaped name or string (U&\"…\") is not sent, because "+
			"what it spells and what the server resolves are not the same text", ErrRefused)
	}

	// An E'' or x'' prefix is part of the literal's opening, not a word.
	backslash := l.backslashEscapes
	if c := l.src[l.pos]; c == 'E' || c == 'e' || c == 'x' || c == 'X' {
		// E'' always honours the backslash, whatever the server's setting.
		if (c == 'E' || c == 'e') && l.dialect.escapeStrings {
			backslash = true
		}
		l.pos++
	}

	open := l.src[l.pos]
	close := open
	// Which delimiters name something the server will resolve, as opposed to
	// delimiting data. Only the first kind is worth recording: a string
	// literal is a value, and reading one as a function name would refuse
	// `WHERE note = 'pg_read_file'`.
	identifier := open == '`' || open == '[' || (open == '"' && !l.dialect.doubleQuoteStrings)
	if open == '[' {
		// A bracketed identifier has no escape at all: the first ] closes it.
		close = ']'
		backslash = false
	}
	if open == '`' {
		// A backtick quotes a name, and a backslash inside one is literal in
		// every dialect that has them.
		backslash = false
	}
	if open == '"' && !l.dialect.doubleQuoteStrings {
		// Elsewhere a double quote delimits an identifier, where a backslash
		// is literal. On MySQL it delimits a string by default, and there a
		// backslash escapes — including escaping the closing quote.
		backslash = false
	}
	l.pos++
	start := l.pos

	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case backslash && c == '\\' && l.pos+1 < len(l.src):
			l.pos += 2
		case c == close:
			// A doubled delimiter is an escaped one and does not close.
			// Brackets have no doubling: ]] is a close followed by a stray ].
			if close != ']' && l.pos+1 < len(l.src) && l.src[l.pos+1] == close {
				l.pos += 2
				continue
			}
			if identifier {
				l.lastQuotedName = strings.ToUpper(l.src[start:l.pos])
			}
			l.pos++
			return nil
		default:
			l.pos++
		}
	}
	// Unterminated. The lexer and the server would now disagree about where
	// the text ends, which is exactly the condition under which a refusal is
	// the only safe answer.
	return fmt.Errorf("%w: a quoted string or name is never closed", ErrRefused)
}

func (l *lexer) consumeDollarQuoted() error {
	tag := l.dollarTag()
	l.pos += len(tag)
	// Closes only on the exact tag, so $a$ ; $b$ ; DROP TABLE t; $a$ is one
	// statement rather than three.
	if end := strings.Index(l.src[l.pos:], tag); end >= 0 {
		l.pos += end + len(tag)
		return nil
	}
	return fmt.Errorf("%w: a dollar-quoted string is never closed", ErrRefused)
}

// consumeBare consumes one byte of unquoted text, accumulating keywords.
func (l *lexer) consumeBare() {
	c := l.src[l.pos]
	if isWordByte(c) {
		l.word.WriteByte(upper(c))
		l.pos++
		return
	}
	l.endWord()
	// Parentheses and commas are structure the classifier needs to see; every
	// other punctuation byte is noise.
	if c == '(' || c == ')' || c == ',' {
		l.current.words = append(l.current.words, string(c))
	}
	l.pos++
}

func (l *lexer) endWord() {
	if l.word.Len() == 0 {
		return
	}
	l.current.words = append(l.current.words, l.word.String())
	l.word.Reset()
}

// endStatement closes the statement under construction, dropping an empty one.
//
// Empty segments are discarded rather than counted, so a trailing semicolon and
// a comment after it do not read as a second statement.
func (l *lexer) endStatement() {
	if len(l.current.words) > 0 {
		l.statements = append(l.statements, l.current)
	}
	l.current = statement{}
}

func isWordByte(c byte) bool {
	return c == '_' || c >= 0x80 ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func isTagByte(c byte, first bool) bool {
	if c == '_' || c >= 0x80 || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
		return true
	}
	return !first && c >= '0' && c <= '9'
}

func upper(c byte) byte {
	if c >= 'a' && c <= 'z' {
		return c - 32
	}
	return c
}

func startsWithSpace(s string) bool {
	if s == "" {
		// End of input after the dashes: nothing follows to be code.
		return true
	}
	switch s[0] {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}

func lineLength(rest string) int {
	if end := strings.IndexByte(rest, '\n'); end >= 0 {
		return end + 1
	}
	return len(rest)
}
