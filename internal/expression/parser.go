package expression

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// This file is the front half of the evaluator: it turns one `{{ … }}` body
// into a syntax tree.
//
// The bodies are JavaScript expressions. n8n evaluates them as JavaScript, so
// a workflow written anywhere else in the ecosystem uses operators, ternaries,
// `??`, optional chaining, template literals and arrow functions, and a grammar
// that only understood a root followed by field reads rejected the majority of
// them. Widening the grammar to the expression subset below is the difference
// between "a workflow that parses here" and "a workflow that was written for
// n8n".
//
// What stays true from the closed grammar it replaces: nothing here is
// statement-level. There is no assignment, no `;`, no comma operator, no
// declaration and no access to the host — an expression can only read the roots
// and transform what it reads with pure functions from a closed list. Widening
// the syntax did not open a door to code; it removed a door that only blocked
// legitimate workflows.

// node is one syntax-tree node.
type node interface{}

type literalNode struct{ value any }

// identNode is a bare name: a root (`$json`), a global (`JSON`, `Math`) or an
// arrow function's parameter.
type identNode struct{ name string }

type memberNode struct {
	object   node
	name     string
	optional bool
}

type indexNode struct {
	object   node
	index    node
	optional bool
}

type callNode struct {
	callee   node
	args     []node
	optional bool
}

type unaryNode struct {
	op      string
	operand node
}

type binaryNode struct {
	op    string
	left  node
	right node
}

type conditionalNode struct{ test, then, otherwise node }

type arrayNode struct{ elements []node }

type objectNode struct {
	keys   []string
	values []node
}

// templateNode is a backtick literal. texts holds the literal parts and exprs
// the interpolations, so len(texts) == len(exprs)+1.
type templateNode struct {
	texts []string
	exprs []node
}

type arrowNode struct {
	params []string
	body   node
}

// nodeRefNode is the `$('Node')` form. The argument is an expression because
// `$('Node ' + suffix)` is legal JavaScript, even though a literal is the
// overwhelmingly common case.
type nodeRefNode struct{ name node }

// spreadNode is `...name` in an argument list, which n8n workflows write as
// `Math.max(...items)` and `$input.all()`-style helpers take.
type spreadNode struct{ value node }

// ---- tokens ----

type tokenKind int

const (
	tokenEOF tokenKind = iota
	tokenNumber
	tokenString
	tokenTemplate
	tokenIdent
	tokenPunct
)

type token struct {
	kind   tokenKind
	text   string
	number float64
	texts  []string
	exprs  []string
	pos    int
}

func lex(source string) ([]token, error) {
	tokens := make([]token, 0, 16)
	position := 0
	for position < len(source) {
		char := source[position]
		switch {
		case char == ' ' || char == '\t' || char == '\n' || char == '\r':
			position++
		case char >= '0' && char <= '9':
			next, number, err := lexNumber(source, position)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, token{kind: tokenNumber, text: source[position:next], number: number, pos: position})
			position = next
		case char == '\'' || char == '"':
			text, next, err := scanString(source, position, char)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, token{kind: tokenString, text: text, pos: position})
			position = next
		case char == '`':
			texts, exprs, next, err := scanTemplate(source, position)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, token{kind: tokenTemplate, texts: texts, exprs: exprs, pos: position})
			position = next
		case char == '$' || char == '_' || char >= utf8.RuneSelf || (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z'):
			next := position
			for next < len(source) {
				character, size := utf8.DecodeRuneInString(source[next:])
				if !isIdentPart(character) {
					break
				}
				next += size
			}
			if next == position {
				// A `$` that is not followed by an identifier character is the
				// `$('Name')` root on its own.
				next = position + 1
			}
			tokens = append(tokens, token{kind: tokenIdent, text: source[position:next], pos: position})
			position = next
		default:
			operator, err := lexPunct(source, position)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, token{kind: tokenPunct, text: operator, pos: position})
			position += len(operator)
		}
	}
	tokens = append(tokens, token{kind: tokenEOF, pos: len(source)})
	return tokens, nil
}

// multiCharacterOperators is ordered longest first, so `===` never lexes as
// `==` followed by `=`.
var multiCharacterOperators = []string{"...", "===", "!==", "?.", "??", "=>", "==", "!=", "<=", ">=", "&&", "||"}

