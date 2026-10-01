// SPDX-License-Identifier: Apache-2.0
import { createFileRegistry, createRegistry, ScalarType } from "@bufbuild/protobuf";
import { expect, it } from "vitest";
import { EntityKind, NetworkQuery, NetworkService, SystemCapability } from "../src/index.js";
import { NetworkService as LegacyNetworkService, file_delidev_v1_delidev } from "../src/gen/delidev/v1/delidev_pb.js";
import { saveNetworkProfile } from "../src/gen/delidev/v1/delidev-NetworkService_connectquery.js";

it("includes independent network operations in generated exports and reflection", () => {
  expect(LegacyNetworkService).toBe(NetworkService);
  expect(saveNetworkProfile).toBe(NetworkQuery.saveNetworkProfile);
  expect(NetworkQuery.saveNetworkProfile).toBe(NetworkService.method.saveNetworkProfile);
  expect(EntityKind.NETWORK_PROFILE).toBe(28);
  expect(EntityKind.NETWORK_ROUTE).toBe(29);
  expect(SystemCapability.SERVER_OUTBOUND_PROXY_V1).toBe(6);
  const registry = createRegistry(file_delidev_v1_delidev);
  expect(registry.getService("delidev.v1.NetworkService")).toBe(NetworkService);
  const restored = createFileRegistry(file_delidev_v1_delidev.proto, name => file_delidev_v1_delidev.dependencies.find(file => file.proto.name === name));
  const service = restored.getService("delidev.v1.NetworkService");
  expect(service?.method.saveNetworkProfile.input.field.credentialJson.number).toBe(4);
  expect(service?.method.saveNetworkProfile.output.fields.some(field => field.name.includes("credential"))).toBe(false);
  expect(service?.method.selectNetworkProfile.input.field.profileRevision.scalar).toBe(ScalarType.UINT64);
});
