package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// pathPlaceholder matches one {name} placeholder in a served path template.
var pathPlaceholder = regexp.MustCompile(`\{[^}]*\}`)

// operationsPath is where a running server publishes the document that
// describes itself. It is read at call time rather than compiled in: the
// document is generated from the handler types the process registered, so a
// table baked into the CLI would describe a server the CLI is not talking to.
const operationsPath = "/api/openapi.json"

// operationMethods are the HTTP methods an OpenAPI path item may carry, in the
// order the index is built and the help text is written.
var operationMethods = []string{
	http.MethodGet,
	http.MethodPut,
	http.MethodPost,
	http.MethodDelete,
	http.MethodPatch,
	http.MethodHead,
	http.MethodOptions,
	http.MethodTrace,
}

// Operation is one operation the running server serves: the id the escape
// hatch takes, the method to send and the server-absolute path template to
// fill in.
type Operation struct {
	ID     string
	Method string
	Path   string
}

// Operations fetches the served document and indexes it by operation id.
//
// The index is cached on the client, which is one invocation: `--list` and the
// resolved call that follows it read the document once, and two invocations
// never share it, so a server upgraded between them is never described by a
// stale copy.
func (c *Client) Operations(ctx context.Context) (map[string]Operation, error) {
	if c.operations != nil {
		return c.operations, nil
	}
	if c.BaseURL == "" {
		return nil, usageError("no server URL: pass --url or set %s", envURLVar)
	}

	resp, err := c.Do(ctx, http.MethodGet, operationsPath, nil, nil, nil)
	if err != nil {
		return nil, err
	}

	index, err := indexOperations(resp.Body)
	if err != nil {
		return nil, err
	}
	c.operations = index

	return index, nil
}

// documentPaths is the part of the served document the index is built from:
// every path item, by path, by method.
type documentPaths map[string]map[string]struct {
	OperationID string `json:"operationId"`
}

// indexOperations turns a served document into the id -> operation index.
//
// An id is unique by specification, and huma refuses to register a duplicate,
// so a document that carries one is not a document this CLI can act on: the
// error is raised rather than resolved by picking an arbitrary method.
func indexOperations(document []byte) (map[string]Operation, error) {
	var doc struct {
		Paths documentPaths `json:"paths"`
	}
	if err := json.Unmarshal(document, &doc); err != nil {
		return nil, &ExitError{
			Code:    ExitFailure,
			ErrCode: "error",
			Message: fmt.Sprintf("%s is not a readable OpenAPI document: %v", operationsPath, err),
		}
	}

	index := make(map[string]Operation, len(doc.Paths))
	for path, item := range doc.Paths {
		for _, method := range operationMethods {
			entry, present := item[strings.ToLower(method)]
			if !present || entry.OperationID == "" {
				continue
			}
			if existing, duplicate := index[entry.OperationID]; duplicate {
				return nil, &ExitError{
					Code:    ExitFailure,
					ErrCode: "error",
					Message: fmt.Sprintf("%s declares %q twice (%s and %s %s)",
						operationsPath, entry.OperationID, existing.Method, method, path),
				}
			}
			index[entry.OperationID] = Operation{ID: entry.OperationID, Method: method, Path: path}
		}
	}

	if len(index) == 0 {
		return nil, &ExitError{
			Code:    ExitFailure,
			ErrCode: "error",
			Message: fmt.Sprintf("%s declares no operations", operationsPath),
		}
	}

	return index, nil
}

// sortedOperations lists the index's ids in a stable order, so `api --list` is
// diffable between two servers.
func sortedOperations(index map[string]Operation) []string {
	ids := make([]string, 0, len(index))
	for id := range index {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	return ids
}

// resolveOperation builds the method and the server-absolute target for one
// operation id.
//
// An id the index does not hold is a usage error, and it is raised here rather
// than left to the server: an unknown /api/v1 path falls through to the SPA
// catch-all and answers 200 HTML, so a status code cannot tell a wrong path
// from a right one.
func resolveOperation(index map[string]Operation, id string, pathParams map[string]string, query url.Values) (string, string, error) {
	operation, known := index[id]
	if !known {
		return "", "", unknownOperationError(id)
	}

	target, err := fillPath(operation.Path, id, pathParams)
	if err != nil {
		return "", "", err
	}
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}

	return operation.Method, target, nil
}

// unknownOperationError is the one refusal an unknown operation id produces,
// so the message names the id and the way to enumerate the real ones.
func unknownOperationError(id string) *ExitError {
	return usageError("unknown operation id %q; run `kilasflow api --list` to see what this server serves", id)
}

// fillPath substitutes every {name} placeholder in a template from params.
//
// A placeholder with no value is a usage error naming the flag that supplies
// it, because a request to a template with a literal {name} in it would land
// on the SPA. Values are path-escaped, so an id carrying a slash cannot
// silently address a different route than the caller meant.
//
// A value that is empty or whitespace-only counts as missing, which is what
// `--path id=$WF_ID` means when WF_ID is unset. Substituting it would build
// `/workflows/`, a path the SPA catch-all answers with 200 text/html, so the
// caller would be handed an HTML document as a successful fetch and no status
// code could tell it. requireOneID refuses an empty id for the same reason.
func fillPath(template, id string, params map[string]string) (string, error) {
	missing := make([]string, 0, 2)
	target := pathPlaceholder.ReplaceAllStringFunc(template, func(placeholder string) string {
		name := placeholder[1 : len(placeholder)-1]
		value, supplied := params[name]
		if !supplied || strings.TrimSpace(value) == "" {
			missing = append(missing, name)
			return placeholder
		}

		return url.PathEscape(value)
	})
	if len(missing) > 0 {
		flags := make([]string, 0, len(missing))
		for _, name := range missing {
			flags = append(flags, fmt.Sprintf("--path %s=<value>", name))
		}

		return "", usageError("operation %s needs %s", id, strings.Join(flags, " "))
	}

	return target, nil
}
