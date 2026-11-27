package node

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kilaslab/kilas-flow/internal/tenantsettings"
)

// SettingsSection is the tenant-settings section this package owns: the built-in
// node types a tenant may or may not use, on top of the deployment's nodes.allow
// and nodes.deny.
const SettingsSection = "nodes"

// settingsReadTimeout bounds one read of a tenant's settings from the store when
// the cache has nothing fresh. The compile and claim paths that ask for it carry
// no context of their own, and a read that hangs would hold a claim.
const settingsReadTimeout = 3 * time.Second

// TenantSettings is the nodes section. Each list is replaced whole by a patch
// that sets it, like every field of every section.
//
// The lists are added to the deployment's: a tenant's allow list is united with
// nodes.allow, its deny list with nodes.deny, and deny wins. A tenant setting
// can therefore narrow what the deployment offers it and can add to a deployment
// allow list, but it cannot lift a nodes.deny entry.
type TenantSettings struct {
	// Allow, when not empty, limits the tenant to these built-ins (and the types
	// the engine names itself), together with any deployment allow entries.
	Allow []string `json:"allow,omitempty"`
	// Deny hides these built-ins from the tenant.
	Deny []string `json:"deny,omitempty"`
}

// RegisterSettings adds the nodes section to a tenant-settings registry. The
// registry checks writes against the node registry it is called on, so a type
// that is not a registered built-in, or an engine-required type on the deny
// list, is refused when the settings are written rather than accepted and
// ignored.
//
// The check is a write check, not a validator: a stored section that failed it
// would be skipped whole on read, so a node type dropped by an upgrade would
// switch off the tenant's other deny entries. Read time is lenient instead: an
// unknown type hides nothing and an engine-required one is never hidden.
func (registry *Registry) RegisterSettings(settings *tenantsettings.Registry) error {
	return tenantsettings.RegisterChecked(settings, SettingsSection, nil, func(section TenantSettings) error {
		var problems []error
		for _, problem := range registry.CheckTenantAvailability(TenantAvailability{Allow: section.Allow, Deny: section.Deny}) {
			problems = append(problems, &tenantsettings.FieldError{
				Field:   fmt.Sprintf("%s[%d]", problem.List, problem.Index),
				Message: problem.Err.Error(),
			})
		}
		return errors.Join(problems...)
	})
}

// SettingsAvailability supplies tenants' own lists from the tenant-settings
// cache, so a registry wired with it honours each tenant's nodes section.
type SettingsAvailability struct {
	Cache *tenantsettings.Cache
}

// TenantAvailability implements TenantAvailabilitySource. A tenant that set no
// nodes section has no lists of its own.
func (source SettingsAvailability) TenantAvailability(tenantID string) (TenantAvailability, error) {
	ctx, cancel := context.WithTimeout(context.Background(), settingsReadTimeout)
	defer cancel()
	settings, ok, err := tenantsettings.Read[TenantSettings](ctx, source.Cache, tenantID, SettingsSection)
	if err != nil {
		return TenantAvailability{}, err
	}
	if !ok {
		return TenantAvailability{}, nil
	}
	return TenantAvailability{Allow: settings.Allow, Deny: settings.Deny}, nil
}
