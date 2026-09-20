package embed_test

import (
	"reflect"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/embed"
)

// deploymentBranding is the white-label pair a deployment may configure once
// for every embedded editor it hosts.
func deploymentBranding() embed.Branding {
	return embed.Branding{Name: "Acme Flows", LogoURL: "https://cdn.example/logo.png"}
}

func TestDeploymentBrandingFillsWhatASessionLeavesBlank(t *testing.T) {
	t.Parallel()

	issuer := lifetimeIssuer(t, embed.WithDefaultBranding(deploymentBranding()))
	session, _, err := issuer.Issue(validRequest())
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if session.Branding != deploymentBranding() {
		t.Errorf("session branding = %#v, want the deployment defaults %#v", session.Branding, deploymentBranding())
	}
}

func TestASessionsOwnBrandingWinsFieldByField(t *testing.T) {
	t.Parallel()

	issuer := lifetimeIssuer(t, embed.WithDefaultBranding(deploymentBranding()))

	named := validRequest()
	named.Branding = embed.Branding{Name: "Birch"}
	session, _, err := issuer.Issue(named)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if session.Branding.Name != "Birch" || session.Branding.LogoURL != deploymentBranding().LogoURL {
		t.Errorf("branding = %#v, want the session's name and the deployment logo", session.Branding)
	}

	logoed := validRequest()
	logoed.Branding = embed.Branding{LogoURL: "https://birch.example/logo.svg"}
	session, _, err = issuer.Issue(logoed)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if session.Branding.LogoURL != "https://birch.example/logo.svg" || session.Branding.Name != deploymentBranding().Name {
		t.Errorf("branding = %#v, want the session's logo and the deployment name", session.Branding)
	}
}

func TestDeploymentBrandingIsValidatedLikeAHostsIs(t *testing.T) {
	t.Parallel()

	for name, defaults := range map[string]embed.Branding{
		"script in name":  {Name: `<script>alert(1)</script>`},
		"http logo":       {LogoURL: "http://cdn.example/logo.png"},
		"javascript logo": {LogoURL: "javascript:alert(1)"},
		"data logo":       {LogoURL: "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4="},
		"relative logo":   {LogoURL: "/logo.png"},
		"bad accent":      {Accent: "red; background: url(javascript:alert(1))"},
	} {
		if _, err := embed.NewIssuer(testKey(), []string{"https://host.example"}, nil,
			embed.WithDefaultBranding(defaults)); err == nil {
			t.Errorf("%s was accepted as a deployment default: %#v", name, defaults)
		}
	}

	if _, err := embed.NewIssuer(testKey(), []string{"https://host.example"}, nil,
		embed.WithDefaultBranding(deploymentBranding())); err != nil {
		t.Errorf("valid deployment branding was rejected: %v", err)
	}
}

func TestDeploymentBrandingSurvivesTheTokenRoundTrip(t *testing.T) {
	t.Parallel()

	issuer := lifetimeIssuer(t, embed.WithDefaultBranding(deploymentBranding()))
	session, token, err := issuer.Issue(validRequest())
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	verified, err := issuer.Verify(token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if verified.Branding != session.Branding {
		t.Errorf("verified branding = %#v, want the merged %#v in the token itself", verified.Branding, session.Branding)
	}
}

func TestDeploymentBrandingIsNotAddedToADatastoreSession(t *testing.T) {
	t.Parallel()

	issuer := lifetimeIssuer(t, embed.WithDefaultBranding(deploymentBranding()))
	session, _, err := issuer.Issue(embed.Request{
		TenantID: "tenant-a", DatastoreID: "datastore_1",
		Scopes: []embed.Scope{embed.ScopeDatastoreRead},
		Origin: "https://host.example",
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	// The defaults exist for the embedded editor's header, and a datastore
	// session has no editor: overlaying them would put a sheet's values on a
	// token that renders nothing.
	if session.Branding != (embed.Branding{}) {
		t.Errorf("datastore session branding = %#v, want the zero value", session.Branding)
	}
}

// TestBrandingWithDefaultsHandlesEveryField is the guard against the defect
// this ticket exists to remove, in miniature.
//
// A merge that names its fields one by one silently ignores any field added to
// Branding later — the same shape as a configuration key with no reader. It
// reflects over the struct instead, so adding a field without extending the
// merge fails here rather than shipping a value nothing reads.
func TestBrandingWithDefaultsHandlesEveryField(t *testing.T) {
	t.Parallel()

	kind := reflect.TypeOf(embed.Branding{})
	if kind.NumField() == 0 {
		t.Fatal("embed.Branding has no fields: this guard would pass vacuously")
	}

	defaults := embed.Branding{}
	writeEveryField(t, reflect.ValueOf(&defaults).Elem(), "x")
	if got := (embed.Branding{}).WithDefaults(defaults); got != defaults {
		t.Errorf("Branding{}.WithDefaults(defaults) = %#v, want %#v: every field the merge skips is a value that does nothing",
			got, defaults)
	}

	session := embed.Branding{}
	writeEveryField(t, reflect.ValueOf(&session).Elem(), "y")
	if got := session.WithDefaults(defaults); got != session {
		t.Errorf("a session's own branding.WithDefaults(defaults) = %#v, want %#v: the session wins where it has a value",
			got, session)
	}
}

// writeEveryField sets every string field to text and every bool to true,
// failing the test on a kind the merge was never taught about.
func writeEveryField(t *testing.T, value reflect.Value, text string) {
	t.Helper()

	kind := value.Type()
	for index := 0; index < kind.NumField(); index++ {
		field := value.Field(index)
		switch field.Kind() {
		case reflect.String:
			field.SetString(text)
		case reflect.Bool:
			field.SetBool(true)
		default:
			t.Fatalf("embed.Branding.%s is a %s: extend Branding.WithDefaults for it, and this guard",
				kind.Field(index).Name, field.Kind())
		}
	}
}
