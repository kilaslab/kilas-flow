package main

import (
	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/embed"
)

// newEmbedIssuer builds the embed token issuer from the configuration.
//
// It is a helper rather than an inline call so that what the configuration
// says reaches the issuer in one place that a test can hold: embed.session_ttl
// once had a default, a comment and a reference entry and no reader.
// TestBootBuildsTheEmbedIssuerThroughTheConfiguredHelper keeps run() calling it.
func newEmbedIssuer(key []byte, cfg config.Config) (*embed.Issuer, error) {
	return embed.NewIssuer(key, cfg.Embed.AllowedOrigins, nil,
		embed.WithDefaultLifetime(cfg.Embed.SessionTTL),
		embed.WithDefaultBranding(embed.Branding{Name: cfg.Branding.Name, LogoURL: cfg.Branding.Logo}))
}
