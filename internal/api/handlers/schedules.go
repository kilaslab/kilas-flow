package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/scheduler"
)

// ScheduleResource is one cron schedule.
type ScheduleResource struct {
	ID         string     `json:"id"`
	WorkflowID string     `json:"workflowId"`
	NodeID     string     `json:"nodeId,omitempty"`
	Cron       string     `json:"cron"`
	Active     bool       `json:"active"`
	LastRunAt  *time.Time `json:"lastRunAt,omitempty"`
	NextRunAt  *time.Time `json:"nextRunAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

// Schedules is the REST surface over cron schedules.
type Schedules struct {
	store   repository.ScheduleRepository
	tenants TenantResolver
}

// NewSchedules constructs the schedule handler.
func NewSchedules(store repository.ScheduleRepository, tenants TenantResolver) *Schedules {
	if tenants == nil {
		tenants = defaultTenantResolver{}
	}
	return &Schedules{store: store, tenants: tenants}
}

type scheduleBody struct {
	WorkflowID string `json:"workflowId,omitempty" doc:"Workflow to run; immutable after creation"`
	NodeID     string `json:"nodeId,omitempty" doc:"Schedule trigger node in that workflow"`
	Cron       string `json:"cron" doc:"Standard five-field cron expression, evaluated in UTC"`
	Active     bool   `json:"active" doc:"Whether this schedule fires"`
}

type createScheduleInput struct{ Body scheduleBody }

type schedulePathInput struct {
	ID string `path:"id" minLength:"1" doc:"Schedule identifier"`
}

type updateScheduleInput struct {
	ID   string `path:"id" minLength:"1" doc:"Schedule identifier"`
	Body scheduleBody
}

type scheduleOutput struct{ Body ScheduleResource }

type createdScheduleOutput struct {
	Status   int    `status:"201"`
	Location string `header:"Location"`
	Body     ScheduleResource
}

type scheduleListOutput struct{ Body []ScheduleResource }

type deletedScheduleOutput struct {
	Status int `status:"204"`
}

// Register wires schedule CRUD.
func (handler *Schedules) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-schedules", Method: http.MethodGet, Path: "/schedules",
		Summary: "List schedules", Description: "Returns every cron schedule in the workspace.", Tags: []string{"Schedules"},
	}, handler.List)
	huma.Register(api, huma.Operation{
		OperationID: "create-schedule", Method: http.MethodPost, Path: "/schedules", DefaultStatus: http.StatusCreated,
		Summary: "Create a schedule", Description: "Binds a cron expression to a workflow.", Tags: []string{"Schedules"},
	}, handler.Create)
	huma.Register(api, huma.Operation{
		OperationID: "update-schedule", Method: http.MethodPut, Path: "/schedules/{id}",
		Summary: "Update a schedule", Description: "Replaces the cron expression and activation state.", Tags: []string{"Schedules"},
	}, handler.Update)
	huma.Register(api, huma.Operation{
		OperationID: "delete-schedule", Method: http.MethodDelete, Path: "/schedules/{id}", DefaultStatus: http.StatusNoContent,
		Summary: "Delete a schedule", Description: "Removes a cron schedule.", Tags: []string{"Schedules"},
	}, handler.Delete)
}

// List returns every schedule in the tenant.
func (handler *Schedules) List(ctx context.Context, _ *struct{}) (*scheduleListOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("schedules unavailable")
	}
	schedules, err := handler.store.List(ctx, handler.tenants.Resolve(ctx))
	if err != nil {
		return nil, handler.problem(err)
	}
	resources := make([]ScheduleResource, 0, len(schedules))
	for _, schedule := range schedules {
		resources = append(resources, scheduleResource(schedule))
	}
	return &scheduleListOutput{Body: resources}, nil
}

// Create stores a schedule and computes its first due time.
func (handler *Schedules) Create(ctx context.Context, input *createScheduleInput) (*createdScheduleOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("schedules unavailable")
	}
	next, err := handler.nextRun(input.Body.Cron, input.Body.Active)
	if err != nil {
		return nil, err
	}
	schedule, err := handler.store.Create(ctx, handler.tenants.Resolve(ctx), repository.Schedule{
		WorkflowID: input.Body.WorkflowID, NodeID: input.Body.NodeID,
		Cron: input.Body.Cron, Active: input.Body.Active, NextRunAt: next,
	})
	if err != nil {
		return nil, handler.problem(err)
	}
	return &createdScheduleOutput{
		Status: http.StatusCreated, Location: "/api/v1/schedules/" + schedule.ID,
		Body: scheduleResource(schedule),
	}, nil
}

// Update replaces the cron expression and activation state.
func (handler *Schedules) Update(ctx context.Context, input *updateScheduleInput) (*scheduleOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("schedules unavailable")
	}
	next, err := handler.nextRun(input.Body.Cron, input.Body.Active)
	if err != nil {
		return nil, err
	}
	schedule, err := handler.store.Update(ctx, handler.tenants.Resolve(ctx), input.ID, repository.Schedule{
		Cron: input.Body.Cron, Active: input.Body.Active, NextRunAt: next,
	})
	if err != nil {
		return nil, handler.problem(err)
	}
	return &scheduleOutput{Body: scheduleResource(schedule)}, nil
}

// Delete removes a schedule.
func (handler *Schedules) Delete(ctx context.Context, input *schedulePathInput) (*deletedScheduleOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("schedules unavailable")
	}
	if err := handler.store.Delete(ctx, handler.tenants.Resolve(ctx), input.ID); err != nil {
		return nil, handler.problem(err)
	}
	return &deletedScheduleOutput{Status: http.StatusNoContent}, nil
}

// nextRun validates the cron expression and computes the first due time.
//
// An inactive schedule has no due time at all, so deactivating one cannot
// leave a stale row that fires the moment it is reactivated.
func (handler *Schedules) nextRun(expression string, active bool) (*time.Time, error) {
	if err := scheduler.Validate(expression); err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	if !active {
		return nil, nil
	}
	next, err := scheduler.Next(expression, time.Now().UTC())
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	return &next, nil
}

func (handler *Schedules) problem(err error) error {
	if errors.Is(err, repository.ErrNotFound) {
		return huma.Error404NotFound("schedule not found")
	}
	return huma.Error422UnprocessableEntity(err.Error())
}

func scheduleResource(schedule repository.Schedule) ScheduleResource {
	return ScheduleResource{
		ID: schedule.ID, WorkflowID: schedule.WorkflowID, NodeID: schedule.NodeID,
		Cron: schedule.Cron, Active: schedule.Active,
		LastRunAt: schedule.LastRunAt, NextRunAt: schedule.NextRunAt,
		CreatedAt: schedule.CreatedAt, UpdatedAt: schedule.UpdatedAt,
	}
}
