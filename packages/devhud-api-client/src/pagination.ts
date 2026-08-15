import { create } from "@bufbuild/protobuf";

import {
  PageRequestSchema,
  type PageRequest,
} from "./gen/devhud/v1/common_pb.js";
import { assertWellFormedUnicode } from "./unicode.js";

export const DEFAULT_PAGE_SIZE = 50;
export const MAX_PAGE_SIZE = 100;
export const MAX_PAGE_TOKEN_BYTES = 2_048;

const textEncoder = new TextEncoder();

export function createPageRequest(
  pageSize = DEFAULT_PAGE_SIZE,
  pageToken = "",
): PageRequest {
  if (!Number.isInteger(pageSize) || pageSize < 0 || pageSize > MAX_PAGE_SIZE) {
    throw new RangeError(`pageSize must be an integer from 0 through ${MAX_PAGE_SIZE}`);
  }
  assertWellFormedUnicode(pageToken, "pageToken");
  if (textEncoder.encode(pageToken).byteLength > MAX_PAGE_TOKEN_BYTES) {
    throw new RangeError(`pageToken must not exceed ${MAX_PAGE_TOKEN_BYTES} UTF-8 bytes`);
  }

  return create(PageRequestSchema, {
    pageSize: pageSize === 0 ? DEFAULT_PAGE_SIZE : pageSize,
    pageToken,
  });
}
