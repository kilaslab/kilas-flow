package repository

import (
	"testing"

	"gorm.io/gorm/schema"
)

// Every model names its table through the namer so database.table_prefix
// applies. NamingStrategy.TableName runs toDBName and inflection.Plural over
// its argument, so a base name that does not round-trip — workflow_versions,
// webhook_bindings and execution_node_runs in particular — would silently
// query the wrong table under a prefix. Proven per model rather than read off
// the inflection rules.
func TestTableNamesSurviveTheNamerRoundTrip(t *testing.T) {
	t.Parallel()

	models := map[string]func(schema.Namer) string{
		"tenants":                 tenantModel{}.TableName,
		"users":                   userModel{}.TableName,
		"api_keys":                apiKeyModel{}.TableName,
		"workflows":               workflowModel{}.TableName,
		"workflow_versions":       workflowVersionModel{}.TableName,
		"workflow_publish_events": workflowPublishEventModel{}.TableName,
		"executions":              executionModel{}.TableName,
		"execution_node_runs":     executionNodeRunModel{}.TableName,
		"execution_waits":         executionWaitModel{}.TableName,
		"credentials":             credentialModel{}.TableName,
		"secret_bindings":         secretBindingModel{}.TableName,
		"webhook_bindings":        webhookBindingModel{}.TableName,
		"webhook_routes":          webhookRouteModel{}.TableName,
		"webhook_deliveries":      webhookDeliveryModel{}.TableName,
		"schedules":               scheduleModel{}.TableName,
		"idempotency_keys":        idempotencyKeyModel{}.TableName,
	}

	if len(models) != len(Models()) {
		t.Fatalf("%d table functions cover %d models: a model without one bypasses the prefix", len(models), len(Models()))
	}

	for base, table := range models {
		t.Run(base, func(t *testing.T) {
			t.Parallel()
			if got := table(schema.NamingStrategy{}); got != base {
				t.Errorf("empty prefix: TableName = %q, want %q", got, base)
			}
			if got := table(schema.NamingStrategy{TablePrefix: "kflow_"}); got != "kflow_"+base {
				t.Errorf("kflow_ prefix: TableName = %q, want %q", got, "kflow_"+base)
			}
		})
	}
}

func TestPollCursorsTableNameSurvivesTheNamerRoundTrip(t *testing.T) {
	t.Parallel()

	if got := (pollCursorModel{}).TableName(schema.NamingStrategy{}); got != "poll_cursors" {
		t.Fatalf("empty prefix: TableName = %q, want poll_cursors", got)
	}
	if got := (pollCursorModel{}).TableName(schema.NamingStrategy{TablePrefix: "kflow_"}); got != "kflow_poll_cursors" {
		t.Fatalf("kflow_ prefix: TableName = %q, want kflow_poll_cursors", got)
	}
}

func TestWorkflowStaticDataTableNameSurvivesTheNamerRoundTrip(t *testing.T) {
	t.Parallel()

	if got := (workflowStaticDataModel{}).TableName(schema.NamingStrategy{}); got != "workflow_static_data" {
		t.Fatalf("empty prefix: TableName = %q, want workflow_static_data", got)
	}
	if got := (workflowStaticDataModel{}).TableName(schema.NamingStrategy{TablePrefix: "kflow_"}); got != "kflow_workflow_static_data" {
		t.Fatalf("kflow_ prefix: TableName = %q, want kflow_workflow_static_data", got)
	}
}
