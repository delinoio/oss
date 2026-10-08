package runmoor

import (
	"context"
	"log/slog"
	"time"
)

type serviceManagerIdentity struct {
	pid   int
	start string
}

func serviceManagerUnavailable() error {
	slog.Warn("service_shutdown_authority_unavailable")
	return problem(ErrControl, "Cannot verify the original service manager for shutdown.", "Recover the manager with its original configuration and storage, then retry service stop or uninstall. Preserve managed data and recovery files.")
}

func (r *serviceReloader) admitServiceManager(ctx context.Context, definition []byte) (serviceManagerIdentity, error) {
	pid, loaded, err := r.nativePID(ctx)
	if err != nil {
		return serviceManagerIdentity{}, err
	}
	if r.Platform == "darwin" && loaded && pid == 0 {
		return serviceManagerIdentity{}, problem(ErrDependency, "Cannot verify the loaded launchd service while it is inactive.", "Inspect the loaded job in the logged-in GUI session, reconcile it with the installed plist, then retry service stop or uninstall.")
	}
	identity := serviceManagerIdentity{pid: pid}
	if pid == 0 {
		return identity, nil
	}
	identity.start, err = r.ProcessStart(pid)
	if err != nil || identity.start == "" {
		return identity, serviceManagerUnavailable()
	}
	args, err := reloadArguments(r.Platform, definition)
	if err != nil || r.invocation(pid, args) != nil {
		return identity, problem(ErrConfig, "The active service does not match its installed definition.", "Gracefully stop the active manager with its original configuration, then retry the service action.")
	}
	if err = r.checkServiceManager(ctx, identity); err != nil {
		return identity, err
	}
	return identity, nil
}

// An inactive observation never grants authority over a manager that appears
// later. An admitted process may exit after draining, but cannot be replaced.
func (r *serviceReloader) checkServiceManager(ctx context.Context, identity serviceManagerIdentity) error {
	pid, loaded, err := r.nativePID(ctx)
	if err != nil || r.Platform == "darwin" && loaded && pid == 0 {
		return serviceManagerUnavailable()
	}
	if pid == 0 {
		return nil
	}
	if pid != identity.pid {
		return serviceManagerUnavailable()
	}
	start, err := r.ProcessStart(pid)
	if err != nil || start == "" || start != identity.start {
		return serviceManagerUnavailable()
	}
	return nil
}

func (r *serviceReloader) drainServiceManager(ctx context.Context, c Config, identity serviceManagerIdentity) error {
	if err := r.checkServiceManager(ctx, identity); err != nil {
		return err
	}
	if identity.pid == 0 {
		// Only confirmed native inactivity permits offline proof. Never consult
		// candidate storage after an active peer becomes unreachable.
		store, err := OpenStore(c)
		if err != nil {
			return err
		}
		snapshot := store.View()
		store.Close()
		if !allTerminated(snapshot) {
			return serviceManagerUnavailable()
		}
		return r.checkServiceManager(ctx, identity)
	}
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	initial, peer, err := r.Control(probe, c, ControlRequest{Action: "status"}, identity.pid)
	cancel()
	if err != nil || peer != identity.pid || initial.Status == nil || !validID(initial.Status.Generation) {
		return serviceManagerUnavailable()
	}
	generation := initial.Status.Generation
	if err := r.checkServiceManager(ctx, identity); err != nil {
		return err
	}
	if _, peer, err := r.Control(ctx, c, ControlRequest{Action: "stop"}, identity.pid); err != nil || peer != identity.pid {
		return serviceManagerUnavailable()
	}
	for {
		if err := r.checkServiceManager(ctx, identity); err != nil {
			return err
		}
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		response, peer, err := r.Control(probe, c, ControlRequest{Action: "status"}, identity.pid)
		cancel()
		if err != nil {
			pid, loaded, nativeErr := r.nativePID(ctx)
			if nativeErr != nil || pid != 0 || r.Platform == "darwin" && loaded {
				return serviceManagerUnavailable()
			}
			// Stop was accepted by the original peer. Only its confirmed exited
			// generation can now supply offline completion, never a fresh store.
			snapshot, storeErr := ReadSnapshot(c)
			if storeErr != nil {
				return serviceManagerUnavailable()
			}
			if snapshot.Generation != generation || !snapshot.Stopping || !allTerminated(snapshot) {
				return serviceManagerUnavailable()
			}
			return r.checkServiceManager(ctx, serviceManagerIdentity{})
		}
		if peer != identity.pid || response.Status == nil || response.Status.Generation != generation || !response.Status.Stopping {
			return serviceManagerUnavailable()
		}
		active := false
		for _, image := range response.Status.Images {
			if image.Phase == ImageOpen || image.Phase == ImageRemoving {
				active = true
			}
		}
		for _, runner := range response.Status.Runners {
			if !runner.Terminated || !runner.LocalCleaned {
				active = true
			}
		}
		if !active {
			return r.checkServiceManager(ctx, identity)
		}
		if !r.Wait(ctx) {
			return serviceManagerUnavailable()
		}
	}
}
