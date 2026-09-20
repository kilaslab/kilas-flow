package loadoptions

import (
	"context"
	"fmt"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/property"
)

// The two loaders a datastore node's pickers are built from.
//
// Named as data rather than as functions, so a node declares which one it
// wants and this package decides what that means. A loader a node could name
// by function would be a function the request could eventually name.
const (
	// DatastoreListLoader answers the data-table resource locator's From
	// list mode with every datastore in the tenant.
	DatastoreListLoader = "datastores"
	// DatastoreMappingColumnsLoader is the schema loader behind the
	// datastore node's resource mapper.
	DatastoreMappingColumnsLoader = "datastores.mappingColumns"
)

// DatastoreDependency is the parameter key the column loader reads: the
// resource locator holding the table the mapper writes into.
const DatastoreDependency = "dataTableId"

// DatastoreOption is one listable datastore.
type DatastoreOption struct {
	ID   string
	Name string
}

// DatastoreColumn is one mappable column of a datastore.
type DatastoreColumn struct {
	Name     string
	Type     string
	ReadOnly bool
}

// Datastores answers the data-table locator's list mode from this process's
// own storage, the way Workflows answers the sub-workflow list. The tenancy
// check has to be here: an internal lookup has no egress policy to hide
// behind, so the caller-supplied tenant is the whole boundary.
//
// An embed session sees no datastores at all. A session is bound to one
// workflow and no datastore is bound to any workflow, so any list handed to
// one would be a directory of the tenant — and the management API already
// denies embed sessions on every datastore path.
func Datastores(list func(ctx context.Context, tenantID string) ([]DatastoreOption, error)) InternalLoader {
	return func(ctx context.Context, scope Scope) (Result, error) {
		if list == nil {
			return Result{}, fmt.Errorf("datastore storage is not available on this server")
		}
		if scope.TenantID == "" {
			return Result{}, fmt.Errorf("a datastore list needs a tenant")
		}
		if scope.WorkflowID != "" {
			return Result{Options: []Option{}, Reason: "embedded editors cannot browse data tables"}, nil
		}
		found, err := list(ctx, scope.TenantID)
		if err != nil {
			return Result{}, fmt.Errorf("the datastore list could not be read")
		}
		options := make([]Option, 0, len(found))
		for _, candidate := range found {
			options = append(options, Option{Label: candidate.Name, Value: candidate.ID})
		}
		return Result{Options: options}, nil
	}
}

// DatastoreColumns answers the datastore mapper's column list. The reference
// is whatever the locator resolved to — an id from the list, or a name the
// operator typed — and the describe callback owns that resolution, so this
// layer never learns which mode the picker was in.
func DatastoreColumns(describe func(ctx context.Context, tenantID, ref string) ([]DatastoreColumn, error)) SchemaLoader {
	return func(ctx context.Context, scope Scope) (property.MapperSchema, error) {
		if describe == nil {
			return property.MapperSchema{}, fmt.Errorf("datastore storage is not available on this server")
		}
		if scope.TenantID == "" {
			return property.MapperSchema{}, fmt.Errorf("datastore columns need a tenant")
		}
		if scope.WorkflowID != "" {
			return property.MapperSchema{}, fmt.Errorf("embedded editors cannot browse data tables")
		}
		ref := strings.TrimSpace(scope.Dependencies[DatastoreDependency])
		if ref == "" {
			return property.MapperSchema{}, fmt.Errorf("choose a data table first")
		}
		columns, err := describe(ctx, scope.TenantID, ref)
		if err != nil {
			return property.MapperSchema{}, fmt.Errorf("the data table's columns could not be read")
		}
		fields := make([]property.MapperField, 0, len(columns))
		for _, column := range columns {
			fields = append(fields, property.MapperField{
				ID: column.Name, DisplayName: column.Name,
				Type: datastoreMapperType(column.Type), ReadOnly: column.ReadOnly,
				CanBeUsedToMatch: true,
			})
		}
		return property.MapperSchema{Fields: fields}, nil
	}
}

// mapperType renders a datastore wire type in the mapper's own vocabulary.
// The catalogue stores date under that name while the mapper calls it
// dateTime; anything unrecognised degrades to a string rather than refusing
// a column the table legitimately holds.
func datastoreMapperType(wire string) string {
	switch strings.ToLower(strings.TrimSpace(wire)) {
	case "number":
		return "number"
	case "boolean":
		return "boolean"
	case "date", "datetime":
		return "dateTime"
	default:
		return "string"
	}
}
