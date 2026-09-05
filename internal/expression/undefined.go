package expression

// Undefined is the value a path that does not exist resolves to.
//
// It is an explicit sentinel rather than Go's nil, because nil is already a
// legitimate JSON null: conflating them would make "the field is absent"
// indistinguishable from "the field is present and null", and a workflow that
// branches on the difference would be silently wrong.
//
// A missing path used to fail the whole execution. n8n's JavaScript returns
// undefined, and real workflows lean on optional fields constantly — a field
// absent on some items should produce an empty value, not stop the run.
type undefinedValue struct{}

// Undefined is the single instance of the sentinel.
var Undefined = undefinedValue{}

// IsUndefined reports the sentinel.
func IsUndefined(value any) bool {
	_, undefined := value.(undefinedValue)
	return undefined
}
