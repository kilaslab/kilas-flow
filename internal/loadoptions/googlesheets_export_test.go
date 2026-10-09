package loadoptions

// SetGoogleSheetsBasesForTest aims the Sheets loaders at a stub and returns the
// function that puts the real addresses back. An empty argument leaves that
// base alone.
func SetGoogleSheetsBasesForTest(drive, sheets string) func() {
	previousDrive, previousSheets := googleDriveBase, googleSheetsBase
	if drive != "" {
		googleDriveBase = drive
	}
	if sheets != "" {
		googleSheetsBase = sheets
	}
	return func() { googleDriveBase, googleSheetsBase = previousDrive, previousSheets }
}