func lexPunct(source string, position int) (string, error) {
	for _, operator := range multiCharacterOperators {
		if strings.HasPrefix(source[position:], operator) {
			return operator, nil
		}
	}
	char := source[position]
	if strings.IndexByte("+-*/%!<>()[]{}.?:,", char) >= 0 {
		return string(char), nil
	}
	if char == '=' {
		return "", fmt.Errorf("expression cannot assign: %q needs a value it does not have here", source[position:])
	}
	if char == ';' {
		return "", fmt.Errorf("expression cannot contain a statement separator ;")
	}
	return "", fmt.Errorf("expression contains unsupported character %q", string(char))
}

func isIdentStart(char rune) bool {
	return char == '$' || char == '_' || unicode.IsLetter(char)
}

func isIdentPart(char rune) bool {
	return isIdentStart(char) || unicode.IsDigit(char)
}

func lexNumber(source string, position int) (int, float64, error) {
	next := position
	for next < len(source) && source[next] >= '0' && source[next] <= '9' {
		next++
	}
	if next < len(source) && source[next] == '.' {
		next++
		for next < len(source) && source[next] >= '0' && source[next] <= '9' {
			next++
		}
	}
	if next < len(source) && (source[next] == 'e' || source[next] == 'E') {
		exponent := next + 1
		if exponent < len(source) && (source[exponent] == '+' || source[exponent] == '-') {
			exponent++
		}
		if exponent < len(source) && source[exponent] >= '0' && source[exponent] <= '9' {
			for exponent < len(source) && source[exponent] >= '0' && source[exponent] <= '9' {
				exponent++
			}
			next = exponent
		}
	}
	number, err := strconv.ParseFloat(source[position:next], 64)
	if err != nil {
		return 0, 0, fmt.Errorf("expression has an unreadable number %q", source[position:next])
	}
	return next, number, nil
}

// scanString reads one quoted literal, applying JavaScript's escape rules: an
// unknown escape is the character itself, not a Go error.
func scanString(source string, position int, quote byte) (string, int, error) {
	var builder strings.Builder
	index := position + 1
	for index < len(source) {
		char := source[index]
		switch char {
		case quote:
			return builder.String(), index + 1, nil
		case '\\':
			value, next, err := scanEscape(source, index)
			if err != nil {
				return "", 0, err
			}
			builder.WriteString(value)
			index = next
		case '\n':
			return "", 0, fmt.Errorf("expression has an unterminated string")
		default:
			builder.WriteByte(char)
			index++
		}
	}
	return "", 0, fmt.Errorf("expression has an unterminated string")
}

func scanEscape(source string, position int) (string, int, error) {
	if position+1 >= len(source) {
		return "", 0, fmt.Errorf("expression ends with a dangling backslash")
	}
	escape := source[position+1]
	switch escape {
	case 'n':
		return "\n", position + 2, nil
	case 't':
		return "\t", position + 2, nil
	case 'r':
		return "\r", position + 2, nil
	case 'b':
		return "\b", position + 2, nil
	case 'f':
		return "\f", position + 2, nil
	case 'v':
		return "\v", position + 2, nil
	case '0':
		return "\x00", position + 2, nil
	case 'u':
		if position+2 < len(source) && source[position+2] == '{' {
			end := strings.IndexByte(source[position+3:], '}')
			if end < 0 {
				return "", 0, fmt.Errorf("expression has an unclosed \\u{")
			}
			code, err := strconv.ParseUint(source[position+3:position+3+end], 16, 32)
			if err != nil {
				return "", 0, fmt.Errorf("expression has an unreadable \\u escape")
			}
			return string(rune(code)), position + 3 + end + 1, nil
		}
		if position+6 > len(source) {
			return "", 0, fmt.Errorf("expression has a short \\u escape")
		}
		code, err := strconv.ParseUint(source[position+2:position+6], 16, 32)
		if err != nil {
			return "", 0, fmt.Errorf("expression has an unreadable \\u escape")
		}
		return string(rune(code)), position + 6, nil
	case 'x':
		if position+4 > len(source) {
			return "", 0, fmt.Errorf("expression has a short \\x escape")
		}
		code, err := strconv.ParseUint(source[position+2:position+4], 16, 32)
		if err != nil {
			return "", 0, fmt.Errorf("expression has an unreadable \\x escape")
		}
		return string(rune(code)), position + 4, nil
	default:
		// JavaScript drops the backslash for anything else: `'\d'` is `d`.
		_, size := utf8.DecodeRuneInString(source[position+1:])
		return source[position+1 : position+1+size], position + 1 + size, nil
	}
}

