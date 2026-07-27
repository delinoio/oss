package reliability

import "github.com/delinoio/oss/servers/internal/safelog"

func webhookHandler(eventType WebhookEventType) HandlerID {
	switch eventType {
	case WebhookOrderPaid:
		return HandlerPolarOrderPaid
	case WebhookSubscriptionCreated:
		return HandlerPolarSubscriptionCreated
	case WebhookSubscriptionUpdated:
		return HandlerPolarSubscriptionUpdated
	case WebhookSubscriptionActive:
		return HandlerPolarSubscriptionActive
	case WebhookSubscriptionUncanceled:
		return HandlerPolarSubscriptionUncanceled
	case WebhookSubscriptionPastDue:
		return HandlerPolarSubscriptionPastDue
	case WebhookSubscriptionCanceled:
		return HandlerPolarSubscriptionCanceled
	case WebhookSubscriptionRevoked:
		return HandlerPolarSubscriptionRevoked
	case WebhookRefundCreated:
		return HandlerPolarRefundCreated
	case WebhookRefundUpdated:
		return HandlerPolarRefundUpdated
	default:
		return ""
	}
}

func outboxHandler(integration Integration, operation IntegrationOperation) HandlerID {
	switch {
	case integration == IntegrationPolar && operation == OperationReportUsage:
		return HandlerPolarReportUsage
	case integration == IntegrationPolar && operation == OperationCancelSubscription:
		return HandlerPolarCancelSubscription
	case integration == IntegrationLogto && operation == OperationDeleteLogtoAccount:
		return HandlerLogtoDeleteAccount
	default:
		return ""
	}
}

func deletionHandler(jobType DeletionJobType) HandlerID {
	switch jobType {
	case DeletionAccount:
		return HandlerDeleteAccount
	case DeletionOrganization:
		return HandlerDeleteOrganization
	default:
		return ""
	}
}

func validHandlerID(id HandlerID) bool {
	switch id {
	case HandlerPolarOrderPaid,
		HandlerPolarSubscriptionCreated,
		HandlerPolarSubscriptionUpdated,
		HandlerPolarSubscriptionActive,
		HandlerPolarSubscriptionUncanceled,
		HandlerPolarSubscriptionPastDue,
		HandlerPolarSubscriptionCanceled,
		HandlerPolarSubscriptionRevoked,
		HandlerPolarRefundCreated,
		HandlerPolarRefundUpdated,
		HandlerPolarReportUsage,
		HandlerPolarCancelSubscription,
		HandlerLogtoDeleteAccount,
		HandlerDeleteAccount,
		HandlerDeleteOrganization:
		return true
	default:
		return false
	}
}

func validAggregate(
	aggregate AggregateType,
	integration Integration,
	operation IntegrationOperation,
) bool {
	switch {
	case integration == IntegrationPolar && operation == OperationReportUsage:
		return aggregate == AggregateUsageRecord
	case integration == IntegrationPolar && operation == OperationCancelSubscription:
		return aggregate == AggregateOrganization
	case integration == IntegrationLogto && operation == OperationDeleteLogtoAccount:
		return aggregate == AggregateAccount
	default:
		return false
	}
}

func validAuditType(eventType AuditEventType) bool {
	switch eventType {
	case AuditAuthorizationDecision,
		AuditOrganizationCreated,
		AuditOrganizationUpdated,
		AuditOrganizationDeleted,
		AuditRoleUpdated,
		AuditInvitationCreated,
		AuditInvitationAccepted,
		AuditInvitationRevoked,
		AuditTeamCreated,
		AuditTeamUpdated,
		AuditTeamDeleted,
		AuditBillingLimitUpdated,
		AuditCheckoutCreated,
		AuditBillingPortalSessionCreated,
		AuditSubscriptionUpdated,
		AuditRefundRecorded,
		AuditReservationCreated,
		AuditReservationCommitted,
		AuditReservationReleased,
		AuditReservationExpired,
		AuditSettlementRecorded,
		AuditBackgroundAuthorizationCreated,
		AuditBackgroundAuthorizationRevoked,
		AuditBackgroundAuthorizationAccessLost,
		AuditBackgroundAuthorizationResourceDeleted,
		AuditBackgroundAuthorizationOwnerDeleted,
		AuditAccountDeletionRequested,
		AuditOrganizationDeletionRequest,
		AuditWebhookReceived,
		AuditWebhookProcessed:
		return true
	default:
		return false
	}
}

func validDecision(decision safelog.Decision) bool {
	switch decision {
	case safelog.DecisionNone, safelog.DecisionAllow, safelog.DecisionDeny:
		return true
	default:
		return false
	}
}

func validResult(result safelog.Result) bool {
	switch result {
	case safelog.ResultSuccess, safelog.ResultFailure, safelog.ResultNoop:
		return true
	default:
		return false
	}
}
