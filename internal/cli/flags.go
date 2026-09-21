package cli

import (
	"flag"
	"time"
)

// defaultTimeout bounds one command end to end. A CLI that an agent drives must
// never hang: a live execution's stream, a stuck server and a half-open socket
// all have to end in an exit code the caller can act on.
const defaultTimeout = 30 * time.Second

// GlobalFlags are the flags every verb accepts.
//
// Ownership of URL, token and configuration precedence belongs to a later
// stage of the ticket; this stage parses and stores them, resolving only the
// flag and the two environment variables.
type GlobalFlags struct {
	JSON      bool
	Quiet     bool
	Verbose   bool
	Yes       bool
	URL       string
	Token     string
	TokenFile string
	Config    string
	Timeout   time.Duration
	// SkillsUsed names the skills an agent consulted to make this call. It is
	// sent on mutating requests only; see Client.SkillsUsed.
	SkillsUsed string

	// provided records which flags the caller actually set, so an explicitly
	// empty --url can mean "talk to nothing" rather than "use the default".
	provided map[string]bool
}

// wasProvided reports whether the caller set the named flag.
func (f *GlobalFlags) wasProvided(name string) bool { return f.provided[name] }

// registerGlobalFlags attaches every global flag to a verb's FlagSet.
func registerGlobalFlags(fs *flag.FlagSet) *GlobalFlags {
	flags := &GlobalFlags{}

	fs.BoolVar(&flags.JSON, "json", false, "print one JSON envelope on stdout, whatever stdout is")
	fs.BoolVar(&flags.Quiet, "quiet", false, "print only the primary identifier, for shell pipelines")
	fs.BoolVar(&flags.Verbose, "verbose", false, "log requests to stderr with credentials redacted")
	fs.BoolVar(&flags.Yes, "yes", false, "confirm a guarded verb; only ever passed on an explicit human instruction")
	fs.StringVar(&flags.URL, "url", "", "base URL of the server, e.g. http://127.0.0.1:8080")
	fs.StringVar(&flags.Token, "token", "", "API token to send; prefer the environment to keep it out of a process listing")
	fs.StringVar(&flags.TokenFile, "token-file", "", "file holding the API token")
	fs.StringVar(&flags.Config, "config", "", "path to the CLI configuration file")
	fs.DurationVar(&flags.Timeout, "timeout", defaultTimeout, "deadline for one command")
	fs.StringVar(&flags.SkillsUsed, "skills-used", "", "comma-separated skills an agent consulted, recorded on the revision a mutating command writes")

	return flags
}

// parseFlags parses the arguments after the verb words and returns the
// positional ones.
//
// Go's flag package stops at the first non-flag token, which would leave
// `workflow get wf_1 --json` unparsed. Parsing repeatedly and keeping each
// non-flag token as a positional keeps the rule simple for a caller: the verb
// words come first, flags may appear anywhere after them, and `--` ends flag
// parsing. It also records which flags were set, which the URL precedence
// rules need in order to tell "not passed" from "passed empty".
func parseFlags(fs *flag.FlagSet, flags *GlobalFlags, args []string) ([]string, error) {
	provided := make(map[string]bool, 4)
	positional := make([]string, 0, len(args))

	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		fs.Visit(func(visited *flag.Flag) { provided[visited.Name] = true })

		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		positional = append(positional, rest[0])
		rest = rest[1:]
	}

	flags.provided = provided

	return positional, nil
}
