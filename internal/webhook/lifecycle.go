package webhook

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
)

// LifecycleContext is what a trigger needs to register itself with a remote
// service.
type LifecycleContext struct {
	TenantID   string
	WorkflowID string
	// Binding is the trigger as activation resolved it, including the
	// parameters the node was configured with.
	Binding repository.WebhookBinding
	// PublicURL is the address the remote service should deliver to. It is the
	// whole point of the hook: a Telegram bot receives nothing until setWebhook
	// is called with one.
	PublicURL string
	// HTTP is the outbound policy. A hook reaches the network only through
	// safehttp, exactly as an executor does.
	HTTP safehttp.Policy
	// Credentials resolves this workflow's tenant's secrets and no others.
	Credentials engine.CredentialResolver
	Logger      *slog.Logger
	// State keeps what this route's registration answered with, for the calls
	// that come after it. Nil when the server has nowhere to keep it, and then
	// a trigger that captures refuses to register.
	State LifecycleStateStore
}

// LifecycleStateStore is one route's lifecycle state: the values a `set`
// captured from its answer.
//
// It lives on the route rather than the binding because bindings are deleted
// and re-inserted on every activation and deleted on deactivation, and the
// subscription a registration made outlives both: the `remove` that runs on
// deactivation, and the `check` that runs when the workflow is activated again,
// are both addressed by it.
type LifecycleStateStore interface {
	Load(context.Context) (map[string]string, error)
	Save(context.Context, map[string]string) error
	Clear(context.Context) error
}

// TriggerLifecycle registers and unregisters a trigger with a remote service.
//
// All three of n8n's methods are kept rather than collapsing to two, because
// Activate may be called on an already-active workflow: CheckExists is what
// makes that a re-check rather than a re-registration.
type TriggerLifecycle interface {
	CheckExists(context.Context, LifecycleContext) (bool, error)
	Create(context.Context, LifecycleContext) error
	Delete(context.Context, LifecycleContext) error
}

// LifecycleRegistry binds a lifecycle ID to its implementation, by the same
// opaque server-owned identifier pattern as an executor — so a trigger
// declaring a hook nobody registered fails at startup rather than at the first
// activation.
type LifecycleRegistry struct {
	hooks map[string]TriggerLifecycle
}

// NewLifecycleRegistry creates an empty lifecycle registry.
func NewLifecycleRegistry() *LifecycleRegistry {
	return &LifecycleRegistry{hooks: map[string]TriggerLifecycle{}}
}

// Register binds one lifecycle implementation during composition.
func (registry *LifecycleRegistry) Register(id string, hook TriggerLifecycle) error {
	if registry == nil || id == "" || hook == nil {
		return fmt.Errorf("webhook lifecycle ID and implementation are required")
	}
	if _, exists := registry.hooks[id]; exists {
		return fmt.Errorf("webhook lifecycle %q is already registered", id)
	}
	registry.hooks[id] = hook
	return nil
}

// Lookup finds a hook.
func (registry *LifecycleRegistry) Lookup(id string) (TriggerLifecycle, bool) {
	if registry == nil {
		return nil, false
	}
	hook, found := registry.hooks[id]
	return hook, found
}

// Registered lists the bound IDs, for the startup check below.
func (registry *LifecycleRegistry) Registered() []string {
	if registry == nil {
		return nil
	}
	ids := make([]string, 0, len(registry.hooks))
	for id := range registry.hooks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// LifecycleDeclarer is the slice of the node registry the startup check reads.
type LifecycleDeclarer interface {
	LifecycleIDs() []string
}

// VerifyLifecycleBindings fails at startup when a node declares a hook that was
// never registered.
//
// Discovering that at the first activation would mean a workflow that saves,
// activates, and silently never registers with the remote service — the exact
// failure this whole ticket exists to remove, reintroduced one level up.
func VerifyLifecycleBindings(declared []string, registry *LifecycleRegistry) error {
	for _, id := range declared {
		if _, found := registry.Lookup(id); !found {
			return fmt.Errorf("node declares webhook lifecycle %q, which is not registered", id)
		}
	}
	return nil
}

// Coordinator runs lifecycle hooks around activation.
//
// It runs them *outside* the activation transaction, deliberately. Binding sync
// happens inside one so there is never a window in which a workflow is active
// but unroutable; a remote HTTP call cannot join it. It can block for seconds
// against somebody else's API while holding a row lock, and it cannot be rolled
// back — un-calling setWebhook is another network call, not a rollback.
type Coordinator struct {
	hooks       *LifecycleRegistry
	routes      repository.WebhookRouteReader
	states      repository.WebhookRouteStateStore
	policy      safehttp.Policy
	credentials func(tenantID string) engine.CredentialResolver
	baseURL     string
	logger      *slog.Logger
}

// NewCoordinator builds the activation-time lifecycle runner.
func NewCoordinator(hooks *LifecycleRegistry, routes repository.WebhookRouteReader, policy safehttp.Policy, credentials func(string) engine.CredentialResolver, baseURL string, logger *slog.Logger) *Coordinator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Coordinator{hooks: hooks, routes: routes, policy: policy, credentials: credentials, baseURL: baseURL, logger: logger}
}

