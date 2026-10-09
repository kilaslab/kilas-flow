package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/docexamples"
)

// The page names ids as shell variables, so an example is a command that runs as
// written and not a placeholder to be edited. The runner provides each one by
// driving the binary under test, once per block of examples, because a block may
// end by deleting what the next one acts on.

var variablePattern = regexp.MustCompile(`\$\{?([A-Z][A-Z0-9_]*)\}?`)

// variablesUsed lists the upper-case shell variables a command line reads, sorted
// and without repeats. A lower-case one is a loop variable and `$json` is an
// expression handed to a verb in single quotes, neither of which the runner seeds.
func variablesUsed(text string) []string {
	seen := map[string]bool{}
	var names []string
	for _, match := range variablePattern.FindAllStringSubmatch(text, -1) {
		if !seen[match[1]] {
			seen[match[1]] = true
			names = append(names, match[1])
		}
	}
	sort.Strings(names)

	return names
}

func usesVariable(text, name string) bool {
	for _, used := range variablesUsed(text) {
		if used == name {
			return true
		}
	}

	return false
}

// needs is what a block has to have before it runs.
type needs struct {
	workflow   bool
	version    bool
	execution  bool
	credential bool
	// inUseCredential is a credential a workflow of its own attaches, so a
	// delete of it is refused unless forced.
	inUseCredential bool
	datastore       bool
	tenant          bool
}

// provided are the variables every example already has: the environment the
// runner sets, and the cursor, which is read per example.
var provided = map[string]bool{
	"TOKEN": true, "HOME": true, "PATH": true,
	"KILASFLOW_URL": true, "KILASFLOW_TOKEN": true, "CURSOR": true,
}

// tenantPattern finds the one tenant the page names. A tenant is a slug, not an
// id the server issues, so it is a word in the command and not a variable.
var tenantPattern = regexp.MustCompile(`\bacme\b`)

// needsOf reads what a block's commands use. unknown lists the variables nothing
// seeds, so a new example cannot quietly depend on one.
func needsOf(texts []string) (needed needs, unknown []string) {
	for _, text := range texts {
		for _, name := range variablesUsed(text) {
			switch name {
			case "WORKFLOW_ID":
				needed.workflow = true
			case "VERSION_ID":
				needed.workflow, needed.version = true, true
			case "EXECUTION_ID":
				needed.workflow, needed.execution = true, true
			case "CREDENTIAL_ID":
				needed.credential = true
			case "IN_USE_CREDENTIAL_ID":
				needed.inUseCredential = true
			case "DATASTORE_ID":
				needed.datastore = true
			default:
				if !provided[name] {
					unknown = append(unknown, name)
				}
			}
		}
		if tenantPattern.MatchString(text) {
			needed.tenant = true
		}
		if usesVariable(text, "CURSOR") {
			// A listing has to have a second page to hand out a cursor to.
			needed.workflow = true
		}
	}
	sort.Strings(unknown)

	return needed, dedupe(unknown)
}

func dedupe(sorted []string) []string {
	var out []string
	for index, name := range sorted {
		if index == 0 || name != sorted[index-1] {
			out = append(out, name)
		}
	}

	return out
}

// seed creates what a block needs, through the binary under test.
func (r *runner) seed(needed needs) (map[string]string, error) {
	vars := map[string]string{}
	r.counter++

	if needed.workflow {
		id, err := r.cli("workflow", "create", "--file", "workflow.json", "--quiet")
		if err != nil {
			return nil, err
		}
		vars["WORKFLOW_ID"] = id
		// Two revisions, so a listing of versions has a second page at --limit 1.
		if _, err := r.cli("api", "update-workflow", "--path", "id="+id, "--body", "@workflow.json"); err != nil {
			return nil, err
		}
	}
	if needed.version {
		out, err := r.cli("workflow", "versions", vars["WORKFLOW_ID"], "--json")
		if err != nil {
			return nil, err
		}
		id, err := firstItemID(out)
		if err != nil {
			return nil, fmt.Errorf("read a revision id from `workflow versions`: %w", err)
		}
		vars["VERSION_ID"] = id
	}
	if needed.execution {
		id, err := r.cli("run", vars["WORKFLOW_ID"], "--wait", "--quiet")
		if err != nil {
			return nil, err
		}
		vars["EXECUTION_ID"] = id
	}
	if needed.credential {
		file := filepath.Join(r.scratch, fmt.Sprintf("seed-credential-%d.json", r.counter))
		document := fmt.Sprintf(`{"name":"Seeded %d","type":"httpHeaderAuth","fields":{"name":"X-Api-Key","value":"<api-key>"}}`, r.counter)
		if err := os.WriteFile(file, []byte(document), 0o600); err != nil {
			return nil, err
		}
		id, err := r.cli("credential", "create", "--file", file, "--yes", "--quiet")
		if err != nil {
			return nil, err
		}
		vars["CREDENTIAL_ID"] = id
	}
	if needed.inUseCredential {
		file := filepath.Join(r.scratch, fmt.Sprintf("seed-in-use-credential-%d.json", r.counter))
		document := fmt.Sprintf(`{"name":"In use %d","type":"httpHeaderAuth","fields":{"name":"X-Api-Key","value":"<api-key>"}}`, r.counter)
		if err := os.WriteFile(file, []byte(document), 0o600); err != nil {
			return nil, err
		}
		id, err := r.cli("credential", "create", "--file", file, "--yes", "--quiet")
		if err != nil {
			return nil, err
		}
		template, err := os.ReadFile(filepath.Join(r.scratch, "workflow.json"))
		if err != nil {
			return nil, err
		}
		attached, err := inUseWorkflowDocument(template, id)
		if err != nil {
			return nil, err
		}
		workflowFile := filepath.Join(r.scratch, fmt.Sprintf("seed-in-use-workflow-%d.json", r.counter))
		if err := os.WriteFile(workflowFile, attached, 0o600); err != nil {
			return nil, err
		}
		if _, err := r.cli("workflow", "create", "--file", workflowFile, "--quiet"); err != nil {
			return nil, err
		}
		vars["IN_USE_CREDENTIAL_ID"] = id
	}
	if needed.datastore {
		id, err := r.cli("datastore", "create", fmt.Sprintf("seeded-%d", r.counter), "--yes", "--quiet")
		if err != nil {
			return nil, err
		}
		vars["DATASTORE_ID"] = id
	}
	if needed.tenant {
		// A block that deleted the tenant leaves nothing behind, and one that did
		// not leaves it in place, so only create it when it is missing.
		if _, err := r.cli("tenant", "get", "acme"); err != nil {
			if _, err := r.cli("api", "create-tenant", "--body", `{"id":"acme","name":"Acme"}`, "--yes"); err != nil {
				return nil, err
			}
		}
	}

	return vars, nil
}

