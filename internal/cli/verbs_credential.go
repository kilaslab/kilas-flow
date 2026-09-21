package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// credentialVerbs are the credential verbs: the read-only pair, the probe, and
// the three guarded writes.
//
// `credential test` is the exception that proves the rule about "read-only":
// it POSTs, but it stores nothing and returns no secret — it runs the
// credential type's probe and reports pass or fail. Requiring the escape hatch
// for it would mean an agent that stored a credential could not check it
// without a route nobody wrote.
//
// create, update and delete are guarded: a credential is a secret every
// workflow in the tenant can reach, so storing, replacing and removing one is
// the tenant's decision, not a scoped key's. They carry the same `--file`
// document `workflow create` carries, sent unchanged, because the fields a
// credential type declares are the server's business — a CLI that knew them
// would have to be released with every type.
//
// Deliberately absent: `credential types` (the design's tree does not have it;
// `kilasflow api list-credential-types` reaches the catalogue, and `api
// test-credential-payload` tests an unsaved one).
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
			Args:      []Arg{arg("credential id")},
			Run:       runCredentialGet,
			Human:     humanResourceResource,
		},
		{
			Path:      "credential test",
			Operation: "test-credential",
			Summary:   "run the credential type's probe and report pass or fail",
			Args:      []Arg{arg("credential id")},
			Run:       runCredentialTest,
			Human:     humanCredentialTest,
		},
		{
			Path:      "credential create",
			Operation: "create-credential",
			Summary:   "store a credential from a JSON document (`--file <path>|-`, guarded)",
			Guarded:   true,
			Refusal:   "stores a secret every workflow in the tenant can use",
			Flags:     registerCreateFlags,
			Run:       runCredentialCreate,
			Human:     humanResourceResource,
		},
		{
			Path:      "credential update",
			Operation: "update-credential",
			Summary:   "replace a stored credential from a JSON document (`--file <path>|-`, guarded)",
			Args:      []Arg{arg("credential id")},
			Guarded:   true,
			Refusal:   "replaces a stored secret",
			Flags:     registerCreateFlags,
			Run:       runCredentialUpdate,
			Human:     humanResourceResource,
		},
		{
			Path:      "credential delete",
			Operation: "delete-credential",
			Summary:   "remove a stored credential (guarded)",
			Args:      []Arg{arg("credential id")},
			Guarded:   true,
			Refusal:   "removes a stored secret other workflows may depend on",
			Run:       runCredentialDelete,
			Human:     humanDeletion,
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

// runCredentialCreate stores a credential from a JSON document.
//
// The document is sent unchanged, the way `workflow create` sends one, and it
// is checked for being JSON before it is sent for the same reason: the API
// would answer a malformed body with 422, which the exit contract maps to "fix
// the invocation" anyway.
func runCredentialCreate(ctx *Context, args []string) error {
	if err := refusePositional(args, "credential create"); err != nil {
		return err
	}

	document, err := credentialDocument(ctx, "credential create")
	if err != nil {
		return err
	}

	resp, err := ctx.Client.Do(ctx.Ctx, http.MethodPost, apiPath("/credentials"), nil, nil, document)
	if err != nil {
		return err
	}

	ctx.Data = jsonOrText(resp.Body)
	// The id the API named in its Location header, like `workflow create`: a
	// --quiet caller pipes the credential it just stored onward.
	ctx.Primary = locationID(resp.Header.Get("Location"))
	if ctx.Primary == "" {
		ctx.Primary = fieldValue(resp.Body, "id")
	}

	return nil
}

// runCredentialUpdate replaces a stored credential.
//
// The API is explicit about what a body may carry: a field sent as the
// redaction placeholder keeps the stored secret, so an update that changes only
// the name does not have to resend one.
func runCredentialUpdate(ctx *Context, args []string) error {
	id, err := requireOneID(args, "credential id")
	if err != nil {
		return err
	}

	document, err := credentialDocument(ctx, "credential update")
	if err != nil {
		return err
	}

	return ctx.writeResource(http.MethodPut, "/credentials/"+url.PathEscape(id), document, id)
}

// runCredentialDelete removes a stored credential.
func runCredentialDelete(ctx *Context, args []string) error {
	id, err := requireOneID(args, "credential id")
	if err != nil {
		return err
	}

	return ctx.deleteResource("/credentials/"+url.PathEscape(id), id)
}

// credentialDocument reads the credential payload --file names.
func credentialDocument(ctx *Context, verb string) ([]byte, error) {
	flags, ok := ctx.VerbFlags.(*createFlags)
	if !ok {
		return nil, usageError("the %s verb was registered without its flags", verb)
	}
	if strings.TrimSpace(flags.file) == "" {
		return nil, usageError("no document: pass --file <path>, or --file - to read it from stdin")
	}

	return readDocument(flags.file, ctx.Env.Stdin)
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
