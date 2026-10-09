package nodes

import "strings"

// SetGoogleSheetsAPIRootForTest aims the Sheets node at a stub and returns the
// function that puts the real address back. It lives in its own file, beside
// SetGoogleAPIRootsForTest, so the two can change independently.
func SetGoogleSheetsAPIRootForTest(sheets string) func() {
	previous := sheetsAPIRoot
	if sheets != "" {
		sheetsAPIRoot = strings.TrimRight(sheets, "/")
	}
	return func() { sheetsAPIRoot = previous }
}
