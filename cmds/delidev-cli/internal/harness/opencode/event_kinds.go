package opencode

// EventKind preserves the pinned native event namespace. Recognition here is
// transport validation only; typed business observers must validate each body.
type EventKind string

const (
	ModelsDevRefreshedEvent           EventKind = "models-dev.refreshed"
	IntegrationUpdatedEvent           EventKind = "integration.updated"
	IntegrationConnectionUpdatedEvent EventKind = "integration.connection.updated"
	CatalogUpdatedEvent               EventKind = "catalog.updated"
	SessionCreatedEvent               EventKind = "session.created"
	SessionUpdatedEvent               EventKind = "session.updated"
	SessionDeletedEvent               EventKind = "session.deleted"
	MessageUpdatedEvent               EventKind = "message.updated"
	MessageRemovedEvent               EventKind = "message.removed"
	MessagePartUpdatedEvent           EventKind = "message.part.updated"
	MessagePartRemovedEvent           EventKind = "message.part.removed"
	SessionNextAgentSwitchedEvent     EventKind = "session.next.agent.switched"
	SessionNextModelSwitchedEvent     EventKind = "session.next.model.switched"
	SessionNextMovedEvent             EventKind = "session.next.moved"
	SessionNextPromptedEvent          EventKind = "session.next.prompted"
	SessionNextPromptAdmittedEvent    EventKind = "session.next.prompt.admitted"
	SessionNextContextUpdatedEvent    EventKind = "session.next.context.updated"
	SessionNextSyntheticEvent         EventKind = "session.next.synthetic"
	SessionNextShellStartedEvent      EventKind = "session.next.shell.started"
	SessionNextShellEndedEvent        EventKind = "session.next.shell.ended"
	SessionNextStepStartedEvent       EventKind = "session.next.step.started"
	SessionNextStepEndedEvent         EventKind = "session.next.step.ended"
	SessionNextStepFailedEvent        EventKind = "session.next.step.failed"
	SessionNextTextStartedEvent       EventKind = "session.next.text.started"
	SessionNextTextDeltaEvent         EventKind = "session.next.text.delta"
	SessionNextTextEndedEvent         EventKind = "session.next.text.ended"
	SessionNextReasoningStartedEvent  EventKind = "session.next.reasoning.started"
	SessionNextReasoningDeltaEvent    EventKind = "session.next.reasoning.delta"
	SessionNextReasoningEndedEvent    EventKind = "session.next.reasoning.ended"
	SessionNextToolInputStartedEvent  EventKind = "session.next.tool.input.started"
	SessionNextToolInputDeltaEvent    EventKind = "session.next.tool.input.delta"
	SessionNextToolInputEndedEvent    EventKind = "session.next.tool.input.ended"
	SessionNextToolCalledEvent        EventKind = "session.next.tool.called"
	SessionNextToolProgressEvent      EventKind = "session.next.tool.progress"
	SessionNextToolSuccessEvent       EventKind = "session.next.tool.success"
	SessionNextToolFailedEvent        EventKind = "session.next.tool.failed"
	SessionNextRetriedEvent           EventKind = "session.next.retried"
	SessionNextCompactionStartedEvent EventKind = "session.next.compaction.started"
	SessionNextCompactionDeltaEvent   EventKind = "session.next.compaction.delta"
	SessionNextCompactionEndedEvent   EventKind = "session.next.compaction.ended"
	SessionNextRevertStagedEvent      EventKind = "session.next.revert.staged"
	SessionNextRevertClearedEvent     EventKind = "session.next.revert.cleared"
	SessionNextRevertCommittedEvent   EventKind = "session.next.revert.committed"
	MessagePartDeltaEvent             EventKind = "message.part.delta"
	SessionDiffEvent                  EventKind = "session.diff"
	SessionErrorEvent                 EventKind = "session.error"
	InstallationUpdatedEvent          EventKind = "installation.updated"
	InstallationUpdateAvailableEvent  EventKind = "installation.update-available"
	FileEditedEvent                   EventKind = "file.edited"
	ReferenceUpdatedEvent             EventKind = "reference.updated"
	PermissionV2AskedEvent            EventKind = "permission.v2.asked"
	PermissionV2RepliedEvent          EventKind = "permission.v2.replied"
	PluginAddedEvent                  EventKind = "plugin.added"
	ProjectDirectoriesUpdatedEvent    EventKind = "project.directories.updated"
	FileWatcherUpdatedEvent           EventKind = "file.watcher.updated"
	PtyCreatedEvent                   EventKind = "pty.created"
	PtyUpdatedEvent                   EventKind = "pty.updated"
	PtyExitedEvent                    EventKind = "pty.exited"
	PtyDeletedEvent                   EventKind = "pty.deleted"
	QuestionV2AskedEvent              EventKind = "question.v2.asked"
	QuestionV2RepliedEvent            EventKind = "question.v2.replied"
	QuestionV2RejectedEvent           EventKind = "question.v2.rejected"
	TodoUpdatedEvent                  EventKind = "todo.updated"
	LspUpdatedEvent                   EventKind = "lsp.updated"
	PermissionAskedEvent              EventKind = "permission.asked"
	PermissionRepliedEvent            EventKind = "permission.replied"
	TuiPromptAppendEvent              EventKind = "tui.prompt.append"
	TuiCommandExecuteEvent            EventKind = "tui.command.execute"
	TuiToastShowEvent                 EventKind = "tui.toast.show"
	TuiSessionSelectEvent             EventKind = "tui.session.select"
	McpToolsChangedEvent              EventKind = "mcp.tools.changed"
	McpBrowserOpenFailedEvent         EventKind = "mcp.browser.open.failed"
	CommandExecutedEvent              EventKind = "command.executed"
	ProjectUpdatedEvent               EventKind = "project.updated"
	SessionStatusEvent                EventKind = "session.status"
	SessionIdleEvent                  EventKind = "session.idle"
	QuestionAskedEvent                EventKind = "question.asked"
	QuestionRepliedEvent              EventKind = "question.replied"
	QuestionRejectedEvent             EventKind = "question.rejected"
	SessionCompactedEvent             EventKind = "session.compacted"
	VcsBranchUpdatedEvent             EventKind = "vcs.branch.updated"
	WorkspaceReadyEvent               EventKind = "workspace.ready"
	WorkspaceFailedEvent              EventKind = "workspace.failed"
	WorkspaceStatusEvent              EventKind = "workspace.status"
	WorktreeReadyEvent                EventKind = "worktree.ready"
	WorktreeFailedEvent               EventKind = "worktree.failed"
	ServerConnectedEvent              EventKind = "server.connected"
	GlobalDisposedEvent               EventKind = "global.disposed"
	ServerInstanceDisposedEvent       EventKind = "server.instance.disposed"
	ServerHeartbeatEvent              EventKind = "server.heartbeat"
)

