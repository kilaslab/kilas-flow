package nodepack

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

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
	// Algorithm is the digest: sha256 or sha512.
	Algorithm string `json:"algorithm"`
	// SecretParameter names the node parameter holding the shared secret. An
	// empty secret means the node is not configured for verification, which is
	// a different thing from a failed check.
	SecretParameter string `json:"secretParameter"`
	// SecretCapture names, instead, a value the lifecycle's `set` captured: the
	// secret a service generated when the webhook was registered, and signs
	// every delivery with from then on. While the node's registration is on,
	// nothing captured yet refuses every delivery; with it off, nothing is ever
	// captured, and that is the same as no secret configured.
	SecretCapture string `json:"secretCapture,omitempty"`
	// SecretCredential names, instead of either, a field of a credential the
	// node carries: the secret never enters the workflow document at all,
	// which is the storage rule a `secretParameter` breaks. Resolved per
	// delivery, so a rotated credential is picked up without reactivation.
	SecretCredential *TriggerSecretCredential `json:"secretCredential,omitempty"`
	// Encoding is how the sender writes the digest: hex (the default) or
	// base64.
	Encoding string `json:"encoding,omitempty"`
	// Prefix stands between the version marker and the digest in the header,
	// as GitHub's `sha256=` or Standard Webhooks' `v1,` do.
	Prefix string `json:"prefix,omitempty"`
	// SigningString is what is signed, as a `{{ .body }}`-style template over
	// the raw body, the timestamp header and any request header. Empty is the
	// raw body alone.
	SigningString string `json:"signingString,omitempty"`
	// TimestampHeader names the header the signing string's timestamp comes
	// from, and the header a tolerance is judged against.
	TimestampHeader string `json:"timestampHeader,omitempty"`
	// ToleranceSeconds is how old (or far ahead) that timestamp may be. Zero
	// means no staleness check.
	ToleranceSeconds int `json:"toleranceSeconds,omitempty"`
	// Preset fills every scheme field above from a named scheme, so a pack
	// for a service that follows it declares one word and its secret. The
	// only preset is `standardWebhooks`.
	Preset string `json:"preset,omitempty"`
}

// TriggerSecretCredential is a signing secret read out of a credential.
type TriggerSecretCredential struct {
	// Type is the credential type, spelled as the pack's own
	// `credentialType` is — the node must carry one of that type for the
	// delivery to be verified.
	Type string `json:"type"`
	// Field is the credential field holding the secret.
	Field string `json:"field"`
}

// StandardWebhooksPreset is the scheme standardwebhooks.com specifies: a
// sha256 over `timestamp.body`, base64, behind a `v1,` prefix, with a
// unix-seconds timestamp judged against a five-minute tolerance. Several
// signatures may arrive comma-separated, which is the rotation story.
const StandardWebhooksPreset = "standardWebhooks"

// hmacPresets are the schemes a pack can name instead of spelling out.
var hmacPresets = map[string]TriggerHMAC{
	StandardWebhooksPreset: {
		Header:           "webhook-signature",
		Algorithm:        "sha256",
		Encoding:         "base64",
		Prefix:           "v1,",
		SigningString:    "{{ .timestamp }}.{{ .body }}",
		TimestampHeader:  "webhook-timestamp",
		ToleranceSeconds: 300,
	},
}

