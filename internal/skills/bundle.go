package skills

import (
	"io/fs"

	skillsbundle "github.com/kilaslab/kilas-flow/skills"
)

// BundleFS returns the bundle the binary carries: the tree under skills/ at the
// repository root, embedded at build time by the skills package that sits in
// that directory, so a shipped image installs the bundle with no checkout.
//
// The verbs read the bundle from here rather than from a path, and that is the
// point of embedding it: a distroless image has no checkout to read, and a
// checkout has no business being the answer to "what does this binary ship".
func BundleFS() fs.FS { return skillsbundle.FS() }

// LoadBundle parses the bundle the binary carries, in name order.
//
// It runs the same loader, and so the same frontmatter contract, as LoadDir
// does over a checkout: a bundle that reaches a harness through the binary is
// held to exactly the rules the repository's tests hold it to.
func LoadBundle() ([]Skill, error) { return loadFS(BundleFS()) }
