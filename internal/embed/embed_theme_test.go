package embed_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/embed"
)

func radius(value int) *int { return &value }

// Theme, font and radius are a closed vocabulary: a host picks from a list
// and a bounded number, never a free-form value the frame would have to trust.
func TestBrandingThemeFontAndRadiusAreValidated(t *testing.T) {
	for name, branding := range map[string]embed.Branding{
		"light":          {Theme: "light"},
		"dark":           {Theme: "dark"},
		"system":         {Theme: "system"},
		"system font":    {FontFamily: "system"},
		"serif":          {FontFamily: "serif"},
		"square corners": {Radius: radius(0)},
		"round corners":  {Radius: radius(embed.BrandingRadiusMax)},
	} {
		if err := branding.Validate(); err != nil {
			t.Errorf("%s: Validate() error = %v, want valid", name, err)
		}
	}
	for name, test := range map[string]struct {
		branding embed.Branding
		mentions string
	}{
		"unknown theme":     {embed.Branding{Theme: "sepia"}, "theme"},
		"font name":         {embed.Branding{FontFamily: "Comic Sans MS"}, "fontFamily"},
		"font url":          {embed.Branding{FontFamily: "url(https://evil.example/f.woff2)"}, "fontFamily"},
		"negative radius":   {embed.Branding{Radius: radius(-1)}, "radius"},
		"radius beyond max": {embed.Branding{Radius: radius(embed.BrandingRadiusMax + 1)}, "radius"},
	} {
		err := test.branding.Validate()
		if err == nil || !strings.Contains(err.Error(), test.mentions) {
			t.Errorf("%s: Validate() error = %v, want one naming %s", name, err, test.mentions)
		}
	}
}

// A session's theme, font and radius win; empty ones fall back to the
// deployment's, field by field.
func TestBrandingThemeFallsBackToTheDeployment(t *testing.T) {
	defaults := embed.Branding{Theme: "light", FontFamily: "system", Radius: radius(4)}
	if got := (embed.Branding{}).WithDefaults(defaults); got.Theme != "light" || got.FontFamily != "system" || got.Radius == nil || *got.Radius != 4 {
		t.Fatalf("WithDefaults(empty) = %+v, want the deployment's theme, font and radius", got)
	}
	own := embed.Branding{Theme: "dark", FontFamily: "serif", Radius: radius(0)}
	if got := own.WithDefaults(defaults); got.Theme != "dark" || got.FontFamily != "serif" || *got.Radius != 0 {
		t.Fatalf("WithDefaults(own) = %+v, want the session's own values, a zero radius included", got)
	}
}
