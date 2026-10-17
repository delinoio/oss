// SPDX-License-Identifier: Apache-2.0
package postgres

// PeriodExpiredSQL returns the SQL condition for a tenant whose confirmed paid
// period has ended at or before the instant that now names. tenant and plan are
// the aliases of the tenants and plans rows and now is a timestamp expression.
//
// Only a calendar-month free period without a provider subscription is exempt:
// maintenance rolls it over. A subscription, including a recurring add-on on a
// free base plan, ends with its confirmed period until a renewal advances it.
func PeriodExpiredSQL(tenant, plan, now string) string {
	return "(" + tenant + ".period_end<=" + now + " AND (NOT " + plan + ".free OR " + tenant + ".external_subscription_id<>''))"
}