// WithState gives each hook its route's lifecycle state to keep captured
// values in. Without it a hook is given none, and a trigger that captures is
// refused before it registers rather than after.
func (coordinator *Coordinator) WithState(states repository.WebhookRouteStateStore) *Coordinator {
	coordinator.states = states
	return coordinator
}

// Activated registers every declaring trigger with its remote service.
//
// A failure is returned to the caller naming the trigger, and the caller
// deactivates: leaving a workflow active whose triggers half-registered is
// worse than leaving it inactive, because the user believes it is listening.
func (coordinator *Coordinator) Activated(ctx context.Context, tenantID, workflowID string, declared map[string]string) ([]Notice, error) {
	if coordinator == nil || coordinator.hooks == nil || coordinator.routes == nil {
		return nil, nil
	}
	bindings, err := coordinator.routes.WebhookRoutes(ctx, repository.TenantScope{ID: tenantID}, workflowID)
	if err != nil {
		return nil, fmt.Errorf("read webhook routes: %w", err)
	}
	notices := make([]Notice, 0, 1)
	for _, binding := range bindings {
		hook, lifecycle := coordinator.hookFor(binding, declared)
		if hook == nil {
			continue
		}
		context := coordinator.contextFor(tenantID, workflowID, binding)
		// Collected before registration is attempted, because the notice is
		// about the case where registration is deliberately not attempted.
		if source, tells := hook.(NoticeSource); tells {
			if message := source.ActivationNotice(context); message != "" {
				notices = append(notices, Notice{
					NodeID: binding.NodeID, NodeType: binding.NodeType, Message: message,
				})
			}
		}
		// CheckExists is what makes activating an already-active workflow a
		// re-check rather than a re-registration.
		if exists, err := hook.CheckExists(ctx, context); err == nil && exists {
			continue
		}
		if err := hook.Create(ctx, context); err != nil {
			return nil, fmt.Errorf("trigger %q could not register with its service (%s): %w", binding.NodeID, lifecycle, err)
		}
	}
	return notices, nil
}

// Deactivated unregisters every declaring trigger.
//
// A failure is logged and never fails the request. A user deactivating a
// workflow must not be blocked by somebody else's service being down, and a
// stale remote registration delivers to a route that no longer resolves — which
// is a 404, not a leak.
func (coordinator *Coordinator) Deactivated(ctx context.Context, tenantID, workflowID string, declared map[string]string) {
	if coordinator == nil || coordinator.hooks == nil || coordinator.routes == nil {
		return
	}
	bindings, err := coordinator.routes.WebhookRoutes(ctx, repository.TenantScope{ID: tenantID}, workflowID)
	if err != nil {
		coordinator.logger.Warn("could not read webhook routes to unregister triggers",
			"workflow", workflowID, "error", err)
		return
	}
	for _, binding := range bindings {
		hook, lifecycle := coordinator.hookFor(binding, declared)
		if hook == nil {
			continue
		}
		if err := hook.Delete(ctx, coordinator.contextFor(tenantID, workflowID, binding)); err != nil {
			coordinator.logger.Warn("a trigger could not unregister from its service",
				"workflow", workflowID, "node", binding.NodeID, "lifecycle", lifecycle, "error", err)
		}
	}
}

func (coordinator *Coordinator) hookFor(binding repository.WebhookBinding, declared map[string]string) (TriggerLifecycle, string) {
	lifecycle, declaredHook := declared[binding.NodeType]
	if !declaredHook || lifecycle == "" {
		return nil, ""
	}
	hook, found := coordinator.hooks.Lookup(lifecycle)
	if !found {
		return nil, ""
	}
	return hook, lifecycle
}

