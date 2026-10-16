// SPDX-License-Identifier: Apache-2.0
package server

// grokManagedExecutionAdvertised is the single switch for System capability 40
// (GROK_SUBSCRIPTION_EXECUTION_V1) and Worker capability 21
// (MANAGED_GROK_SUBSCRIPTIONS_V1). It stays false until the opt-in real-account
// run (see the Grok subscription contract, "Real-account acceptance runbook")
// has verified native subscription execution with cached_token on the current
// stable release. While it is false, GetStatus never lists System 40,
// AttachWorker never lists Worker 21 as supported and rejects a Worker report of
// 21 as an unknown capability, so no Worker can hold the capability that every
// Grok execute Take, dispatch and execution-authority path requires. It is a
// variable only so tests can exercise the activated paths; production code never
// assigns it.
var grokManagedExecutionAdvertised = false
