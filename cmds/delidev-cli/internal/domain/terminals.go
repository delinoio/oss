// SPDX-License-Identifier: Apache-2.0
package domain

type TerminalState string
type TerminalAction string

const (
	TerminalStarting    TerminalState  = "starting"
	TerminalRunning     TerminalState  = "running"
	TerminalExited      TerminalState  = "exited"
	TerminalClosed      TerminalState  = "closed"
	TerminalUncertain   TerminalState  = "uncertain"
	TerminalCreate      TerminalAction = "create"
	TerminalInput       TerminalAction = "input"
	TerminalResize      TerminalAction = "resize"
	TerminalClose       TerminalAction = "close"
	MaxSessionTerminals                = 8
	MaxTerminalRecords                 = 128
	MaxTerminalInput                   = 32 << 10
)

type TerminalOperation struct {
	ID      ID             `json:"id"`
	Action  TerminalAction `json:"action"`
	Input   []byte         `json:"input,omitempty"`
	Rows    uint16         `json:"rows,omitempty"`
	Columns uint16         `json:"columns,omitempty"`
	Claimed bool           `json:"claimed,omitempty"`
}

// Output is an ephemeral, bounded byte stream, never a resource document.
// Pending input is private dispatch data, omitted from public resources and
// cleared after its once-only native operation is reported.
type Terminal struct {
	OwnerInstanceID ID                 `json:"owner_instance_id"`
	MachineID       ID                 `json:"machine_id"`
	InstanceID      ID                 `json:"instance_id"`
	DeviceID        ID                 `json:"device_id,omitempty"`
	ShellOverride   string             `json:"shell_override,omitempty"`
	Shell           string             `json:"shell,omitempty"`
	Cwd             string             `json:"cwd,omitempty"`
	State           TerminalState      `json:"state"`
	Rows            uint16             `json:"rows"`
	Columns         uint16             `json:"columns"`
	Pending         *TerminalOperation `json:"pending,omitempty"`
	CloseRequestID  ID                 `json:"close_request_id,omitempty"`
	OutputLost      bool               `json:"output_lost,omitempty"`
	CleanupVerified bool               `json:"cleanup_verified"`
	ExitCode        *int               `json:"exit_code,omitempty"`
	Problem         *Error             `json:"problem,omitempty"`
}

func (t Terminal) Live() bool { return !t.CleanupVerified }
func TerminalUnavailable() *Error {
	return Fail(Unavailable, "The owning terminal Worker is unavailable.", "Reconnect the session's Worker and reattach to the original terminal.")
}
