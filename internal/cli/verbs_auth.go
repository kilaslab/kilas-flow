package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// authVerbs are the credential verbs: the only part of the CLI that writes a
// credential down rather than reading one.
//
// `auth login` and `auth logout` carry no operation id, and that is a
// deliberate reading of the design's one-verb-per-operation rule: the `login`
// and `logout` operations mint and clear a *browser cookie*, which is not what
// this stores. What this verb does is local — validate a token against
// `get-me` and write it to the caller's own configuration file at 0600 — so it
// is the file, not an operation, that it is about. `auth whoami` reads the
// identity the stored credential resolves to, and that is `get-me`.
func authVerbs() []Verb {
	return []Verb{
		{
			Path:    "auth login",
			Summary: "validate a token against the server and store it at 0600 in the configuration file",
			Run:     runAuthLogin,
			Human:   humanAuthLogin,
		},
		{
			Path:    "auth logout",
			Summary: "remove the stored token, keeping the server URL",
			Run:     runAuthLogout,
			Human:   humanAuthLogout,
		},
		{
			Path:      "auth whoami",
			Operation: "get-me",
			Summary:   "print the identity the configured credential resolves to",
			Run:       runAuthWhoami,
			Human:     humanIdentity,
		},
	}
}

// loginResult is what `auth login` reports. The credential has no field here
// and never will: the caller already has it, and printing it back would put it
// in a transcript.
type loginResult struct {
	URL        string `json:"url"`
	TenantID   string `json:"tenantId,omitempty"`
	Kind       string `json:"kind,omitempty"`
	ConfigPath string `json:"configPath"`
}

// logoutResult is what `auth logout` reports.
type logoutResult struct {
	URL        string `json:"url"`
	ConfigPath string `json:"configPath"`
}

// runAuthLogin stores a credential the server accepts.
//
// The token is validated before it is written: a configuration file holding a
// token the server refuses is worse than no file, because the next command
// fails somewhere else and the reason is no longer visible.
func runAuthLogin(ctx *Context, args []string) error {
	if err := refusePositional(args, "auth login"); err != nil {
		return err
	}
	if err := requireServerURL(ctx); err != nil {
		return err
	}

	token, err := loginToken(ctx)
	if err != nil {
		return err
	}

	identity, err := whoamiWith(ctx, token)
	if err != nil {
		return err
	}

	settings, err := ctx.settings()
	if err != nil {
		return err
	}
	if err := saveConfig(settings.ConfigPath, fileConfig{URL: settings.URL, Token: token}); err != nil {
		return err
	}

	ctx.Data = loginResult{
		URL:        settings.URL,
		TenantID:   identity.TenantID,
		Kind:       identity.Kind,
		ConfigPath: settings.ConfigPath,
	}
	ctx.Primary = identity.TenantID

	return nil
}

// loginToken resolves the credential to store.
//
// A token passed as the flag's value is refused outright: it lands in shell
// history and in the process listing, and the whole point of this verb is to
// keep it out of both.
func loginToken(ctx *Context) (string, error) {
	flags := ctx.Flags

	switch {
	case flags.wasProvided("token"):
		if strings.TrimSpace(flags.Token) != "-" {
			return "", usageError(
				"a token must not be passed on the command line (it lands in shell history and the process listing); "+
					"use `--token -`, `--token-file <path>`, or %s", envTokenVar)
		}

		raw, err := io.ReadAll(ctx.Env.Stdin)
		if err != nil {
			return "", usageError("could not read the token from stdin: %v", err)
		}

		token := strings.TrimSpace(string(raw))
		if token == "" {
			return "", usageError("no token on stdin: pipe one in, e.g. `printf %%s \"$TOKEN\" | kilasflow auth login --token -`")
		}

		return token, nil
	case flags.wasProvided("token-file"):
		return readTokenFile(flags.TokenFile)
	case strings.TrimSpace(getenv(ctx.Env, envTokenVar)) != "":
		return strings.TrimSpace(getenv(ctx.Env, envTokenVar)), nil
	default:
		return "", usageError(
			"no token to store: use `--token -`, `--token-file <path>`, or set %s", envTokenVar)
	}
}

// whoamiWith reads the identity a candidate credential resolves to, without
// touching the invocation's own client, so an invalid candidate cannot be
// mistaken for the configured one.
func whoamiWith(ctx *Context, token string) (identity, error) {
	probe := *ctx.Client
	probe.Token = token

	resp, err := probe.Do(ctx.Ctx, http.MethodGet, apiPath("/auth/me"), nil, nil, nil)
	if err != nil {
		return identity{}, err
	}

	return decodeIdentity(resp.Body)
}

