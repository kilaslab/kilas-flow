package jsrun

import (
	"reflect"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dop251/goja/ast"
)

// HTML-like comments (ECMAScript Annex B).
//
// In a script, as opposed to a module, V8 reads `<!--` as the start of a
// comment that runs to the end of its line, wherever it stands in code, and
// `-->` the same way when nothing but whitespace and comments comes before
// it on its line. n8n compiles a Code node's body as a script, so an HTML
// comment left in pasted code is harmless there. goja's parser has no such
// comments: `<!--` is a syntax error to it, or worse, `x<!--y` parses as
// `x < !--y` and computes something else.
//
// So before goja parses a body that could hold one, htmlComments reads the
// body as a lexer would and turns each HTML-like comment in code into spaces:
// byte for byte, so every offset, line and column stays where it was, and
// what goja reports points at the user's own text. A `<!--` inside a string,
// a template, a regular expression or another comment is left alone, as V8
// leaves it.
//
// The one thing a lexer cannot know without the parser is whether a `/`
// starts a regular expression or divides. htmlComments decides it from the
// token before, as parsers conventionally do, and records the regular
// expressions it read; once goja has parsed the blanked text, checkHTMLComments
// holds the ones goja found to exactly those. If they differ, the lexer read
// some part of the body differently from the parser, the comments it found
// cannot be trusted, and the body is refused rather than run on a guess.

// htmlScan is what the lexer found in a body.
type htmlScan struct {
	// blanked is the body with its HTML-like comments turned into spaces. It
	// is the body itself when there were none.
	blanked string
	// comments counts the comments blanked.
	comments int
	// regexps are the regular-expression literals the lexer read, as byte
	// ranges of the body, in order.
	regexps [][2]int
}

// mayHoldHTMLComment reports whether a body has text that could be an
// HTML-like comment at all: any `<!--`, or a `-->` with nothing but
// whitespace, or a block comment's end, before it on its line. Most bodies
// have neither and skip the lexer.
func mayHoldHTMLComment(source string) bool {
	if strings.Contains(source, "<!--") {
		return true
	}
	for rest, offset := source, 0; ; {
		at := strings.Index(rest, "-->")
		if at < 0 {
			return false
		}
		before := strings.TrimRightFunc(source[lineStartBefore(source, offset+at):offset+at], isLineWhiteSpace)
		if before == "" || strings.HasSuffix(before, "*/") {
			return true
		}
		rest, offset = rest[at+3:], offset+at+3
	}
}

// firstHTMLCommentLine is the user's line of the first `<!--` or `-->` in a
// body that has one.
func firstHTMLCommentLine(source string) int {
	first := len(source)
	for _, marker := range []string{"<!--", "-->"} {
		if at := strings.Index(source, marker); at >= 0 && at < first {
			first = at
		}
	}
	return strings.Count(source[:first], "\n") + 1
}

// lineStartBefore is the offset just after the last line terminator before
// offset, or 0.
func lineStartBefore(text string, offset int) int {
	for at := offset; at > 0; {
		r, size := utf8.DecodeLastRuneInString(text[:at])
		if isLineTerminator(r) {
			return at
		}
		at -= size
	}
	return 0
}

func isLineTerminator(r rune) bool {
	return r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029'
}

// isLineWhiteSpace is JavaScript's white space other than line terminators,
// as goja's own lexer reads it.
func isLineWhiteSpace(r rune) bool {
	switch r {
	case '\t', '\v', '\f', ' ', '\u00a0', '\ufeff':
		return true
	case '\n', '\r', '\u2028', '\u2029', '\u0085':
		return false
	}
	return unicode.IsSpace(r)
}

// isIdentifierRune is a rune that can continue a name: ASCII letters, digits,
// `$` and `_`, a `\` that starts a Unicode escape, and any other character
// that is not white space or a line terminator. The last is generous, but a
// character that is neither can only be a syntax error, which goja reports.
func isIdentifierRune(r rune) bool {
	if r < utf8.RuneSelf {
		return isIdentifierByte(byte(r)) || r == '\\' || r == '#'
	}
	return !isLineWhiteSpace(r) && !isLineTerminator(r)
}

// regexpAfterWord lists the words after which a `/` starts a regular
// expression: an operand is expected, not an operator.
var regexpAfterWord = map[string]bool{
	"return": true, "typeof": true, "instanceof": true, "in": true, "of": true, "new": true, "delete": true,
	"void": true, "throw": true, "case": true, "do": true, "else": true, "yield": true, "await": true, "extends": true,
}

// controlWord lists the words whose parenthesised head is followed by a
// statement, where a `/` starts a regular expression.
var controlWord = map[string]bool{"if": true, "while": true, "for": true, "with": true}

