// SPDX-License-Identifier: Apache-2.0
package server

// claudeServerUsageAdvertised is the single switch for System capability 109
// (CLAUDE_SERVER_USAGE_V1). The protocol, domain model and protected storage
// exist, but the capability stays unadvertised until the complete server-owned
// usage login, confirmation, refresh and cleanup lifecycle is implemented and
// activated as one feature. While it is false the six usage RPCs return
// Unimplemented and no usage state is written.
const claudeServerUsageAdvertised = false
