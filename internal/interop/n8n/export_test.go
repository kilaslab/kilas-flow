package n8n

import "sort"

// OutputPortNameForTest and InputPortNameForTest expose the adapter's port
// naming so a test can prove it is the exact inverse of what the node pack
// registers. The two are mirrored rather than shared, because the adapter must
// not depend on the node pack.
func OutputPortNameForTest(kilasType string, index int) string {
	return outputPortName(kilasType, index)
}
func InputPortNameForTest(kilasType string, index int) string { return inputPortName(kilasType, index) }

// N8NConditionNamesForTest is every condition name the importer maps.
//
// Exported so the SQL-text table below can fail when a condition is added
// without a row, rather than leaving the new one untested by construction.
func N8NConditionNamesForTest() []string {
	names := make([]string, 0, len(postgresConditionOperators))
	for name := range postgresConditionOperators {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// DataTableToolStructureIssuesForTest exposes the importer's structure safety
// net, so a test can hand it parameters no n8n node imports into today: the
// net exists for the slot a future importer change leaves an expression.
func DataTableToolStructureIssuesForTest(parameters map[string]any, issues []Unsupported) []Unsupported {
	return dataTableToolStructureIssues(parameters, issues)
}
