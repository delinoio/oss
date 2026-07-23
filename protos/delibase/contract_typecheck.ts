import { create } from "@bufbuild/protobuf";
import type { Client } from "@connectrpc/connect";

import {
  AccountService,
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

export type UsageClient = Client<typeof UsageService>;
