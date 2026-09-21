package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sidecarnode"
	"github.com/kilaslab/kilas-flow/nodes"
	"github.com/kilaslab/kilas-flow/sidecar"
)

// sidecarDeps is everything the JavaScript sidecar needs from the composition
// root: the three catalogues it registers into, the egress policy its
// packages reach the network through, and the logger its children and
// exclusions report to.
type sidecarDeps struct {
	Definitions *node.Registry
	Executors   *engine.Registry
	Credentials *credentials.Registry
	Policy      safehttp.Policy
	Log         *slog.Logger
	// checkNode verifies the operator's Node installation. Nil means
	// sidecar.CheckNode; a test substitutes one to control the verdict.
	checkNode func(path string) (string, error)
}

// sidecarNodeVerdictTTL bounds how long a failed Node probe is believed.
//
// The availability report runs on every catalogue request, and CheckNode
// spawns `node --version` (with a five-second cap), so probing per request
// would put a process spawn behind a dropdown. A positive verdict needs no
// re-probe at all: the stat in nodeStatus already notices the binary going
// away. A negative one expires so a Node that is installed or upgraded is
// picked up without a restart.
const sidecarNodeVerdictTTL = 30 * time.Second

// sidecarIdleSweepFloor is the slowest the idle sweep runs, however short the
// configured idle timeout is.
const sidecarIdleSweepFloor = 10 * time.Second

// sidecarRuntime is a booted JavaScript sidecar: the pool of per-tenant
// processes, the loaded community nodes, and the cheap answer to "can this
// deployment run them right now".
type sidecarRuntime struct {
	pool        *sidecar.Pool
	index       *sidecarnode.Index
	definitions *node.Registry
	log         *slog.Logger
	cancel      context.CancelFunc
	children    *childTracker

	nodePath    string
	idleTimeout time.Duration
	checkNode   func(string) (string, error)
	verdictMu   sync.Mutex
	verdict     string
	verdictAt   time.Time
	verdictTTL  time.Duration
	now         func() time.Time
}

// setupSidecar boots the JavaScript sidecar.
//
// It returns (nil, nil) — no runtime, no error — while sidecar.enabled is
// false. That is not a convenience: it is what keeps a default install
// byte-for-byte what it was before this section existed. Nothing looks for
// Node on PATH, no runtime directory is created, and no process is started.
//
// When it is enabled, the community packages are loaded here, at composition,
// before the registry is shared and before any goroutine reads
// credentials.Default(): a package's credential types register into that
// global catalogue, and composition is the one moment that is safe.
func setupSidecar(ctx context.Context, cfg config.Sidecar, deps sidecarDeps) (*sidecarRuntime, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	log := deps.Log
	if log == nil {
		log = slog.Default()
	}
	checkNode := deps.checkNode
	if checkNode == nil {
		checkNode = sidecar.CheckNode
	}

	nodePath, err := resolveSidecarNode(cfg.NodePath)
	if err != nil {
		return nil, err
	}
	version, err := checkNode(nodePath)
	if err != nil {
		return nil, fmt.Errorf("the JavaScript sidecar needs Node 24 LTS (set sidecar.node_path, or install Node and put it on PATH): %w", err)
	}

	packagesDir, err := resolveSidecarPackagesDir(cfg.PackagesDir)
	if err != nil {
		return nil, fmt.Errorf("sidecar.packages_dir: %w", err)
	}
	runtimeDir, err := filepath.Abs(cfg.RuntimeDir)
	if err != nil {
		return nil, fmt.Errorf("sidecar.runtime_dir: %w", err)
	}
	runnerPath, err := sidecar.ExtractRunner(runtimeDir)
	if err != nil {
		return nil, fmt.Errorf("extract the sidecar runner: %w", err)
	}

	limits := sidecarLimits(cfg)
	children := newChildTracker()
	spawn := children.wrap(sidecar.RunnerSpawn(sidecar.RunnerConfig{
		NodePath:    nodePath,
		RunnerPath:  runnerPath,
		PackagesDir: packagesDir,
		Packages:    cfg.Packages,
		RuntimeDir:  runtimeDir,
		Wrapper:     cfg.Wrapper,
		HeapMB:      cfg.MaxHeapMB,
		MaxRSSMB:    cfg.MaxRSSMB,
		Diag:        sidecar.NewLogWriter(log, "sidecar"),
	}))
	pool := sidecar.NewPool(spawn, limits)

	// The load runs in the child, under the same permission model and limits
	// as a real run. A fatal package error or a real catalogue collision
	// refuses the boot naming the package; a single node file that fails to
	// load is a Warn and that node is excluded.
	index, err := sidecarnode.Load(ctx, sidecarnode.LoadDeps{
		Spawn:          spawn,
		Limits:         limits,
		Definitions:    deps.Definitions,
		Credentials:    deps.Credentials,
		SharedSettings: nodes.SidecarSharedSettings(),
		Log:            log,
	})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("load the JavaScript sidecar's community packages: %w", err)
	}
	if index.Len() == 0 {
		log.Warn("the JavaScript sidecar loaded no community nodes; check that sidecar.packages names the packages and that sidecar.packages_dir holds them",
			"packages", cfg.Packages)
	}
	if err := nodes.RegisterSidecarExecutor(deps.Executors, pool, index, deps.Definitions, deps.Policy, log); err != nil {
		pool.Close()
		return nil, fmt.Errorf("register the JavaScript sidecar executor: %w", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	runtime := &sidecarRuntime{
		pool:        pool,
		index:       index,
		definitions: deps.Definitions,
		log:         log,
		cancel:      cancel,
		children:    children,
		nodePath:    nodePath,
		idleTimeout: cfg.IdleTimeout,
		checkNode:   checkNode,
		verdictTTL:  sidecarNodeVerdictTTL,
		now:         time.Now,
	}
	go runtime.sweep(runCtx)

	log.Info("JavaScript sidecar started",
		"node", version, "packages", cfg.Packages, "nodes", index.Len(), "excluded", len(index.Exclusions()))
	return runtime, nil
}

// sweep reaps idle tenant processes until the context ends. The interval
// tracks the configured idle timeout so a short timeout is not rounded up to
// the floor, and never polls faster than the floor.
func (runtime *sidecarRuntime) sweep(ctx context.Context) {
	interval := runtime.idleSweepInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			runtime.pool.CloseIdle(now)
		}
	}
}