// blockAfterWord lists the words after which a `{` opens a block.
var blockAfterWord = map[string]bool{"else": true, "do": true, "try": true, "finally": true, "static": true}

// blockAfterPunctuator lists the punctuators after which a `{` opens a
// block; after any other, it opens an object literal. A lexer cannot always
// tell (a function expression's body is a block after `)` too), which is
// why checkHTMLComments holds its reading to goja's.
var blockAfterPunctuator = map[string]bool{")": true, ";": true, "{": true, "}": true, "=>": true, ":label": true}

// brace kinds on the lexer's stack.
const (
	braceBlock      = 'b'
	braceExpression = 'o'
	braceTemplate   = 't'
)

type htmlLexer struct {
	source string
	out    []byte
	at     int
	scan   htmlScan
	// regexp says a `/` here starts a regular expression.
	regexp bool
	// lineStart says only white space and comments came before this point on
	// its line, where `-->` starts a comment.
	lineStart bool
	// word is the name or keyword just read, and "" after any other token.
	// afterDot says the token before was `.` or `?.`, so a word here is a
	// property name, not a keyword.
	word     string
	afterDot bool
	// previous is the last punctuator read, or "" after a word or literal.
	previous string
	braces   []byte
	// questions counts, for the code outside any brace and inside each open
	// one, the `?` of conditional expressions still waiting for their `:`.
	// A `:` that answers none, in a block, ends a label or a case, and a `{`
	// after it opens a block.
	questions []int
	// parens records, for each open parenthesis, whether it is a control
	// statement's head.
	parens []bool
}

// htmlComments lexes a body and blanks its HTML-like comments.
func htmlComments(source string) htmlScan {
	lexer := &htmlLexer{source: source, regexp: true, lineStart: true, previous: "{", questions: []int{0}}
	lexer.run()
	if lexer.out != nil {
		lexer.scan.blanked = string(lexer.out)
	} else {
		lexer.scan.blanked = source
	}
	return lexer.scan
}

func (l *htmlLexer) rest() string { return l.source[l.at:] }

// comment blanks from here to the end of the line.
func (l *htmlLexer) comment() {
	if l.scan.comments == 0 {
		l.out = []byte(l.source)
	}
	l.scan.comments++
	for l.at < len(l.source) {
		r, size := utf8.DecodeRuneInString(l.rest())
		if isLineTerminator(r) {
			return
		}
		for index := range size {
			l.out[l.at+index] = ' '
		}
		l.at += size
	}
}

// token records that a token other than a comment was read: a punctuator,
// a word, or a literal when both are "".
func (l *htmlLexer) token(regexp bool, punctuator, word string) {
	l.regexp, l.lineStart, l.previous, l.word = regexp, false, punctuator, word
	l.afterDot = punctuator == "." || punctuator == "?."
}

func (l *htmlLexer) run() {
	for l.at < len(l.source) {
		r, size := utf8.DecodeRuneInString(l.rest())
		switch {
		case isLineTerminator(r):
			l.at += size
			l.lineStart = true
		case isLineWhiteSpace(r):
			l.at += size
		case strings.HasPrefix(l.rest(), "//"):
			l.skipLine()
		case strings.HasPrefix(l.rest(), "/*"):
			l.blockComment()
		case strings.HasPrefix(l.rest(), "<!--"):
			l.comment()
		case l.lineStart && strings.HasPrefix(l.rest(), "-->"):
			l.comment()
		case r == '\'' || r == '"':
			l.string(byte(r))
		case r == '`':
			l.at++
			l.template()
		case r == '/' && l.regexp:
			l.regularExpression()
		case r >= '0' && r <= '9', r == '.' && len(l.rest()) > 1 && l.rest()[1] >= '0' && l.rest()[1] <= '9':
			l.number()
		case isIdentifierRune(r):
			l.name()
		default:
			l.punctuator()
		}
	}
}

func (l *htmlLexer) skipLine() {
	for l.at < len(l.source) {
		r, size := utf8.DecodeRuneInString(l.rest())
		if isLineTerminator(r) {
			return
		}
		l.at += size
	}
}

// blockComment skips a /* */ comment. One that spans lines leaves the lexer
// at the start of a line, so a `-->` right after it starts a comment.
func (l *htmlLexer) blockComment() {
	end := strings.Index(l.source[l.at+2:], "*/")
	if end < 0 {
		l.at = len(l.source)
		return
	}
	text := l.source[l.at : l.at+2+end+2]
	if strings.ContainsAny(text, "\n\r\u2028\u2029") {
		l.lineStart = true
	}
	l.at += len(text)
}

