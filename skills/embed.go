// Package skills embeds the agent skills bundle into the kilasflow binary.
//
// The bundle is this directory: one directory per skill, each holding a
// SKILL.md and the reference files beside it, plus the generated index.json.
// It ships inside the binary — the way internal/web embeds the SPA — so
// `kilasflow skills install` works from a distroless image, with no checkout,
// no network, and no filesystem access beyond the directory it installs into.
//
// This file is the bundle's only Go source, and it lives here because it has
// to: a package can embed its own directory and the tree below it and nothing
// above it, so the package that can reach the bundle is the one standing in it.
//
// The patterns below deliberately spell the bundle's shape rather than listing
// today's skills: a pattern naming the twelve domains would silently drop the
// router the moment it lands, and the failure would be a binary that installs
// eleven skills and says nothing. internal/skills/embed_test.go asserts the
// embedded tree and this directory hold the same files, byte for byte, so a
// file the patterns miss fails a test rather than a customer.
package skills

import (
	"embed"
	"io/fs"
)

// bundle holds every file the bundle ships.
//
// The three patterns are the three things a bundle holds, and together they
// exclude this file — a Go source file is the embed directive's own address,
// not bundle content, and install must not copy it into a harness's skills
// directory:
//
//	index.json              the generated index a harness reads
//	*/SKILL.md              one skill per directory
//	*/references/*.md       the depth beside it
//
// The all: prefix is not cosmetic. It is what makes the walk include files
// whose names begin with "." or "_", which go:embed skips otherwise; a bundle
// file named that way would be installed silently missing. The directory names
// are a *glob* rather than a list on purpose: see the package comment.
//
//go:embed all:index.json all:*/SKILL.md all:*/references/*.md
var bundle embed.FS

// FS returns the embedded bundle, rooted where a harness's skills directory
// would be: index.json and the skill directories beside it.
//
// It is the bundle the binary installs, checks and exports, so a caller reads
// one tree and the three verbs cannot disagree about what is in it.
func FS() fs.FS { return bundle }