// resolved is the scheme a delivery is actually verified with: the preset's
// values, when one was named, and otherwise the block's own.
func (hmac *TriggerHMAC) resolved() TriggerHMAC {
	if hmac.Preset == "" {
		return *hmac
	}
	preset := hmacPresets[hmac.Preset]
	preset.SecretParameter = hmac.SecretParameter
	preset.SecretCapture = hmac.SecretCapture
	preset.SecretCredential = hmac.SecretCredential
	return preset
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

// requests is the lifecycle as the descriptor form that performs it.
func (lifecycle *TriggerLifecycle) requests() webhook.RequestLifecycle {
	return webhook.RequestLifecycle{Check: lifecycle.Check, Set: lifecycle.Set, Remove: lifecycle.Remove}
}

// captures reports whether the lifecycle's `set` keeps a value under key.
func (lifecycle *TriggerLifecycle) captures(key string) bool {
	if lifecycle == nil || lifecycle.Set == nil {
		return false
	}
	_, captured := lifecycle.Set.Capture[key]
	return captured
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
	if trigger.Lifecycle != nil {
		if err := trigger.Lifecycle.requests().Validate(); err != nil {
			return err
		}
	}
	if trigger.HMAC == nil {
		return nil
	}
	if err := trigger.HMAC.validate(); err != nil {
		return err
	}
	// A secret nothing captures is never there: the node would refuse every
	// delivery while its registration is on, and accept every one unsigned
	// while it is off — a pack that looks verified and never is.
	if trigger.HMAC.SecretCapture != "" && !trigger.Lifecycle.captures(trigger.HMAC.SecretCapture) {
		return fmt.Errorf("hmac.secretCapture is %q, which the lifecycle's set does not capture", trigger.HMAC.SecretCapture)
	}
	return nil
}

// validate checks the hmac block's own shape: the scheme fields, the preset's
// exclusivity, and the one-secret rule. The pack-level halves — that a
// secretCredential's type is the pack's own credentialType — live where the
// pack is known, in RegisterTrigger.
func (hmac *TriggerHMAC) validate() error {
	scheme := hmac.resolved()

	if hmac.Preset != "" {
		if _, known := hmacPresets[hmac.Preset]; !known {
			return fmt.Errorf("hmac preset %q is not one this build carries; the only preset is %s", hmac.Preset, StandardWebhooksPreset)
		}
		// A preset is the whole scheme in one word; a block that names one and
		// then also spells a scheme field is two schemes disagreeing, and the
		// one that runs is whichever half won.
		for _, own := range []struct {
			field  string
			set    bool
			setter string
		}{
			{"header", hmac.Header != "", "header"},
			{"algorithm", hmac.Algorithm != "", "algorithm"},
			{"encoding", hmac.Encoding != "", "encoding"},
			{"prefix", hmac.Prefix != "", "prefix"},
			{"signingString", hmac.SigningString != "", "signingString"},
			{"timestampHeader", hmac.TimestampHeader != "", "timestampHeader"},
			{"toleranceSeconds", hmac.ToleranceSeconds != 0, "toleranceSeconds"},
		} {
			if own.set {
				return fmt.Errorf("hmac names both the %s preset and %s; the preset is the whole scheme, so say only the secret", hmac.Preset, own.setter)
			}
		}
	} else {
		if scheme.Algorithm != "sha256" && scheme.Algorithm != "sha512" {
			return fmt.Errorf("hmac algorithm %q is not implemented; use sha256 or sha512", scheme.Algorithm)
		}
		if scheme.Encoding != "" && scheme.Encoding != "hex" && scheme.Encoding != "base64" {
			return fmt.Errorf("hmac encoding %q is not implemented; use hex or base64", scheme.Encoding)
		}
		if strings.TrimSpace(scheme.Header) == "" {
			return fmt.Errorf("hmac needs the header the signature arrives in")
		}
		if err := webhook.CheckSigningString(scheme.SigningString, scheme.TimestampHeader); err != nil {
			return fmt.Errorf("hmac signingString: %w", err)
		}
		if hmac.ToleranceSeconds < 0 {
			return fmt.Errorf("hmac toleranceSeconds cannot be negative")
		}
		if hmac.ToleranceSeconds > 0 && hmac.TimestampHeader == "" {
			return fmt.Errorf("hmac names a tolerance with no timestampHeader to judge")
		}
	}

	secrets := 0
	if hmac.SecretParameter != "" {
		secrets++
	}
	if hmac.SecretCapture != "" {
		secrets++
	}
	if hmac.SecretCredential != nil {
		secrets++
		if strings.TrimSpace(hmac.SecretCredential.Type) == "" || strings.TrimSpace(hmac.SecretCredential.Field) == "" {
			return fmt.Errorf("hmac.secretCredential names a type and a field, and needs both")
		}
	}
	if secrets > 1 {
		return fmt.Errorf("hmac names a secretParameter, a secretCapture and a secretCredential together: a delivery is verified with one secret")
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
		return fmt.Errorf("node %q: download media: %w", ir.Name, safehttp.RedactError(err))
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
	scheme := trigger.HMAC.resolved()
	verify := webhook.HMACSchemeVerifier(webhook.HMACScheme{
		Header:          scheme.Header,
		Algorithm:       scheme.Algorithm,
		Encoding:        scheme.Encoding,
		Prefix:          scheme.Prefix,
		SigningString:   scheme.SigningString,
		TimestampHeader: scheme.TimestampHeader,
		Tolerance:       time.Duration(scheme.ToleranceSeconds) * time.Second,
	}, func(delivery webhook.Delivery) (string, error) {
		return trigger.secretOf(delivery)
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
	//
	// A captured secret is the exception, while the node's registration is
	// on. Such a node will be signed for, and its secret is absent only in a
	// window — before its registration has answered, or after a remove cleared
	// it while the workflow stayed active — in which accepting unsigned
	// deliveries would let anybody who has the URL inject events. So it
	// refuses them until the secret is there.
	kind.Verify = func(delivery webhook.Delivery) error {
		secret, err := trigger.secretOf(delivery)
		if err != nil {
			// A credential the block names but cannot read is a configured
			// verification that failed, not one that is off.
			return err
		}
		if secret != "" {
			return verify(delivery)
		}
		if trigger.awaitsCapturedSecret(delivery) {
			return fmt.Errorf("this endpoint is signed with a secret its registration has not provided yet, so no delivery can be verified")
		}
		return nil
	}
	// The verifier above skips itself when the node holds no secret, so it
	// authenticates a delivery only while one is configured. Without this a
	// deployment with webhook.require_auth on would refuse every pack trigger
	// that never had a secret to check — the state every imported workflow
	// arrives in.
	kind.Verifies = func(delivery webhook.Delivery) bool {
		secret, err := trigger.secretOf(delivery)
		if err != nil {
			// The secret is configured but unreadable in this probe; the
			// deliveries themselves are refused, which is what
			// webhook.require_auth wants to hear about.
			return true
		}
		return secret != ""
	}
	return kind
}

// awaitsCapturedSecret reports whether a delivery's node verifies with a
// captured secret and has its registration on — so a secret is coming, and
// its absence is a refusal rather than "not configured". The same test the
// lifecycle gate applies, so the two cannot disagree about a node.
func (trigger *Trigger) awaitsCapturedSecret(delivery webhook.Delivery) bool {
	return trigger.HMAC.SecretCapture != "" && trigger.Lifecycle != nil &&
		webhook.LifecycleEnabled(delivery.Binding.Parameters, trigger.Lifecycle.EnabledParameter)
}

// secretOf is the secret a delivery is verified with: the field of the
// credential the node carries, when the trigger names one; the value the
// registration captured, when it names one of those; and otherwise the node's
// parameter. Never one as a fallback for another — a delivery is verified
// with one secret, whichever the block names.
//
// The credential path is per delivery on purpose: a credential rotated on the
// tenant's side is the secret the sender now signs with, and an activation
// boundary is not part of that story. A credential the block names but the
// node does not carry reads as no secret configured — the same as an empty
// parameter — while a credential that is carried but cannot be read is a
// refusal: that node said it verifies, and this delivery is not one it can.
func (trigger *Trigger) secretOf(delivery webhook.Delivery) (string, error) {
	if credential := trigger.HMAC.SecretCredential; credential != nil {
		references, _ := delivery.Binding.Parameters["$credentials"].(map[string]any)
		id, _ := references[credential.Type].(string)
		if strings.TrimSpace(id) == "" {
			return "", nil
		}
		fields, err := delivery.Fields(credential.Type)
		if err != nil {
			return "", fmt.Errorf("the %s credential this endpoint verifies with could not be read", credential.Type)
		}
		secret := strings.TrimSpace(fields[credential.Field])
		if secret == "" {
			return "", fmt.Errorf("the %s credential has no %s field to verify with", credential.Type, credential.Field)
		}
		return secret, nil
	}
	if trigger.HMAC.SecretCapture != "" {
		return strings.TrimSpace(delivery.Binding.Captured[trigger.HMAC.SecretCapture]), nil
	}
	secret, _ := delivery.Binding.Parameters[trigger.HMAC.SecretParameter].(string)
	return strings.TrimSpace(secret), nil
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
	// The secret a `secretCredential` names is read out of the node's own
	// credential, and a binding carries references for exactly the types the
	// pack's definition requires — so a type the pack does not declare is a
	// secret no delivery can ever resolve, and a pack that looks verified and
	// never is.
	if credential := trigger.HMAC.SecretCredential; credential != nil {
		if pack.CredentialType == "" {
			return fmt.Errorf("node pack %q: hmac.secretCredential reads a credential field, but the pack declares no credentialType", pack.Type)
		}
		if credential.Type != pack.CredentialType {
			return fmt.Errorf("node pack %q: hmac.secretCredential names type %q, but the pack's credentialType is %q", pack.Type, credential.Type, pack.CredentialType)
		}
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
	hook := trigger.Lifecycle.requests()
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
