import type { ReactNode } from "react";
import { Navigate, Route, Routes } from "react-router-dom";

import { AppFrame } from "./components/AppFrame";
import { ProtectedRoute } from "./components/ProtectedRoute";
import { AccountPage } from "./pages/AccountPage";
import { CatalogDetailPage, CatalogPage } from "./pages/CatalogPages";
import { HomePage } from "./pages/HomePage";
import { InvitePage } from "./pages/InvitePage";
import { NotFoundPage } from "./pages/NotFoundPage";
import { OnboardingPage } from "./pages/OnboardingPage";
import {
  BillingPage,
  MembersPage,
  OrganizationAppsPage,
  OrganizationSettingsPage,
  TeamsPage,
  UsagePage,
} from "./pages/OrganizationPages";
import { OrganizationShell } from "./pages/OrganizationShell";

export function App({ callbackPage }: { callbackPage: ReactNode }) {
  return (
    <AppFrame>
      <Routes>
        <Route path="/" element={<HomePage />} />
        <Route path="/apps" element={<CatalogPage />} />
        <Route path="/apps/:appSlug" element={<CatalogDetailPage />} />
        <Route path="/auth/callback" element={callbackPage} />
        <Route
          path="/onboarding"
          element={
            <ProtectedRoute checkOnboarding={false}>
              <OnboardingPage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/invite/:token"
          element={
            <ProtectedRoute checkOnboarding={false}>
              <InvitePage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/o/:orgSlug"
          element={
            <ProtectedRoute>
              <OrganizationShell>
                <Navigate replace to="apps" />
              </OrganizationShell>
            </ProtectedRoute>
          }
        />
        <Route
          path="/o/:orgSlug/apps"
          element={
            <ProtectedRoute>
              <OrganizationShell>
                <OrganizationAppsPage />
              </OrganizationShell>
            </ProtectedRoute>
          }
        />
        <Route
          path="/o/:orgSlug/members"
          element={
            <ProtectedRoute>
              <OrganizationShell>
                <MembersPage />
              </OrganizationShell>
            </ProtectedRoute>
          }
        />
        <Route
          path="/o/:orgSlug/teams"
          element={
            <ProtectedRoute>
              <OrganizationShell>
                <TeamsPage />
              </OrganizationShell>
            </ProtectedRoute>
          }
        />
        <Route
          path="/o/:orgSlug/billing"
          element={
            <ProtectedRoute>
              <OrganizationShell>
                <BillingPage />
              </OrganizationShell>
            </ProtectedRoute>
          }
        />
        <Route
          path="/o/:orgSlug/usage"
          element={
            <ProtectedRoute>
              <OrganizationShell>
                <UsagePage />
              </OrganizationShell>
            </ProtectedRoute>
          }
        />
        <Route
          path="/o/:orgSlug/settings"
          element={
            <ProtectedRoute>
              <OrganizationShell>
                <OrganizationSettingsPage />
              </OrganizationShell>
            </ProtectedRoute>
          }
        />
        <Route
          path="/account"
          element={
            <ProtectedRoute>
              <AccountPage />
            </ProtectedRoute>
          }
        />
        <Route path="*" element={<NotFoundPage />} />
      </Routes>
    </AppFrame>
  );
}
