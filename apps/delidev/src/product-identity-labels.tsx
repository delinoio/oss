// SPDX-License-Identifier: Apache-2.0
import {
  createContext,
  useCallback,
  useContext,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useQueryClient, type QueryCache } from "@tanstack/react-query";
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
/**
 * Returns the exact optional document name, else alias, of a resource. Retain
 * this raw value (never a presentation string) when a later render must derive
 * the localized and numbered label again.
 */
export function productResourceObservedName(
  resource?: Resource,
): string | undefined {
  const data = resource ? decodeResourceDocument(resource) : undefined;
  return typeof data?.name === "string"
    ? data.name
    : typeof data?.alias === "string"
      ? data.alias
      : undefined;
}
function observeResource(registry: ProductIdentities, resource: Resource) {
  if (!resource.id || !supportsResourceSchema(resource)) return;
  registry.observe(
    presentationKind(resource.kind),
    resource.id,
    productResourceObservedName(resource),
  );
}
/** Keys whose subtrees carry content or protected evidence and are never observed. */
const excludedObservationKeys: ReadonlySet<string> = new Set([
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
]);
/**
 * Visits authorized client observations once per object identity and records
 * resource names in the registry. Never inspects content bytes.
 */
class CacheObserver {
  private readonly seen = new WeakSet<object>();
  constructor(readonly registry: ProductIdentities) {}
  visit = (value: unknown) => {
    if (!value || typeof value !== "object" || this.seen.has(value)) return;
    this.seen.add(value);
    if ("documentJson" in value && "kind" in value && "id" in value) {
      observeResource(this.registry, value as Resource);
      return;
    }
    if (Array.isArray(value)) value.forEach(this.visit);
    else
      for (const [key, entry] of Object.entries(value)) {
        if (!excludedObservationKeys.has(key)) this.visit(entry);
      }
  };
  /** Full traversal; only the provider setup paths call this, never a consumer render. */
  scan(cache: QueryCache) {
    for (const query of cache.getAll()) this.visit(query.state.data);
  }
}
interface Owner {
  registry: ProductIdentities;
}
const IdentityOwner = createContext<Owner | undefined>(undefined);
/**
 * The existing connection boundary owns this memory; this provider starts no
 * read. It scans the cache once when created, then observes only the data of
 * added or updated queries at the cache-update boundary. Consumers never scan.
 */
export function ProductIdentityProvider({ children }: { children: ReactNode }) {
  const client = useQueryClient();
  const [observer] = useState(() => {
    const created = new CacheObserver(new ProductIdentities());
    // Render-safe: the registry is private until this provider commits.
    created.scan(client.getQueryCache());
    return created;
  });
  const owner = useMemo(
    () => ({ registry: observer.registry }),
    [observer],
  );
  useLayoutEffect(() => {
    const cache = client.getQueryCache();
    const unsubscribe = cache.subscribe((event) => {
      if (event.type === "added" || event.type === "updated")
        observer.visit(event.query.state.data);
    });
    // Cover events between the render-time scan (or a client change) and subscription.
    observer.scan(cache);
    return unsubscribe;
  }, [client, observer]);
  return <IdentityOwner.Provider value={owner}>{children}</IdentityOwner.Provider>;
}
export function useProductLabels(): IdentityLabel {
  const language = useLocale();
  const owner = useContext(IdentityOwner);
  const local = useRef(new ProductIdentities());
  const registry = owner?.registry ?? local.current;
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
  return label(
    presentationKind(resource?.kind),
    resource?.id,
    productResourceObservedName(resource),
  );
}
export function ResourceIdentity({ resource }: { resource?: Resource }) {
  return productResourceName(useProductLabels(), resource);
}
