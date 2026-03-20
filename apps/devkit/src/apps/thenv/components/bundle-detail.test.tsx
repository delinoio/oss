import { create } from "@bufbuild/protobuf";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import {
  BundleFileSchema,
  BundleStatus,
  BundleVersionSummarySchema,
  FileType,
  ScopeSchema,
} from "@/gen/thenv/v1/thenv_pb";
import * as thenvQueries from "@/apps/thenv/hooks/use-thenv-queries";
import { BundleDetail } from "./bundle-detail";

vi.mock("@/apps/thenv/hooks/use-thenv-queries", () => ({
  usePullBundleVersion: vi.fn(),
  useActivateBundleVersionMutation: vi.fn(),
  useRotateBundleVersionMutation: vi.fn(),
}));

const mockedUsePullBundleVersion = vi.mocked(thenvQueries.usePullBundleVersion);
const mockedUseActivateBundleVersionMutation = vi.mocked(thenvQueries.useActivateBundleVersionMutation);
const mockedUseRotateBundleVersionMutation = vi.mocked(thenvQueries.useRotateBundleVersionMutation);

function renderWithProviders(ui: React.ReactElement) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
    },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      {ui}
    </QueryClientProvider>,
  );
}

describe("BundleDetail", () => {
  const scope = create(ScopeSchema, {
    workspaceId: "workspace-1",
    projectId: "project-1",
    environmentId: "development",
  });

  beforeEach(() => {
    mockedUseActivateBundleVersionMutation.mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
    } as never);
    mockedUseRotateBundleVersionMutation.mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
    } as never);
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it("requests the selected bundle version id", () => {
    mockedUsePullBundleVersion.mockReturnValue({
      data: {
        version: create(BundleVersionSummarySchema, {
          bundleVersionId: "ver-1",
          status: BundleStatus.ACTIVE,
          createdBy: "admin",
        }),
        files: [],
      },
      isLoading: false,
    } as never);

    renderWithProviders(<BundleDetail versionId="ver-1" scope={scope} />);

    expect(mockedUsePullBundleVersion).toHaveBeenCalledWith(scope, "ver-1");
  });

  it("hides secret content by default and reveals it only on explicit action", async () => {
    const user = userEvent.setup();
    const secretContent = "API_KEY=super-secret";

    mockedUsePullBundleVersion.mockReturnValue({
      data: {
        version: create(BundleVersionSummarySchema, {
          bundleVersionId: "ver-2",
          status: BundleStatus.ACTIVE,
          createdBy: "admin",
        }),
        files: [
          create(BundleFileSchema, {
            fileType: FileType.ENV,
            plaintext: new TextEncoder().encode(secretContent),
          }),
        ],
      },
      isLoading: false,
    } as never);

    renderWithProviders(<BundleDetail versionId="ver-2" scope={scope} />);

    expect(
      screen.getByText("Hidden by default. Use Reveal to view secret content."),
    ).toBeInTheDocument();
    expect(screen.queryByText(secretContent)).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Reveal" }));
    expect(screen.getByText(secretContent)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Copy" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Hide" }));
    expect(screen.queryByText(secretContent)).not.toBeInTheDocument();
  });
});
