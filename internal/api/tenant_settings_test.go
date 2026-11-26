package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/ratelimit"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/tenantsettings"
)

// settingsAPI is the authenticated API with tenant settings wired the way the
// composition root wires them: the rateLimit section registered and one cache
// over the auth store. With defaults, a real rate limiter reads its overrides
// through that same cache.
func settingsAPI(t *testing.T, defaults map[string]int) (*authenticatedAPI, map[string]string, *tenantsettings.Cache) {
	t.Helper()
	registry := tenantsettings.NewRegistry()
	if err := ratelimit.RegisterSettings(registry); err != nil {
		t.Fatalf("RegisterSettings() error = %v", err)
	}
	var cache *tenantsettings.Cache
	server, keys := newAuthenticatedAPIWith(t, func(deps *api.Deps) {
		cache = tenantsettings.NewCache(registry, deps.AuthStore)
		deps.TenantSettings = cache
		if defaults != nil {
			deps.RateLimiter = ratelimit.NewService(ratelimit.NewMemoryStore(), defaults).
				WithOverrides(ratelimit.SettingsOverrides{Cache: cache})
		}
	}, "acme", "globex", repository.OperatorTenantID)
	return server, keys, cache
}

type settingsTenantBody struct {
	ID              string                     `json:"id"`
	Settings        map[string]json.RawMessage `json:"settings"`
	SettingsVersion int64                      `json:"settingsVersion"`
}

func settingsDecode(t *testing.T, raw []byte) settingsTenantBody {
	t.Helper()
	var body settingsTenantBody
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode tenant: %v (body: %s)", err, raw)
	}
	return body
}

func settingsPatch(t *testing.T, server *authenticatedAPI, key, tenantID, patch string, want int) []byte {
	t.Helper()
	recorder := server.call(t, http.MethodPatch, "/api/v1/tenants/"+tenantID, key, json.RawMessage(patch))
	if recorder.Code != want {
		t.Fatalf("PATCH %s %s = %d, want %d (body: %s)", tenantID, patch, recorder.Code, want, recorder.Body)
	}
	return recorder.Body.Bytes()
}

func settingsSection(t *testing.T, body settingsTenantBody) string {
	t.Helper()
	raw, ok := body.Settings["rateLimit"]
	if !ok {
		return ""
	}
	return string(raw)
}