func (coordinator *Coordinator) contextFor(tenantID, workflowID string, binding repository.WebhookBinding) LifecycleContext {
	var resolver engine.CredentialResolver
	if coordinator.credentials != nil {
		resolver = coordinator.credentials(tenantID)
	}
	var state LifecycleStateStore
	if coordinator.states != nil {
		state = routeState{
			store: coordinator.states, tenant: repository.TenantScope{ID: tenantID}, route: binding.Route,
		}
	}
	return LifecycleContext{
		TenantID: tenantID, WorkflowID: workflowID, Binding: binding,
		PublicURL:   coordinator.baseURL + "/webhook/" + binding.Route,
		HTTP:        coordinator.policy,
		Credentials: resolver,
		Logger:      coordinator.logger,
		State:       state,
	}
}

// routeState is the state store narrowed to one tenant's route, so a hook can
// reach its own route's values and no other's.
type routeState struct {
	store  repository.WebhookRouteStateStore
	tenant repository.TenantScope
	route  string
}

func (state routeState) Load(ctx context.Context) (map[string]string, error) {
	return state.store.LifecycleState(ctx, state.tenant, state.route)
}

func (state routeState) Save(ctx context.Context, values map[string]string) error {
	return state.store.SaveLifecycleState(ctx, state.tenant, state.route, values)
}

func (state routeState) Clear(ctx context.Context) error {
	return state.store.ClearLifecycleState(ctx, state.tenant, state.route)
}

// Notice is something activation could not do and the user now has to.
//
// It exists because the failure it prevents is silent: a trigger whose service
// was never told where to deliver looks exactly like one that is listening. An
// error would be wrong — the workflow *is* active and the user may well paste
// the URL in by hand, which is how the n8n node works — so this is neither an
// error nor a log line, it is part of the activation answer.
type Notice struct {
	NodeID   string `json:"nodeId"`
	NodeType string `json:"nodeType"`
	Message  string `json:"message"`
}

// NoticeSource is a lifecycle that can say what it did not do.
type NoticeSource interface {
	ActivationNotice(LifecycleContext) string
}

// GatedLifecycle runs an inner lifecycle only when the node turned it on.
//
// Off by default is the whole point: registering a webhook writes to a
// customer's own instance, and importing a workflow and pressing Activate is
// not a thing that should quietly reconfigure somebody's WhatsApp gateway.
type GatedLifecycle struct {
	// EnabledParameter names the boolean parameter that turns it on.
	EnabledParameter string
	Lifecycle        TriggerLifecycle
	// Notice is shown when it is off, with `{{ url }}` replaced by the route.
	Notice string
}

var _ TriggerLifecycle = GatedLifecycle{}
var _ NoticeSource = GatedLifecycle{}

func (gated GatedLifecycle) enabled(lifecycleContext LifecycleContext) bool {
	if gated.EnabledParameter == "" {
		return true
	}
	on, _ := lifecycleContext.Binding.Parameters[gated.EnabledParameter].(bool)
	return on
}

// CheckExists reports "already registered" when the hook is off, which is what
// stops Create from running.
func (gated GatedLifecycle) CheckExists(ctx context.Context, lifecycleContext LifecycleContext) (bool, error) {
	if !gated.enabled(lifecycleContext) {
		return true, nil
	}
	return gated.Lifecycle.CheckExists(ctx, lifecycleContext)
}

func (gated GatedLifecycle) Create(ctx context.Context, lifecycleContext LifecycleContext) error {
	if !gated.enabled(lifecycleContext) {
		return nil
	}
	return gated.Lifecycle.Create(ctx, lifecycleContext)
}

func (gated GatedLifecycle) Delete(ctx context.Context, lifecycleContext LifecycleContext) error {
	if !gated.enabled(lifecycleContext) {
		return nil
	}
	return gated.Lifecycle.Delete(ctx, lifecycleContext)
}

// ActivationNotice names the URL the user has to paste, when nothing pasted it
// for them.
func (gated GatedLifecycle) ActivationNotice(lifecycleContext LifecycleContext) string {
	if gated.Notice == "" || gated.enabled(lifecycleContext) {
		return ""
	}
	return strings.ReplaceAll(gated.Notice, "{{ url }}", lifecycleContext.PublicURL)
}
