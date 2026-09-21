package cli

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/skills"
)

// skillsVerbs are the five skills verbs of design §5.6: they read the bundle
// out of the binary, write it into a harness's directory, compare what is
// installed with what this build ships, and hand it out unchanged.
//
// Every one of them is local: Operation "" and no HTTP, the way `pack validate`
// is local, because the bundle travels inside the binary and none of these
// verbs is about a server. That is also what makes them work from the shipped
// image — there is no checkout to read, no network to reach, and the only
// filesystem they touch is the install target.
//
// None of them is guarded. Installing local documentation is not publishing a
// workflow or deleting one: it changes a directory the caller named, and the
// worst case — overwriting a file somebody edited — is what `--force` asks
// about rather than what `--yes` exists for.
func skillsVerbs() []Verb {
	return []Verb{
		{
			Path:    "skills list",
			Summary: "list the skills this binary ships, with the situation each one is for",
			Run:     runSkillsList,
			Human:   humanSkillsList,
		},
		{
			Path:    "skills show",
			Summary: "print one skill's SKILL.md, or one of its reference files (`--reference <file>`)",
			Args:    []Arg{arg("skill name")},
			Flags:   registerSkillsShowFlags,
			Run:     runSkillsShow,
		},
		{
			Path:    "skills install",
			Summary: "install the bundle into a harness directory (`--target`, `--scope`, `--force`, `--dry-run`)",
			Flags:   registerSkillsInstallFlags,
			Run:     runSkillsInstall,
			Human:   humanSkillsInstall,
		},
		{
			Path:    "skills check",
			Summary: "compare an installed bundle with this binary's and exit non-zero on drift",
			Flags:   registerSkillsCheckFlags,
			Run:     runSkillsCheck,
			Human:   humanSkillsCheck,
		},
		{
			Path:    "skills export",
			Summary: "write the bundle as the harness index (`--format json`) or a tarball (`--format tar`)",
			Flags:   registerSkillsExportFlags,
			Run:     runSkillsExport,
			Human:   humanSkillsExport,
		},
	}
}

// The install targets and scopes of design §5.6.
const (
	skillsTargetAgents = "agents"
	skillsTargetClaude = "claude"
	skillsTargetCodex  = "codex"
	// skillsDirTarget is `dir:<path>`: the escape hatch for a harness whose
	// directory nobody has taught the CLI about.
	skillsDirTarget = "dir:"

	skillsScopeProject = "project"
	skillsScopeUser    = "user"

	// skillsIndexFile is the generated index a harness reads without parsing
	// markdown. It travels with the bundle and lands at the install root.
	skillsIndexFile = "index.json"
	// skillsSkillBody is the file that makes a directory a skill.
	skillsSkillBody = "SKILL.md"
)

// skillsTargetRoots are the harness directories a target maps onto, relative to
// the scope root.
//
// `agents` is the generic harness directory — `.agents/skills`, where this
// repository keeps the pine skill and where AGENTS.md points Codex, Factory and
// Gemini — and it is the default because it is the one that is not a single
// vendor's. `claude` and `codex` are the two harnesses with a directory of their
// own. `cursor` is deliberately absent: the parent ticket's CLI surface and this
// ticket's acceptance criteria both name claude, codex, agents and dir:<path>.
var skillsTargetRoots = map[string]string{
	skillsTargetAgents: filepath.Join(".agents", "skills"),
	skillsTargetClaude: filepath.Join(".claude", "skills"),
	skillsTargetCodex:  filepath.Join(".codex", "skills"),
}

// skillsDestination is where an install writes and where a check looks.
type skillsDestination struct {
	// Target is what --target said, including the dir:<path> form.
	Target string
	// Scope is the scope the target resolved under, or "" when an absolute
	// dir:<path> named the directory outright and no scope applied.
	Scope string
	// Dir is the directory the bundle's files go under.
	Dir string
}

