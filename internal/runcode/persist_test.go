package runcode_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/runcode"
	"github.com/kilaslab/kilas-flow/internal/wasmtest"
)

// Every test in this file is toolchain-free and proves it: exec.LookPath reads
// PATH, and these tests clear it, so anything that tried to reach `go` would
// fail here rather than silently depending on the machine. That also means
// none of them may call t.Parallel().
const (
	translationVersion = runcode.RuntimeVersion
	emptyItems         = "{\"items\":[]}\n"
)

// fixedCompiler returns one module for any source and counts how often it was
// asked to build, which is how "the artifact survived a restart" is proved
// without a toolchain.
type fixedCompiler struct {
	module []byte

	mu     sync.Mutex
	builds int
}

func (compiler *fixedCompiler) Available() bool { return true }

func (compiler *fixedCompiler) Compile(context.Context, string) ([]byte, error) {
	compiler.mu.Lock()
	defer compiler.mu.Unlock()
	compiler.builds++
	return compiler.module, nil
}

func (compiler *fixedCompiler) count() int {
	compiler.mu.Lock()
	defer compiler.mu.Unlock()
	return compiler.builds
}

// translationFiles lists what wazero wrote under the version's translation
// directory. wazero appends its own wazero-<version>-<arch>-<os> directory, so
// one level of globbing is not enough.
func translationFiles(t *testing.T, dir, version string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "translations", version, "*", "*"))
	if err != nil {
		t.Fatalf("glob translations: %v", err)
	}
	sort.Strings(matches)
	return matches
}

// minimalArtifact is the artifact the cache tests store: a hand-built module
// that needs no compiler, plus the fields Execute checks.
func minimalArtifact(stdout string) runcode.Artifact {
	module := wasmtest.MinimalModule(stdout)
	return runcode.Artifact{
		Hash:           runcode.SourceHash(stdout),
		RuntimeVersion: runcode.RuntimeVersion,
		Module:         module,
		CompiledAt:     time.Now().UTC(),
	}
}

func artifactPaths(dir, version, hash string) (module string, meta string) {
	base := filepath.Join(dir, "artifacts", version, hash)
	return base + ".wasm", base + ".json"
}

// The whole point of the shared ModuleCache is that wazero is not asked to
// translate the same module twice, and a timing assertion is not a proof of
// that — it is a proof that the machine was fast enough on the day. This
// deletes wazero's own on-disk translation after the first run and shows the
// second run on the same cache does not write it again: a memory hit returns
// before wazero ever consults the file cache. A fresh cache over the emptied
// directory is the control, and it does write the file again, so the probe
// cannot pass vacuously.
func TestASecondRunOfAnUnchangedArtifactDoesNotRecompile(t *testing.T) {
	t.Setenv("PATH", "")

	ctx := context.Background()
	dir := t.TempDir()
	modules := newPersistentModuleCache(t, dir, runcode.RuntimeVersion)

	runner := runcode.NewRunner(nil, runcode.NewMemoryCache(), modules, runcode.DefaultLimits())
	artifact := minimalArtifact(emptyItems)

	if _, err := runner.Execute(ctx, artifact, nil); err != nil {
		t.Fatalf("first Execute() error = %v", err)
	}

	files := translationFiles(t, dir, runcode.RuntimeVersion)
	if len(files) == 0 {
		// The interpreter engine does not write translations at all. Skipping
		// with a reason is honest; asserting anything here would be a lie.
		t.Skipf("wazero wrote no translation files under %s; this platform uses the interpreter", dir)
	}
	if len(files) != 1 {
		t.Fatalf("wazero wrote %d translation files for one module, want 1: %v", len(files), files)
	}
	for _, file := range files {
		if err := os.Remove(file); err != nil {
			t.Fatalf("remove translation: %v", err)
		}
	}

	// Second run, same ModuleCache. Nothing was recompiled, so nothing is
	// written back.
	if _, err := runner.Execute(ctx, artifact, nil); err != nil {
		t.Fatalf("second Execute() error = %v", err)
	}
	if files := translationFiles(t, dir, runcode.RuntimeVersion); len(files) != 0 {
		t.Errorf("the second run rewrote %d translation files, so the module was translated again: %v", len(files), files)
	}

	// Control: an empty cache over the same directory has no memory hit and no
	// file to load, so it must compile and write.
	control := newPersistentModuleCache(t, dir, runcode.RuntimeVersion)
	controlRunner := runcode.NewRunner(nil, runcode.NewMemoryCache(), control, runcode.DefaultLimits())
	if _, err := controlRunner.Execute(ctx, artifact, nil); err != nil {
		t.Fatalf("control Execute() error = %v", err)
	}
	if files := translationFiles(t, dir, runcode.RuntimeVersion); len(files) != 1 {
		t.Errorf("the control run wrote %d translation files, want 1: the probe proves nothing", len(files))
	}
}

