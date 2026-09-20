package nodepack

import (
	"bytes"
	"context"
	"crypto/sha512"
	"fmt"
	"hash"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/webhook"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// TriggerExecutorID is the binding a pack's webhook trigger names.
const TriggerExecutorID = "core.packTrigger"

// Trigger describes a webhook trigger node: one output per event.
//
// A service that delivers every event of a session to one URL and tells them
// apart by a field in the body becomes a fan-out node here, which is what lets
// an imported workflow keep its wiring. A connection in a workflow document is
// an output **index**, so the order of Events is the contract — not a
// convenience, and not something to sort.
type Trigger struct {
	// Events are the event names, in the order the source document lists them.
	Events []string `json:"events"`
	// CatchAll names the extra output an unrecognised event goes to. An event
	// this build has never heard of is a new event in a newer service, and
	// dropping it silently is how a workflow stops working after somebody
	// else's upgrade.
	CatchAll string `json:"catchAll"`
	// EventPath is the dotted path in the trigger item naming the event.
	EventPath string `json:"eventPath"`
	// Shape names how a delivery becomes the trigger item.
	Shape string `json:"shape"`
	// Webhook declares the inbound binding.
	Webhook *node.WebhookDeclaration `json:"webhook"`
	// HMAC, when set, verifies a signature over the raw request body before an
	// execution exists.
	HMAC *TriggerHMAC `json:"hmac,omitempty"`
	// Lifecycle is optional auto-registration with the remote service.
	Lifecycle *TriggerLifecycle `json:"lifecycle,omitempty"`
	// Media, when set, downloads the media a delivery links to and attaches it
	// as a binary reference.
	Media *TriggerMedia `json:"media,omitempty"`
	// Notice is shown after activation when auto-registration is off, with
	// `{{ url }}` replaced by the route the service must deliver to. Without
	// it an imported workflow looks correct and receives nothing.
	Notice string `json:"notice,omitempty"`
}

// TriggerHMAC describes signature verification.
type TriggerHMAC struct {
	Header string `json:"header"`
	// Algorithm is the digest. Only sha512 is implemented, which is what WAHA
	// signs with; anything else is refused at registration rather than
	// silently accepting every delivery.
	Algorithm string `json:"algorithm"`
	// SecretParameter names the node parameter holding the shared secret. An
	// empty secret means the node is not configured for verification, which is
	// a different thing from a failed check.
	SecretParameter string `json:"secretParameter"`
}

// TriggerLifecycle describes optional auto-registration.
type TriggerLifecycle struct {
	// ID is the opaque server-owned binding, like an executor's.
	ID string `json:"id"`
	// EnabledParameter names the boolean parameter that turns it on. It is a
	// parameter rather than a default because registering a webhook writes to
	// a customer's own instance, which is not a thing importing a workflow
	// should do silently.
	EnabledParameter string                     `json:"enabledParameter"`
	Check            *webhook.RequestDescriptor `json:"check,omitempty"`
	Set              *webhook.RequestDescriptor `json:"set,omitempty"`
	Remove           *webhook.RequestDescriptor `json:"remove,omitempty"`
}

type registryKey struct {
	nodeType string
	version  workflow.TypeVersion
}

// TriggerMedia describes how a delivery's media becomes an attachment.
//
// A webhook that links to media is a webhook whose payload expires: the URL is
// behind the sender's own auth, it is fetched twice if two nodes need it, and
// it is gone by the time anyone reads the execution record. Downloading it once,
// at the trigger, is what makes the media part of the run rather than a
// reference to something that used to exist.
type TriggerMedia struct {
	// EnabledParameter names the boolean that turns it on. Off by default: a
	// trigger that silently downloads every photo of a busy WhatsApp session
	// is a disk-usage surprise, not a feature.
	EnabledParameter string `json:"enabledParameter"`
	// URLPath, MimeTypePath and FileNamePath are dotted paths in the item.
	URLPath      string `json:"urlPath"`
	MimeTypePath string `json:"mimeTypePath,omitempty"`
	FileNamePath string `json:"fileNamePath,omitempty"`
	// Property is the binary property the reference is attached under.
	Property string `json:"property"`
}

// TriggerRegistry holds the event table of every registered trigger pack.
type TriggerRegistry struct {
	mu       sync.RWMutex
	triggers map[registryKey]*Trigger
}

// NewTriggerRegistry returns an empty registry.
func NewTriggerRegistry() *TriggerRegistry {
	return &TriggerRegistry{triggers: map[registryKey]*Trigger{}}
}

// Register stores one trigger's table.
func (registry *TriggerRegistry) Register(nodeType string, version workflow.TypeVersion, trigger *Trigger) error {
	if registry == nil || trigger == nil {
		return fmt.Errorf("a trigger description is required")
	}
	if err := trigger.validate(); err != nil {
		return fmt.Errorf("trigger for %s: %w", nodeType, err)
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	key := registryKey{nodeType: nodeType, version: version}
	if _, exists := registry.triggers[key]; exists {
		return fmt.Errorf("trigger for %s v%s is already registered", nodeType, version)
	}
	registry.triggers[key] = trigger
	return nil
}

// Lookup returns one trigger's table.
func (registry *TriggerRegistry) Lookup(nodeType string, version workflow.TypeVersion) (*Trigger, bool) {
	if registry == nil {
		return nil, false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	trigger, found := registry.triggers[registryKey{nodeType: nodeType, version: version}]
	return trigger, found
}

func (trigger *Trigger) validate() error {
	if len(trigger.Events) == 0 {
		return fmt.Errorf("a trigger needs at least one event")
	}
	seen := map[string]bool{}
	for _, event := range trigger.Events {
		if strings.TrimSpace(event) == "" {
			return fmt.Errorf("a trigger event needs a name")
		}
		if seen[event] {
			return fmt.Errorf("event %q is declared twice, which would make two ports mean the same thing", event)
		}
		seen[event] = true
	}
	if strings.TrimSpace(trigger.CatchAll) == "" {
		return fmt.Errorf("a trigger needs a catch-all output; an unrecognised event has to go somewhere")
	}
	if seen[trigger.CatchAll] {
		return fmt.Errorf("the catch-all output %q collides with a declared event", trigger.CatchAll)
	}
	if strings.TrimSpace(trigger.EventPath) == "" {
		return fmt.Errorf("a trigger needs the path of the field naming the event")
	}
	if trigger.Webhook == nil {
		return fmt.Errorf("a trigger needs a webhook declaration, or activation binds no route")
	}
	if trigger.HMAC != nil && trigger.HMAC.Algorithm != "sha512" {
		return fmt.Errorf("hmac algorithm %q is not implemented; use sha512", trigger.HMAC.Algorithm)
	}
	return nil
}

// Ports are the trigger's outputs, in index order: one per event, then the
// catch-all.
func (trigger *Trigger) Ports() []workflow.Port {
	ports := make([]workflow.Port, 0, len(trigger.Events)+1)
	for _, event := range trigger.Events {
		ports = append(ports, workflow.Port{Name: event, DisplayName: event, Kind: workflow.ConnectionMain})
	}
	return append(ports, workflow.Port{
		Name: trigger.CatchAll, DisplayName: "Other", Kind: workflow.ConnectionMain,
	})
}

// PortIndex is where an event's items go. An unrecognised event goes to the
// catch-all, which is the last port.
func (trigger *Trigger) PortIndex(event string) int {
	for index, declared := range trigger.Events {
		if declared == event {
			return index
		}
	}
	return len(trigger.Events)
}

// TriggerExecutor emits a delivery on the one port matching its event.
type TriggerExecutor struct {
	triggers *TriggerRegistry
	policy   safehttp.Policy
	client   *http.Client
}

// NewTriggerExecutor builds the fan-out executor.
//
// It takes the egress policy because a trigger that downloads media makes an
// outbound call, and it makes it the same way every other outbound call in this
// server is made.
func NewTriggerExecutor(triggers *TriggerRegistry, policy safehttp.Policy) *TriggerExecutor {
	return &TriggerExecutor{triggers: triggers, policy: policy, client: safehttp.NewClient(policy)}
}

// Execute routes the trigger item to its event's port and leaves the rest
// empty.
//
// Empty is what makes the fan-out real: the runner prunes a branch whose
// incoming port delivered nothing, so nineteen other branches do not run
// because one event arrived.
func (executor *TriggerExecutor) Execute(ctx context.Context, ir workflow.IRNode, _ workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	trigger, found := executor.triggers.Lookup(ir.Type, ir.TypeVersion)
	if !found {
		return nil, fmt.Errorf("node %q: no trigger description is registered for %s v%s", ir.Name, ir.Type, ir.TypeVersion)
	}

	output := make(workflow.NodeOutput, len(trigger.Events)+1)
	for index := range output {
		output[index] = []workflow.Item{}
	}
	item := request.Input
	if item.JSON == nil {
		item.JSON = map[string]any{}
	}
	if trigger.Media != nil {
		if err := executor.attachMedia(ctx, ir, trigger.Media, &item, request); err != nil {
			return nil, err
		}
	}

	event, _ := readPath(item.JSON, trigger.EventPath).(string)
	output[trigger.PortIndex(event)] = []workflow.Item{item}
	return output, nil
}

// attachMedia downloads the linked media and puts a reference on the item.
//
// A failure to download is a failure of the run rather than a silently
// media-less item: a workflow whose whole purpose is the photo should not
// proceed as though there wasn't one.
func (executor *TriggerExecutor) attachMedia(
	ctx context.Context,
	ir workflow.IRNode,
	media *TriggerMedia,
	item *workflow.Item,
	request engine.Request,
) error {
	if media.EnabledParameter != "" {
		on, _ := ir.Parameters[media.EnabledParameter].(bool)
		if !on {
			return nil
		}
	}
	link, _ := readPath(item.JSON, media.URLPath).(string)
	if strings.TrimSpace(link) == "" {
		// No media on this delivery. Most events carry none, so this is the
		// ordinary case rather than a problem.
		return nil
	}
	if request.Binaries == nil {
		return fmt.Errorf("node %q: binary storage is not configured on this server", ir.Name)
	}

	target, err := url.Parse(link)
	if err != nil {
		return fmt.Errorf("node %q: the delivery's media URL is unusable: %w", ir.Name, err)
	}
	// The URL comes from the sender, so it is checked before the dial as well
	// as at it — a webhook that could name an internal address would turn this
	// trigger into an SSRF gadget.
	if err := executor.policy.CheckURL(target); err != nil {
		return fmt.Errorf("node %q: %w", ir.Name, err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return fmt.Errorf("node %q: %w", ir.Name, err)
	}
	// Media on a self-hosted instance is usually behind the same API key.
	if err := request.Authenticate(ctx, ir, httpRequest); err != nil {
		return err
	}
	response, err := executor.client.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("node %q: download media: %w", ir.Name, err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return fmt.Errorf("node %q: downloading media answered %d", ir.Name, response.StatusCode)
	}
	contents, truncated, err := executor.policy.ReadBody(response.Body)
	if err != nil {
		return fmt.Errorf("node %q: download media: %w", ir.Name, err)
	}
	if truncated {
		return fmt.Errorf("node %q: the media exceeds the configured size limit", ir.Name)
	}

	name, _ := readPath(item.JSON, media.FileNamePath).(string)
	mimeType, _ := readPath(item.JSON, media.MimeTypePath).(string)
	if name == "" {
		name = path.Base(target.Path)
	}
	reference, err := request.Binaries.Put(name, mimeType, bytes.NewReader(contents))
	if err != nil {
		return fmt.Errorf("node %q: store media: %w", ir.Name, err)
	}
	// The reference, never the bytes. The payload does not enter the item, the
	// execution record or a log line.
	if item.Binary == nil {
		item.Binary = map[string]workflow.BinaryRef{}
	}
	property := media.Property
	if property == "" {
		property = "data"
	}
	item.Binary[property] = reference
	return nil
}

// readPath walks a dotted path into an item.
func readPath(value any, path string) any {
	current := any(value)
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[segment]
	}
	return current
}

// TriggerKind is how the HTTP boundary shapes and verifies this trigger's
// deliveries.
func (trigger *Trigger) TriggerKind() webhook.TriggerKind {
	kind := webhook.TriggerKind{Shape: webhook.Shape(trigger.Shape)}
	if trigger.HMAC == nil {
		return kind
	}
	verify := webhook.HMACVerifier(trigger.HMAC.Header,
		func() hash.Hash { return sha512.New() },
		func(delivery webhook.Delivery) (string, error) {
			return trigger.secretOf(delivery), nil
		})
	// Verification is per node, not per node type: a node with no secret
	// configured is not verified at all.
	//
	// That is a deliberate trade rather than an oversight. The endpoint is an
	// unguessable minted route, every imported workflow arrives with no secret
	// because the package it came from cannot verify one, and refusing those
	// would make importing a working workflow produce a broken one. A node that
	// *does* carry a secret is then held to it strictly — a missing or
	// malformed signature is a refusal, never a pass.
	kind.Verify = func(delivery webhook.Delivery) error {
		if trigger.secretOf(delivery) == "" {
			return nil
		}
		return verify(delivery)
	}
	// The verifier above skips itself when the node holds no secret, so it
	// authenticates a delivery only while one is configured. Without this a
	// deployment with webhook.require_auth on would refuse every pack trigger
	// that never had a secret to check — the state every imported workflow
	// arrives in.
	kind.Verifies = func(delivery webhook.Delivery) bool {
		return trigger.secretOf(delivery) != ""
	}
	return kind
}

func (trigger *Trigger) secretOf(delivery webhook.Delivery) string {
	secret, _ := delivery.Binding.Parameters[trigger.HMAC.SecretParameter].(string)
	return strings.TrimSpace(secret)
}

// RegisterTrigger installs a trigger pack's runtime halves.
//
// Four registries, one call, deliberately: the event table the executor routes
// with, the shape and verifier the HTTP boundary applies, and the lifecycle hook
// activation runs. A trigger missing any one of them registers cleanly and then
// misbehaves in a way that looks like a different bug — a delivery shaped as the
// wrong envelope, a signature never checked, a workflow that receives nothing.
func RegisterTrigger(
	triggers *TriggerRegistry,
	deliveries *webhook.Registry,
	lifecycles *webhook.LifecycleRegistry,
	pack *Pack,
) error {
	if pack == nil || pack.Trigger == nil {
		return fmt.Errorf("this pack declares no trigger")
	}
	if triggers == nil || deliveries == nil {
		return fmt.Errorf("node pack %q: a trigger needs both an event registry and a delivery registry", pack.Type)
	}
	trigger := pack.Trigger
	if err := triggers.Register(pack.Type, pack.Version, trigger); err != nil {
		return err
	}
	// The delivery shape is per node *type*, not per version: two versions of
	// one trigger receive the same envelope from the same service. The second
	// registration is therefore expected and is not an error.
	if err := deliveries.Register(pack.Type, trigger.TriggerKind()); err != nil && !strings.Contains(err.Error(), "already registered") {
		return err
	}
	if trigger.Lifecycle == nil {
		return nil
	}
	hook := webhook.RequestLifecycle{
		Check: trigger.Lifecycle.Check, Set: trigger.Lifecycle.Set, Remove: trigger.Lifecycle.Remove,
	}
	// Opt-in: the hook does nothing unless the node turned it on. Registering a
	// webhook writes to a customer's own instance, which is not a thing
	// importing and activating a workflow should do silently.
	gated := webhook.GatedLifecycle{
		EnabledParameter: trigger.Lifecycle.EnabledParameter,
		Lifecycle:        hook,
		Notice:           trigger.Notice,
	}
	if err := lifecycles.Register(trigger.Lifecycle.ID, gated); err != nil && !strings.Contains(err.Error(), "already registered") {
		return err
	}
	return nil
}
