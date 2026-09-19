package runmoor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type ImageRequest struct {
	Action        string    `json:"action"`
	ID            string    `json:"id,omitempty"`
	Name          string    `json:"name,omitempty"`
	IPSW          string    `json:"ipsw,omitempty"`
	From          string    `json:"from,omitempty"`
	SourceHome    string    `json:"source_home,omitempty"`
	Resources     Resources `json:"resources"`
	RunnerPath    string    `json:"runner_path,omitempty"`
	RunnerVersion string    `json:"runner_version,omitempty"`
}
type ImageManager struct {
	Store *Store
	Tart  *TartDriver
}

func (m *ImageManager) Operate(ctx context.Context, c Config, req ImageRequest) (*Image, error) {
	if e := m.Tart.check(ctx, c); e != nil {
		return nil, e
	}
	if req.Action == "create" {
		return m.create(ctx, c, req)
	}
	s := m.Store.View()
	im := s.Images[req.ID]
	if im == nil {
		return nil, problem(ErrImage, "Image revision does not exist.", "Use 'runmoor image list' to select an existing revision.")
	}
	uncreated := false
	if e := verifyVMOwner(c, im.VM, s.Installation, im.ID); e != nil {
		_, vmErr := os.Lstat(vmPath(c, im.VM))
		_, ownerErr := os.Lstat(vmOwnerPath(c, im.VM))
		uncreated = req.Action == "remove" && im.Phase != ImageOpen && os.IsNotExist(vmErr) && os.IsNotExist(ownerErr)
		if !uncreated {
			return nil, e
		}
	}
	switch req.Action {
	case "open":
		ctx, cancel := context.WithTimeout(ctx, c.Preparation(Tart))
		defer cancel()
		if im.Phase == ImageSealed || im.Phase == ImageRemoving {
			return nil, problem(ErrImage, "A sealed or removing revision cannot be opened for setup.", "Create a new revision with --from pointing at the sealed revision UUID.")
		}
		if im.Phase == ImageOpen {
			v, e := m.Tart.vm(ctx, c, im.VM)
			if e != nil {
				return nil, e
			}
			if v.Running {
				return im, nil
			}
		}
		if e := m.reserve(c, im.ID); e != nil {
			return nil, e
		}
		if _, e := m.Tart.Exec.Start(c.TartExecutable, []string{"run", "--no-audio", im.VM}, tartEnv(c)); e != nil {
			return nil, m.imageFailure(im.ID, problem(ErrPreparation, "Cannot open the setup VM.", "Inspect Tart and close unused setup VMs before retrying."))
		}
		// A detached process is not proof of boot. Keep its reservation until
		// Tart confirms the VM running or reports an actionable startup failure.
		for {
			v, e := m.Tart.vm(ctx, c, im.VM)
			if e == nil && v.Running {
				break
			}
			if !waitContext(ctx, 250*time.Millisecond) {
				return nil, m.imageFailure(im.ID, problem(ErrPreparation, "Setup VM startup was not confirmed before the preparation deadline.", "Inspect Tart boot compatibility and image list. The reservation remains until the VM is confirmed stopped; retry image open after correcting the failure."))
			}
		}
	case "seal":
		ctx, cancel := context.WithTimeout(ctx, c.Preparation(Tart))
		defer cancel()
		if im.Phase == ImageSealed {
			return im, nil
		}
		if !versionPattern.MatchString(req.RunnerVersion) {
			return nil, problem(ErrConfig, "Sealing requires an exact --runner-version.", "Install a pinned runner in a clean image first.")
		}
		if req.RunnerPath == "" {
			req.RunnerPath = "/Users/runner/actions-runner"
		}
		if !filepath.IsAbs(req.RunnerPath) || strings.ContainsAny(req.RunnerPath, "\x00\n\r") {
			return nil, problem(ErrConfig, "Runner path must be an absolute guest path.", "Use the prepared runner installation directory.")
		}
		if e := m.reserve(c, im.ID); e != nil {
			return nil, e
		}
		v, e := m.Tart.vm(ctx, c, im.VM)
		if e != nil {
			return nil, m.imageFailure(im.ID, e)
		}
		if !v.Running {
			if _, e = m.Tart.Exec.Start(c.TartExecutable, []string{"run", "--no-graphics", "--no-audio", im.VM}, tartEnv(c)); e != nil {
				return nil, m.imageFailure(im.ID, problem(ErrPreparation, "Cannot boot the image for validation.", "Check Tart and image compatibility."))
			}
		}
		if e = m.Tart.guestReady(ctx, c, im.VM, req.RunnerPath, req.RunnerVersion); e != nil {
			return nil, m.imageFailure(im.ID, e)
		}
		r := Runner{ID: im.ID, Handle: Handle{VM: im.VM}}
		if e = m.Tart.Stop(ctx, c, r, s); e != nil {
			return nil, m.imageFailure(im.ID, e)
		}
		digest, e := imageDigest(c, im.VM)
		if e != nil {
			return nil, m.imageFailure(im.ID, e)
		}
		if e = m.Store.Update(func(v *Snapshot) error {
			i := v.Images[im.ID]
			i.Phase = ImageSealed
			i.Digest = digest
			i.RunnerPath = req.RunnerPath
			i.RunnerVersion = req.RunnerVersion
			i.Problem = nil
			return nil
		}); e != nil {
			return nil, e
		}
	case "remove":
		if e := m.Store.Update(func(v *Snapshot) error {
			for _, p := range v.Pools {
				if p.Phase != Retired && p.Spec.Backend == Tart && p.Spec.Image == im.ID {
					return problem(ErrImageInUse, "An active pool references this image.", "Remove or change the pool and drain its old generation first.")
				}
			}
			for _, r := range v.Runners {
				if r.Image == im.ID && r.Phase != Completed {
					return problem(ErrImageInUse, "An execution still references this image.", "Wait for execution and cleanup to finish.")
				}
			}
			if v.Images[im.ID].Phase == ImageOpen {
				return problem(ErrImageInUse, "The image is open for setup or validation.", "Close its Tart window and wait for it to stop before removal.")
			}
			v.Images[im.ID].Phase = ImageRemoving
			return nil
		}); e != nil {
			return nil, e
		}
		if e := cleanupImportArchive(c, im.ID); e != nil {
			return nil, m.imageFailure(im.ID, e)
		}
		if !uncreated {
			if e := m.Tart.Cleanup(ctx, c, Runner{ID: im.ID, Handle: Handle{VM: im.VM}}, s); e != nil {
				return nil, m.imageFailure(im.ID, e)
			}
		}
		if e := m.Store.Update(func(v *Snapshot) error { delete(v.Images, im.ID); return nil }); e != nil {
			return nil, e
		}
		return im, nil
	default:
		return nil, problem(ErrConfig, "Unknown image operation.", "Use create, open, seal, list or remove.")
	}
	return m.Store.View().Images[im.ID], nil
}
func (m *ImageManager) reserve(c Config, id string) error {
	if e := diskCheck(c); e != nil {
		return e
	}
	return m.Store.Update(func(s *Snapshot) error {
		im := s.Images[id]
		if im.Phase == ImageOpen {
			return nil
		}
		used, count, vms := usage(*s)
		if used.CPU+im.Resources.CPU > c.Host.CPU || used.MemoryMiB+im.Resources.MemoryMiB > c.Host.MemoryMiB || count+1 > c.Host.MaxRunners || vms >= 2 {
			return problem(ErrCapacity, "No host capacity is available for image preparation.", "Wait for jobs to finish or close another setup VM; macOS permits at most two Runmoor VMs.")
		}
		im.Phase = ImageOpen
		im.Problem = nil
		return nil
	})
}
func (m *ImageManager) imageFailure(id string, err error) error {
	p := classify(err, ErrImage, "Image operation failed.", "Inspect image status and retry or explicitly remove the preparation revision.")
	_ = m.Store.Update(func(s *Snapshot) error {
		if i := s.Images[id]; i != nil {
			i.Problem = p
			// A failed, reaped create command with no VM consumed no capacity.
			// Unknown outcomes after a crash retain their reservation for recovery.
			if _, err := os.Lstat(vmPath(s.Config, i.VM)); os.IsNotExist(err) && i.Phase == ImageOpen {
				i.Phase = ImagePreparing
			}
		}
		return nil
	})
	return p
}
func (m *ImageManager) create(ctx context.Context, c Config, req ImageRequest) (result *Image, err error) {
	if !safeName.MatchString(req.Name) || (req.IPSW == "") == (req.From == "") || !validResources(req.Resources) {
		return nil, problem(ErrConfig, "Image creation requires a safe name, explicit resources and exactly one --ipsw or --from source.", "See 'runmoor image create --help'.")
	}
	if req.IPSW != "" && (!filepath.IsAbs(req.IPSW) || !strings.HasSuffix(strings.ToLower(req.IPSW), ".ipsw")) {
		return nil, problem(ErrConfig, "IPSW input must be a local absolute .ipsw path.", "Download the restore image yourself and provide its path.")
	}
	id := newID()
	im := &Image{ID: id, Name: req.Name, Phase: ImagePreparing, VM: "rm-image-" + id, Resources: req.Resources, Source: req.From, CreatedAt: nowUTC()}
	if req.IPSW != "" {
		im.Source = req.IPSW
	}
	if e := m.Store.Update(func(s *Snapshot) error { s.Images[id] = im; return nil }); e != nil {
		return nil, e
	}
	if e := m.reserve(c, id); e != nil {
		_ = m.Store.Update(func(s *Snapshot) error { delete(s.Images, id); return nil })
		return nil, e
	}
	s := m.Store.View()
	if e := claimVM(c, im.VM, s.Installation, id); e != nil {
		return nil, m.imageFailure(id, e)
	}
	var args []string
	if req.IPSW != "" {
		args = []string{"create", "--from-ipsw", req.IPSW, im.VM}
	} else if source := s.Images[req.From]; source != nil && source.Phase == ImageSealed {
		if e := m.Tart.validateSealed(ctx, c, source, s.Installation); e != nil {
			return nil, m.imageFailure(id, e)
		}
		args = []string{"clone", source.VM, im.VM}
	} else if strings.HasPrefix(req.From, "oci://") {
		ref := strings.TrimPrefix(req.From, "oci://")
		if strings.ContainsAny(ref, "\x00\n\r") || !strings.Contains(ref, "/") {
			return nil, m.imageFailure(id, problem(ErrImage, "Invalid OCI source.", "Use an oci://registry/namespace/image reference."))
		}
		if _, e := m.Tart.run(ctx, c, []string{"pull", ref}, nil); e != nil {
			return nil, m.imageFailure(id, e)
		}
		b, e := m.Tart.run(ctx, c, []string{"fqn", ref}, nil)
		if e != nil {
			return nil, m.imageFailure(id, e)
		}
		resolved := strings.TrimSpace(string(b))
		if !imagePattern.MatchString(resolved) {
			return nil, m.imageFailure(id, problem(ErrImage, "OCI image did not resolve to an immutable digest.", "Use a digest-pinned OCI image supported by Tart."))
		}
		im.Source = resolved
		args = []string{"clone", resolved, im.VM}
	} else if filepath.IsAbs(req.From) && strings.HasSuffix(req.From, ".tvm") {
		args = []string{"import", req.From, im.VM}
	} else {
		if !safeName.MatchString(req.From) {
			return nil, m.imageFailure(id, problem(ErrImage, "Unsupported local image source.", "Use a local Tart name, an absolute .tvm path, a sealed revision UUID or an oci:// reference."))
		}
		sourceHome := req.SourceHome
		if sourceHome == "" {
			home, _ := os.UserHomeDir()
			sourceHome = filepath.Join(home, ".tart")
		}
		if !filepath.IsAbs(sourceHome) || filepath.Clean(sourceHome) == filepath.Join(c.Storage.Data, "tart") {
			return nil, m.imageFailure(id, problem(ErrImage, "Source storage must be an external absolute Tart home.", "Use a sealed revision UUID for Runmoor-owned images."))
		}
		archive := importArchivePath(c, id)
		defer func() {
			if e := cleanupImportArchive(c, id); e != nil {
				result, err = nil, m.imageFailure(id, e)
			}
		}()
		env := append(minimalEnv(), "TART_HOME="+sourceHome, "TART_NO_AUTO_PRUNE=1")
		b, e := m.Tart.Exec.Run(ctx, c.TartExecutable, []string{"get", req.From, "--format", "json"}, env, nil)
		var v vmInfo
		if e != nil || jsonDecodeVM(b, &v) != nil || v.Running || v.State == "suspended" {
			return nil, m.imageFailure(id, problem(ErrImage, "The source image must be a stopped local VM.", "Stop the operator-owned source image before importing it."))
		}
		if _, e = m.Tart.Exec.Run(ctx, c.TartExecutable, []string{"export", req.From, archive}, env, nil); e != nil {
			return nil, m.imageFailure(id, problem(ErrImage, "Cannot export the operator-owned source image.", "Check source permissions and free space; its contents are never modified by Runmoor."))
		}
		args = []string{"import", archive, im.VM}
	}
	if _, e := m.Tart.run(ctx, c, args, nil); e != nil {
		return nil, m.imageFailure(id, e)
	}
	if _, e := m.Tart.run(ctx, c, []string{"set", im.VM, "--cpu", strconv.Itoa(req.Resources.CPU), "--memory", strconv.FormatInt(req.Resources.MemoryMiB, 10)}, nil); e != nil {
		return nil, m.imageFailure(id, e)
	}
	if e := m.Store.Update(func(s *Snapshot) error {
		s.Images[id].Phase = ImagePreparing
		s.Images[id].Source = im.Source
		return nil
	}); e != nil {
		return nil, e
	}
	return m.Store.View().Images[id], nil
}
func jsonDecodeVM(b []byte, v *vmInfo) error { return json.Unmarshal(b, v) }