func (runtime *sidecarRuntime) idleSweepInterval() time.Duration {
	interval := runtime.idleTimeout / 4
	if interval < sidecarIdleSweepFloor {
		return sidecarIdleSweepFloor
	}
	return interval
}

// Availability reports every sidecar-tagged node type this deployment cannot
// run, keyed the same way the node catalogue keys its entries. It is the
// composition root's answer to the API's per-request availability hook, so it
// must stay cheap: a stat, plus a cached probe that only runs when a previous
// one failed.
func (runtime *sidecarRuntime) Availability() map[string]string {
	reason := runtime.nodeStatus()
	if reason == "" {
		return nil
	}
	report := map[string]string{}
	for _, definition := range runtime.definitions.List() {
		if definition.Source == node.SourceSidecar {
			report[definition.Type] = "the JavaScript sidecar cannot start: " + reason
		}
	}
	return report
}

// nodeStatus returns "" while the sidecar's Node looks usable, or the reason
// it does not. The stat notices the common outage — Node removed from the
// image — without spawning anything; the cached CheckNode verdict covers a
// binary that is present but unusable, and expires so recovery is picked up.
func (runtime *sidecarRuntime) nodeStatus() string {
	if _, err := os.Stat(runtime.nodePath); err != nil {
		return "the Node binary " + runtime.nodePath + " is not present"
	}
	runtime.verdictMu.Lock()
	defer runtime.verdictMu.Unlock()
	if runtime.verdict == "" {
		return ""
	}
	now := runtime.now()
	if now.Sub(runtime.verdictAt) < runtime.verdictTTL {
		return runtime.verdict
	}
	if _, err := runtime.checkNode(runtime.nodePath); err != nil {
		runtime.verdict = err.Error()
	} else {
		runtime.verdict = ""
	}
	runtime.verdictAt = now
	return runtime.verdict
}

// Close stops every tenant process and the idle sweep. It is deferred in the
// composition root, so it runs after the workers have drained: a child is
// stopped only once nothing can be running in it.
func (runtime *sidecarRuntime) Close() {
	if runtime == nil {
		return
	}
	if runtime.cancel != nil {
		runtime.cancel()
	}
	if pids := runtime.children.all(); len(pids) > 0 {
		runtime.log.Debug("stopping JavaScript sidecar processes", "pids", pids)
	}
	runtime.pool.Close()
}

