package runcode

import "errors"

// The named failures of one sandboxed call.
//
// A limit that stops a module is not an interesting fact on its own: the
// caller has to be able to tell which limit was hit without reading English,
// because the four mean different things to whoever has to fix them. Wall
// clock and memory are the user's program being too big or too slow; output is
// a program that will not stop talking; host calls are a pack that wants more
// of the host than it was granted. So each is a sentinel that errors.Is
// answers, alongside the ExecutionError whose Detail is what a user reads.
var (
	// ErrTimeLimit reports that the module ran past the call's wall clock.
	ErrTimeLimit = errors.New("code exceeded its time limit")
	// ErrMemoryLimit reports that the module could not fit the call's linear
	// memory limit.
	ErrMemoryLimit = errors.New("code exceeded its memory limit")
	// ErrOutputLimit reports that the module wrote more than the call allows.
	ErrOutputLimit = errors.New("code produced more output than allowed")
	// ErrHostCallLimit reports that the module called into the host more often
	// than the call allows.
	ErrHostCallLimit = errors.New("code made more host calls than allowed")
)
