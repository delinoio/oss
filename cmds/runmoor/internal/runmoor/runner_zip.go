package runmoor

import (
	"archive/tar"
	"archive/zip"
	"context"
	"io"
	"os"
	"path"
	"runtime"
	"strings"
)

func safeWindowsArchiveName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.TrimRight(name, " .") != name || strings.ContainsAny(name, "<>:\"/\\|?*\x00\r\n") {
		return false
	}
	for _, c := range name {
		if c < 32 {
			return false
		}
	}
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return false
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && strings.ContainsRune("123456789", rune(base[3])) {
		return false
	}
	for _, prefix := range []string{"COM", "LPT"} {
		if strings.HasPrefix(base, prefix) {
			suffix := strings.TrimPrefix(base, prefix)
			if suffix == "¹" || suffix == "²" || suffix == "³" {
				return false
			}
		}
	}
	return true
}

// Normalize ZIPs to the existing bounded tar representation. Validation is
// independent of the extraction host, so Linux fixtures cover Windows names.
func repackRunnerZip(ctx context.Context, input *os.File, out *os.File) error {
	info, err := input.Stat()
	if err != nil {
		return err
	}
	z, err := zip.NewReader(input, info.Size())
	if err != nil || len(z.File) > 100000 {
		return archiveProblem()
	}
	tw := tar.NewWriter(out)
	entries := map[string]bool{}
	var total uint64
	listener, run := false, false
	for _, f := range z.File {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		name := strings.TrimSuffix(f.Name, "/")
		if path.Clean(name) != name || strings.HasPrefix(name, "/") {
			return archiveProblem()
		}
		for _, part := range strings.Split(name, "/") {
			if !safeWindowsArchiveName(part) {
				return archiveProblem()
			}
		}
		key := strings.ToLower(name)
		if _, ok := entries[key]; ok {
			return archiveProblem()
		}
		for parent := path.Dir(key); parent != "."; parent = path.Dir(parent) {
			if dir, exists := entries[parent]; exists && !dir {
				return archiveProblem()
			}
		}
		dir := f.FileInfo().IsDir()
		if !dir && !f.Mode().IsRegular() {
			return archiveProblem()
		}
		if !dir {
			for prior := range entries {
				if strings.HasPrefix(prior, key+"/") {
					return archiveProblem()
				}
			}
		}
		entries[key] = dir
		if f.UncompressedSize64 > 4<<30 || total+f.UncompressedSize64 > 4<<30 {
			return archiveProblem()
		}
		total += f.UncompressedSize64
		h := &tar.Header{Name: name, Mode: 0700, Typeflag: tar.TypeDir}
		if !dir {
			h.Typeflag = tar.TypeReg
			h.Size = int64(f.UncompressedSize64)
			h.Mode = 0600
		}
		if err = tw.WriteHeader(h); err != nil {
			return err
		}
		if dir {
			continue
		}
		r, e := f.Open()
		if e != nil {
			return archiveProblem()
		}
		n, e := io.Copy(tw, &contextReader{ctx, io.LimitReader(r, int64(f.UncompressedSize64)+1)})
		closeErr := r.Close()
		if e != nil || closeErr != nil || n != int64(f.UncompressedSize64) {
			return archiveProblem()
		}
		listener = listener || name == "bin/Runner.Listener.exe"
		run = run || name == "run.cmd"
	}
	if !listener || !run {
		return archiveProblem()
	}
	if err = tw.Close(); err != nil {
		return err
	}
	return out.Sync()
}
func repackRunnerArchive(ctx context.Context, in *os.File, out *os.File) error {
	if runtime.GOOS == "windows" {
		return repackRunnerZip(ctx, in, out)
	}
	return repackRunnerTar(ctx, in, out)
}
