package cli

import (
	"archive/tar"
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/skills"
)

// skillsTestEnv is the environment the skills tests run in: a working directory
// that holds no part of the repository, and a home directory that holds no
// configuration.
//
// Both are temporary on purpose. Every file the verbs read has to come out of
// the binary — that is the whole point of embedding the bundle — and a test
// run from the package directory would not notice if a verb had quietly gone
// back to reading ./skills.
func skillsTestEnv(t *testing.T) (work, home string) {
	t.Helper()

	work = t.TempDir()
	home = t.TempDir()
	t.Chdir(work)

	return work, home
}

// skillsCLI runs one invocation in the test environment.
func skillsCLI(t *testing.T, home string, args ...string) (code int, stdout, stderr string) {
	t.Helper()

	code, _, stdout, stderr = runCLI(t, Env{Args: args, Getenv: homeEnv(home, nil)})

	return code, stdout, stderr
}

// envelopeData returns the envelope's data object, which is where a successful
// invocation keeps its payload.
func envelopeData(t *testing.T, stdout string) map[string]any {
	t.Helper()

	doc := envelope(t, stdout)
	data, ok := doc["data"].(map[string]any)
	if !ok {
		t.Fatalf("the envelope carries no data object: %s", stdout)
	}

	return data
}

// failureObject returns the envelope's error object, which is where a failed
// invocation keeps its code and message.
func failureObject(t *testing.T, stdout string) map[string]any {
	t.Helper()

	doc := envelope(t, stdout)
	failure, ok := doc["error"].(map[string]any)
	if !ok {
		t.Fatalf("the envelope carries no error object: %s", stdout)
	}

	return failure
}

// field reads one string field of an envelope object.
func field(t *testing.T, object map[string]any, name string) string {
	t.Helper()

	value, ok := object[name].(string)
	if !ok {
		t.Fatalf("the envelope object has no string %q: %v", name, object)
	}

	return value
}

