// SPDX-License-Identifier: Apache-2.0
package worker

// grokManagedExecutionEnabled is the Worker half of the switch for Worker
// capability 21 (MANAGED_GROK_SUBSCRIPTIONS_V1). The Worker reports the
// capability only when this is true and the server also echoed it; the server
// echoes it only behind its own default-off switch. It stays false until the
// opt-in real-account run (see the Grok subscription contract) has verified
// native cached_token execution on the current stable release. It is a variable
// only so tests can exercise the activated paths; production code never assigns
// it.
var grokManagedExecutionEnabled = false
