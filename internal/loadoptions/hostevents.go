package loadoptions

import (
	"context"
	"fmt"
	"sort"

	"github.com/kilaslab/kilas-flow/internal/node"
)

// HostEventCatalogue lists the node definitions one tenant may see.
//
// It is the slice of *node.Registry the loader needs, taken as an interface
// like every other source here: the loader has no business registering or
// scoping anything, and a seam that could would eventually be used to.
type HostEventCatalogue interface {
	ListFor(tenantID string) []node.Definition
}

// HostEvents is the internal loader behind a host-event trigger's picker: the
// host event types the node packs this server carries declare, offered per
// tenant.
//
// The catalogue a pack declares is the editor-facing half of a host-event
// pack — the types with their titles — and this is where it reaches a picker:
// one list, aggregated across every pack the tenant may see, so a workflow
// author chooses from the events this server actually knows about rather than
// typing a type and hoping it matches what the host publishes.
//
// It constructs no outbound request, so the tenancy check has to be here for
// the same reason it is on the workflow list: nothing else stands between a
// browser and every pack's catalogue.
func HostEvents(catalogue HostEventCatalogue) InternalLoader {
	return func(_ context.Context, scope Scope) (Result, error) {
		if catalogue == nil {
			return Result{}, fmt.Errorf("the node catalogue is not available on this server")
		}
		if scope.TenantID == "" {
			return Result{}, fmt.Errorf("a host event list needs a tenant")
		}
		seen := make(map[string]bool)
		options := make([]Option, 0)
		for _, definition := range catalogue.ListFor(scope.TenantID) {
			for _, event := range definition.EventCatalogue {
				if event.Type == "" || seen[event.Type] {
					continue
				}
				seen[event.Type] = true
				label := event.Title
				if label == "" {
					label = event.Type
				}
				options = append(options, Option{Label: label, Value: event.Type})
			}
		}
		if len(options) == 0 {
			return Result{Options: options, Reason: "No node pack on this server declares a host event catalogue yet, so there are no event types to offer. Types can still be published and subscribed to; they are just not listed here."}, nil
		}
		sort.Slice(options, func(left, right int) bool { return options[left].Value < options[right].Value })
		return Result{Options: options}, nil
	}
}
