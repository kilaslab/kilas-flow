// Authoring subcommands for hand-written node packs.
//
// nodepackgen has two jobs and they stay in one binary because they share one
// format: the flag-driven generator turns an OpenAPI document into a pack,
// and the subcommands below help an author write one by hand. A bare
// invocation with flags still runs the generator — `make node-packs` and
// every script that calls it are unaffected. A first argument that is not a
// flag dispatches here instead:
//
//	nodepackgen scaffold -dir <packs dir>/<name> [-type ...]
//	nodepackgen validate <pack dir or pack.json>...
//	nodepackgen pack -dir <pack dir>
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kilaslabs/kilas-flow/internal/nodepack"
)

// runAuthor runs one authoring subcommand. It reports whether the subcommand
// name was recognised, so run can fall through to the generator path.
func runAuthor(name string, args []string) (bool, error) {
	switch name {
	case "scaffold":
		return true, runScaffold(args)
	case "validate":
		return true, runValidate(args)
	case "pack":
		return true, runPack(args)
	default:
		return false, nil
	}
}

// runScaffold writes a minimal working pack directory: one resource, one
// operation, one credential reference, shaped after the hand-written
// packs/telegram/pack.json. It loads and executes without further editing.
func runScaffold(args []string) error {
	flags := flag.NewFlagSet("scaffold", flag.ContinueOnError)
	dir := flags.String("dir", "", "pack directory to create, e.g. packs/example")
	nodeType := flags.String("type", "pack.example", "node type the pack registers")
	displayName := flags.String("display-name", "Example", "display name the editor shows")
	description := flags.String("description", "", "pack description")
	category := flags.String("category", "Messaging", "editor category")
	credentialType := flags.String("credential-type", "exampleApi", "credential type the pack authenticates with")
	baseURL := flags.String("base-url", "{{ $credentials.baseUrl }}", "shared request base URL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		return fmt.Errorf("scaffold -dir is required")
	}
	pack := nodepack.Scaffold(nodepack.ScaffoldOptions{
		Type: *nodeType, DisplayName: *displayName, Description: *description,
		Category: *category, CredentialType: *credentialType, BaseURL: *baseURL,
	})
	if issues := nodepack.Validate(pack, nodepack.ManifestName); len(issues) != 0 {
		return fmt.Errorf("scaffold produced an invalid pack: %v", issues)
	}
	if err := nodepack.WritePackDir(*dir, pack); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "nodepackgen: scaffolded %s\n", filepath.Join(*dir, nodepack.ManifestName))
	return nil
}

// runValidate reports every problem in each named pack at once, with the file
// and JSON path each came from, rather than aborting on the first unknown
// field. It exits nonzero when any pack has any issue.
func runValidate(args []string) error {
	flags := flag.NewFlagSet("validate", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() == 0 {
		return fmt.Errorf("validate needs at least one pack directory or pack.json path")
	}
	failed := false
	for _, path := range flags.Args() {
		issues := validatePath(path)
		for _, issue := range issues {
			fmt.Fprintln(os.Stderr, "nodepackgen:", issue.Error())
		}
		if len(issues) > 0 {
			failed = true
		}
	}
	if failed {
		return fmt.Errorf("validate found problems")
	}
	fmt.Fprintln(os.Stderr, "nodepackgen: packs are valid")
	return nil
}

func validatePath(path string) []nodepack.Issue {
	info, err := os.Stat(path)
	if err != nil {
		return []nodepack.Issue{{File: path, Path: "$", Message: fmt.Sprintf("cannot read pack: %v", err)}}
	}
	if info.IsDir() {
		return nodepack.ValidateDir(filepath.Dir(path), filepath.Base(path))
	}
	return nodepack.ValidateFile(path)
}

// runPack bundles a pack directory for installation: today that is the
// checksum record the loader verifies, so an operator never computes a digest
// by hand. A pack is JSON the server reads — there is nothing to compile, so
// this is deliberately named for what it does rather than `build`.
func runPack(args []string) error {
	flags := flag.NewFlagSet("pack", flag.ContinueOnError)
	dir := flags.String("dir", "", "pack directory holding a pack.json")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		return fmt.Errorf("pack -dir is required")
	}
	if issues := nodepack.ValidateFile(filepath.Join(*dir, nodepack.ManifestName)); len(issues) != 0 {
		for _, issue := range issues {
			fmt.Fprintln(os.Stderr, "nodepackgen:", issue.Error())
		}
		return fmt.Errorf("pack refuses to seal an invalid pack")
	}
	if err := nodepack.WriteChecksum(*dir); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "nodepackgen: wrote %s\n", filepath.Join(*dir, nodepack.ChecksumName))
	return nil
}
