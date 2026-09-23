// Package luxon embeds the vendored Luxon bundle for the JavaScript runtime.
//
// This file is KilasFlow's; the bundle beside it is upstream's, byte for byte
// (see PROVENANCE.md). Running the bundle defines one global, `luxon`.
package luxon

import _ "embed"

// Version is the upstream release the bundle was taken from.
const Version = "3.7.2"

// Source is the bundle's JavaScript text.
//
//go:embed luxon.min.js
var Source string