// string skips a quoted string. An unescaped line terminator ends it, as a
// syntax error goja reports.
func (l *htmlLexer) string(quote byte) {
	l.at++
	for l.at < len(l.source) {
		r, size := utf8.DecodeRuneInString(l.rest())
		switch {
		case r == rune(quote):
			l.at++
			l.token(false, "", "")
			return
		case r == '\\':
			l.at++
			if strings.HasPrefix(l.rest(), "\r\n") {
				l.at += 2
			} else if l.at < len(l.source) {
				_, next := utf8.DecodeRuneInString(l.rest())
				l.at += next
			}
		case r == '\n' || r == '\r':
			l.token(false, "", "")
			return
		default:
			l.at += size
		}
	}
	l.token(false, "", "")
}

// open records an opened brace, or a template's `${`.
func (l *htmlLexer) open(kind byte) {
	l.braces = append(l.braces, kind)
	l.questions = append(l.questions, 0)
}

// template skips template text up to its closing backquote, or up to a `${`,
// where the lexer returns to code until the matching `}`.
func (l *htmlLexer) template() {
	for l.at < len(l.source) {
		switch {
		case l.source[l.at] == '`':
			l.at++
			l.token(false, "", "")
			return
		case l.source[l.at] == '\\':
			l.at += 2
		case strings.HasPrefix(l.rest(), "${"):
			l.at += 2
			l.open(braceTemplate)
			l.token(true, "${", "")
			return
		default:
			l.at++
		}
	}
}

// regularExpression skips a regular-expression literal and its flags, and
// records where it was.
func (l *htmlLexer) regularExpression() {
	start := l.at
	l.at++
	inClass := false
	for l.at < len(l.source) {
		r, size := utf8.DecodeRuneInString(l.rest())
		if isLineTerminator(r) {
			break
		}
		l.at += size
		if r == '\\' {
			if l.at < len(l.source) {
				_, next := utf8.DecodeRuneInString(l.rest())
				l.at += next
			}
			continue
		}
		if r == '[' {
			inClass = true
		} else if r == ']' {
			inClass = false
		} else if r == '/' && !inClass {
			for l.at < len(l.source) && isIdentifierByte(l.source[l.at]) {
				l.at++
			}
			break
		}
	}
	l.scan.regexps = append(l.scan.regexps, [2]int{start, l.at})
	l.token(false, "", "")
}

// number skips a numeric literal, generously: whatever letters, digits, dots
// and underscores follow, and a sign after a decimal exponent's e.
func (l *htmlLexer) number() {
	hex := len(l.rest()) > 1 && l.source[l.at] == '0' && strings.ContainsRune("xXbBoO", rune(l.rest()[1]))
	for l.at < len(l.source) {
		c := l.source[l.at]
		if isIdentifierByte(c) || c == '.' {
			l.at++
			continue
		}
		if (c == '+' || c == '-') && !hex && (l.source[l.at-1] == 'e' || l.source[l.at-1] == 'E') {
			l.at++
			continue
		}
		break
	}
	l.token(false, "", "")
}

// name reads a name or a keyword.
func (l *htmlLexer) name() {
	start := l.at
	for l.at < len(l.source) {
		r, size := utf8.DecodeRuneInString(l.rest())
		if !isIdentifierRune(r) {
			break
		}
		l.at += size
		if r == '\\' && l.at < len(l.source) {
			_, next := utf8.DecodeRuneInString(l.rest())
			l.at += next
		}
	}
	word := l.source[start:l.at]
	if l.afterDot {
		l.token(false, "", "")
		return
	}
	l.token(regexpAfterWord[word], "", word)
}

