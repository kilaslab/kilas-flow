package runcode

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
)

// The three directories a persistent code cache keeps under its root.
//
// They are siblings under one operator-configured directory so that a
// deployment has one path to mount and one path to keep private, and so the
// version sweeper can see all of them at once.
const (
	artifactDirName    = "artifacts"
	translationDirName = "translations"
	goBuildDirName     = "go-build"
)

// hexDigest is the only shape of key the cache accepts.
//
// Hashes reach the cache from workflow source and from anything that can write
// a workflow document, and a hash is used as a file name: without this check,
// `../x` would write outside the directory the operator decided to trust.
var hexDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// artifactMeta is the sidecar file beside a stored module.
//
// It is JSON rather than a single concatenated file because the two halves are
// written at different times: the module first, the meta second, so a reader
// never sees metadata describing a module that is not there yet.
type artifactMeta struct {
	Hash           string `json:"hash"`
	RuntimeVersion string `json:"runtimeVersion"`
	CompiledAt     string `json:"compiledAt"`
	ModuleSha256   string `json:"moduleSha256"`
	Size           int64  `json:"size"`
}

// DiskCache stores compiled artifacts under a directory, so a restart does not
// throw away work that needed a Go toolchain to produce.
//
// The directory is keyed on the runtime version before anything else: an
// artifact built against an older compilation or execution contract is a
// different artifact, not a stale copy of this one, and the version is part of
// the path rather than a field a reader has to remember to check.
//
// Get and Put are safe for concurrent use. Put is called from execution paths
// that can overlap, and it writes through temporary files so a reader either
// sees the previous content or the complete new content and never a partial
// module.
type DiskCache struct {
	root     string // the operator's directory
	dir      string // <root>/artifacts/<version>
	version  string
	maxBytes int64 // 0 means unbounded
	onError  func(error)

	// front is a write-through memo of what this process has stored. A Code
	// node executing over many items asks for the same artifact repeatedly,
	// and answering from memory keeps the repeated case off the filesystem
	// without giving up the durability the disk gives.
	mu    sync.Mutex
	front map[string]Artifact
}

var _ Cache = (*DiskCache)(nil)

// NewDiskCache opens the artifact cache under dir for one runtime version.
//
// maxBytes bounds the whole directory (artifacts and translations together)
// and 0 means unbounded. The instance fails rather than degrading when the
// directory cannot be created: a caller that wanted persistence and silently
// got a memory cache would only find out after a restart, which is exactly the
// moment persistence was supposed to pay off.
func NewDiskCache(dir, version string, maxBytes int64) (*DiskCache, error) {
	if dir == "" {
		return nil, errors.New("a persistent code cache needs a directory")
	}
	if version == "" {
		return nil, errors.New("a persistent code cache needs a runtime version")
	}
	cache := &DiskCache{
		root:     dir,
		dir:      filepath.Join(dir, artifactDirName, version),
		version:  version,
		maxBytes: maxBytes,
		front:    map[string]Artifact{},
	}
	if err := os.MkdirAll(cache.dir, 0o700); err != nil {
		return nil, fmt.Errorf("create the code artifact cache at %s: %w", cache.dir, err)
	}
	return cache, nil
}

// OnError reports what the cache could not do.
//
// Cache.Put has no error return, by design: a Code node must run whether or
// not its artifact could be persisted. A deployment that wants to know about a
// full or unwritable cache logs what arrives here.
func (cache *DiskCache) OnError(report func(error)) { cache.onError = report }

// Get returns a stored artifact, or a miss.
//
// Every failure short of "not there" is a miss as well, and the entry is
// deleted: a truncated module, a digest that does not match, a meta file that
// lies about its version. The alternative is serving something corrupted, and
// the cache can always be rebuilt from source.
func (cache *DiskCache) Get(hash string) (Artifact, bool) {
	if !hexDigest.MatchString(hash) {
		return Artifact{}, false
	}
	cache.mu.Lock()
	artifact, found := cache.front[hash]
	cache.mu.Unlock()
	if found {
		return artifact, true
	}

	artifact, found = cache.read(hash)
	if !found {
		return Artifact{}, false
	}
	cache.mu.Lock()
	cache.front[hash] = artifact
	cache.mu.Unlock()
	return artifact, true
}

// Put stores an artifact and then enforces the budget.
//
// The budget is re-checked here rather than on a timer because translations
// are written by wazero, not by this type: the only moment at which the
// directory's size is both known and actionable is just after the cache
// itself added something.
func (cache *DiskCache) Put(artifact Artifact) {
	if !hexDigest.MatchString(artifact.Hash) {
		cache.report(fmt.Errorf("refusing to store a code artifact whose hash %q is not a hex digest", artifact.Hash))
		return
	}
	if artifact.RuntimeVersion != "" && artifact.RuntimeVersion != cache.version {
		cache.report(fmt.Errorf("refusing to store a code artifact built for %q in the %q cache",
			artifact.RuntimeVersion, cache.version))
		return
	}
	if err := cache.write(artifact); err != nil {
		cache.report(err)
		return
	}
	cache.mu.Lock()
	cache.front[artifact.Hash] = artifact
	cache.mu.Unlock()

	if err := cache.enforceBudget(); err != nil {
		cache.report(err)
	}
}