// inUseWorkflowDocument is the examples' workflow with its HTTP Request node
// attaching a header credential, so that credential is in use.
func inUseWorkflowDocument(template []byte, credentialID string) ([]byte, error) {
	var document map[string]any
	if err := json.Unmarshal(template, &document); err != nil {
		return nil, fmt.Errorf("read the workflow fixture: %w", err)
	}
	nodes, _ := document["nodes"].([]any)
	attached := false
	for _, entry := range nodes {
		node, _ := entry.(map[string]any)
		if node["type"] == "kilasflow.httpRequest" {
			node["credentials"] = map[string]any{"httpHeaderAuth": credentialID}
			attached = true
		}
	}
	if !attached {
		return nil, fmt.Errorf("the workflow fixture has no HTTP Request node to attach a credential to")
	}

	return json.Marshal(document)
}

// firstItemID reads data.items[0].id from a CLI envelope.
func firstItemID(envelope string) (string, error) {
	var decoded struct {
		Data struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(envelope), &decoded); err != nil {
		return "", err
	}
	if len(decoded.Data.Items) == 0 || decoded.Data.Items[0].ID == "" {
		return "", fmt.Errorf("the listing is empty: %s", envelope)
	}

	return decoded.Data.Items[0].ID, nil
}

// cursor reads a real cursor for an example that passes one: the example's own
// listing, asked for a single item, hands out the cursor of its second page.
func (r *runner) cursor(example docexamples.Example, vars map[string]string) (string, error) {
	probe, err := probeCommand(example.Words)
	if err != nil {
		return "", err
	}
	code, output, err := r.shell(probe, vars)
	if err != nil {
		return "", fmt.Errorf("cursor probe %q: %w", probe, err)
	}
	if code != 0 {
		return "", fmt.Errorf("cursor probe %q exited %d:\n%s", probe, code, indent(output))
	}
	var decoded struct {
		Data struct {
			NextCursor string `json:"nextCursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &decoded); err != nil {
		return "", fmt.Errorf("cursor probe %q did not print an envelope: %w", probe, err)
	}
	if decoded.Data.NextCursor == "" {
		return "", fmt.Errorf("cursor probe %q returned no nextCursor: the seed has too little data to page", probe)
	}

	return decoded.Data.NextCursor, nil
}

// probeCommand is the example's own command with its paging replaced by one item
// per page and a JSON envelope, as a line for sh. Variables stay expandable.
func probeCommand(words []string) (string, error) {
	hasCursor := false
	kept := make([]string, 0, len(words)+3)
	for index := 0; index < len(words); index++ {
		word := words[index]
		switch {
		case word == "--cursor" || word == "--limit":
			hasCursor = hasCursor || word == "--cursor"
			index++
		case strings.HasPrefix(word, "--cursor="):
			hasCursor = true
		case strings.HasPrefix(word, "--limit="):
		default:
			kept = append(kept, word)
		}
	}
	if !hasCursor {
		return "", fmt.Errorf("the command passes no --cursor, so there is nothing to probe")
	}
	kept = append(kept, "--limit", "1", "--json")

	quoted := make([]string, len(kept))
	for index, word := range kept {
		if plainWord.MatchString(word) {
			quoted[index] = word

			continue
		}
		quoted[index] = shellQuote(word)
	}

	return strings.Join(quoted, " "), nil
}

// plainWord is a word a shell reads as itself, which needs no quoting.
var plainWord = regexp.MustCompile(`^[A-Za-z0-9_./:=@%+,-]+$`)

// shellQuote wraps a word in double quotes, so it stays one word whatever it
// holds, with the one expansion the page relies on left alone: a `$VARIABLE`.
func shellQuote(word string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`")

	return `"` + replacer.Replace(word) + `"`
}