// scanTemplate reads a backtick literal and returns its literal parts and the
// source of each `${ … }` interpolation.
func scanTemplate(source string, position int) ([]string, []string, int, error) {
	texts := []string{}
	exprs := []string{}
	var builder strings.Builder
	index := position + 1
	for index < len(source) {
		char := source[index]
		switch char {
		case '`':
			texts = append(texts, builder.String())
			return texts, exprs, index + 1, nil
		case '\\':
			value, next, err := scanEscape(source, index)
			if err != nil {
				return nil, nil, 0, err
			}
			builder.WriteString(value)
			index = next
		case '$':
			if index+1 < len(source) && source[index+1] == '{' {
				texts = append(texts, builder.String())
				builder.Reset()
				body, next, err := scanInterpolation(source, index+2)
				if err != nil {
					return nil, nil, 0, err
				}
				exprs = append(exprs, body)
				index = next
				continue
			}
			builder.WriteByte(char)
			index++
		default:
			builder.WriteByte(char)
			index++
		}
	}
	return nil, nil, 0, fmt.Errorf("expression has an unterminated template literal")
}

// scanInterpolation reads the body of a `${ … }`, respecting nested strings,
// templates and braces so `${{a:{b:1}}}` and `${\`x${y}\`}` both work.
func scanInterpolation(source string, position int) (string, int, error) {
	depth := 1
	index := position
	for index < len(source) {
		switch source[index] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return source[position:index], index + 1, nil
			}
		case '\'', '"':
			_, next, err := scanString(source, index, source[index])
			if err != nil {
				return "", 0, err
			}
			index = next
			continue
		case '`':
			_, _, next, err := scanTemplate(source, index)
			if err != nil {
				return "", 0, err
			}
			index = next
			continue
		}
		index++
	}
	return "", 0, fmt.Errorf("expression has an unclosed ${")
}

// ---- parser ----

type parser struct {
	source string
	tokens []token
	pos    int
}

func parseExpression(body string) (node, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, fmt.Errorf("expression is empty")
	}
	tokens, err := lex(body)
	if err != nil {
		return nil, err
	}
	parser := &parser{source: body, tokens: tokens}
	expression, err := parser.parseConditional()
	if err != nil {
		return nil, err
	}
	if parser.peek().kind != tokenEOF {
		return nil, fmt.Errorf("expression contains unsupported syntax at %q", strings.TrimSpace(parser.source[parser.peek().pos:]))
	}
	return expression, nil
}

func (p *parser) peek() token { return p.tokens[p.pos] }

func (p *parser) at(text string) bool { return p.peek().kind == tokenPunct && p.peek().text == text }

func (p *parser) accept(text string) bool {
	if p.at(text) {
		p.pos++
		return true
	}
	return false
}

func (p *parser) expect(text string) error {
	if p.accept(text) {
		return nil
	}
	return fmt.Errorf("expression expected %q at %q", text, strings.TrimSpace(p.source[p.peek().pos:]))
}

func (p *parser) parseConditional() (node, error) {
	test, err := p.parseNullish()
	if err != nil {
		return nil, err
	}
	if !p.accept("?") {
		return test, nil
	}
	then, err := p.parseConditional()
	if err != nil {
		return nil, err
	}
	if err := p.expect(":"); err != nil {
		return nil, err
	}
	otherwise, err := p.parseConditional()
	if err != nil {
		return nil, err
	}
	return conditionalNode{test: test, then: then, otherwise: otherwise}, nil
}

// parseBinary is the precedence climber every level above unary uses.
func (p *parser) parseBinary(next func() (node, error), operators ...string) (node, error) {
	left, err := next()
	if err != nil {
		return nil, err
	}
	for {
		matched := ""
		for _, operator := range operators {
			if p.at(operator) {
				matched = operator
				break
			}
		}
		if matched == "" {
			return left, nil
		}
		p.pos++
		right, err := next()
		if err != nil {
			return nil, err
		}
		left = binaryNode{op: matched, left: left, right: right}
	}
}

