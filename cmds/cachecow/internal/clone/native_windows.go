// SPDX-License-Identifier: Apache-2.0

package clone

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const sourceOpenFlags = os.O_RDONLY

var getDiskFreeSpace = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetDiskFreeSpaceW")

type duplicateExtents struct {
	File         windows.Handle
	SourceOffset int64
	TargetOffset int64
	ByteCount    int64
}

type integrityInformation struct {
	Algorithm   uint16
	Reserved    uint16
	Flags       uint32
	ChunkSize   uint32
	ClusterSize uint32
}

func volume(h windows.Handle) (uint32, error) {
	var name [32]uint16
	var serial, flags uint32
	if err := windows.GetVolumeInformationByHandle(h, nil, 0, &serial, nil, &flags, &name[0], uint32(len(name))); err != nil {
		return 0, err
	}
	const supportsBlockRefcounting = 0x08000000
	if !strings.EqualFold(windows.UTF16ToString(name[:]), "ReFS") || flags&supportsBlockRefcounting == 0 {
		return 0, ErrUnsupported
	}
	return serial, nil
}

func nativeClone(ctx context.Context, src *os.File, dst string) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	source := windows.Handle(src.Fd())
	sourceVolume, err := volume(source)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := out.Close(); closeErr != nil {
			err = fmt.Errorf("close native clone (prior error: %v): %w", err, closeErr)
		}
	}()
	target := windows.Handle(out.Fd())
	targetVolume, err := volume(target)
	if err != nil {
		return err
	}
	if sourceVolume != targetVolume {
		return errors.Join(ErrUnsupported, windows.ERROR_NOT_SAME_DEVICE)
	}

	// ReFS requires identical integrity settings and sparse destinations for
	// sparse sources. Apply these before extending the new, private file.
	var returned uint32
	var integrity integrityInformation
	if err = windows.DeviceIoControl(source, windows.FSCTL_GET_INTEGRITY_INFORMATION, nil, 0, (*byte)(unsafe.Pointer(&integrity)), uint32(unsafe.Sizeof(integrity)), &returned, nil); err != nil {
		return capabilityError(err)
	}
	settings := struct {
		Algorithm uint16
		Reserved  uint16
		Flags     uint32
	}{Algorithm: integrity.Algorithm, Flags: integrity.Flags}
	if err = windows.DeviceIoControl(target, windows.FSCTL_SET_INTEGRITY_INFORMATION, (*byte)(unsafe.Pointer(&settings)), uint32(unsafe.Sizeof(settings)), nil, 0, &returned, nil); err != nil {
		return capabilityError(err)
	}
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(source, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_SPARSE_FILE != 0 {
		if err = windows.DeviceIoControl(target, windows.FSCTL_SET_SPARSE, nil, 0, nil, 0, &returned, nil); err != nil {
			return capabilityError(err)
		}
	}
	cluster, err := clusterSize(filepath.Dir(dst))
	if err != nil {
		return err
	}
	stat, err := src.Stat()
	if err != nil {
		return err
	}
	length, err := alignedLength(stat.Size(), cluster)
	if err != nil {
		return err
	}
	// Extend only the destination to a cluster boundary. The last source cluster
	// may contain EOF; retain the exact logical length after duplicating it.
	if err = out.Truncate(length); err != nil {
		return err
	}
	err = cloneChunks(ctx, length, cluster, func(offset, size int64) error {
		data := duplicateExtents{source, offset, offset, size}
		return capabilityError(windows.DeviceIoControl(target, windows.FSCTL_DUPLICATE_EXTENTS_TO_FILE, (*byte)(unsafe.Pointer(&data)), uint32(unsafe.Sizeof(data)), nil, 0, &returned, nil))
	})
	if err != nil {
		return err
	}
	return out.Truncate(stat.Size())
}

func clusterSize(path string) (int64, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var root [windows.MAX_PATH + 1]uint16
	if err = windows.GetVolumePathName(name, &root[0], uint32(len(root))); err != nil {
		return 0, err
	}
	var sectors, bytesPerSector, free, total uint32
	ok, _, callErr := getDiskFreeSpace.Call(uintptr(unsafe.Pointer(&root[0])), uintptr(unsafe.Pointer(&sectors)), uintptr(unsafe.Pointer(&bytesPerSector)), uintptr(unsafe.Pointer(&free)), uintptr(unsafe.Pointer(&total)))
	if ok == 0 {
		return 0, callErr
	}
	return int64(sectors) * int64(bytesPerSector), nil
}

func capabilityError(err error) error {
	if errors.Is(err, windows.ERROR_NOT_SAME_DEVICE) || errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_INVALID_FUNCTION) {
		return errors.Join(ErrUnsupported, err)
	}
	return err
}
