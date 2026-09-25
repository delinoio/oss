package store

import (
	"context"
	"math"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func (s *Store) DiagnosticStorage(ctx context.Context) (domain.DiagnosticStorage, error) {
	result := domain.DiagnosticStorage{Resources: []domain.DiagnosticResourceCount{}}
	err := s.Read(ctx, func(tx *Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		var pages, size uint64
		if err := tx.tx.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages); err != nil {
			return storageError(err)
		}
		if err := tx.tx.QueryRowContext(ctx, "PRAGMA page_size").Scan(&size); err != nil {
			return storageError(err)
		}
		if size == 0 || pages > math.MaxUint64/size {
			return domain.Fail(domain.RecoveryRequired, "Database size metadata is invalid.", "Inspect the original database without repair or replacement.")
		}
		bytes := pages * size
		result.LogicalDatabaseBytes = &bytes
		rows, err := tx.tx.QueryContext(ctx, "SELECT kind, COUNT(*) FROM entities GROUP BY kind ORDER BY kind")
		if err != nil {
			return storageError(err)
		}
		defer rows.Close()
		for rows.Next() {
			var entry domain.DiagnosticResourceCount
			if err := rows.Scan(&entry.Kind, &entry.Count); err != nil {
				return storageError(err)
			}
			if !entry.Kind.Valid() {
				return domain.Fail(domain.RecoveryRequired, "Database resource metadata is invalid.", "Inspect the original database before attempting recovery.")
			}
			result.Resources = append(result.Resources, entry)
		}
		return storageError(rows.Err())
	})
	if err != nil {
		return result, err
	}
	// Filesystem facts are sampled separately from the database read transaction;
	// physical and logical sizes are not additive and do not imply reclaimable bytes.
	bytes, err := privateFileSize(filepath.Join(s.root, "state.sqlite"), false)
	if err != nil {
		return result, err
	}
	result.DatabaseBytes = &bytes
	wal, err := privateFileSize(filepath.Join(s.root, "state.sqlite-wal"), true)
	if err != nil {
		return result, err
	}
	result.WALBytes = &wal
	capacity, available, err := volumeSpace(s.root)
	if err != nil {
		return result, storageError(err)
	}
	result.VolumeCapacityBytes, result.VolumeAvailableBytes = &capacity, &available
	result.Result = domain.DiagnosticResult{State: domain.DiagnosticObserved}
	return result, nil
}
func privateFileSize(path string, optional bool) (uint64, error) {
	info, err := os.Lstat(path)
	if optional && os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, storageError(err)
	}
	if !info.Mode().IsRegular() || info.Size() < 0 {
		return 0, domain.Fail(domain.RecoveryRequired, "Storage metadata is not a regular owned file.", "Inspect the original data scope without replacing it.")
	}
	if err := security.RegularPrivate(path); err != nil {
		return 0, err
	}
	return uint64(info.Size()), nil
}