// A translation loaded from disk must not be rewritten, which is what makes a
// restart cheap. wazero writes its files temp+rename, so a rewrite changes the
// modification time; a file-cache hit leaves it exactly as it was.
func TestCompiledModulesSurviveARestart(t *testing.T) {
	t.Setenv("PATH", "")

	ctx := context.Background()
	dir := t.TempDir()
	artifact := minimalArtifact(emptyItems)

	first := newPersistentModuleCache(t, dir, runcode.RuntimeVersion)
	if _, err := runcode.NewRunner(nil, runcode.NewMemoryCache(), first, runcode.DefaultLimits()).
		Execute(ctx, artifact, nil); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	before := translationModTimes(t, dir, runcode.RuntimeVersion)
	if len(before) == 0 {
		t.Skipf("wazero wrote no translation files under %s; this platform uses the interpreter", dir)
	}
	if err := first.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// One filesystem timestamp tick, so a rewrite could not hide behind it.
	time.Sleep(15 * time.Millisecond)

	second := newPersistentModuleCache(t, dir, runcode.RuntimeVersion)
	if _, err := runcode.NewRunner(nil, runcode.NewMemoryCache(), second, runcode.DefaultLimits()).
		Execute(ctx, artifact, nil); err != nil {
		t.Fatalf("Execute() after restart error = %v", err)
	}
	after := translationModTimes(t, dir, runcode.RuntimeVersion)

	if len(after) != len(before) {
		t.Fatalf("translation files after a restart = %v, want the same %v", after, before)
	}
	for name, modified := range before {
		if !after[name].Equal(modified) {
			t.Errorf("translation %s was rewritten after a restart (%s -> %s), so it was not loaded from disk",
				name, modified, after[name])
		}
	}
}

// A build against an older host ABI must be rebuilt rather than loaded, and the
// translation cache has to draw the same line: two versions sharing one
// directory must never read each other's translations.
func TestATranslationCacheIsKeyedOnTheRuntimeVersion(t *testing.T) {
	t.Setenv("PATH", "")

	ctx := context.Background()
	dir := t.TempDir()
	artifact := minimalArtifact(emptyItems)

	older := newPersistentModuleCache(t, dir, "wasip1-v0")
	if _, err := runcode.NewRunner(nil, runcode.NewMemoryCache(), older, runcode.DefaultLimits()).
		Execute(ctx, artifact, nil); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	olderFiles := translationFiles(t, dir, "wasip1-v0")
	if len(olderFiles) == 0 {
		t.Skipf("wazero wrote no translation files under %s; this platform uses the interpreter", dir)
	}

	current := newPersistentModuleCache(t, dir, "wasip1-v1")
	if _, err := runcode.NewRunner(nil, runcode.NewMemoryCache(), current, runcode.DefaultLimits()).
		Execute(ctx, artifact, nil); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if files := translationFiles(t, dir, "wasip1-v1"); len(files) != 1 {
		t.Errorf("the current version wrote %d translations, want 1", len(files))
	}
	if after := translationFiles(t, dir, "wasip1-v0"); len(after) != len(olderFiles) {
		t.Errorf("the older version's translations changed: %v -> %v", olderFiles, after)
	}
}