func importArchivePath(c Config, id string) string {
	return filepath.Join(c.Storage.Data, "import-"+id+".tvm")
}

// The image UUID is persisted before export begins. Its deterministic archive
// path is therefore recoverable even if no post-export state update occurred.
// Never sweep matching filenames without that durable image ownership record.
func cleanupImportArchive(c Config, id string) error {
	if !validID(id) {
		return problem(ErrOwnership, "Import archive ownership is invalid.", "Preserve the image journal and restore its matching state backup.")
	}
	if err := privateDir(c.Storage.Data); err != nil {
		return err
	}
	archive := importArchivePath(c, id)
	info, err := os.Lstat(archive)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return problem(ErrCleanup, "Cannot inspect the temporary image export.", "Check managed data directory permissions and retry image cleanup.")
	}
	if !info.Mode().IsRegular() {
		return problem(ErrOwnership, "Temporary image export is not a regular file.", "Inspect the image's managed import archive; preserve unexpected directories and symlink targets.")
	}
	if err = os.Remove(archive); err != nil && !os.IsNotExist(err) {
		return problem(ErrCleanup, "Cannot remove the temporary image export.", "Check managed data directory permissions and retry image cleanup.")
	}
	return nil
}

func (m *ImageManager) Reconcile(ctx context.Context, c Config) error {
	var cleanupErr error
	for id, im := range m.Store.View().Images {
		// Manager serialization excludes active create/import operations here.
		// Include preparing, sealed and removing revisions, not just open VMs.
		if err := cleanupImportArchive(c, id); err != nil {
			cleanupErr = m.imageFailure(id, err)
			continue
		}
		if im.Phase != ImageOpen {
			continue
		}
		v, e := m.Tart.vm(ctx, c, im.VM)
		if e != nil {
			if err := m.Store.Update(func(s *Snapshot) error {
				if im := s.Images[id]; im != nil && im.Problem == nil {
					im.Problem = problem(ErrOwnership, "Image preparation state cannot be confirmed; its reservation remains held.", "Restore Tart connectivity and the matching private image data, then inspect image list. Preserve uncertain setup processes and ownership records.")
				}
				return nil
			}); err != nil {
				return err
			}
			continue
		}
		if !v.Running {
			if e = m.Store.Update(func(s *Snapshot) error {
				if im := s.Images[id]; im != nil && im.Phase == ImageOpen {
					im.Phase = ImagePreparing
				}
				return nil
			}); e != nil {
				return e
			}
		}
	}
	return cleanupErr
}
