package state

// Windows does not expose a portable directory fsync through os.File.
func syncParent(string) error { return nil }