func TestAnArtifactSurvivesARestart(t *testing.T) {
	t.Setenv("PATH", "")

	dir := t.TempDir()
	artifact := minimalArtifact(emptyItems)

	cache, err := runcode.NewDiskCache(dir, runcode.RuntimeVersion, 0)
	if err != nil {
		t.Fatalf("NewDiskCache() error = %v", err)
	}
	cache.Put(artifact)

	restarted, err := runcode.NewDiskCache(dir, runcode.RuntimeVersion, 0)
	if err != nil {
		t.Fatalf("NewDiskCache() after restart error = %v", err)
	}
	loaded, found := restarted.Get(artifact.Hash)
	if !found {
		t.Fatal("an artifact written before the restart was a miss afterwards")
	}
	if !bytes.Equal(loaded.Module, artifact.Module) {
		t.Error("the restored module differs from the stored one")
	}
	if loaded.Hash != artifact.Hash || loaded.RuntimeVersion != runcode.RuntimeVersion {
		t.Errorf("restored artifact = %+v, want the stored identity", loaded)
	}
	if !loaded.CompiledAt.Equal(artifact.CompiledAt.Truncate(time.Second)) {
		t.Errorf("CompiledAt = %s, want %s (the meta file stores RFC3339 seconds)",
			loaded.CompiledAt, artifact.CompiledAt.Truncate(time.Second))
	}
}

