package skills

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestEmbeddedBundleIsTheSourceTree compares the bundle inside the binary with
// skills/ on disk, file for file and byte for byte.
//
// The embed patterns spell the bundle's shape — the index, one SKILL.md per
// skill, the references beside it — rather than naming today's skills, because
// a pattern that named them would silently drop the router the moment it
// landed. That makes the patterns a claim about the tree, and this is the test
// that holds them to it: a bundle file no pattern matches would ship missing,
// and the only symptom would be a binary that installs one skill fewer and
// exits 0.
func TestEmbeddedBundleIsTheSourceTree(t *testing.T) {
	disk := diskBundle(t)
	embedded := embeddedBundle(t)

	if len(embedded) != len(disk) {
		t.Fatalf("the embedded bundle holds %d files, skills/ holds %d (%s)",
			len(embedded), len(disk), soleDifference(embedded, disk))
	}
	for name, body := range disk {
		got, ok := embedded[name]
		if !ok {
			t.Errorf("%s is in skills/ but not in the binary", name)

			continue
		}
		if !bytes.Equal(got, body) {
			t.Errorf("%s differs between skills/ and the binary", name)
		}
	}
}

// TestLoadBundleReadsTheEmbeddedTree asserts the verbs read the binary's copy
// with the same loader, and so the same frontmatter contract, as the checkout's.
//
// LoadBundle is what every `skills` verb drives. A second reader — one that
// skipped the reference files, or the stamp — would let an install ship a
// bundle the checker refuses.
func TestLoadBundleReadsTheEmbeddedTree(t *testing.T) {
	embedded, err := LoadBundle()
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}
	disk, err := LoadDir(filepath.Join(testRepoRoot(t), "skills"))
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	if len(embedded) != len(disk) {
		t.Fatalf("the embedded bundle holds %d skills, skills/ holds %d", len(embedded), len(disk))
	}
	for index, skill := range embedded {
		other := disk[index]
		if skill.Name != other.Name || skill.SkillsVersion != other.SkillsVersion {
			t.Fatalf("skill %d: the binary holds %s v%d, skills/ holds %s v%d",
				index, skill.Name, skill.SkillsVersion, other.Name, other.SkillsVersion)
		}
		if strings.Join(skill.References, ",") != strings.Join(other.References, ",") {
			t.Fatalf("%s: the binary holds references %q, skills/ holds %q",
				skill.Name, skill.References, other.References)
		}
	}
}

// diskBundle reads skills/ from the repository root, keyed by the path a caller
// of the embedded tree would open.
//
// The bundle's own Go source is skipped: it is the embed directive's address,
// the one file in this directory that is not bundle content, and the patterns
// in embed.go deliberately do not match it.
func diskBundle(t *testing.T) map[string][]byte {
	t.Helper()

	root := filepath.Join(testRepoRoot(t), "skills")
	files := make(map[string][]byte)

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}

		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(name)] = body

		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(files) == 0 {
		t.Fatalf("%s holds no bundle files", root)
	}

	return files
}

// embeddedBundle reads the bundle out of the binary, keyed the same way.
func embeddedBundle(t *testing.T) map[string][]byte {
	t.Helper()

	files := make(map[string][]byte)
	err := fs.WalkDir(BundleFS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}

		body, err := fs.ReadFile(BundleFS(), name)
		if err != nil {
			return err
		}
		files[name] = body

		return nil
	})
	if err != nil {
		t.Fatalf("walk the embedded bundle: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("the embedded bundle holds no files")
	}

	return files
}

// soleDifference names the file that is in exactly one of two trees, so a count
// mismatch says which file caused it rather than only how many there are.
func soleDifference(left, right map[string][]byte) string {
	missing := make([]string, 0, 2)
	extra := make([]string, 0, 2)
	for name := range left {
		if _, ok := right[name]; !ok {
			extra = append(extra, name)
		}
	}
	for name := range right {
		if _, ok := left[name]; !ok {
			missing = append(missing, name)
		}
	}

	sort.Strings(missing)
	sort.Strings(extra)

	return "only embedded: " + strings.Join(extra, ",") + "; only on disk: " + strings.Join(missing, ",")
}
