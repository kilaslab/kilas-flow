// Package handlers holds the HTTP operations exposed by the kilasflow API.
//
// Operations are registered with Huma using typed input and output structs.
// The OpenAPI document is generated from those types, so the published spec
// cannot drift from the code that serves it.
package handlers

import (
	"context"
	"net/http"
	"reflect"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslab/kilas-flow/internal/datastore"
)

// Pinger is the readiness dependency: anything that can report whether it is
// reachable. The database satisfies it.
type Pinger interface {
	Ping(ctx context.Context) error
}

// FleetReporter reports the schema-version spread of the datastores this
// instance serves. The datastore engine satisfies it. It is an interface so a
// handler test can stand in for a fleet, and so a Deps without a row store
// leaves readiness exactly as it was.
type FleetReporter interface {
	FleetStatus(ctx context.Context) (datastore.FleetStatus, error)
}

// System serves liveness and readiness probes.
type System struct {
	version string
	db      Pinger
	fleet   FleetReporter
}

// NewSystem builds the system handler.
func NewSystem(version string, db Pinger) *System {
	return &System{version: version, db: db}
}

// WithFleet reports the datastore fleet's version spread from readiness.
func (s *System) WithFleet(fleet FleetReporter) *System {
	s.fleet = fleet

	return s
}

// HealthOutput is the liveness response.
type HealthOutput struct {
	Body struct {
		Status  string `json:"status" example:"ok" doc:"Always \"ok\" when the process is serving traffic"`
		Version string `json:"version" example:"0.1.0" doc:"KilasFlow version"`
	}
}

// ReadyDatastores is the datastore spread readiness reports: schema versions
// and counts only. It is named apart from the datastore resource schemas on
// purpose — those describe a datastore, this describes the whole fleet.
type ReadyDatastores struct {
	SchemaVersion int              `json:"schemaVersion" example:"1" doc:"The datastore schema version this build serves"`
	Spread        map[string]int64 `json:"spread" doc:"Datastores per schema version, keyed by the version number"`
	Behind        int64            `json:"behind" doc:"Datastores below the served version: a migration is outstanding"`
	Ahead         int64            `json:"ahead" doc:"Datastores above the served version: a newer build migrated them and this build refuses them"`
}

// ReadyOutput is the readiness response.
type ReadyOutput struct {
	Body struct {
		Status     string           `json:"status" example:"ok" doc:"Overall readiness"`
		Database   string           `json:"database" example:"ok" doc:"Database reachability"`
		Error      string           `json:"error,omitempty" doc:"Why the instance is not ready, when it is not"`
		Datastores *ReadyDatastores `json:"datastores,omitempty" doc:"Datastore schema-version spread; absent when the instance has no datastore store"`
	}
}

// NotReadyProblem is the migration-outstanding readiness 503 body: the RFC 9457
// problem document every non-success response carries, with the same
// `datastores` block the 200 body carries. It is not the only 503 this endpoint
// answers — an unreachable database and a fleet that cannot be read are plain
// `ErrorModel`s, which carry no block, because neither state can read the
// catalogue.
//
// The block is repeated on the not-ready path on purpose. It is the one state
// in which a monitor needs the spread — a datastore is behind, so an upgrade
// or a stuck migration is in progress — and the contract forbids reading it
// out of `detail` (docs/reference/api-contract.md: "never match on `detail`
// text"). Without the block the spread would be machine-readable only while
// readiness is green, which is when it says nothing.
type NotReadyProblem struct {
	huma.ErrorModel

	Datastores *ReadyDatastores `json:"datastores,omitempty" doc:"Datastore schema-version spread; the same block the 200 body carries"`
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
		Description: "Reports whether the instance can serve requests. Verifies " +
			"that the database is reachable and that no datastore is waiting on " +
			"a schema migration; the body carries the datastore schema-version " +
			"spread, counts only. Returns 503 when either check fails: a " +
			"migration outstanding carries the same datastores block in the " +
			"problem document, while an unreachable database — a catalogue that " +
			"cannot be read — carries none.",
		Tags: []string{"System"},
		// The 503 is declared so the spread a client reads while readiness
		// refuses is in the document, not only in the handler: the schema is
		// generated from the type the handler returns. The `default` response is
		// repeated here because Huma only adds it while it is the operation's
		// only declared response, and dropping it would take every other failure
		// of this endpoint out of the document.
		Responses: map[string]*huma.Response{
			"default": problemResponse(api, reflect.TypeOf(huma.ErrorModel{}), "Error"),
			"503": problemResponse(api, reflect.TypeOf(NotReadyProblem{}),
				"Not ready: the database is unreachable or a datastore migration is outstanding. "+
					"The body is a problem document; a migration outstanding carries the same `datastores` "+
					"block the 200 body carries, while an unreachable database carries none."),
		},
	}, s.Ready)
}

// problemResponse is one declared problem response: the media type every error
// in this API is served with, and the schema of the Go type the handler
// returns for it.
func problemResponse(api huma.API, body reflect.Type, description string) *huma.Response {
	return &huma.Response{
		Description: description,
		Content: map[string]*huma.MediaType{
			"application/problem+json": {
				Schema: api.OpenAPI().Components.Schemas.Schema(body, true, body.Name()),
			},
		},
	}
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
		return nil, unavailableProblem(ctx, "database unreachable", err)
	}

	out := &ReadyOutput{}
	out.Body.Status = "ok"
	out.Body.Database = "ok"

	if s.fleet == nil {
		return out, nil
	}

	status, err := s.fleet.FleetStatus(ctx)
	if err != nil {
		return nil, unavailableProblem(ctx, "datastore fleet status unavailable", err)
	}
	if !status.Ready() {
		// The problem document carries the same block the 200 body carries, so
		// a monitor reads the spread from a stable field instead of parsing
		// `detail` — see NotReadyProblem.
		return nil, &NotReadyProblem{
			ErrorModel: huma.ErrorModel{
				Status: http.StatusServiceUnavailable,
				Title:  http.StatusText(http.StatusServiceUnavailable),
				Detail: status.Problem(),
			},
			Datastores: readyDatastores(status),
		}
	}

	out.Body.Datastores = readyDatastores(status)

	return out, nil
}

// readyDatastores renders a fleet status as the block readiness reports, on
// the 200 body and on the not-ready problem document alike: one shape, so a
// client reads the spread the same way whichever status it arrives with.
func readyDatastores(status datastore.FleetStatus) *ReadyDatastores {
	return &ReadyDatastores{
		SchemaVersion: status.Version,
		Spread:        spreadCounts(status.Spread),
		Behind:        status.Behind,
		Ahead:         status.Ahead,
	}
}

// spreadCounts keys the spread by version string: a JSON object's keys are
// strings, so a map[int]int64 would serialise as an array of pairs. The map is
// never nil, so an empty spread is {} rather than null.
func spreadCounts(spread map[int]int64) map[string]int64 {
	counts := make(map[string]int64, len(spread))
	for version, count := range spread {
		counts[strconv.Itoa(version)] = count
	}

	return counts
}
