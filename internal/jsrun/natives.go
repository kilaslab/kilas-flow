package jsrun

import (
	"fmt"
	"math"
	"sort"
)

// native is a Go function the runtime's own modules call: a hash, a cipher, a
// time-zone lookup. It takes and returns plain values only, so it knows
// nothing of the engine:
//
//   - arguments arrive as nil, bool, int64, float64, string or []byte (a
//     typed array or ArrayBuffer), or []any and map[string]any;
//   - a result may be any of those, and []byte arrives in JavaScript as an
//     ArrayBuffer.
//
// A native runs on the VM's goroutine while the code waits, so it must be
// bounded: whatever size it is asked for is checked against the per-call caps
// before any work is done.
type native func(args []any) (any, error)

// nativeError is an error a native raises as a particular JavaScript error,
// such as a RangeError for a size out of bounds.
type nativeError struct {
	name, message string
}

func (e *nativeError) Error() string { return e.message }

func rangeError(format string, args ...any) error {
	return &nativeError{name: "RangeError", message: fmt.Sprintf(format, args...)}
}

func typeError(format string, args ...any) error {
	return &nativeError{name: "TypeError", message: fmt.Sprintf(format, args...)}
}

var natives = map[string]native{}

// registerNative adds a native under a name the modules call it by. It is
// called from init, and a name registered twice is a programming error.
func registerNative(name string, fn native) {
	if _, taken := natives[name]; taken {
		panic("jsrun: native " + name + " registered twice")
	}
	natives[name] = fn
}

// nativeNames lists the registered natives, for tests.
func nativeNames() []string {
	names := make([]string, 0, len(natives))
	for name := range natives {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func argument(args []any, index int) any {
	if index < len(args) {
		return args[index]
	}
	return nil
}

// argString reads a string argument.
func argString(args []any, index int, name string) (string, error) {
	text, ok := argument(args, index).(string)
	if !ok {
		return "", typeError("%s must be a string", name)
	}
	return text, nil
}

// argBytes reads a byte argument: a typed array, an ArrayBuffer, or a
// string, taken as UTF-8. The bytes are copied, so a native can never write
// into the script's memory.
func argBytes(args []any, index int, name string) ([]byte, error) {
	switch value := argument(args, index).(type) {
	case []byte:
		return append([]byte(nil), value...), nil
	case string:
		return []byte(value), nil
	case nil:
		return nil, typeError("%s is required", name)
	default:
		return nil, typeError("%s must be bytes or a string", name)
	}
}

// argInt reads a whole-number argument within [low, high].
func argInt(args []any, index int, name string, low, high int64) (int64, error) {
	var number float64
	switch value := argument(args, index).(type) {
	case int64:
		number = float64(value)
	case float64:
		number = value
	default:
		return 0, typeError("%s must be a number", name)
	}
	if math.IsNaN(number) || number != math.Trunc(number) || number < float64(low) || number > float64(high) {
		return 0, rangeError("%s must be a whole number from %d to %d", name, low, high)
	}
	return int64(number), nil
}

// argMap reads an options object; a missing one is empty.
func argMap(args []any, index int) map[string]any {
	options, _ := argument(args, index).(map[string]any)
	return options
}