// TestSkillsInstallAndCheckNeedNoCheckout is the acceptance proof for the verbs
// working from the shipped image: a working directory that holds no part of the
// repository, a home directory that holds no configuration, and an install
// whose every file has to have come out of the binary.
//
// It asserts the file layout a consumer sees — one directory per skill, its
// SKILL.md and its reference files beside the index, every byte equal to the
// copy the binary carries — and then that the same binary, run again, calls
// that tree in sync.
func TestSkillsInstallAndCheckNeedNoCheckout(t *testing.T) {
	work, home := skillsTestEnv(t)
	root := filepath.Join(work, "installed")

	code, stdout, stderr := skillsCLI(t, home, "skills", "install", "--target", "dir:"+root, "--json")
	if code != ExitOK {
		t.Fatalf("skills install exited %d, want 0: %s%s", code, stdout, stderr)
	}
	data := envelopeData(t, stdout)
	if got := field(t, data, "path"); got != root {
		t.Fatalf("install path = %q, want %q", got, root)
	}

	files, loaded, err := readBundle()
	if err != nil {
		t.Fatalf("read the binary's bundle: %v", err)
	}
	if len(loaded) < 2 || len(files) < 3 {
		t.Fatalf("the binary's bundle holds %d skills and %d files, which is too few to prove anything", len(loaded), len(files))
	}

	installed := make([]string, 0, len(files))
	for _, file := range files {
		dest := filepath.Join(root, filepath.FromSlash(file.Path))

		onDisk, err := os.ReadFile(dest)
		if err != nil {
			t.Errorf("%s was not installed: %v", file.Path, err)

			continue
		}
		if !bytes.Equal(onDisk, file.Data) {
			t.Errorf("%s was installed with different bytes than the binary carries", file.Path)
		}
		installed = append(installed, file.Path)
	}

	// The layout a harness walks, asserted through the filesystem rather than
	// through the list the install reported: every skill directory, its
	// SKILL.md and every reference the skill declares.
	for _, skill := range loaded {
		body := filepath.Join(root, skill.Name, "SKILL.md")
		if _, err := os.Stat(body); err != nil {
			t.Errorf("%s installed no SKILL.md: %v", skill.Name, err)
		}
		for _, reference := range skill.References {
			if _, err := os.Stat(filepath.Join(root, skill.Name, filepath.FromSlash(reference))); err != nil {
				t.Errorf("%s installed no %s: %v", skill.Name, reference, err)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(root, skillsIndexFile)); err != nil {
		t.Errorf("the install left no %s for a harness to read: %v", skillsIndexFile, err)
	}

	// And the same binary calls its own install in sync: no server, no
	// checkout, nothing to compare against but the bundle it carries.
	code, stdout, stderr = skillsCLI(t, home, "skills", "check", "--target", "dir:"+root, "--json")
	if code != ExitOK {
		t.Fatalf("skills check exited %d after a clean install, want 0: %s%s", code, stdout, stderr)
	}
	report := envelopeData(t, stdout)
	if ok, _ := report["ok"].(bool); !ok {
		t.Fatalf("check reported drift after a clean install: %s", stdout)
	}
	if got := int(report["files"].(float64)); got != len(installed) {
		t.Fatalf("check compared %d files, the install wrote %d", got, len(installed))
	}
}

// TestSkillsInstallDefaultsToTheCheckout pins the default destination:
// project scope and the generic agents target, which is where this repository
// already keeps the pine skill. A default that wrote into the home directory
// instead would put a checkout's skills somewhere the checkout cannot review,
// and a test that never ran the default would not see it.
func TestSkillsInstallDefaultsToTheCheckout(t *testing.T) {
	_, home := skillsTestEnv(t)

	code, stdout, stderr := skillsCLI(t, home, "skills", "install", "--dry-run", "--json")
	if code != ExitOK {
		t.Fatalf("skills install --dry-run exited %d, want 0: %s%s", code, stdout, stderr)
	}

	// The working directory, read back rather than reconstructed: project scope
	// means "under the checkout", and the process is standing in it.
	current, err := os.Getwd()
	if err != nil {
		t.Fatalf("read the working directory: %v", err)
	}
	want := filepath.Join(current, ".agents", "skills")
	if got := field(t, envelopeData(t, stdout), "path"); got != want {
		t.Fatalf("the default destination is %q, want %q", got, want)
	}

	// --dry-run means the caller can ask where the bundle would go without
	// changing the directory they are standing in.
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Fatalf("--dry-run created %s", want)
	}

	// The user scope resolves under the home directory instead, and the two
	// scopes are the only difference between the two invocations.
	code, stdout, stderr = skillsCLI(t, home, "skills", "install", "--target", "claude", "--scope", "user", "--dry-run", "--json")
	if code != ExitOK {
		t.Fatalf("skills install --scope user exited %d, want 0: %s%s", code, stdout, stderr)
	}
	if got, want := field(t, envelopeData(t, stdout), "path"), filepath.Join(home, ".claude", "skills"); got != want {
		t.Fatalf("the user-scope destination is %q, want %q", got, want)
	}
}

// TestSkillsInstallIsIdempotentAndRefusesAnEditedFile is the write contract: a
// second install of an unchanged bundle writes nothing, an edited file stops
// the install with the edit intact, and --force is what replaces it.
func TestSkillsInstallIsIdempotentAndRefusesAnEditedFile(t *testing.T) {
	work, home := skillsTestEnv(t)
	root := filepath.Join(work, "installed")
	target := "dir:" + root

	if code, stdout, stderr := skillsCLI(t, home, "skills", "install", "--target", target, "--json"); code != ExitOK {
		t.Fatalf("the first install exited %d: %s%s", code, stdout, stderr)
	}

	// A re-run of the same install is not work: every file it would write is
	// already there, byte for byte.
	code, stdout, stderr := skillsCLI(t, home, "skills", "install", "--target", target, "--json")
	if code != ExitOK {
		t.Fatalf("the second install exited %d, want 0: %s%s", code, stdout, stderr)
	}
	second := envelopeData(t, stdout)
	if wrote, _ := second["files"].([]any); len(wrote) != 0 {
		t.Fatalf("the second install rewrote %d file(s)", len(wrote))
	}
	if unchanged, _ := second["unchanged"].([]any); len(unchanged) == 0 {
		t.Fatal("the second install reported nothing already in place")
	}

	// An edit is somebody's work, so the install stops rather than overwriting
	// it, and it stops before writing anything: a bundle half this build's and
	// half theirs is the one state neither --force nor a second run describes.
	_, loaded, err := readBundle()
	if err != nil {
		t.Fatalf("read the binary's bundle: %v", err)
	}
	edited := loaded[0]
	body := filepath.Join(root, edited.Name, "SKILL.md")
	original, err := os.ReadFile(body)
	if err != nil {
		t.Fatalf("read the installed %s: %v", body, err)
	}
	edit := append(append([]byte{}, original...), []byte("\n<!-- edited by hand -->\n")...)
	if err := os.WriteFile(body, edit, 0o644); err != nil {
		t.Fatalf("edit the installed skill: %v", err)
	}

	code, stdout, stderr = skillsCLI(t, home, "skills", "install", "--target", target, "--json")
	if code != ExitConflict {
		t.Fatalf("installing over an edited file exited %d, want %d: %s%s", code, ExitConflict, stdout, stderr)
	}
	if got := field(t, failureObject(t, stdout), "code"); got != "install_conflict" {
		t.Fatalf("the refusal code is %q, want install_conflict", got)
	}
	if after, err := os.ReadFile(body); err != nil || !bytes.Equal(after, edit) {
		t.Fatalf("the edit was overwritten by a refused install (err=%v)", err)
	}

	code, stdout, stderr = skillsCLI(t, home, "skills", "install", "--target", target, "--force", "--json")
	if code != ExitOK {
		t.Fatalf("installing with --force exited %d, want 0: %s%s", code, stdout, stderr)
	}
	if after, err := os.ReadFile(body); err != nil || !bytes.Equal(after, original) {
		t.Fatalf("--force did not restore the binary's copy (err=%v)", err)
	}

	if code, stdout, stderr := skillsCLI(t, home, "skills", "check", "--target", target, "--json"); code != ExitOK {
		t.Fatalf("check after --force exited %d, want 0: %s%s", code, stdout, stderr)
	}
}

// TestSkillsCheckReportsDriftWhenAStampChanges is the drift half of the
// acceptance criteria: mutating a stamp makes the verb fail, naming the file
// and both versions. "Drift" is what an agent has to act on, so it is reported
// as a failure with a code rather than as prose the caller has to read.
func TestSkillsCheckReportsDriftWhenAStampChanges(t *testing.T) {
	work, home := skillsTestEnv(t)
	root := filepath.Join(work, "installed")
	target := "dir:" + root

	if code, stdout, stderr := skillsCLI(t, home, "skills", "install", "--target", target, "--json"); code != ExitOK {
		t.Fatalf("install exited %d: %s%s", code, stdout, stderr)
	}

	_, loaded, err := readBundle()
	if err != nil {
		t.Fatalf("read the binary's bundle: %v", err)
	}
	skill := loaded[0]

	body := filepath.Join(root, skill.Name, "SKILL.md")
	installed, err := os.ReadFile(body)
	if err != nil {
		t.Fatalf("read the installed %s: %v", body, err)
	}
	const stamp = "kilasflow_skills_version: "
	mutated := bytes.Replace(installed, []byte(stamp+strconv.Itoa(skill.SkillsVersion)), []byte(stamp+strconv.Itoa(skill.SkillsVersion-1)), 1)
	if bytes.Equal(mutated, installed) {
		t.Fatalf("%s does not declare %s%d, so the stamp cannot be mutated", body, stamp, skill.SkillsVersion)
	}
	if err := os.WriteFile(body, mutated, 0o644); err != nil {
		t.Fatalf("mutate the stamp: %v", err)
	}

	code, stdout, stderr := skillsCLI(t, home, "skills", "check", "--target", target, "--json")
	if code == ExitOK {
		t.Fatalf("check passed with a mutated stamp: %s%s", stdout, stderr)
	}

	failure := failureObject(t, stdout)
	if got := field(t, failure, "code"); got != "skills_drift" {
		t.Fatalf("the drift code is %q, want skills_drift", got)
	}

	message := field(t, failure, "message")
	for _, want := range []string{
		filepath.ToSlash(filepath.Join(skill.Name, "SKILL.md")),
		bundleStamp(skill.SkillsVersion - 1),
		bundleStamp(skill.SkillsVersion),
	} {
		if !strings.Contains(message, want) {
			t.Errorf("the drift message does not name %q: %s", want, message)
		}
	}
}

// TestSkillsCheckReportsASkillTheBinaryDoesNotShip covers the one drift a byte
// comparison cannot see: every file of this build's bundle is present and
// identical, and the harness still loads a skill this build knows nothing
// about.
func TestSkillsCheckReportsASkillTheBinaryDoesNotShip(t *testing.T) {
	work, home := skillsTestEnv(t)
	root := filepath.Join(work, "installed")
	target := "dir:" + root

	if code, stdout, stderr := skillsCLI(t, home, "skills", "install", "--target", target, "--json"); code != ExitOK {
		t.Fatalf("install exited %d: %s%s", code, stdout, stderr)
	}

	stranger := filepath.Join(root, "kilasflow-from-another-build")
	if err := os.MkdirAll(stranger, 0o755); err != nil {
		t.Fatalf("create %s: %v", stranger, err)
	}
	if err := os.WriteFile(filepath.Join(stranger, "SKILL.md"), []byte("---\nname: kilasflow-from-another-build\n---\n"), 0o644); err != nil {
		t.Fatalf("write the stranger's SKILL.md: %v", err)
	}

	code, stdout, stderr := skillsCLI(t, home, "skills", "check", "--target", target, "--json")
	if code == ExitOK {
		t.Fatalf("check passed with an unknown skill installed: %s%s", stdout, stderr)
	}
	if !strings.Contains(field(t, failureObject(t, stdout), "message"), "kilasflow-from-another-build") {
		t.Fatalf("the drift does not name the unknown skill: %s", stdout)
	}
}

// TestSkillsShowPrintsTheDocumentAndItsReferences covers the read side: a
// terminal gets the file itself, frontmatter included, because that is what an
// agent loads and what the file on disk looks like.
func TestSkillsShowPrintsTheDocumentAndItsReferences(t *testing.T) {
	_, home := skillsTestEnv(t)

	_, loaded, err := readBundle()
	if err != nil {
		t.Fatalf("read the binary's bundle: %v", err)
	}
	skill := loaded[0]
	if len(skill.References) == 0 {
		t.Fatalf("%s declares no reference files, so the --reference path cannot be exercised", skill.Name)
	}

	want, err := fs.ReadFile(skills.BundleFS(), filepath.ToSlash(filepath.Join(skill.Name, "SKILL.md")))
	if err != nil {
		t.Fatalf("read the embedded SKILL.md: %v", err)
	}

	// On a terminal, and through a pipe without --json, the file itself is the
	// output: `skills show <name> > SKILL.md` is how one file leaves the binary.
	for _, tty := range []bool{true, false} {
		code, _, stdout, stderr := runCLI(t, Env{
			Args:   []string{"skills", "show", skill.Name},
			Getenv: homeEnv(home, nil),
			TTY:    tty,
		})
		if code != ExitOK {
			t.Fatalf("skills show (tty=%v) exited %d, want 0: %s%s", tty, code, stdout, stderr)
		}
		if stdout != string(want) {
			t.Fatalf("skills show (tty=%v) printed %d bytes, the embedded SKILL.md is %d", tty, len(stdout), len(want))
		}
	}

	// --json is how a caller asks for the envelope instead, and the document
	// travels in it unchanged.
	code, stdout, stderr := skillsCLI(t, home, "skills", "show", skill.Name, "--json")
	if code != ExitOK {
		t.Fatalf("skills show --json exited %d, want 0: %s%s", code, stdout, stderr)
	}
	if got := field(t, envelopeData(t, stdout), "content"); got != string(want) {
		t.Fatalf("skills show --json carried %d bytes of content, the embedded SKILL.md is %d", len(got), len(want))
	}

	// --quiet prints the identifier alone, which for a document is its path.
	code, stdout, stderr = skillsCLI(t, home, "skills", "show", skill.Name, "--quiet")
	if code != ExitOK {
		t.Fatalf("skills show --quiet exited %d, want 0: %s%s", code, stdout, stderr)
	}
	if got, want := stdout, skill.Name+"/SKILL.md\n"; got != want {
		t.Fatalf("skills show --quiet printed %q, want %q", got, want)
	}

	reference := skill.References[0]
	wantReference, err := fs.ReadFile(skills.BundleFS(), filepath.ToSlash(filepath.Join(skill.Name, reference)))
	if err != nil {
		t.Fatalf("read the embedded %s: %v", reference, err)
	}

	code, _, stdout, stderr = runCLI(t, Env{
		Args:   []string{"skills", "show", skill.Name, "--reference", filepath.Base(reference)},
		Getenv: homeEnv(home, nil),
		TTY:    true,
	})
	if code != ExitOK {
		t.Fatalf("skills show --reference exited %d, want 0: %s%s", code, stdout, stderr)
	}
	if stdout != string(wantReference) {
		t.Fatalf("skills show --reference printed %d bytes, the embedded file is %d", len(stdout), len(wantReference))
	}

	// A path that ends in the reference file works as well as the bare name, so
	// a shell completion's answer is accepted; only the base name is honoured,
	// which is what keeps a reference lookup inside the skill.
	code, _, stdout, stderr = runCLI(t, Env{
		Args:   []string{"skills", "show", skill.Name, "--reference", filepath.ToSlash(reference)},
		Getenv: homeEnv(home, nil),
		TTY:    true,
	})
	if code != ExitOK {
		t.Fatalf("skills show --reference %s exited %d, want 0: %s%s", reference, code, stdout, stderr)
	}
	if stdout != string(wantReference) {
		t.Fatalf("skills show --reference %s printed %d bytes, the embedded file is %d", reference, len(stdout), len(wantReference))
	}

	// An unknown name is a lookup miss (exit 4), not a malformed invocation,
	// and the message names what does exist so the caller can fix the name.
	code, stdout, _ = skillsCLI(t, home, "skills", "show", "kilasflow-nope", "--json")
	if code != ExitNotFound {
		t.Fatalf("skills show of an unknown skill exited %d, want %d", code, ExitNotFound)
	}
	if !strings.Contains(field(t, failureObject(t, stdout), "message"), skill.Name) {
		t.Fatalf("the miss does not name a skill that exists: %s", stdout)
	}

	code, stdout, _ = skillsCLI(t, home, "skills", "show", skill.Name, "--reference", "NO_SUCH_FILE.md", "--json")
	if code != ExitNotFound {
		t.Fatalf("skills show of an unknown reference exited %d, want %d", code, ExitNotFound)
	}
	if !strings.Contains(field(t, failureObject(t, stdout), "message"), filepath.Base(reference)) {
		t.Fatalf("the miss does not name the reference the skill ships: %s", stdout)
	}
}

// TestSkillsListNamesEverySkillTheBinaryShips keeps the listing honest: the
// names a caller iterates are the whole bundle, once each, and --quiet prints
// them one per line for a pipeline.
func TestSkillsListNamesEverySkillTheBinaryShips(t *testing.T) {
	_, home := skillsTestEnv(t)

	code, stdout, stderr := skillsCLI(t, home, "skills", "list", "--json")
	if code != ExitOK {
		t.Fatalf("skills list exited %d, want 0: %s%s", code, stdout, stderr)
	}

	_, loaded, err := readBundle()
	if err != nil {
		t.Fatalf("read the binary's bundle: %v", err)
	}

	data := envelopeData(t, stdout)
	if got := int(data["count"].(float64)); got != len(loaded) {
		t.Fatalf("skills list reports %d skills, the binary holds %d", got, len(loaded))
	}
	listed, _ := data["skills"].([]any)
	names := make([]string, 0, len(listed))
	for _, entry := range listed {
		object, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("a listed skill is not an object: %v", entry)
		}
		name := field(t, object, "name")
		names = append(names, name)
		if field(t, object, "description") == "" {
			t.Errorf("%s is listed with no description, so a router cannot choose it", name)
		}
	}
	if got, want := strings.Join(names, ","), strings.Join(skillNames(loaded), ","); got != want {
		t.Fatalf("skills list reports %q, the binary holds %q", got, want)
	}

	code, stdout, stderr = skillsCLI(t, home, "skills", "list", "--quiet")
	if code != ExitOK {
		t.Fatalf("skills list --quiet exited %d, want 0: %s%s", code, stdout, stderr)
	}
	if got, want := stdout, strings.Join(names, "\n")+"\n"; got != want {
		t.Fatalf("skills list --quiet printed %q, want one name per line %q", got, want)
	}
}