func (kind EventKind) valid() bool {
	switch kind {
	case ModelsDevRefreshedEvent,
		IntegrationUpdatedEvent,
		IntegrationConnectionUpdatedEvent,
		CatalogUpdatedEvent,
		SessionCreatedEvent,
		SessionUpdatedEvent,
		SessionDeletedEvent,
		MessageUpdatedEvent,
		MessageRemovedEvent,
		MessagePartUpdatedEvent,
		MessagePartRemovedEvent,
		SessionNextAgentSwitchedEvent,
		SessionNextModelSwitchedEvent,
		SessionNextMovedEvent,
		SessionNextPromptedEvent,
		SessionNextPromptAdmittedEvent,
		SessionNextContextUpdatedEvent,
		SessionNextSyntheticEvent,
		SessionNextShellStartedEvent,
		SessionNextShellEndedEvent,
		SessionNextStepStartedEvent,
		SessionNextStepEndedEvent,
		SessionNextStepFailedEvent,
		SessionNextTextStartedEvent,
		SessionNextTextDeltaEvent,
		SessionNextTextEndedEvent,
		SessionNextReasoningStartedEvent,
		SessionNextReasoningDeltaEvent,
		SessionNextReasoningEndedEvent,
		SessionNextToolInputStartedEvent,
		SessionNextToolInputDeltaEvent,
		SessionNextToolInputEndedEvent,
		SessionNextToolCalledEvent,
		SessionNextToolProgressEvent,
		SessionNextToolSuccessEvent,
		SessionNextToolFailedEvent,
		SessionNextRetriedEvent,
		SessionNextCompactionStartedEvent,
		SessionNextCompactionDeltaEvent,
		SessionNextCompactionEndedEvent,
		SessionNextRevertStagedEvent,
		SessionNextRevertClearedEvent,
		SessionNextRevertCommittedEvent,
		MessagePartDeltaEvent,
		SessionDiffEvent,
		SessionErrorEvent,
		InstallationUpdatedEvent,
		InstallationUpdateAvailableEvent,
		FileEditedEvent,
		ReferenceUpdatedEvent,
		PermissionV2AskedEvent,
		PermissionV2RepliedEvent,
		PluginAddedEvent,
		ProjectDirectoriesUpdatedEvent,
		FileWatcherUpdatedEvent,
		PtyCreatedEvent,
		PtyUpdatedEvent,
		PtyExitedEvent,
		PtyDeletedEvent,
		QuestionV2AskedEvent,
		QuestionV2RepliedEvent,
		QuestionV2RejectedEvent,
		TodoUpdatedEvent,
		LspUpdatedEvent,
		PermissionAskedEvent,
		PermissionRepliedEvent,
		TuiPromptAppendEvent,
		TuiCommandExecuteEvent,
		TuiToastShowEvent,
		TuiSessionSelectEvent,
		McpToolsChangedEvent,
		McpBrowserOpenFailedEvent,
		CommandExecutedEvent,
		ProjectUpdatedEvent,
		SessionStatusEvent,
		SessionIdleEvent,
		QuestionAskedEvent,
		QuestionRepliedEvent,
		QuestionRejectedEvent,
		SessionCompactedEvent,
		VcsBranchUpdatedEvent,
		WorkspaceReadyEvent,
		WorkspaceFailedEvent,
		WorkspaceStatusEvent,
		WorktreeReadyEvent,
		WorktreeFailedEvent,
		ServerConnectedEvent,
		GlobalDisposedEvent,
		ServerInstanceDisposedEvent,
		ServerHeartbeatEvent:
		return true
	default:
		return false
	}
}
