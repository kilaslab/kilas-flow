package corpus

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Source says where a fixture came from, which decides both what may be done
// with its bytes and how a failure should be read. A KilasFlow fixture failing
// is a bug; an upstream template failing is a measurement.
type Source string

const (
	// SourceKilasFlow is authored here and committed, so it is always present.
	SourceKilasFlow Source = "kilasflow"
	// SourceWAHATemplates is the official WAHA template set. Unlicensed
	// upstream: fetched, never committed.
	SourceWAHATemplates Source = "waha-templates"
	// SourceNodesBase is n8n's own node fixtures. Sustainable Use License:
	// fetched from the read-only reference checkout, never committed.
	SourceNodesBase Source = "nodes-base"
	// SourcePrivate is the owner's own exported client workflows, dropped into
	// a gitignored overlay. They contain live phone numbers, hostnames and
	// credential names, so their content never reaches a report or test output.
	SourcePrivate Source = "private"
)

// SyncCommand is the exact command that materialises the corpus. Tests that
// skip name it, so nobody has to go looking.
const SyncCommand = "KILASFLOW_N8N_REFERENCE=/path/to/n8n make corpus"

// Directory environment variables, each with a repository-relative default.
const (
	DirEnv        = "KILASFLOW_CORPUS_DIR"
	PrivateDirEnv = "KILASFLOW_CORPUS_PRIVATE_DIR"
)

//go:embed fixtures/*.json
var authored embed.FS

// Fixture is one n8n workflow document with the provenance needed to read a
// result. Payload is the raw JSON exactly as upstream wrote it.
type Fixture struct {
	// Name identifies the fixture stably across syncs, e.g.
	// "waha-templates/chatting-template/template".
	Name string
	// Source says which corpus it belongs to.
	Source Source
	// Payload is the raw n8n workflow JSON.
	Payload []byte
}

// Redacted returns a description safe to print. A fixture may hold a client's
// live phone numbers and hostnames, so nothing but its identity is ever
// reportable.
func (fixture Fixture) Redacted() string {
	return fmt.Sprintf("%s (%s, %d bytes)", fixture.Name, fixture.Source, len(fixture.Payload))
}

// Available reports whether the third-party corpus has been materialised. The
// authored fixtures are always available; this asks about the fetched ones.
func Available() bool {
	entries, err := os.ReadDir(Dir())
	return err == nil && len(entries) > 0
}

// Dir is where scripts/corpus-sync.sh writes the fetched fixtures.
func Dir() string {
	if override := strings.TrimSpace(os.Getenv(DirEnv)); override != "" {
		return override
	}
	return filepath.Join(repositoryRoot(), ".corpus")
}

// PrivateDir is the overlay for the owner's own exported workflows. It is
// gitignored and usually absent; when it holds documents they are scored
// alongside the public corpus with no code change.
func PrivateDir() string {
	if override := strings.TrimSpace(os.Getenv(PrivateDirEnv)); override != "" {
		return override
	}
	return filepath.Join(repositoryRoot(), ".corpus-private")
}

// repositoryRoot walks up from the working directory to the module root, so a
// test resolves the same directory whichever package it runs from.
func repositoryRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}

// Load returns every fixture available on this machine, sorted by name within
// source so a report's row order is stable.
//
// Absent third-party fixtures are not an error: a clean clone has none, and
// Available reports that separately.
func Load() ([]Fixture, error) {
	var fixtures []Fixture

	authoredFixtures, err := loadAuthored()
	if err != nil {
		return nil, err
	}
	fixtures = append(fixtures, authoredFixtures...)

	fetched, err := loadDirectory(Dir(), "")
	if err != nil {
		return nil, err
	}
	fixtures = append(fixtures, fetched...)

	private, err := loadDirectory(PrivateDir(), string(SourcePrivate))
	if err != nil {
		return nil, err
	}
	fixtures = append(fixtures, private...)

	sort.SliceStable(fixtures, func(i, j int) bool {
		if fixtures[i].Source != fixtures[j].Source {
			return fixtures[i].Source < fixtures[j].Source
		}
		return fixtures[i].Name < fixtures[j].Name
	})
	return fixtures, nil
}

func loadAuthored() ([]Fixture, error) {
	entries, err := authored.ReadDir("fixtures")
	if err != nil {
		return nil, fmt.Errorf("reading embedded fixtures: %w", err)
	}
	fixtures := make([]Fixture, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		payload, err := authored.ReadFile(path.Join("fixtures", entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading embedded fixture %s: %w", entry.Name(), err)
		}
		fixtures = append(fixtures, Fixture{
			Name:    string(SourceKilasFlow) + "/" + strings.TrimSuffix(entry.Name(), ".json"),
			Source:  SourceKilasFlow,
			Payload: payload,
		})
	}
	return fixtures, nil
}

// loadDirectory reads every .json under root. When forceSource is empty the
// source is the first path segment, which is how the sync script lays the
// fetched corpus out; the private overlay has no such structure, so it passes
// its source explicitly.
func loadDirectory(root, forceSource string) ([]Fixture, error) {
	info, err := os.Stat(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading corpus directory %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("corpus path %s is not a directory", root)
	}

	var fixtures []Fixture
	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			return nil
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		payload, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(filepath.ToSlash(relative), ".json")
		source := Source(forceSource)
		if forceSource == "" {
			segments := strings.SplitN(name, "/", 2)
			source = Source(segments[0])
		} else {
			name = forceSource + "/" + name
		}
		fixtures = append(fixtures, Fixture{Name: name, Source: source, Payload: payload})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking corpus directory %s: %w", root, err)
	}
	return fixtures, nil
}

// Manifest is the committed pin file: upstream commits and a digest per
// fixture. The fixtures are not committed; these digests are what makes a
// re-sync reproducible and an upstream edit loud.
type Manifest struct {
	Sources  map[string]ManifestSource `json:"sources"`
	Fixtures []ManifestFixture         `json:"fixtures"`
}

// ManifestSource pins one upstream and records why it may not be committed.
type ManifestSource struct {
	Repository string `json:"repository,omitempty"`
	Origin     string `json:"origin,omitempty"`
	Commit     string `json:"commit"`
	Licence    string `json:"licence"`
}

// ManifestFixture is one pinned file.
type ManifestFixture struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

//go:embed MANIFEST.json
var manifestBytes []byte

// LoadManifest returns the committed pins.
func LoadManifest() (Manifest, error) {
	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("parsing the corpus manifest: %w", err)
	}
	return manifest, nil
}
