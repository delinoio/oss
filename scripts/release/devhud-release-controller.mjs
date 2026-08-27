#!/usr/bin/env node

import { createHash } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

import { redact } from "./devhud-public-release.mjs";

const commands = new Set(["prepare", "promote-api", "promote-updater", "status", "rollback"]);

function sha256(bytes) { return createHash("sha256").update(bytes).digest("hex"); }

function exactRevision(value, name) {
  if (typeof value !== "string" || !/^[a-f0-9]{40}$/u.test(value)) throw new Error(`${name} must be an exact lowercase 40-hex commit`);
  return value;
}

export function controllerRequest(command, options, environment = process.env) {
  if (!commands.has(command)) throw new Error(`unsupported release-controller command: ${command}`);
  const identity = {
    schemaVersion: 1,
    project: "devhud",
    version: options.version,
    tag: `devhud@v${options.version}`,
    revision: exactRevision(options.revision, "release revision"),
    authorizationRevision: exactRevision(options["authorization-revision"], "workflow authorization revision"),
  };
  if (command === "prepare") {
    const updater = readFileSync(resolve(options.updater));
    return {
      path: "v1/devhud/releases/prepare",
      method: "POST",
      body: {
        ...identity,
        apiImage: options["api-image"],
        sweeperImage: options["sweeper-image"],
        updater: { sha256: sha256(updater), contentBase64: updater.toString("base64") },
      },
    };
  }
  const suffix = command === "status" ? "status" : command;
  if (command === "status") {
    return { path: `v1/devhud/releases/${encodeURIComponent(identity.tag)}/${suffix}?${new URLSearchParams(identity)}`, method: "GET", body: undefined };
  }
  return { path: `v1/devhud/releases/${encodeURIComponent(identity.tag)}/${suffix}`, method: "POST", body: identity };
}

export function validateControllerResponse(result, options, command = "request") {
  if (result.project !== "devhud" || result.version !== options.version || result.revision !== options.revision || result.authorizationRevision !== options["authorization-revision"] || result.ok !== true) {
    throw new Error(`release controller ${command} returned mismatched release state`);
  }
  return result;
}

export async function callController(command, options, environment = process.env, fetchImpl = fetch) {
  const request = controllerRequest(command, options, environment);
  const base = environment.DEVHUD_RELEASE_CONTROLLER_URL.endsWith("/") ? environment.DEVHUD_RELEASE_CONTROLLER_URL : `${environment.DEVHUD_RELEASE_CONTROLLER_URL}/`;
  const response = await fetchImpl(new URL(request.path, base), {
    method: request.method,
    redirect: "error",
    headers: { authorization: `Bearer ${environment.DEVHUD_RELEASE_CONTROLLER_TOKEN}`, accept: "application/json", ...(request.body ? { "content-type": "application/json" } : {}) },
    ...(request.body ? { body: JSON.stringify(request.body) } : {}),
  });
  if (!response.ok) throw new Error(`release controller ${command} failed with HTTP ${response.status}`);
  return validateControllerResponse(await response.json(), options, command);
}

function parse(arguments_) {
  const command = arguments_.shift();
  const options = {};
  for (let index = 0; index < arguments_.length; index += 2) {
    const name = arguments_[index];
    const value = arguments_[index + 1];
    if (!name?.startsWith("--") || !value || value.startsWith("--")) throw new Error(`invalid argument: ${name ?? "missing"}`);
    options[name.slice(2)] = value;
  }
  for (const name of ["version", "revision", "authorization-revision"]) if (!options[name]) throw new Error(`--${name} is required`);
  return { command, options };
}

export async function main(arguments_ = process.argv.slice(2), environment = process.env) {
  const { command, options } = parse([...arguments_]);
  const result = await callController(command, options, environment);
  const serialized = `${JSON.stringify(redact(result, environment), null, 2)}\n`;
  if (options.output) writeFileSync(resolve(options.output), serialized); else process.stdout.write(serialized);
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  try { await main(); } catch (error) {
    process.stderr.write(`[devhud.controller] ${redact(String(error.message))}\n`);
    process.exit(1);
  }
}