// resolveSkillsDestination turns --target and --scope into one directory.
//
// Project scope is the default and resolves under the checkout: installing the
// skills is the first thing a session does in a repository, and writing them
// there is what makes a team share one copy. User scope resolves under the home
// directory, for a harness that reads its skills from there.
//
// A relative dir:<path> resolves the same way, so `--target dir:.agents/skills
// --scope user` means the user's own copy of the generic directory rather than
// a path nobody can predict. An absolute dir:<path> is taken as given, because
// a caller who typed an absolute path already said exactly where it goes.
func resolveSkillsDestination(ctx *Context, target, scope string) (skillsDestination, error) {
	target = strings.TrimSpace(target)
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = skillsScopeProject
	}

	if relative, ok := strings.CutPrefix(target, skillsDirTarget); ok {
		relative = strings.TrimSpace(relative)
		if relative == "" {
			return skillsDestination{}, usageError("--target %s needs a path, as in --target dir:/tmp/skills", skillsDirTarget)
		}
		if filepath.IsAbs(relative) {
			return skillsDestination{Target: target, Dir: filepath.Clean(relative)}, nil
		}

		root, err := skillsScopeRoot(ctx, scope)
		if err != nil {
			return skillsDestination{}, err
		}

		return skillsDestination{Target: target, Scope: scope, Dir: filepath.Join(root, relative)}, nil
	}

	if target == "" {
		target = skillsTargetAgents
	}
	relative, known := skillsTargetRoots[target]
	if !known {
		return skillsDestination{}, usageError("unknown --target %q: pass %s, %s, %s or %s<path>",
			target, skillsTargetClaude, skillsTargetCodex, skillsTargetAgents, skillsDirTarget)
	}

	root, err := skillsScopeRoot(ctx, scope)
	if err != nil {
		return skillsDestination{}, err
	}

	return skillsDestination{Target: target, Scope: scope, Dir: filepath.Join(root, relative)}, nil
}

// skillsScopeRoot is the directory a target resolves under: the checkout for
// project scope, the home directory for user scope.
func skillsScopeRoot(ctx *Context, scope string) (string, error) {
	switch scope {
	case skillsScopeProject:
		dir, err := os.Getwd()
		if err != nil {
			return "", outputWriteError("could not read the working directory: %v", err)
		}

		return dir, nil
	case skillsScopeUser:
		home := strings.TrimSpace(getenv(ctx.Env, "HOME"))
		if home == "" {
			return "", usageError("--scope %s needs HOME; set it, or install into the checkout with --scope %s", skillsScopeUser, skillsScopeProject)
		}

		return home, nil
	}

	return "", usageError("unknown --scope %q: pass %s or %s", scope, skillsScopeProject, skillsScopeUser)
}

// skillsFile is one file of the bundle inside the binary, named the way a
// harness's directory holds it: the path relative to the bundle's root.
type skillsFile struct {
	Path string
	Data []byte
}

// readBundle reads the whole embedded bundle: every file, and the skills parsed
// from it.
//
// One read feeds all five verbs, so install writes exactly what check compares
// and export packs, and they cannot disagree about what the bundle holds. It
// validates on the way through — the same loader the repository's own tests run
// — so a verb cannot install a bundle the checker would refuse.
func readBundle() ([]skillsFile, []skills.Skill, error) {
	loaded, err := skills.LoadBundle()
	if err != nil {
		return nil, nil, bundleError(err)
	}

	var files []skillsFile
	err = fs.WalkDir(skills.BundleFS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}

		data, err := fs.ReadFile(skills.BundleFS(), name)
		if err != nil {
			return err
		}
		files = append(files, skillsFile{Path: name, Data: data})

		return nil
	})
	if err != nil {
		return nil, nil, bundleError(err)
	}

	return files, loaded, nil
}

// bundleError reports a bundle that cannot be read.
//
// Both readers run against bytes compiled into the binary, so this is
// unreachable in practice: a bundle that breaks the frontmatter contract fails
// the build rather than a command. It exists because the alternative is a verb
// that cannot fail, and an install that had no way to stop would be the one
// that leaves a harness with half a bundle.
func bundleError(err error) error {
	return &ExitError{
		Code:    ExitFailure,
		ErrCode: "bundle_unreadable",
		Message: "the skills bundle inside this binary could not be read: " + err.Error(),
	}
}

// skillNames is every skill's name, in load order.
func skillNames(loaded []skills.Skill) []string {
	names := make([]string, 0, len(loaded))
	for _, skill := range loaded {
		names = append(names, skill.Name)
	}

	return names
}

// findSkill returns one skill of a loaded bundle.
func findSkill(loaded []skills.Skill, name string) (skills.Skill, bool) {
	for _, skill := range loaded {
		if skill.Name == name {
			return skill, true
		}
	}

	return skills.Skill{}, false
}

