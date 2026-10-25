package nodepack_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/loadoptions"
	"github.com/kilaslab/kilas-flow/internal/nodepack"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
)

// loadingManifest is a host-API-shaped pack: one resource, one operation, and
// the parameters given — pickers a free-text ID field cannot stand in for.
func loadingManifest(parameters string, credentialType string) []byte {
	credentials := ""
	if credentialType != "" {
		credentials = `"credentialType": "` + credentialType + `",`
	}
	return []byte(`{
		"type": "pack.hostcrm", "version": 1,
		"displayName": "Host CRM", "category": "Communication",
		` + credentials + `
		"requestDefaults": {"baseUrl": "https://crm.host.example"},
		"resources": [
			{
				"name": "message",
				"description": "Messages",
				"operations": [
					{
						"name": "send",
						"description": "Send a message",
						"method": "POST",
						"url": "/api/messages",
						"sends": [{"from": "channel", "type": "body", "property": "channel"}]
					}
				]
			}
		],
		"parameters": [` + parameters + `]
	}`)
}

const channelPicker = `{
	"key": "channel", "label": "Channel", "kind": "options", "required": true,
	"loadOptions": {
		"source": "http", "method": "GET", "endpoint": "/api/channels",
		"credentialType": "exampleApi", "itemsPath": "data",
		"labelTemplate": "{{ name }} ({{ provider }})", "valueField": "id",
		"dependsOn": []
	}
}`

// TestPackParametersCanLoadOptionsFromTheHostsService is the capability: a
// pack parameter may fill its picker over HTTP at edit time, on the kinds a
// picker is — options, multiOptions, and a resourceLocator's list mode — with
// the loader carried into the definition the editor and the load-options
// endpoint both read.
func TestPackParametersCanLoadOptionsFromTheHostsService(t *testing.T) {
	t.Parallel()

	manifest := loadingManifest(strings.Join([]string{
		channelPicker,
		strings.Replace(strings.Replace(channelPicker, `"key": "channel"`, `"key": "tags"`, 1), `"kind": "options"`, `"kind": "multiOptions"`, 1),
		`{
			"key": "agent", "label": "Agent", "kind": "resourceLocator", "required": true,
			"modes": [
				{"name": "id", "label": "By ID", "kind": "string", "pattern": "^a_[0-9]+$", "patternHint": "an agent id, a_<digits>"},
				{"name": "list", "label": "From list", "kind": "options", "placeholder": "Search agents…",
				 "loadOptions": {"source": "http", "method": "GET", "endpoint": "/api/agents", "credentialType": "exampleApi", "itemsPath": "data", "valueField": "id", "labelTemplate": "{{ name }}"}}
			]
		}`,
	}, ",\n"), "exampleApi")

	if issues := nodepack.ValidateBytes(manifest, "pack.json"); len(issues) != 0 {
		t.Fatalf("a pack with loadOptions was refused: %v", issues)
	}
	definition, _, err := nodepack.Load(decodeLoadedPack(t, manifest))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	byKey := map[string]property.PropertyDefinition{}
	for _, declared := range definition.Parameters {
		byKey[declared.Key] = declared
	}

	channel := byKey["channel"]
	if channel.LoadOptions == nil || channel.LoadOptions.Source != property.LoaderHTTP || channel.LoadOptions.Endpoint != "/api/channels" {
		t.Fatalf("channel loader = %+v", channel.LoadOptions)
	}
	if channel.LoadOptions.CredentialType != "exampleApi" {
		t.Errorf("channel loader credential = %q", channel.LoadOptions.CredentialType)
	}
	if tags := byKey["tags"]; tags.LoadOptions == nil {
		t.Error("a multiOptions parameter's loader did not survive the load")
	}

	agent := byKey["agent"]
	if agent.Kind != property.KindResourceLocator || len(agent.Modes) != 2 {
		t.Fatalf("agent locator modes = %d", len(agent.Modes))
	}
	list := agent.Modes[1]
	if list.Kind != property.KindOptions || list.LoadOptions == nil || list.LoadOptions.Endpoint != "/api/agents" {
		t.Fatalf("agent list mode = %+v", list)
	}
	if typed := agent.Modes[0]; typed.LoadOptions != nil {
		t.Error("a typed mode carried a loader it has no way to use")
	}
}

