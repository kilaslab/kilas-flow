// Built-in node availability: the operator's allow and deny lists.
//
// An entry is written `"<node type>"` (every tenant) or
// `"<node type>=<tenant id>"` (one tenant), the same `type=tenant` shape as
// packs.visible_to and for the same reasons: a list of strings rather than a YAML
// map, because koanf's "." key delimiter would split a type like
// `kilasflow.sqlite` into two levels, and a list is what the environment splitter
// already understands, so KILASFLOW_NODES_DENY works with no new machinery.
//
// The grammar is checked here: an empty type, an empty tenant after an "=", and a
// deny entry naming a type the engine requires. What a tenant ID may be, and
// whether a type is a registered built-in, are the node registry's rules, applied
// when the lists reach it, so this file and the registry cannot disagree about
// them.

package config

import (
	"fmt"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/node"
)

// parseAvailabilityRules splits and trims the entries of one list. key is the
// config key, `nodes.allow` or `nodes.deny`, so an error names where the entry
// came from. deny refuses the engine-required types.
//
// Splitting on the FIRST '=' for the reason parseVisibilityGrants does: a tenant
// ID cannot contain '=', so a second one is a mistake, and folding it into the
// tenant would hide a typo inside a scope that matches no tenant at all.
func parseAvailabilityRules(key string, entries []string, deny bool) ([]node.AvailabilityRule, error) {
	rules := make([]node.AvailabilityRule, 0, len(entries))
	for index, entry := range entries {
		nodeType, tenantID, scoped := strings.Cut(entry, "=")
		nodeType, tenantID = strings.TrimSpace(nodeType), strings.TrimSpace(tenantID)
		switch {
		case nodeType == "":
			return nil, fmt.Errorf("%s[%d] %q: the node type is empty", key, index, entry)
		case scoped && tenantID == "":
			return nil, fmt.Errorf("%s[%d] %q: the tenant ID is empty; write the node type alone to apply it to every tenant", key, index, entry)
		case strings.Contains(tenantID, "="):
			return nil, fmt.Errorf("%s[%d] %q: the tenant ID contains \"=\", which the grammar reserves", key, index, entry)
		case deny && node.IsEngineRequired(nodeType):
			return nil, fmt.Errorf("%s[%d] %q: the engine names this node type itself, so it cannot be denied (engine-required types: %s)",
				key, index, entry, strings.Join(node.EngineRequiredTypes(), ", "))
		}
		rules = append(rules, node.AvailabilityRule{Type: nodeType, Tenant: tenantID})
	}
	return rules, nil
}

// AvailabilityRules returns the allow and deny lists parsed, in the order the
// entries were written. This is what the composition root hands the node
// registry's ApplyAvailability, which validates the rest: that each type is a
// registered built-in and each tenant ID could name a tenant.
func (nodes Nodes) AvailabilityRules() (node.AvailabilityRules, error) {
	allow, err := parseAvailabilityRules("nodes.allow", nodes.Allow, false)
	if err != nil {
		return node.AvailabilityRules{}, err
	}
	deny, err := parseAvailabilityRules("nodes.deny", nodes.Deny, true)
	if err != nil {
		return node.AvailabilityRules{}, err
	}
	return node.AvailabilityRules{Allow: allow, Deny: deny}, nil
}

// validateAvailability checks the shape of every entry, so a malformed one
// refuses the boot naming the key and the index it came from rather than
// reaching the registry as a rule that matches nothing.
func (nodes Nodes) validateAvailability() error {
	_, err := nodes.AvailabilityRules()
	return err
}
