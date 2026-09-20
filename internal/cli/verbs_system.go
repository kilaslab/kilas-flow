package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
)

// systemVerbs are the verbs that need no workflow, no tenant and no state: the
// ones an agent runs first to find out what it is talking to.
func systemVerbs() []Verb {
	return []Verb{
		{
			Path:    "version",
			Summary: "print this binary's version and, when a server is reachable, its version",
			Run:     runVersion,
			Human:   humanVersion,
		},
		{
			Path:      "health",
			Operation: "get-health",
			Summary:   "report that the process is up (does not check dependencies)",
			Run:       runHealth,
			Human:     humanSystemBody,
		},
		{
			Path:      "ready",
			Operation: "get-ready",
			Summary:   "report whether the instance can serve; exits 6 when it cannot",
			Run:       runReady,
			Human:     humanSystemBody,
		},
		{
			Path:    "help",
			Summary: "list the verbs this binary implements",
			Run:     runHelp,
			Human:   humanHelp,
		},
	}
}

// versionData is the payload of `version`.
type versionData struct {
	Version    string        `json:"version"`
	APIVersion string        `json:"apiVersion"`
	Server     *serverStatus `json:"server,omitempty"`
}

// serverStatus is what the version verb learned about the server, when one was
// reachable. An unreachable server is information, not a failure.
type serverStatus struct {
	Version   string `json:"version,omitempty"`
	Reachable bool   `json:"reachable"`
	Error     string `json:"error,omitempty"`
}

// runVersion reports the binary's version, and probes a server if one is
// configured. The probe never fails the command.
func runVersion(ctx *Context, _ []string) error {
	data := versionData{Version: ctx.Env.Version, APIVersion: apiVersion}
	ctx.Primary = data.Version

	if ctx.Client.BaseURL != "" {
		status := probeServer(ctx)
		data.Server = &status
	}

	ctx.Data = data

	return nil
}

// probeServer asks the server for its version through the liveness probe.
func probeServer(ctx *Context) serverStatus {
	resp, err := ctx.Client.Do(ctx.Ctx, http.MethodGet, apiPath("/health"), nil, nil, nil)
	if err != nil {
		return serverStatus{Reachable: false, Error: err.Error()}
	}

	var body struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		return serverStatus{Reachable: true}
	}

	return serverStatus{Reachable: true, Version: body.Version}
}

// runHealth fetches the liveness probe.
func runHealth(ctx *Context, _ []string) error {
	return fetchSystem(ctx, "/health")
}

// runReady fetches the readiness probe. A 503 becomes exit 6 with the
// not_ready code, which is the code an agent is told to wait on.
func runReady(ctx *Context, _ []string) error {
	return fetchSystem(ctx, "/ready")
}

// fetchSystem performs a probe and carries its body verbatim as the payload.
func fetchSystem(ctx *Context, path string) error {
	if err := requireServerURL(ctx); err != nil {
		return err
	}

	resp, err := ctx.Client.Do(ctx.Ctx, http.MethodGet, apiPath(path), nil, nil, nil)
	if err != nil {
		return err
	}

	ctx.Data = jsonOrText(resp.Body)
	ctx.Primary = fieldValue(resp.Body, "status")

	return nil
}

// runHelp lists the command tree from the registry, so the listing cannot
// drift from the verbs that exist.
func runHelp(ctx *Context, _ []string) error {
	verbs := registry()
	rows := make([]verbSummary, 0, len(verbs))
	for _, verb := range verbs {
		rows = append(rows, verbSummary{
			Path:      verb.Path,
			Summary:   verb.Summary,
			Operation: verb.Operation,
			Guarded:   verb.Guarded,
		})
	}

	ctx.Data = rows

	return nil
}

// verbSummary is one row of `help`.
type verbSummary struct {
	Path      string `json:"path"`
	Summary   string `json:"summary"`
	Operation string `json:"operation"`
	Guarded   bool   `json:"guarded"`
}

// requireServerURL refuses a verb that cannot do anything without a server.
//
// The message names every way to name one, because a caller who has just hit it
// needs the list rather than a hint, and an agent that hit it needs to know
// which of its own settings to change.
func requireServerURL(ctx *Context) error {
	if ctx.Client.BaseURL == "" {
		return usageError(
			"no server URL: pass --url, set %s, or name one in the configuration file (--config <path>, default %s)",
			envURLVar, defaultConfigPath(ctx.Env))
	}

	return nil
}

// jsonOrText keeps a JSON body intact and falls back to text for anything
// else, so the envelope always carries valid JSON.
func jsonOrText(body []byte) any {
	if json.Valid(body) {
		return json.RawMessage(body)
	}

	return string(body)
}

// fieldValue reads one string field out of a JSON response body.
func fieldValue(body []byte, field string) string {
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		return ""
	}

	value, _ := fields[field].(string)

	return value
}

// systemFieldOrder is the order the probe fields are printed in, with the
// fields that matter to a reader first.
var systemFieldOrder = []string{"status", "version", "database", "error"}

// humanVersion prints the version pair and the server's answer, when probed.
func humanVersion(w io.Writer, data any) {
	value, ok := data.(versionData)
	if !ok {
		printJSONValue(w, data)

		return
	}

	pairs := [][2]string{{"version", value.Version}, {"apiVersion", value.APIVersion}}
	switch {
	case value.Server == nil:
		pairs = append(pairs, [2]string{"server", "not probed (no URL configured)"})
	case value.Server.Reachable:
		pairs = append(pairs, [2]string{"server", "reachable, version " + value.Server.Version})
	default:
		pairs = append(pairs, [2]string{"server", "unreachable: " + value.Server.Error})
	}

	printKV(w, pairs)
}

// humanSystemBody prints a probe body as key/value lines.
func humanSystemBody(w io.Writer, data any) {
	raw, ok := data.(json.RawMessage)
	if !ok {
		if text, isText := data.(string); isText {
			fmt.Fprintln(w, text)

			return
		}
		printJSONValue(w, data)

		return
	}

	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		fmt.Fprintln(w, string(raw))

		return
	}

	printKV(w, orderedFields(fields))
}

// orderedFields lists the fields that matter first, then the rest in name
// order, so the output is stable between runs.
func orderedFields(fields map[string]any) [][2]string {
	seen := make(map[string]bool, len(fields))
	pairs := make([][2]string, 0, len(fields))

	for _, name := range systemFieldOrder {
		value, present := fields[name]
		if !present {
			continue
		}
		seen[name] = true
		pairs = append(pairs, [2]string{name, fmt.Sprint(value)})
	}

	rest := make([]string, 0, len(fields))
	for name := range fields {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	for _, name := range rest {
		pairs = append(pairs, [2]string{name, fmt.Sprint(fields[name])})
	}

	return pairs
}

// humanHelp prints one line per registered verb.
func humanHelp(w io.Writer, data any) {
	rows, ok := data.([]verbSummary)
	if !ok {
		printJSONValue(w, data)

		return
	}

	pairs := make([][2]string, 0, len(rows))
	for _, row := range rows {
		summary := row.Summary
		if row.Guarded {
			summary += " [requires --yes]"
		}
		pairs = append(pairs, [2]string{"kilasflow " + row.Path, summary})
	}

	printKV(w, pairs)
}
