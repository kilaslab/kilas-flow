package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/webhook"
)

// TelegramLifecycle registers and unregisters a bot's webhook.
//
// It is Go rather than a request descriptor because the secret it sends is
// derived from the bot token and the route, and a descriptor is templates over
// credential fields — it cannot compute an HMAC. The trade is deliberate: a
// descriptor covers a service whose registration is one templated call, and
// Telegram's is not one of them.
type TelegramLifecycle struct {
	pollers *TelegramPollers
}

// NewTelegramLifecycle builds the hook. A nil poller supervisor leaves polling
// unavailable and says so at activation rather than pretending to start one.
func NewTelegramLifecycle(pollers *TelegramPollers) TelegramLifecycle {
	return TelegramLifecycle{pollers: pollers}
}

var _ webhook.TriggerLifecycle = TelegramLifecycle{}

// CheckExists reports whether Telegram already points at this route.
//
// It compares the registered URL, not merely whether one exists. A bot pointing
// somewhere else is not registered for *this* workflow, and treating it as
// though it were would leave the workflow silently unreachable — which, since
// Telegram allows one webhook per bot, is exactly what happens when two
// workflows share a credential.
func (lifecycle TelegramLifecycle) CheckExists(ctx context.Context, lifecycleContext webhook.LifecycleContext) (bool, error) {
	if telegramPolling(lifecycleContext) {
		return false, nil
	}
	bot, err := telegramCredentialFor(ctx, lifecycleContext)
	if err != nil {
		return false, err
	}
	body, err := telegramCall(ctx, lifecycleContext.HTTP, bot, "getWebhookInfo", nil)
	if err != nil {
		return false, err
	}
	var answer struct {
		Result struct {
			URL string `json:"url"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return false, nil
	}
	return answer.Result.URL == lifecycleContext.PublicURL, nil
}

// Create registers the webhook, or starts the poller.
func (lifecycle TelegramLifecycle) Create(ctx context.Context, lifecycleContext webhook.LifecycleContext) error {
	bot, err := telegramCredentialFor(ctx, lifecycleContext)
	if err != nil {
		return err
	}
	if telegramPolling(lifecycleContext) {
		if lifecycle.pollers == nil {
			return fmt.Errorf("this deployment cannot poll Telegram; use webhook delivery")
		}
		// Telegram refuses getUpdates while a webhook is set, and the resulting
		// error names neither cause nor cure — so the webhook is removed first,
		// every time, rather than only when we think one exists.
		if _, err := telegramCall(ctx, lifecycleContext.HTTP, bot, "deleteWebhook", nil); err != nil {
			return fmt.Errorf("could not clear the existing webhook before polling: %w", err)
		}
		return lifecycle.pollers.Start(lifecycleContext, bot.credential)
	}

	if !strings.HasPrefix(lifecycleContext.PublicURL, "https://") {
		// setWebhook accepts only HTTPS, and its own error for this is opaque.
		return fmt.Errorf(
			"Telegram delivers only to an HTTPS address, and this server's public URL is %q. Set a public HTTPS URL, or switch this trigger's Delivery to polling for local development.",
			lifecycleContext.PublicURL)
	}
	arguments := map[string]any{
		"url":          lifecycleContext.PublicURL,
		"secret_token": TelegramSecret(bot.token, lifecycleContext.Binding.Route),
	}
	if allowed := TelegramAllowedUpdates(lifecycleContext.Binding.Parameters); len(allowed) > 0 {
		arguments["allowed_updates"] = allowed
	}
	_, err = telegramCall(ctx, lifecycleContext.HTTP, bot, "setWebhook", arguments)
	return err
}

// Delete unregisters it, or stops the poller.
func (lifecycle TelegramLifecycle) Delete(ctx context.Context, lifecycleContext webhook.LifecycleContext) error {
	if lifecycle.pollers != nil {
		lifecycle.pollers.Stop(lifecycleContext.Binding.Route)
	}
	if telegramPolling(lifecycleContext) {
		return nil
	}
	bot, err := telegramCredentialFor(ctx, lifecycleContext)
	if err != nil {
		return err
	}
	_, err = telegramCall(ctx, lifecycleContext.HTTP, bot, "deleteWebhook", nil)
	return err
}

func telegramPolling(lifecycleContext webhook.LifecycleContext) bool {
	mode, _ := lifecycleContext.Binding.Parameters["delivery"].(string)
	return mode == "polling"
}

// telegramBot is what a Bot API call needs from its credential: the token and
// the address, and the credential itself, whose domain bound every call is
// checked against.
type telegramBot struct {
	token      string
	baseURL    string
	credential engine.Credential
}

// telegramCredentialFor resolves the trigger's Telegram credential.
//
// The type is checked as the engine checks a node's credential: a credential
// of another type bound here would have its secret sent to the Bot API as a
// token.
func telegramCredentialFor(ctx context.Context, lifecycleContext webhook.LifecycleContext) (telegramBot, error) {
	if lifecycleContext.Credentials == nil {
		return telegramBot{}, fmt.Errorf("credentials are not available to this trigger")
	}
	references, _ := lifecycleContext.Binding.Parameters["$credentials"].(map[string]any)
	id, _ := references[TelegramCredentialType].(string)
	if strings.TrimSpace(id) == "" {
		return telegramBot{}, fmt.Errorf("this trigger needs a %s credential to register itself", TelegramCredentialType)
	}
	credential, err := lifecycleContext.Credentials.ResolveCredential(ctx, id)
	if err != nil {
		return telegramBot{}, fmt.Errorf("resolve the Telegram credential: %w", err)
	}
	if err := credential.CheckType(TelegramCredentialType); err != nil {
		return telegramBot{}, err
	}
	return telegramBotFrom(credential)
}

// telegramBotFrom reads the token and address out of a Telegram credential.
func telegramBotFrom(credential engine.Credential) (telegramBot, error) {
	token := strings.TrimSpace(credential.Fields["accessToken"])
	if token == "" {
		return telegramBot{}, fmt.Errorf("the Telegram credential has no access token")
	}
	return telegramBot{token: token, baseURL: TelegramBaseURL(credential.Fields["baseUrl"]), credential: credential}, nil
}

// telegramCall makes one Bot API call through the egress policy.
func telegramCall(ctx context.Context, policy safehttp.Policy, bot telegramBot, method string, arguments map[string]any) ([]byte, error) {
	target, err := telegramEndpoint(bot.baseURL, "/bot"+bot.token+"/"+method)
	if err != nil {
		return nil, err
	}
	if err := policy.CheckURL(target); err != nil {
		return nil, err
	}

	var body []byte
	if arguments != nil {
		encoded, marshalErr := json.Marshal(arguments)
		if marshalErr != nil {
			return nil, marshalErr
		}
		body = encoded
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	// The token rides in the path, so the credential's domain bound is checked
	// against the host it is about to be sent to, and its redirect scope is
	// bound to the request: the same checks the engine applies to a node's
	// own request, rather than a copy of them.
	if err := bot.credential.ScopeRequest(request); err != nil {
		return nil, err
	}
	response, err := safehttp.NewClient(policy).Do(request)
	if err != nil {
		// The URL carries the bot token in its path, and this error reaches
		// activation's answer and the polling loop's Warn line on every
		// failure: only the scheme and host go on.
		return nil, safehttp.RedactError(err)
	}
	defer response.Body.Close()
	contents, _, err := policy.ReadBody(response.Body)
	if err != nil {
		return nil, err
	}

	var answer struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal(contents, &answer)
	if response.StatusCode >= 400 || !answer.OK {
		reason := answer.Description
		if reason == "" {
			reason = fmt.Sprintf("the Telegram API answered %d", response.StatusCode)
		}
		// The Bot API repeats the request URL in some errors, and the URL
		// contains the token, so only the description is carried out.
		return nil, fmt.Errorf("%s failed: %s", method, reason)
	}
	return contents, nil
}

// TelegramPollers owns one getUpdates loop per active polling trigger.
//
// Polling is a development affordance and says so in the node: it is
// single-process, so a deployment running several workers would poll the same
// bot from each of them. That is correct for the single binary shipped today
// and is the first thing to revisit when work moves across processes.
type TelegramPollers struct {
	policy safehttp.Policy
	runner webhook.Runner
	root   context.Context

	mu      sync.Mutex
	running map[string]context.CancelFunc
}

// NewTelegramPollers builds the supervisor. The root context outlives any
// single activation request: a poller cancelled when its HTTP request finished
// would stop the moment it started.
func NewTelegramPollers(root context.Context, policy safehttp.Policy, runner webhook.Runner) *TelegramPollers {
	return &TelegramPollers{policy: policy, runner: runner, root: root, running: map[string]context.CancelFunc{}}
}

// Start begins polling for one binding with its Telegram credential,
// replacing any existing loop for it. The credential rather than its token is
// handed over, so every poll is checked against its domain bound as the
// registration calls are.
func (pollers *TelegramPollers) Start(lifecycleContext webhook.LifecycleContext, credential engine.Credential) error {
	if pollers == nil || pollers.runner == nil {
		return fmt.Errorf("this deployment cannot poll Telegram; use webhook delivery")
	}
	bot, err := telegramBotFrom(credential)
	if err != nil {
		return err
	}
	pollers.Stop(lifecycleContext.Binding.Route)

	ctx, cancel := context.WithCancel(pollers.root)
	pollers.mu.Lock()
	pollers.running[lifecycleContext.Binding.Route] = cancel
	pollers.mu.Unlock()

	go pollers.loop(ctx, lifecycleContext.Binding, bot)
	return nil
}

// Stop ends the loop for one binding, if there is one.
func (pollers *TelegramPollers) Stop(route string) {
	if pollers == nil {
		return
	}
	pollers.mu.Lock()
	cancel, running := pollers.running[route]
	delete(pollers.running, route)
	pollers.mu.Unlock()
	if running {
		cancel()
	}
}

// Running lists the routes being polled, for diagnostics and tests.
func (pollers *TelegramPollers) Running() []string {
	if pollers == nil {
		return nil
	}
	pollers.mu.Lock()
	defer pollers.mu.Unlock()
	routes := make([]string, 0, len(pollers.running))
	for route := range pollers.running {
		routes = append(routes, route)
	}
	return routes
}

// loop calls getUpdates until it is cancelled.
//
// Long polling rather than a timer: `timeout` holds the connection open until
// an update arrives, so an idle bot costs one open request rather than a
// request a second, and a message is delivered as soon as it exists.
func (pollers *TelegramPollers) loop(ctx context.Context, binding repository.WebhookBinding, bot telegramBot) {
	logger := slog.Default().With("route", binding.Route, "workflow", binding.WorkflowID, "node", binding.NodeID)
	logger.Info("polling Telegram for updates")
	defer logger.Info("stopped polling Telegram")

	offset := 0
	failures := 0
	for {
		if ctx.Err() != nil {
			return
		}
		updates, err := pollers.fetch(ctx, binding, bot, offset)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			failures++
			// Backing off matters: a bot whose token was revoked would
			// otherwise hammer the API in a tight loop for as long as the
			// workflow stays active.
			wait := telegramBackoff(failures)
			logger.Warn("polling Telegram failed", "error", err, "retryIn", wait)
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			continue
		}
		failures = 0

		for _, update := range updates {
			id, _ := update["update_id"].(float64)
			if int(id) >= offset {
				// The next call acknowledges this one. Telegram redelivers
				// anything below the offset, so advancing past a queued update
				// is what stops it arriving twice.
				offset = int(id) + 1
			}
			payload, err := json.Marshal(update)
			if err != nil {
				continue
			}
			if !pollers.accepts(binding, update) {
				continue
			}
			if _, err := pollers.runner.QueueWebhook(ctx, binding, payload); err != nil && ctx.Err() == nil {
				logger.Warn("a polled update could not be queued", "error", err)
			}
		}
	}
}

// accepts applies the same restriction filters the HTTP boundary applies, so
// the two delivery modes behave identically.
func (pollers *TelegramPollers) accepts(binding repository.WebhookBinding, update map[string]any) bool {
	accepted, reason := TelegramFilter(webhook.Delivery{Binding: binding, Body: update})
	if !accepted {
		slog.Default().Info("a polled update was filtered out by its trigger",
			"route", binding.Route, "workflow", binding.WorkflowID, "reason", reason)
	}
	return accepted
}

func (pollers *TelegramPollers) fetch(ctx context.Context, binding repository.WebhookBinding, bot telegramBot, offset int) ([]map[string]any, error) {
	arguments := map[string]any{"timeout": telegramPollSeconds, "offset": offset}
	if allowed := TelegramAllowedUpdates(binding.Parameters); len(allowed) > 0 {
		arguments["allowed_updates"] = allowed
	}
	// The long poll holds the connection open, so the policy's own timeout has
	// to be longer than the poll or every call would be cancelled mid-wait.
	policy := pollers.policy
	if policy.Timeout > 0 && policy.Timeout < telegramPollTimeout {
		policy.Timeout = telegramPollTimeout
	}
	body, err := telegramCall(ctx, policy, bot, "getUpdates", arguments)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Result []map[string]any `json:"result"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return nil, fmt.Errorf("getUpdates returned something that is not JSON: %w", err)
	}
	return answer.Result, nil
}

// telegramBackoff grows to a minute and stops there. Longer would make a
// recovered bot look dead; shorter would keep a revoked token hot.
func telegramBackoff(failures int) time.Duration {
	wait := time.Duration(failures) * 2 * time.Second
	if wait > time.Minute {
		return time.Minute
	}
	return wait
}

const (
	telegramPollSeconds = 25
	telegramPollTimeout = 40 * time.Second
)

// RegisterLifecycles installs the trigger lifecycle hooks this package ships.
//
// It exists so composition and the binding test call the same function: a test
// that built its own registry would assert that composition agrees with the
// test, which is not the property anybody wants.
func RegisterLifecycles(registry *webhook.LifecycleRegistry, pollers *TelegramPollers) error {
	return registry.Register(TelegramTriggerLifecycleID, NewTelegramLifecycle(pollers))
}

// RegisterTriggerKinds installs how this package's triggers shape and check
// their deliveries.
func RegisterTriggerKinds(registry *webhook.Registry) error {
	for nodeType, kind := range map[string]webhook.TriggerKind{
		// The n8n shape, not KilasFlow's own envelope: an imported webhook's
		// workflow reads `$json.body`, `$json.query`, `$json.headers`,
		// `$json.params`, `$json.webhookUrl` and `$json.executionMode`, and
		// none of those six keys existed under the envelope.
		WebhookNodeType:         {Shape: webhook.ShapeN8NCore},
		FormTriggerNodeType:     FormTriggerKind(),
		TelegramTriggerNodeType: TelegramTriggerKind(),
	} {
		if err := registry.Register(nodeType, kind); err != nil {
			return err
		}
	}
	return nil
}

// TelegramDefaultBaseURL is the Bot API's public address.
const TelegramDefaultBaseURL = "https://api.telegram.org"

// TelegramBaseURL is where a credential says its Bot API lives.
//
// Telegram publishes a local Bot API server, and a deployment running one has
// no route to the public host at all. It is a credential field rather than
// server configuration because it belongs to the bot: two bots on one server
// can legitimately live behind different addresses.
func TelegramBaseURL(configured string) string {
	if trimmed := strings.TrimSpace(configured); trimmed != "" {
		return strings.TrimSuffix(trimmed, "/")
	}
	return TelegramDefaultBaseURL
}

// telegramEndpoint joins a base URL and a Bot API path.
func telegramEndpoint(baseURL, path string) (*url.URL, error) {
	base, err := url.Parse(TelegramBaseURL(baseURL))
	if err != nil {
		return nil, fmt.Errorf("the Telegram credential's base URL is unusable: %w", err)
	}
	if base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("the Telegram credential's base URL must be absolute, such as %s", TelegramDefaultBaseURL)
	}
	joined := *base
	joined.Path = strings.TrimSuffix(base.Path, "/") + path
	return &joined, nil
}
