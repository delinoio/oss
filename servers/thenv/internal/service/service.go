package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	thenvv1 "github.com/delinoio/oss/servers/thenv/gen/proto/thenv/v1"
	"github.com/delinoio/oss/servers/thenv/internal/contracts"
	"github.com/delinoio/oss/servers/thenv/internal/logging"

	_ "modernc.org/sqlite"
)

const (
	defaultListLimit = 20
	maxListLimit     = 100
)

type Config struct {
	DBPath                string
	MasterKeyBase64       string
	BootstrapAdminSubject string
}

type Service struct {
	db                    *sql.DB
	logger                *logging.Logger
	masterKey             []byte
	bootstrapAdminSubject string
}

type scopeKey struct {
	workspaceID   string
	projectID     string
	environmentID string
}

type callMeta struct {
	subject      string
	actor        string
	requestID    string
	traceID      string
	role         thenvv1.Role
	authSource   authIdentitySource
	subjectBound bool
}

type authIdentitySource uint8

const (
	authIdentitySourceUnspecified authIdentitySource = iota
	authIdentitySourceHeader
	authIdentitySourceHashedLegacy
)

func (s authIdentitySource) String() string {
	switch s {
	case authIdentitySourceHeader:
		return "header"
	case authIdentitySourceHashedLegacy:
		return "hashed-legacy"
	default:
		return "unspecified"
	}
}

type queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func New(ctx context.Context, cfg Config, logger *logging.Logger) (*Service, error) {
	if logger == nil {
		logger = logging.New()
	}

	masterKey, err := decodeMasterKey(cfg.MasterKeyBase64)
	if err != nil {
		return nil, fmt.Errorf("decode master key: %w", err)
	}

	if strings.TrimSpace(cfg.DBPath) == "" {
		return nil, errors.New("db path is required")
	}

	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable sqlite foreign keys: %w", err)
	}

	if err := applyMigrations(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply migrations: %w", err)
	}

	bootstrap := strings.TrimSpace(cfg.BootstrapAdminSubject)
	if bootstrap == "" {
		bootstrap = "admin"
	}

	return &Service{
		db:                    db,
		logger:                logger,
		masterKey:             masterKey,
		bootstrapAdminSubject: bootstrap,
	}, nil
}

