package jsrun

import (
	"reflect"

	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/file"
)

// What the compiled body needs so the errors goja throws can be given V8's
// words (see wording.go): where each call names a callee V8 would print, and
// a call to the rewording function at the start of every catch clause.

// rewordParameter is the wrapper function's last parameter: the runtime's
// reword, which every instrumented catch clause calls. It is added to the
// parsed tree only, under a name no source text can spell, so the code can
// neither read it nor shadow it, and the wrapper's own text is unchanged.
const rewordParameter = "kilasflow:reword"

// callSites finds, in a parsed body, every `new`, and every call whose
// callee V8 would name or that builds an error, keyed by where goja reports
// an error there.
func callSites(program *ast.Program) map[position]callSite {
	sites := map[position]callSite{}
	place := func(idx file.Idx) position {
		at := program.File.Position(int(idx) - program.File.Base())
		return position{at.Line, at.Column}
	}
	eachNode(reflect.ValueOf(program), func(node any) {
		switch node := node.(type) {
		case *ast.CallExpression:
			if _, isSuper := node.Callee.(*ast.SuperExpression); isSuper {
				// super(…) constructs, as `new` does: an error a subclass of an
				// error constructor builds is made here.
				sites[place(node.LeftParenthesis)] = callSite{construct: true}
				return
			}
			text, ok := calleeText(node.Callee)
			builds := buildsError(node.Callee) || ok && text == "Reflect.construct"
			if ok || builds {
				sites[place(node.LeftParenthesis)] = callSite{text: text, buildsError: builds}
			}
		case *ast.NewExpression:
			text, _ := calleeText(node.Callee)
			sites[place(node.New)] = callSite{text: text, construct: true, buildsError: buildsError(node.Callee)}
		}
	})
	return sites
}

// calleeText is a callee as V8 prints it in "… is not a function", for the
// forms that printing was recorded for (testdata/parity/errors.json): names,
// `this`, property chains through `.`, `?.` and brackets, and calls within
// them, which print as `(...)`. The object of a property that an `await` or
// a `new` produced prints as `(intermediate value)`. Anything else answers
// false and keeps goja's words.
func calleeText(callee ast.Expression) (string, bool) {
	switch node := callee.(type) {
	case *ast.Identifier:
		return node.Name.String(), true
	case *ast.ThisExpression:
		return "this", true
	case *ast.OptionalChain:
		return calleeText(node.Expression)
	case *ast.CallExpression:
		if _, optional := node.Callee.(*ast.Optional); optional {
			return "", false
		}
		text, ok := calleeText(node.Callee)
		return text + "(...)", ok
	case *ast.DotExpression:
		left, dot, ok := memberBase(node.Left)
		return left + dot + node.Identifier.Name.String(), ok
	case *ast.BracketExpression:
		left, dot, ok := memberBase(node.Left)
		if !ok {
			return "", false
		}
		switch key := node.Member.(type) {
		case *ast.StringLiteral:
			// V8 prints a string key as a name, whatever it holds: a['q-r']
			// is `a.q-r`.
			if dot != "." {
				return "", false
			}
			return left + "." + key.Value.String(), true
		case *ast.NumberLiteral:
			number, ok := numberValue(key.Value)
			return left + bracketOpen(dot) + jsNumber(number) + "]", ok
		case *ast.BooleanLiteral:
			return left + bracketOpen(dot) + key.Literal + "]", true
		case *ast.NullLiteral:
			return left + bracketOpen(dot) + "null]", true
		}
		switch node.Member.(type) {
		case *ast.Identifier, *ast.ThisExpression, *ast.DotExpression, *ast.BracketExpression, *ast.CallExpression:
			inner, ok := calleeText(node.Member)
			return left + bracketOpen(dot) + inner + "]", ok
		}
	}
	return "", false
}

// buildsError says a callee names a built-in error constructor: `TypeError`,
// or a property of that name, as in `globalThis.TypeError`.
func buildsError(callee ast.Expression) bool {
	switch node := callee.(type) {
	case *ast.Identifier:
		return errorConstructors[node.Name.String()]
	case *ast.DotExpression:
		return errorConstructors[node.Identifier.Name.String()]
	}
	return false
}

// memberBase prints the object of a member access, and whether it is
// reached with `.` or `?.`.
func memberBase(left ast.Expression) (text, dot string, ok bool) {
	dot = "."
	if optional, isOptional := left.(*ast.Optional); isOptional {
		left, dot = optional.Expression, "?."
	}
	switch left.(type) {
	case *ast.AwaitExpression, *ast.NewExpression:
		return "(intermediate value)", dot, true
	}
	text, ok = calleeText(left)
	return text, dot, ok
}

func bracketOpen(dot string) string {
	if dot == "?." {
		return "?.["
	}
	return "["
}

func numberValue(value any) (float64, bool) {
	switch number := value.(type) {
	case int64:
		return float64(number), true
	case float64:
		return number, true
	}
	return 0, false
}

// prepareRewording records a parsed body's call sites and instruments its
// catch clauses, just before it is compiled. The body has passed checkShape,
// so its one statement is the wrapper function.
func prepareRewording(program *ast.Program) map[position]callSite {
	sites := callSites(program)
	outer := program.Body[0].(*ast.ExpressionStatement).Expression.(*ast.FunctionLiteral)
	instrumentCatches(program, outer)
	return sites
}

// instrumentCatches gives the wrapper function its rewordParameter, and
// every catch clause in the body a first statement that hands the caught
// value to it: `catch (error) { kilasflow:reword(error); … }`. Only the
// parsed tree changes; the text, and so every position and every function's
// source, stays as the user wrote it. A catch whose parameter is a
// destructuring pattern reads the error before any statement runs, so it is
// left as it is.
func instrumentCatches(program *ast.Program, outer *ast.FunctionLiteral) {
	outer.ParameterList.List = append(outer.ParameterList.List, &ast.Binding{
		Target: &ast.Identifier{Name: rewordParameter, Idx: outer.ParameterList.Closing},
	})
	eachNode(reflect.ValueOf(program), func(node any) {
		clause, ok := node.(*ast.CatchStatement)
		if !ok || clause.Body == nil {
			return
		}
		caught, ok := clause.Parameter.(*ast.Identifier)
		if !ok {
			return
		}
		at := clause.Body.LeftBrace
		call := &ast.ExpressionStatement{Expression: &ast.CallExpression{
			Callee:           &ast.Identifier{Name: rewordParameter, Idx: at},
			LeftParenthesis:  at,
			ArgumentList:     []ast.Expression{&ast.Identifier{Name: caught.Name, Idx: at}},
			RightParenthesis: at,
		}}
		clause.Body.List = append([]ast.Statement{call}, clause.Body.List...)
	})
}
