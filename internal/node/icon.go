package node

import (
	"fmt"
	"regexp"
	"strings"
)

// IconAsset is a node's own artwork, held by the registry rather than read from
// disk.
//
// Holding the bytes here is what lets a pack loaded at composition carry its
// artwork without an asset-directory convention nobody would remember to
// follow.
type IconAsset struct {
	// MediaType is the served Content-Type. Only SVG and PNG are accepted.
	MediaType string
	Bytes     []byte
}

// Supported icon media types. The list is closed: an icon is rendered inside a
// customer's page, so a format this server cannot reason about is not served.
const (
	IconSVG = "image/svg+xml"
	IconPNG = "image/png"
)

var (
	// SVG is an *active document format*: it can carry script, event handlers
	// and external references, and the editor is embedded in customer pages —
	// so a stored XSS in an icon is a cross-tenant problem, not a cosmetic one.
	scriptElement     = regexp.MustCompile(`(?is)<\s*script\b`)
	foreignObject     = regexp.MustCompile(`(?is)<\s*foreignObject\b`)
	eventAttribute    = regexp.MustCompile(`(?is)\bon[a-z]+\s*=`)
	externalRefererer = regexp.MustCompile(`(?is)(href|xlink:href|src)\s*=\s*["']\s*(?:https?:|//|javascript:)`)
	svgRoot           = regexp.MustCompile(`(?is)<\s*svg\b`)
)

// ValidateIcon refuses artwork that could execute in the editor.
//
// Checked at registration rather than on every request: a pack that ships a
// hostile icon should fail to load, loudly, rather than be rendered safely
// forever by defences that only have to be forgotten once. The response headers
// and the `<img>` rendering are the other two layers, and all three are wanted.
func ValidateIcon(icon *IconAsset) error {
	if icon == nil {
		return nil
	}
	if len(icon.Bytes) == 0 {
		return fmt.Errorf("an icon must have bytes")
	}
	switch icon.MediaType {
	case IconPNG:
		return nil
	case IconSVG:
	default:
		return fmt.Errorf("icon media type %q is not supported", icon.MediaType)
	}

	body := string(icon.Bytes)
	if !svgRoot.MatchString(body) {
		return fmt.Errorf("this icon does not parse as an SVG document")
	}
	for _, check := range []struct {
		pattern *regexp.Regexp
		reason  string
	}{
		{scriptElement, "contains a script element"},
		{foreignObject, "contains a foreignObject, which can host arbitrary markup"},
		{eventAttribute, "contains an event handler attribute"},
		{externalRefererer, "references an external or javascript URL"},
	} {
		if check.pattern.MatchString(body) {
			return fmt.Errorf("this icon %s and cannot be served", check.reason)
		}
	}
	return nil
}

// IconFor returns a definition's artwork for a theme, falling back to its light
// variant when a node ships only one.
func (definition Definition) IconFor(theme string) *IconAsset {
	if strings.EqualFold(theme, "dark") && definition.IconDark != nil {
		return definition.IconDark
	}
	return definition.IconLight
}

func cloneAsset(icon *IconAsset) *IconAsset {
	if icon == nil {
		return nil
	}
	return &IconAsset{MediaType: icon.MediaType, Bytes: append([]byte(nil), icon.Bytes...)}
}
