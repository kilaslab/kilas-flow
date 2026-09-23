package jsrun

import (
	"fmt"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strings"
	"unicode"

	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/file"
	"github.com/dop251/goja/parser"
	"github.com/dop251/goja/token"
)

// Analysis is what a body uses, found without running it.
type Analysis struct {
	// Requires lists the modules the body names in a literal require(),
	// with any "node:" prefix removed, sorted.
	Requires []string
	// DynamicRequire reports a require() whose argument is not a literal,
	// which can only be checked when it runs.
	DynamicRequire bool
	// UsesLuxon reports any reference to Luxon, so the library can be loaded
	// before the clock starts instead of on first use.
	UsesLuxon bool
	// Unsupported lists every construct the runtime refuses.
	Unsupported []Unsupported
}

// The bounds on a body's shape, checked before goja does anything costly.
//
// goja's parser and compiler recurse on nesting with no limit of their own,
// and a Go stack overflow is fatal: it cannot be recovered, so it would take
// the server down, not just one run. Some nesting is also quadratic: 20,000
// nested arrow functions take seconds to parse, and 30,000 nested blocks
// seconds to compile, all of it outside any time limit. Real code comes
// nowhere near these bounds; the largest Code node among the 500 most-viewed
// n8n templates is under 30 KiB.
const (
	// MaxSourceBytes bounds a body's length, which bounds how deep anything in
	// it can nest while it is parsed.
	MaxSourceBytes = 128 << 10
	// maxArrowFunctions bounds arrow functions, which are nested without
	// brackets and parse in quadratic time. Counted in the raw text, where
	// the count can only err high, so it cannot be dodged.
	maxArrowFunctions = 1000
	// maxNesting bounds the depth of the parsed tree, checked before it is
	// compiled.
	maxNesting = 1000
	// maxFoldCost bounds the work goja does folding one constant expression
	// while it compiles, in the units foldCost counts. A
	// ||, && or ?? with a constant left side evaluates that side once to ask
	// whether the operator is constant and again to emit it, and an operator
	// above it asks and emits in turn, so the work multiplies per level: 40
	// levels of `0 || 0` would hold a core for hours, and 147 bytes spell
	// them. Literals joined by other operators fold in polynomial time, so a
	// query built from hundreds of string literals stays far below it. The
	// worst expressions it admits (18 levels of `0 || 0`, or 11 with an
	// arithmetic operator between each) compile in a few milliseconds.
	maxFoldCost = 1 << 21
	// maxBigIntBits bounds a constant BigInt expression goja would work out
	// while compiling. BigInt arithmetic has no size limit, and a folded one
	// runs in setup, which nothing interrupts: `1n << 68719476736n` is an
	// 8 GiB allocation and `3n ** 3000000000n` hours of a core. A million
	// bits is a 300,000-digit number, far past any real constant.
	maxBigIntBits = 1 << 20
)

// checkSourceSize refuses a body too large to parse safely.
func checkSourceSize(source string) error {
	var found []Unsupported
	if len(source) > MaxSourceBytes {
		found = append(found, Unsupported{Subject: fmt.Sprintf("is longer than %s", byteSize(MaxSourceBytes))})
	}
	if strings.Count(source, "=>") > maxArrowFunctions {
		found = append(found, Unsupported{Subject: fmt.Sprintf("has more than %d arrow functions", maxArrowFunctions)})
	}
	if len(found) > 0 {
		return &UnsupportedError{Found: found}
	}
	return nil
}

// shippedModules are the modules require() can return. There is no npm and no
// module directory, so this list is the whole of it.
var shippedModules = shippedModuleNames()

// luxonNames are the identifiers that mean a body uses Luxon. Loading it when
// it is not needed costs nothing but a few milliseconds of setup, which is
// not charged, so the list errs on the generous side.
var luxonNames = map[string]bool{
	"DateTime": true, "Duration": true, "Interval": true, "Info": true, "Settings": true,
	"luxon": true, "$now": true, "$today": true,
}

// Analyze reports what a Code node's JavaScript uses, and refuses what the
// runtime cannot run faithfully, without running any of it. It is what the
// importer and the node's validation call, so both say what a run would.
//
// The error is a *SyntaxError for code that does not parse, and an
// *UnsupportedError (errors.Is ErrUnsupported) for a refused construct. The
// Analysis is returned alongside an UnsupportedError.
func Analyze(source string, mode Mode) (Analysis, error) {
	prepared, err := sharedAnalyses.prepare(source, mode)
	if prepared == nil {
		return Analysis{}, err
	}
	return prepared.analysis, err
}

