// Package lodash embeds the vendored lodash bundle for the JavaScript runtime.
//
// This file is KilasFlow's; the bundle beside it is upstream's, byte for byte
// (see PROVENANCE.md). Running the bundle defines one global, `_`.
package lodash

import _ "embed"

// Version is the upstream release the bundle was taken from.
const Version = "4.18.1"

// Source is the bundle's JavaScript text.
//
//go:embed lodash.min.js
var Source string
