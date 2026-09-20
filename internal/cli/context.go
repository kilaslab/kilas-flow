package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// contextBulletinLimit caps each listing the briefing carries. An agent's first
// turn wants to know what exists, not to download a tenant's whole catalogue;
// every listing says whether it was cut short and the verbs that page exist.
const contextBulletinLimit = 20

// contextVerbs is the verb an agent runs first.
//
// It is local: it reads six operations rather than driving one, so it names no
// operation id — the same rule the other local verbs follow — and meta.operation
// falls back to the verb path "context".
func contextVerbs() []Verb {
	return []Verb{
		{
			Path:    "context",
			Summary: "read-only briefing for a first turn: server, identity, workflows, datastores, node types",
			Run:     runContext,
			Human:   humanContext,
		},
	}
}

// contextPayload is the briefing.
//
// The shape is deliberately flat and always carries every section: a section
// that failed says so under its own `error` rather than disappearing, because
// an agent that cannot tell "there are no workflows" from "listing workflows
// was refused" will plan the wrong next step.
type contextPayload struct {
	CLI        contextCLI      `json:"cli"`
	Server     contextServer   `json:"server"`
	Identity   contextIdentity `json:"identity"`
	Workflows  contextListing  `json:"workflows"`
	Datastores contextListing  `json:"datastores"`
	NodeTypes  contextCount    `json:"nodeTypes"`
}

// contextCLI is what the binary itself is.
type contextCLI struct {
	Version string `json:"version"`
}

// contextServer is the instance the CLI reached.
type contextServer struct {
	URL    string `json:"url"`
	Ready  bool   `json:"ready"`
	Health any    `json:"health"`
}

// contextListing is one capped, paged listing.
type contextListing struct {
	Count     int              `json:"count"`
	Items     []map[string]any `json:"items"`
	Truncated bool             `json:"truncated"`
	Error     *contextError    `json:"error,omitempty"`
}

// contextCount is a listing only the size of which matters.
type contextCount struct {
	Count int           `json:"count"`
	Error *contextError `json:"error,omitempty"`
}

// contextError is one section's failure.
type contextError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// contextIdentity is the caller's identity, projected onto the fields an agent
// can act on. The credential itself has no field here and never will, and a
// failure to read it is recorded under `error` the way every other section's is.
type contextIdentity struct {
	TenantID string        `json:"tenantId,omitempty"`
	Kind     string        `json:"kind,omitempty"`
	Label    string        `json:"label,omitempty"`
	KeyID    string        `json:"keyId,omitempty"`
	UserID   string        `json:"userId,omitempty"`
	Error    *contextError `json:"error,omitempty"`
}

// runContext composes the briefing from six read operations.
//
// Readiness is the one section that fails the verb: every other answer is about
// a server that is serving, and reporting a briefing full of section errors for
// an instance that cannot serve would only hide the reason. The rest fail
// independently.
func runContext(ctx *Context, _ []string) error {
	if err := requireServerURL(ctx); err != nil {
		return err
	}

	payload := contextPayload{CLI: contextCLI{Version: ctx.Env.Version}}
	payload.Server.URL = ctx.Client.BaseURL

	if _, err := ctx.get(apiPath("/ready")); err != nil {
		return err
	}
	payload.Server.Ready = true
	payload.Server.Health = ctx.sectionJSON("/health")

	identity, err := ctx.get(apiPath("/auth/me"))
	// An auth-disabled instance genuinely has no identity, so a refused or
	// unreadable principal is a section failure and not a failure of the
	// briefing.
	switch {
	case err != nil:
		payload.Identity = contextIdentity{Error: sectionError(err)}
	default:
		principal, err := projectIdentity(identity)
		if err != nil {
			payload.Identity = contextIdentity{Error: sectionError(err)}
		} else {
			payload.Identity = principal
		}
	}

	payload.Workflows = ctx.listSection("/workflows", func(body []byte) ([]map[string]any, error) {
		var items []map[string]any
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, fmt.Errorf("workflow list is not an array: %w", err)
		}

		return project(items, "id", "name", "active"), nil
	})

	payload.Datastores = ctx.listSection("/datastores", func(body []byte) ([]map[string]any, error) {
		var page struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("datastore page is not an object: %w", err)
		}

		return project(page.Items, "id", "name"), nil
	})

	nodeTypes, err := ctx.get(apiPath("/node-types"))
	if err != nil {
		payload.NodeTypes = contextCount{Error: sectionError(err)}
	} else {
		var definitions []json.RawMessage
		if err := json.Unmarshal(nodeTypes, &definitions); err != nil {
			payload.NodeTypes = contextCount{Error: sectionError(fmt.Errorf("node catalogue is not a list: %w", err))}
		} else {
			payload.NodeTypes = contextCount{Count: len(definitions)}
		}
	}

	ctx.Data = payload

	return nil
}

