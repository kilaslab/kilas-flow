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
		"credentials":             credentialModel{}.TableName,
		"secret_bindings":         secretBindingModel{}.TableName,
		"webhook_bindings":        webhookBindingModel{}.TableName,
		"webhook_routes":          webhookRouteModel{}.TableName,
		"webhook_deliveries":      webhookDeliveryModel{}.TableName,
		"schedules":               scheduleModel{}.TableName,
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
