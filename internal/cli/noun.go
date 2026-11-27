package cli

import (
	"strings"
)

// verbsOfNoun returns every verb whose first word is noun, in registry order.
//
// A noun is not a verb of its own: `workflow` names a family, and the family's
// verbs are the help a caller who typed only the noun is asking for.
func verbsOfNoun(verbs []Verb, noun string) []Verb {
	if noun == "" || strings.HasPrefix(noun, "-") {
		return nil
	}
	family := make([]Verb, 0, 8)
	for _, verb := range verbs {
		words := strings.Fields(verb.Path)
		if len(words) > 1 && words[0] == noun {
			family = append(family, verb)
		}
	}

	return family
}

// isHelpWord reports whether a word asks for help.
func isHelpWord(word string) bool {
	switch word {
	case "help", "-h", "-help", "--help":
		return true
	default:
		return false
	}
}

// runNoun answers an invocation that named a noun but none of its verbs.
//
// The noun alone, or followed by a help word, lists the family: the same rows
// and rendering `kilasflow help` uses, narrowed to the noun, so a JSON caller
// gets an envelope and a terminal gets the table. Anything else after the noun
// is a usage error that points at the family's own help.
func runNoun(env Env, noun string, family []Verb, rest []string) int {
	flags := &GlobalFlags{}
	for _, word := range rest {
		switch word {
		case "--json", "-json":
			flags.JSON = true
		case "--quiet", "-quiet":
			flags.Quiet = true
		}
	}
	ctx := &Context{Env: env, Verb: Verb{Path: noun, Human: humanHelp}, Flags: flags}

	asked := ""
	for _, word := range rest {
		if isHelpWord(word) {
			asked = ""
			break
		}
		if !strings.HasPrefix(word, "-") {
			asked = word
			break
		}
	}
	if asked != "" {
		return renderResult(env, ctx, usageError("unknown %s verb %q: run `kilasflow %s --help` for its verbs", noun, asked, noun))
	}

	rows := make([]verbSummary, 0, len(family))
	for _, verb := range family {
		rows = append(rows, verbSummary{
			Path: verb.Path, Summary: verb.Summary, Operation: verb.Operation, Guarded: verb.Guarded,
		})
	}
	ctx.Data = rows

	return renderResult(env, ctx, nil)
}

// usageLine is a verb's synopsis with its positional arguments named, the way
// its own usage errors name them: `kilasflow workflow get <workflow id> [flags]`.
func usageLine(verb Verb) string {
	parts := []string{"kilasflow", verb.Path}
	for _, argument := range verb.Args {
		if argument.Optional {
			parts = append(parts, "[<"+argument.Name+">]")
		} else {
			parts = append(parts, "<"+argument.Name+">")
		}
	}

	return strings.Join(append(parts, "[flags]"), " ")
}
