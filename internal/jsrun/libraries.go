package jsrun

import (
	"sort"
	"sync"
	"time"

	"github.com/kilaslab/kilas-flow/third_party/lodash"
	"github.com/kilaslab/kilas-flow/third_party/luxon"
)

// library is a vendored bundle a body can use. It is compiled once per
// process and run in a VM only when the body uses it: before the clock starts
// when the analysis saw it named, or on first use otherwise.
//
// A bundle runs inside a function, and exportExpression is what that function
// returns, so no bundle leaves a global behind: lodash would otherwise take
// `_`, and Luxon's `var luxon` a global no one could remove.
type library struct {
	name             string
	source           func() string
	exportExpression string
}

var libraries = map[string]library{
	"lodash": {name: "lodash", source: func() string { return lodash.Source }, exportExpression: "_.noConflict()"},
	"luxon":  {name: "luxon", source: func() string { return luxon.Source }, exportExpression: "luxon"},
}

func libraryNames() []string {
	names := make([]string, 0, len(libraries))
	for name := range libraries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// compiledLibraries caches compiled bundles by name and match timeout, since
// the timeout is baked into every pattern a bundle compiles.
var compiledLibraries = struct {
	sync.Mutex
	programs map[libraryKey]*program
}{programs: map[libraryKey]*program{}}

type libraryKey struct {
	name    string
	timeout time.Duration
}

func (lib library) program() (*program, error) {
	key := libraryKey{name: lib.name, timeout: currentMatchTimeout()}
	compiledLibraries.Lock()
	defer compiledLibraries.Unlock()
	if compiled, ok := compiledLibraries.programs[key]; ok {
		return compiled, nil
	}
	compiled, err := compileTrusted(lib.name, "(function () {\n"+lib.source()+"\n;return "+lib.exportExpression+";\n})()")
	if err != nil {
		return nil, err
	}
	compiledLibraries.programs[key] = compiled
	return compiled, nil
}

// librariesFor lists the libraries to load before the code runs, by its
// analysis.
func librariesFor(analysis Analysis, forced []string) []string {
	wanted := map[string]bool{}
	for _, name := range analysis.Requires {
		wanted[name] = true
	}
	if analysis.UsesLuxon {
		wanted["luxon"] = true
	}
	for _, name := range forced {
		wanted[name] = true
	}
	preload := []string{}
	for _, name := range libraryNames() {
		if wanted[name] {
			preload = append(preload, name)
		}
	}
	return preload
}
