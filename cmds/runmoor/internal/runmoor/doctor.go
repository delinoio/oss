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

type Check struct {
	Name    string   `json:"name"`
	Pool    string   `json:"pool,omitempty"`
	OK      bool     `json:"ok"`
	Warning bool     `json:"warning,omitempty"`
	Problem *Problem `json:"error,omitempty"`
}
type DoctorReport struct {
	SchemaVersion int     `json:"schema_version"`
	Version       string  `json:"version"`
	Checks        []Check `json:"checks"`
}

func Doctor(ctx context.Context, c Config, s Snapshot, factory func(Connection) (Remote, error), drivers DriverFactory) DoctorReport {
	r := DoctorReport{SchemaVersion: 1, Version: Version, Checks: []Check{}}
	add := func(name, pool string, err error, warning bool) {
		v := Check{Name: name, Pool: pool, OK: err == nil, Warning: warning}
		if err != nil {
			v.Problem = classify(err, ErrDependency, "Dependency check failed.", "Inspect the documented compatibility requirements.")
		}
		r.Checks = append(r.Checks, v)
	}
	add("platform", "", platformCheck(ctx), false)
	add("disk_reserve", "", diskCheck(c), false)
	name, _ := powerCommand()
	_, err := exec.LookPath(name)
	if err != nil {
		err = problem(ErrPower, "Sleep inhibition utility is unavailable.", "Install or enable the documented OS sleep inhibition utility.")
	}
	add("sleep_inhibition", "", err, true)
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
	var releases []struct {
		Tag        string    `json:"tag_name"`
		Published  time.Time `json:"published_at"`
		Draft      bool      `json:"draft"`
		Prerelease bool      `json:"prerelease"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&releases) != nil {
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
