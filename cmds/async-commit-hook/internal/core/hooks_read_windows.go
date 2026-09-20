package core

import "os"

func openNativeHook(root *os.Root, path string) (*os.File, error) {
	// Root enforces confinement; the caller checks the opened regular-file
	// identity against its nonsymlink snapshot before reading any bytes.
	return root.Open(path)
}