// bundleStamp names a version the way a reader of the envelope reads it.
func bundleStamp(version int) string { return "bundle v" + strconv.Itoa(version) }

// skillsListed is one skill as `skills list` reports it: what a router needs to
// pick a skill, without the body.
type skillsListed struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Path        string   `json:"path"`
	Commands    []string `json:"commands"`
	Operations  []string `json:"operations"`
	Nodes       []string `json:"nodes"`
	NotShipped  []string `json:"notShipped"`
	References  []string `json:"references"`
}

// skillsList is the payload of `skills list`.
type skillsList struct {
	SkillsVersion int            `json:"skillsVersion"`
	Count         int            `json:"count"`
	Skills        []skillsListed `json:"skills"`
}

// runSkillsList reports the bundle this binary carries.
//
// The list is built from the frontmatter, not from the shipped index: the
// index is generated from these files, so reading the files answers with what
// the binary actually holds even if the two were ever to disagree.
func runSkillsList(ctx *Context, args []string) error {
	if err := refusePositional(args, "skills list"); err != nil {
		return err
	}

	_, loaded, err := readBundle()
	if err != nil {
		return err
	}

	listed := make([]skillsListed, 0, len(loaded))
	for _, skill := range loaded {
		listed = append(listed, skillsListed{
			Name:        skill.Name,
			Description: skill.Description,
			Path:        skill.Path,
			Commands:    skill.Commands,
			Operations:  skill.Operations,
			Nodes:       skill.Nodes,
			NotShipped:  skill.NotShipped,
			References:  skill.References,
		})
	}

	ctx.Data = skillsList{SkillsVersion: skills.SkillsVersion, Count: len(listed), Skills: listed}
	// One name per line, so a shell pipeline can iterate the bundle.
	ctx.Primary = strings.Join(skillNames(loaded), "\n")

	return nil
}

// humanSkillsList prints the bundle's stamp once and then one line per skill:
// every skill in the bundle carries the same stamp, so repeating it per row
// would be noise.
func humanSkillsList(w io.Writer, data any) {
	list, ok := data.(skillsList)
	if !ok {
		printJSONValue(w, data)

		return
	}

	fmt.Fprintf(w, "%s · %d skills\n", bundleStamp(list.SkillsVersion), list.Count)

	rows := make([][]string, 0, len(list.Skills))
	for _, skill := range list.Skills {
		rows = append(rows, []string{skill.Name, skill.Description})
	}
	printTable(w, []string{"NAME", "DESCRIPTION"}, rows)
}

// skillsShowFlags say which document to print.
type skillsShowFlags struct {
	reference string
}

// registerSkillsShowFlags attaches --reference.
func registerSkillsShowFlags(fs *flag.FlagSet) any {
	flags := &skillsShowFlags{}
	fs.StringVar(&flags.reference, "reference", "", "print this file from the skill's references/ instead of its SKILL.md")

	return flags
}

// skillsDocument is the payload of `skills show` when the caller asked for the
// envelope rather than the document.
type skillsDocument struct {
	Name          string   `json:"name"`
	Path          string   `json:"path"`
	SkillsVersion int      `json:"skillsVersion"`
	Description   string   `json:"description"`
	References    []string `json:"references"`
	Content       string   `json:"content"`
}

