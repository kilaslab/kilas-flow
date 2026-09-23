package jsrun

import (
	"sync"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/buffer"
	"github.com/dop251/goja_nodejs/require"
	"github.com/dop251/goja_nodejs/url"
)

// Buffer and URL come from goja_nodejs (MIT, by goja's author): Node's Buffer
// class with its read and write methods, and a WHATWG URL parser. The
// runtime's buffer module adds what that Buffer lacks, the rest of Node's
// encodings among it, and bounds its allocations.
//
// goja_nodejs installs them through its own require(), whose default loader
// reads files from disk. The registry here is given a loader that refuses
// everything, is enabled only long enough to install the two globals, and
// its require is removed again before any other code runs; the runtime's own
// require() takes its place.

var nodeRegistry = sync.OnceValue(func() *require.Registry {
	return require.NewRegistry(require.WithLoader(func(string) ([]byte, error) {
		return nil, require.ModuleFileDoesNotExistError
	}))
})

func enableNodeGlobals(rt *goja.Runtime) {
	nodeRegistry().Enable(rt)
	buffer.Enable(rt)
	url.Enable(rt)
	rt.GlobalObject().Delete("require")
}
