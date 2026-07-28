import { create } from "@bufbuild/protobuf";
import type { Client } from "@connectrpc/connect";

import {
  DeckDeviceService,
  DeckIntegrationService,
  DeckLimit,
  DeckViewService,
  OwnerScope,
  QueryBuilderSchema,
  ShortcutBindingSchema,
  ShortcutKey,
  ShortcutModifier,
  ViewShortcutConfigurationSchema,
  ViewKind,
  ViewQuerySchema,
} from "./index.js";

export const canonicalServiceDescriptors = [
  DeckViewService,
  DeckIntegrationService,
  DeckDeviceService,
] as const;

export const rawQueryWithBuilder = create(ViewQuerySchema, {
  rawQuery: "is:pr is:open assignee:@me future:opaque",
  builder: create(QueryBuilderSchema, {
    unrecognizedRawClauses: ["future:opaque"],
  }),
});

export const contractLimits = {
  personalViews: DeckLimit.MAX_PERSONAL_VIEWS,
  organizationViews: DeckLimit.MAX_ORGANIZATION_VIEWS,
  pullRequestResults: DeckLimit.MAX_PULL_REQUEST_RESULTS,
} as const;

export const initialViewKind = ViewKind.GITHUB_PULL_REQUESTS;
export const personalScope = OwnerScope.PERSONAL;
export const shortcutConfiguration = create(
  ViewShortcutConfigurationSchema,
  {
    binding: create(ShortcutBindingSchema, {
      modifiers: [ShortcutModifier.CONTROL, ShortcutModifier.SHIFT],
      key: ShortcutKey.K,
    }),
  },
);
export type DeckViewClient = Client<typeof DeckViewService>;