// runSkillsShow prints one document of the bundle.
//
// The document goes to stdout raw, frontmatter included, because that is what
// the file on disk looks like and what an agent loads: rendering the body alone
// would hide the declarations a router reads. It is the output on a pipe too —
// `kilasflow skills show kilasflow-triggers > SKILL.md` is the obvious way to
// lift one file out of the binary, and an envelope there would write JSON into
// a file named `.md`. `--json` asks for the envelope instead, and `--quiet`
// prints the document's path alone like every other verb.
func runSkillsShow(ctx *Context, args []string) error {
	name, err := requireOneID(args, "skill name")
	if err != nil {
		return err
	}

	flags, ok := ctx.VerbFlags.(*skillsShowFlags)
	if !ok {
		return usageError("the skills show verb was registered without its flags")
	}

	_, loaded, err := readBundle()
	if err != nil {
		return err
	}

	skill, found := findSkill(loaded, name)
	if !found {
		return &ExitError{
			Code:    ExitNotFound,
			ErrCode: "not_found",
			Message: fmt.Sprintf("no skill named %q; this binary ships %s", name, strings.Join(skillNames(loaded), ", ")),
		}
	}

	document := path.Join(skill.Name, skillsSkillBody)
	var content string

	if reference := strings.TrimSpace(flags.reference); reference != "" {
		// Only the base name is honoured, so a caller cannot ask for a file
		// outside the skill — and a path that ends in the file name still
		// works, which is what a shell completion hands over.
		file := path.Base(reference)
		document = path.Join(skill.Name, "references", file)

		text, known := skill.ReferenceText[path.Join("references", file)]
		if !known {
			return &ExitError{
				Code:    ExitNotFound,
				ErrCode: "not_found",
				Message: fmt.Sprintf("%s has no reference file %q; it ships %s", skill.Name, reference, referenceList(skill)),
			}
		}
		content = text
	} else {
		raw, err := fs.ReadFile(skills.BundleFS(), document)
		if err != nil {
			return bundleError(err)
		}
		content = string(raw)
	}

	ctx.Data = skillsDocument{
		Name:          skill.Name,
		Path:          document,
		SkillsVersion: skill.SkillsVersion,
		Description:   skill.Description,
		References:    skill.References,
		Content:       content,
	}
	ctx.Primary = document

	// The envelope, or the identifier alone, for a caller that asked for one:
	// resolveMode's pipe rule is deliberately not applied, because the document
	// is the output and `skills show <name> | head` is a legitimate way to read
	// it.
	if ctx.Flags != nil && (ctx.Flags.JSON || ctx.Flags.Quiet) {
		return nil
	}

	if _, err := io.WriteString(stdout(ctx.Env), content); err != nil {
		return outputWriteError("could not write %s: %v", document, err)
	}
	// The document is the output; Run writes no envelope over it.
	ctx.Streamed = true

	return nil
}

// referenceList names a skill's reference files for an error message.
func referenceList(skill skills.Skill) string {
	if len(skill.References) == 0 {
		return "none"
	}

	return strings.Join(skill.References, ", ")
}

// skillsInstallFlags are the install's destination and its two modes.
type skillsInstallFlags struct {
	target string
	scope  string
	force  bool
	dryRun bool
}

// registerSkillsInstallFlags attaches the install's flags.
func registerSkillsInstallFlags(fs *flag.FlagSet) any {
	flags := &skillsInstallFlags{}
	fs.StringVar(&flags.target, "target", skillsTargetAgents, "where to install: "+targetList()+", or "+skillsDirTarget+"<path>")
	fs.StringVar(&flags.scope, "scope", skillsScopeProject, "which root the target resolves under: "+skillsScopeProject+" (the checkout) or "+skillsScopeUser+" (the home directory)")
	fs.BoolVar(&flags.force, "force", false, "overwrite files that differ from this binary's bundle")
	fs.BoolVar(&flags.dryRun, "dry-run", false, "report where the bundle would go and write nothing")

	return flags
}

// targetList names the harness targets in the order the help text lists them.
func targetList() string {
	return strings.Join([]string{skillsTargetClaude, skillsTargetCodex, skillsTargetAgents}, ", ")
}

// skillsInstall is the payload of `skills install`.
type skillsInstall struct {
	Path          string   `json:"path"`
	Target        string   `json:"target"`
	Scope         string   `json:"scope"`
	SkillsVersion int      `json:"skillsVersion"`
	Skills        []string `json:"skills"`
	Files         []string `json:"files"`
	Unchanged     []string `json:"unchanged"`
	DryRun        bool     `json:"dryRun"`
}

