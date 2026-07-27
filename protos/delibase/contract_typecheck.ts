import { create } from "@bufbuild/protobuf";
import type { Client } from "@connectrpc/connect";

import {
  AccountService,
  AuthorizedUsageContextSchema,
  BackgroundUsagePeriod,
  BackgroundUsagePurpose,
  BillingService,
  CatalogService,
  OrganizationService,
  TeamService,
  UsageService,
  UsdMicrosSchema,
  UsageUnitsSchema,
} from "./index.js";

export const canonicalServiceDescriptors = [
  AccountService,
  OrganizationService,
  TeamService,
  CatalogService,
  BillingService,
  UsageService,
] as const;

export const tenUsd = create(UsdMicrosSchema, { value: 10_000_000n });
export const oneUsageUnit = create(UsageUnitsSchema, { value: 1n });
export const realQAStorageDay = create(AuthorizedUsageContextSchema, {
  purpose: BackgroundUsagePurpose.REALQA_STORAGE,
  period: BackgroundUsagePeriod.UTC_DAY,
});

export type UsageClient = Client<typeof UsageService>;