// parseWrapped parses the wrapped body. Source maps are never loaded: goja's
// parser would otherwise read a file named by a trailing sourceMappingURL
// comment, and a script could name /dev/zero.
func parseWrapped(w wrapped) (*ast.Program, error) {
	program, err := parser.ParseFile(nil, sourceName, w.text, 0, parser.WithDisableSourceMaps)
	if err != nil {
		return nil, classifyParseError(err, w)
	}
	return program, nil
}

// classifyParseError tells a construct the engine does not support apart
// from a plain mistake. goja reports both as "unexpected token", but a user
// who wrote `for await` has written valid JavaScript and deserves to be told
// it is the runtime that refuses it.
func classifyParseError(err error, w wrapped) error {
	list, ok := err.(parser.ErrorList)
	if !ok || len(list) == 0 {
		return &SyntaxError{Message: err.Error()}
	}
	first := list[0]
	line := w.userLine(first.Position.Line)
	if subject := unsupportedAt(w.text, offsetOf(w.text, first.Position.Line, first.Position.Column), first.Message); subject != "" {
		return &UnsupportedError{Found: []Unsupported{{Subject: subject, Line: line}}}
	}
	return &SyntaxError{Message: first.Message, Line: line, Column: first.Position.Column}
}

// offsetOf turns a 1-based line and column into a byte offset.
func offsetOf(text string, line, column int) int {
	offset := 0
	for current := 1; current < line; current++ {
		next := strings.IndexByte(text[offset:], '\n')
		if next < 0 {
			return len(text)
		}
		offset += next + 1
	}
	offset += column - 1
	if offset > len(text) {
		return len(text)
	}
	return offset
}

// unsupportedAt names the unsupported construct a parse error points at, or
// answers "" for an ordinary syntax error.
func unsupportedAt(text string, offset int, message string) string {
	rest := text[offset:]
	before := strings.TrimRightFunc(text[:offset], unicode.IsSpace)
	switch {
	case strings.HasPrefix(rest, "await") && endsWithWord(strings.TrimSuffix(before, "("), "for"):
		return "uses for await"
	case strings.Contains(message, "reserved word") && (startsWithWord(rest, "import") || startsWithWord(rest, "export")):
		return "uses import or export (use require() instead)"
	case strings.HasPrefix(rest, "*") && endsWithWord(before, "async"),
		strings.HasSuffix(before, "*") && endsWithWord(strings.TrimSuffix(before, "*"), "async"):
		// goja points at the star of `async *method()`, or at the name after it.
		return "uses an async generator"
	}
	return ""
}

func endsWithWord(text, word string) bool {
	text = strings.TrimRightFunc(text, unicode.IsSpace)
	if !strings.HasSuffix(text, word) {
		return false
	}
	head := text[:len(text)-len(word)]
	return head == "" || !isIdentifierByte(head[len(head)-1])
}

func startsWithWord(text, word string) bool {
	return strings.HasPrefix(text, word) && (len(text) == len(word) || !isIdentifierByte(text[len(word)]))
}

