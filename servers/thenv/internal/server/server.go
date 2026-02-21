package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/pkg/thenv/api"
)

type Server struct {
	logger        *slog.Logger
	db            *sql.DB
	jwtSecret     []byte
	workspaceKeys map[string][]byte
	defaultKey    []byte
	superAdmins   map[string]struct{}
}

func New(ctx context.Context, cfg Config, logger *slog.Logger) (*Server, error) {
	db, err := openDatabase(ctx, cfg.DatabasePath)
	if err != nil {
		return nil, err
	}

	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	return &Server{
		logger:        logger,
		db:            db,
		jwtSecret:     cfg.JWTSecret,
		workspaceKeys: cfg.WorkspaceKeys,
		defaultKey:    cfg.DefaultKey,
		superAdmins:   cfg.SuperAdmins,
	}, nil
}

func (s *Server) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	codec := api.JSONCodec{}

	mux.Handle(api.ProcedurePushBundleVersion, connect.NewUnaryHandler(api.ProcedurePushBundleVersion, s.handlePushBundleVersion, connect.WithCodec(codec)))
	mux.Handle(api.ProcedurePullActiveBundle, connect.NewUnaryHandler(api.ProcedurePullActiveBundle, s.handlePullActiveBundle, connect.WithCodec(codec)))
	mux.Handle(api.ProcedureListBundleVersions, connect.NewUnaryHandler(api.ProcedureListBundleVersions, s.handleListBundleVersions, connect.WithCodec(codec)))
	mux.Handle(api.ProcedureActivateBundle, connect.NewUnaryHandler(api.ProcedureActivateBundle, s.handleActivateBundleVersion, connect.WithCodec(codec)))
	mux.Handle(api.ProcedureRotateBundleVersion, connect.NewUnaryHandler(api.ProcedureRotateBundleVersion, s.handleRotateBundleVersion, connect.WithCodec(codec)))
	mux.Handle(api.ProcedureGetPolicy, connect.NewUnaryHandler(api.ProcedureGetPolicy, s.handleGetPolicy, connect.WithCodec(codec)))
	mux.Handle(api.ProcedureSetPolicy, connect.NewUnaryHandler(api.ProcedureSetPolicy, s.handleSetPolicy, connect.WithCodec(codec)))
	mux.Handle(api.ProcedureListAuditEvents, connect.NewUnaryHandler(api.ProcedureListAuditEvents, s.handleListAuditEvents, connect.WithCodec(codec)))
	return mux
}

