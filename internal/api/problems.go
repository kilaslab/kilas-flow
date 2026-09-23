package api

import (
	"reflect"
	"sync"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslab/kilas-flow/internal/api/handlers"
)

// problemRedaction installs the redaction once per process. huma keeps its
// error constructor in a package variable, so a second NewServer — every test
// builds one — must not wrap the wrapper again.
var problemRedaction sync.Once

// installProblemRedaction keeps request values that may be secrets out of the
// problem documents huma builds for a request it refuses.
//
// huma's validator reports each failure with the value it was checking. For a
// field that is the field, but for a missing or an unexpected property it is
// the whole object that should have held it, and for a body that does not parse
// it is the raw bytes. A refused credential create therefore answered with the
// token it refused, and the CLI envelope, the MCP tool result and every log
// that kept either carried it on.
//
// Only huma's own path changes. NewErrorWithContext is what huma calls for a
// validation or parse failure, while a handler that builds its problem itself —
// a compile refusal's issue codes, an idempotency conflict, a row's current
// stamp — returns it directly and keeps the value it chose to send.
func installProblemRedaction() {
	problemRedaction.Do(func() {
		build := huma.NewErrorWithContext
		huma.NewErrorWithContext = func(ctx huma.Context, status int, msg string, errs ...error) huma.StatusError {
			return build(ctx, status, msg, redactDetails(sensitiveOperation(ctx), errs)...)
		}
	})
}

// redactDetails returns errs with every value that could carry more than the
// caller's own field dropped, or every value at all on a secret-bearing
// operation. The details are copied rather than edited, because they are
// huma's.
//
// A detail is recognised the way huma's NewError recognises one, by asserting
// ErrorDetailer directly: a wrapped detail is reduced to its message there and
// never carries a value in the first place.
func redactDetails(sensitive bool, errs []error) []error {
	redacted := make([]error, len(errs))
	for index, err := range errs {
		redacted[index] = err
		detailer, ok := err.(huma.ErrorDetailer)
		if !ok {
			continue
		}
		detail := detailer.ErrorDetail()
		if detail == nil || detail.Value == nil || !(sensitive || echoesBody(detail)) {
			continue
		}
		stripped := *detail
		stripped.Value = nil
		redacted[index] = &stripped
	}
	return redacted
}

// echoesBody reports whether a detail's value may be more than the one field
// it names: its location is the body itself, which is where huma puts the raw
// bytes of a body it cannot parse, or its value is an object or a list, which
// is how huma reports a missing or an unexpected property — with the whole
// object around it.
func echoesBody(detail *huma.ErrorDetail) bool {
	if detail.Location == "body" {
		return true
	}
	switch reflect.ValueOf(detail.Value).Kind() {
	case reflect.Map, reflect.Slice, reflect.Array:
		return true
	default:
		return false
	}
}

// sensitiveOperation reports whether the operation being answered marked its
// body as secret-bearing, where any field may be the secret and no value is
// safe to send back.
func sensitiveOperation(ctx huma.Context) bool {
	if ctx == nil {
		return false
	}
	operation := ctx.Operation()
	if operation == nil {
		return false
	}
	sensitive, _ := operation.Metadata[handlers.SensitiveBodyKey].(bool)
	return sensitive
}
