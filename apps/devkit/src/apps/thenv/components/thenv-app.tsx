"use client";

import { useCallback, useEffect, useState } from "react";

import { BundleStatus, type Scope } from "@/gen/thenv/v1/thenv_pb";

import { ThenvTransportProvider } from "@/apps/thenv/hooks/use-thenv-transport";
import { useListBundleVersions } from "@/apps/thenv/hooks/use-thenv-queries";
import { ScopeSelector } from "./scope-selector";
import { BundleList } from "./bundle-list";
import { BundleDetail } from "./bundle-detail";
import { PolicyEditor } from "./policy-editor";
import { AuditViewer } from "./audit-viewer";

type ThenvTab = "bundles" | "policy" | "audit";

function ThenvContent() {
  const [scope, setScope] = useState<Scope | undefined>(undefined);
  const [activeTab, setActiveTab] = useState<ThenvTab>("bundles");
  const [selectedVersionId, setSelectedVersionId] = useState<string | undefined>(undefined);

  const { data: versionsData, isLoading: versionsLoading } = useListBundleVersions(scope);
  const versions = versionsData?.versions ?? [];

  const handleScopeChange = useCallback((nextScope: Scope) => {
    setScope(nextScope);
    setSelectedVersionId(undefined);
  }, []);

  useEffect(() => {
    if (!scope) {
      return;
    }
    if (versions.length === 0) {
      if (selectedVersionId !== undefined) {
        setSelectedVersionId(undefined);
      }
      return;
    }

    const selectedExists = selectedVersionId
      ? versions.some((version) => version.bundleVersionId === selectedVersionId)
      : false;
    if (selectedExists) {
      return;
    }

    const activeVersion = versions.find((version) => version.status === BundleStatus.ACTIVE);
    setSelectedVersionId(activeVersion?.bundleVersionId ?? versions[0]?.bundleVersionId);
  }, [scope, versions, selectedVersionId]);

  const tabs: { id: ThenvTab; label: string }[] = [
    { id: "bundles", label: "Bundles" },
    { id: "policy", label: "Policy" },
    { id: "audit", label: "Audit" },
  ];

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "1.5rem" }}>
      <ScopeSelector onScopeChange={handleScopeChange} />

      {scope && (
        <>
          <div style={{ display: "flex", gap: "0.5rem", borderBottom: "1px solid #e2e8f0" }}>
            {tabs.map((tab) => (
              <button
                key={tab.id}
                onClick={() => setActiveTab(tab.id)}
                style={{
                  padding: "0.5rem 1rem",
                  border: "none",
                  borderBottom: activeTab === tab.id ? "2px solid #0c5fca" : "2px solid transparent",
                  backgroundColor: "transparent",
                  color: activeTab === tab.id ? "#0c5fca" : "#64748b",
                  cursor: "pointer",
                  fontSize: "0.875rem",
                  fontWeight: activeTab === tab.id ? 600 : 400,
                }}
              >
                {tab.label}
              </button>
            ))}
          </div>

          {activeTab === "bundles" && (
            <div style={{ display: "flex", flexDirection: "column", gap: "1.5rem" }}>
              <BundleList
                versions={versionsData?.versions ?? []}
                isLoading={versionsLoading}
                onSelect={setSelectedVersionId}
                selectedVersionId={selectedVersionId}
              />
              {selectedVersionId && scope && (
                <BundleDetail versionId={selectedVersionId} scope={scope} />
              )}
            </div>
          )}

          {activeTab === "policy" && <PolicyEditor scope={scope} />}

          {activeTab === "audit" && <AuditViewer scope={scope} />}
        </>
      )}

      {!scope && (
        <p style={{ color: "#64748b", fontSize: "0.875rem" }}>
          Select a scope above to view and manage your environment bundles.
        </p>
      )}
    </div>
  );
}

export function ThenvApp() {
  return (
    <ThenvTransportProvider>
      <ThenvContent />
    </ThenvTransportProvider>
  );
}
