package runmoor

import (
	"context"
	"encoding/json"
	"net/http"
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
	Store  *Store
	Tart   *TartDriver
	Client *http.Client
}

func (m *ImageManager) imageRunner(im *Image) Runner {
	s := m.Store.View()
	return Runner{ID: im.ID, Handle: Handle{VM: im.VM, PID: s.ImageTartPIDs[im.ID]}}
}

func (m *ImageManager) confirmImageTartExited(c Config, im *Image) error {
	s := m.Store.View()
	if err := m.Tart.confirmTartVMAbsent(c, m.imageRunner(im), s); err != nil {
		return err
	}
	return removeTartRunAlias(c, tartRunAlias(im.ID))
}

func (m *ImageManager) startTart(ctx context.Context, c Config, im *Image, installation string, args []string) (int, error) {
	pid, processStart, err := m.Tart.startOwned(ctx, c, im.VM, installation, im.ID, args)
	if pid > 0 {
		if persistErr := m.Store.Update(func(s *Snapshot) error {
			if s.Images[im.ID] == nil {
				return staleUpdate()
			}
			if s.ImageTartPIDs == nil {
				s.ImageTartPIDs = map[string]int{}
			}
			s.ImageTartPIDs[im.ID] = pid
			if s.ImageTartStarts == nil {
				s.ImageTartStarts = map[string]string{}
			}
			s.ImageTartStarts[im.ID] = processStart
			return nil
		}); persistErr != nil {
			return pid, persistErr
		}
	}
	return pid, err
}

func (m *ImageManager) stopTart(ctx context.Context, c Config, im *Image, s Snapshot) error {
	if err := m.Tart.Stop(ctx, c, m.imageRunner(im), s); err != nil {
		return err
	}
	return m.Store.Update(func(snapshot *Snapshot) error {
		delete(snapshot.ImageTartPIDs, im.ID)
		delete(snapshot.ImageTartStarts, im.ID)
		return nil
	})
}

