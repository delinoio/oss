import { render, screen } from "@testing-library/react";
import { beforeEach } from "vitest";

import {
  PickerSource,
  SignedUrlProvider,
  UploadHttpMethod,
} from "@/apps/remote-file-picker/contracts";
import { encodeJsonBase64Url } from "@/apps/remote-file-picker/encoding";

import RemoteFilePickerPage from "./page";

function buildValidRequestParam(): string {
  return encodeJsonBase64Url({
    requestId: "req-page-test",
    uploadTarget: {
      provider: SignedUrlProvider.AwsS3,
      method: UploadHttpMethod.Put,
      url: "https://bucket.s3.amazonaws.com/path/file.png",
      expiresAt: "2099-01-01T00:00:00.000Z",
    },
    allowedSources: [PickerSource.LocalFile, PickerSource.MobileCamera],
    callback: {
      returnUrl: "https://host.example/upload/completion",
      postMessageTargetOrigin: "https://host.example",
    },
  });
}

describe("RemoteFilePickerPage", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", "/apps/remote-file-picker");
  });

  it("renders phase 1 upload UI instead of placeholder content", async () => {
    const request = buildValidRequestParam();
    window.history.replaceState({}, "", `/apps/remote-file-picker?request=${request}`);

    render(<RemoteFilePickerPage />);

    expect(
      await screen.findByRole("heading", { name: "Remote File Picker Upload" }),
    ).toBeInTheDocument();
    expect(screen.queryByText("Remote File Picker Placeholder")).not.toBeInTheDocument();
  });

  it("shows a clear error when request payload is missing", async () => {
    render(<RemoteFilePickerPage />);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Missing request payload. Add the request query parameter.",
    );
  });
});
