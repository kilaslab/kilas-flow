package nodes

import "strings"

func SetGoogleAPIRootsForTest(drive, gmail string) func() {
	previousDrive, previousGmail := driveAPIRoot, gmailAPIRoot
	if drive != "" {
		driveAPIRoot = strings.TrimRight(drive, "/")
	}
	if gmail != "" {
		gmailAPIRoot = strings.TrimRight(gmail, "/")
	}
	return func() {
		driveAPIRoot, gmailAPIRoot = previousDrive, previousGmail
	}
}