// runSkillsInstall writes the bundle into a harness directory.
//
// The files are planned before any of them is written, because a refusal
// half-way through is the one outcome nothing can describe afterwards: a
// harness left with a bundle that is partly this binary's and partly whoever
// edited it. A file that is already identical counts as installed rather than
// as work, so re-running the verb is idempotent and a script may always call
// it; a file that differs stops the install unless --force says otherwise.
func runSkillsInstall(ctx *Context, args []string) error {
	if err := refusePositional(args, "skills install"); err != nil {
		return err
	}

	flags, ok := ctx.VerbFlags.(*skillsInstallFlags)
	if !ok {
		return usageError("the skills install verb was registered without its flags")
	}

	destination, err := resolveSkillsDestination(ctx, flags.target, flags.scope)
	if err != nil {
		return err
	}

	files, loaded, err := readBundle()
	if err != nil {
		return err
	}

	var (
		writes    []skillsFile
		unchanged []string
		conflicts []string
	)

	for _, file := range files {
		dest := filepath.Join(destination.Dir, filepath.FromSlash(file.Path))

		existing, readErr := os.ReadFile(dest)
		switch {
		case readErr == nil && bytes.Equal(existing, file.Data):
			unchanged = append(unchanged, file.Path)
		case readErr == nil && !flags.force:
			conflicts = append(conflicts, file.Path)
		case readErr != nil && !errors.Is(readErr, fs.ErrNotExist):
			return outputWriteError("could not read %s: %v", dest, readErr)
		default:
			writes = append(writes, file)
		}
	}

	if len(conflicts) > 0 {
		encoded, err := json.Marshal(conflicts)
		if err != nil {
			return bundleError(err)
		}

		return &ExitError{
			Code:    ExitConflict,
			ErrCode: "install_conflict",
			Message: fmt.Sprintf("%s already holds %d file(s) that differ from this binary's bundle, the first is %s; pass --force to overwrite them, or install somewhere else with --target "+skillsDirTarget+"<path>",
				destination.Dir, len(conflicts), conflicts[0]),
			Issues: encoded,
		}
	}

	report := skillsInstall{
		Path:          destination.Dir,
		Target:        destination.Target,
		Scope:         destination.Scope,
		SkillsVersion: skills.SkillsVersion,
		Skills:        skillNames(loaded),
		Files:         make([]string, 0, len(writes)),
		Unchanged:     unchanged,
		DryRun:        flags.dryRun,
	}
	for _, file := range writes {
		report.Files = append(report.Files, file.Path)

		if flags.dryRun {
			continue
		}

		dest := filepath.Join(destination.Dir, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return outputWriteError("could not create %s: %v", filepath.Dir(dest), err)
		}
		// 0644: the bundle is published documentation that ships inside the
		// binary, not tenant data, and a harness that cannot read it is a
		// harness the install failed silently for.
		if err := os.WriteFile(dest, file.Data, 0o644); err != nil {
			return outputWriteError("could not write %s: %v", dest, err)
		}
	}

	ctx.Data = report
	ctx.Primary = destination.Dir

	return nil
}

// humanSkillsInstall prints where the bundle went and how much of it was
// already there, which is the difference between a first install and a re-run.
func humanSkillsInstall(w io.Writer, data any) {
	report, ok := data.(skillsInstall)
	if !ok {
		printJSONValue(w, data)

		return
	}

	verb := "installed"
	if report.DryRun {
		verb = "would install"
	}
	fmt.Fprintf(w, "%s %d skills into %s (%s)\n", verb, len(report.Skills), report.Path, bundleStamp(report.SkillsVersion))
	if len(report.Unchanged) > 0 {
		fmt.Fprintf(w, "%d file(s) were already in place\n", len(report.Unchanged))
	}
}

// skillsCheckFlags are where a check looks.
type skillsCheckFlags struct {
	target string
	scope  string
}

// registerSkillsCheckFlags attaches the check's destination flags. They are the
// install's, minus the two modes that only make sense while writing.
func registerSkillsCheckFlags(fs *flag.FlagSet) any {
	flags := &skillsCheckFlags{}
	fs.StringVar(&flags.target, "target", skillsTargetAgents, "which installation to check: "+targetList()+", or "+skillsDirTarget+"<path>")
	fs.StringVar(&flags.scope, "scope", skillsScopeProject, "which root the target resolves under: "+skillsScopeProject+" (the checkout) or "+skillsScopeUser+" (the home directory)")

	return flags
}

// skillsDrift is one way an installed bundle differs from this binary's.
//
// The kinds are the five a comparison can see: a file the binary ships is not
// there (missing), a file it cannot read (unreadable), a SKILL.md that declares
// another version (version), a file whose bytes differ (content), and a skill
// directory the binary does not ship at all (extra). The last one is why a byte
// comparison alone is not enough: every file of the binary's bundle can be
// present and identical while the harness still loads a skill this build knows
// nothing about.
type skillsDrift struct {
	Kind      string `json:"kind"`
	File      string `json:"file"`
	Installed string `json:"installed"`
	Expected  string `json:"expected"`
	Message   string `json:"message"`
}