func (s *Server) handlePushBundleVersion(ctx context.Context, req *connect.Request[api.PushBundleVersionRequest]) (*connect.Response[api.PushBundleVersionResponse], error) {
	actor, scope, err := s.authorizeAndValidate(ctx, req.Header(), req.Msg.Scope, api.RoleWriter)
	if err != nil {
		return nil, err
	}
	if len(req.Msg.Files) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("at least one file is required"))
	}

	masterKey, err := s.masterKeyForScope(scope)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}

	validatedFiles, err := validateFiles(req.Msg.Files)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	versionID := newIdentifier("ver")
	createdAt := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("begin transaction: %w", err))
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `INSERT INTO bundle_versions (
		bundle_version_id, workspace_id, project_id, environment_id, status, created_by, created_at, metadata
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		versionID,
		scope.WorkspaceID,
		scope.ProjectID,
		scope.EnvironmentID,
		int(api.BundleStatusArchived),
		actor.Subject,
		createdAt.Format(time.RFC3339Nano),
		req.Msg.Metadata,
	); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("insert bundle version: %w", err))
	}

	for _, file := range validatedFiles {
		encrypted, err := encryptPayload(masterKey, file.Content)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("encrypt file %s: %w", file.FileType.String(), err))
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO bundle_files (
			bundle_version_id, file_type, ciphertext, wrapped_dek, payload_nonce, dek_nonce, checksum, byte_length
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			versionID,
			int(file.FileType),
			encrypted.Ciphertext,
			encrypted.WrappedDEK,
			encrypted.PayloadNonce,
			encrypted.DEKNonce,
			checksum(file.Content),
			len(file.Content),
		); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("insert bundle file: %w", err))
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("commit push transaction: %w", err))
	}

	s.logOperation("push", actor.Subject, scope, "success", "", map[string]any{
		"bundle_version_id": versionID,
		"file_types":        collectFileTypeStrings(validatedFiles),
	})
	s.writeAudit(ctx, auditRecord{
		EventType:       api.AuditEventTypePush,
		Actor:           actor.Subject,
		Scope:           scope,
		TargetVersionID: versionID,
		Result:          "success",
		RequestID:       req.Header().Get("X-Request-Id"),
		TraceID:         req.Header().Get("X-Trace-Id"),
		Metadata: map[string]any{
			"fileTypes": collectFileTypeStrings(validatedFiles),
		},
	})

	return connect.NewResponse(&api.PushBundleVersionResponse{
		BundleVersionID: versionID,
		CreatedAt:       createdAt,
		Status:          api.BundleStatusArchived,
	}), nil
}

func (s *Server) handlePullActiveBundle(ctx context.Context, req *connect.Request[api.PullActiveBundleRequest]) (*connect.Response[api.PullActiveBundleResponse], error) {
	actor, scope, err := s.authorizeAndValidate(ctx, req.Header(), req.Msg.Scope, api.RoleReader)
	if err != nil {
		return nil, err
	}

	masterKey, err := s.masterKeyForScope(scope)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}

	versionID := strings.TrimSpace(req.Msg.BundleVersionID)
	if versionID == "" {
		versionID, err = s.resolveActiveVersionID(ctx, scope)
		if err != nil {
			return nil, err
		}
	}

	summary, err := s.getVersionSummary(ctx, scope, versionID)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT file_type, ciphertext, wrapped_dek, payload_nonce, dek_nonce, checksum, byte_length
	FROM bundle_files WHERE bundle_version_id = ?`, versionID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("query bundle files: %w", err))
	}
	defer rows.Close()

	files := make([]api.BundleFilePayload, 0, 2)
	for rows.Next() {
		var fileType int
		var encrypted EncryptedPayload
		var fileChecksum string
		var fileByteLength int64
		if err := rows.Scan(&fileType, &encrypted.Ciphertext, &encrypted.WrappedDEK, &encrypted.PayloadNonce, &encrypted.DEKNonce, &fileChecksum, &fileByteLength); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("scan bundle file: %w", err))
		}
		plaintext, err := decryptPayload(masterKey, encrypted)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("decrypt file payload: %w", err))
		}
		files = append(files, api.BundleFilePayload{
			FileType:   api.FileType(fileType),
			Content:    plaintext,
			Checksum:   fileChecksum,
			ByteLength: fileByteLength,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("iterate files: %w", err))
	}

	s.logOperation("pull", actor.Subject, scope, "success", "", map[string]any{
		"bundle_version_id": versionID,
		"file_types":        collectFileTypeStrings(files),
	})
	s.writeAudit(ctx, auditRecord{
		EventType:       api.AuditEventTypePull,
		Actor:           actor.Subject,
		Scope:           scope,
		TargetVersionID: versionID,
		Result:          "success",
		RequestID:       req.Header().Get("X-Request-Id"),
		TraceID:         req.Header().Get("X-Trace-Id"),
		Metadata: map[string]any{
			"fileTypes": collectFileTypeStrings(files),
		},
	})

	return connect.NewResponse(&api.PullActiveBundleResponse{Version: summary, Files: files}), nil
}