func (s *Service) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func applyMigrations(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS bundle_versions (
			bundle_version_id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			project_id TEXT NOT NULL,
			environment_id TEXT NOT NULL,
			status INTEGER NOT NULL,
			created_by TEXT NOT NULL,
			created_at_unix_ns INTEGER NOT NULL,
			source_version_id TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_bundle_versions_scope_time
			ON bundle_versions(workspace_id, project_id, environment_id, created_at_unix_ns DESC, bundle_version_id DESC)`,
		`CREATE TABLE IF NOT EXISTS bundle_file_payloads (
			bundle_version_id TEXT NOT NULL,
			file_type INTEGER NOT NULL,
			ciphertext BLOB NOT NULL,
			ciphertext_nonce BLOB NOT NULL,
			encrypted_dek BLOB NOT NULL,
			dek_nonce BLOB NOT NULL,
			checksum TEXT NOT NULL,
			byte_length INTEGER NOT NULL,
			PRIMARY KEY(bundle_version_id, file_type),
			FOREIGN KEY(bundle_version_id) REFERENCES bundle_versions(bundle_version_id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS active_bundle_pointers (
			workspace_id TEXT NOT NULL,
			project_id TEXT NOT NULL,
			environment_id TEXT NOT NULL,
			bundle_version_id TEXT NOT NULL,
			updated_by TEXT NOT NULL,
			updated_at_unix_ns INTEGER NOT NULL,
			PRIMARY KEY(workspace_id, project_id, environment_id)
		)`,
		`CREATE TABLE IF NOT EXISTS policy_bindings (
			workspace_id TEXT NOT NULL,
			project_id TEXT NOT NULL,
			environment_id TEXT NOT NULL,
			subject TEXT NOT NULL,
			role INTEGER NOT NULL,
			PRIMARY KEY(workspace_id, project_id, environment_id, subject)
		)`,
		`CREATE TABLE IF NOT EXISTS policy_revisions (
			workspace_id TEXT NOT NULL,
			project_id TEXT NOT NULL,
			environment_id TEXT NOT NULL,
			revision INTEGER NOT NULL,
			updated_at_unix_ns INTEGER NOT NULL,
			PRIMARY KEY(workspace_id, project_id, environment_id)
		)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			event_id TEXT PRIMARY KEY,
			event_type INTEGER NOT NULL,
			actor TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			project_id TEXT NOT NULL,
			environment_id TEXT NOT NULL,
			bundle_version_id TEXT,
			target_bundle_version_id TEXT,
			outcome INTEGER NOT NULL,
			request_id TEXT NOT NULL,
			trace_id TEXT NOT NULL,
			created_at_unix_ns INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_events_scope_time
			ON audit_events(workspace_id, project_id, environment_id, created_at_unix_ns DESC, event_id DESC)`,
	}

	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func decodeMasterKey(raw string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("THENV_MASTER_KEY_B64 is required")
	}

	decoded, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(trimmed)
		if err != nil {
			return nil, err
		}
	}
	if len(decoded) != 32 {
		return nil, fmt.Errorf("master key must decode to 32 bytes, got %d", len(decoded))
	}
	return decoded, nil
}

func (s *Service) PushBundleVersion(ctx context.Context, req *connect.Request[thenvv1.PushBundleVersionRequest]) (*connect.Response[thenvv1.PushBundleVersionResponse], error) {
	scope, err := validateScope(req.Msg.GetScope())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	files, err := normalizeFiles(req.Msg.GetFiles())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	meta, err := s.authorize(ctx, req.Header(), scope, thenvv1.Role_ROLE_WRITER, contracts.ThenvOperationPush, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PUSH)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	versionID, err := newID("ver")
	if err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationPush, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PUSH, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", versionID, collectFileTypes(files), nil)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationPush, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PUSH, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", versionID, collectFileTypes(files), err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	status := thenvv1.BundleStatus_BUNDLE_STATUS_ARCHIVED
	currentActiveID, err := getActiveVersionID(ctx, tx, scope)
	if err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationPush, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PUSH, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", versionID, collectFileTypes(files), err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PUSH, "", versionID, thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if currentActiveID == "" {
		status = thenvv1.BundleStatus_BUNDLE_STATUS_ACTIVE
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO bundle_versions(bundle_version_id, workspace_id, project_id, environment_id, status, created_by, created_at_unix_ns, source_version_id)
		 VALUES(?, ?, ?, ?, ?, ?, ?, NULL)`,
		versionID,
		scope.workspaceID,
		scope.projectID,
		scope.environmentID,
		int32(status),
		meta.actor,
		now.UnixNano(),
	); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationPush, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PUSH, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", versionID, collectFileTypes(files), err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PUSH, "", versionID, thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	for _, file := range files {
		encrypted, encErr := s.encrypt(file.GetPlaintext())
		if encErr != nil {
			s.logOperation(meta, scope, contracts.ThenvOperationPush, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PUSH, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", versionID, collectFileTypes(files), encErr)
			_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PUSH, "", versionID, thenvv1.Outcome_OUTCOME_FAILED)
			return nil, connect.NewError(connect.CodeInternal, encErr)
		}

		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO bundle_file_payloads(bundle_version_id, file_type, ciphertext, ciphertext_nonce, encrypted_dek, dek_nonce, checksum, byte_length)
			 VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
			versionID,
			int32(file.GetFileType()),
			encrypted.ciphertext,
			encrypted.ciphertextNonce,
			encrypted.encryptedDEK,
			encrypted.dekNonce,
			encrypted.checksum,
			encrypted.byteLength,
		); err != nil {
			s.logOperation(meta, scope, contracts.ThenvOperationPush, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PUSH, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", versionID, collectFileTypes(files), err)
			_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PUSH, "", versionID, thenvv1.Outcome_OUTCOME_FAILED)
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	if status == thenvv1.BundleStatus_BUNDLE_STATUS_ACTIVE {
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO active_bundle_pointers(workspace_id, project_id, environment_id, bundle_version_id, updated_by, updated_at_unix_ns)
			 VALUES(?, ?, ?, ?, ?, ?)
			 ON CONFLICT(workspace_id, project_id, environment_id)
			 DO UPDATE SET bundle_version_id = excluded.bundle_version_id, updated_by = excluded.updated_by, updated_at_unix_ns = excluded.updated_at_unix_ns`,
			scope.workspaceID,
			scope.projectID,
			scope.environmentID,
			versionID,
			meta.actor,
			now.UnixNano(),
		); err != nil {
			s.logOperation(meta, scope, contracts.ThenvOperationPush, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PUSH, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", versionID, collectFileTypes(files), err)
			_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PUSH, "", versionID, thenvv1.Outcome_OUTCOME_FAILED)
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	if err := tx.Commit(); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationPush, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PUSH, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", versionID, collectFileTypes(files), err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PUSH, "", versionID, thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	summary, err := s.readBundleSummary(ctx, s.db, scope, versionID)
	if err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationPush, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PUSH, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", versionID, collectFileTypes(files), err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PUSH, "", versionID, thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if err := s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PUSH, versionID, "", thenvv1.Outcome_OUTCOME_SUCCESS); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationPush, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PUSH, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", versionID, collectFileTypes(files), err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	s.logOperation(meta, scope, contracts.ThenvOperationPush, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PUSH, contracts.RoleDecisionAllow, contracts.OperationResultSuccess, versionID, "", collectFileTypes(files), nil)
	return connect.NewResponse(&thenvv1.PushBundleVersionResponse{Version: summary}), nil
}

func (s *Service) PullActiveBundle(ctx context.Context, req *connect.Request[thenvv1.PullActiveBundleRequest]) (*connect.Response[thenvv1.PullActiveBundleResponse], error) {
	scope, err := validateScope(req.Msg.GetScope())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	meta, err := s.authorize(ctx, req.Header(), scope, thenvv1.Role_ROLE_READER, contracts.ThenvOperationPull, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PULL)
	if err != nil {
		return nil, err
	}

	versionID := strings.TrimSpace(req.Msg.GetBundleVersionId())
	if versionID == "" {
		versionID, err = getActiveVersionID(ctx, s.db, scope)
		if err != nil {
			s.logOperation(meta, scope, contracts.ThenvOperationPull, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PULL, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", "", nil, err)
			_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PULL, "", "", thenvv1.Outcome_OUTCOME_FAILED)
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if versionID == "" {
			err := errors.New("no active bundle version")
			s.logOperation(meta, scope, contracts.ThenvOperationPull, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PULL, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", "", nil, err)
			_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PULL, "", "", thenvv1.Outcome_OUTCOME_FAILED)
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
	}

	summary, err := s.readBundleSummary(ctx, s.db, scope, versionID)
	if err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationPull, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PULL, contracts.RoleDecisionAllow, contracts.OperationResultFailure, versionID, "", nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PULL, versionID, "", thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT file_type, ciphertext, ciphertext_nonce, encrypted_dek, dek_nonce
		 FROM bundle_file_payloads
		 WHERE bundle_version_id = ?
		 ORDER BY file_type ASC`,
		versionID,
	)
	if err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationPull, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PULL, contracts.RoleDecisionAllow, contracts.OperationResultFailure, versionID, "", nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PULL, versionID, "", thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	defer rows.Close()

	files := make([]*thenvv1.BundleFile, 0)
	fileTypes := make([]thenvv1.FileType, 0)
	for rows.Next() {
		var fileTypeRaw int32
		var ciphertext []byte
		var ciphertextNonce []byte
		var encryptedDEK []byte
		var dekNonce []byte
		if err := rows.Scan(&fileTypeRaw, &ciphertext, &ciphertextNonce, &encryptedDEK, &dekNonce); err != nil {
			s.logOperation(meta, scope, contracts.ThenvOperationPull, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PULL, contracts.RoleDecisionAllow, contracts.OperationResultFailure, versionID, "", nil, err)
			_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PULL, versionID, "", thenvv1.Outcome_OUTCOME_FAILED)
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		fileType := thenvv1.FileType(fileTypeRaw)
		plaintext, err := s.decrypt(ciphertext, ciphertextNonce, encryptedDEK, dekNonce)
		if err != nil {
			s.logOperation(meta, scope, contracts.ThenvOperationPull, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PULL, contracts.RoleDecisionAllow, contracts.OperationResultFailure, versionID, "", nil, err)
			_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PULL, versionID, "", thenvv1.Outcome_OUTCOME_FAILED)
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		files = append(files, &thenvv1.BundleFile{FileType: fileType, Plaintext: plaintext})
		fileTypes = append(fileTypes, fileType)
	}
	if err := rows.Err(); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationPull, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PULL, contracts.RoleDecisionAllow, contracts.OperationResultFailure, versionID, "", nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PULL, versionID, "", thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if err := s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PULL, versionID, "", thenvv1.Outcome_OUTCOME_SUCCESS); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationPull, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PULL, contracts.RoleDecisionAllow, contracts.OperationResultFailure, versionID, "", fileTypes, err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	s.logOperation(meta, scope, contracts.ThenvOperationPull, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_PULL, contracts.RoleDecisionAllow, contracts.OperationResultSuccess, versionID, "", fileTypes, nil)
	return connect.NewResponse(&thenvv1.PullActiveBundleResponse{Version: summary, Files: files}), nil
}

func (s *Service) ListBundleVersions(ctx context.Context, req *connect.Request[thenvv1.ListBundleVersionsRequest]) (*connect.Response[thenvv1.ListBundleVersionsResponse], error) {
	scope, err := validateScope(req.Msg.GetScope())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	meta, err := s.authorize(ctx, req.Header(), scope, thenvv1.Role_ROLE_READER, contracts.ThenvOperationList, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST)
	if err != nil {
		return nil, err
	}

	limit := normalizeLimit(req.Msg.GetLimit())
	offset, err := parseCursor(req.Msg.GetCursor())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	rows, err := s.db.QueryContext(
		ctx,
		`SELECT bundle_version_id
		 FROM bundle_versions
		 WHERE workspace_id = ? AND project_id = ? AND environment_id = ?
		 ORDER BY created_at_unix_ns DESC, bundle_version_id DESC
		 LIMIT ? OFFSET ?`,
		scope.workspaceID,
		scope.projectID,
		scope.environmentID,
		limit+1,
		offset,
	)
	if err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationList, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", "", nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, "", "", thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	defer rows.Close()

	versionIDs := make([]string, 0, limit+1)
	for rows.Next() {
		var versionID string
		if err := rows.Scan(&versionID); err != nil {
			s.logOperation(meta, scope, contracts.ThenvOperationList, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", "", nil, err)
			_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, "", "", thenvv1.Outcome_OUTCOME_FAILED)
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		versionIDs = append(versionIDs, versionID)
	}
	if err := rows.Err(); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationList, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", "", nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, "", "", thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	nextCursor := ""
	if len(versionIDs) > limit {
		versionIDs = versionIDs[:limit]
		nextCursor = strconv.Itoa(offset + limit)
	}

	versions := make([]*thenvv1.BundleVersionSummary, 0, len(versionIDs))
	for _, versionID := range versionIDs {
		summary, err := s.readBundleSummary(ctx, s.db, scope, versionID)
		if err != nil {
			s.logOperation(meta, scope, contracts.ThenvOperationList, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, contracts.RoleDecisionAllow, contracts.OperationResultFailure, versionID, "", nil, err)
			_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, "", "", thenvv1.Outcome_OUTCOME_FAILED)
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		versions = append(versions, summary)
	}

	if err := s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, "", "", thenvv1.Outcome_OUTCOME_SUCCESS); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationList, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", "", nil, err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	s.logOperation(meta, scope, contracts.ThenvOperationList, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, contracts.RoleDecisionAllow, contracts.OperationResultSuccess, "", "", nil, nil)
	return connect.NewResponse(&thenvv1.ListBundleVersionsResponse{Versions: versions, NextCursor: nextCursor}), nil
}

func (s *Service) ActivateBundleVersion(ctx context.Context, req *connect.Request[thenvv1.ActivateBundleVersionRequest]) (*connect.Response[thenvv1.ActivateBundleVersionResponse], error) {
	scope, err := validateScope(req.Msg.GetScope())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	targetVersionID := strings.TrimSpace(req.Msg.GetBundleVersionId())
	if targetVersionID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bundle_version_id is required"))
	}

	meta, err := s.authorize(ctx, req.Header(), scope, thenvv1.Role_ROLE_ADMIN, contracts.ThenvOperationActivate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ACTIVATE)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationActivate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ACTIVATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", targetVersionID, nil, err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := s.readBundleSummary(ctx, tx, scope, targetVersionID); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationActivate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ACTIVATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", targetVersionID, nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ACTIVATE, "", targetVersionID, thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	previousActiveID, err := getActiveVersionID(ctx, tx, scope)
	if err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationActivate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ACTIVATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", targetVersionID, nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ACTIVATE, "", targetVersionID, thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if previousActiveID != "" && previousActiveID != targetVersionID {
		if _, err := tx.ExecContext(
			ctx,
			`UPDATE bundle_versions
			 SET status = ?
			 WHERE bundle_version_id = ? AND workspace_id = ? AND project_id = ? AND environment_id = ?`,
			int32(thenvv1.BundleStatus_BUNDLE_STATUS_ARCHIVED),
			previousActiveID,
			scope.workspaceID,
			scope.projectID,
			scope.environmentID,
		); err != nil {
			s.logOperation(meta, scope, contracts.ThenvOperationActivate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ACTIVATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, previousActiveID, targetVersionID, nil, err)
			_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ACTIVATE, previousActiveID, targetVersionID, thenvv1.Outcome_OUTCOME_FAILED)
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	if _, err := tx.ExecContext(
		ctx,
		`UPDATE bundle_versions
		 SET status = ?
		 WHERE bundle_version_id = ? AND workspace_id = ? AND project_id = ? AND environment_id = ?`,
		int32(thenvv1.BundleStatus_BUNDLE_STATUS_ACTIVE),
		targetVersionID,
		scope.workspaceID,
		scope.projectID,
		scope.environmentID,
	); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationActivate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ACTIVATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, previousActiveID, targetVersionID, nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ACTIVATE, previousActiveID, targetVersionID, thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO active_bundle_pointers(workspace_id, project_id, environment_id, bundle_version_id, updated_by, updated_at_unix_ns)
		 VALUES(?, ?, ?, ?, ?, ?)
		 ON CONFLICT(workspace_id, project_id, environment_id)
		 DO UPDATE SET bundle_version_id = excluded.bundle_version_id, updated_by = excluded.updated_by, updated_at_unix_ns = excluded.updated_at_unix_ns`,
		scope.workspaceID,
		scope.projectID,
		scope.environmentID,
		targetVersionID,
		meta.actor,
		time.Now().UTC().UnixNano(),
	); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationActivate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ACTIVATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, previousActiveID, targetVersionID, nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ACTIVATE, previousActiveID, targetVersionID, thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if err := tx.Commit(); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationActivate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ACTIVATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, previousActiveID, targetVersionID, nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ACTIVATE, previousActiveID, targetVersionID, thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	activeSummary, err := s.readBundleSummary(ctx, s.db, scope, targetVersionID)
	if err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationActivate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ACTIVATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, previousActiveID, targetVersionID, nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ACTIVATE, previousActiveID, targetVersionID, thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var previousSummary *thenvv1.BundleVersionSummary
	if previousActiveID != "" {
		previousSummary, _ = s.readBundleSummary(ctx, s.db, scope, previousActiveID)
	}

	if err := s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ACTIVATE, previousActiveID, targetVersionID, thenvv1.Outcome_OUTCOME_SUCCESS); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationActivate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ACTIVATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, previousActiveID, targetVersionID, nil, err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	s.logOperation(meta, scope, contracts.ThenvOperationActivate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ACTIVATE, contracts.RoleDecisionAllow, contracts.OperationResultSuccess, previousActiveID, targetVersionID, nil, nil)
	return connect.NewResponse(&thenvv1.ActivateBundleVersionResponse{PreviousActive: previousSummary, Active: activeSummary}), nil
}

func (s *Service) RotateBundleVersion(ctx context.Context, req *connect.Request[thenvv1.RotateBundleVersionRequest]) (*connect.Response[thenvv1.RotateBundleVersionResponse], error) {
	scope, err := validateScope(req.Msg.GetScope())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	meta, err := s.authorize(ctx, req.Header(), scope, thenvv1.Role_ROLE_WRITER, contracts.ThenvOperationRotate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationRotate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", "", nil, err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	sourceVersionID := strings.TrimSpace(req.Msg.GetFromVersionId())
	if sourceVersionID == "" {
		sourceVersionID, err = getActiveVersionID(ctx, tx, scope)
		if err != nil {
			s.logOperation(meta, scope, contracts.ThenvOperationRotate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", "", nil, err)
			_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, "", "", thenvv1.Outcome_OUTCOME_FAILED)
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	if sourceVersionID == "" {
		err := errors.New("no source version available for rotate")
		s.logOperation(meta, scope, contracts.ThenvOperationRotate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", "", nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, "", "", thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	if _, err := s.readBundleSummary(ctx, tx, scope, sourceVersionID); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationRotate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, sourceVersionID, "", nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, sourceVersionID, "", thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	newVersionID, err := newID("ver")
	if err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationRotate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, sourceVersionID, "", nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, sourceVersionID, "", thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	now := time.Now().UTC().UnixNano()
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO bundle_versions(bundle_version_id, workspace_id, project_id, environment_id, status, created_by, created_at_unix_ns, source_version_id)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		newVersionID,
		scope.workspaceID,
		scope.projectID,
		scope.environmentID,
		int32(thenvv1.BundleStatus_BUNDLE_STATUS_ARCHIVED),
		meta.actor,
		now,
		sourceVersionID,
	); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationRotate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, sourceVersionID, newVersionID, nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, sourceVersionID, newVersionID, thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO bundle_file_payloads(bundle_version_id, file_type, ciphertext, ciphertext_nonce, encrypted_dek, dek_nonce, checksum, byte_length)
		 SELECT ?, file_type, ciphertext, ciphertext_nonce, encrypted_dek, dek_nonce, checksum, byte_length
		 FROM bundle_file_payloads
		 WHERE bundle_version_id = ?`,
		newVersionID,
		sourceVersionID,
	); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationRotate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, sourceVersionID, newVersionID, nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, sourceVersionID, newVersionID, thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	previousActiveID, err := getActiveVersionID(ctx, tx, scope)
	if err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationRotate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, sourceVersionID, newVersionID, nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, sourceVersionID, newVersionID, thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if previousActiveID != "" && previousActiveID != newVersionID {
		if _, err := tx.ExecContext(
			ctx,
			`UPDATE bundle_versions
			 SET status = ?
			 WHERE bundle_version_id = ? AND workspace_id = ? AND project_id = ? AND environment_id = ?`,
			int32(thenvv1.BundleStatus_BUNDLE_STATUS_ARCHIVED),
			previousActiveID,
			scope.workspaceID,
			scope.projectID,
			scope.environmentID,
		); err != nil {
			s.logOperation(meta, scope, contracts.ThenvOperationRotate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, previousActiveID, newVersionID, nil, err)
			_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, previousActiveID, newVersionID, thenvv1.Outcome_OUTCOME_FAILED)
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	if _, err := tx.ExecContext(
		ctx,
		`UPDATE bundle_versions
		 SET status = ?
		 WHERE bundle_version_id = ? AND workspace_id = ? AND project_id = ? AND environment_id = ?`,
		int32(thenvv1.BundleStatus_BUNDLE_STATUS_ACTIVE),
		newVersionID,
		scope.workspaceID,
		scope.projectID,
		scope.environmentID,
	); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationRotate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, previousActiveID, newVersionID, nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, previousActiveID, newVersionID, thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO active_bundle_pointers(workspace_id, project_id, environment_id, bundle_version_id, updated_by, updated_at_unix_ns)
		 VALUES(?, ?, ?, ?, ?, ?)
		 ON CONFLICT(workspace_id, project_id, environment_id)
		 DO UPDATE SET bundle_version_id = excluded.bundle_version_id, updated_by = excluded.updated_by, updated_at_unix_ns = excluded.updated_at_unix_ns`,
		scope.workspaceID,
		scope.projectID,
		scope.environmentID,
		newVersionID,
		meta.actor,
		now,
	); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationRotate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, previousActiveID, newVersionID, nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, previousActiveID, newVersionID, thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if err := tx.Commit(); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationRotate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, previousActiveID, newVersionID, nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, previousActiveID, newVersionID, thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	versionSummary, err := s.readBundleSummary(ctx, s.db, scope, newVersionID)
	if err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationRotate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, previousActiveID, newVersionID, nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, previousActiveID, newVersionID, thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var previousSummary *thenvv1.BundleVersionSummary
	if previousActiveID != "" {
		previousSummary, _ = s.readBundleSummary(ctx, s.db, scope, previousActiveID)
	}

	if err := s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, previousActiveID, newVersionID, thenvv1.Outcome_OUTCOME_SUCCESS); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationRotate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, previousActiveID, newVersionID, nil, err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	s.logOperation(meta, scope, contracts.ThenvOperationRotate, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_ROTATE, contracts.RoleDecisionAllow, contracts.OperationResultSuccess, previousActiveID, newVersionID, nil, nil)
	return connect.NewResponse(&thenvv1.RotateBundleVersionResponse{Version: versionSummary, PreviousActive: previousSummary}), nil
}

func (s *Service) GetPolicy(ctx context.Context, req *connect.Request[thenvv1.GetPolicyRequest]) (*connect.Response[thenvv1.GetPolicyResponse], error) {
	scope, err := validateScope(req.Msg.GetScope())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	meta, err := s.authorize(ctx, req.Header(), scope, thenvv1.Role_ROLE_ADMIN, contracts.ThenvOperationList, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST)
	if err != nil {
		return nil, err
	}

	bindings, revision, err := s.readPolicy(ctx, s.db, scope)
	if err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationList, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", "", nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, "", "", thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if err := s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, "", "", thenvv1.Outcome_OUTCOME_SUCCESS); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationList, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", "", nil, err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	s.logOperation(meta, scope, contracts.ThenvOperationList, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, contracts.RoleDecisionAllow, contracts.OperationResultSuccess, "", "", nil, nil)
	return connect.NewResponse(&thenvv1.GetPolicyResponse{Bindings: bindings, PolicyRevision: revision}), nil
}

func (s *Service) SetPolicy(ctx context.Context, req *connect.Request[thenvv1.SetPolicyRequest]) (*connect.Response[thenvv1.SetPolicyResponse], error) {
	scope, err := validateScope(req.Msg.GetScope())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	meta, err := s.authorize(ctx, req.Header(), scope, thenvv1.Role_ROLE_ADMIN, contracts.ThenvOperationPolicy, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_POLICY_UPDATE)
	if err != nil {
		return nil, err
	}

	for _, binding := range req.Msg.GetBindings() {
		if strings.TrimSpace(binding.GetSubject()) == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("policy subject cannot be empty"))
		}
		if binding.GetRole() == thenvv1.Role_ROLE_UNSPECIFIED {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("policy role cannot be unspecified"))
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationPolicy, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_POLICY_UPDATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", "", nil, err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(
		ctx,
		`DELETE FROM policy_bindings WHERE workspace_id = ? AND project_id = ? AND environment_id = ?`,
		scope.workspaceID,
		scope.projectID,
		scope.environmentID,
	); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationPolicy, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_POLICY_UPDATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", "", nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_POLICY_UPDATE, "", "", thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	for _, binding := range req.Msg.GetBindings() {
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO policy_bindings(workspace_id, project_id, environment_id, subject, role)
			 VALUES(?, ?, ?, ?, ?)`,
			scope.workspaceID,
			scope.projectID,
			scope.environmentID,
			strings.TrimSpace(binding.GetSubject()),
			int32(binding.GetRole()),
		); err != nil {
			s.logOperation(meta, scope, contracts.ThenvOperationPolicy, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_POLICY_UPDATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", "", nil, err)
			_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_POLICY_UPDATE, "", "", thenvv1.Outcome_OUTCOME_FAILED)
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	var revision int64
	if err := tx.QueryRowContext(
		ctx,
		`SELECT revision FROM policy_revisions WHERE workspace_id = ? AND project_id = ? AND environment_id = ?`,
		scope.workspaceID,
		scope.projectID,
		scope.environmentID,
	).Scan(&revision); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			s.logOperation(meta, scope, contracts.ThenvOperationPolicy, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_POLICY_UPDATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", "", nil, err)
			_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_POLICY_UPDATE, "", "", thenvv1.Outcome_OUTCOME_FAILED)
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		revision = 0
	}
	revision++

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO policy_revisions(workspace_id, project_id, environment_id, revision, updated_at_unix_ns)
		 VALUES(?, ?, ?, ?, ?)
		 ON CONFLICT(workspace_id, project_id, environment_id)
		 DO UPDATE SET revision = excluded.revision, updated_at_unix_ns = excluded.updated_at_unix_ns`,
		scope.workspaceID,
		scope.projectID,
		scope.environmentID,
		revision,
		time.Now().UTC().UnixNano(),
	); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationPolicy, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_POLICY_UPDATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", "", nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_POLICY_UPDATE, "", "", thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if err := tx.Commit(); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationPolicy, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_POLICY_UPDATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", "", nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_POLICY_UPDATE, "", "", thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	bindings, _, err := s.readPolicy(ctx, s.db, scope)
	if err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationPolicy, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_POLICY_UPDATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", "", nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_POLICY_UPDATE, "", "", thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if err := s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_POLICY_UPDATE, "", "", thenvv1.Outcome_OUTCOME_SUCCESS); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationPolicy, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_POLICY_UPDATE, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", "", nil, err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	s.logOperation(meta, scope, contracts.ThenvOperationPolicy, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_POLICY_UPDATE, contracts.RoleDecisionAllow, contracts.OperationResultSuccess, "", "", nil, nil)
	return connect.NewResponse(&thenvv1.SetPolicyResponse{Bindings: bindings, PolicyRevision: revision}), nil
}

func (s *Service) ListAuditEvents(ctx context.Context, req *connect.Request[thenvv1.ListAuditEventsRequest]) (*connect.Response[thenvv1.ListAuditEventsResponse], error) {
	scope, err := validateScope(req.Msg.GetScope())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	meta, err := s.authorize(ctx, req.Header(), scope, thenvv1.Role_ROLE_ADMIN, contracts.ThenvOperationList, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST)
	if err != nil {
		return nil, err
	}

	limit := normalizeLimit(req.Msg.GetLimit())
	offset, err := parseCursor(req.Msg.GetCursor())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	parts := []string{
		`SELECT event_id, event_type, actor, workspace_id, project_id, environment_id, bundle_version_id, target_bundle_version_id, outcome, request_id, trace_id, created_at_unix_ns
		 FROM audit_events
		 WHERE workspace_id = ? AND project_id = ? AND environment_id = ?`,
	}
	args := []any{scope.workspaceID, scope.projectID, scope.environmentID}

	if req.Msg.GetEventType() != thenvv1.AuditEventType_AUDIT_EVENT_TYPE_UNSPECIFIED {
		parts = append(parts, "AND event_type = ?")
		args = append(args, int32(req.Msg.GetEventType()))
	}
	if actor := strings.TrimSpace(req.Msg.GetActor()); actor != "" {
		parts = append(parts, "AND actor = ?")
		args = append(args, actor)
	}
	if fromTime := req.Msg.GetFromTime(); fromTime != nil {
		parts = append(parts, "AND created_at_unix_ns >= ?")
		args = append(args, fromTime.AsTime().UTC().UnixNano())
	}
	if toTime := req.Msg.GetToTime(); toTime != nil {
		parts = append(parts, "AND created_at_unix_ns <= ?")
		args = append(args, toTime.AsTime().UTC().UnixNano())
	}

	parts = append(parts, "ORDER BY created_at_unix_ns DESC, event_id DESC LIMIT ? OFFSET ?")
	args = append(args, limit+1, offset)

	rows, err := s.db.QueryContext(ctx, strings.Join(parts, " "), args...)
	if err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationList, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", "", nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, "", "", thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	defer rows.Close()

	events := make([]*thenvv1.AuditEvent, 0, limit+1)
	for rows.Next() {
		var eventID string
		var eventTypeRaw int32
		var actor string
		var workspaceID string
		var projectID string
		var environmentID string
		var bundleVersionID sql.NullString
		var targetBundleVersionID sql.NullString
		var outcomeRaw int32
		var requestID string
		var traceID string
		var createdAtUnixNs int64

		if err := rows.Scan(
			&eventID,
			&eventTypeRaw,
			&actor,
			&workspaceID,
			&projectID,
			&environmentID,
			&bundleVersionID,
			&targetBundleVersionID,
			&outcomeRaw,
			&requestID,
			&traceID,
			&createdAtUnixNs,
		); err != nil {
			s.logOperation(meta, scope, contracts.ThenvOperationList, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", "", nil, err)
			_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, "", "", thenvv1.Outcome_OUTCOME_FAILED)
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		event := &thenvv1.AuditEvent{
			EventId:               eventID,
			EventType:             thenvv1.AuditEventType(eventTypeRaw),
			Actor:                 actor,
			Scope:                 &thenvv1.Scope{WorkspaceId: workspaceID, ProjectId: projectID, EnvironmentId: environmentID},
			BundleVersionId:       nullableString(bundleVersionID),
			TargetBundleVersionId: nullableString(targetBundleVersionID),
			Outcome:               thenvv1.Outcome(outcomeRaw),
			RequestId:             requestID,
			TraceId:               traceID,
			CreatedAt:             timestamppb.New(time.Unix(0, createdAtUnixNs).UTC()),
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationList, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", "", nil, err)
		_ = s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, "", "", thenvv1.Outcome_OUTCOME_FAILED)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	nextCursor := ""
	if len(events) > limit {
		events = events[:limit]
		nextCursor = strconv.Itoa(offset + limit)
	}

	if err := s.writeAudit(ctx, s.db, meta, scope, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, "", "", thenvv1.Outcome_OUTCOME_SUCCESS); err != nil {
		s.logOperation(meta, scope, contracts.ThenvOperationList, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, contracts.RoleDecisionAllow, contracts.OperationResultFailure, "", "", nil, err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	s.logOperation(meta, scope, contracts.ThenvOperationList, thenvv1.AuditEventType_AUDIT_EVENT_TYPE_LIST, contracts.RoleDecisionAllow, contracts.OperationResultSuccess, "", "", nil, nil)
	return connect.NewResponse(&thenvv1.ListAuditEventsResponse{Events: events, NextCursor: nextCursor}), nil
}

func (s *Service) readPolicy(ctx context.Context, q queryer, scope scopeKey) ([]*thenvv1.PolicyBinding, int64, error) {
	rows, err := q.QueryContext(
		ctx,
		`SELECT subject, role
		 FROM policy_bindings
		 WHERE workspace_id = ? AND project_id = ? AND environment_id = ?
		 ORDER BY subject ASC`,
		scope.workspaceID,
		scope.projectID,
		scope.environmentID,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	bindings := make([]*thenvv1.PolicyBinding, 0)
	for rows.Next() {
		var subject string
		var roleRaw int32
		if err := rows.Scan(&subject, &roleRaw); err != nil {
			return nil, 0, err
		}
		bindings = append(bindings, &thenvv1.PolicyBinding{Subject: subject, Role: thenvv1.Role(roleRaw)})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	var revision int64
	if err := q.QueryRowContext(
		ctx,
		`SELECT revision
		 FROM policy_revisions
		 WHERE workspace_id = ? AND project_id = ? AND environment_id = ?`,
		scope.workspaceID,
		scope.projectID,
		scope.environmentID,
	).Scan(&revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return bindings, 0, nil
		}
		return nil, 0, err
	}

	return bindings, revision, nil
}

func (s *Service) readBundleSummary(ctx context.Context, q queryer, scope scopeKey, versionID string) (*thenvv1.BundleVersionSummary, error) {
	var statusRaw int32
	var createdBy string
	var createdAtUnixNs int64
	var sourceVersionID sql.NullString
	if err := q.QueryRowContext(
		ctx,
		`SELECT status, created_by, created_at_unix_ns, source_version_id
		 FROM bundle_versions
		 WHERE bundle_version_id = ? AND workspace_id = ? AND project_id = ? AND environment_id = ?`,
		versionID,
		scope.workspaceID,
		scope.projectID,
		scope.environmentID,
	).Scan(&statusRaw, &createdBy, &createdAtUnixNs, &sourceVersionID); err != nil {
		return nil, err
	}

	fileTypeRows, err := q.QueryContext(
		ctx,
		`SELECT file_type FROM bundle_file_payloads WHERE bundle_version_id = ? ORDER BY file_type ASC`,
		versionID,
	)
	if err != nil {
		return nil, err
	}
	defer fileTypeRows.Close()

	fileTypes := make([]thenvv1.FileType, 0)
	for fileTypeRows.Next() {
		var fileTypeRaw int32
		if err := fileTypeRows.Scan(&fileTypeRaw); err != nil {
			return nil, err
		}
		fileTypes = append(fileTypes, thenvv1.FileType(fileTypeRaw))
	}
	if err := fileTypeRows.Err(); err != nil {
		return nil, err
	}

	return &thenvv1.BundleVersionSummary{
		BundleVersionId: versionID,
		Status:          thenvv1.BundleStatus(statusRaw),
		CreatedBy:       createdBy,
		CreatedAt:       timestamppb.New(time.Unix(0, createdAtUnixNs).UTC()),
		FileTypes:       fileTypes,
		SourceVersionId: nullableString(sourceVersionID),
	}, nil
}

func (s *Service) authorize(
	ctx context.Context,
	headers http.Header,
	scope scopeKey,
	requiredRole thenvv1.Role,
	operation contracts.ThenvOperation,
	eventType thenvv1.AuditEventType,
) (callMeta, error) {
	meta := extractCallMeta(headers)
	if strings.TrimSpace(meta.subject) == "" {
		err := connect.NewError(connect.CodeUnauthenticated, errors.New("missing actor subject"))
		s.logOperation(meta, scope, operation, eventType, contracts.RoleDecisionDeny, contracts.OperationResultDenied, "", "", nil, err)
		return callMeta{}, err
	}
	if !meta.subjectBound {
		err := connect.NewError(connect.CodeUnauthenticated, errors.New("subject must match bearer token"))
		s.logOperation(meta, scope, operation, eventType, contracts.RoleDecisionDeny, contracts.OperationResultDenied, "", "", nil, err)
		return callMeta{}, err
	}

	if err := s.ensureBootstrapBinding(ctx, s.db, scope, meta.subject); err != nil {
		s.logOperation(meta, scope, operation, eventType, contracts.RoleDecisionDeny, contracts.OperationResultFailure, "", "", nil, err)
		return callMeta{}, connect.NewError(connect.CodeInternal, err)
	}

	role, ok, err := s.lookupRole(ctx, s.db, scope, meta.subject)
	if err != nil {
		s.logOperation(meta, scope, operation, eventType, contracts.RoleDecisionDeny, contracts.OperationResultFailure, "", "", nil, err)
		return callMeta{}, connect.NewError(connect.CodeInternal, err)
	}
	if !ok || !roleSatisfies(role, requiredRole) {
		meta.role = role
		auditErr := s.writeAudit(ctx, s.db, meta, scope, eventType, "", "", thenvv1.Outcome_OUTCOME_DENIED)
		if auditErr != nil {
			s.logOperation(meta, scope, operation, eventType, contracts.RoleDecisionDeny, contracts.OperationResultFailure, "", "", nil, auditErr)
			return callMeta{}, connect.NewError(connect.CodeInternal, auditErr)
		}

		err := connect.NewError(connect.CodePermissionDenied, fmt.Errorf("insufficient role: required=%s", requiredRole.String()))
		s.logOperation(meta, scope, operation, eventType, contracts.RoleDecisionDeny, contracts.OperationResultDenied, "", "", nil, err)
		return callMeta{}, err
	}

	meta.role = role
	s.logOperation(meta, scope, operation, eventType, contracts.RoleDecisionAllow, contracts.OperationResultSuccess, "", "", nil, nil)
	return meta, nil
}

func (s *Service) ensureBootstrapBinding(ctx context.Context, q queryer, scope scopeKey, subject string) error {
	if subject != s.bootstrapAdminSubject {
		return nil
	}

	_, found, err := s.lookupRole(ctx, q, scope, subject)
	if err != nil {
		return err
	}
	if found {
		return nil
	}

	tx, ok := q.(*sql.DB)
	if !ok {
		if _, err := q.ExecContext(
			ctx,
			`INSERT INTO policy_bindings(workspace_id, project_id, environment_id, subject, role)
			 VALUES(?, ?, ?, ?, ?)
			 ON CONFLICT(workspace_id, project_id, environment_id, subject) DO NOTHING`,
			scope.workspaceID,
			scope.projectID,
			scope.environmentID,
			subject,
			int32(thenvv1.Role_ROLE_ADMIN),
		); err != nil {
			return err
		}
		return bumpPolicyRevision(ctx, q, scope)
	}

	dbTx, err := tx.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = dbTx.Rollback()
	}()

	if _, err := dbTx.ExecContext(
		ctx,
		`INSERT INTO policy_bindings(workspace_id, project_id, environment_id, subject, role)
		 VALUES(?, ?, ?, ?, ?)
		 ON CONFLICT(workspace_id, project_id, environment_id, subject) DO NOTHING`,
		scope.workspaceID,
		scope.projectID,
		scope.environmentID,
		subject,
		int32(thenvv1.Role_ROLE_ADMIN),
	); err != nil {
		return err
	}
	if err := bumpPolicyRevision(ctx, dbTx, scope); err != nil {
		return err
	}
	return dbTx.Commit()
}

func bumpPolicyRevision(ctx context.Context, q queryer, scope scopeKey) error {
	var revision int64
	if err := q.QueryRowContext(
		ctx,
		`SELECT revision FROM policy_revisions WHERE workspace_id = ? AND project_id = ? AND environment_id = ?`,
		scope.workspaceID,
		scope.projectID,
		scope.environmentID,
	).Scan(&revision); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		revision = 0
	}
	revision++

	_, err := q.ExecContext(
		ctx,
		`INSERT INTO policy_revisions(workspace_id, project_id, environment_id, revision, updated_at_unix_ns)
		 VALUES(?, ?, ?, ?, ?)
		 ON CONFLICT(workspace_id, project_id, environment_id)
		 DO UPDATE SET revision = excluded.revision, updated_at_unix_ns = excluded.updated_at_unix_ns`,
		scope.workspaceID,
		scope.projectID,
		scope.environmentID,
		revision,
		time.Now().UTC().UnixNano(),
	)
	return err
}

func (s *Service) lookupRole(ctx context.Context, q queryer, scope scopeKey, subject string) (thenvv1.Role, bool, error) {
	var roleRaw int32
	err := q.QueryRowContext(
		ctx,
		`SELECT role FROM policy_bindings WHERE workspace_id = ? AND project_id = ? AND environment_id = ? AND subject = ?`,
		scope.workspaceID,
		scope.projectID,
		scope.environmentID,
		subject,
	).Scan(&roleRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return thenvv1.Role_ROLE_UNSPECIFIED, false, nil
		}
		return thenvv1.Role_ROLE_UNSPECIFIED, false, err
	}
	return thenvv1.Role(roleRaw), true, nil
}

func roleSatisfies(currentRole thenvv1.Role, requiredRole thenvv1.Role) bool {
	rank := map[thenvv1.Role]int{
		thenvv1.Role_ROLE_UNSPECIFIED: 0,
		thenvv1.Role_ROLE_READER:      1,
		thenvv1.Role_ROLE_WRITER:      2,
		thenvv1.Role_ROLE_ADMIN:       3,
	}
	return rank[currentRole] >= rank[requiredRole]
}

func extractCallMeta(headers http.Header) callMeta {
	bearerToken := extractBearerToken(headers)
	subject := strings.TrimSpace(headers.Get("X-Thenv-Subject"))
	authSource := authIdentitySourceUnspecified
	if subject != "" {
		authSource = authIdentitySourceHeader
	}
	subjectBound := subject != "" && bearerToken != "" && subject == bearerToken
	actor := subject
	if subjectBound {
		actor = hashLegacyTokenActor(subject)
		authSource = authIdentitySourceHashedLegacy
	}
	requestID := strings.TrimSpace(headers.Get("X-Request-Id"))
	if requestID == "" {
		requestID = newRequestID("req")
	}
	traceID := strings.TrimSpace(headers.Get("X-Trace-Id"))
	if traceID == "" {
		traceID = newRequestID("trace")
	}

	return callMeta{
		subject:      subject,
		actor:        actor,
		requestID:    requestID,
		traceID:      traceID,
		authSource:   authSource,
		subjectBound: subjectBound,
	}
}

func extractBearerToken(headers http.Header) string {
	authHeader := strings.TrimSpace(headers.Get("Authorization"))
	if len(authHeader) < len("Bearer ") {
		return ""
	}
	if !strings.EqualFold(authHeader[:len("Bearer ")], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(authHeader[len("Bearer "):])
}

func hashLegacyTokenActor(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "token_sha256:" + hex.EncodeToString(sum[:8])
}

func newRequestID(prefix string) string {
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(raw))
}

func validateScope(scope *thenvv1.Scope) (scopeKey, error) {
	if scope == nil {
		return scopeKey{}, errors.New("scope is required")
	}

	workspaceID := strings.TrimSpace(scope.GetWorkspaceId())
	projectID := strings.TrimSpace(scope.GetProjectId())
	environmentID := strings.TrimSpace(scope.GetEnvironmentId())
	if workspaceID == "" || projectID == "" || environmentID == "" {
		return scopeKey{}, errors.New("scope requires workspace_id, project_id, and environment_id")
	}

	return scopeKey{workspaceID: workspaceID, projectID: projectID, environmentID: environmentID}, nil
}

func normalizeFiles(files []*thenvv1.BundleFile) ([]*thenvv1.BundleFile, error) {
	if len(files) == 0 {
		return nil, errors.New("at least one file payload is required")
	}

	seen := map[thenvv1.FileType]bool{}
	normalized := make([]*thenvv1.BundleFile, 0, len(files))
	for _, file := range files {
		if file == nil {
			return nil, errors.New("file payload cannot be nil")
		}
		fileType := file.GetFileType()
		if fileType == thenvv1.FileType_FILE_TYPE_UNSPECIFIED {
			return nil, errors.New("file_type must not be unspecified")
		}
		if seen[fileType] {
			return nil, fmt.Errorf("duplicate file_type: %s", fileType.String())
		}
		seen[fileType] = true
		normalized = append(normalized, file)
	}
	return normalized, nil
}

func normalizeLimit(raw int32) int {
	limit := int(raw)
	if limit <= 0 {
		return defaultListLimit
	}
	if limit > maxListLimit {
		return maxListLimit
	}
	return limit
}

func parseCursor(cursor string) (int, error) {
	trimmed := strings.TrimSpace(cursor)
	if trimmed == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(trimmed)
	if err != nil || value < 0 {
		return 0, errors.New("invalid cursor")
	}
	return value, nil
}

func getActiveVersionID(ctx context.Context, q queryer, scope scopeKey) (string, error) {
	var versionID string
	err := q.QueryRowContext(
		ctx,
		`SELECT bundle_version_id FROM active_bundle_pointers WHERE workspace_id = ? AND project_id = ? AND environment_id = ?`,
		scope.workspaceID,
		scope.projectID,
		scope.environmentID,
	).Scan(&versionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return versionID, nil
}

type encryptedPayload struct {
	ciphertext      []byte
	ciphertextNonce []byte
	encryptedDEK    []byte
	dekNonce        []byte
	checksum        string
	byteLength      int
}

func (s *Service) encrypt(plaintext []byte) (*encryptedPayload, error) {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return nil, err
	}

	plaintextCipher, err := aes.NewCipher(dek)
	if err != nil {
		return nil, err
	}
	plaintextGCM, err := cipher.NewGCM(plaintextCipher)
	if err != nil {
		return nil, err
	}
	plaintextNonce := make([]byte, plaintextGCM.NonceSize())
	if _, err := rand.Read(plaintextNonce); err != nil {
		return nil, err
	}
	ciphertext := plaintextGCM.Seal(nil, plaintextNonce, plaintext, nil)

	masterCipher, err := aes.NewCipher(s.masterKey)
	if err != nil {
		return nil, err
	}
	masterGCM, err := cipher.NewGCM(masterCipher)
	if err != nil {
		return nil, err
	}
	dekNonce := make([]byte, masterGCM.NonceSize())
	if _, err := rand.Read(dekNonce); err != nil {
		return nil, err
	}
	encryptedDEK := masterGCM.Seal(nil, dekNonce, dek, nil)

	hash := sha256.Sum256(plaintext)
	return &encryptedPayload{
		ciphertext:      ciphertext,
		ciphertextNonce: plaintextNonce,
		encryptedDEK:    encryptedDEK,
		dekNonce:        dekNonce,
		checksum:        hex.EncodeToString(hash[:]),
		byteLength:      len(plaintext),
	}, nil
}

func (s *Service) decrypt(ciphertext []byte, ciphertextNonce []byte, encryptedDEK []byte, dekNonce []byte) ([]byte, error) {
	masterCipher, err := aes.NewCipher(s.masterKey)
	if err != nil {
		return nil, err
	}
	masterGCM, err := cipher.NewGCM(masterCipher)
	if err != nil {
		return nil, err
	}
	dek, err := masterGCM.Open(nil, dekNonce, encryptedDEK, nil)
	if err != nil {
		return nil, err
	}

	plaintextCipher, err := aes.NewCipher(dek)
	if err != nil {
		return nil, err
	}
	plaintextGCM, err := cipher.NewGCM(plaintextCipher)
	if err != nil {
		return nil, err
	}
	return plaintextGCM.Open(nil, ciphertextNonce, ciphertext, nil)
}

func newID(prefix string) (string, error) {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_%d_%s", prefix, time.Now().UTC().UnixNano(), hex.EncodeToString(random)), nil
}

func (s *Service) writeAudit(
	ctx context.Context,
	q queryer,
	meta callMeta,
	scope scopeKey,
	eventType thenvv1.AuditEventType,
	bundleVersionID string,
	targetBundleVersionID string,
	outcome thenvv1.Outcome,
) error {
	eventID, err := newID("audit")
	if err != nil {
		return err
	}

	_, err = q.ExecContext(
		ctx,
		`INSERT INTO audit_events(event_id, event_type, actor, workspace_id, project_id, environment_id, bundle_version_id, target_bundle_version_id, outcome, request_id, trace_id, created_at_unix_ns)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		eventID,
		int32(eventType),
		meta.actor,
		scope.workspaceID,
		scope.projectID,
		scope.environmentID,
		nullIfEmpty(bundleVersionID),
		nullIfEmpty(targetBundleVersionID),
		int32(outcome),
		meta.requestID,
		meta.traceID,
		time.Now().UTC().UnixNano(),
	)
	return err
}

