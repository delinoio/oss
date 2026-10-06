// SPDX-License-Identifier: Apache-2.0
/** A deliberate account-usage navigation, consumed once in connection memory. */
export interface UsageEntry {
  key: string;
  accountId: string;
  fromUnixMs: bigint;
  untilUnixMs: bigint;
}
