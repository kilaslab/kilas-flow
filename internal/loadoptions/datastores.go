package loadoptions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/datastore"
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
		// A name the By-Name locator would refuse as shared is one the picker
		// must not show twice: such a pair survives the unique index where the
		// database folds case less than Go does, and this list is where its
		// owner tells the two apart. Asked of the resolver itself, so the label
		// and the locator can never disagree about which names are the same;
		// quadratic, over one tenant's catalogue, which the datastore limit
		// bounds.
		listed := make([]datastore.Datastore, 0, len(found))
		for _, candidate := range found {
			listed = append(listed, datastore.Datastore{ID: candidate.ID, Name: candidate.Name})
		}
		options := make([]Option, 0, len(found))
		for _, candidate := range found {
			label := candidate.Name
			if _, err := datastore.ResolveByName(listed, candidate.Name); errors.Is(err, datastore.ErrAmbiguousName) {
				label += " · " + shortDatastoreID(candidate.ID)
			}
			options = append(options, Option{Label: label, Value: candidate.ID})
		}
		return Result{Options: options}, nil
	}
}

// shortDatastoreID is the tail of a datastore id, which is enough to tell two
// tables apart in a label. The tail rather than the head: an id is a UUIDv7,
// whose leading characters are its creation time, so two tables made the same
// minute share them.
func shortDatastoreID(id string) string {
	const width = 8
	if len(id) <= width {
		return id
	}
	return id[len(id)-width:]
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
