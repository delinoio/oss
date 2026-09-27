package workspace

import "os"

func openWorkspaceEntry(root *os.Root, path string) (*os.File, error) {
	return root.Open(path)
}
