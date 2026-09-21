package store

// managedOpenHook is a deterministic test seam for interposition regressions.
// Production code leaves it nil.
var managedOpenHook func()

func beforeManagedOpen() {
	if managedOpenHook != nil {
		managedOpenHook()
	}
}
