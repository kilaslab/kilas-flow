package config

import (
	"fmt"

	"github.com/kilaslab/kilas-flow/internal/embed"
)

// validateEmbed checks the embed settings whose rules belong to the embed
// package.
//
// It lives beside Validate rather than inside it so that the one config file
// that imports internal/embed is this one, and so the range is embed's own
// (embed.CheckDefaultLifetime, the same function the issuer option runs) rather
// than a second copy here that could drift from it.
//
// It runs whether or not an embed signing key is set. With no key no issuer is
// built, and a bad value would otherwise be accepted silently and refused only
// on the day someone adds the key.
func (c Config) validateEmbed() error {
	if err := embed.CheckDefaultLifetime(c.Embed.SessionTTL); err != nil {
		return fmt.Errorf("embed.session_ttl: %w (write a unit, for example 15m: a bare number is read as nanoseconds)", err)
	}
	// The deployment's branding goes through the same validation a host's does,
	// here at boot rather than at mint: a logo the editor could never render
	// must stop the server, not answer 422 to every session a host asks for.
	// The two branding keys this ticket removed are not validated here: nothing
	// read them and no "Powered by" mark exists anywhere. A configuration that
	// still sets them is reported by warnUnknownKeys, never refused here.
	if err := (embed.Branding{Name: c.Branding.Name, LogoURL: c.Branding.Logo}).Validate(); err != nil {
		return fmt.Errorf("branding.name / branding.logo: %w", err)
	}
	return nil
}
