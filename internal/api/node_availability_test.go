package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/tenantsettings"
	"github.com/kilaslab/kilas-flow/nodes"
)

// Built-in availability, end to end through the HTTP API: a tenant's own lists
// arrive by PATCH /tenants/{id} (the "nodes" settings section), the deployment's
// by the registry rules, and both reach the catalogue and the activation gate.

// hideableType is the built-in these tests hide. A no-op needs no parameters, so
// a workflow holding it is valid for every tenant that may use it.
const hideableType = nodes.NoOpNodeType

// setNodeType is the built-in the allow-list test keeps.
const setNodeType = "kilasflow.set"

// availabilityAPI is the authenticated API wired the way the composition root
// wires built-in availability: the deployment rules applied, the "nodes" section
// registered and the registry reading tenants' lists through the settings cache.
func availabilityAPI(t *testing.T, rules node.AvailabilityRules) (*authenticatedAPI, map[string]string) {
	t.Helper()
	settings := tenantsettings.NewRegistry()
	return newAuthenticatedAPIWith(t, func(deps *api.Deps) {
		if err := deps.NodeRegistry.ApplyAvailability(rules); err != nil {
			t.Fatalf("ApplyAvailability() error = %v", err)
		}
		if err := deps.NodeRegistry.RegisterSettings(settings); err != nil {
			t.Fatalf("RegisterSettings() error = %v", err)
		}
		cache := tenantsettings.NewCache(settings, deps.AuthStore)
		deps.NodeRegistry.SetTenantAvailabilitySource(node.SettingsAvailability{Cache: cache})
		deps.TenantSettings = cache
	}, "acme", "globex", repository.OperatorTenantID)
}

func activatesWith(t *testing.T, server *authenticatedAPI, key string) (int, []string) {
	t.Helper()
	created := server.callJSON(t, http.MethodPost, "/api/v1/workflows", key,
		workflowDraft(crmWorkflow("Uses a built-in", hideableType)), http.StatusCreated)
	id, _ := created["id"].(string)
	recorder := server.call(t, http.MethodPost, "/api/v1/workflows/"+id+"/activate", key, nil)
	var problem activationProblem
	if recorder.Code != http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
			t.Fatalf("decode activation problem: %v (body: %s)", err, recorder.Body)
		}
	}
	return recorder.Code, codesOf(problem)
}

// A tenant's PATCH takes effect at once on the process that served it: the
// catalogue drops the type for that tenant only, and activation answers with the
// not-available code, not the unknown-type one.
func TestATenantSettingHidesABuiltinFromItsCatalogueAndItsCompiler(t *testing.T) {
	server, keys := availabilityAPI(t, node.AvailabilityRules{})
	operator := keys[repository.OperatorTenantID]

	before, _ := catalogueTypes(t, server, keys["acme"])
	if !containsType(before, hideableType) {
		t.Fatalf("acme's catalogue does not list %q before any rule: %v", hideableType, before)
	}
	if code, _ := activatesWith(t, server, keys["acme"]); code != http.StatusOK {
		t.Fatalf("acme activation before any rule = %d, want 200", code)
	}

	body := settingsDecode(t, settingsPatch(t, server, operator, "acme",
		`{"settings":{"nodes":{"deny":["`+hideableType+`"]}}}`, http.StatusOK))
	if got := string(body.Settings["nodes"]); got != `{"deny":["`+hideableType+`"]}` {
		t.Fatalf("stored nodes section = %s", got)
	}

	acme, _ := catalogueTypes(t, server, keys["acme"])
	globex, _ := catalogueTypes(t, server, keys["globex"])
	if containsType(acme, hideableType) {
		t.Errorf("acme's catalogue still lists %q after the deny: %v", hideableType, acme)
	}
	if !containsType(globex, hideableType) {
		t.Errorf("globex's catalogue lost %q: %v", hideableType, globex)
	}
	if code, codes := activatesWith(t, server, keys["acme"]); code != http.StatusUnprocessableEntity || !containsString(codes, "node.not_available") {
		t.Errorf("acme activation = %d %v, want 422 node.not_available", code, codes)
	}
	if code, _ := activatesWith(t, server, keys["globex"]); code != http.StatusOK {
		t.Errorf("globex activation = %d, want 200", code)
	}

	// Removing the section restores the type.
	settingsPatch(t, server, operator, "acme", `{"settings":{"nodes":null}}`, http.StatusOK)
	restored, _ := catalogueTypes(t, server, keys["acme"])
	if !containsType(restored, hideableType) {
		t.Errorf("acme's catalogue does not list %q after the section was removed", hideableType)
	}
}

// The deployment's rules and a tenant's own add up: the tenant cannot lift what
// the deployment denies.
func TestATenantSettingCannotLiftADeploymentDeny(t *testing.T) {
	server, keys := availabilityAPI(t, node.AvailabilityRules{
		Deny: []node.AvailabilityRule{{Type: hideableType}},
	})
	operator := keys[repository.OperatorTenantID]

	settingsPatch(t, server, operator, "acme", `{"settings":{"nodes":{"allow":["`+hideableType+`"]}}}`, http.StatusOK)
	types, _ := catalogueTypes(t, server, keys["acme"])
	if containsType(types, hideableType) {
		t.Errorf("a tenant allow list lifted a deployment deny: %v", types)
	}
}

