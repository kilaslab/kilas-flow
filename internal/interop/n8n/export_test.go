package n8n

// OutputPortNameForTest and InputPortNameForTest expose the adapter's port
// naming so a test can prove it is the exact inverse of what the node pack
// registers. The two are mirrored rather than shared, because the adapter must
// not depend on the node pack.
func OutputPortNameForTest(kilasType string, index int) string {
	return outputPortName(kilasType, index)
}
func InputPortNameForTest(kilasType string, index int) string { return inputPortName(kilasType, index) }
