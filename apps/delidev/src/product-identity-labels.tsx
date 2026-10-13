// SPDX-License-Identifier: Apache-2.0
import {
  createContext,
  useCallback,
  useContext,
  useRef,
  type ReactNode,
} from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  EntityKind,
  decodeResourceDocument,
  supportsResourceSchema,
  type Resource,
} from "@delinoio/delidev-api-client";
import { copy, useLocale, type MessageKey } from "./localization";
import {
  ProductIdentities,
  PresentationKind,
  type IdentityLabel,
} from "./product-identity";
export {
  PresentationKind,
  productDiagnostic,
  productMetadata,
  productMetadataJSON,
  generatedIdentityFields,
  configurationPreviewPaths,
} from "./product-identity";

const kinds: Partial<Record<EntityKind, PresentationKind>> = {
  [EntityKind.PROJECT]: PresentationKind.Project,
  [EntityKind.REPOSITORY]: PresentationKind.Repository,
  [EntityKind.SESSION]: PresentationKind.Session,
  [EntityKind.AGENT]: PresentationKind.Agent,
  [EntityKind.ACCOUNT]: PresentationKind.Account,
  [EntityKind.PROVIDER]: PresentationKind.Provider,
  [EntityKind.MODEL]: PresentationKind.Model,
  [EntityKind.MACHINE]: PresentationKind.Runner,
  [EntityKind.DEVICE]: PresentationKind.Device,
  [EntityKind.SETTINGS]: PresentationKind.Settings,
  [EntityKind.INTEGRATION]: PresentationKind.Integration,
  [EntityKind.NETWORK_PROFILE]: PresentationKind.NetworkProfile,
  [EntityKind.SCHEDULE]: PresentationKind.Schedule,
  [EntityKind.TEMPLATE]: PresentationKind.Template,
  [EntityKind.TERMINAL]: PresentationKind.Terminal,
  [EntityKind.REVIEW]: PresentationKind.Review,
  [EntityKind.SNAPSHOT]: PresentationKind.Storage,
  [EntityKind.OCCURRENCE]: PresentationKind.Occurrence,
  [EntityKind.QUEUE]: PresentationKind.Input,
  [EntityKind.SUBAGENT]: PresentationKind.Agent,
  [EntityKind.JOB]: PresentationKind.Request,
  [EntityKind.SSH_SETUP]: PresentationKind.Request,
};
export function presentationKind(kind?: EntityKind): PresentationKind {
  return kind === undefined
    ? PresentationKind.Resource
    : (kinds[kind] ?? PresentationKind.Resource);
}
export function identityFallback(kind: PresentationKind): string {
  return copy(`product-identity.${kind}` as MessageKey);
}
function observeResource(registry: ProductIdentities, resource: Resource) {
  if (!resource.id || !supportsResourceSchema(resource)) return;
  const data = decodeResourceDocument(resource);
  const name =
    typeof data?.name === "string"
      ? data.name
      : typeof data?.alias === "string"
        ? data.alias
        : undefined;
  registry.observe(presentationKind(resource.kind), resource.id, name);
}
interface Owner {
  registry: ProductIdentities;
  observe: () => void;
}
const IdentityOwner = createContext<Owner | undefined>(undefined);
/** The existing connection boundary owns this memory; this provider starts no read. */
export function ProductIdentityProvider({ children }: { children: ReactNode }) {
  const client = useQueryClient();
  const registry = useRef(new ProductIdentities());
  const seen = useRef(new WeakSet<object>());
  const observe = () => {
    const visit = (value: unknown) => {
      if (!value || typeof value !== "object" || seen.current.has(value))
        return;
      seen.current.add(value);
      if ("documentJson" in value && "kind" in value && "id" in value) {
        observeResource(registry.current, value as Resource);
        return;
      }
      if (Array.isArray(value)) value.forEach(visit);
      else
        for (const [key, entry] of Object.entries(value)) {
          // Scan only authorized client observations; never inspect content bytes.
          if (
            ![
              "documentJson",
              "responseJson",
              "reportJson",
              "bundleJson",
              "native",
              "metadata",
              "content",
              "output",
              "text",
              "prompt",
              "frames",
              "messages",
            ].includes(key)
          )
            visit(entry);
        }
    };
    for (const query of client.getQueryCache().getAll())
      visit(query.state.data);
  };
  return (
    <IdentityOwner.Provider value={{ registry: registry.current, observe }}>
      {children}
    </IdentityOwner.Provider>
  );
}
export function useProductLabels(): IdentityLabel {
  const language = useLocale();
  const owner = useContext(IdentityOwner);
  const local = useRef(new ProductIdentities());
  const registry = owner?.registry ?? local.current;
  owner?.observe();
  return useCallback(
    (kind, id, name) =>
      registry.label(
        kind,
        id,
        identityFallback(kind),
        (value, number) =>
          copy("product-identity.numbered", { name: value, number }),
        name,
      ),
    [registry, language],
  );
}
export function ProductIdentity({
  kind,
  id,
  name,
}: {
  kind: PresentationKind;
  id?: string;
  name?: string;
}) {
  return useProductLabels()(kind, id, name);
}
export function productResourceName(
  label: IdentityLabel,
  resource?: Resource,
): string {
  const data = resource ? decodeResourceDocument(resource) : undefined;
  const name =
    typeof data?.name === "string"
      ? data.name
      : typeof data?.alias === "string"
        ? data.alias
        : undefined;
  return label(presentationKind(resource?.kind), resource?.id, name);
}
export function ResourceIdentity({ resource }: { resource?: Resource }) {
  return productResourceName(useProductLabels(), resource);
}
