package store

import "golang.org/x/sys/unix"

func claimBackupImage(from, to string) error {
	return unix.RenamexNp(from, to, unix.RENAME_EXCL)
}
