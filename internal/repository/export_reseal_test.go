package repository

// SetResealHook installs a function Reseal calls for each row after opening it
// and before writing it back, so a test can change the row in between.
func SetResealHook(store *GORMCredentialStore, hook func(credentialID string)) {
	store.resealHook = hook
}