func (m *ImageManager) Operate(ctx context.Context, c Config, req ImageRequest) (*Image, error) {
	if e := m.Tart.check(ctx, c); e != nil {
		return nil, e
	}
	if req.Action == "create" {
		var e error
		req.Resources, e = defaultImageResources(c, req.Resources)
		if e != nil {
			return nil, e
		}
		return m.create(ctx, c, req)
	}
	s := m.Store.View()
	im := s.Images[req.ID]
	if im == nil {
		return nil, problem(ErrImage, "Image revision does not exist.", "Use 'runmoor image list' to select an existing revision.")
	}
	if err := promoteCreatedTartVM(c, im.VM, s.Installation, im.ID); err != nil {
		return nil, err
	}
	vmAbsent := false
	if e := verifyVMOwner(c, im.VM, s.Installation, im.ID); e != nil {
		if req.Action == "remove" {
			var absenceErr error
			vmAbsent, absenceErr = vmCanBeRemovedAsAbsent(c, im.VM, s.Installation, im.ID)
			if absenceErr != nil {
				return nil, absenceErr
			}
		}
		if !vmAbsent {
			return nil, e
		}
	}
	switch req.Action {
	case "close":
		if im.Phase != ImageOpen {
			return im, nil
		}
		ctx, cancel := context.WithTimeout(ctx, c.Preparation(Tart))
		defer cancel()
		if err := m.stopTart(ctx, c, im, s); err != nil {
			return nil, m.imageFailure(im.ID, err)
		}
		if err := m.Store.Update(func(v *Snapshot) error {
			v.Images[im.ID].Phase = ImagePreparing
			return nil
		}); err != nil {
			return nil, err
		}
		return m.Store.View().Images[im.ID], nil
	case "probe":
		if im.Phase != ImageOpen {
			return nil, problem(ErrImage, "Setup image is not open.", "Open the image before checking guest readiness.")
		}
		err := m.Tart.preparedGuestOwned(ctx, c, im.VM, "/Users/runner/actions-runner", s.Installation, im.ID)
		if p, ok := err.(*Problem); ok && p.Code == ErrOwnership {
			return im, m.imageFailure(im.ID, err)
		}
		return im, err
	case "verify-boot":
		if im.Phase != ImageOpen {
			return nil, problem(ErrImage, "Setup image is not open.", "Open the image before verifying boot readiness.")
		}
		ctx, cancel := context.WithTimeout(ctx, c.Preparation(Tart))
		defer cancel()
		if err := m.stopTart(ctx, c, im, s); err != nil {
			return nil, m.imageFailure(im.ID, err)
		}
		if _, err := m.startTart(ctx, c, im, s.Installation, []string{"run", "--no-graphics", "--no-audio", im.VM}); err != nil {
			if p, ok := err.(*Problem); ok && p.Code == ErrOwnership {
				return nil, m.imageFailure(im.ID, err)
			}
			return nil, m.imageFailure(im.ID, problem(ErrPreparation, "Cannot restart the setup VM.", "Inspect Tart and reopen the owned setup image."))
		}
		for {
			v, err := m.Tart.vmOwned(ctx, c, im.VM, s.Installation, im.ID)
			if err == nil && v.Running {
				guestErr := m.Tart.preparedGuestOwned(ctx, c, im.VM, "/Users/runner/actions-runner", s.Installation, im.ID)
				if guestErr == nil {
					return im, nil
				}
				if p, ok := guestErr.(*Problem); ok && p.Code == ErrImage {
					return nil, m.imageFailure(im.ID, p)
				}
				if p, ok := guestErr.(*Problem); ok && p.Code == ErrOwnership {
					return nil, m.imageFailure(im.ID, p)
				}
			}
			if p, ok := err.(*Problem); ok && p.Code == ErrOwnership {
				return nil, m.imageFailure(im.ID, err)
			}
			if !waitContext(ctx, time.Second) {
				return nil, m.imageFailure(im.ID, problem(ErrPreparation, "Guest Agent did not start after a clean boot.", "Enable Guest Agent 0.14.2 RPC for the logged-in runner account, then retry setup."))
			}
		}
	case "open":
		ctx, cancel := context.WithTimeout(ctx, c.Preparation(Tart))
		defer cancel()
		if im.Phase == ImageSealed || im.Phase == ImageImported || im.Phase == ImageRemoving {
			return nil, problem(ErrImage, "A sealed or removing revision cannot be opened for setup.", "Create a new revision with --from pointing at the sealed revision UUID.")
		}
		if im.Phase == ImageOpen {
			v, e := m.Tart.vmOwned(ctx, c, im.VM, s.Installation, im.ID)
			if e != nil {
				if p, ok := e.(*Problem); ok && p.Code == ErrOwnership {
					return nil, m.imageFailure(im.ID, e)
				}
				return nil, e
			}
			if v.Running {
				return im, nil
			}
			if e := m.confirmImageTartExited(c, im); e != nil {
				return nil, m.imageFailure(im.ID, problem(ErrOwnership, "The previous setup Tart process has not been confirmed stopped.", "Preserve its VM reservation and retry image open after the recorded Tart process exits."))
			}
		}
		if e := m.reserve(c, im.ID); e != nil {
			return nil, e
		}
		if _, e := m.startTart(ctx, c, im, s.Installation, []string{"run", "--no-audio", im.VM}); e != nil {
			if p, ok := e.(*Problem); ok && p.Code == ErrOwnership {
				return nil, m.imageFailure(im.ID, e)
			}
			return nil, m.imageFailure(im.ID, problem(ErrPreparation, "Cannot open the setup VM.", "Inspect Tart and close unused setup VMs before retrying."))
		}
		// A detached process is not proof of boot. Keep its reservation until
		// Tart confirms the VM running or reports an actionable startup failure.
		for {
			v, e := m.Tart.vmOwned(ctx, c, im.VM, s.Installation, im.ID)
			if e == nil && v.Running {
				break
			}
			if p, ok := e.(*Problem); ok && p.Code == ErrOwnership {
				return nil, m.imageFailure(im.ID, e)
			}
			if !waitContext(ctx, 250*time.Millisecond) {
				return nil, m.imageFailure(im.ID, problem(ErrPreparation, "Setup VM startup was not confirmed before the preparation deadline.", "Inspect Tart boot compatibility and image list. The reservation remains until the VM is confirmed stopped; retry image open after correcting the failure."))
			}
		}
	case "seal":
		if im.Phase == ImageImported {
			return nil, problem(ErrImage, "An imported managed source is immutable.", "Create a separate revision from its UUID before changing it.")
		}
		ctx, cancel := context.WithTimeout(ctx, c.Preparation(Tart))
		defer cancel()
		if im.Phase == ImageSealed {
			return im, nil
		}
		automatic := req.RunnerVersion == "" || req.RunnerVersion == LatestRunner
		if !automatic && !versionPattern.MatchString(req.RunnerVersion) {
			return nil, problem(ErrConfig, "Runner version must be latest or an exact version.", "Omit --runner-version to install the latest runner.")
		}
		if req.RunnerPath == "" {
			req.RunnerPath = "/Users/runner/actions-runner"
		}
		if !validRunnerPath(req.RunnerPath) {
			return nil, problem(ErrConfig, "Runner path must be an absolute clean guest path.", "Use the same absolute guest directory as runner_path in TOML, without '..', NUL or line breaks.")
		}
		v, e := m.Tart.vmOwned(ctx, c, im.VM, s.Installation, im.ID)
		if e != nil {
			return nil, m.imageFailure(im.ID, e)
		}
		if e = validateTartGuestOS(v.OS); e != nil {
			return nil, m.imageFailure(im.ID, e)
		}
		if e := m.reserve(c, im.ID); e != nil {
			return nil, e
		}
		if !v.Running {
			if _, e = m.startTart(ctx, c, im, s.Installation, []string{"run", "--no-graphics", "--no-audio", im.VM}); e != nil {
				if p, ok := e.(*Problem); ok && p.Code == ErrOwnership {
					return nil, m.imageFailure(im.ID, e)
				}
				return nil, m.imageFailure(im.ID, problem(ErrPreparation, "Cannot boot the image for validation.", "Check Tart and image compatibility."))
			}
		}
		if automatic {
			if !managedPathValid(req.RunnerPath) {
				return nil, m.imageFailure(im.ID, problem(ErrConfig, "Automatic installation requires a dedicated clean runner directory.", "Use the default runner_path."))
			}
			cli := m.Client
			if cli == nil {
				cli = defaultReleaseClient()
			}
			probe, cancel := context.WithTimeout(ctx, 10*time.Second)
			releases, e := fetchRunnerReleases(probe, cli)
			cancel()
			if e != nil {
				return nil, m.imageFailure(im.ID, e)
			}
			if len(releases) == 0 {
				return nil, m.imageFailure(im.ID, releaseProblem())
			}
			if e = cleanupRunnerDownload(c, im.ID); e != nil {
				return nil, m.imageFailure(im.ID, e)
			}
			builder := ManagedImageBuilder{Store: m.Store, Images: m, Client: cli}
			archive, e := builder.archive(ctx, c, Pool{Backend: Tart, Arch: "arm64"}, RunnerArtifact{ID: im.ID}, releases[0])
			if e != nil {
				return nil, m.imageFailure(im.ID, e)
			}
			if e = builder.installTartArchive(ctx, c, im.VM, req.RunnerPath, archive, s.Installation, im.ID); e != nil {
				return nil, m.imageFailure(im.ID, e)
			}
			req.RunnerVersion = releases[0].Version()
		}
		if e = m.Tart.guestReadyOwned(ctx, c, im.VM, req.RunnerPath, req.RunnerVersion, s.Installation, im.ID); e != nil {
			return nil, m.imageFailure(im.ID, e)
		}
		if e = m.stopTart(ctx, c, im, s); e != nil {
			return nil, m.imageFailure(im.ID, e)
		}
		digest, e := imageDigestOwned(ctx, c, im.VM, s.Installation, im.ID)
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
		if artifactReferenced(s, im.ID) {
			return nil, problem(ErrImageInUse, "An automatic runner configuration references this image.", "Change or remove the pool and allow managed retention to complete.")
		}
		if vmAbsent {
			if err := m.confirmImageTartExited(c, im); err != nil {
				return nil, m.imageFailure(im.ID, err)
			}
		}
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
			if v.Images[im.ID].Phase == ImageOpen && !vmAbsent {
				return problem(ErrImageInUse, "The image is open for setup or validation.", "Close its Tart window and wait for it to stop before removal.")
			}
			v.Images[im.ID].Phase = ImageRemoving
			return nil
		}); e != nil {
			return nil, e
		}
		if e := cleanupRunnerDownload(c, im.ID); e != nil {
			return nil, m.imageFailure(im.ID, e)
		}
		if e := cleanupImportArchive(c, im.ID); e != nil {
			return nil, m.imageFailure(im.ID, e)
		}
		if e := m.Tart.Cleanup(ctx, c, m.imageRunner(im), s); e != nil {
			return nil, m.imageFailure(im.ID, e)
		}
		if e := m.Store.Update(func(v *Snapshot) error {
			delete(v.Images, im.ID)
			delete(v.ImageTartPIDs, im.ID)
			delete(v.ImageTartStarts, im.ID)
			return nil
		}); e != nil {
			return nil, e
		}
		return im, nil
	default:
		return nil, problem(ErrConfig, "Unknown image operation.", "Use create, open, seal, list or remove.")
	}
	return m.Store.View().Images[im.ID], nil
}