// The persisted identity is the HTTP-visible hash *and* the runtime version,
// which is the trap the ticket names: a stale artifact compiled against a host
// contract that no longer exists must never be loaded.
func TestAnArtifactFromAnOlderRuntimeVersionIsRebuiltNotLoaded(t *testing.T) {
	t.Setenv("PATH", "")

	dir := t.TempDir()
	artifact := minimalArtifact(emptyItems)

	older, err := runcode.NewDiskCache(dir, "wasip1-v0", 0)
	if err != nil {
		t.Fatalf("NewDiskCache() error = %v", err)
	}
	older.Put(artifact)

	current, err := runcode.NewDiskCache(dir, runcode.RuntimeVersion, 0)
	if err != nil {
		t.Fatalf("NewDiskCache() error = %v", err)
	}
	if _, found := current.Get(artifact.Hash); found {
		t.Error("an artifact built against wasip1-v0 was loaded by a wasip1-v1 cache")
	}

	// A meta file that lies about its own version is corruption, not a load.
	modulePath, metaPath := artifactPaths(dir, runcode.RuntimeVersion, artifact.Hash)
	if err := os.MkdirAll(filepath.Dir(modulePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modulePath, artifact.Module, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(artifact.Module)
	meta := fmt.Sprintf(`{"hash":%q,"runtimeVersion":"wasip1-v0","compiledAt":%q,"moduleSha256":%q,"size":%d}`,
		artifact.Hash, time.Now().UTC().Format(time.RFC3339), hex.EncodeToString(sum[:]), len(artifact.Module))
	if err := os.WriteFile(metaPath, []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}

	restarted, err := runcode.NewDiskCache(dir, runcode.RuntimeVersion, 0)
	if err != nil {
		t.Fatalf("NewDiskCache() error = %v", err)
	}
	if _, found := restarted.Get(artifact.Hash); found {
		t.Error("a meta file claiming an older runtime version was loaded")
	}
}

// The artifact cache is the expensive half of the work: it is what needed the
// toolchain. A restart must not throw it away.
func TestARunnerOverADiskCacheDoesNotBuildAgainAfterARestart(t *testing.T) {
	t.Setenv("PATH", "")

	ctx := context.Background()
	dir := t.TempDir()
	compiler := &fixedCompiler{module: wasmtest.MinimalModule(emptyItems)}
	const source = "return items, nil"

	cache, err := runcode.NewDiskCache(dir, runcode.RuntimeVersion, 0)
	if err != nil {
		t.Fatalf("NewDiskCache() error = %v", err)
	}
	first := runcode.NewRunner(compiler, cache, nil, runcode.DefaultLimits())
	artifact, err := first.Artifact(ctx, source)
	if err != nil {
		t.Fatalf("Artifact() error = %v", err)
	}
	if compiler.count() != 1 {
		t.Fatalf("the first run built %d times, want 1", compiler.count())
	}
	result, err := first.Execute(ctx, artifact, nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(result.Items) != 0 {
		t.Errorf("Execute() returned %d items, want none", len(result.Items))
	}

	restarted, err := runcode.NewDiskCache(dir, runcode.RuntimeVersion, 0)
	if err != nil {
		t.Fatalf("NewDiskCache() after restart error = %v", err)
	}
	second := runcode.NewRunner(compiler, restarted, nil, runcode.DefaultLimits())
	again, err := second.Artifact(ctx, source)
	if err != nil {
		t.Fatalf("Artifact() after restart error = %v", err)
	}
	if compiler.count() != 1 {
		t.Errorf("the toolchain ran %d times across a restart, want 1: the artifact did not survive", compiler.count())
	}
	if !bytes.Equal(again.Module, artifact.Module) {
		t.Error("the restored artifact holds different bytes")
	}
}

// A stored artifact that does not match its own digest is a miss, not a
// crash — and it is removed, so the next run rebuilds instead of retrying the
// same corruption forever.
func TestACorruptOrTruncatedArtifactIsAMissAndIsRemoved(t *testing.T) {
	t.Setenv("PATH", "")

	dir := t.TempDir()
	artifact := minimalArtifact(emptyItems)
	cache, err := runcode.NewDiskCache(dir, runcode.RuntimeVersion, 0)
	if err != nil {
		t.Fatalf("NewDiskCache() error = %v", err)
	}
	cache.Put(artifact)

	modulePath, metaPath := artifactPaths(dir, runcode.RuntimeVersion, artifact.Hash)
	if err := os.WriteFile(modulePath, artifact.Module[:len(artifact.Module)/2], 0o600); err != nil {
		t.Fatal(err)
	}

	restarted, err := runcode.NewDiskCache(dir, runcode.RuntimeVersion, 0)
	if err != nil {
		t.Fatalf("NewDiskCache() error = %v", err)
	}
	if _, found := restarted.Get(artifact.Hash); found {
		t.Error("a truncated artifact was served from the cache")
	}
	for _, path := range []string{modulePath, metaPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s survived a corrupt entry, want it removed (stat error = %v)", path, err)
		}
	}
}

// A hash reaches the cache from user-controlled source, so it is validated
// rather than joined onto a path: ../x would otherwise write outside the
// directory the deployment decided to trust.
func TestADiskCacheRefusesAHashThatIsNotAHexDigest(t *testing.T) {
	t.Setenv("PATH", "")

	dir := t.TempDir()
	cache, err := runcode.NewDiskCache(dir, runcode.RuntimeVersion, 0)
	if err != nil {
		t.Fatalf("NewDiskCache() error = %v", err)
	}
	module := wasmtest.MinimalModule(emptyItems)

	for _, hash := range []string{"../x", "", strings.ToUpper(runcode.SourceHash("upper"))} {
		cache.Put(runcode.Artifact{Hash: hash, RuntimeVersion: runcode.RuntimeVersion, Module: module})
		if _, found := cache.Get(hash); found {
			t.Errorf("Get(%q) found an artifact, want a miss", hash)
		}
	}

	if _, err := os.Stat(filepath.Join(dir, "artifacts", "x.wasm")); !os.IsNotExist(err) {
		t.Errorf("a rejected hash wrote outside the version directory (stat error = %v)", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "artifacts", runcode.RuntimeVersion))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read artifacts directory: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("the artifacts directory holds %d files after rejected writes, want none", len(entries))
	}
}

// Put is called from execution paths that can overlap, and a reader must never
// see a module file without the meta that describes it.
func TestConcurrentPutsOfOneArtifactNeverExposeAPartialFile(t *testing.T) {
	t.Setenv("PATH", "")

	dir := t.TempDir()
	cache, err := runcode.NewDiskCache(dir, runcode.RuntimeVersion, 0)
	if err != nil {
		t.Fatalf("NewDiskCache() error = %v", err)
	}
	artifact := minimalArtifact(emptyItems)

	var (
		wait     sync.WaitGroup
		failures = make(chan string, 32)
	)
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			cache.Put(artifact)
			loaded, found := cache.Get(artifact.Hash)
			if !found {
				failures <- "Get() missed an artifact that was just written"
				return
			}
			if !bytes.Equal(loaded.Module, artifact.Module) {
				failures <- "Get() returned a partial module"
			}
		}()
	}
	wait.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}

	// Nothing may be left behind as a temp file, and a fresh reader must see
	// the complete pair.
	entries, err := os.ReadDir(filepath.Join(dir, "artifacts", runcode.RuntimeVersion))
	if err != nil {
		t.Fatalf("read artifacts directory: %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Errorf("a temporary file survived concurrent writes: %s", entry.Name())
		}
	}
	if len(entries) != 2 {
		t.Errorf("the artifacts directory holds %d files, want the module and its meta", len(entries))
	}
	restarted, err := runcode.NewDiskCache(dir, runcode.RuntimeVersion, 0)
	if err != nil {
		t.Fatalf("NewDiskCache() error = %v", err)
	}
	if loaded, found := restarted.Get(artifact.Hash); !found || !bytes.Equal(loaded.Module, artifact.Module) {
		t.Error("a reader after the writes did not see the complete artifact")
	}
}