// A tenant's allow list limits it to the listed built-ins plus the types the
// engine names itself.
func TestATenantAllowListLimitsItsCatalogue(t *testing.T) {
	server, keys := availabilityAPI(t, node.AvailabilityRules{})
	operator := keys[repository.OperatorTenantID]

	settingsPatch(t, server, operator, "acme", `{"settings":{"nodes":{"allow":["`+setNodeType+`"]}}}`, http.StatusOK)
	acme, _ := catalogueTypes(t, server, keys["acme"])
	globex, _ := catalogueTypes(t, server, keys["globex"])
	if !containsType(acme, setNodeType) {
		t.Errorf("acme's catalogue lost the allowed type: %v", acme)
	}
	if containsType(acme, hideableType) {
		t.Errorf("acme's catalogue lists a built-in outside its allow list: %v", acme)
	}
	for _, required := range node.EngineRequiredTypes() {
		if !containsType(acme, required) {
			t.Errorf("acme's catalogue lost the engine-required %q: %v", required, acme)
		}
	}
	if !containsType(globex, hideableType) {
		t.Errorf("globex's catalogue is limited by acme's allow list: %v", globex)
	}
}

// The tenant API refuses what the boot refuses: an engine-required type on the
// deny list is a 422, located at the entry, and nothing is written. So is a type
// that is not a registered built-in.
func TestTheTenantAPIRefusesAnUndeniableOrUnknownType(t *testing.T) {
	server, keys := availabilityAPI(t, node.AvailabilityRules{})
	operator := keys[repository.OperatorTenantID]

	for _, test := range []struct {
		name, patch, location, want string
	}{
		{"error trigger", `{"settings":{"nodes":{"deny":["` + nodes.ErrorTriggerNodeType + `"]}}}`, "body.settings.nodes.deny[0]", "cannot be denied"},
		{"sub-workflow trigger", `{"settings":{"nodes":{"deny":["` + hideableType + `","` + nodes.ExecuteWorkflowTriggerType + `"]}}}`, "body.settings.nodes.deny[1]", "cannot be denied"},
		{"unregistered", `{"settings":{"nodes":{"allow":["kilasflow.nope"]}}}`, "body.settings.nodes.allow[0]", "not registered"},
		{"a pack's type", `{"settings":{"nodes":{"deny":["pack.telegram"]}}}`, "body.settings.nodes.deny[0]", ""},
		{"unknown field", `{"settings":{"nodes":{"block":["` + hideableType + `"]}}}`, "body.settings.nodes.block", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := settingsPatch(t, server, operator, "acme", test.patch, http.StatusUnprocessableEntity)
			var problem struct {
				Errors []struct {
					Message  string `json:"message"`
					Location string `json:"location"`
					Value    struct {
						Code string `json:"code"`
					} `json:"value"`
				} `json:"errors"`
			}
			if err := json.Unmarshal(raw, &problem); err != nil {
				t.Fatalf("decode problem: %v (body: %s)", err, raw)
			}
			if len(problem.Errors) != 1 || problem.Errors[0].Location != test.location {
				t.Fatalf("problem = %s, want one error at %s", raw, test.location)
			}
			if !strings.Contains(problem.Errors[0].Message, test.want) {
				t.Errorf("message = %q, want it to contain %q", problem.Errors[0].Message, test.want)
			}
		})
	}
	read := settingsDecode(t, server.call(t, http.MethodGet, "/api/v1/tenants/acme", operator, nil).Body.Bytes())
	if read.SettingsVersion != 0 || len(read.Settings) != 0 {
		t.Errorf("after refused patches = %+v, want nothing written", read)
	}
}

// A denied built-in's icon answers like an unknown type to the tenant it is
// hidden from.
func TestAHiddenBuiltinsIconAnswersLikeAnUnknownType(t *testing.T) {
	server, keys := availabilityAPI(t, node.AvailabilityRules{
		Deny: []node.AvailabilityRule{{Type: nodes.GmailNodeType, Tenant: "acme"}},
	})
	hidden := server.call(t, http.MethodGet, "/api/v1/node-types/"+nodes.GmailNodeType+"/icon", keys["acme"], nil)
	unknown := server.call(t, http.MethodGet, "/api/v1/node-types/kilasflow.nope/icon", keys["acme"], nil)
	if hidden.Code != http.StatusNotFound || unknown.Code != http.StatusNotFound {
		t.Fatalf("icon statuses = %d (hidden) %d (unknown), want 404 for both", hidden.Code, unknown.Code)
	}
	if hidden.Body.String() != unknown.Body.String() {
		t.Errorf("a hidden type's 404 differs from an unknown type's:\n%s\n%s", hidden.Body, unknown.Body)
	}
}