// GoBuildDir is the build cache a toolchain build is pointed at, so that a
// read-only root filesystem with only the data volume writable can still
// compile.
func GoBuildDir(root string) string { return filepath.Join(root, goBuildDirName) }

// NewPersistentModuleCache opens the translation cache under dir for one
// runtime version.
//
// wazero appends its own version-, architecture- and OS-specific directory
// beneath the one given, so per-version separation is what keeps a translation
// written by an older host from being loaded by a newer one.
func NewPersistentModuleCache(dir, version string) (*ModuleCache, error) {
	if dir == "" {
		return nil, errors.New("a persistent code cache needs a directory")
	}
	if version == "" {
		return nil, errors.New("a persistent code cache needs a runtime version")
	}
	translations := filepath.Join(dir, translationDirName, version)
	if err := os.MkdirAll(translations, 0o700); err != nil {
		return nil, fmt.Errorf("create the code translation cache at %s: %w", translations, err)
	}
	compilation, err := wazero.NewCompilationCacheWithDir(translations)
	if err != nil {
		return nil, fmt.Errorf("open the wazero translation cache at %s: %w", translations, err)
	}
	return &ModuleCache{compilation: compilation}, nil
}

// PruneStaleVersions removes every artifact and translation directory under
// dir that belongs to a runtime version other than keepVersion.
//
// It touches artifacts and translations only. Everything else under the
// directory — the toolchain's build cache in particular — belongs to another
// owner and is left exactly as it is.
func PruneStaleVersions(dir, keepVersion string) ([]string, error) {
	if dir == "" {
		return nil, nil
	}
	var removed []string
	for _, parent := range []string{artifactDirName, translationDirName} {
		base := filepath.Join(dir, parent)
		entries, err := os.ReadDir(base)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return removed, fmt.Errorf("read %s: %w", base, err)
		}
		for _, entry := range entries {
			if entry.Name() == keepVersion {
				continue
			}
			path := filepath.Join(base, entry.Name())
			if err := os.RemoveAll(path); err != nil {
				return removed, fmt.Errorf("remove %s: %w", path, err)
			}
			removed = append(removed, path)
		}
	}
	return removed, nil
}

// read loads one artifact from disk, or reports a miss.
func (cache *DiskCache) read(hash string) (Artifact, bool) {
	modulePath, metaPath := cache.paths(hash)

	raw, err := os.ReadFile(metaPath)
	if err != nil {
		if !os.IsNotExist(err) {
			cache.report(fmt.Errorf("read %s: %w", metaPath, err))
		}
		return Artifact{}, false
	}
	var meta artifactMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		cache.discard(hash, fmt.Errorf("the code cache entry at %s is unreadable: %w", metaPath, err))
		return Artifact{}, false
	}
	if meta.Hash != hash || meta.RuntimeVersion != cache.version {
		cache.discard(hash, fmt.Errorf("the code cache entry at %s describes %s/%s",
			metaPath, meta.Hash, meta.RuntimeVersion))
		return Artifact{}, false
	}
	module, err := os.ReadFile(modulePath)
	if err != nil {
		cache.discard(hash, fmt.Errorf("read the cached module %s: %w", modulePath, err))
		return Artifact{}, false
	}
	sum := sha256.Sum256(module)
	if int64(len(module)) != meta.Size || hex.EncodeToString(sum[:]) != meta.ModuleSha256 {
		cache.discard(hash, fmt.Errorf("the cached module at %s does not match its digest", modulePath))
		return Artifact{}, false
	}
	compiledAt, err := time.Parse(time.RFC3339, meta.CompiledAt)
	if err != nil {
		cache.discard(hash, fmt.Errorf("the code cache entry at %s has no usable timestamp: %w", metaPath, err))
		return Artifact{}, false
	}

	// Touching the module keeps eviction roughly least-recently-used rather
	// than oldest-written.
	now := time.Now()
	_ = os.Chtimes(modulePath, now, now)

	return Artifact{Hash: hash, RuntimeVersion: meta.RuntimeVersion, Module: module, CompiledAt: compiledAt}, true
}