// The budget is a soft cap on a directory that would otherwise grow with every
// distinct Code body ever run, and its order is the interesting part:
// translations are recompiled from an artifact with no toolchain, so they go
// before the artifacts, which cannot be rebuilt at all.
func TestTheCacheEvictsTranslationsBeforeArtifactsPastItsBudget(t *testing.T) {
	t.Setenv("PATH", "")

	version := runcode.RuntimeVersion

	// The eviction threshold is a fraction of the budget, so the sizes below
	// are chosen so that the arithmetic lands on exactly one eviction step:
	// translations alone, or translations plus the oldest artifact.
	const translationSize = 750

	measure := func(t *testing.T) int {
		t.Helper()
		probeDir := t.TempDir()
		probe, err := runcode.NewDiskCache(probeDir, version, 0)
		if err != nil {
			t.Fatalf("NewDiskCache() error = %v", err)
		}
		probe.Put(minimalArtifact(emptyItems))
		return directorySize(t, filepath.Join(probeDir, "artifacts", version))
	}

	seed := func(t *testing.T, dir string, oldestBytes, newerBytes int) {
		t.Helper()
		writeSeededFile(t, filepath.Join(dir, "artifacts", version, "oldest.wasm"), oldestBytes/2, time.Now().Add(-3*time.Hour))
		writeSeededFile(t, filepath.Join(dir, "artifacts", version, "oldest.json"), oldestBytes-oldestBytes/2, time.Now().Add(-3*time.Hour))
		writeSeededFile(t, filepath.Join(dir, "artifacts", version, "newer.wasm"), newerBytes/2, time.Now().Add(-2*time.Hour))
		writeSeededFile(t, filepath.Join(dir, "artifacts", version, "newer.json"), newerBytes-newerBytes/2, time.Now().Add(-2*time.Hour))
		writeSeededFile(t, filepath.Join(dir, "translations", version, "wazero-probe", "a"), translationSize, time.Now().Add(-2*time.Hour))
		writeSeededFile(t, filepath.Join(dir, "translations", version, "wazero-probe", "b"), translationSize, time.Now().Add(-1*time.Hour))
	}

	t.Run("translations go first", func(t *testing.T) {
		size := measure(t)
		if size > 900 {
			t.Fatalf("a stored artifact is %d bytes; the arithmetic below assumes under 900", size)
		}
		dir := t.TempDir()
		seed(t, dir, 5000, 3500)
		budget := int64(2*translationSize + 8500 + size - 1)
		cache, err := runcode.NewDiskCache(dir, version, budget)
		if err != nil {
			t.Fatalf("NewDiskCache() error = %v", err)
		}
		cache.Put(minimalArtifact(emptyItems))

		if files := translationFiles(t, dir, version); len(files) != 0 {
			t.Errorf("translations survived a budget eviction: %v", files)
		}
		for _, name := range []string{"oldest.wasm", "oldest.json", "newer.wasm", "newer.json"} {
			if _, err := os.Stat(filepath.Join(dir, "artifacts", version, name)); err != nil {
				t.Errorf("an artifact was evicted while translations were still cheaper to lose: %s", name)
			}
		}
		if total := int64(directorySize(t, dir)); total > budget {
			t.Errorf("the cache holds %d bytes against a budget of %d", total, budget)
		}
	})

	t.Run("artifacts go oldest first", func(t *testing.T) {
		size := measure(t)
		if size > 900 {
			t.Fatalf("a stored artifact is %d bytes; the arithmetic below assumes under 900", size)
		}
		dir := t.TempDir()
		seed(t, dir, 4000, 15000)
		budget := int64(2*translationSize + 19000 + size - 1)
		cache, err := runcode.NewDiskCache(dir, version, budget)
		if err != nil {
			t.Fatalf("NewDiskCache() error = %v", err)
		}
		cache.Put(minimalArtifact(emptyItems))

		if files := translationFiles(t, dir, version); len(files) != 0 {
			t.Errorf("translations survived a budget eviction: %v", files)
		}
		if _, err := os.Stat(filepath.Join(dir, "artifacts", version, "oldest.wasm")); !os.IsNotExist(err) {
			t.Errorf("the oldest artifact survived while a newer one was kept (stat error = %v)", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "artifacts", version, "newer.wasm")); err != nil {
			t.Errorf("the newer artifact was evicted before the older one: %v", err)
		}
		justWritten, _ := artifactPaths(dir, version, minimalArtifact(emptyItems).Hash)
		if _, err := os.Stat(justWritten); err != nil {
			t.Errorf("the artifact just written was evicted: %v", err)
		}
		if total := int64(directorySize(t, dir)); total > budget {
			t.Errorf("the cache holds %d bytes against a budget of %d", total, budget)
		}
	})
}

