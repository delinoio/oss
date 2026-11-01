// SPDX-License-Identifier: Apache-2.0

// Package ownership decides which native file owners identify the current user
// for current-user-private storage. Callers still validate the access-control
// list; an accepted owner alone does not make a path private.
package ownership
