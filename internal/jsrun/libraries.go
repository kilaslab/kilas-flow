package jsrun

import (
	"sync"
	"time"

	"github.com/kilaslab/kilas-flow/third_party/lodash"
)

// library is a vendored bundle a body can use. It is compiled once per
// process and run in a VM only when the body's analysis asks for it, before
// the clock starts.
type library struct {
	name   string
	source func() string
}

var libraries = map[string]library{
	"lodash": {name: "lodash", source: func() string { return lodash.Source }},
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
	compiled, err := compileTrusted(lib.name, lib.source())
	if err != nil {
		return nil, err
	}
	compiledLibraries.programs[key] = compiled
	return compiled, nil
}

// librariesFor lists the libraries a body needs, by its analysis.
func librariesFor(analysis Analysis, forced []string) []library {
	wanted := map[string]bool{}
	for _, name := range analysis.Requires {
		wanted[name] = true
	}
	for _, name := range forced {
		wanted[name] = true
	}
	var needed []library
	for _, name := range []string{"lodash"} {
		if lib, ok := libraries[name]; ok && wanted[name] {
			needed = append(needed, lib)
		}
	}
	return needed
}
