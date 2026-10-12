// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// commandPaths describes command words only. The owning handlers still parse
// flags, validate values and acquire authority. Admission must not invoke them:
// even a rejected local operation can otherwise read credentials or create state.
// A path may also be a parent (for example, sidechat and sidechat send).
var commandPaths = map[string][]string{
	"account": {
		"connect", "disconnect", "status", "validate", "login", "logout", "refresh", "cancel-login", "login-progress",
		"refresh-quota", "refresh-all-quotas", "consume-reset-credit", "reconcile-reset-credit", "oauth start",
		"oauth complete", "oauth status", "oauth cancel",
	},
	"backup":          {"restore-status", "restore", "create", "creation", "creations", "list", "deletion", "deletions", "delete", "inspect"},
	"browser-profile": {"capabilities", "register", "account-status", "status", "list", "confirm-removal"},
	"browser-storage": {"prepare"},
	"configuration":   {"export", "preview", "apply"},
	"connection": {
		"removed", "list", "remove", "rename", "worker-register", "worker-inspect", "worker-status", "worker-start",
		"worker-stop", "worker-network-prepare", "worker-network-import", "worker-network-status", "inspect", "verify",
		"retry", "pair",
	},
	"device": {"inspect-local", "recover-local", "pair", "pair-local", "inspect", "create-pairing", "revoke"},
	"doctor": {""},
	"events": {""},
	"github": {
		"pr list", "pr search", "pr get", "pr open", "pr diff", "pr checks", "pr reviewers", "pr feedback", "pr ci",
		"pr rules", "pr statuses", "issue list", "issue search", "issue get", "issue open", "issue diff", "issue checks",
		"issue reviewers", "issue feedback", "issue ci", "issue rules", "issue statuses", "pr remediation capabilities",
		"pr remediation fix", "pr remediation list", "pr remediation resume", "pr problems refresh", "pr problems list",
		"pr problems dismiss",
	},
	"inbox":       {"list", "get", "inspect", "mark-read", "mark-unread"},
	"integration": {"create", "edit", "replace-token", "validate", "delete", "token-form"},
	"interaction": {"approve", "respond"},
	"machine":     {"ssh inspect", "ssh start", "ssh get", "ssh cancel", "ssh reconcile", "discover"},
	"model":       {"native-discover", "native-observation", "native-list", "native-cancel"},
	"network": {
		"export-bundle", "worker-status", "profile list", "profile get", "profile save", "profile delete", "status",
		"select", "export-metadata",
	},
	"notification": {"observe-connection", "preferences", "configure", "list", "inspect", "claim", "report"},
	"presentation": {"open-github", "dispatch-github"},
	"project":      {"prompt-history list", "prompt-history clear"},
	"provider":     {"presets", "inventory", "discover"},
	"queue":        {"list", "edit", "remove", "delete"},
	"repository":   {"inspect"},
	"schedule": {
		"create", "edit", "list", "history", "occurrence", "get", "inspect", "next-run", "pause", "resume", "delete",
		"run-now",
	},
	"search": {""},
	"server": {
		"desktop-host", "start", "run", "ensure", "desktop-launch", "desktop-status", "desktop-retry", "overview", "status",
		"stop", "service install", "service status", "service start", "service stop", "service remove",
	},
	"service-control": {"install", "status", "start", "stop", "remove"},
	"service-run":     {""},
	"session": {
		"sidechat", "sidechat send", "fork", "budget get", "budget set", "budget remove", "diagnostics", "subagents",
		"delete", "deletion", "compact", "context", "terminal create", "terminal input", "terminal resize", "terminal close",
		"terminal list", "terminal inspect", "terminal output", "terminal reattach", "forward start", "forward status",
		"forward stop", "forward reconcile", "pr link", "pr unlink", "pr get", "pr list", "files roots", "files list",
		"files read", "files diff", "files review-context", "diff", "review-context", "native-review create", "native-review get", "review create", "review edit",
		"review delete", "review submit", "review list", "review get", "steer", "create", "enqueue", "list", "stop",
		"archive", "restore", "unarchive", "resume", "rename", "prepare", "recover-workspace", "recover-execution",
		"switch-account",
	},
	"settings": {"defaults"},
	"snapshot": {"list"},
	"storage":  {"cancel", "operation", "preview", "create", "cleanup", "inspect", "restore", "recover", "delete"},
	"update": {
		"check", "get", "worker-request", "cancel", "native-prepare", "native-begin", "native-outcome", "native-inspect",
		"native-verify",
	},
	"usage": {"summary", "pricing version", "pricing get", "pricing set", "pricing mode", "pricing refresh"},
	"worker": {
		"desktop-host", "desktop-prepare", "update-replace", "update-rollback", "ssh-setup", "network prepare",
		"network import", "network status", "inspect", "pair-local", "pair", "status", "stop", "start", "service install",
		"service status", "service start", "service stop", "service remove",
	},
}

func validateCommandShape(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	command, rest := args[0], args[1:]
	for _, path := range commandPaths[command] {
		words := strings.Fields(path)
		if len(rest) < len(words) || !slices.Equal(rest[:len(words)], words) {
			continue
		}
		tail := rest[len(words):]
		if len(tail) == 0 {
			return nil
		}
		// These handlers take no flags. All others own a flag parser after the
		// complete command path; flag values are not additional command words.
		if command == "doctor" || command == "browser-storage" || command == "settings" && path == "defaults" || command == "server" && (path == "overview" || path == "status" || path == "stop") || command == "github" && path == "pr remediation capabilities" {
			continue
		}
		if strings.HasPrefix(tail[0], "-") && tail[0] != "-" {
			return nil
		}
	}
	if len(rest) > 0 && genericCommandAction(command, rest[0]) && (len(rest) == 1 || strings.HasPrefix(rest[1], "-") && rest[1] != "-") {
		return nil
	}
	return usage()
}

func genericCommandAction(command, action string) bool {
	// Specialized dispatch owns these complete families; accepting a generic
	// action here would admit a command that its real handler cannot execute.
	switch command {
	case "update", "storage", "service-control", "search", "notification", "model", "github", "network", "browser-profile", "server", "doctor", "configuration", "backup", "events":
		return false
	case "session", "queue":
		if action != "get" && action != "inspect" && action != "snapshot" {
			return false
		}
	case "inbox", "schedule":
		if action != "snapshot" {
			return false
		}
	case "integration":
		if action != "list" && action != "get" && action != "snapshot" {
			return false
		}
	}
	kind := domain.Kind(command)
	if !kind.Valid() || rpc.WireKind(kind) == pb.EntityKind_ENTITY_KIND_UNSPECIFIED {
		return false
	}
	switch action {
	case "list", "snapshot", "get", "inspect", "create", "edit", "save", "delete":
		return true
	case "routing":
		return kind == domain.AgentKind
	default:
		return false
	}
}