func TestOperatorPatchesTenantSettingsAndGetReadsThemBack(t *testing.T) {
	server, keys, _ := settingsAPI(t, nil)
	operator := keys[repository.OperatorTenantID]

	initial := settingsDecode(t, server.call(t, http.MethodGet, "/api/v1/tenants/acme", operator, nil).Body.Bytes())
	if initial.Settings == nil || len(initial.Settings) != 0 || initial.SettingsVersion != 0 {
		t.Fatalf("a fresh tenant = %+v, want settings {} at version 0", initial)
	}

	first := settingsDecode(t, settingsPatch(t, server, operator, "acme", `{"settings":{"rateLimit":{"runPerMinute":5}}}`, http.StatusOK))
	if settingsSection(t, first) != `{"runPerMinute":5}` || first.SettingsVersion != 1 {
		t.Fatalf("after the first patch = %+v", first)
	}
	second := settingsDecode(t, settingsPatch(t, server, operator, "acme", `{"settings":{"rateLimit":{"webhookPerMinute":7}}}`, http.StatusOK))
	if settingsSection(t, second) != `{"runPerMinute":5,"webhookPerMinute":7}` || second.SettingsVersion != 2 {
		t.Fatalf("after the merge = %+v", second)
	}
	// Resending the same patch changes nothing, so the version stays.
	same := settingsDecode(t, settingsPatch(t, server, operator, "acme", `{"settings":{"rateLimit":{"webhookPerMinute":7}}}`, http.StatusOK))
	if same.SettingsVersion != 2 {
		t.Errorf("a no-op patch moved the version to %d", same.SettingsVersion)
	}
	third := settingsDecode(t, settingsPatch(t, server, operator, "acme", `{"settings":{"rateLimit":{"runPerMinute":null}}}`, http.StatusOK))
	if settingsSection(t, third) != `{"webhookPerMinute":7}` || third.SettingsVersion != 3 {
		t.Fatalf("after nulling a field = %+v", third)
	}

	read := settingsDecode(t, server.call(t, http.MethodGet, "/api/v1/tenants/acme", operator, nil).Body.Bytes())
	if settingsSection(t, read) != `{"webhookPerMinute":7}` || read.SettingsVersion != 3 {
		t.Errorf("GET = %+v, want what the last patch answered", read)
	}
	var list struct {
		Items []settingsTenantBody `json:"items"`
	}
	if err := json.Unmarshal(server.call(t, http.MethodGet, "/api/v1/tenants", operator, nil).Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	for _, item := range list.Items {
		switch item.ID {
		case "acme":
			if item.SettingsVersion != 3 || settingsSection(t, item) != `{"webhookPerMinute":7}` {
				t.Errorf("listed acme = %+v", item)
			}
		case "globex":
			if item.SettingsVersion != 0 || item.Settings == nil || len(item.Settings) != 0 {
				t.Errorf("listed globex = %+v, want {} at version 0", item)
			}
		}
	}

	cleared := settingsDecode(t, settingsPatch(t, server, operator, "acme", `{"settings":{"rateLimit":null}}`, http.StatusOK))
	if len(cleared.Settings) != 0 || cleared.Settings == nil || cleared.SettingsVersion != 4 {
		t.Errorf("after nulling the section = %+v, want {} at version 4", cleared)
	}
}

func TestTenantSettingsRefuseUnknownKeysWithANamedCode(t *testing.T) {
	server, keys, _ := settingsAPI(t, nil)
	operator := keys[repository.OperatorTenantID]

	cases := []struct {
		patch    string
		location string
		code     string
	}{
		{`{"settings":{"limits":{"runPerMinute":5}}}`, "body.settings.limits", tenantsettings.CodeUnknownKey},
		{`{"settings":{"rateLimit":{"runsPerMinute":5}}}`, "body.settings.rateLimit.runsPerMinute", tenantsettings.CodeUnknownKey},
		{`{"settings":{"rateLimit":{"runPerMinute":0}}}`, "body.settings.rateLimit", tenantsettings.CodeInvalid},
		{`{"settings":{"rateLimit":{"runPerMinute":"five"}}}`, "body.settings.rateLimit.runPerMinute", tenantsettings.CodeInvalid},
	}
	for _, tc := range cases {
		raw := settingsPatch(t, server, operator, "acme", tc.patch, http.StatusUnprocessableEntity)
		var problem struct {
			Errors []struct {
				Location string `json:"location"`
				Value    struct {
					Code string `json:"code"`
				} `json:"value"`
			} `json:"errors"`
		}
		if err := json.Unmarshal(raw, &problem); err != nil {
			t.Fatalf("decode problem: %v (body: %s)", err, raw)
		}
		if len(problem.Errors) != 1 || problem.Errors[0].Location != tc.location || problem.Errors[0].Value.Code != tc.code {
			t.Errorf("PATCH %s problem = %s, want %s at %s", tc.patch, raw, tc.code, tc.location)
		}
	}
	read := settingsDecode(t, server.call(t, http.MethodGet, "/api/v1/tenants/acme", operator, nil).Body.Bytes())
	if read.SettingsVersion != 0 || len(read.Settings) != 0 {
		t.Errorf("after refused patches = %+v, want nothing written", read)
	}
}

func TestOnlyTheOperatorMayPatchATenant(t *testing.T) {
	server, keys, _ := settingsAPI(t, nil)
	settingsPatch(t, server, keys["acme"], "acme", `{"settings":{"rateLimit":{"runPerMinute":1000000}}}`, http.StatusForbidden)
	settingsPatch(t, server, keys[repository.OperatorTenantID], "nobody", `{"settings":{}}`, http.StatusNotFound)
	read := settingsDecode(t, server.call(t, http.MethodGet, "/api/v1/tenants/acme", keys[repository.OperatorTenantID], nil).Body.Bytes())
	if read.SettingsVersion != 0 {
		t.Errorf("a customer's patch was written: %+v", read)
	}
}

func TestATenantRateLimitSettingTakesEffectOnTheNextRun(t *testing.T) {
	server, keys, cache := settingsAPI(t, map[string]int{ratelimit.BucketRun: 100})
	operator := keys[repository.OperatorTenantID]
	acmeWorkflow := server.createWorkflowAs(t, keys["acme"], "Settings throttled run")
	globexWorkflow := server.createWorkflowAs(t, keys["globex"], "Settings unthrottled run")

	// Warm the cache, so the change below has to be seen through invalidation
	// rather than through a first read.
	server.callJSON(t, http.MethodPost, "/api/v1/workflows/"+acmeWorkflow+"/run", keys["acme"], map[string]any{}, http.StatusAccepted)
	if _, err := cache.Get(t.Context(), "acme"); err != nil {
		t.Fatalf("warm the cache: %v", err)
	}

	settingsPatch(t, server, operator, "acme", `{"settings":{"rateLimit":{"runPerMinute":1}}}`, http.StatusOK)

	// acme's bucket still holds 99 of its old 100 tokens, and the next spend
	// caps it to the new capacity of 1: one run, then a refusal.
	server.callJSON(t, http.MethodPost, "/api/v1/workflows/"+acmeWorkflow+"/run", keys["acme"], map[string]any{}, http.StatusAccepted)
	recorder := server.call(t, http.MethodPost, "/api/v1/workflows/"+acmeWorkflow+"/run", keys["acme"], map[string]any{})
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("acme's second run after runPerMinute 1 = %d, want 429 (body: %s)", recorder.Code, recorder.Body)
	}
	var problem struct {
		Errors []struct {
			Value struct {
				Code string `json:"code"`
			} `json:"value"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil || len(problem.Errors) != 1 || problem.Errors[0].Value.Code != "rate_limited" {
		t.Errorf("the refusal = %s, want the rate_limited code", recorder.Body)
	}
	// globex set nothing and keeps the deployment default.
	for i := 0; i < 2; i++ {
		server.callJSON(t, http.MethodPost, "/api/v1/workflows/"+globexWorkflow+"/run", keys["globex"], map[string]any{}, http.StatusAccepted)
	}
}