// EvictTenant stops the tenant's sidecar process, for the tenant-deletion path
// to call once something owns it. A tenant with no process is a no-op.
func (runtime *sidecarRuntime) EvictTenant(tenant string) {
	if runtime == nil || strings.TrimSpace(tenant) == "" {
		return
	}
	if pids := runtime.children.forTenant(tenant); len(pids) > 0 {
		runtime.log.Info("evicting a deleted tenant's JavaScript sidecar process", "tenant", tenant, "pids", pids)
	}
	runtime.pool.Evict(tenant)
}

// mergeAvailability combines the deployment's own availability reports. A nil
// report contributes nothing, so a declined sidecar changes nothing, and an
// empty merge stays nil so the catalogue carries no empty `unavailable` map.
func mergeAvailability(reports ...func() map[string]string) func() map[string]string {
	return func() map[string]string {
		var merged map[string]string
		for _, report := range reports {
			if report == nil {
				continue
			}
			for nodeType, reason := range report() {
				if reason == "" {
					continue
				}
				if merged == nil {
					merged = map[string]string{}
				}
				merged[nodeType] = reason
			}
		}
		return merged
	}
}

// childTracker remembers the process id of every child the pool started.
//
// The pool keeps no pid list, and an operator diagnosing a process this host
// could not reap — a SIGKILLed host can leave a busy child behind — needs the
// pid. It is also what proves shutdown and eviction stop the processes they
// say they stopped.
type childTracker struct {
	mu       sync.Mutex
	byTenant map[string][]int
}

func newChildTracker() *childTracker {
	return &childTracker{byTenant: map[string][]int{}}
}

func (tracker *childTracker) wrap(next sidecar.SpawnFunc) sidecar.SpawnFunc {
	return func(ctx context.Context, tenant, socketPath string) (*sidecar.Child, error) {
		child, err := next(ctx, tenant, socketPath)
		if err == nil && child != nil {
			tracker.mu.Lock()
			tracker.byTenant[tenant] = append(tracker.byTenant[tenant], child.PID)
			tracker.mu.Unlock()
		}
		return child, err
	}
}

func (tracker *childTracker) forTenant(tenant string) []int {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return append([]int(nil), tracker.byTenant[tenant]...)
}

func (tracker *childTracker) all() []int {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	var pids []int
	for _, tenantPIDs := range tracker.byTenant {
		pids = append(pids, tenantPIDs...)
	}
	return pids
}

// resolveSidecarNode resolves the operator's Node binary. Empty means PATH,
// which is resolved once here so the availability stat has a real path to
// check later.
func resolveSidecarNode(configured string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		found, err := exec.LookPath("node")
		if err != nil {
			return "", fmt.Errorf("sidecar.enabled is true but no node binary is on PATH (set sidecar.node_path to name one): %w", err)
		}
		return found, nil
	}
	resolved, err := filepath.Abs(configured)
	if err != nil {
		return "", fmt.Errorf("sidecar.node_path %q: %w", configured, err)
	}
	return resolved, nil
}

// resolveSidecarPackagesDir canonicalises the packages directory. The read
// grant Node's permission model receives has to be the resolved path: Node
// refuses to start when a granted path traverses a symbolic link, and on macOS
// the platform temp directory is one.
func resolveSidecarPackagesDir(configured string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return "", fmt.Errorf("no packages directory is configured")
	}
	abs, err := filepath.Abs(configured)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("the packages directory %s is not readable: %w", abs, err)
	}
	return resolved, nil
}

// sidecarLimits maps the configuration onto the pool's own bounds. The frame,
// host-call and catalogue limits keep their defaults: they are protocol
// guardrails rather than deployment decisions, and normalizeLimits raises the
// frame bound above the output bound so the output bound stays reachable.
func sidecarLimits(cfg config.Sidecar) sidecar.Limits {
	limits := sidecar.DefaultLimits()
	limits.Timeout = cfg.Timeout
	limits.SpawnTimeout = cfg.SpawnTimeout
	limits.IdleTimeout = cfg.IdleTimeout
	limits.NodeMaxHeapMB = cfg.MaxHeapMB
	limits.MaxRSSMB = cfg.MaxRSSMB
	limits.MaxProcesses = cfg.MaxProcesses
	limits.MaxOutputBytes = cfg.MaxOutputBytes
	return limits
}
