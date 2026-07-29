package service

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"

	"connectrpc.com/connect"
	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/servers/devhud-deck/internal/authn"
	"github.com/delinoio/oss/servers/devhud-deck/internal/contracts"
	"github.com/delinoio/oss/servers/devhud-deck/internal/database"
	"github.com/delinoio/oss/servers/devhud-deck/internal/rpcerr"
	"github.com/google/uuid"
)

func viewerFromContext(ctx context.Context) (contracts.Viewer, error) {
	viewer, ok := authn.ViewerFromContext(ctx)
	if !ok || viewer.AccountID == uuid.Nil {
		return contracts.Viewer{}, rpcerr.New(connect.CodeUnauthenticated,
			deckv1.ErrorReason_ERROR_REASON_AUTHENTICATION_REQUIRED)
	}
	return viewer, nil
}

func parseUUID(value *deckv1.UuidV7) (uuid.UUID, error) {
	if value == nil {
		return uuid.Nil, rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
	id, err := uuid.Parse(value.Value)
	if err != nil || id.Version() != 7 || value.Value != strings.ToLower(id.String()) {
		return uuid.Nil, rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
	return id, nil
}

func ownerID(owner *deckv1.Owner) (uuid.UUID, error) {
	if owner == nil {
		return uuid.Nil, rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
	switch owner.Scope {
	case deckv1.OwnerScope_OWNER_SCOPE_PERSONAL:
		return parseUUID(owner.GetAccountId())
	case deckv1.OwnerScope_OWNER_SCOPE_ORGANIZATION:
		return parseUUID(owner.GetOrganizationId())
	default:
		return uuid.Nil, rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
}

func authorizeOwner(
	viewer contracts.Viewer,
	owner *deckv1.Owner,
	manage bool,
) (uuid.UUID, error) {
	id, err := ownerID(owner)
	if err != nil {
		return uuid.Nil, err
	}
	switch owner.Scope {
	case deckv1.OwnerScope_OWNER_SCOPE_PERSONAL:
		if id != viewer.AccountID {
			return uuid.Nil, rpcerr.New(connect.CodePermissionDenied,
				deckv1.ErrorReason_ERROR_REASON_PERMISSION_DENIED)
		}
	case deckv1.OwnerScope_OWNER_SCOPE_ORGANIZATION:
		allowed := viewer.IsMember(id)
		if manage {
			allowed = viewer.CanManage(id)
		}
		if !allowed {
			return uuid.Nil, rpcerr.New(connect.CodePermissionDenied,
				deckv1.ErrorReason_ERROR_REASON_PERMISSION_DENIED)
		}
	default:
		return uuid.Nil, rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
	return id, nil
}

func authorizeBilling(
	viewer contracts.Viewer,
	billing *deckv1.BillingSelection,
) error {
	if billing == nil ||
		(billing.GetOrganizationId().GetValue() == "" &&
			billing.GetTeamId().GetValue() == "") {
		return nil
	}
	organizationID, err := parseUUID(billing.GetOrganizationId())
	if err != nil {
		return err
	}
	var teamID uuid.UUID
	if billing.GetTeamId().GetValue() != "" {
		teamID, err = parseUUID(billing.GetTeamId())
		if err != nil {
			return err
		}
	}
	if !viewer.CanUseTeam(organizationID, teamID) {
		return rpcerr.New(connect.CodePermissionDenied,
			deckv1.ErrorReason_ERROR_REASON_PERMISSION_DENIED)
	}
	return nil
}

func validateExpected(
	expected *deckv1.Revision,
	resourceID uuid.UUID,
	hasher interface {
		ETag(uuid.UUID, uint64) string
	},
) (uint64, error) {
	if expected == nil || expected.Value == 0 || expected.Etag == "" ||
		expected.Etag != hasher.ETag(resourceID, expected.Value) {
		return 0, rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
	return expected.Value, nil
}

func mapDatabaseError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, database.ErrNotFound) {
		return rpcerr.New(connect.CodeNotFound,
			deckv1.ErrorReason_ERROR_REASON_NOT_FOUND)
	}
	if errors.Is(err, database.ErrIdempotencyConflict) {
		return rpcerr.Conflict(
			deckv1.ErrorReason_ERROR_REASON_IDEMPOTENCY_CONFLICT, nil, nil)
	}
	if errors.Is(err, database.ErrDeletionInProgress) {
		return rpcerr.New(connect.CodeFailedPrecondition,
			deckv1.ErrorReason_ERROR_REASON_DELETION_IN_PROGRESS)
	}
	if errors.Is(err, database.ErrAccountSwitch) {
		return rpcerr.New(connect.CodeFailedPrecondition,
			deckv1.ErrorReason_ERROR_REASON_REGISTRATION_CLEANUP_PENDING)
	}
	var limit *database.LimitError
	if errors.As(err, &limit) {
		reason := deckv1.ErrorReason_ERROR_REASON_PERSONAL_VIEW_LIMIT_REACHED
		if limit.Organization {
			reason = deckv1.ErrorReason_ERROR_REASON_ORGANIZATION_VIEW_LIMIT_REACHED
		}
		return rpcerr.New(connect.CodeResourceExhausted, reason)
	}
	var stale *database.StaleError
	if errors.As(err, &stale) {
		var id *deckv1.UuidV7
		var revision *deckv1.Revision
		if stale.ResourceID != uuid.Nil {
			id = &deckv1.UuidV7{Value: stale.ResourceID.String()}
		}
		if stale.Revision != 0 {
			revision = &deckv1.Revision{Value: stale.Revision}
		}
		return rpcerr.Conflict(
			deckv1.ErrorReason_ERROR_REASON_STALE_REVISION, id, revision)
	}
	return rpcerr.New(connect.CodeInternal,
		deckv1.ErrorReason_ERROR_REASON_UNSPECIFIED)
}

func pageSize(page *deckv1.PageRequest, defaultSize, maxSize uint32) uint32 {
	if page == nil || page.PageSize == 0 {
		return defaultSize
	}
	if page.PageSize > maxSize {
		return maxSize
	}
	return page.PageSize
}

func encodeOffset(value uint32) []byte {
	encoded := make([]byte, 4)
	binary.BigEndian.PutUint32(encoded, value)
	return encoded
}

func decodeOffset(value []byte) uint32 { return binary.BigEndian.Uint32(value) }
