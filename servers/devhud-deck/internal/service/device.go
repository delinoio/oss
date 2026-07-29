package service

import (
	"context"
	"strings"
	"time"

	"connectrpc.com/connect"
	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/servers/devhud-deck/internal/audit"
	"github.com/delinoio/oss/servers/devhud-deck/internal/authn"
	"github.com/delinoio/oss/servers/devhud-deck/internal/contracts"
	"github.com/delinoio/oss/servers/devhud-deck/internal/database"
	"github.com/delinoio/oss/servers/devhud-deck/internal/rpcerr"
	"github.com/delinoio/oss/servers/devhud-deck/internal/security"
	"github.com/delinoio/oss/servers/devhud-deck/internal/shortcut"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

const deviceLease = 30 * 24 * time.Hour

func (service *Device) GetDevice(
	ctx context.Context,
	request *connect.Request[deckv1.GetDeviceRequest],
) (*connect.Response[deckv1.GetDeviceResponse], error) {
	viewer, err := viewerFromContext(ctx)
	if err != nil {
		return nil, err
	}
	deviceID, err := parseUUID(request.Msg.DeviceId)
	if err != nil {
		return nil, err
	}
	registration, err := service.dependencies.Store.GetDevice(
		ctx, viewer.AccountID, deviceID, service.dependencies.Clock.Now().UTC())
	if err != nil {
		return nil, mapDatabaseError(err)
	}
	if err := service.authorizeRegistrationViews(ctx, viewer, registration); err != nil {
		return nil, err
	}
	return connect.NewResponse(&deckv1.GetDeviceResponse{
		Registration: registration,
	}), nil
}

func (service *Device) RegisterDevice(
	ctx context.Context,
	request *connect.Request[deckv1.RegisterDeviceRequest],
) (*connect.Response[deckv1.RegisterDeviceResponse], error) {
	viewer, err := viewerFromContext(ctx)
	if err != nil {
		return nil, err
	}
	deviceID, err := parseUUID(request.Msg.DeviceId)
	if err != nil {
		return nil, err
	}
	idempotencyID, err := parseUUID(request.Msg.GetIdempotencyKey().GetValue())
	if err != nil {
		return nil, err
	}
	write, err := service.deviceWrite(
		ctx, viewer, request.Msg.Platform, request.Msg.DisplayName,
		request.Msg.Push, request.Msg.DetailedNotificationTextEnabled,
		request.Msg.Shortcuts, request.Msg.Widgets)
	if err != nil {
		return nil, err
	}
	hasExpected := request.Msg.ExpectedRevision != nil
	expected := uint64(0)
	if hasExpected {
		expected, err = validateExpected(request.Msg.ExpectedRevision, deviceID,
			service.dependencies.Hasher)
		if err != nil {
			return nil, err
		}
	}
	requestForDigest := proto.Clone(request.Msg).(*deckv1.RegisterDeviceRequest)
	requestForDigest.IdempotencyKey = nil
	serialized, err := proto.MarshalOptions{Deterministic: true}.Marshal(requestForDigest)
	if err != nil {
		return nil, rpcerr.New(connect.CodeInternal,
			deckv1.ErrorReason_ERROR_REASON_UNSPECIFIED)
	}
	digest := security.Digest(serialized)
	registrationID, err := service.dependencies.IDs.New()
	if err != nil {
		return nil, rpcerr.New(connect.CodeInternal,
			deckv1.ErrorReason_ERROR_REASON_UNSPECIFIED)
	}
	grant, err := security.NewGrant()
	if err != nil {
		return nil, rpcerr.New(connect.CodeInternal,
			deckv1.ErrorReason_ERROR_REASON_UNSPECIFIED)
	}
	now := service.dependencies.Clock.Now().UTC()
	ownerHash := service.dependencies.Hasher.Sum(
		"owner", "OWNER_SCOPE_PERSONAL:"+viewer.AccountID.String())
	registration, grant, replayed, err := service.dependencies.Store.RegisterDevice(
		ctx, database.RegisterDeviceParams{
			RegistrationID: registrationID,
			DeviceID:       deviceID,
			AccountID:      viewer.AccountID,
			IdempotencyKey: idempotencyID,
			RequestDigest:  digest,
			OwnerHash:      ownerHash,
			Write:          write,
			Expected:       expected,
			HasExpected:    hasExpected,
			Grant:          grant,
			LeaseExpiresAt: now.Add(deviceLease),
			Now:            now,
		})
	if err != nil {
		return nil, (&View{dependencies: service.dependencies}).mapStaleWithETag(err)
	}
	if err := (&View{dependencies: service.dependencies}).recordAudit(
		ctx, viewer.Subject, audit.EventDeviceRegistered,
		deckv1.OwnerScope_OWNER_SCOPE_PERSONAL, ownerHash[:],
		audit.ResourceDevice, deviceID, audit.OutcomeSuccess); err != nil {
		return nil, err
	}
	response := connect.NewResponse(&deckv1.RegisterDeviceResponse{
		Registration: registration,
		Idempotency: &deckv1.IdempotencyResult{
			Operation: deckv1.IdempotentOperation_IDEMPOTENT_OPERATION_REGISTER_DEVICE,
			Replayed:  replayed,
		},
	})
	response.Header().Set(authn.DeviceRevocationGrantHeader, grant)
	return response, nil
}

func (service *Device) UpdateDevice(
	ctx context.Context,
	request *connect.Request[deckv1.UpdateDeviceRequest],
) (*connect.Response[deckv1.UpdateDeviceResponse], error) {
	viewer, err := viewerFromContext(ctx)
	if err != nil {
		return nil, err
	}
	registrationID, err := parseUUID(request.Msg.RegistrationId)
	if err != nil {
		return nil, err
	}
	current, accountID, err := service.dependencies.Store.GetDeviceByRegistration(
		ctx, registrationID)
	if err != nil || accountID != viewer.AccountID {
		return nil, mapDatabaseError(database.ErrNotFound)
	}
	deviceID, err := parseUUID(current.Device.DeviceId)
	if err != nil {
		return nil, err
	}
	expected, err := validateExpected(request.Msg.ExpectedRevision, deviceID,
		service.dependencies.Hasher)
	if err != nil {
		return nil, err
	}
	write, err := service.deviceWrite(
		ctx, viewer, current.Device.Platform, request.Msg.DisplayName,
		request.Msg.Push, request.Msg.DetailedNotificationTextEnabled,
		request.Msg.Shortcuts, request.Msg.Widgets)
	if err != nil {
		return nil, err
	}
	registration, err := service.dependencies.Store.UpdateDevice(
		ctx, registrationID, viewer.AccountID, expected, write,
		service.dependencies.Clock.Now().UTC())
	if err != nil {
		return nil, (&View{dependencies: service.dependencies}).mapStaleWithETag(err)
	}
	ownerHash := service.dependencies.Hasher.Sum(
		"owner", "OWNER_SCOPE_PERSONAL:"+viewer.AccountID.String())
	if err := (&View{dependencies: service.dependencies}).recordAudit(
		ctx, viewer.Subject, audit.EventDeviceUpdated,
		deckv1.OwnerScope_OWNER_SCOPE_PERSONAL, ownerHash[:],
		audit.ResourceDevice, deviceID, audit.OutcomeSuccess); err != nil {
		return nil, err
	}
	return connect.NewResponse(&deckv1.UpdateDeviceResponse{
		Registration: registration,
	}), nil
}

func (service *Device) UnregisterDevice(
	ctx context.Context,
	request *connect.Request[deckv1.UnregisterDeviceRequest],
) (*connect.Response[deckv1.UnregisterDeviceResponse], error) {
	registrationID, err := parseUUID(request.Msg.RegistrationId)
	if err != nil {
		return nil, err
	}
	grant, cleanup := authn.CleanupGrantFromContext(ctx)
	accountID := uuid.Nil
	subject := "device-cleanup"
	if !cleanup {
		viewer, err := viewerFromContext(ctx)
		if err != nil {
			return nil, err
		}
		accountID = viewer.AccountID
		subject = viewer.Subject
	}
	wasRegistered, err := service.dependencies.Store.UnregisterDevice(
		ctx, registrationID, accountID, grant, service.dependencies.Clock.Now().UTC())
	if err != nil {
		return nil, mapDatabaseError(err)
	}
	ownerHash := service.dependencies.Hasher.Sum("owner",
		"OWNER_SCOPE_PERSONAL:"+accountID.String())
	outcome := audit.OutcomeNoop
	if wasRegistered {
		outcome = audit.OutcomeSuccess
	}
	if err := (&View{dependencies: service.dependencies}).recordAudit(
		ctx, subject, audit.EventDeviceUnregistered,
		deckv1.OwnerScope_OWNER_SCOPE_PERSONAL, ownerHash[:],
		audit.ResourceDevice, registrationID, outcome); err != nil {
		return nil, err
	}
	return connect.NewResponse(&deckv1.UnregisterDeviceResponse{
		RegistrationId: request.Msg.RegistrationId,
		WasRegistered:  wasRegistered,
	}), nil
}

func (service *Device) UpdateViewNotificationPreference(
	ctx context.Context,
	request *connect.Request[deckv1.UpdateViewNotificationPreferenceRequest],
) (*connect.Response[deckv1.UpdateViewNotificationPreferenceResponse], error) {
	viewer, err := viewerFromContext(ctx)
	if err != nil {
		return nil, err
	}
	registrationID, err := parseUUID(request.Msg.RegistrationId)
	if err != nil {
		return nil, err
	}
	_, accountID, err := service.dependencies.Store.GetDeviceByRegistration(
		ctx, registrationID)
	if err != nil || accountID != viewer.AccountID {
		return nil, mapDatabaseError(database.ErrNotFound)
	}
	viewID, err := parseUUID(request.Msg.ViewId)
	if err != nil {
		return nil, err
	}
	view, err := service.dependencies.Store.GetView(ctx, viewID)
	if err != nil {
		return nil, mapDatabaseError(err)
	}
	if _, err := authorizeOwner(viewer, view.Owner, false); err != nil {
		return nil, err
	}
	if request.Msg.ExpectedRevision == nil || request.Msg.Preference == nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
	expected, err := validateExpected(
		request.Msg.ExpectedRevision, viewID, service.dependencies.Hasher)
	if err != nil {
		return nil, err
	}
	notification, err := service.dependencies.Store.UpdateNotificationPreference(
		ctx, registrationID, viewID, expected,
		request.Msg.Preference, service.dependencies.Clock.Now().UTC())
	if err != nil {
		return nil, (&View{dependencies: service.dependencies}).mapStaleWithETag(err)
	}
	ownerHash := service.dependencies.Hasher.Sum(
		"owner", "OWNER_SCOPE_PERSONAL:"+viewer.AccountID.String())
	if err := (&View{dependencies: service.dependencies}).recordAudit(
		ctx, viewer.Subject, audit.EventNotificationPreferenceUpdated,
		deckv1.OwnerScope_OWNER_SCOPE_PERSONAL, ownerHash[:],
		audit.ResourceNotification, viewID, audit.OutcomeSuccess); err != nil {
		return nil, err
	}
	return connect.NewResponse(&deckv1.UpdateViewNotificationPreferenceResponse{
		Notification: notification,
	}), nil
}

func (service *Device) ResolveNotificationEvent(
	context.Context,
	*connect.Request[deckv1.ResolveNotificationEventRequest],
) (*connect.Response[deckv1.ResolveNotificationEventResponse], error) {
	return connect.NewResponse(&deckv1.ResolveNotificationEventResponse{
		Resolution:       deckv1.NotificationResolution_NOTIFICATION_RESOLUTION_GENERIC,
		NotificationText: "Deck view updated",
	}), nil
}

func (service *Device) deviceWrite(
	ctx context.Context,
	viewer contracts.Viewer,
	platform deckv1.DevicePlatform,
	displayName string,
	push *deckv1.PushRegistration,
	detailed bool,
	configurations []*deckv1.ViewShortcutConfiguration,
	widgetConfigurations []*deckv1.WidgetConfiguration,
) (database.DeviceWrite, error) {
	if platform < deckv1.DevicePlatform_DEVICE_PLATFORM_MACOS ||
		platform > deckv1.DevicePlatform_DEVICE_PLATFORM_ANDROID ||
		strings.TrimSpace(displayName) == "" || len(displayName) > 200 ||
		push == nil || push.Provider < deckv1.PushProvider_PUSH_PROVIDER_APPLE ||
		push.Provider > deckv1.PushProvider_PUSH_PROVIDER_FIREBASE ||
		strings.TrimSpace(push.OpaquePushToken) == "" ||
		len(configurations) > 20 {
		return database.DeviceWrite{}, rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
	bindingCounts := make(map[string]int)
	seenShortcuts := make(map[uuid.UUID]struct{})
	for _, configuration := range configurations {
		shortcutID, err := parseUUID(configuration.GetShortcutId())
		if err != nil {
			return database.DeviceWrite{}, err
		}
		if _, duplicate := seenShortcuts[shortcutID]; duplicate {
			return database.DeviceWrite{}, rpcerr.New(connect.CodeInvalidArgument,
				deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
		}
		seenShortcuts[shortcutID] = struct{}{}
	}
	type parsedShortcut struct {
		id      uuid.UUID
		viewID  uuid.UUID
		config  *deckv1.ViewShortcutConfiguration
		binding string
	}
	parsed := make([]parsedShortcut, 0, len(configurations))
	for _, configuration := range configurations {
		shortcutID, err := parseUUID(configuration.GetShortcutId())
		if err != nil {
			return database.DeviceWrite{}, err
		}
		viewID, err := parseUUID(configuration.GetViewId())
		if err != nil {
			return database.DeviceWrite{}, err
		}
		if err := service.authorizeReferencedView(ctx, viewer, viewID); err != nil {
			return database.DeviceWrite{}, err
		}
		binding, err := shortcut.CanonicalBinding(configuration.Binding)
		if err != nil {
			return database.DeviceWrite{}, rpcerr.New(connect.CodeInvalidArgument,
				deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
		}
		bindingCounts[binding]++
		parsed = append(parsed, parsedShortcut{
			id: shortcutID, viewID: viewID, config: configuration, binding: binding,
		})
	}
	shortcuts := make([]*deckv1.ViewShortcut, 0, len(parsed))
	for _, shortcut := range parsed {
		state := deckv1.ShortcutState_SHORTCUT_STATE_ACTIVE
		if bindingCounts[shortcut.binding] > 1 {
			state = deckv1.ShortcutState_SHORTCUT_STATE_CONFLICTED
		}
		shortcuts = append(shortcuts, &deckv1.ViewShortcut{
			ShortcutId: shortcut.config.ShortcutId,
			ViewId:     shortcut.config.ViewId,
			Binding:    proto.Clone(shortcut.config.Binding).(*deckv1.ShortcutBinding),
			State:      state,
		})
	}
	widgets := make([]*deckv1.WidgetState, 0, len(widgetConfigurations))
	seenWidgets := make(map[uuid.UUID]struct{})
	for _, configuration := range widgetConfigurations {
		widgetID, err := parseUUID(configuration.GetWidgetId())
		if err != nil {
			return database.DeviceWrite{}, err
		}
		if _, duplicate := seenWidgets[widgetID]; duplicate {
			return database.DeviceWrite{}, rpcerr.New(connect.CodeInvalidArgument,
				deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
		}
		seenWidgets[widgetID] = struct{}{}
		viewID, err := parseUUID(configuration.GetViewId())
		if err != nil {
			return database.DeviceWrite{}, err
		}
		if err := service.authorizeReferencedView(ctx, viewer, viewID); err != nil {
			return database.DeviceWrite{}, err
		}
		if configuration.Family < deckv1.WidgetFamily_WIDGET_FAMILY_APPLE_SMALL ||
			configuration.Family > deckv1.WidgetFamily_WIDGET_FAMILY_ANDROID_LIST ||
			(configuration.Privacy != deckv1.WidgetPrivacy_WIDGET_PRIVACY_COUNTS_ONLY &&
				configuration.Privacy != deckv1.WidgetPrivacy_WIDGET_PRIVACY_REPOSITORY_AND_TITLES) {
			return database.DeviceWrite{}, rpcerr.New(connect.CodeInvalidArgument,
				deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
		}
		widgets = append(widgets, &deckv1.WidgetState{
			WidgetId: configuration.WidgetId,
			ViewId:   configuration.ViewId,
			Family:   configuration.Family,
			Privacy:  configuration.Privacy,
			Snapshot: &deckv1.WidgetSnapshot{
				Freshness: deckv1.FreshnessState_FRESHNESS_STATE_NEVER_REFRESHED,
			},
		})
	}
	return database.DeviceWrite{
		Platform: platform, DisplayName: strings.TrimSpace(displayName),
		Push:                            proto.Clone(push).(*deckv1.PushRegistration),
		DetailedNotificationTextEnabled: detailed,
		Shortcuts:                       shortcuts, Widgets: widgets,
	}, nil
}

func (service *Device) authorizeRegistrationViews(
	ctx context.Context,
	viewer contracts.Viewer,
	registration *deckv1.DeviceRegistration,
) error {
	if registration == nil || registration.Device == nil {
		return rpcerr.New(connect.CodeInternal,
			deckv1.ErrorReason_ERROR_REASON_UNSPECIFIED)
	}
	seen := make(map[uuid.UUID]struct{},
		len(registration.Device.Shortcuts)+len(registration.Device.Widgets))
	authorize := func(value *deckv1.UuidV7) error {
		viewID, err := parseUUID(value)
		if err != nil {
			return err
		}
		if _, checked := seen[viewID]; checked {
			return nil
		}
		seen[viewID] = struct{}{}
		return service.authorizeReferencedView(ctx, viewer, viewID)
	}
	for _, shortcut := range registration.Device.Shortcuts {
		if err := authorize(shortcut.GetViewId()); err != nil {
			return err
		}
	}
	for _, widget := range registration.Device.Widgets {
		if err := authorize(widget.GetViewId()); err != nil {
			return err
		}
	}
	return nil
}

func (service *Device) authorizeReferencedView(
	ctx context.Context,
	viewer contracts.Viewer,
	viewID uuid.UUID,
) error {
	view, err := service.dependencies.Store.GetView(ctx, viewID)
	if err != nil {
		return mapDatabaseError(err)
	}
	_, err = authorizeOwner(viewer, view.Owner, false)
	if err != nil {
		return err
	}
	allowed, err := (&View{dependencies: service.dependencies}).
		canReadViewRepositories(ctx, viewer, view)
	if err != nil {
		return err
	}
	if !allowed {
		return rpcerr.New(connect.CodePermissionDenied,
			deckv1.ErrorReason_ERROR_REASON_GITHUB_PERMISSION_DENIED)
	}
	return nil
}