// skillsCheck is the payload of `skills check`.
type skillsCheck struct {
	Path          string        `json:"path"`
	Target        string        `json:"target"`
	Scope         string        `json:"scope"`
	SkillsVersion int           `json:"skillsVersion"`
	OK            bool          `json:"ok"`
	Files         int           `json:"files"`
	Drift         []skillsDrift `json:"drift"`
}

// runSkillsCheck compares an installed bundle with the one this binary carries.
//
// It reports and never repairs: the design's §5.6 says so — the whole point of
// the verb is to be the thing a CI gate or a session can ask without changing
// anything, and a check that silently updated would be a check nobody could
// trust the answer of. Every byte of every file is compared, and each installed
// SKILL.md carries its own version stamp, which is reported separately so a
// stale copy says *which* bundle it came from rather than only "differs".
func runSkillsCheck(ctx *Context, args []string) error {
	if err := refusePositional(args, "skills check"); err != nil {
		return err
	}

	flags, ok := ctx.VerbFlags.(*skillsCheckFlags)
	if !ok {
		return usageError("the skills check verb was registered without its flags")
	}

	destination, err := resolveSkillsDestination(ctx, flags.target, flags.scope)
	if err != nil {
		return err
	}

	files, loaded, err := readBundle()
	if err != nil {
		return err
	}

	report := skillsCheck{
		Path:          destination.Dir,
		Target:        destination.Target,
		Scope:         destination.Scope,
		SkillsVersion: skills.SkillsVersion,
	}

	info, statErr := os.Stat(destination.Dir)
	switch {
	case errors.Is(statErr, fs.ErrNotExist):
		// No directory at all is one finding rather than one per file: what the
		// caller has to do about it is install, not read one line per skill.
		report.Drift = append(report.Drift, skillsDrift{
			Kind:      "missing",
			File:      destination.Dir,
			Installed: "absent",
			Expected:  bundleStamp(skills.SkillsVersion),
			Message:   fmt.Sprintf("no skills are installed at %s", destination.Dir),
		})
	case statErr != nil:
		report.Drift = append(report.Drift, skillsDrift{
			Kind:      "unreadable",
			File:      destination.Dir,
			Installed: "unreadable",
			Expected:  bundleStamp(skills.SkillsVersion),
			Message:   fmt.Sprintf("could not read %s: %v", destination.Dir, statErr),
		})
	case !info.IsDir():
		report.Drift = append(report.Drift, skillsDrift{
			Kind:      "missing",
			File:      destination.Dir,
			Installed: "a file",
			Expected:  "a directory of skills",
			Message:   fmt.Sprintf("%s is not a directory", destination.Dir),
		})
	default:
		report.Files = len(files)
		report.Drift = compareInstalled(destination.Dir, files)

		extras, err := extraSkills(destination.Dir, loaded)
		if err != nil {
			report.Drift = append(report.Drift, skillsDrift{
				Kind:      "unreadable",
				File:      destination.Dir,
				Installed: "unreadable",
				Expected:  bundleStamp(skills.SkillsVersion),
				Message:   fmt.Sprintf("could not list %s: %v", destination.Dir, err),
			})
		}
		report.Drift = append(report.Drift, extras...)
	}

	report.OK = len(report.Drift) == 0
	ctx.Data = report
	ctx.Primary = strconv.FormatBool(report.OK)

	if report.OK {
		return nil
	}

	encoded, err := json.Marshal(report.Drift)
	if err != nil {
		return bundleError(err)
	}

	// Exit 1, the same verdict `pack validate` reaches: the invocation was
	// right and the thing it inspected is not what this build ships.
	return &ExitError{
		Code:    ExitFailure,
		ErrCode: "skills_drift",
		Message: driftMessage(report),
		Issues:  encoded,
	}
}