func isIdentifierByte(b byte) bool {
	return b == '_' || b == '$' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// escapesWrapper is the failure for code that closes the function it runs in.
// Such code would run part of itself during setup, outside its time limit.
func escapesWrapper(line int) error {
	return &SyntaxError{Message: "the code closes the function it runs in; remove the extra closing brackets", Line: line}
}

// checkShape proves the parsed program is exactly the wrapper around the
// user's body: one statement, the wrapper function, whose body function
// opens and closes at the braces the wrapper wrote. Code that balances its
// own brackets to close the wrapper early and reopen it has a different
// shape, whatever it looks like as text.
func checkShape(program *ast.Program, w wrapped) (*ast.FunctionLiteral, error) {
	if len(program.Body) != 1 {
		return nil, escapesWrapper(0)
	}
	statement, ok := program.Body[0].(*ast.ExpressionStatement)
	if !ok {
		return nil, escapesWrapper(0)
	}
	outer, ok := statement.Expression.(*ast.FunctionLiteral)
	if !ok || outer.Body == nil || int(outer.Body.RightBrace) != w.outerClose+1 || len(outer.Body.List) != 1 {
		return nil, escapesWrapper(0)
	}
	ret, ok := outer.Body.List[0].(*ast.ReturnStatement)
	if !ok {
		return nil, escapesWrapper(0)
	}
	call, ok := ret.Argument.(*ast.CallExpression)
	if !ok {
		return nil, escapesWrapper(0)
	}
	member, ok := call.Callee.(*ast.DotExpression)
	if !ok {
		return nil, escapesWrapper(0)
	}
	body, ok := member.Left.(*ast.FunctionLiteral)
	if !ok || !body.Async || body.Body == nil || int(body.Body.LeftBrace) != w.open+1 {
		return nil, escapesWrapper(0)
	}
	if int(body.Body.RightBrace) != w.close+1 {
		return nil, escapesWrapper(w.lineAt(int(body.Body.RightBrace) - 1))
	}
	return body, nil
}

// inspect walks the body and records what it uses.
func inspect(body *ast.FunctionLiteral, w wrapped) Analysis {
	walker := &inspector{w: w, requires: map[string]bool{}, seen: map[Unsupported]bool{}, folds: map[ast.Expression]fold{}}
	walker.walk(reflect.ValueOf(body.Body), true, 0)
	for name := range walker.requires {
		walker.analysis.Requires = append(walker.analysis.Requires, name)
	}
	sort.Strings(walker.analysis.Requires)
	return walker.analysis
}

var astPackage = reflect.TypeOf(ast.Program{}).PkgPath()

// inspector walks goja's AST by reflection, because the package has no
// visitor, and a hand-written switch over every node type would silently
// stop descending into a node type added in a later goja.
type inspector struct {
	w        wrapped
	analysis Analysis
	requires map[string]bool
	seen     map[Unsupported]bool
	tooDeep  bool
	// folds memoises foldCost, so a chain is measured once however many of
	// its nodes the walk visits.
	folds map[ast.Expression]fold
}

// walk descends through AST values. bodyThis reports whether `this` here is
// the body's own `this`: a nested function or class has its own, an arrow
// function shares its parent's. depth counts nodes above this one, and a tree
// deeper than maxNesting is refused, and not descended any further, so the
// walk itself stays shallow.
func (in *inspector) walk(value reflect.Value, bodyThis bool, depth int) {
	switch value.Kind() {
	case reflect.Interface:
		if !value.IsNil() {
			in.walk(value.Elem(), bodyThis, depth)
		}
	case reflect.Pointer:
		if value.IsNil() || value.Type().Elem().PkgPath() != astPackage {
			return
		}
		if node, ok := value.Interface().(ast.Node); ok {
			if depth++; depth > maxNesting {
				if !in.tooDeep {
					in.tooDeep = true
					in.refuse(fmt.Sprintf("nests more than %d levels deep", maxNesting), node.Idx0())
				}
				return
			}
			bodyThis = in.visit(node, bodyThis)
		}
		in.walk(value.Elem(), bodyThis, depth)
	case reflect.Struct:
		if value.Type().PkgPath() != astPackage {
			return
		}
		for index := 0; index < value.NumField(); index++ {
			field := value.Type().Field(index)
			// DeclarationList repeats declarations already in the body.
			if !field.IsExported() || field.Name == "DeclarationList" {
				continue
			}
			in.walk(value.Field(index), bodyThis, depth)
		}
	case reflect.Slice:
		for index := 0; index < value.Len(); index++ {
			in.walk(value.Index(index), bodyThis, depth)
		}
	}
}

// visit records one node and answers whether `this` below it is still the
// body's.
func (in *inspector) visit(node ast.Node, bodyThis bool) bool {
	switch node := node.(type) {
	case *ast.FunctionLiteral:
		if node.Async && node.Generator {
			in.refuse("uses an async generator", node.Idx0())
		}
		return false
	case *ast.ClassLiteral:
		return false
	case *ast.RegExpLiteral:
		in.regexp(node.Pattern, node.Flags, node.Idx0())
	case *ast.CallExpression:
		in.call(node.Callee, node.ArgumentList, node.Idx0())
	case *ast.NewExpression:
		if isIdentifier(node.Callee, "RegExp") {
			in.regexpConstructor(node.ArgumentList, node.Idx0())
		}
	case *ast.DotExpression:
		if bodyThis && isThis(node.Left) {
			in.thisMember(string(node.Identifier.Name), node.Idx0())
		}
	case *ast.BracketExpression:
		if literal, ok := node.Member.(*ast.StringLiteral); ok && bodyThis && isThis(node.Left) {
			in.thisMember(string(literal.Value), node.Idx0())
		}
	case *ast.Identifier:
		if luxonNames[string(node.Name)] {
			in.analysis.UsesLuxon = true
		}
	case *ast.BinaryExpression, *ast.UnaryExpression:
		cost := in.foldCost(node.(ast.Expression))
		if cost.ask+cost.emit > maxFoldCost {
			in.refuse("has a constant expression too deeply nested to compile", node.Idx0())
		}
		if cost.bigBits > maxBigIntBits {
			in.refuse(fmt.Sprintf("has a BigInt constant too large to work out (more than %d bits)", maxBigIntBits), node.Idx0())
		}
	}
	return bodyThis
}

// fold is what goja's compiler does with one expression: whether it calls it
// constant, what asking that costs (its constant() method), and what
// emitting it costs. Compiling an expression costs ask + emit.
type fold struct {
	constant  bool
	ask, emit float64
	// bigBits bounds the bit length of a constant BigInt value, and
	// bigMagnitude its size, for an expression goja works out while
	// compiling; both are 0 when the expression is no BigInt constant.
	bigBits, bigMagnitude float64
}

// foldCost follows goja's compiler (compiler_expr.go) over the expressions it
// folds, erring towards cost: a logical operator whose left side is constant
// counts as constant, whatever its right side, since goja evaluates that left
// side before it knows. Anything else is one unit that goja never folds
// through; its own parts are measured where the walk reaches them. The sums
// are floats so a hostile expression reaches +Inf rather than wrapping.
func (in *inspector) foldCost(expression ast.Expression) fold {
	if cost, ok := in.folds[expression]; ok {
		return cost
	}
	cost := fold{ask: 1, emit: 1}
	switch node := expression.(type) {
	case *ast.NumberLiteral:
		cost.constant = true
		if value, ok := node.Value.(*big.Int); ok {
			cost.bigBits = float64(max(value.BitLen(), 1))
			cost.bigMagnitude, _ = new(big.Float).SetInt(new(big.Int).Abs(value)).Float64()
		}
	case *ast.StringLiteral, *ast.BooleanLiteral, *ast.NullLiteral:
		cost.constant = true
	case *ast.UnaryExpression:
		operand := in.foldCost(node.Operand)
		cost = fold{constant: operand.constant, ask: operand.ask, emit: operand.ask + operand.emit + 1}
		if operand.bigBits > 0 && (node.Operator == token.MINUS || node.Operator == token.BITWISE_NOT) {
			cost.bigBits, cost.bigMagnitude = operand.bigBits+1, operand.bigMagnitude+1
		}
	case *ast.BinaryExpression:
		left, right := in.foldCost(node.Left), in.foldCost(node.Right)
		cost.emit = left.ask + left.emit + right.ask + right.emit + 1
		switch node.Operator {
		case token.LOGICAL_OR, token.LOGICAL_AND, token.COALESCE:
			// Asking evaluates a constant left side, then asks the right.
			cost.constant, cost.ask = left.constant, left.ask
			if left.constant {
				cost.ask += left.emit + right.ask
			}
			// The value is one side or the other.
			cost.bigBits, cost.bigMagnitude = max(left.bigBits, right.bigBits), max(left.bigMagnitude, right.bigMagnitude)
		default:
			cost.constant, cost.ask = left.constant && right.constant, left.ask
			if left.constant {
				cost.ask += right.ask
			}
			if left.bigBits > 0 && right.bigBits > 0 {
				cost.bigBits, cost.bigMagnitude = bigBound(node.Operator, left, right)
			}
		}
	}
	in.folds[expression] = cost
	return cost
}

// bigBound bounds the bits and the size of a BigInt operation on two
// constant BigInt operands, from theirs. A shift by a negative amount shifts
// the other way, so a shift's bound takes the amount's size either way.
// Comparisons give no BigInt.
func bigBound(operator token.Token, left, right fold) (bits, magnitude float64) {
	switch operator {
	case token.PLUS, token.MINUS:
		return max(left.bigBits, right.bigBits) + 1, left.bigMagnitude + right.bigMagnitude
	case token.MULTIPLY:
		return left.bigBits + right.bigBits, left.bigMagnitude * right.bigMagnitude
	case token.SLASH, token.REMAINDER:
		return left.bigBits, left.bigMagnitude
	case token.EXPONENT:
		return left.bigBits * right.bigMagnitude, math.Pow(left.bigMagnitude, right.bigMagnitude)
	case token.SHIFT_LEFT, token.SHIFT_RIGHT, token.UNSIGNED_SHIFT_RIGHT:
		return left.bigBits + right.bigMagnitude, left.bigMagnitude * math.Pow(2, right.bigMagnitude)
	case token.AND, token.OR, token.EXCLUSIVE_OR:
		return max(left.bigBits, right.bigBits), 2 * max(left.bigMagnitude, right.bigMagnitude)
	}
	return 0, 0
}

func isIdentifier(expression ast.Expression, name string) bool {
	identifier, ok := expression.(*ast.Identifier)
	return ok && string(identifier.Name) == name
}

func isThis(expression ast.Expression) bool {
	_, ok := expression.(*ast.ThisExpression)
	return ok
}

func (in *inspector) call(callee ast.Expression, arguments []ast.Expression, at file.Idx) {
	identifier, ok := callee.(*ast.Identifier)
	if !ok {
		return
	}
	switch string(identifier.Name) {
	case "require":
		in.require(arguments, at)
	case "RegExp":
		in.regexpConstructor(arguments, at)
	case "$getWorkflowStaticData":
		in.refuse("uses $getWorkflowStaticData", at)
	}
}

func (in *inspector) require(arguments []ast.Expression, at file.Idx) {
	if len(arguments) == 0 {
		return
	}
	literal, ok := arguments[0].(*ast.StringLiteral)
	if !ok {
		in.analysis.DynamicRequire = true
		return
	}
	name := strings.TrimPrefix(string(literal.Value), "node:")
	in.requires[name] = true
	if name == "luxon" {
		in.analysis.UsesLuxon = true
	}
	if !shippedModules[name] {
		in.refuse(fmt.Sprintf("requires the module %q", name), at)
	}
}

// thisMember checks `this.<name>` on the body's own `this`. Credentials are
// never reachable from a Code node, as in n8n.
func (in *inspector) thisMember(name string, at file.Idx) {
	switch name {
	case "getCredentials":
		in.refuse("calls this.getCredentials", at)
	case "helpers":
		in.refuse("uses this.helpers", at)
	}
}

func (in *inspector) regexpConstructor(arguments []ast.Expression, at file.Idx) {
	if len(arguments) == 0 {
		return
	}
	pattern, ok := arguments[0].(*ast.StringLiteral)
	if !ok {
		return // Checked when it runs.
	}
	flags := ""
	if len(arguments) > 1 {
		literal, ok := arguments[1].(*ast.StringLiteral)
		if !ok {
			return
		}
		flags = string(literal.Value)
	}
	in.regexp(string(pattern.Value), flags, at)
}

// regexp refuses what goja's regular expressions cannot do faithfully. A
// \p{…} property escape under the u flag is the dangerous one: goja accepts
// it and matches nothing, so the code runs and computes something else.
func (in *inspector) regexp(pattern, flags string, at file.Idx) {
	for _, flag := range "vd" {
		if strings.ContainsRune(flags, flag) {
			in.refuse(fmt.Sprintf("uses the regular-expression flag %q", flag), at)
		}
	}
	if strings.ContainsRune(flags, 'u') && hasPropertyEscape(pattern) {
		in.refuse(`uses a regular expression with \p{…} property escapes`, at)
	}
}

// hasPropertyEscape finds \p{ or \P{ in a pattern, skipping escaped
// backslashes, so /\\p{x}/ (a backslash, then "p{x}") is not a hit.
func hasPropertyEscape(pattern string) bool {
	for index := 0; index < len(pattern); index++ {
		if pattern[index] != '\\' || index+1 >= len(pattern) {
			continue
		}
		next := pattern[index+1]
		if (next == 'p' || next == 'P') && index+2 < len(pattern) && pattern[index+2] == '{' {
			return true
		}
		index++
	}
	return false
}

func (in *inspector) refuse(subject string, at file.Idx) {
	found := Unsupported{Subject: subject, Line: in.w.lineAt(int(at) - 1)}
	if in.seen[found] {
		return
	}
	in.seen[found] = true
	in.analysis.Unsupported = append(in.analysis.Unsupported, found)
}
