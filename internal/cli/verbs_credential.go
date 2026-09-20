package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// credentialVerbs are the read-only credential verbs.
//
// `credential test` is the exception that proves the rule about "read-only":
// it POSTs, but it stores nothing and returns no secret — it runs the
// credential type's probe and reports pass or fail. Requiring the escape hatch
// for it would mean an agent that stored a credential could not check it
// without a route nobody wrote.
//
// Deliberately absent: create, update and delete (phase 2, where a scoped token
// exists to refuse them, and guarded), and `credential types` (the design's tree
// does not have it; `kilasflow api list-credential-types` reaches the catalogue,
// and `api test-credential-payload` tests an unsaved one).
func credentialVerbs() []Verb {
	return []Verb{
		{
			Path:      "credential list",
			Operation: "list-credentials",
			Summary:   "list stored credentials, without any secret value (`--limit`, `--cursor`)",
			Flags:     registerPageFlags,
			Run:       runCredentialList,
			Human:     humanResourceList("id", "name", "type", "updatedAt"),
		},
		{
			Path:      "credential get",
			Operation: "get-credential",
			Summary:   "read one credential, without its secret value",
			Run:       runCredentialGet,
			Human:     humanResourceResource,
		},
		{
			Path:      "credential test",
			Operation: "test-credential",
			Summary:   "run the credential type's probe and report pass or fail",
			Run:       runCredentialTest,
			Human:     humanCredentialTest,
		},
	}
}

// runCredentialList reads one page of credentials.
func runCredentialList(ctx *Context, args []string) error {
	if err := refusePositional(args, "credential list"); err != nil {
		return err
	}

	list, err := ctx.listPage("/credentials", pageQuery(ctx))
	if err != nil {
		return err
	}

	return listPageResult(ctx, list)
}

// runCredentialGet reads one credential.
func runCredentialGet(ctx *Context, args []string) error {
	id, err := requireOneID(args, "credential id")
	if err != nil {
		return err
	}

	return ctx.readResource(http.MethodGet, "/credentials/"+url.PathEscape(id), nil, id)
}

// runCredentialTest runs the stored credential's probe.
func runCredentialTest(ctx *Context, args []string) error {
	id, err := requireOneID(args, "credential id")
	if err != nil {
		return err
	}

	// No body: the probe reads the stored credential. Sending the secret back
	// to be tested would be sending it for no reason.
	path := "/credentials/" + url.PathEscape(id) + "/test"

	resp, err := ctx.Client.Do(ctx.Ctx, http.MethodPost, apiPath(path), nil, nil, nil)
	if err != nil {
		return err
	}

	ctx.Data = jsonOrText(resp.Body)
	// The verdict is what a pipeline branches on, so it is what --quiet prints:
	// the credential id is already on the command line.
	ctx.Primary = strconv.FormatBool(credentialVerdict(resp.Body))

	return nil
}

// credentialVerdict reads the probe's pass/fail out of the response.
func credentialVerdict(body []byte) bool {
	var verdict struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(body, &verdict); err != nil {
		return false
	}

	return verdict.OK
}

// humanCredentialTest prints the verdict, the explanation, and which fields the
// probe took from storage rather than from a payload — without that last line,
// "it works" is ambiguous exactly when the caller was editing a secret.
func humanCredentialTest(w io.Writer, data any) {
	raw, ok := data.(json.RawMessage)
	if !ok {
		printJSONValue(w, data)

		return
	}

	var verdict struct {
		OK                  bool     `json:"ok"`
		Detail              string   `json:"detail"`
		ResolvedFromStorage []string `json:"resolvedFromStorage"`
		Untestable          bool     `json:"untestable"`
	}
	if err := json.Unmarshal(raw, &verdict); err != nil {
		printJSONValue(w, data)

		return
	}

	pairs := [][2]string{
		{"ok", strconv.FormatBool(verdict.OK)},
		{"detail", verdict.Detail},
	}
	if verdict.Untestable {
		pairs = append(pairs, [2]string{"untestable", "this server has no probe for the type"})
	}
	if len(verdict.ResolvedFromStorage) > 0 {
		pairs = append(pairs, [2]string{"from storage", strings.Join(verdict.ResolvedFromStorage, ", ")})
	}

	printKV(w, pairs)
}