// runAuthLogout removes the stored credential and keeps everything else.
//
// With no configuration file there is nothing to remove, and that is a success:
// a script that logs out an installation which was never logged in has got what
// it asked for.
func runAuthLogout(ctx *Context, args []string) error {
	if err := refusePositional(args, "auth logout"); err != nil {
		return err
	}

	settings, err := ctx.settings()
	if err != nil {
		return err
	}

	if settings.FileExists {
		file := settings.File
		file.Token = ""
		if err := saveConfig(settings.ConfigPath, file); err != nil {
			return err
		}
	}

	ctx.Data = logoutResult{URL: settings.URL, ConfigPath: settings.ConfigPath}

	return nil
}

// runAuthWhoami prints the identity behind the configured credential.
func runAuthWhoami(ctx *Context, args []string) error {
	if err := refusePositional(args, "auth whoami"); err != nil {
		return err
	}
	if err := requireServerURL(ctx); err != nil {
		return err
	}

	resp, err := ctx.Client.Do(ctx.Ctx, http.MethodGet, apiPath("/auth/me"), nil, nil, nil)
	if err != nil {
		return err
	}

	principal, err := decodeIdentity(resp.Body)
	if err != nil {
		return err
	}

	ctx.Data = principal
	ctx.Primary = principal.TenantID

	return nil
}

// identity is the caller as an agent can act on it: who, how, and with which
// key. It is a projection, so a field the API grows does not appear here by
// accident, and a credential field can never appear here at all.
type identity struct {
	TenantID string `json:"tenantId,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Label    string `json:"label,omitempty"`
	KeyID    string `json:"keyId,omitempty"`
	UserID   string `json:"userId,omitempty"`
	// The three fields that say what this credential may do. A tenant-wide
	// key carries none of them, which is exactly what a guarded verb reads
	// before it refuses: the scope list is the difference between a key that
	// may activate a workflow and one that may not.
	Scopes     []string `json:"scopes,omitempty"`
	WorkflowID string   `json:"workflowId,omitempty"`
	ExpiresAt  string   `json:"expiresAt,omitempty"`
}

// Scoped reports whether this credential is an agent token rather than the
// tenant's own key. It is the one question the guarded verbs turn on.
func (who identity) Scoped() bool { return len(who.Scopes) > 0 }

// decodeIdentity reads a principal resource.
func decodeIdentity(body []byte) (identity, error) {
	var principal identity
	if err := json.Unmarshal(body, &principal); err != nil {
		return identity{}, &ExitError{
			Code:    ExitFailure,
			ErrCode: "unexpected_response",
			Message: "the identity resource is not an object: " + err.Error(),
		}
	}

	return principal, nil
}

// refusePositional rejects an argument a verb has no use for: accepting one
// silently would let a typo pass as a successful command.
func refusePositional(args []string, verb string) error {
	if len(args) > 0 {
		return usageError("%s takes no arguments; got %q", verb, args[0])
	}

	return nil
}

// humanAuthLogin prints where the credential went, and as whom it acts.
func humanAuthLogin(w io.Writer, data any) {
	result, ok := data.(loginResult)
	if !ok {
		printJSONValue(w, data)

		return
	}

	printKV(w, [][2]string{
		{"stored in", result.ConfigPath},
		{"server", result.URL},
		{"identity", strings.TrimSpace(result.Kind + " " + result.TenantID)},
	})
}

// humanAuthLogout prints what the file now says.
func humanAuthLogout(w io.Writer, data any) {
	result, ok := data.(logoutResult)
	if !ok {
		printJSONValue(w, data)

		return
	}

	printKV(w, [][2]string{
		{"removed from", result.ConfigPath},
		{"server", result.URL},
	})
}

// humanIdentity prints the caller as key/value lines.
func humanIdentity(w io.Writer, data any) {
	principal, ok := data.(identity)
	if !ok {
		printJSONValue(w, data)

		return
	}

	rows := [][2]string{
		{"tenant", principal.TenantID},
		{"kind", principal.Kind},
		{"label", principal.Label},
		{"key", principal.KeyID},
		{"user", principal.UserID},
	}
	// Printed only when they exist: an empty "scopes" line on a tenant-wide
	// key would read as a key with no authority rather than as one with all
	// of it.
	if principal.Scoped() {
		rows = append(rows, [2]string{"scopes", strings.Join(principal.Scopes, ", ")})
	}
	if principal.WorkflowID != "" {
		rows = append(rows, [2]string{"bound to", principal.WorkflowID})
	}
	if principal.ExpiresAt != "" {
		rows = append(rows, [2]string{"expires", principal.ExpiresAt})
	}

	printKV(w, rows)
}
