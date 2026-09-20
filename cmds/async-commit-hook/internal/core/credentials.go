package core

import (
	"io"
	"os"
)

const maxCredentialFileBytes = 64 * 1024

func readCredentialFile(path string) ([]byte, error) {
	unavailable := func() ([]byte, error) {
		return nil, E("credential-unavailable", "credential file must be a readable regular file of at most 64 KiB", 3)
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() > maxCredentialFileBytes {
		return unavailable()
	}
	f, err := openCredentialFile(path)
	if err != nil {
		return unavailable()
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(before, info) || info.Size() > maxCredentialFileBytes {
		return unavailable()
	}
	// The file can grow after Stat. Limit the actual stream as well as its metadata.
	b, err := io.ReadAll(io.LimitReader(f, maxCredentialFileBytes+1))
	if err != nil || len(b) > maxCredentialFileBytes {
		return unavailable()
	}
	return b, nil
}
