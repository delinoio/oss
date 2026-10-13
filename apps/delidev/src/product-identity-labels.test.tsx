// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, renderHook, screen } from "@testing-library/react";
import { type ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import {
  EntityKind,
  ResourceSchema,
  type Resource,
} from "@delinoio/delidev-api-client";
import {
  PresentationKind,
  ProductIdentityProvider,
  useProductLabels,
} from "./product-identity-labels";

const first = "0195c9c0-7b13-7000-8000-000000000001";
const second = "0195c9c0-7b13-7000-8000-000000000002";
const third = "0195c9c0-7b13-7000-8000-000000000003";
const project = (id: string, name?: string): Resource =>
  create(ResourceSchema, {
    id,
    kind: EntityKind.PROJECT,
    schemaVersion: 1,
    documentJson: new TextEncoder().encode(
      JSON.stringify(name === undefined ? {} : { name }),
    ),
  });
function harness() {
  const client = new QueryClient();
  const getAll = vi.spyOn(client.getQueryCache(), "getAll");
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>
      <ProductIdentityProvider>{children}</ProductIdentityProvider>
    </QueryClientProvider>
  );
  return { client, getAll, wrapper };
}
function Row({ id }: { id: string }) {
  const label = useProductLabels();
  return <li data-testid={id}>{label(PresentationKind.Project, id)}</li>;
}

describe("ProductIdentityProvider cache observation", () => {
  it("shows names of already-cached resources on the first render", () => {
    const { client, wrapper } = harness();
    client.setQueryData(["projects"], { projects: [project(first, "Alpha")] });
    render(<Row id={first} />, { wrapper });
    expect(screen.getByTestId(first).textContent).toBe("Alpha");
  });

  it("does not traverse the query cache per consumer render", () => {
    const { client, getAll, wrapper } = harness();
    client.setQueryData(["projects"], { projects: [project(first, "Alpha")] });
    const ids = Array.from({ length: 60 }, (_, index) => `row-${index}`);
    const view = render(
      <ul>
        {ids.map((id) => (
          <Row key={id} id={id} />
        ))}
      </ul>,
      { wrapper },
    );
    const afterMount = getAll.mock.calls.length;
    // One render-safe creation scan and one subscription catch-up scan.
    expect(afterMount).toBeLessThanOrEqual(2);
    view.rerender(
      <ul>
        {ids.map((id) => (
          <Row key={id} id={id} />
        ))}
      </ul>,
    );
    act(() => {
      client.setQueryData(["projects"], {
        projects: [project(first, "Alpha"), project(second, "Beta")],
      });
    });
    expect(getAll.mock.calls.length).toBe(afterMount);
  });

  it("observes updated cache data at the update boundary without a consumer render", () => {
    const { client, wrapper } = harness();
    const { result } = renderHook(() => useProductLabels(), { wrapper });
    expect(result.current(PresentationKind.Project, first)).toMatch(/1$/);
    act(() => {
      client.setQueryData(["projects"], {
        projects: [project(first, "Alpha")],
      });
    });
    // No re-render happened; the retained registry already has the new name.
    expect(result.current(PresentationKind.Project, first)).toBe("Alpha");
  });

  it("refreshes a rendered label when its query data changes", () => {
    const { client, wrapper } = harness();
    client.setQueryData(["projects"], { projects: [project(first)] });
    function Subscribed() {
      const data = client.getQueryData<{ projects: Resource[] }>(["projects"]);
      const label = useProductLabels();
      return (
        <p data-testid="label">
          {label(PresentationKind.Project, first)}:{data?.projects.length}
        </p>
      );
    }
    const view = render(<Subscribed />, { wrapper });
    expect(screen.getByTestId("label").textContent).toMatch(/1:1$/);
    act(() => {
      client.setQueryData(["projects"], {
        projects: [project(first, "Alpha")],
      });
    });
    view.rerender(<Subscribed />);
    expect(screen.getByTestId("label").textContent).toBe("Alpha:1");
  });

  it("keeps numbers stable across cache changes and pagination", () => {
    const { client, wrapper } = harness();
    client.setQueryData(["projects", 1], {
      projects: [project(first), project(second)],
    });
    const { result } = renderHook(() => useProductLabels(), { wrapper });
    const label = result.current;
    const firstLabel = label(PresentationKind.Project, first);
    const secondLabel = label(PresentationKind.Project, second);
    expect(firstLabel).toMatch(/1$/);
    expect(secondLabel).toMatch(/2$/);
    act(() => {
      client.setQueryData(["projects", 2], { projects: [project(third)] });
      client.setQueryData(["projects", 1], { projects: [project(second)] });
    });
    expect(label(PresentationKind.Project, third)).toMatch(/3$/);
    expect(label(PresentationKind.Project, first)).toBe(firstLabel);
    expect(label(PresentationKind.Project, second)).toBe(secondLabel);
  });

  it("never observes excluded content keys", () => {
    const { client, wrapper } = harness();
    const hidden = project(first, "Hidden");
    const excluded = [
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
    ];
    const data = Object.fromEntries(excluded.map((key) => [key, [hidden]]));
    client.setQueryData(["before"], data);
    const { result } = renderHook(() => useProductLabels(), { wrapper });
    act(() => {
      client.setQueryData(["after"], { nested: data });
    });
    expect(result.current(PresentationKind.Project, first)).toMatch(/1$/);
    act(() => {
      client.setQueryData(["visible"], { nested: { projects: [hidden] } });
    });
    expect(result.current(PresentationKind.Project, first)).toBe("Hidden");
  });
});
