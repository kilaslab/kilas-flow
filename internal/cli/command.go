package cli

import (
	"errors"
	"flag"
	"io"
	"strings"
)

// Verb is one command in the command tree.
//
// Path is space-separated ("workflow get") and is also the human name of the
// verb; Operation is the API operation id the verb drives, or "" when the verb
// is local (version, help, auth login). Guarded marks a verb that refuses
// without --yes; Refusal is the sentence it refuses with.
type Verb struct {
	Path      string
	Summary   string
	Operation string
	Guarded   bool
	Refusal   string
	// Flags registers verb-specific flags on the FlagSet and returns whatever
	// the verb's Run needs to read them.
	Flags func(*flag.FlagSet) any
	// Run executes the verb with the positional arguments that survived flag
	// parsing, and stores its payload on the Context.
	Run func(*Context, []string) error
	// Human renders the payload for a terminal; nil falls back to indented
	// JSON.
	Human func(io.Writer, any)
}

// ServeVerb is the explicit spelling of the server path, `kilasflow serve`. The
// server is the binary's default meaning, not a CLI command: the word is
// recognised here so `help` lists it and an unknown-word check cannot mistake
// it for a typo, and cmd/kilasflow needs the same word to take it out of the
// arguments the server parses.
const ServeVerb = "serve"

// ServerArgs returns the arguments the server's own flag set must parse.
//
// Run recognises the explicit `serve` verb and hands the invocation back
// without touching what follows it — those flags belong to the server — but
// the word itself is still the first element of argv, and run() refuses a
// leftover positional argument, which is what `kilasflow serve` used to hit.
// Removing it here is what makes `kilasflow serve -config x` and
// `kilasflow -config x` reach the server with the same arguments.
func ServerArgs(args []string) []string {
	if len(args) > 0 && args[0] == ServeVerb {
		return args[1:]
	}

	return args
}

// errServeRequested is how the serve verb reports that the caller, not the
// CLI, owns this invocation.
var errServeRequested = errors.New("the server path was requested")

// registry returns every verb this binary implements.
//
// Later stages of the ticket append their verb families here and nowhere else,
// so the command tree has exactly one definition.
func registry() []Verb {
	verbs := []Verb{serveVerb()}
	verbs = append(verbs, systemVerbs()...)
	verbs = append(verbs, contextVerbs()...)
	verbs = append(verbs, apiVerbs()...)
	verbs = append(verbs, authVerbs()...)
	verbs = append(verbs, workflowVerbs()...)
	verbs = append(verbs, runVerbs()...)
	verbs = append(verbs, execVerbs()...)
	verbs = append(verbs, nodeVerbs()...)
	verbs = append(verbs, credentialVerbs()...)
	verbs = append(verbs, datastoreVerbs()...)
	verbs = append(verbs, scheduleVerbs()...)
	verbs = append(verbs, tenantVerbs()...)
	verbs = append(verbs, packVerbs()...)

	return verbs
}

// serveVerb is the explicit form of the default behaviour, for scripts.
//
// Its Run is never reached: Run refuses to flag-parse `serve` because the
// flags after it belong to the server (-config, -version, -role), not to the
// CLI. The entry exists so `help` lists it and so an unknown-word check cannot
// mistake it for a typo.
func serveVerb() Verb {
	return Verb{
		Path:    ServeVerb,
		Summary: "run the API, worker and scheduler (the default when no verb is given)",
		Run:     func(*Context, []string) error { return errServeRequested },
	}
}

// splitVerb matches the longest registered verb whose words lead args.
//
// Greedy longest-prefix is what makes `workflow get-version v1` resolve to
// `workflow get-version` rather than to a shorter `workflow` prefix, and it is
// deterministic by construction: the verb's words must come first, flags may
// appear anywhere after them.
func splitVerb(verbs []Verb, args []string) (Verb, []string, bool) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return Verb{}, nil, false
	}

	var (
		best    Verb
		longest int
		found   bool
	)

	for _, verb := range verbs {
		words := strings.Fields(verb.Path)
		if len(words) == 0 || len(words) > len(args) {
			continue
		}

		matches := true
		for i, word := range words {
			if args[i] != word {
				matches = false
				break
			}
		}
		if !matches || (found && len(words) <= longest) {
			continue
		}

		best, longest, found = verb, len(words), true
	}

	if !found {
		return Verb{}, nil, false
	}

	return best, args[longest:], true
}
