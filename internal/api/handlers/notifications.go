package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslab/kilas-flow/internal/repository"
)

// DeliveryLog reads one tenant's outbound notification log.
type DeliveryLog interface {
	List(ctx context.Context, tenant repository.TenantScope, filter repository.OutboundFilter) ([]repository.OutboundEvent, error)
}

// NotificationDeliveryResource is one outbound event as the delivery log
// shows it.
type NotificationDeliveryResource struct {
	ID             string          `json:"id" doc:"Stable event id, sent as the webhook-id header on every attempt"`
	Type           string          `json:"type" doc:"Event type, such as execution.failed"`
	SubjectID      string          `json:"subjectId,omitempty" doc:"The execution or workflow the event is about"`
	Endpoint       string          `json:"endpoint" doc:"Where the event is sent, as it was when the event was written"`
	Status         string          `json:"status" enum:"pending,delivered,dead" doc:"pending until a 2xx, delivered after one, dead once its attempts ran out"`
	Attempts       int             `json:"attempts" doc:"Attempts made so far"`
	LastStatusCode int             `json:"lastStatusCode,omitempty" doc:"HTTP status of the last attempt; absent when no response arrived"`
	LastError      string          `json:"lastError,omitempty" doc:"Why the last attempt failed"`
	NextAttemptAt  *time.Time      `json:"nextAttemptAt,omitempty" doc:"When the next attempt is due; absent once delivered or dead"`
	DeliveredAt    *time.Time      `json:"deliveredAt,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	Payload        json.RawMessage `json:"payload" doc:"The exact body every attempt sends"`
}

// Notifications is the REST surface over the tenant's notification delivery
// log.
type Notifications struct {
	log     DeliveryLog
	tenants TenantResolver
}

// NewNotifications constructs the delivery log handler.
func NewNotifications(log DeliveryLog, tenants TenantResolver) *Notifications {
	if tenants == nil {
		tenants = defaultTenantResolver{}
	}
	return &Notifications{log: log, tenants: tenants}
}

type listNotificationDeliveriesInput struct {
	Status string `query:"status" enum:"pending,delivered,dead" doc:"Only events in this state"`
	Limit  int    `query:"limit" minimum:"1" maximum:"200" doc:"Maximum events to return (default 50)"`
}

type notificationDeliveriesOutput struct {
	Body []NotificationDeliveryResource
}

// Register wires the delivery log.
func (handler *Notifications) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-notification-deliveries", Method: http.MethodGet, Path: "/notifications/deliveries",
		Summary:     "List notification deliveries",
		Description: "Returns the newest outbound notification events of the caller's tenant with their delivery state: attempts made, the last response status and error, and when the next attempt is due. Events are kept for notifications.retention.",
		Tags:        []string{"Notifications"},
	}, handler.List)
}

// List returns the newest events of the caller's tenant.
func (handler *Notifications) List(ctx context.Context, input *listNotificationDeliveriesInput) (*notificationDeliveriesOutput, error) {
	if handler.log == nil {
		return nil, huma.Error503ServiceUnavailable("notification deliveries unavailable")
	}
	events, err := handler.log.List(ctx, handler.tenants.Resolve(ctx), repository.OutboundFilter{
		Status: input.Status, Limit: input.Limit,
	})
	if err != nil {
		slog.ErrorContext(ctx, "list notification deliveries", "error", err)
		return nil, huma.Error500InternalServerError("notification deliveries could not be read")
	}
	resources := make([]NotificationDeliveryResource, 0, len(events))
	for _, event := range events {
		resources = append(resources, NotificationDeliveryResource{
			ID: event.ID, Type: event.EventType, SubjectID: event.SubjectID, Endpoint: event.Endpoint,
			Status: event.Status, Attempts: event.Attempts, LastStatusCode: event.LastStatusCode,
			LastError: event.LastError, NextAttemptAt: event.NextAttemptAt, DeliveredAt: event.DeliveredAt,
			CreatedAt: event.CreatedAt, UpdatedAt: event.UpdatedAt, Payload: event.Payload,
		})
	}
	return &notificationDeliveriesOutput{Body: resources}, nil
}