// Zero means unbounded, which is what an operator who has not thought about
// the cache must get: an eviction nobody asked for is a rebuild a
// toolchain-free deployment cannot perform.
func TestAZeroBudgetNeverEvicts(t *testing.T) {
	t.Setenv("PATH", "")

	dir := t.TempDir()
	version := runcode.RuntimeVersion
	writeSeededFile(t, filepath.Join(dir, "translations", version, "wazero-probe", "a"), 4096, time.Now())

	cache, err := runcode.NewDiskCache(dir, version, 0)
	if err != nil {
		t.Fatalf("NewDiskCache() error = %v", err)
	}
	cache.Put(minimalArtifact(emptyItems))

	if files := translationFiles(t, dir, version); len(files) != 1 {
		t.Errorf("a zero budget evicted %d translation files, want the file untouched", 1-len(files))
	}
}

// A cache directory is reused across host versions, and a translation written
// by an older wazero and an older RuntimeVersion is dead weight that would
// otherwise accumulate for the life of the installation.
func TestStaleVersionDirectoriesArePrunedAndTheCurrentOneIsKept(t *testing.T) {
	t.Setenv("PATH", "")

	dir := t.TempDir()
	version := runcode.RuntimeVersion
	for _, path := range []string{
		filepath.Join(dir, "artifacts", "wasip1-v0", "old.wasm"),
		filepath.Join(dir, "translations", "wasip1-v0", "wazero-old", "entry"),
		filepath.Join(dir, "artifacts", version, "current.wasm"),
		filepath.Join(dir, "translations", version, "wazero-current", "entry"),
		filepath.Join(dir, "go-build", "keep"),
		filepath.Join(dir, "unrelated.txt"),
	} {
		writeSeededFile(t, path, 16, time.Now())
	}

	removed, err := runcode.PruneStaleVersions(dir, version)
	if err != nil {
		t.Fatalf("PruneStaleVersions() error = %v", err)
	}
	want := []string{
		filepath.Join(dir, "artifacts", "wasip1-v0"),
		filepath.Join(dir, "translations", "wasip1-v0"),
	}
	sort.Strings(removed)
	if strings.Join(removed, ",") != strings.Join(want, ",") {
		t.Errorf("PruneStaleVersions() removed %v, want %v", removed, want)
	}
	for _, path := range []string{
		filepath.Join(dir, "artifacts", version, "current.wasm"),
		filepath.Join(dir, "translations", version, "wazero-current", "entry"),
		filepath.Join(dir, "go-build", "keep"),
		filepath.Join(dir, "unrelated.txt"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s did not survive the prune: %v", path, err)
		}
	}
}

// newPersistentModuleCache opens the on-disk translation cache and closes it
// when the test ends.
func newPersistentModuleCache(t *testing.T, dir, version string) *runcode.ModuleCache {
	t.Helper()
	cache, err := runcode.NewPersistentModuleCache(dir, version)
	if err != nil {
		t.Fatalf("NewPersistentModuleCache() error = %v", err)
	}
	t.Cleanup(func() {
		if err := cache.Close(context.Background()); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return cache
}

// translationModTimes maps each translation file to its modification time.
func translationModTimes(t *testing.T, dir, version string) map[string]time.Time {
	t.Helper()
	times := map[string]time.Time{}
	for _, path := range translationFiles(t, dir, version) {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		times[path] = info.ModTime()
	}
	return times
}

func writeSeededFile(t *testing.T, path string, size int, modified time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), size), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
}

// directorySize totals the regular files under a directory.
func directorySize(t *testing.T, dir string) int {
	t.Helper()
	total := 0
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += int(info.Size())
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return total
}
