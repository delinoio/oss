package opencode

import (
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// FrozenObservation retains one already validated original arrival. Its private
// canonical payload never enters JSON or logs. It can reproduce owned typed
// copies for bounded deferred publication without consuming the observer again.
type FrozenObservation struct {
	event     NativeEvent
	repeated  bool
	finalized bool
	rejected  []string
	always    []string
	bytes     int
}

func freezeObservation(event NativeEvent, value inputObservation) *FrozenObservation {
	result := &FrozenObservation{event: event, repeated: value.Repeated, finalized: value.MessageFinalized, rejected: slices.Clone(value.RejectionSources), always: slices.Clone(value.AlwaysObservations), bytes: len(event.Properties) + len(event.ID) + len(event.Kind) + 128}
	for _, group := range [][]string{result.rejected, result.always} {
		for _, id := range group {
			result.bytes += len(id) + 16
		}
	}
	return result
}

func (o inputObservation) Freeze() (*FrozenObservation, error) {
	if o.frozen == nil || o.EventID != o.frozen.event.ID || o.Kind != o.frozen.event.Kind {
		return nil, observerProblem()
	}
	return o.frozen, nil
}

func (f *FrozenObservation) Bytes() int {
	if f == nil {
		return 0
	}
	return f.bytes
}

func (f *FrozenObservation) Thaw() (inputObservation, error) {
	if f == nil {
		return inputObservation{}, observerProblem()
	}
	event := f.event
	result := inputObservation{EventID: event.ID, Kind: event.Kind, Repeated: f.repeated, MessageFinalized: f.finalized, RejectionSources: slices.Clone(f.rejected), AlwaysObservations: slices.Clone(f.always), frozen: f}
	fields, err := object(event.Properties)
	if err != nil {
		return inputObservation{}, observerProblem()
	}
	switch event.Kind {
	case MessageUpdatedEvent:
		value, decodeErr := decodeNativeMessage(fields["info"])
		result.Message, err = &value, decodeErr
	case MessagePartUpdatedEvent:
		value, decodeErr := decodeNativePart(fields["part"])
		result.Part, err = &value, decodeErr
	case PermissionAskedEvent, QuestionAskedEvent:
		value, decodeErr := decodeNativeInteraction(event.Kind, event.Properties)
		result.Interaction, err = &value, decodeErr
	case PermissionRepliedEvent, QuestionRepliedEvent, QuestionRejectedEvent:
		value, decodeErr := decodeNativeInteractionReply(event.Kind, event.Properties)
		result.InteractionReply, err = &value, decodeErr
	case MessagePartDeltaEvent:
		message, _ := boundedString(fields["messageID"], 30, true)
		part, _ := boundedString(fields["partID"], 30, true)
		text, valid := boundedString(fields["delta"], maxHTTPBody, false)
		if !valid || !nativeID(message, "msg") || !nativeID(part, "prt") {
			err = observerProblem()
		} else {
			result.Delta = &NativeTextDelta{MessageID: message, PartID: part, Text: text}
		}
	case SessionErrorEvent:
		result.Error, err = decodeNativeError(fields["error"])
	case TodoUpdatedEvent:
		var todos []domain.OpenCodeTodo
		err = domain.Decode(fields["todos"], &todos)
		session, _ := boundedString(fields["sessionID"], 30, true)
		result.Todo = &NativeTodoUpdate{SessionID: session, Todos: todos}
	case FileEditedEvent, FileWatcherUpdatedEvent:
		file, _ := boundedString(fields["file"], 32768, true)
		kind := domain.OpenCodeFileEdited
		if event.Kind == FileWatcherUpdatedEvent {
			switch {
			case scalar(fields["event"], "add"):
				kind = domain.OpenCodeFileAdded
			case scalar(fields["event"], "change"):
				kind = domain.OpenCodeFileChanged
			case scalar(fields["event"], "unlink"):
				kind = domain.OpenCodeFileUnlinked
			default:
				err = observerProblem()
			}
		}
		result.WorkspaceEvent = &domain.OpenCodeWorkspaceEvent{Kind: kind, NativeEventID: event.ID, File: file}
	case SessionStatusEvent:
		result.Ancillary = slices.Clone(event.Properties)
		status, problem := object(fields["status"])
		if problem != nil {
			err = problem
		} else if scalar(status["type"], "retry") {
			result.Retry, err = decodeNativeRetry(fields["status"])
		}
	case SessionUpdatedEvent, SessionDiffEvent, PluginAddedEvent, IntegrationConnectionUpdatedEvent:
		result.Ancillary = slices.Clone(event.Properties)
	case SessionIdleEvent, LspUpdatedEvent, ServerHeartbeatEvent, ModelsDevRefreshedEvent, CatalogUpdatedEvent, ReferenceUpdatedEvent, IntegrationUpdatedEvent:
	default:
		err = observerProblem()
	}
	if err != nil {
		return inputObservation{}, err
	}
	return result, nil
}