// TestPackLoadOptionsRefusals pins what an author hears from
// `nodepackgen validate`, so a loader this server cannot honour is an
// authoring error rather than a picker that loads nothing.
func TestPackLoadOptionsRefusals(t *testing.T) {
	t.Parallel()

	loader := func(fields ...string) string {
		return "{" + strings.Join(fields, ",") + "}"
	}
	for name, testCase := range map[string]struct {
		parameters     []string
		credentialType string
		want           string
	}{
		"a string field cannot load": {
			parameters: []string{`{"key": "path", "label": "Path", "kind": "string", "loadOptions": ` + loader(`"source": "http"`, `"endpoint": "/x"`, `"valueField": "id"`) + `}`},
			want:       "only options, multiOptions",
		},
		"a locator loads per mode, not on the property": {
			parameters: []string{`{"key": "agent", "label": "Agent", "kind": "resourceLocator", "modes": [{"name": "id", "label": "By ID", "kind": "string"}], "loadOptions": ` + loader(`"source": "http"`, `"endpoint": "/x"`, `"valueField": "id"`) + `}`},
			want:       "declare loadOptions on the mode",
		},
		"a locator with no modes": {
			parameters: []string{`{"key": "agent", "label": "Agent", "kind": "resourceLocator"}`},
			want:       "needs at least one mode",
		},
		"a list mode with no loader": {
			parameters: []string{`{"key": "agent", "label": "Agent", "kind": "resourceLocator", "modes": [{"name": "list", "label": "From list", "kind": "options"}]}`},
			want:       "declares no loader",
		},
		"a mode named expression": {
			parameters: []string{`{"key": "agent", "label": "Agent", "kind": "resourceLocator", "modes": [{"name": "expression", "label": "Expression", "kind": "string"}]}`},
			want:       "may not be named",
		},
		"no endpoint": {
			parameters: []string{`{"key": "channel", "label": "Channel", "kind": "options", "loadOptions": ` + loader(`"source": "http"`, `"valueField": "id"`) + `}`},
			want:       "needs an endpoint",
		},
		"no value field": {
			parameters: []string{`{"key": "channel", "label": "Channel", "kind": "options", "loadOptions": ` + loader(`"source": "http"`, `"endpoint": "/x"`) + `}`},
			want:       "needs a value field",
		},
		"an internal loader": {
			parameters: []string{`{"key": "channel", "label": "Channel", "kind": "options", "loadOptions": ` + loader(`"source": "internal"`, `"name": "sql.tables"`) + `}`},
			want:       "internal loaders are the server's own",
		},
		"another tenant-facing type's credential": {
			parameters:     []string{`{"key": "channel", "label": "Channel", "kind": "options", "loadOptions": ` + loader(`"source": "http"`, `"endpoint": "/x"`, `"valueField": "id"`, `"credentialType": "otherApi"`) + `}`},
			credentialType: "hostApi",
			want:           "does not require",
		},
		"a loader with no pack credential": {
			parameters: []string{`{"key": "channel", "label": "Channel", "kind": "options", "loadOptions": ` + loader(`"source": "http"`, `"endpoint": "/x"`, `"valueField": "id"`, `"credentialType": "exampleApi"`) + `}`},
			want:       "does not require",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			manifest := loadingManifest(strings.Join(testCase.parameters, ","), testCase.credentialType)
			issues := nodepack.ValidateBytes(manifest, "pack.json")
			found := false
			for _, issue := range issues {
				found = found || strings.Contains(issue.Message, testCase.want)
			}
			if !found {
				t.Fatalf("issues = %v, want one mentioning %q", issues, testCase.want)
			}
		})
	}
}

// TestAPackLoaderResolvesWithTheNodesCredential closes the loop: the loader a
// pack declared, as Load() shaped it, resolves through the same resolver the
// load-options endpoint uses — over the egress policy, signed with the
// credential the node attached, and with each option labelled from the
// service's own fields.
func TestAPackLoaderResolvesWithTheNodesCredential(t *testing.T) {
	t.Parallel()

	var seenKey, seenPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenKey = r.URL.Query().Get("key")
		seenPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []any{
				map[string]any{"id": "ch_1", "name": "Support", "provider": "whatsapp"},
				map[string]any{"id": "ch_2", "name": "Sales", "provider": "instagram"},
			},
		})
	}))
	defer server.Close()

	manifest := loadingManifest(channelPicker, "exampleApi")
	definition, _, err := nodepack.Load(decodeLoadedPack(t, manifest))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	var loader *property.OptionsLoader
	for _, declared := range definition.Parameters {
		if declared.Key == "channel" {
			loader = declared.LoadOptions
		}
	}
	if loader == nil {
		t.Fatal("the channel parameter carried no loader")
	}

	// The endpoint as a pack writes it is relative; the resolver composes it
	// with the base URL parameter, which is the customer's own configured
	// address for the service.
	loader.Endpoint = server.URL + "/api/channels"
	resolver := loadoptions.NewResolver(safehttp.Policy{
		AllowedHosts:     []string{strings.SplitN(strings.TrimPrefix(server.URL, "http://"), ":", 2)[0]},
		MaxResponseBytes: 1 << 20, Timeout: 5 * time.Second, AllowPrivateNetworks: true,
	}, time.Minute)
	// A test's own loopback stub, allowed by name — the same per-endpoint
	// shape the e2e suites use; every other address stays refused by the
	// policy this resolver was built with.
	result, err := resolver.Load(context.Background(), *loader, loadoptions.Scope{}, "credential_1", fixedCredentialResolver{})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if seenPath != "/api/channels" {
		t.Errorf("requested %q", seenPath)
	}
	if seenKey != "crm-secret" {
		t.Errorf("key = %q, want the node's credential applied", seenKey)
	}
	if len(result.Options) != 2 || result.Options[0].Value != "ch_1" {
		t.Fatalf("options = %+v", result.Options)
	}
	if label := result.Options[0].Label; !strings.Contains(label, "Support") || !strings.Contains(label, "whatsapp") {
		t.Errorf("label = %q, want it rendered from the service's fields", label)
	}
}

type fixedCredentialResolver struct{}

func (fixedCredentialResolver) Resolve(_ context.Context, _ string) (credentials.Record, map[string]string, error) {
	return credentials.Record{Type: "exampleApi", Name: "Host CRM"},
		map[string]string{"apiKey": "crm-secret"}, nil
}

func decodeLoadedPack(t *testing.T, data []byte) *nodepack.Pack {
	t.Helper()
	pack, err := nodepack.Decode(data)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	return pack
}