// compareInstalled reads every file of the binary's bundle out of the installed
// tree and reports what is not there, cannot be read, or differs.
func compareInstalled(dir string, files []skillsFile) []skillsDrift {
	drift := make([]skillsDrift, 0)
	for _, file := range files {
		dest := filepath.Join(dir, filepath.FromSlash(file.Path))

		existing, err := os.ReadFile(dest)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			drift = append(drift, skillsDrift{
				Kind:      "missing",
				File:      file.Path,
				Installed: "absent",
				Expected:  "this binary's copy",
				Message:   fmt.Sprintf("%s is not installed", file.Path),
			})

			continue
		case err != nil:
			drift = append(drift, skillsDrift{
				Kind:      "unreadable",
				File:      file.Path,
				Installed: "unreadable",
				Expected:  "this binary's copy",
				Message:   fmt.Sprintf("could not read %s: %v", dest, err),
			})

			continue
		}

		// The stamp of a SKILL.md is a separate finding from its bytes: "this
		// skill came from bundle v0" is what tells a caller to re-install,
		// where "differs" only tells them the two files are not equal. It is
		// also the finding that survives a stamp edit which a byte diff would
		// have reported as an edit among edits.
		if path.Base(file.Path) == skillsSkillBody {
			declared, err := skills.DeclaredVersion(existing)
			switch {
			case err != nil:
				drift = append(drift, skillsDrift{
					Kind:      "version",
					File:      file.Path,
					Installed: "no readable stamp",
					Expected:  bundleStamp(skills.SkillsVersion),
					Message:   fmt.Sprintf("%s: %v", file.Path, err),
				})

				continue
			case declared != skills.SkillsVersion:
				drift = append(drift, skillsDrift{
					Kind:      "version",
					File:      file.Path,
					Installed: bundleStamp(declared),
					Expected:  bundleStamp(skills.SkillsVersion),
					Message:   fmt.Sprintf("%s declares %s; this binary ships %s", file.Path, bundleStamp(declared), bundleStamp(skills.SkillsVersion)),
				})

				continue
			}
		}

		if !bytes.Equal(existing, file.Data) {
			drift = append(drift, skillsDrift{
				Kind:      "content",
				File:      file.Path,
				Installed: "different bytes",
				Expected:  "this binary's copy",
				Message:   fmt.Sprintf("%s differs from this binary's copy", file.Path),
			})
		}
	}

	return drift
}

// extraSkills names installed skill directories the bundle does not hold.
func extraSkills(dir string, loaded []skills.Skill) ([]skillsDrift, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	drift := make([]skillsDrift, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, entry.Name(), skillsSkillBody)); err != nil {
			continue
		}
		if _, shipped := findSkill(loaded, entry.Name()); shipped {
			continue
		}

		drift = append(drift, skillsDrift{
			Kind:      "extra",
			File:      path.Join(entry.Name(), skillsSkillBody),
			Installed: "present",
			Expected:  "not part of " + bundleStamp(skills.SkillsVersion),
			Message:   fmt.Sprintf("%s is installed but this binary ships no such skill", entry.Name()),
		})
	}

	return drift, nil
}

// driftMessage names what differs, and how much of it does.
func driftMessage(report skillsCheck) string {
	first := report.Drift[0]
	if len(report.Drift) == 1 {
		return fmt.Sprintf("the bundle installed at %s is not this binary's %s: %s", report.Path, bundleStamp(report.SkillsVersion), first.Message)
	}

	return fmt.Sprintf("the bundle installed at %s is not this binary's %s: %d differences, the first is %s",
		report.Path, bundleStamp(report.SkillsVersion), len(report.Drift), first.Message)
}

// humanSkillsCheck prints the verdict and then every difference, because the
// reason to run the verb by hand is to read them.
func humanSkillsCheck(w io.Writer, data any) {
	report, ok := data.(skillsCheck)
	if !ok {
		printJSONValue(w, data)

		return
	}

	printKV(w, [][2]string{
		{"path", report.Path},
		{"bundle", bundleStamp(report.SkillsVersion)},
		{"ok", strconv.FormatBool(report.OK)},
		{"files", strconv.Itoa(report.Files)},
	})
	for _, drift := range report.Drift {
		fmt.Fprintf(w, "%s: %s: %s\n", drift.Kind, drift.File, drift.Message)
	}
}

// skillsExportFlags say what to write and where.
type skillsExportFlags struct {
	format string
	out    string
}

// registerSkillsExportFlags attaches --format and --out.
func registerSkillsExportFlags(fs *flag.FlagSet) any {
	flags := &skillsExportFlags{}
	fs.StringVar(&flags.format, "format", "json", "what to write: json (the index a harness reads) or tar (the whole bundle)")
	fs.StringVar(&flags.out, "out", "-", "write to a file; - (the default) streams it to stdout")

	return flags
}

