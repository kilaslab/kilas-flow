// Per-tenant node visibility: the operator's override of what a pack's manifest
// declared.
//
// A grant is written `"<node type>=<tenant id>"`, one per entry, and every entry
// for a type together replace that type's manifest set. A list of strings rather
// than a YAML map on purpose: koanf's "." key delimiter would split a map key
// like `pack.telegram` into two levels, and a list is what the environment
// splitter already understands, so `KILASFLOW_PACKS_VISIBLE_TO` works with no
// new machinery.
//
// The grammar is checked here — a missing separator, an empty type, an empty
// tenant. What a tenant ID may be is node's rule, applied when the grants reach
// the registry, so this file and the pack manifest loader cannot disagree about
// it.

package config

import (
	"fmt"
	"strings"
)

// visibilityGrant is one parsed `type=tenant` entry.
type visibilityGrant struct {
	nodeType string
	tenantID string
}

// parseVisibilityGrants splits and trims the entries, refusing anything that is
// not a `type=tenant` pair.
//
// Splitting on the FIRST '=' rather than on every one: a tenant ID cannot
// contain '=' (node.CheckTenantID refuses it), so a second one is a mistake, and
// treating it as part of the tenant would hide a typo inside a scope that then
// matches no tenant at all.
func parseVisibilityGrants(entries []string) ([]visibilityGrant, error) {
	grants := make([]visibilityGrant, 0, len(entries))
	for index, entry := range entries {
		nodeType, tenantID, found := strings.Cut(entry, "=")
		nodeType, tenantID = strings.TrimSpace(nodeType), strings.TrimSpace(tenantID)
		switch {
		case !found:
			return nil, fmt.Errorf("packs.visible_to[%d] %q: want \"<node type>=<tenant id>\"", index, entry)
		case nodeType == "":
			return nil, fmt.Errorf("packs.visible_to[%d] %q: the node type is empty", index, entry)
		case tenantID == "":
			return nil, fmt.Errorf("packs.visible_to[%d] %q: the tenant ID is empty", index, entry)
		case strings.Contains(tenantID, "="):
			return nil, fmt.Errorf("packs.visible_to[%d] %q: the tenant ID contains \"=\", which the grammar reserves", index, entry)
		}
		grants = append(grants, visibilityGrant{nodeType: nodeType, tenantID: tenantID})
	}
	return grants, nil
}

// VisibilityGrants returns the override as a node type to tenant list map, in
// the order the entries were written.
//
// This is what the composition root hands the node registry, whose
// ApplyVisibility validates the rest: that each type exists, that it is not a
// built-in, and that each tenant ID could name a tenant.
func (packs Packs) VisibilityGrants() (map[string][]string, error) {
	if len(packs.VisibleTo) == 0 {
		return nil, nil
	}
	grants, err := parseVisibilityGrants(packs.VisibleTo)
	if err != nil {
		return nil, err
	}
	byType := make(map[string][]string, len(grants))
	for _, grant := range grants {
		byType[grant.nodeType] = append(byType[grant.nodeType], grant.tenantID)
	}
	return byType, nil
}

// validateVisibleTo checks the shape of every entry, so a malformed grant refuses
// the boot naming the key and the index it came from rather than reaching the
// registry as a scope that matches nothing.
func (packs Packs) validateVisibleTo() error {
	_, err := parseVisibilityGrants(packs.VisibleTo)
	return err
}
