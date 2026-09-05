// Package conditions evaluates the filter language IF, Filter and Switch share.
//
// One evaluator, not three. The three nodes differ only in what they do with
// the answer — IF routes to two ports, Filter drops what does not match, Switch
// routes to the first matching rule — and giving each its own comparison logic
// is how `"5" == 5` comes out differently depending on which node asked.
//
// # The shape is n8n's
//
// A condition is `{leftValue, operator: {type, operation}, rightValue}`, a
// filter is an ordered list of them plus a combinator and an options bag, and
// the operator's *type* selects the comparison. That is deliberate rather than
// convenient: an imported workflow carries exactly this, and a shape of our own
// would mean translating on the way in, translating back on the way out, and
// being wrong about a case nobody thought of.
//
// # Type validation
//
// `loose` converts before comparing, which is what n8n does by default and what
// every real workflow relies on — a webhook body is all strings, and a
// condition comparing one to a number has to work. `strict` refuses instead,
// and says which value was the wrong type.
//
// The conversions are table-tested rather than inferred, because the difference
// between "5" being 5 and "5" being nothing is a branch a workflow takes.
package conditions
