// Package handlers holds the HTTP operations exposed by the kilasflow API.
//
// Operations are registered with Huma using typed input and output structs.
// The OpenAPI document is generated from those types, so the published spec
// cannot drift from the code that serves it.
package handlers

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// Pinger is the readiness dependency: anything that can report whether it is
// reachable. The database satisfies it.
type Pinger interface {
	Ping(ctx context.Context) error
}

// System serves liveness and readiness probes.
type System struct {
	version string
	db      Pinger
}

// NewSystem builds the system handler.
func NewSystem(version string, db Pinger) *System {
	return &System{version: version, db: db}
}

// HealthOutput is the liveness response.
type HealthOutput struct {
	Body struct {
		Status  string `json:"status" example:"ok" doc:"Always \"ok\" when the process is serving traffic"`
		Version string `json:"version" example:"0.1.0" doc:"KilasFlow version"`
	}
}

// ReadyOutput is the readiness response.
type ReadyOutput struct {
	Body struct {
		Status   string `json:"status" example:"ok" doc:"Overall readiness"`
		Database string `json:"database" example:"ok" doc:"Database reachability"`
		Error    string `json:"error,omitempty" doc:"Why the instance is not ready, when it is not"`
	}
}

// Register wires the system operations onto the given API or group.
func (s *System) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-health",
		Method:      http.MethodGet,
		Path:        "/health",
		Summary:     "Liveness probe",
		Description: "Reports that the process is up. Does not check dependencies; " +
			"use /ready for that.",
		Tags: []string{"System"},
	}, s.Health)

	huma.Register(api, huma.Operation{
		OperationID: "get-ready",
		Method:      http.MethodGet,
		Path:        "/ready",
		Summary:     "Readiness probe",
		Description: "Reports whether the instance can serve requests. Verifies that " +
			"the database is reachable. Returns 503 when it is not.",
		Tags: []string{"System"},
	}, s.Ready)
}

// Health always succeeds while the process is serving.
func (s *System) Health(ctx context.Context, _ *struct{}) (*HealthOutput, error) {
	out := &HealthOutput{}
	out.Body.Status = "ok"
	out.Body.Version = s.version

	return out, nil
}

// Ready reports 503 when a dependency is unavailable, so an orchestrator stops
// routing traffic to this instance rather than serving failures.
func (s *System) Ready(ctx context.Context, _ *struct{}) (*ReadyOutput, error) {
	if err := s.db.Ping(ctx); err != nil {
		return nil, huma.Error503ServiceUnavailable("database unreachable", err)
	}

	out := &ReadyOutput{}
	out.Body.Status = "ok"
	out.Body.Database = "ok"

	return out, nil
}