func (s *Server) handleListBundleVersions(ctx context.Context, req *connect.Request[api.ListBundleVersionsRequest]) (*connect.Response[api.ListBundleVersionsResponse], error) {
	actor, scope, err := s.authorizeAndValidate(ctx, req.Header(), req.Msg.Scope, api.RoleReader)
	if err != nil {
		return nil, err
	}

	limit := api.NormalizeLimit(req.Msg.Limit)
	offset, err := parseCursor(req.Msg.Cursor)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	rows, err := s.db.QueryContext(ctx, `SELECT bundle_version_id, status, created_by, created_at, source_version_id
	FROM bundle_versions
	WHERE workspace_id = ? AND project_id = ? AND environment_id = ?
	ORDER BY created_at DESC, bundle_version_id DESC
	LIMIT ? OFFSET ?`, scope.WorkspaceID, scope.ProjectID, scope.EnvironmentID, int(limit)+1, offset)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("query bundle versions: %w", err))
	}
	defer rows.Close()

	versions := make([]api.BundleVersionSummary, 0, limit)
	for rows.Next() {
		var version api.BundleVersionSummary
		var createdAtRaw string
		var statusValue int
		var sourceVersionID sql.NullString
		if err := rows.Scan(&version.BundleVersionID, &statusValue, &version.CreatedBy, &createdAtRaw, &sourceVersionID); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("scan version row: %w", err))
		}
		createdAt, err := time.Parse(time.RFC3339Nano, createdAtRaw)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("parse version timestamp: %w", err))
		}
		version.Scope = scope
		version.Status = api.BundleStatus(statusValue)
		version.CreatedAt = createdAt
		version.SourceVersionID = sourceVersionID.String
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("iterate versions: %w", err))
	}

	cursor := ""
	if len(versions) > int(limit) {
		versions = versions[:limit]
		cursor = nextCursor(limit, offset, int(limit)+1)
	}

	s.logOperation("list", actor.Subject, scope, "success", "", map[string]any{
		"version_count": len(versions),
	})
	s.writeAudit(ctx, auditRecord{
		EventType: api.AuditEventTypeList,
		Actor:     actor.Subject,
		Scope:     scope,
		Result:    "success",
		RequestID: req.Header().Get("X-Request-Id"),
		TraceID:   req.Header().Get("X-Trace-Id"),
		Metadata: map[string]any{
			"versionCount": len(versions),
		},
	})

	return connect.NewResponse(&api.ListBundleVersionsResponse{Versions: versions, NextCursor: cursor}), nil
}

func (s *Server) handleActivateBundleVersion(ctx context.Context, req *connect.Request[api.ActivateBundleVersionRequest]) (*connect.Response[api.ActivateBundleVersionResponse], error) {
	actor, scope, err := s.authorizeAndValidate(ctx, req.Header(), req.Msg.Scope, api.RoleAdmin)
	if err != nil {
		return nil, err
	}
	versionID := strings.TrimSpace(req.Msg.BundleVersionID)
	if versionID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bundleVersionId is required"))
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("begin activate transaction: %w", err))
	}
	defer tx.Rollback()

	var previousVersionID string
	_ = tx.QueryRowContext(ctx, `SELECT bundle_version_id FROM active_bundle_pointers
	WHERE workspace_id = ? AND project_id = ? AND environment_id = ?`,
		scope.WorkspaceID, scope.ProjectID, scope.EnvironmentID).Scan(&previousVersionID)

	if _, err := s.getVersionSummaryWithExecutor(ctx, tx, scope, versionID); err != nil {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO active_bundle_pointers (
		workspace_id, project_id, environment_id, bundle_version_id, updated_by, updated_at
	) VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT(workspace_id, project_id, environment_id)
	DO UPDATE SET bundle_version_id = excluded.bundle_version_id, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		scope.WorkspaceID,
		scope.ProjectID,
		scope.EnvironmentID,
		versionID,
		actor.Subject,
		now,
	); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("update active pointer: %w", err))
	}

	if _, err := tx.ExecContext(ctx, `UPDATE bundle_versions
	SET status = ?
	WHERE workspace_id = ? AND project_id = ? AND environment_id = ?`,
		int(api.BundleStatusArchived), scope.WorkspaceID, scope.ProjectID, scope.EnvironmentID,
	); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("archive bundle versions: %w", err))
	}

	if _, err := tx.ExecContext(ctx, `UPDATE bundle_versions
	SET status = ?
	WHERE bundle_version_id = ?`, int(api.BundleStatusActive), versionID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("activate bundle version: %w", err))
	}

	if err := tx.Commit(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("commit activate transaction: %w", err))
	}

	currentSummary, err := s.getVersionSummary(ctx, scope, versionID)
	if err != nil {
		return nil, err
	}

	var previousSummary *api.BundleVersionSummary
	if previousVersionID != "" {
		summary, err := s.getVersionSummary(ctx, scope, previousVersionID)
		if err == nil {
			previousSummary = &summary
		}
	}

	s.logOperation("activate", actor.Subject, scope, "success", "", map[string]any{
		"target_bundle_version_id": versionID,
	})
	s.writeAudit(ctx, auditRecord{
		EventType:       api.AuditEventTypeActivate,
		Actor:           actor.Subject,
		Scope:           scope,
		TargetVersionID: versionID,
		Result:          "success",
		RequestID:       req.Header().Get("X-Request-Id"),
		TraceID:         req.Header().Get("X-Trace-Id"),
	})

	return connect.NewResponse(&api.ActivateBundleVersionResponse{Previous: previousSummary, Current: currentSummary}), nil
}

