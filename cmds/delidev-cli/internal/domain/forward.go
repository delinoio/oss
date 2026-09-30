package domain

import (
	"net"
	"strconv"
)

type ForwardState string

const (
	ForwardPending  ForwardState = "pending"
	ForwardActive   ForwardState = "active"
	ForwardStopping ForwardState = "stopping"
	ForwardStopped  ForwardState = "stopped"
)

// Forward contains ownership metadata only. Traffic and authentication tokens
// are never stored. Claims survive process loss and cannot grant a second run.
type Forward struct {
	MachineID        ID           `json:"machine_id"`
	WorkerDeviceID   ID           `json:"worker_device_id"`
	WorkerInstanceID ID           `json:"worker_instance_id"`
	PrimaryStreamID  ID           `json:"primary_stream_id"`
	ClientDeviceID   ID           `json:"client_device_id,omitempty"`
	ClientType       DeviceType   `json:"client_type"`
	ClientRuntimeID  ID           `json:"client_runtime_id"`
	WorkerRuntimeID  ID           `json:"worker_runtime_id"`
	Epoch            ID           `json:"epoch"`
	WorkerPort       uint32       `json:"worker_port"`
	LocalPort        uint32       `json:"local_port"`
	LocalEndpoint    string       `json:"local_endpoint,omitempty"`
	State            ForwardState `json:"state"`
	ClientClaimed    bool         `json:"client_claimed"`
	WorkerClaimed    bool         `json:"worker_claimed"`
	ClientClean      bool         `json:"client_clean"`
	WorkerClean      bool         `json:"worker_clean"`
}

func (f Forward) Closed() bool { return f.State == ForwardStopped && f.ClientClean && f.WorkerClean }
func (f *Forward) Stop() {
	f.State = ForwardStopping
	// An unclaimed lifetime never had native authority or socket handles.
	f.ClientClean = f.ClientClean || !f.ClientClaimed
	f.WorkerClean = f.WorkerClean || !f.WorkerClaimed
	if f.ClientClean && f.WorkerClean {
		f.State = ForwardStopped
	}
}
func (f Forward) ValidateEndpoint(endpoint string) error {
	host, port, err := net.SplitHostPort(endpoint)
	n, parseErr := strconv.ParseUint(port, 10, 16)
	if err != nil || parseErr != nil || n == 0 || (host != "127.0.0.1" && host != "::1") || strconv.FormatUint(n, 10) != port || (f.LocalPort != 0 && uint64(f.LocalPort) != n) {
		return Fail(InvalidArgument, "The forward endpoint must be an exact loopback listener.", "Use the original requested local port or its OS-assigned port.")
	}
	return nil
}
