"use strict";

const { execFileSync } = require("node:child_process");
const { appendFileSync } = require("node:fs");
const path = require("node:path");
const { resolveLauncher } = require("../clibox.cjs");

const { version, launcher } = resolveLauncher();
const actual = execFileSync(process.execPath, [launcher, "--version"], { encoding: "utf8" }).trim();
if (actual !== `clibox ${version}`) throw new Error("The installed clibox executable does not match the repository pin.");
if (process.env.GITHUB_PATH) appendFileSync(process.env.GITHUB_PATH, `${path.resolve(__dirname, "../../node_modules/.bin")}\n`);
console.log(JSON.stringify({ event: "repository_clibox_ready", version, launcher, platform: process.platform, architecture: process.arch }));
