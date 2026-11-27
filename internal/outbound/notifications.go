package outbound

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/tenantsettings"
)

// SettingsSection is the tenant-settings section this package owns: the one
// endpoint a tenant's notifications go to, and which events it hears.
const SettingsSection = "notifications"

// DefaultEvents is what a tenant that names an endpoint and no events
// receives: failures only, the event a host alerts on.
var DefaultEvents = []string{repository.OutboundExecutionFailed}

// Events are every event a tenant can subscribe to.
var Events = []string{
	repository.OutboundExecutionFailed,
	repository.OutboundExecutionCompleted,
	repository.OutboundWorkflowActivated,
	repository.OutboundWorkflowDeactivated,
}

// maxEndpointLength bounds the stored URL, as the column a route is stored in
// is bounded: a URL past it is a mistake, not an endpoint.
const maxEndpointLength = 2048

// TenantSettings is the notifications section.
type TenantSettings struct {
	// URL is the endpoint every subscribed event is posted to. Empty turns
	// notifications off for the tenant.
	URL string `json:"url,omitempty"`
	// Events narrows or widens what is sent. Empty means DefaultEvents.
	Events []string `json:"events,omitempty"`
}

// Validate refuses a URL that is not an absolute http(s) address without
// credentials, and an event this build does not send. The address itself is
// judged again by the egress policy on every delivery, which is where a
// private or disallowed host is refused: a URL valid today can resolve
// somewhere else tomorrow.
func (settings TenantSettings) Validate() error {
	var problems []error
	if settings.URL != "" {
		if err := checkEndpoint(settings.URL); err != nil {
			problems = append(problems, &tenantsettings.FieldError{Field: "url", Message: err.Error()})
		}
	}
	for _, event := range settings.Events {
		if !slices.Contains(Events, event) {
			problems = append(problems, &tenantsettings.FieldError{
				Field:   "events",
				Message: fmt.Sprintf("%q is not an event this server sends; use %s", event, strings.Join(Events, ", ")),
			})
		}
	}
	return errors.Join(problems...)
}

func checkEndpoint(raw string) error {
	if len(raw) > maxEndpointLength {
		return fmt.Errorf("must be at most %d characters", maxEndpointLength)
	}
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return fmt.Errorf("must be an absolute http or https URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("must be an absolute http or https URL")
	}
	if parsed.User != nil {
		return fmt.Errorf("must not carry credentials; deliveries are authenticated by their signature")
	}
	return nil
}

// subscribes reports whether the section asks for eventType.
func (settings TenantSettings) subscribes(eventType string) bool {
	events := settings.Events
	if len(events) == 0 {
		events = DefaultEvents
	}
	return slices.Contains(events, eventType)
}

// RegisterSettings adds the notifications section to a tenant-settings
// registry.
func RegisterSettings(registry *tenantsettings.Registry) error {
	return tenantsettings.Register(registry, SettingsSection, TenantSettings.Validate)
}

// Router routes outbox events by the tenant's notifications section, read
// from the settings document the state change's own transaction read.
//
// A document that does not decode, or a section that no longer validates, is
// no subscription: notifications are skipped rather than the state change
// failed, because a run must not fail over where its report goes.
func Router() repository.OutboxRouter {
	return func(_ string, eventType string, settings []byte) (string, bool) {
		if len(settings) == 0 {
			return "", false
		}
		var document struct {
			Notifications *TenantSettings `json:"notifications"`
		}
		if err := json.Unmarshal(settings, &document); err != nil || document.Notifications == nil {
			return "", false
		}
		section := *document.Notifications
		if section.URL == "" || section.Validate() != nil || !section.subscribes(eventType) {
			return "", false
		}
		return section.URL, true
	}
}

// TenantSecret derives one tenant's signing secret from the deployment's
// notifications key: base64 of HMAC-SHA256(key, "kilasflow-notifications:" +
// tenantID).
//
// Derived rather than stored, so no secret sits in a settings document a
// tenant API reads back, and each tenant's differs: a receiver holding one
// tenant's secret cannot forge another tenant's deliveries. The operator
// computes a tenant's secret with the same formula and hands it to whoever
// verifies that tenant's endpoint. Rotating the key rotates every tenant's
// secret at once.
func TenantSecret(key []byte, tenantID string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("kilasflow-notifications:"))
	mac.Write([]byte(tenantID))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// DerivedSecrets is a SecretSource over TenantSecret. An empty key yields an
// empty secret, which the dispatcher refuses to send with.
func DerivedSecrets(key []byte) SecretSource {
	return func(tenantID string) (string, error) {
		if len(key) == 0 {
			return "", nil
		}
		return TenantSecret(key, tenantID), nil
	}
}
