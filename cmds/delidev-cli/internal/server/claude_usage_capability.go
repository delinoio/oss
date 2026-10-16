// SPDX-License-Identifier: Apache-2.0
package server

// claudeServerUsageAdvertised is the single switch for System capability 109
// (CLAUDE_SERVER_USAGE_V1). It is on because the complete server-owned usage
// feature is implemented: the protocol, domain model and protected storage, the
// login, identity confirmation, cancel and disconnect lifecycle, the usage-read
// owner with supervised renewal and backoff, and the desktop presentation. The
// capability and the six usage RPCs are one unit: GetStatus advertises the
// capability exactly when the RPCs are served (claudeUsageActive), so an older
// peer never observes a partial feature.
const claudeServerUsageAdvertised = true
