package runmoor

import (
	"context"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Latest-100 release responses include descriptions and assets that can exceed
// 2 MiB. Keep an explicit budget while allowing ordinary release metadata.
const runnerReleaseResponseLimit = 8 << 20

type Check struct {
	Name    string   `json:"name"`
	Image   string   `json:"image,omitempty"`
	Pool    string   `json:"pool,omitempty"`
	OK      bool     `json:"ok"`
	Warning bool     `json:"warning,omitempty"`
	Problem *Problem `json:"error,omitempty"`
}
type DoctorReport struct {
	SchemaVersion int     `json:"schema_version"`
	Version       string  `json:"version"`
	Checks        []Check `json:"checks"`
	Status        *Status `json:"status,omitempty"`
}

func Doctor(ctx context.Context, c Config, s Snapshot, factory func(Connection) (Remote, error), drivers DriverFactory) DoctorReport {
	r := DoctorReport{SchemaVersion: 1, Version: Version, Checks: []Check{}}
	if s.Installation != "" {
		r.Status = statusOf(s, false)
	}
	add := func(name, pool string, err error, warning bool) {
		v := Check{Name: name, Pool: pool, OK: err == nil, Warning: warning}
		if err != nil {
			v.Problem = classify(err, ErrDependency, "Dependency check failed.", "Inspect the documented compatibility requirements.")
		}
		r.Checks = append(r.Checks, v)
	}
	add("platform", "", platformCheck(ctx), false)
	add("disk_reserve", "", diskCheck(c), false)
	if s.Config.DockerCapacityPending {
		add("docker_capacity", "", problem(ErrRetry, "Docker capacity has not been verified for this manager run.", "Restore Docker connectivity; capacity detection retries automatically."), false)
	}
	name, args := powerCommand()
	if runtime.GOOS == "darwin" {
		args = []string{"-i", "/usr/bin/true"}
	} else {
		args[len(args)-1] = "/bin/true"
	}
	_, err := exec.LookPath(name)
	if err != nil {
		err = problem(ErrPower, "Sleep inhibition utility is unavailable.", "Install or enable the documented OS sleep inhibition utility.")
	}

	if err == nil {
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err = (OSCommand{}).Run(probe, name, args, minimalEnv(), nil)
		cancel()
		if err != nil {
			err = problem(ErrPower, "OS sleep inhibition could not be acquired in this user session.", "Check power-management permissions; work may continue without guaranteed sleep inhibition.")
		}
	}
	add("sleep_inhibition", "", err, true)
	for _, p := range s.Pools {
		if p.Problem != nil {
			add("pool_state", p.Spec.Name, p.Problem, false)
		}
	}
	for _, runner := range s.Runners {
		if runner.Phase != Completed && runner.Problem != nil {
			add("execution_recovery", runner.PoolID, runner.Problem, false)
		}
	}

	for _, im := range s.Images {
		if im.Problem != nil {
			r.Checks = append(r.Checks, Check{Name: "image_recovery", Image: im.ID, Problem: im.Problem})
		}
	}
	for _, q := range s.Managed {
		err := error(q.Problem)
		if q.Problem == nil {
			err = nil
		}
		if q.Current == nil && err == nil {
			err = problem(ErrRetry, "Managed runner image has not been prepared yet.", "Start the manager and inspect runner update status.")
		}
		if !q.Expires.IsZero() && !time.Now().Before(q.Expires) {
			err = problem(ErrRunnerVersion, "The managed runner passed its known support deadline.", "Restore update dependencies; new work waits for a supported image.")
		}
		add("runner_update", q.Name, err, q.Current != nil && (q.Expires.IsZero() || time.Now().Before(q.Expires)))
	}
	for _, p := range c.Pools {
		poolConfig := c
		if managesRunner(p) {
			managed := s.Managed[p.Name]
			if managed == nil {
				add("runner_update", p.Name, problem(ErrRetry, "Managed runner image is awaiting preparation.", "Start the manager; doctor does not download images."), false)
				continue
			}
			if managed.Current == nil {
				// The managed-state check above already reports pending preparation.
				continue
			}
			// Offline callers supply requested TOML, whose latest/source fields
			// remain unresolved. Inspect the same committed environment as a live
			// manager without downloading or replacing any image.
			p = *managed.Current
			poolConfig = s.Config
		}
		probe, cancel := context.WithTimeout(ctx, 30*time.Second)
		driver, e := drivers(p.Backend)
		if e == nil {
			e = driver.Validate(probe, poolConfig, p, s)
		}
		add("backend_and_image", p.Name, e, false)
		remote, e := factory(poolConfig.Connection(p.Connection))
		if e == nil {
			e = remote.Check(probe, p)
		}
		add("github_authentication", p.Name, e, false)
		cancel()
	}
	versions := map[string]bool{}
	for _, p := range c.Pools {
		if managesRunner(p) || s.Managed[p.Name] != nil {
			continue
		}
		if versions[p.RunnerVersion] {
			continue
		}
		versions[p.RunnerVersion] = true
		probe, cancel := context.WithTimeout(ctx, 10*time.Second)
		e := checkRunnerVersion(probe, p.RunnerVersion, http.DefaultClient)
		cancel()
		warning := true
		if v, ok := e.(*Problem); ok && v.Code == ErrRunnerVersion {
			warning = false
		}
		add("runner_version", p.Name, e, warning)
	}
	return r
}
func checkRunnerVersion(ctx context.Context, pinned string, client *http.Client) error {
	releases, e := fetchRunnerReleases(ctx, client)
	if e != nil {
		return e
	}
	var olderUpdate time.Time
	found := false
	for _, r := range releases {
		if r.Draft || r.Prerelease {
			continue
		}
		if strings.TrimPrefix(r.Tag, "v") == pinned {
			found = true
			break
		}
		if olderUpdate.IsZero() || r.Published.Before(olderUpdate) {
			olderUpdate = r.Published
		}
	}
	if !found || (!olderUpdate.IsZero() && time.Since(olderUpdate) > 30*24*time.Hour) {
		return problem(ErrRunnerVersion, "Pinned runner is outside the documented update window or is not a known release.", "Prepare and select a new digest or sealed image with a supported runner; never update a running job environment.")
	}
	if !olderUpdate.IsZero() {
		return problem(ErrRetry, "A newer runner release is available.", "Prepare a replacement pinned image within GitHub's update window; critical updates may be required sooner.")
	}
	return nil
}
func supportedBuild() bool {
	return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" || runtime.GOOS == "linux" && (runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64")
}
