// Package corpus exposes the n8n importer regression corpus to any test in the
// repository.
//
// Go's testdata convention is package-local, and the tickets that need to
// measure import fidelity are mostly engine and compiler tickets. Reaching
// across packages with a ../../ path would be brittle in exactly the way that
// makes a measuring instrument stop being trusted, so the corpus is a package
// with one import path instead.
//
// Most of the corpus cannot be committed. n8n's own fixtures are
// LicenseRef-n8n-sustainable-use, and the WAHA templates repository carries no
// licence at all, which is stricter still. Those are fetched into a gitignored
// directory by scripts/corpus-sync.sh, pinned by upstream commit and verified
// per file by digest. Only KilasFlow-authored fixtures live in this package's
// bytes. See .pine/memory/licensing.md.
//
// A clean clone has no corpus materialised, so Load reports that rather than
// failing, and corpus-dependent tests skip with the command that fixes it.
package corpus
