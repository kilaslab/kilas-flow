package embed

import (
	"sort"
	"strings"
)

// DatastoreRef names one data table a session may address.
//
// Both halves are needed. An editor may address a table by its catalogue id or
// by its name, and a By-Name locator is resolved against the tenant's live list
// at run time — so the name, not the id, is the only value a confinement check
// can compare when the document names a table that way.
type DatastoreRef struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// Confinement is the set of tenant resources one workflow session's document
// may reference.
//
// It exists because scopes alone cannot express the guarantee the embed model
// sells. A session holding workflow:write may save any document into its own
// workflow, and the Datastore node and a node's credential reference then act
// with the *tenant's* authority rather than the session's: a guest editor could
// point a save at a sibling data table, list every table in the tenant, or
// attach a credential the workflow never referenced, and run it.
//
// The confinement is minted into the token when the session is created, derived
// from the revision the *owner* published — the last document a trusted caller
// authored. An empty confinement is therefore the strict reading, not the loose
// one: a session whose workflow has never been published may reference no
// credential and no data table at all.
type Confinement struct {
	// Credentials are the credential ids the document may attach.
	Credentials []string `json:"crd,omitempty"`
	// Datastores are the data tables the document may address, by id and name.
	Datastores []DatastoreRef `json:"dst,omitempty"`
	// Workflows are the workflow ids an Execute Sub-workflow node may call.
	// The session's own workflow is added when the session is issued, so
	// recursion into itself never needs a second entry.
	Workflows []string `json:"wfw,omitempty"`
}

// AllowsCredential reports whether the confinement names a credential id.
func (confinement Confinement) AllowsCredential(id string) bool {
	return containsExact(confinement.Credentials, id)
}

// AllowsDatastoreID reports whether the confinement names a data table id.
//
// A blank id never matches, not even a blank entry: a node whose table is not
// chosen yet addresses nothing, and reading it as allowed would let the one
// locator the check cannot interpret through.
func (confinement Confinement) AllowsDatastoreID(id string) bool {
	if strings.TrimSpace(id) == "" {
		return false
	}
	for _, datastore := range confinement.Datastores {
		if datastore.ID == id {
			return true
		}
	}
	return false
}

// AllowsWorkflow reports whether the confinement names a workflow id.
func (confinement Confinement) AllowsWorkflow(id string) bool {
	return containsExact(confinement.Workflows, id)
}

// Empty reports whether the confinement names nothing at all.
func (confinement Confinement) Empty() bool {
	return len(confinement.Credentials) == 0 && len(confinement.Datastores) == 0 && len(confinement.Workflows) == 0
}

// normalized returns the confinement with duplicates, blanks, and stray
// whitespace removed and a deterministic order, so two tokens minted from the
// same workflow are byte-identical and a test can compare them directly.
func (confinement Confinement) normalized(selfWorkflowID string) Confinement {
	normalized := Confinement{
		Credentials: trimSorted(confinement.Credentials),
		Workflows:   trimSorted(confinement.Workflows),
	}
	seen := map[string]bool{}
	for _, datastore := range confinement.Datastores {
		id := strings.TrimSpace(datastore.ID)
		name := strings.TrimSpace(datastore.Name)
		if id == "" && name == "" {
			continue
		}
		key := id + "\x00" + strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		normalized.Datastores = append(normalized.Datastores, DatastoreRef{ID: id, Name: name})
	}
	sort.Slice(normalized.Datastores, func(left, right int) bool {
		if normalized.Datastores[left].ID != normalized.Datastores[right].ID {
			return normalized.Datastores[left].ID < normalized.Datastores[right].ID
		}
		return normalized.Datastores[left].Name < normalized.Datastores[right].Name
	})

	// A workflow may always call itself: recursion is the one sub-workflow
	// reference a session cannot be confined out of, because the workflow it
	// names is the one the session already owns.
	if self := strings.TrimSpace(selfWorkflowID); self != "" {
		normalized.Workflows = trimSorted(append(normalized.Workflows, self))
	}
	return normalized
}

func trimSorted(values []string) []string {
	seen := map[string]bool{}
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		cleaned = append(cleaned, value)
	}
	if len(cleaned) == 0 {
		return nil
	}
	sort.Strings(cleaned)
	return cleaned
}

func containsExact(values []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