func (s *Server) handleRotateBundleVersion(ctx context.Context, req *connect.Request[api.RotateBundleVersionRequest]) (*connect.Response[api.RotateBundleVersionResponse], error) {
	actor, scope, err := s.authorizeAndValidate(ctx, req.Header(), req.Msg.Scope, api.RoleWriter)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("begin rotate transaction: %w", err))
	}
	defer tx.Rollback()

	sourceVersionID := strings.TrimSpace(req.Msg.FromVersionID)
	if sourceVersionID == "" {
		sourceVersionID, err = s.resolveActiveVersionIDWithExecutor(ctx, tx, scope)
		if err != nil {
			return nil, err
		}
	}
	if _, err := s.getVersionSummaryWithExecutor(ctx, tx, scope, sourceVersionID); err != nil {
		return nil, err
	}

	rows, err := tx.QueryContext(ctx, `SELECT file_type, ciphertext, wrapped_dek, payload_nonce, dek_nonce, checksum, byte_length
	FROM bundle_files WHERE bundle_version_id = ?`, sourceVersionID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("query source files: %w", err))
	}
	defer rows.Close()

	type encryptedFile struct {
		FileType   int
		Ciphertext []byte
		WrappedDEK []byte
		Nonce      []byte
		DEKNonce   []byte
		Checksum   string
		ByteLength int64
	}

	files := make([]encryptedFile, 0, 2)
	for rows.Next() {
		var file encryptedFile
		if err := rows.Scan(&file.FileType, &file.Ciphertext, &file.WrappedDEK, &file.Nonce, &file.DEKNonce, &file.Checksum, &file.ByteLength); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("scan source file: %w", err))
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("iterate source files: %w", err))
	}
	if len(files) == 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("source version contains no files"))
	}

	newVersionID := newIdentifier("ver")
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `INSERT INTO bundle_versions (
		bundle_version_id, workspace_id, project_id, environment_id, status, created_by, created_at, source_version_id, metadata
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, '')`,
		newVersionID,
		scope.WorkspaceID,
		scope.ProjectID,
		scope.EnvironmentID,
		int(api.BundleStatusActive),
		actor.Subject,
		now.Format(time.RFC3339Nano),
		sourceVersionID,
	); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("insert rotated version: %w", err))
	}

	for _, file := range files {
		if _, err := tx.ExecContext(ctx, `INSERT INTO bundle_files (
			bundle_version_id, file_type, ciphertext, wrapped_dek, payload_nonce, dek_nonce, checksum, byte_length
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			newVersionID,
			file.FileType,
			file.Ciphertext,
			file.WrappedDEK,
			file.Nonce,
			file.DEKNonce,
			file.Checksum,
			file.ByteLength,
		); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("copy file payload: %w", err))
		}
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO active_bundle_pointers (
		workspace_id, project_id, environment_id, bundle_version_id, updated_by, updated_at
	) VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT(workspace_id, project_id, environment_id)
	DO UPDATE SET bundle_version_id = excluded.bundle_version_id, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		scope.WorkspaceID,
		scope.ProjectID,
		scope.EnvironmentID,
		newVersionID,
		actor.Subject,
		now.Format(time.RFC3339Nano),
	); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("update active pointer during rotate: %w", err))
	}

	if _, err := tx.ExecContext(ctx, `UPDATE bundle_versions
	SET status = ?
	WHERE workspace_id = ? AND project_id = ? AND environment_id = ?`,
		int(api.BundleStatusArchived), scope.WorkspaceID, scope.ProjectID, scope.EnvironmentID,
	); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("archive versions in rotate: %w", err))
	}
	if _, err := tx.ExecContext(ctx, `UPDATE bundle_versions
	SET status = ?
	WHERE bundle_version_id = ?`, int(api.BundleStatusActive), newVersionID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("activate rotated version: %w", err))
	}

	if err := tx.Commit(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("commit rotate transaction: %w", err))
	}

	summary, err := s.getVersionSummary(ctx, scope, newVersionID)
	if err != nil {
		return nil, err
	}

	s.logOperation("rotate", actor.Subject, scope, "success", "", map[string]any{
		"source_bundle_version_id": sourceVersionID,
		"target_bundle_version_id": newVersionID,
	})
	s.writeAudit(ctx, auditRecord{
		EventType:       api.AuditEventTypeRotate,
		Actor:           actor.Subject,
		Scope:           scope,
		TargetVersionID: newVersionID,
		Result:          "success",
		RequestID:       req.Header().Get("X-Request-Id"),
		TraceID:         req.Header().Get("X-Trace-Id"),
		Metadata: map[string]any{
			"sourceBundleVersionId": sourceVersionID,
		},
	})

	return connect.NewResponse(&api.RotateBundleVersionResponse{
		BundleVersionID: newVersionID,
		Current:         summary,
	}), nil
}

func (s *Server) handleGetPolicy(ctx context.Context, req *connect.Request[api.GetPolicyRequest]) (*connect.Response[api.GetPolicyResponse], error) {
	actor, scope, err := s.authorizeAndValidate(ctx, req.Header(), req.Msg.Scope, api.RoleAdmin)
	if err != nil {
		return nil, err
	}

	revision, bindings, err := s.getPolicyBindings(ctx, scope)
	if err != nil {
		return nil, err
	}

	s.logOperation("get-policy", actor.Subject, scope, "success", "", map[string]any{"binding_count": len(bindings)})
	return connect.NewResponse(&api.GetPolicyResponse{Scope: scope, PolicyRevision: revision, Bindings: bindings}), nil
}

func (s *Server) handleSetPolicy(ctx context.Context, req *connect.Request[api.SetPolicyRequest]) (*connect.Response[api.SetPolicyResponse], error) {
	actor, scope, err := s.authorizeAndValidate(ctx, req.Header(), req.Msg.Scope, api.RoleAdmin)
	if err != nil {
		return nil, err
	}

	for _, binding := range req.Msg.Bindings {
		if strings.TrimSpace(binding.Subject) == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("policy subject is required"))
		}
		if binding.Role != api.RoleReader && binding.Role != api.RoleWriter && binding.Role != api.RoleAdmin {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("policy role must be reader, writer, or admin"))
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("begin set policy transaction: %w", err))
	}
	defer tx.Rollback()

	var currentRevision int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(policy_revision), 0)
	FROM policy_bindings WHERE workspace_id = ? AND project_id = ? AND environment_id = ?`,
		scope.WorkspaceID, scope.ProjectID, scope.EnvironmentID).Scan(&currentRevision); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("query policy revision: %w", err))
	}
	nextRevision := currentRevision + 1

	if _, err := tx.ExecContext(ctx, `DELETE FROM policy_bindings
	WHERE workspace_id = ? AND project_id = ? AND environment_id = ?`,
		scope.WorkspaceID, scope.ProjectID, scope.EnvironmentID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("delete existing policy bindings: %w", err))
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, binding := range req.Msg.Bindings {
		if _, err := tx.ExecContext(ctx, `INSERT INTO policy_bindings (
			workspace_id, project_id, environment_id, subject, role, policy_revision, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			scope.WorkspaceID,
			scope.ProjectID,
			scope.EnvironmentID,
			binding.Subject,
			int(binding.Role),
			nextRevision,
			now,
		); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("insert policy binding: %w", err))
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("commit set policy transaction: %w", err))
	}

	s.logOperation("set-policy", actor.Subject, scope, "success", "", map[string]any{"policy_revision": nextRevision, "binding_count": len(req.Msg.Bindings)})
	s.writeAudit(ctx, auditRecord{
		EventType: api.AuditEventTypePolicyUpdate,
		Actor:     actor.Subject,
		Scope:     scope,
		Result:    "success",
		RequestID: req.Header().Get("X-Request-Id"),
		TraceID:   req.Header().Get("X-Trace-Id"),
		Metadata: map[string]any{
			"policyRevision": nextRevision,
			"bindingCount":   len(req.Msg.Bindings),
		},
	})

	return connect.NewResponse(&api.SetPolicyResponse{Scope: scope, PolicyRevision: nextRevision}), nil
}

func (s *Server) handleListAuditEvents(ctx context.Context, req *connect.Request[api.ListAuditEventsRequest]) (*connect.Response[api.ListAuditEventsResponse], error) {
	actor, scope, err := s.authorizeAndValidate(ctx, req.Header(), req.Msg.Scope, api.RoleAdmin)
	if err != nil {
		return nil, err
	}

	limit := api.NormalizeLimit(req.Msg.Limit)
	offset, err := parseCursor(req.Msg.Cursor)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	query := `SELECT event_id, event_type, actor, target_bundle_version_id, result, failure_code, request_id, trace_id, created_at, metadata
	FROM audit_events
	WHERE workspace_id = ? AND project_id = ? AND environment_id = ?`
	args := []any{scope.WorkspaceID, scope.ProjectID, scope.EnvironmentID}

	if req.Msg.EventType != api.AuditEventTypeUnspecified {
		query += " AND event_type = ?"
		args = append(args, int(req.Msg.EventType))
	}
	if actorFilter := strings.TrimSpace(req.Msg.Actor); actorFilter != "" {
		query += " AND actor = ?"
		args = append(args, actorFilter)
	}
	if req.Msg.StartTime != nil {
		query += " AND created_at >= ?"
		args = append(args, req.Msg.StartTime.UTC().Format(time.RFC3339Nano))
	}
	if req.Msg.EndTime != nil {
		query += " AND created_at <= ?"
		args = append(args, req.Msg.EndTime.UTC().Format(time.RFC3339Nano))
	}

	query += " ORDER BY created_at DESC, event_id DESC LIMIT ? OFFSET ?"
	args = append(args, int(limit)+1, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("query audit events: %w", err))
	}
	defer rows.Close()

	events := make([]api.AuditEvent, 0, limit)
	for rows.Next() {
		var event api.AuditEvent
		var eventType int
		var createdAtRaw string
		if err := rows.Scan(
			&event.EventID,
			&eventType,
			&event.Actor,
			&event.TargetBundleVersionID,
			&event.Result,
			&event.FailureCode,
			&event.RequestID,
			&event.TraceID,
			&createdAtRaw,
			&event.Metadata,
		); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("scan audit event: %w", err))
		}
		createdAt, err := time.Parse(time.RFC3339Nano, createdAtRaw)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("parse audit timestamp: %w", err))
		}
		event.EventType = api.AuditEventType(eventType)
		event.CreatedAt = createdAt
		event.Scope = scope
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("iterate audit events: %w", err))
	}

	next := ""
	if len(events) > int(limit) {
		events = events[:limit]
		next = nextCursor(limit, offset, int(limit)+1)
	}

	s.logOperation("list-audit", actor.Subject, scope, "success", "", map[string]any{"event_count": len(events)})
	return connect.NewResponse(&api.ListAuditEventsResponse{Events: events, NextCursor: next}), nil
}

func (s *Server) authorizeAndValidate(ctx context.Context, headers http.Header, scope api.Scope, minimum api.Role) (identity, api.Scope, error) {
	if err := scope.Validate(); err != nil {
		return identity{}, api.Scope{}, connect.NewError(connect.CodeInvalidArgument, err)
	}
	actor, err := s.authenticate(headers)
	if err != nil {
		return identity{}, api.Scope{}, err
	}
	if _, err := s.authorizeAtLeast(ctx, actor, scope, minimum); err != nil {
		return identity{}, api.Scope{}, err
	}
	return actor, scope, nil
}

func (s *Server) resolveActiveVersionID(ctx context.Context, scope api.Scope) (string, error) {
	return s.resolveActiveVersionIDWithExecutor(ctx, s.db, scope)
}

func (s *Server) resolveActiveVersionIDWithExecutor(ctx context.Context, exec queryRowExecutor, scope api.Scope) (string, error) {
	var versionID string
	err := exec.QueryRowContext(ctx, `SELECT bundle_version_id FROM active_bundle_pointers
	WHERE workspace_id = ? AND project_id = ? AND environment_id = ?`, scope.WorkspaceID, scope.ProjectID, scope.EnvironmentID).Scan(&versionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", connect.NewError(connect.CodeNotFound, errors.New("active bundle version not found"))
		}
		return "", connect.NewError(connect.CodeInternal, fmt.Errorf("query active pointer: %w", err))
	}
	return versionID, nil
}

func (s *Server) getVersionSummary(ctx context.Context, scope api.Scope, versionID string) (api.BundleVersionSummary, error) {
	return s.getVersionSummaryWithExecutor(ctx, s.db, scope, versionID)
}

func (s *Server) getVersionSummaryWithExecutor(ctx context.Context, exec queryRowExecutor, scope api.Scope, versionID string) (api.BundleVersionSummary, error) {
	query := `SELECT status, created_by, created_at, source_version_id FROM bundle_versions
	WHERE workspace_id = ? AND project_id = ? AND environment_id = ? AND bundle_version_id = ?`
	var status int
	var createdBy string
	var createdAtRaw string
	var sourceVersionID sql.NullString
	if err := exec.QueryRowContext(ctx, query, scope.WorkspaceID, scope.ProjectID, scope.EnvironmentID, versionID).Scan(&status, &createdBy, &createdAtRaw, &sourceVersionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return api.BundleVersionSummary{}, connect.NewError(connect.CodeNotFound, errors.New("bundle version not found in scope"))
		}
		return api.BundleVersionSummary{}, connect.NewError(connect.CodeInternal, fmt.Errorf("query version summary: %w", err))
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdAtRaw)
	if err != nil {
		return api.BundleVersionSummary{}, connect.NewError(connect.CodeInternal, fmt.Errorf("parse version created_at: %w", err))
	}
	return api.BundleVersionSummary{
		BundleVersionID: versionID,
		Scope:           scope,
		Status:          api.BundleStatus(status),
		CreatedBy:       createdBy,
		CreatedAt:       createdAt,
		SourceVersionID: sourceVersionID.String,
	}, nil
}

func validateFiles(files []api.BundleFilePayload) ([]api.BundleFilePayload, error) {
	validated := make([]api.BundleFilePayload, 0, len(files))
	seen := map[api.FileType]struct{}{}
	for _, file := range files {
		if file.FileType != api.FileTypeEnv && file.FileType != api.FileTypeDevVars {
			return nil, fmt.Errorf("unsupported file type: %d", file.FileType)
		}
		if _, ok := seen[file.FileType]; ok {
			return nil, fmt.Errorf("duplicate file type in request: %s", file.FileType.String())
		}
		if len(file.Content) == 0 {
			return nil, fmt.Errorf("file content for %s is empty", file.FileType.String())
		}
		seen[file.FileType] = struct{}{}
		validated = append(validated, file)
	}
	sort.SliceStable(validated, func(i, j int) bool {
		return validated[i].FileType < validated[j].FileType
	})
	return validated, nil
}

func checksum(value []byte) string {
	hash := sha256.Sum256(value)
	return hex.EncodeToString(hash[:])
}

func (s *Server) masterKeyForScope(scope api.Scope) ([]byte, error) {
	if key, ok := s.workspaceKeys[scope.WorkspaceID]; ok {
		return key, nil
	}
	if len(s.defaultKey) == 32 {
		return s.defaultKey, nil
	}
	return nil, fmt.Errorf("no workspace master key for workspace %q", scope.WorkspaceID)
}

type auditRecord struct {
	EventType       api.AuditEventType
	Actor           string
	Scope           api.Scope
	TargetVersionID string
	Result          string
	FailureCode     string
	RequestID       string
	TraceID         string
	Metadata        map[string]any
}

func (s *Server) writeAudit(ctx context.Context, record auditRecord) {
	metadataJSON := "{}"
	if len(record.Metadata) > 0 {
		if encoded, err := json.Marshal(record.Metadata); err == nil {
			metadataJSON = string(encoded)
		}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_events (
		event_id, event_type, actor, workspace_id, project_id, environment_id, target_bundle_version_id,
		result, failure_code, request_id, trace_id, created_at, metadata
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		newIdentifier("evt"),
		int(record.EventType),
		record.Actor,
		record.Scope.WorkspaceID,
		record.Scope.ProjectID,
		record.Scope.EnvironmentID,
		record.TargetVersionID,
		record.Result,
		record.FailureCode,
		record.RequestID,
		record.TraceID,
		time.Now().UTC().Format(time.RFC3339Nano),
		metadataJSON,
	)
	if err != nil {
		s.logger.Error("failed to persist audit event", "event_type", record.EventType.String(), "error", err)
	}
}

func (s *Server) logOperation(operation string, actor string, scope api.Scope, result string, failureCode string, extra map[string]any) {
	attrs := []any{
		"operation", operation,
		"actor", actor,
		"workspace_id", scope.WorkspaceID,
		"project_id", scope.ProjectID,
		"environment_id", scope.EnvironmentID,
		"result", result,
	}
	if failureCode != "" {
		attrs = append(attrs, "failure_code", failureCode)
	}
	for key, value := range extra {
		attrs = append(attrs, key, value)
	}
	s.logger.Info("thenv operation", attrs...)
}

func collectFileTypeStrings(files []api.BundleFilePayload) []string {
	values := make([]string, 0, len(files))
	for _, file := range files {
		values = append(values, file.FileType.String())
	}
	sort.Strings(values)
	return values
}

type queryRowExecutor interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func newIdentifier(prefix string) string {
	bytes := make([]byte, 10)
	_, _ = rand.Read(bytes)
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(bytes))
}

func (s *Server) getPolicyBindings(ctx context.Context, scope api.Scope) (int64, []api.PolicyBinding, error) {
	var revision int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(policy_revision), 0)
	FROM policy_bindings WHERE workspace_id = ? AND project_id = ? AND environment_id = ?`,
		scope.WorkspaceID, scope.ProjectID, scope.EnvironmentID).Scan(&revision); err != nil {
		return 0, nil, connect.NewError(connect.CodeInternal, fmt.Errorf("query policy revision: %w", err))
	}
	if revision == 0 {
		return 0, []api.PolicyBinding{}, nil
	}

	rows, err := s.db.QueryContext(ctx, `SELECT subject, role FROM policy_bindings
	WHERE workspace_id = ? AND project_id = ? AND environment_id = ? AND policy_revision = ?
	ORDER BY subject ASC`,
		scope.WorkspaceID, scope.ProjectID, scope.EnvironmentID, revision)
	if err != nil {
		return 0, nil, connect.NewError(connect.CodeInternal, fmt.Errorf("query policy bindings: %w", err))
	}
	defer rows.Close()

	bindings := make([]api.PolicyBinding, 0)
	for rows.Next() {
		var subject string
		var roleValue int
		if err := rows.Scan(&subject, &roleValue); err != nil {
			return 0, nil, connect.NewError(connect.CodeInternal, fmt.Errorf("scan policy binding: %w", err))
		}
		bindings = append(bindings, api.PolicyBinding{Subject: subject, Role: api.Role(roleValue)})
	}
	if err := rows.Err(); err != nil {
		return 0, nil, connect.NewError(connect.CodeInternal, fmt.Errorf("iterate policy bindings: %w", err))
	}
	return revision, bindings, nil
}
