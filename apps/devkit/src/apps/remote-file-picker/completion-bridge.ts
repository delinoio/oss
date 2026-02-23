import {
  CompletionCallback,
  CompletionDeliveryChannel,
  CompletionDeliveryResult,
  RemoteFilePickerCompletionResult,
} from "@/apps/remote-file-picker/contracts";
import { encodeJsonBase64Url } from "@/apps/remote-file-picker/encoding";

export const REMOTE_FILE_PICKER_POST_MESSAGE_TYPE =
  "devkit.remote-file-picker.completion";

interface CompletionBridgeWindow {
  opener?: {
    closed?: boolean;
    postMessage: (message: unknown, targetOrigin: string) => void;
  } | null;
  location: {
    assign: (url: string) => void;
  };
}

export interface CompletionBridgeDependencies {
  windowRef?: CompletionBridgeWindow;
}

function buildRedirectUrl(
  callback: CompletionCallback,
  result: RemoteFilePickerCompletionResult,
): string {
  const url = new URL(callback.returnUrl);
  url.searchParams.set("result", encodeJsonBase64Url(result));
  return url.toString();
}

export function deliverCompletionResult(
  result: RemoteFilePickerCompletionResult,
  callback: CompletionCallback,
  dependencies: CompletionBridgeDependencies = {},
): CompletionDeliveryResult {
  const windowRef = dependencies.windowRef ?? (window as CompletionBridgeWindow);
  let postMessageAttempted = false;

  if (callback.postMessageTargetOrigin && windowRef.opener && !windowRef.opener.closed) {
    try {
      windowRef.opener.postMessage(
        {
          type: REMOTE_FILE_PICKER_POST_MESSAGE_TYPE,
          payload: result,
        },
        callback.postMessageTargetOrigin,
      );
      postMessageAttempted = true;
    } catch {
      // Fallback to redirect below.
    }
  }

  try {
    const redirectUrl = buildRedirectUrl(callback, result);
    windowRef.location.assign(redirectUrl);

    return {
      delivered: true,
      channel: CompletionDeliveryChannel.Redirect,
      message: postMessageAttempted
        ? "Completion delivered via redirect callback after postMessage handoff."
        : "Completion delivered via redirect callback.",
    };
  } catch {
    return {
      delivered: false,
      channel: CompletionDeliveryChannel.Failed,
      message: "Completion delivery failed for both postMessage and redirect fallback.",
    };
  }
}