func vmCanBeRemovedAsAbsent(c Config, name, installation, entity string) (bool, error) {
	if !safeName.MatchString(name) {
		return false, ambiguousVMOwnership()
	}
	if err := promoteCreatedTartVM(c, name, installation, entity); err != nil {
		return false, err
	}
	if _, err := os.Lstat(vmPath(c, name)); !os.IsNotExist(err) {
		if err != nil {
			return false, problem(ErrPermission, "Cannot inspect the Tart VM directory.", "Preserve the image and check private data directory permissions.")
		}
		return false, nil
	}
	if _, err := os.Lstat(vmOwnerPath(c, name)); os.IsNotExist(err) {
		return true, nil
	} else if err != nil {
		return false, problem(ErrPermission, "Cannot inspect the Tart VM ownership record.", "Preserve the image and check private data directory permissions.")
	}
	if err := verifyVMOwnerRecord(c, name, installation, entity); err != nil {
		return false, err
	}
	return true, nil
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
		budget := cloneSnapshot(*s)
		delete(budget.Artifacts, id)
		used, count, vms := usage(budget)
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
	snapshot := m.Store.View()
	image := snapshot.Images[id]
	released := false
	if image != nil && image.Phase == ImageOpen {
		if _, statErr := os.Lstat(vmPath(snapshot.Config, image.VM)); os.IsNotExist(statErr) {
			released = m.confirmImageTartExited(snapshot.Config, image) == nil
		}
	}
	_ = m.Store.Update(func(s *Snapshot) error {
		if i := s.Images[id]; i != nil {
			i.Problem = p
			if released && i.Phase == ImageOpen {
				i.Phase = ImagePreparing
				delete(s.ImageTartPIDs, id)
				delete(s.ImageTartStarts, id)
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
	if req.IPSW != "" && req.IPSW != "latest" && (!filepath.IsAbs(req.IPSW) || !strings.HasSuffix(strings.ToLower(req.IPSW), ".ipsw")) {
		return nil, problem(ErrConfig, "IPSW input must be 'latest' or a local absolute .ipsw path.", "Use 'latest' for the newest restore image supported by this Mac, or provide a downloaded .ipsw path.")
	}
	id := req.ID
	if id == "" {
		id = newID()
	} else if !validID(id) {
		return nil, problem(ErrConfig, "Invalid preparation identity.", "Retry the managed image operation.")
	}
	im := &Image{ID: id, Name: req.Name, Phase: ImagePreparing, VM: "rm-image-" + id, Resources: req.Resources, Source: req.From, CreatedAt: nowUTC()}
	if req.IPSW != "" {
		im.Source = req.IPSW
	}
	if e := m.Store.Update(func(s *Snapshot) error {
		if s.Images[id] != nil {
			return problem(ErrOwnership, "Image identity already exists.", "Use a fresh image revision.")
		}
		s.Images[id] = im
		return nil
	}); e != nil {
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
	creationHome, creationLock, e := prepareTartCreationHome(c, id)
	if e != nil {
		return nil, m.imageFailure(id, e)
	}
	defer func() {
		_ = creationLock.Close()
		cleanupTartCreationHome(c, id)
	}()
	stageVM := creationVMName(id)
	var args []string
	var cloneSource *Image
	if req.IPSW != "" {
		args = []string{"create", "--from-ipsw", req.IPSW, stageVM}
	} else if source := s.Images[req.From]; source != nil && (source.Phase == ImageSealed || source.Phase == ImageImported) {
		if e := m.Tart.validateSealed(ctx, c, source, s.Installation); e != nil {
			return nil, m.imageFailure(id, e)
		}
		cloneSource = source
		args = []string{"clone", source.VM, stageVM}
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
		args = []string{"clone", resolved, stageVM}
	} else if filepath.IsAbs(req.From) && strings.HasSuffix(req.From, ".tvm") {
		args = []string{"import", req.From, stageVM}
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
		args = []string{"import", archive, stageVM}
	}
	var createErr error
	if cloneSource != nil {
		_, createErr = m.Tart.runOwnedAtHome(ctx, c, creationHome, cloneSource.VM, cloneSource.VM, s.Installation, cloneSource.ID, args, nil)
	} else {
		_, createErr = m.Tart.runAtHome(ctx, c, creationHome, args, nil)
	}
	if createErr != nil {
		return nil, m.imageFailure(id, createErr)
	}
	if e := publishAndMoveCreatedTartVM(c, creationHome, stageVM, im.VM, s.Installation, id); e != nil {
		return nil, m.imageFailure(id, e)
	}
	if _, e := m.Tart.runOwned(ctx, c, s.Installation, id, im.VM, []string{"set", im.VM, "--cpu", strconv.Itoa(req.Resources.CPU), "--memory", strconv.FormatInt(req.Resources.MemoryMiB, 10)}, nil); e != nil {
		return nil, m.imageFailure(id, e)
	}
	if e := m.Store.Update(func(s *Snapshot) error {
		s.Images[id].Phase = ImagePreparing
		s.Images[id].CreationComplete = true
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
	snapshot := m.Store.View()
	for id, im := range snapshot.Images {
		// Manager serialization excludes active create/import operations here.
		// Include preparing, sealed and removing revisions, not just open VMs.
		if err := cleanupRunnerDownload(c, id); err != nil {
			cleanupErr = m.imageFailure(id, err)
			continue
		}
		if err := cleanupImportArchive(c, id); err != nil {
			cleanupErr = m.imageFailure(id, err)
			continue
		}
		if err := promoteCreatedTartVM(c, im.VM, snapshot.Installation, im.ID); err != nil {
			cleanupErr = m.imageFailure(id, err)
			continue
		}
		if im.Phase != ImageOpen {
			continue
		}
		v, e := m.Tart.vmOwned(ctx, c, im.VM, snapshot.Installation, im.ID)
		if e != nil {
			if _, statErr := os.Lstat(vmPath(c, im.VM)); os.IsNotExist(statErr) && m.confirmImageTartExited(c, im) == nil {
				if err := m.Store.Update(func(s *Snapshot) error {
					if current := s.Images[id]; current != nil && current.Phase == ImageOpen {
						current.Phase = ImagePreparing
						if current.Problem == nil {
							current.Problem = problem(ErrImage, "The owned setup VM is absent after its Tart process exited.", "Remove the image revision or restore its matching Tart VM data.")
						}
						delete(s.ImageTartPIDs, id)
						delete(s.ImageTartStarts, id)
					}
					return nil
				}); err != nil {
					return err
				}
				continue
			}
			ownershipFailure := false
			if p, ok := e.(*Problem); ok {
				ownershipFailure = p.Code == ErrOwnership
			}
			if err := m.Store.Update(func(s *Snapshot) error {
				if im := s.Images[id]; im != nil && (im.Problem == nil || ownershipFailure && im.Problem.Code != ErrOwnership) {
					im.Problem = problem(ErrOwnership, "Image preparation state cannot be confirmed; its reservation remains held.", "Restore Tart connectivity and the matching private image data, then inspect image list. Preserve uncertain setup processes and ownership records.")
				}
				return nil
			}); err != nil {
				return err
			}
			continue
		}
		if !v.Running {
			if err := m.confirmImageTartExited(c, im); err != nil {
				cleanupErr = m.imageFailure(id, problem(ErrOwnership, "Tart has not confirmed the setup process exited.", "Preserve the VM and reservation, then retry image reconciliation."))
				continue
			}
			if e = m.Store.Update(func(s *Snapshot) error {
				if im := s.Images[id]; im != nil && im.Phase == ImageOpen {
					im.Phase = ImagePreparing
				}
				delete(s.ImageTartPIDs, id)
				delete(s.ImageTartStarts, id)
				return nil
			}); e != nil {
				return e
			}
		}
	}
	return cleanupErr
}