func (p *parser) parseNullish() (node, error) {
	return p.parseBinary(p.parseOr, "??")
}

func (p *parser) parseOr() (node, error) {
	return p.parseBinary(p.parseAnd, "||")
}

func (p *parser) parseAnd() (node, error) {
	return p.parseBinary(p.parseEquality, "&&")
}

func (p *parser) parseEquality() (node, error) {
	return p.parseBinary(p.parseRelational, "===", "!==", "==", "!=")
}

func (p *parser) parseRelational() (node, error) {
	return p.parseBinary(p.parseAdditive, "<=", ">=", "<", ">")
}

func (p *parser) parseAdditive() (node, error) {
	return p.parseBinary(p.parseMultiplicative, "+", "-")
}

func (p *parser) parseMultiplicative() (node, error) {
	return p.parseBinary(p.parseUnary, "*", "/", "%")
}

func (p *parser) parseUnary() (node, error) {
	if p.at("!") || p.at("-") || p.at("+") {
		operator := p.peek().text
		p.pos++
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return unaryNode{op: operator, operand: operand}, nil
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() (node, error) {
	object, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		switch {
		case p.accept("."):
			name, err := p.parseMemberName()
			if err != nil {
				return nil, err
			}
			object = memberNode{object: object, name: name}
		case p.accept("?."):
			if p.accept("(") {
				args, err := p.parseArguments()
				if err != nil {
					return nil, err
				}
				if err := checkArity(callName(object), args); err != nil {
					return nil, err
				}
				object = callNode{callee: object, args: args, optional: true}
				continue
			}
			if p.accept("[") {
				index, err := p.parseConditional()
				if err != nil {
					return nil, err
				}
				if err := p.expect("]"); err != nil {
					return nil, err
				}
				object = indexNode{object: object, index: index, optional: true}
				continue
			}
			name, err := p.parseMemberName()
			if err != nil {
				return nil, err
			}
			object = memberNode{object: object, name: name, optional: true}
		case p.accept("["):
			index, err := p.parseConditional()
			if err != nil {
				return nil, err
			}
			if err := p.expect("]"); err != nil {
				return nil, err
			}
			object = indexNode{object: object, index: index}
		case p.accept("("):
			args, err := p.parseArguments()
			if err != nil {
				return nil, err
			}
			if err := checkArity(callName(object), args); err != nil {
				return nil, err
			}
			object = callNode{callee: object, args: args}
		default:
			return object, nil
		}
	}
}

// parseMemberName reads the property after `.`, which may be a reserved word:
// `$json.if`, `$json.for` and `$json.in` are ordinary keys on real payloads.
func (p *parser) parseMemberName() (string, error) {
	next := p.peek()
	if next.kind != tokenIdent && next.kind != tokenNumber {
		return "", fmt.Errorf("expression has an empty field name at %q", strings.TrimSpace(p.source[next.pos:]))
	}
	p.pos++
	return next.text, nil
}

func (p *parser) parseArguments() ([]node, error) {
	args := []node{}
	if p.accept(")") {
		return args, nil
	}
	for {
		if p.accept("...") {
			value, err := p.parseConditional()
			if err != nil {
				return nil, err
			}
			args = append(args, spreadNode{value: value})
		} else {
			value, err := p.parseConditional()
			if err != nil {
				return nil, err
			}
			args = append(args, value)
		}
		if p.accept(")") {
			return args, nil
		}
		if err := p.expect(","); err != nil {
			return nil, err
		}
		if p.accept(")") {
			return args, nil
		}
	}
}

func (p *parser) parsePrimary() (node, error) {
	next := p.peek()
	switch next.kind {
	case tokenNumber:
		p.pos++
		return literalNode{value: next.number}, nil
	case tokenString:
		p.pos++
		return literalNode{value: next.text}, nil
	case tokenTemplate:
		p.pos++
		return p.templateExpression(next)
	case tokenIdent:
		return p.parseIdentifier()
	}
	if p.accept("(") {
		if p.arrowAhead() {
			return p.parseArrow("(")
		}
		inner, err := p.parseConditional()
		if err != nil {
			return nil, err
		}
		if err := p.expect(")"); err != nil {
			return nil, err
		}
		return inner, nil
	}
	if p.accept("[") {
		elements := []node{}
		for !p.accept("]") {
			element, err := p.parseConditional()
			if err != nil {
				return nil, err
			}
			elements = append(elements, element)
			if p.accept("]") {
				break
			}
			if err := p.expect(","); err != nil {
				return nil, err
			}
		}
		return arrayNode{elements: elements}, nil
	}
	if p.accept("{") {
		object := objectNode{}
		for !p.accept("}") {
			key := p.peek()
			var name string
			switch key.kind {
			case tokenIdent, tokenString:
				name = key.text
				p.pos++
			case tokenNumber:
				name = jsNumber(key.number)
				p.pos++
			default:
				return nil, fmt.Errorf("expression has an unreadable object key at %q", strings.TrimSpace(p.source[key.pos:]))
			}
			if err := p.expect(":"); err != nil {
				return nil, err
			}
			value, err := p.parseConditional()
			if err != nil {
				return nil, err
			}
			object.keys = append(object.keys, name)
			object.values = append(object.values, value)
			if p.accept("}") {
				break
			}
			if err := p.expect(","); err != nil {
				return nil, err
			}
		}
		return object, nil
	}
	return nil, fmt.Errorf("expression contains unsupported syntax at %q", strings.TrimSpace(p.source[next.pos:]))
}

func (p *parser) templateExpression(next token) (node, error) {
	template := templateNode{texts: next.texts}
	for index, source := range next.exprs {
		expression, err := parseExpression(source)
		if err != nil {
			return nil, fmt.Errorf("template placeholder %d: %w", index+1, err)
		}
		template.exprs = append(template.exprs, expression)
	}
	return template, nil
}

func (p *parser) parseIdentifier() (node, error) {
	next := p.peek()
	p.pos++
	switch next.text {
	case "true":
		return literalNode{value: true}, nil
	case "false":
		return literalNode{value: false}, nil
	case "null":
		return literalNode{value: nil}, nil
	case "undefined":
		return literalNode{value: Undefined}, nil
	}
	// A single arrow parameter: `t => t.json`.
	if p.at("=>") {
		p.pos++
		body, err := p.parseConditional()
		if err != nil {
			return nil, err
		}
		return arrowNode{params: []string{next.text}, body: body}, nil
	}
	if next.text == "$" {
		if !p.at("(") {
			return nil, fmt.Errorf("$ needs a node name: write $('Node Name')")
		}
		p.pos++
		args, err := p.parseArguments()
		if err != nil {
			return nil, err
		}
		if len(args) != 1 {
			return nil, fmt.Errorf("$(…) takes one node name, got %d", len(args))
		}
		return nodeRefNode{name: args[0]}, nil
	}
	return identNode{name: next.text}, nil
}

// arrowAhead reports whether the `(` just consumed opens an arrow function's
// parameter list rather than a parenthesised expression.
func (p *parser) arrowAhead() bool {
	depth := 1
	for index := p.pos; index < len(p.tokens); index++ {
		next := p.tokens[index]
		if next.kind == tokenPunct {
			switch next.text {
			case "(", "[", "{":
				depth++
			case ")", "]", "}":
				depth--
				if depth == 0 {
					return index+1 < len(p.tokens) && p.tokens[index+1].kind == tokenPunct && p.tokens[index+1].text == "=>"
				}
			}
		}
		if next.kind == tokenEOF {
			return false
		}
	}
	return false
}

func (p *parser) parseArrow(opening string) (node, error) {
	params := []string{}
	if p.accept(")") {
		// No parameters: `() => …`.
	} else {
		for {
			param := p.peek()
			if param.kind != tokenIdent {
				return nil, fmt.Errorf("expression arrow function parameters must be names, got %q", strings.TrimSpace(p.source[param.pos:]))
			}
			p.pos++
			params = append(params, param.text)
			if p.accept(")") {
				break
			}
			if err := p.expect(","); err != nil {
				return nil, err
			}
		}
	}
	if err := p.expect("=>"); err != nil {
		return nil, err
	}
	body, err := p.parseConditional()
	if err != nil {
		return nil, err
	}
	return arrowNode{params: params, body: body}, nil
}