// TestSkillsExportHandsOutTheBundleUnchanged covers both formats: the index a
// harness reads, byte for byte, and a tarball a host can unpack somewhere the
// binary will never write. The archive carries the same files the install
// writes — one bundle, three ways out — and two exports of one binary are
// byte-identical, which is what makes the tarball cacheable.
func TestSkillsExportHandsOutTheBundleUnchanged(t *testing.T) {
	work, home := skillsTestEnv(t)

	wantIndex, err := fs.ReadFile(skills.BundleFS(), skillsIndexFile)
	if err != nil {
		t.Fatalf("read the embedded %s: %v", skillsIndexFile, err)
	}

	code, stdout, stderr := skillsCLI(t, home, "skills", "export", "--format", "json")
	if code != ExitOK {
		t.Fatalf("skills export exited %d, want 0: %s%s", code, stdout, stderr)
	}
	if stdout != string(wantIndex) {
		t.Fatalf("skills export --format json printed %d bytes, the shipped index is %d", len(stdout), len(wantIndex))
	}

	first := filepath.Join(work, "first.tar")
	code, stdout, stderr = skillsCLI(t, home, "skills", "export", "--format", "tar", "--out", first, "--json")
	if code != ExitOK {
		t.Fatalf("skills export --format tar exited %d, want 0: %s%s", code, stdout, stderr)
	}
	if got := field(t, envelopeData(t, stdout), "path"); got != first {
		t.Fatalf("the export reported %q, want %q", got, first)
	}

	files, _, err := readBundle()
	if err != nil {
		t.Fatalf("read the binary's bundle: %v", err)
	}
	assertTarballMatchesBundle(t, first, files)

	second := filepath.Join(work, "second.tar")
	if code, stdout, stderr := skillsCLI(t, home, "skills", "export", "--format", "tar", "--out", second, "--json"); code != ExitOK {
		t.Fatalf("the second export exited %d: %s%s", code, stdout, stderr)
	}
	one, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("read %s: %v", first, err)
	}
	two, err := os.ReadFile(second)
	if err != nil {
		t.Fatalf("read %s: %v", second, err)
	}
	if !bytes.Equal(one, two) {
		t.Fatal("two exports of one binary differ, so a tarball cannot be cached or checksummed")
	}

	code, stdout, _ = skillsCLI(t, home, "skills", "export", "--format", "xml", "--json")
	if code != ExitUsage {
		t.Fatalf("an unknown --format exited %d, want %d", code, ExitUsage)
	}
	if got := field(t, failureObject(t, stdout), "code"); got != "usage" {
		t.Fatalf("the unknown-format code is %q, want usage", got)
	}
}

// assertTarballMatchesBundle unpacks an export and compares it with the
// binary's bundle, which is what a host that untars it ends up with.
func assertTarballMatchesBundle(t *testing.T, archive string, files []skillsFile) {
	t.Helper()

	handle, err := os.Open(archive)
	if err != nil {
		t.Fatalf("open %s: %v", archive, err)
	}
	defer handle.Close()

	unpacked := make(map[string][]byte)
	reader := tar.NewReader(handle)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read %s: %v", archive, err)
		}

		body, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read %s out of %s: %v", header.Name, archive, err)
		}
		unpacked[header.Name] = body
	}

	if len(unpacked) != len(files) {
		t.Fatalf("the archive holds %d files, the bundle holds %d", len(unpacked), len(files))
	}
	for _, file := range files {
		body, present := unpacked[file.Path]
		if !present {
			t.Errorf("the archive is missing %s", file.Path)

			continue
		}
		if !bytes.Equal(body, file.Data) {
			t.Errorf("%s differs between the archive and the binary", file.Path)
		}
	}
}