// get performs one read of the briefing.
func (ctx *Context) get(path string) ([]byte, error) {
	resp, err := ctx.Client.Do(ctx.Ctx, http.MethodGet, path, nil, nil, nil)
	if err != nil {
		return nil, err
	}

	return resp.Body, nil
}

// sectionJSON reads one section that is its own payload, falling back to a
// recorded section error rather than failing the briefing.
func (ctx *Context) sectionJSON(path string) any {
	body, err := ctx.get(apiPath(path))
	if err != nil {
		return sectionError(err)
	}

	return jsonOrText(body)
}

// listSection reads one capped listing and records its page size and whether
// the server had more to give.
//
// A payload the collector cannot read is a section failure rather than an empty
// list: "nothing exists" and "this answer made no sense" are different facts,
// and only one of them means the agent should stop looking.
func (ctx *Context) listSection(path string, collect func([]byte) ([]map[string]any, error)) contextListing {
	query := url.Values{"limit": {strconv.Itoa(contextBulletinLimit)}}

	resp, err := ctx.Client.Do(ctx.Ctx, http.MethodGet, apiPath(path), query, nil, nil)
	if err != nil {
		return contextListing{Items: []map[string]any{}, Error: sectionError(err)}
	}

	items, err := collect(resp.Body)
	if err != nil {
		return contextListing{Items: []map[string]any{}, Error: sectionError(err)}
	}
	if items == nil {
		items = []map[string]any{}
	}

	return contextListing{
		Count: len(items),
		Items: items,
		// The cursor is what the server sends to say "there is another page";
		// counting the page instead would call a full final page truncated.
		Truncated: resp.Header.Get("X-Next-Cursor") != "",
	}
}

// projectIdentity keeps the fields an agent can act on out of the principal
// resource. A principal the response does not carry is an error rather than an
// empty identity, so "nobody is signed in" cannot be read as a caller with no
// tenant.
func projectIdentity(principal []byte) (contextIdentity, error) {
	var resource struct {
		TenantID string `json:"tenantId"`
		Kind     string `json:"kind"`
		Label    string `json:"label"`
		KeyID    string `json:"keyId"`
		UserID   string `json:"userId"`
	}
	if err := json.Unmarshal(principal, &resource); err != nil {
		return contextIdentity{}, fmt.Errorf("principal is not an object: %w", err)
	}

	return contextIdentity{
		TenantID: resource.TenantID,
		Kind:     resource.Kind,
		Label:    resource.Label,
		KeyID:    resource.KeyID,
		UserID:   resource.UserID,
	}, nil
}

// project narrows each item to the named fields, so a briefing does not grow
// every time the API's resource grows a field.
func project(items []map[string]any, fields ...string) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		narrowed := make(map[string]any, len(fields))
		for _, field := range fields {
			if value, present := item[field]; present {
				narrowed[field] = value
			}
		}
		out = append(out, narrowed)
	}

	return out
}

// sectionError turns a section's failure into the value the section carries.
func sectionError(err error) *contextError {
	failure := asExitError(err)

	return &contextError{Code: failure.ErrCode, Message: failure.Message}
}

// humanContext prints the briefing as headings and lines.
func humanContext(w io.Writer, data any) {
	payload, ok := data.(contextPayload)
	if !ok {
		printJSONValue(w, data)

		return
	}

	printKV(w, [][2]string{
		{"cli", payload.CLI.Version},
		{"server", payload.Server.URL},
		{"ready", strconv.FormatBool(payload.Server.Ready)},
	})

	if payload.Identity.Error != nil {
		printKV(w, [][2]string{{"identity", payload.Identity.Error.Code + ": " + payload.Identity.Error.Message}})
	} else {
		printKV(w, [][2]string{
			{"identity", payload.Identity.Kind + " " + payload.Identity.Label},
			{"tenant", payload.Identity.TenantID},
		})
	}

	printKV(w, [][2]string{
		{"workflows", listingSummary(payload.Workflows)},
		{"datastores", listingSummary(payload.Datastores)},
		{"nodeTypes", countSummary(payload.NodeTypes.Count, payload.NodeTypes.Error)},
	})
}

// listingSummary renders one listing for a terminal.
func listingSummary(listing contextListing) string {
	if listing.Error != nil {
		return listing.Error.Code + ": " + listing.Error.Message
	}

	summary := strconv.Itoa(listing.Count) + " listed"
	if listing.Truncated {
		summary += " (more available)"
	}

	return summary
}

// countSummary renders a count-only section for a terminal.
func countSummary(count int, failure *contextError) string {
	if failure != nil {
		return failure.Code + ": " + failure.Message
	}

	return strconv.Itoa(count)
}
