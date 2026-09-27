package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Only completed root Bash tasks with an independently verified inline tool
// result may cross this boundary. Event hashes retain original observation
// provenance without copying command text, output paths or task descriptions.
// They do not reconstruct an asynchronous executor or grant TaskOutput access.
type bashTaskHistory struct {
	Started          string
	Completed        string
	StartDigest      [sha256.Size]byte
	CompletionDigest [sha256.Size]byte
}
type checkpointBashTask struct {
	ID               string `json:"id"`
	Tool             string `json:"tool_id"`
	Started          string `json:"started_native_id"`
	Completed        string `json:"completed_native_id"`
	StartDigest      string `json:"started_sha256"`
	CompletionDigest string `json:"completed_sha256"`
}

func taskFlagTrue(value *bool) bool { return value != nil && *value }
func retainBashTaskHistory(current *nativeTaskState, observation *NativeTaskObservation, native string, raw []byte) {
	v := observation
	if v.Kind == TaskStarted {
		if v.Type != nil && *v.Type == LocalBashTask && v.ToolID != nil && v.SubagentType == nil && v.Prompt == nil && v.WorkflowName == nil && v.SpawnDepth == nil && !taskFlagTrue(v.Backgrounded) && !taskFlagTrue(v.SkipTranscript) && !taskFlagTrue(v.Ambient) {
			current.inlineBash = &bashTaskHistory{Started: native, StartDigest: sha256.Sum256(raw)}
		}
		return
	}
	if current.inlineBash == nil {
		return
	}
	// Once incompatible state has been observed, later false flags or a success
	// cannot turn that history into the synchronous inline profile.
	if v.SubagentType != nil || taskFlagTrue(v.SkipTranscript) || taskFlagTrue(v.Ambient) || v.Patch != nil && (taskFlagTrue(v.Patch.Backgrounded) || v.Patch.Status != nil && v.Patch.Status.terminal() && *v.Patch.Status != TaskCompleted) {
		current.inlineBash = nil
		return
	}
	if v.Kind == TaskNotification {
		if v.Status == nil || *v.Status != TaskCompleted || v.Reason != nil {
			current.inlineBash = nil
			return
		}
		copy := *current.inlineBash
		copy.Completed, copy.CompletionDigest = native, sha256.Sum256(raw)
		current.inlineBash = &copy
	}
}
func (b *ExecutionBinding) closedBashTasks() ([]checkpointBashTask, error) {
	if len(b.backgroundTasks) != 0 || len(b.tasks) > 4096 {
		return nil, continuationUnavailable()
	}
	var result []checkpointBashTask
	for id, task := range b.tasks {
		proof := task.inlineBash
		tool, exists := b.content.tools[task.tool]
		if task.kind != LocalBashTask || task.agentType != "" || task.status != TaskCompleted || !task.notified || task.backgrounded || proof == nil || !nativeUUID(proof.Started) || !nativeUUID(proof.Completed) || proof.Started == proof.Completed || !b.seen[proof.Started] || !b.seen[proof.Completed] || proof.StartDigest == ([sha256.Size]byte{}) || proof.CompletionDigest == ([sha256.Size]byte{}) || !exists || tool.name != "Bash" || tool.parent != "" || !tool.finished || !tool.streamed || tool.inline == nil || tool.inline.Error {
			return nil, continuationUnavailable()
		}
		result = append(result, checkpointBashTask{ID: id, Tool: task.tool, Started: proof.Started, Completed: proof.Completed, StartDigest: hex.EncodeToString(proof.StartDigest[:]), CompletionDigest: hex.EncodeToString(proof.CompletionDigest[:])})
	}
	slices.SortFunc(result, func(a, b checkpointBashTask) int { return strings.Compare(a.ID, b.ID) })
	return result, nil
}
func (cp sessionCheckpoint) validateBashTasks() error {
	if len(cp.BashTasks) > 4096 {
		return historyUncertain()
	}
	tools := map[string]checkpointInlineTool{}
	for _, tool := range cp.BashTools {
		tools[tool.ID] = tool
	}
	seenTools, events := map[string]bool{}, map[string]bool{}
	for i, task := range cp.BashTasks {
		tool, exists := tools[task.Tool]
		if domain.Text(task.ID, "native task identity", 1024, true) != nil || i > 0 && cp.BashTasks[i-1].ID >= task.ID || !exists || seenTools[task.Tool] || !validHistoryDigest(task.StartDigest) || !validHistoryDigest(task.CompletionDigest) || task.Started == task.Completed {
			return historyUncertain()
		}
		for _, native := range []string{task.Started, task.Completed} {
			if !checkpointHasIdentity(cp.NativeIDs, native) || native == tool.Turn || native == string(tool.Input) || native == tool.Result || events[native] {
				return historyUncertain()
			}
			events[native] = true
		}
		seenTools[task.Tool] = true
	}
	return nil
}
func restoreBashTasks(b *ExecutionBinding, tasks []checkpointBashTask) {
	if len(tasks) == 0 {
		return
	}
	b.tasks = map[string]nativeTaskState{}
	for _, task := range tasks {
		proof := &bashTaskHistory{Started: task.Started, Completed: task.Completed}
		start, _ := hex.DecodeString(task.StartDigest)
		end, _ := hex.DecodeString(task.CompletionDigest)
		copy(proof.StartDigest[:], start)
		copy(proof.CompletionDigest[:], end)
		b.tasks[task.ID] = nativeTaskState{tool: task.Tool, kind: LocalBashTask, status: TaskCompleted, notified: true, inlineBash: proof}
	}
}
