package runmoor

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type ControlRequest struct {
	Action string        `json:"action"`
	Pool   string        `json:"pool,omitempty"`
	Force  bool          `json:"force,omitempty"`
	Image  *ImageRequest `json:"image,omitempty"`
}
type ControlResponse struct {
	SchemaVersion int           `json:"schema_version"`
	Status        *Status       `json:"status,omitempty"`
	Image         *Image        `json:"image,omitempty"`
	Images        []*Image      `json:"images,omitempty"`
	Problem       *Problem      `json:"error,omitempty"`
	Doctor        *DoctorReport `json:"doctor,omitempty"`
}
type Status struct {
	SchemaVersion  int          `json:"schema_version"`
	Version        string       `json:"version"`
	Running        bool         `json:"manager_running"`
	Generation     string       `json:"generation"`
	Paused         bool         `json:"paused"`
	Stopping       bool         `json:"stopping"`
	Budget         Budget       `json:"budget"`
	Reserved       Resources    `json:"reserved"`
	Active         int          `json:"active_runners_and_setup_vms"`
	VMs            int          `json:"macos_vms"`
	PendingCleanup int          `json:"pending_cleanup"`
	Pools          []PoolStatus `json:"pools"`
	Runners        []Runner     `json:"runners"`
	Power          *Problem     `json:"power_warning,omitempty"`
}
type PoolStatus struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Generation string    `json:"generation"`
	Phase      PoolPhase `json:"phase"`
	Backend    Backend   `json:"backend"`
	ScaleSet   string    `json:"scale_set"`
	Demand     int       `json:"demand"`
	Total      int       `json:"total"`
	Busy       int       `json:"busy"`
	Problem    *Problem  `json:"problem,omitempty"`
}

func statusOf(s Snapshot, running bool) *Status {
	used, n, vms := usage(s)
	v := &Status{SchemaVersion: 1, Version: Version, Running: running, Generation: s.Generation, Paused: s.Paused, Stopping: s.Stopping, Budget: s.Config.Host, Reserved: used, Active: n, VMs: vms, PendingCleanup: pendingCleanup(s), Pools: []PoolStatus{}, Runners: []Runner{}, Power: s.PowerProblem}
	for _, p := range sortedPools(s) {
		if p.Phase == Retired {
			continue
		}
		n, b := liveCount(s, p.ID)
		v.Pools = append(v.Pools, PoolStatus{p.ID, p.Spec.Name, p.Generation, p.Phase, p.Spec.Backend, p.Spec.ScaleSet, p.Demand, n, b, p.Problem})
	}
	for _, r := range s.Runners {
		if r.Phase != Completed {
			v.Runners = append(v.Runners, *r)
		}
	}
	sort.Slice(v.Runners, func(i, j int) bool { return v.Runners[i].ID < v.Runners[j].ID })
	return v
}
func (m *Manager) ServeControl() (*http.Server, error) {
	c := m.Store.View().Config
	path := filepath.Join(c.Storage.State, "control.sock")
	if st, e := os.Lstat(path); e == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return nil, problem(ErrPermission, "Control socket path contains another kind of file.", "Preserve the file and choose a clean state directory.")
		}
		if e = os.Remove(path); e != nil {
			return nil, e
		}
	}
	listener, e := net.Listen("unix", path)
	if e != nil {
		return nil, problem(ErrControl, "Cannot listen on the private control socket.", "Check state path permissions and Unix socket length.")
	}
	if e = os.Chmod(path, 0600); e != nil {
		listener.Close()
		return nil, e
	}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, MaxHeaderBytes: 8192, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/v1/control" {
			http.NotFound(w, r)
			return
		}
		var req ControlRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		dec.DisallowUnknownFields()
		if e := dec.Decode(&req); e != nil {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(ControlResponse{SchemaVersion: 1, Problem: problem(ErrConfig, "Invalid local control request.", "Use a compatible Runmoor CLI.")})
			return
		}
		var extra any
		if dec.Decode(&extra) != io.EOF {
			w.WriteHeader(400)
			return
		}
		resp := m.Control(r.Context(), req)
		_ = json.NewEncoder(w).Encode(resp)
	})}
	go func() { _ = server.Serve(listener) }()
	return server, nil
}
func (m *Manager) Control(ctx context.Context, req ControlRequest) ControlResponse {
	resp := ControlResponse{SchemaVersion: 1}
	var err error
	if req.Force && req.Action != "stop" {
		resp.Problem = problem(ErrConfig, "Force is supported only for stop.", "Use stop with an optional pool.")
		return resp
	}
	switch req.Action {
	case "doctor":
		s := m.Store.View()
		r := Doctor(ctx, s.Config, s, m.RemoteFactory, m.Drivers)
		resp.Doctor = &r
	case "status":
		resp.Status = statusOf(m.Store.View(), true)
	case "reload":
		err = m.Reload(ctx)
	case "pause", "drain":
		err = m.Pause(req.Pool)
	case "resume":
		err = m.Resume(ctx, req.Pool)
	case "stop":
		if req.Pool != "" {
			err = m.StopPool(req.Pool, req.Force)
		} else {
			err = m.Stop(req.Force)
		}
	case "image":
		if req.Image == nil {
			err = problem(ErrConfig, "Image operation is missing.", "Use an image subcommand.")
			break
		}
		m.imageMu.Lock()
		defer m.imageMu.Unlock()
		resp.Image, err = m.Images.Operate(ctx, m.Store.View().Config, *req.Image)
	case "images":
		for _, im := range m.Store.View().Images {
			resp.Images = append(resp.Images, im)
		}
		sort.Slice(resp.Images, func(i, j int) bool { return resp.Images[i].ID < resp.Images[j].ID })
	default:
		err = problem(ErrConfig, "Unknown local control action.", "Use a supported Runmoor CLI command.")
	}
	if err != nil {
		resp.Problem = classify(err, ErrControl, "Local control operation failed.", "Inspect manager status and retry.")
	}
	return resp
}
func SendControl(ctx context.Context, c Config, req ControlRequest) (ControlResponse, error) {
	var out ControlResponse
	path := filepath.Join(c.Storage.State, "control.sock")
	st, e := os.Lstat(path)
	if e != nil || st.Mode()&os.ModeSocket == 0 || st.Mode().Perm()&0077 != 0 {
		return out, problem(ErrControl, "The private manager socket is unavailable.", "Start 'runmoor run' or the installed user service.")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	data, _ := json.Marshal(req)
	r, e := http.NewRequestWithContext(ctx, http.MethodPost, "http://runmoor/v1/control", bytes.NewReader(data))
	if e != nil {
		return out, e
	}
	res, e := client.Do(r)
	if e != nil {
		return out, problem(ErrControl, "Cannot contact the local manager.", "Check manager status; a stopped manager may leave a stale socket.")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&out) != nil || out.SchemaVersion != 1 {
		return out, problem(ErrControl, "The manager returned an incompatible response.", "Use the CLI matching the running manager version.")
	}
	if out.Problem != nil {
		return out, out.Problem
	}
	return out, nil
}
