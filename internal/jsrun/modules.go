package jsrun

import (
	"embed"
	"fmt"
	"sync"
)

// The runtime's own modules: KilasFlow-authored JavaScript under
// js/modules, over Go natives where the work is Go's to do.
//
// Each file evaluates to a factory, `(function (kit) { … return exports })`.
// Every VM runs them all, in moduleOrder, before any user code: each defines
// the globals it provides through kit.define, and what it returns is what
// require() gives for its name. Running them eagerly keeps them out of the
// code's reach: they capture the built-ins they need before the code can
// change any of them.
//
//go:embed js/modules/*.js
var moduleSources embed.FS

// moduleOrder is the order modules run in; each sees the globals the ones
// before it defined.
var moduleOrder = []string{"util", "intl", "luxon"}

// requirable maps a name require() accepts to the module that answers it.
// lodash is a library, loaded on first use rather than always. Luxon is a
// library too, but its module answers for it, so require('luxon') gives the
// instance configured with the workflow's zone however it came to be loaded.
var requirable = map[string]string{"util": "util", "luxon": "luxon"}

var compiledModules = struct {
	sync.Mutex
	programs map[libraryKey]*program
}{programs: map[libraryKey]*program{}}

// moduleProgram compiles a module once per match timeout, like a library.
func moduleProgram(name string) (*program, error) {
	key := libraryKey{name: name, timeout: currentMatchTimeout()}
	compiledModules.Lock()
	defer compiledModules.Unlock()
	if compiled, ok := compiledModules.programs[key]; ok {
		return compiled, nil
	}
	source, err := moduleSources.ReadFile("js/modules/" + name + ".js")
	if err != nil {
		return nil, fmt.Errorf("jsrun: module %s: %w", name, err)
	}
	compiled, err := compileTrusted("kilasflow:"+name, string(source))
	if err != nil {
		return nil, err
	}
	compiledModules.programs[key] = compiled
	return compiled, nil
}

// shippedModuleNames lists what require() can return: the modules and the
// libraries. The analyser refuses anything else before the code runs.
func shippedModuleNames() map[string]bool {
	names := map[string]bool{}
	for name := range requirable {
		names[name] = true
	}
	for name := range libraries {
		names[name] = true
	}
	return names
}
