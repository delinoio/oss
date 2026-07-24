import { create } from "@bufbuild/protobuf";
import {
  AccountStatus,
  GetAccountStateResponseSchema,
  OrganizationRole,
} from "@delinoio/delibase-connect";
import type { ReactNode } from "react";

import { AccountStateProvider } from "../components/ProtectedRoute";

export function TestAccountStateProvider({
  children,
  onboardingRequired = false,
  organizations = [
    {
      name: "Acme",
      organizationId: {
        value: "organization-id",
      },
      role: OrganizationRole.OWNER,
      slug: "acme",
    },
  ],
}: {
  children: ReactNode;
  onboardingRequired?: boolean;
  organizations?: Array<{
    name: string;
    organizationId: { value: string };
    role: OrganizationRole;
    slug: string;
  }>;
}) {
  return (
    <AccountStateProvider
      accountState={create(GetAccountStateResponseSchema, {
        account: {
          accountId: { value: "account-id" },
          displayName: "Deli Developer",
          status: AccountStatus.ACTIVE,
        },
        onboardingRequired,
        organizations,
      })}
      refreshAccountState={async () => undefined}
    >
      {children}
    </AccountStateProvider>
  );
}