func (s *Service) logOperation(
	meta callMeta,
	scope scopeKey,
	operation contracts.ThenvOperation,
	eventType thenvv1.AuditEventType,
	roleDecision contracts.RoleDecision,
	result contracts.OperationResult,
	bundleVersionID string,
	targetBundleVersionID string,
	fileTypes []thenvv1.FileType,
	err error,
) {
	fields := map[string]any{
		"operation":                operation,
		"event_type":               eventType.String(),
		"actor":                    meta.actor,
		"auth_identity_source":     meta.authSource.String(),
		"scope":                    map[string]string{"workspaceId": scope.workspaceID, "projectId": scope.projectID, "environmentId": scope.environmentID},
		"role_decision":            roleDecision,
		"role":                     meta.role.String(),
		"bundle_version_id":        bundleVersionID,
		"target_bundle_version_id": targetBundleVersionID,
		"file_types":               stringifyFileTypes(fileTypes),
		"result":                   result,
		"request_id":               meta.requestID,
		"trace_id":                 meta.traceID,
	}
	if err != nil {
		fields["error"] = err.Error()
	}
	s.logger.Event(fields)
}

func collectFileTypes(files []*thenvv1.BundleFile) []thenvv1.FileType {
	fileTypes := make([]thenvv1.FileType, 0, len(files))
	for _, file := range files {
		if file == nil {
			continue
		}
		fileTypes = append(fileTypes, file.GetFileType())
	}
	return fileTypes
}

func stringifyFileTypes(fileTypes []thenvv1.FileType) []string {
	if len(fileTypes) == 0 {
		return nil
	}
	result := make([]string, 0, len(fileTypes))
	for _, fileType := range fileTypes {
		result = append(result, fileType.String())
	}
	return result
}

func nullableString(value sql.NullString) string {
	if value.Valid {
		return value.String
	}
	return ""
}

func nullIfEmpty(value string) any {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return trimmed
}
