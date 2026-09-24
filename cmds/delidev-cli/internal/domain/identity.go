package domain

import (
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

type ID string

func NewID() ID { return ID(uuid.Must(uuid.NewV7()).String()) }
func (id ID) Validate() error {
	v, err := uuid.Parse(string(id))
	if err != nil || v.Version() != 7 || v.Variant() != uuid.RFC4122 || v.String() != string(id) {
		return Fail(InvalidArgument, "An identifier must be a canonical lowercase RFC 9562 UUID v7.", "Use the exact ID returned by the server.")
	}
	return nil
}

type Kind string

const PairingKind Kind = "pairing"

const (
	ProjectKind     Kind = "project"
	RepositoryKind  Kind = "repository"
	AgentKind       Kind = "agent"
	AccountKind     Kind = "account"
	ProviderKind    Kind = "provider"
	ModelKind       Kind = "model"
	MachineKind     Kind = "machine"
	SessionKind     Kind = "session"
	TemplateKind    Kind = "template"
	SettingsKind    Kind = "settings"
	ScheduleKind    Kind = "schedule"
	OccurrenceKind  Kind = "occurrence"
	MessageKind     Kind = "message"
	QueueKind       Kind = "queue"
	SteerKind       Kind = "steer"
	InteractionKind Kind = "interaction"
	ReviewKind      Kind = "review"
	SnapshotKind    Kind = "snapshot"
	DeviceKind      Kind = "device"
	IntegrationKind Kind = "integration"
	PullRequestKind Kind = "pull_request"
	ProblemKind     Kind = "problem"
	InboxKind       Kind = "inbox"
	UsageKind       Kind = "usage"
	JobKind         Kind = "job"
	RoutingKind     Kind = "routing"
)

func (k Kind) Valid() bool {
	switch k {
	case PairingKind, ProjectKind, RepositoryKind, AgentKind, AccountKind, ProviderKind, ModelKind, MachineKind, SessionKind, TemplateKind, SettingsKind, ScheduleKind, OccurrenceKind, MessageKind, QueueKind, SteerKind, InteractionKind, ReviewKind, SnapshotKind, DeviceKind, IntegrationKind, PullRequestKind, ProblemKind, InboxKind, UsageKind, JobKind, RoutingKind:
		return true
	default:
		return false
	}
}

func Text(value, label string, max int, required bool) error {
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) || len(value) > max || (required && strings.TrimSpace(value) == "") {
		return Fail(InvalidArgument, "Invalid "+label+".", "Provide well-formed, bounded UTF-8 text without NUL bytes.")
	}
	return nil
}

func Decode(data []byte, target any) error {
	if len(data) > 1<<20 || !utf8.Valid(data) {
		return Fail(InvalidArgument, "JSON input exceeds its limit or is not UTF-8.", "Use a document no larger than 1 MiB.")
	}
	if err := uniqueJSON(data); err != nil {
		return err
	}
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return Fail(InvalidArgument, "JSON input does not match the command schema.", "Check field names, types, and enum values.")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return Fail(InvalidArgument, "Expected exactly one JSON document.", "Remove trailing data.")
	}
	return nil
}

// Reject duplicate object keys before decoding into typed documents. Otherwise
// different clients can disagree about which policy or credential field wins.
func uniqueJSON(data []byte) error {
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.UseNumber()
	var walk func(int) error
	invalid := func() error {
		return Fail(InvalidArgument, "JSON contains duplicate keys, invalid structure, or excessive nesting.", "Use one unambiguous JSON document with unique fields.")
	}
	walk = func(depth int) error {
		if depth > 64 {
			return invalid()
		}
		token, err := d.Token()
		if err != nil {
			return invalid()
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return invalid()
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return invalid()
				}
				seen[name] = true
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			close, err := d.Token()
			if err != nil || close != json.Delim('}') {
				return invalid()
			}
		case '[':
			for d.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			close, err := d.Token()
			if err != nil || close != json.Delim(']') {
				return invalid()
			}
		default:
			return invalid()
		}
		return nil
	}
	if err := walk(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return invalid()
	}
	return nil
}