// skillsExport is the payload of `skills export` when it wrote a file.
type skillsExport struct {
	Format        string `json:"format"`
	Path          string `json:"path"`
	Bytes         int    `json:"bytes"`
	Skills        int    `json:"skills"`
	SkillsVersion int    `json:"skillsVersion"`
}

// runSkillsExport hands the bundle out unchanged.
//
// `--format json` is the machine-readable index of design §5.6: the generated
// document a harness reads without parsing markdown. `--format tar` is the
// whole bundle in one file, for a host that has the binary and wants the skills
// somewhere the binary will never write — a CI cache, an image layer, another
// machine. Both are the bytes the binary carries: the export re-renders nothing,
// so what leaves this verb is what an install would write.
func runSkillsExport(ctx *Context, args []string) error {
	if err := refusePositional(args, "skills export"); err != nil {
		return err
	}

	flags, ok := ctx.VerbFlags.(*skillsExportFlags)
	if !ok {
		return usageError("the skills export verb was registered without its flags")
	}

	files, loaded, err := readBundle()
	if err != nil {
		return err
	}

	format := strings.ToLower(strings.TrimSpace(flags.format))
	var body []byte
	switch format {
	case "json":
		index, found := findBundleFile(files, skillsIndexFile)
		if !found {
			return bundleError(errors.New("the bundle carries no " + skillsIndexFile))
		}
		body = index
	case "tar":
		body, err = tarBundle(files)
		if err != nil {
			return bundleError(err)
		}
	default:
		return usageError("unknown --format %q: pass json or tar", flags.format)
	}

	if flags.out == "-" {
		if _, err := stdout(ctx.Env).Write(body); err != nil {
			return outputWriteError("could not write the export to stdout: %v", err)
		}
		// The bytes are the output; Run writes no envelope over them.
		ctx.Streamed = true

		return nil
	}

	// 0644, unlike an execution's CSV export: the bundle is published
	// documentation that already ships inside the binary, and a tarball a team
	// cannot read is a tarball nobody shares.
	if err := os.WriteFile(flags.out, body, 0o644); err != nil {
		return outputWriteError("could not write %s: %v", flags.out, err)
	}

	ctx.Data = skillsExport{
		Format:        format,
		Path:          flags.out,
		Bytes:         len(body),
		Skills:        len(loaded),
		SkillsVersion: skills.SkillsVersion,
	}
	ctx.Primary = flags.out

	return nil
}

// findBundleFile returns one file of a read bundle.
func findBundleFile(files []skillsFile, name string) ([]byte, bool) {
	for _, file := range files {
		if file.Path == name {
			return file.Data, true
		}
	}

	return nil, false
}

// tarBundle packs the bundle.
//
// The archive is deterministic — files in path order, no timestamps, no owner,
// no host — so two exports of one binary are byte-identical and a tarball can
// be checksummed, cached and diffed. Nothing here reads the clock, which is
// also why the header carries no modification time: a release artefact that
// changes on every export is an artefact no cache can hold.
func tarBundle(files []skillsFile) ([]byte, error) {
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)

	for _, file := range files {
		header := &tar.Header{
			Name:   file.Path,
			Mode:   0o644,
			Size:   int64(len(file.Data)),
			Format: tar.FormatUSTAR,
		}
		if err := writer.WriteHeader(header); err != nil {
			return nil, fmt.Errorf("write the header of %s: %w", file.Path, err)
		}
		if _, err := writer.Write(file.Data); err != nil {
			return nil, fmt.Errorf("write %s: %w", file.Path, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close the archive: %w", err)
	}

	return buffer.Bytes(), nil
}

// humanSkillsExport prints where the bytes went and how many there were.
func humanSkillsExport(w io.Writer, data any) {
	report, ok := data.(skillsExport)
	if !ok {
		printJSONValue(w, data)

		return
	}

	printKV(w, [][2]string{
		{"format", report.Format},
		{"path", report.Path},
		{"bytes", strconv.Itoa(report.Bytes)},
		{"skills", strconv.Itoa(report.Skills)},
		{"bundle", bundleStamp(report.SkillsVersion)},
	})
}
