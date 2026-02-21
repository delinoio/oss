package contracts

type DerunCommand string

const (
	DerunCommandRun DerunCommand = "run"
	DerunCommandMCP DerunCommand = "mcp"
)

type DerunSessionState string

const (
	DerunSessionStateStarting DerunSessionState = "starting"
	DerunSessionStateRunning  DerunSessionState = "running"
	DerunSessionStateExited   DerunSessionState = "exited"
	DerunSessionStateSignaled DerunSessionState = "signaled"
	DerunSessionStateFailed   DerunSessionState = "failed"
	DerunSessionStateExpired  DerunSessionState = "expired"
)

type DerunOutputChannel string

const (
	DerunOutputChannelPTY    DerunOutputChannel = "pty"
	DerunOutputChannelStdout DerunOutputChannel = "stdout"
	DerunOutputChannelStderr DerunOutputChannel = "stderr"
)

type DerunTransportMode string

const (
	DerunTransportModePosixPTY      DerunTransportMode = "posix-pty"
	DerunTransportModeWindowsConPTY DerunTransportMode = "windows-conpty"
	DerunTransportModePipe          DerunTransportMode = "pipe"
)

type DerunMCPTool string

const (
	DerunMCPToolListSessions DerunMCPTool = "derun_list_sessions"
	DerunMCPToolGetSession   DerunMCPTool = "derun_get_session"
	DerunMCPToolReadOutput   DerunMCPTool = "derun_read_output"
	DerunMCPToolWaitOutput   DerunMCPTool = "derun_wait_output"
)
