package runmoor

import (
	"context"
	"encoding/json"
	"io"
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
	for _, p := range c.Pools {
		probe, cancel := context.WithTimeout(ctx, 30*time.Second)
		driver, e := drivers(p.Backend)
		if e == nil {
			e = driver.Validate(probe, c, p, s)
		}
		add("backend_and_image", p.Name, e, false)
		remote, e := factory(c.Connection(p.Connection))
		if e == nil {
			e = remote.Check(probe, p)
		}
		add("github_authentication", p.Name, e, false)
		cancel()
	}
	versions := map[string]bool{}
	for _, p := range c.Pools {
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
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/actions/runner/releases?per_page=100", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "runmoor/"+Version)
	resp, e := client.Do(req)
	if e != nil {
		return problem(ErrRetry, "Runner release freshness is unknown while GitHub is unavailable.", "Check the pinned runner release manually; no automatic update is performed.")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return problem(ErrRetry, "Runner release freshness could not be checked.", "Retry doctor after GitHub rate limits or connectivity recover.")
	}
	// The extra byte distinguishes size exhaustion from malformed upstream JSON;
	// never decode an incomplete prefix, even if it contains a complete value.
	body, e := io.ReadAll(io.LimitReader(resp.Body, runnerReleaseResponseLimit+1))
	if len(body) > runnerReleaseResponseLimit {
		return problem(ErrRetry, "Runner release metadata exceeds the 8 MiB response limit.", "Check the official actions/runner release notes manually and retry doctor later; no automatic update is performed.")
	}
	if e != nil {
		return problem(ErrRetry, "Runner release metadata could not be read.", "Retry doctor after GitHub connectivity recovers; check the pinned runner release manually in the meantime.")
	}
	var releases []struct {
		Tag        string    `json:"tag_name"`
		Published  time.Time `json:"published_at"`
		Draft      bool      `json:"draft"`
		Prerelease bool      `json:"prerelease"`
	}
	if json.Unmarshal(body, &releases) != nil {
		return problem(ErrRetry, "Runner release metadata is invalid.", "Check the official actions/runner release notes.")
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