// punctuator reads one operator or bracket, and tracks the brackets that
// decide what a later `/` or `}` means.
func (l *htmlLexer) punctuator() {
	rest := l.rest()
	c := rest[0]
	switch {
	case c == '(':
		l.parens = append(l.parens, l.word != "" && controlWord[l.word])
		l.at++
		l.token(true, "(", "")
	case c == ')':
		control := false
		if len(l.parens) > 0 {
			control = l.parens[len(l.parens)-1]
			l.parens = l.parens[:len(l.parens)-1]
		}
		l.at++
		l.token(control, ")", "")
	case c == '[':
		l.at++
		l.token(true, "[", "")
	case c == ']':
		l.at++
		l.token(false, "]", "")
	case c == '{':
		kind := byte(braceExpression)
		if l.word != "" && (blockAfterWord[l.word] || !regexpAfterWord[l.word]) || l.word == "" && blockAfterPunctuator[l.previous] {
			kind = braceBlock
		}
		l.open(kind)
		l.at++
		l.token(true, "{", "")
	case c == '}':
		kind := byte(braceBlock)
		if len(l.braces) > 0 {
			kind = l.braces[len(l.braces)-1]
			l.braces = l.braces[:len(l.braces)-1]
			l.questions = l.questions[:len(l.questions)-1]
		}
		l.at++
		if kind == braceTemplate {
			l.template()
			return
		}
		l.token(kind == braceBlock, "}", "")
	case strings.HasPrefix(rest, "=>"):
		l.at += 2
		l.token(true, "=>", "")
	case strings.HasPrefix(rest, "++"), strings.HasPrefix(rest, "--"):
		l.at += 2
		l.token(false, rest[:2], "")
	case strings.HasPrefix(rest, "..."):
		l.at += 3
		l.token(true, "...", "")
	case strings.HasPrefix(rest, "?.") && !(len(rest) > 2 && rest[2] >= '0' && rest[2] <= '9'):
		l.at += 2
		l.token(false, "?.", "")
	case c == '.':
		l.at++
		l.token(false, ".", "")
	case strings.HasPrefix(rest, "??="):
		l.at += 3
		l.token(true, "??=", "")
	case strings.HasPrefix(rest, "??"):
		l.at += 2
		l.token(true, "??", "")
	case c == '?':
		l.questions[len(l.questions)-1]++
		l.at++
		l.token(true, "?", "")
	case c == ':':
		// A `:` answers a pending `?`, or follows a property's name in an
		// object literal, where a `{` after it is an object too; anywhere
		// else it ends a label or a case, and a `{` after it is a block.
		punctuator := ":"
		if pending := &l.questions[len(l.questions)-1]; *pending > 0 {
			*pending--
		} else if len(l.braces) == 0 || l.braces[len(l.braces)-1] == braceBlock {
			punctuator = ":label"
		}
		l.at++
		l.token(true, punctuator, "")
	case strings.HasPrefix(rest, "<<="):
		l.at += 3
		l.token(true, "<<=", "")
	case strings.HasPrefix(rest, "<<"), strings.HasPrefix(rest, "<="):
		l.at += 2
		l.token(true, rest[:2], "")
	case c == ';':
		l.at++
		l.token(true, ";", "")
	default:
		_, size := utf8.DecodeRuneInString(rest)
		l.at += size
		l.token(true, rest[:size], "")
	}
}

// checkHTMLComments holds the regular expressions goja found in a parsed body
// to the ones the lexer read. bodyStart is the offset of the body in the
// parsed text.
func checkHTMLComments(program *ast.Program, scan htmlScan, bodyStart int) bool {
	var found [][2]int
	eachNode(reflect.ValueOf(program), func(node any) {
		if literal, ok := node.(*ast.RegExpLiteral); ok {
			start := int(literal.Idx) - 1 - bodyStart
			found = append(found, [2]int{start, start + len(literal.Literal)})
		}
	})
	slices.SortFunc(found, func(a, b [2]int) int { return a[0] - b[0] })
	return slices.Equal(found, scan.regexps)
}

// restoreSources gives every function and class the text it was written
// with, HTML-like comments included, so Function.prototype.toString shows
// what the user wrote. goja keeps each one's source as a slice of the text
// it parsed; the slice ends where the node does, and the blanked text is the
// same length as the original.
func restoreSources(program *ast.Program, blanked, original string) {
	restore := func(source *string, end int) {
		start := end - len(*source)
		if start >= 0 && end <= len(blanked) && blanked[start:end] == *source {
			*source = original[start:end]
		}
	}
	eachNode(reflect.ValueOf(program), func(node any) {
		switch node := node.(type) {
		case *ast.FunctionLiteral:
			restore(&node.Source, int(node.Idx1())-1)
		case *ast.ArrowFunctionLiteral:
			restore(&node.Source, int(node.Idx1())-1)
		case *ast.ClassLiteral:
			restore(&node.Source, int(node.Idx1())-1)
		case *ast.ClassStaticBlock:
			restore(&node.Source, int(node.Block.Idx1())-1)
		}
	})
}

// eachNode calls visit with every AST node under value. The tree's depth is
// bounded by maxNesting before this runs, so the recursion is too.
func eachNode(value reflect.Value, visit func(any)) {
	switch value.Kind() {
	case reflect.Interface:
		if !value.IsNil() {
			eachNode(value.Elem(), visit)
		}
	case reflect.Pointer:
		if value.IsNil() || value.Type().Elem().PkgPath() != astPackage {
			return
		}
		visit(value.Interface())
		eachNode(value.Elem(), visit)
	case reflect.Struct:
		if value.Type().PkgPath() != astPackage {
			return
		}
		for index := 0; index < value.NumField(); index++ {
			if field := value.Type().Field(index); field.IsExported() && field.Name != "DeclarationList" {
				eachNode(value.Field(index), visit)
			}
		}
	case reflect.Slice:
		for index := 0; index < value.Len(); index++ {
			eachNode(value.Index(index), visit)
		}
	}
}