// write stores the module and then its metadata.
func (cache *DiskCache) write(artifact Artifact) error {
	if err := os.MkdirAll(cache.dir, 0o700); err != nil {
		return fmt.Errorf("create the code artifact cache at %s: %w", cache.dir, err)
	}
	sum := sha256.Sum256(artifact.Module)
	compiledAt := artifact.CompiledAt
	if compiledAt.IsZero() {
		compiledAt = time.Now().UTC()
	}
	meta, err := json.Marshal(artifactMeta{
		Hash:           artifact.Hash,
		RuntimeVersion: cache.version,
		CompiledAt:     compiledAt.UTC().Format(time.RFC3339),
		ModuleSha256:   hex.EncodeToString(sum[:]),
		Size:           int64(len(artifact.Module)),
	})
	if err != nil {
		return fmt.Errorf("encode code artifact metadata: %w", err)
	}

	modulePath, metaPath := cache.paths(artifact.Hash)
	if err := writeFileAtomically(modulePath, artifact.Module); err != nil {
		return err
	}
	// Last: a reader that finds the module without its metadata sees a miss,
	// which it can recover from by rebuilding.
	return writeFileAtomically(metaPath, meta)
}

// discard removes an entry that cannot be trusted and reports why.
func (cache *DiskCache) discard(hash string, cause error) {
	modulePath, metaPath := cache.paths(hash)
	for _, path := range []string{modulePath, metaPath} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			cache.report(fmt.Errorf("remove the unusable code cache entry %s: %w", path, err))
		}
	}
	cache.mu.Lock()
	delete(cache.front, hash)
	cache.mu.Unlock()
	cache.report(cause)
}

func (cache *DiskCache) paths(hash string) (modulePath, metaPath string) {
	base := filepath.Join(cache.dir, hash)
	return base + ".wasm", base + ".json"
}

func (cache *DiskCache) report(err error) {
	if err == nil || cache.onError == nil {
		return
	}
	cache.onError(err)
}

// entry is one file the budget accounts for.
type entry struct {
	path        string
	size        int64
	modified    time.Time
	translation bool
}

// enforceBudget brings the version's directories back under the budget.
//
// Translations go first and oldest-modified first within each kind: a
// translation is wazero's machine code for a module that is still on disk, so
// losing one costs a recompile, while losing an artifact costs a build with a
// Go toolchain that the deployment may not have. Overshooting to ninety
// percent of the budget is deliberate — evicting one file at a time to land
// exactly on the cap would mean a scan and a scan's worth of syscalls on every
// single write.
func (cache *DiskCache) enforceBudget() error {
	if cache.maxBytes <= 0 {
		return nil
	}
	entries, total := cache.scan()
	if total <= cache.maxBytes {
		return nil
	}
	target := cache.maxBytes * 9 / 10

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].translation != entries[j].translation {
			return entries[i].translation
		}
		return entries[i].modified.Before(entries[j].modified)
	})

	evicted := map[string]bool{}
	for _, candidate := range entries {
		if total <= target {
			break
		}
		if err := os.Remove(candidate.path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("evict %s: %w", candidate.path, err)
		}
		total -= candidate.size
		if !candidate.translation {
			evicted[strings.TrimSuffix(filepath.Base(candidate.path), filepath.Ext(candidate.path))] = true
		}
	}
	if len(evicted) > 0 {
		cache.mu.Lock()
		for hash := range evicted {
			delete(cache.front, hash)
		}
		cache.mu.Unlock()
	}
	return nil
}

// scan totals the files the budget covers: this version's artifacts and this
// version's translations, and nothing else under the directory.
func (cache *DiskCache) scan() ([]entry, int64) {
	directories := []struct {
		path        string
		translation bool
	}{
		{cache.dir, false},
		{filepath.Join(cache.root, translationDirName, cache.version), true},
	}
	var (
		entries []entry
		total   int64
	)
	for _, directory := range directories {
		_ = filepath.WalkDir(directory.path, func(path string, dirent fs.DirEntry, err error) error {
			if err != nil || dirent.IsDir() {
				// A directory that vanished, or one that cannot be read, is
				// not a reason to fail an execution.
				return nil
			}
			info, err := dirent.Info()
			if err != nil {
				return nil
			}
			entries = append(entries, entry{
				path:        path,
				size:        info.Size(),
				modified:    info.ModTime(),
				translation: directory.translation,
			})
			total += info.Size()
			return nil
		})
	}
	return entries, total
}

// writeFileAtomically writes content to path through a temporary file in the
// same directory, so a reader never observes a half-written file. The file is
// owner-only: it is native machine code, and the directory is a trust
// boundary.
func writeFileAtomically(path string, content []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	temporary := file.Name()
	defer func() {
		if temporary != "" {
			_ = os.Remove(temporary)
		}
	}()

	if _, err := file.Write(content); err != nil {
		file.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return fmt.Errorf("set permissions on %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("publish %s: %w", path, err)
	}
	temporary = ""
	return nil
}
