// SPDX-License-Identifier: Apache-2.0
import { StringDecoder } from "node:string_decoder";

const lineLimit = 64 * 1024;
const drainTimeoutMs = 100;
const omitted = "[desktop diagnostic line omitted]";
const ansi = /\x1b\[[0-?]*[ -/]*[@-~]/g;

function argumentSpellings(arguments_) {
  const values = new Set();
  for (const argument of arguments_) {
    // Cargo and child tools can print either complete argv tokens or option
    // values, and can split a multiline value into several diagnostic lines.
    const raw = [argument, argument.replace(ansi, "")];
    if (argument.startsWith("-") && argument.includes("="))
      raw.push(argument.slice(argument.indexOf("=") + 1));
    for (const value of raw.flatMap((value) => [value, ...value.split(/[\r\n]+/)])) {
      if (!value) continue;
      values.add(value);
      values.add(JSON.stringify(value).slice(1, -1));
      values.add(value.replace(/[^a-zA-Z0-9_./:-]/g, "\\$&"));
      values.add(value.replaceAll("'", "'\\''"));
    }
  }
  // Replace once, longest first, so shorter arguments cannot expose part of a
  // longer one or recursively alter the replacement marker.
  return [...values].sort((left, right) => right.length - left.length);
}

export function desktopDiagnosticFilter(arguments_) {
  const values = argumentSpellings(arguments_).filter(
    (value) => value.length <= lineLimit,
  );
  const matcher = values.length
    ? new RegExp(
        values
          .map((value) => value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"))
          .join("|"),
        "g",
      )
    : undefined;
  return (line) => {
    // Color escape bytes must not split a private value and bypass matching.
    const plain = line.replace(ansi, "");
    if (
      /^\s*Running\s+[`'"]/.test(plain) ||
      /^\s*(?:error:\s*)?process didn't exit successfully:/.test(plain)
    )
      return omitted;
    const redacted = matcher
      ? plain.replace(matcher, () => "[private argument]")
      : plain;
    // Keep ordinary color diagnostics intact. If matching required a normalized
    // line, emit that redacted line so no split escape can restore private text.
    return redacted === plain ? line : redacted;
  };
}

function captureStream(stream, write, filter) {
  if (!stream) return { done: Promise.resolve(true), stop() {} };
  const decoder = new StringDecoder("utf8");
  let pending = "";
  let pendingBytes = 0;
  let dropping = false;
  let finished = false;
  let carriageReturn = false;
  let complete;
  const done = new Promise((resolve) => {
    complete = resolve;
  });
  const consume = (value) => {
    for (const character of value) {
      if (character === "\n" && carriageReturn) {
        carriageReturn = false;
        continue;
      }
      carriageReturn = character === "\r";
      if (character === "\n" || character === "\r") {
        if (!dropping && pending) write(filter(pending) + "\n");
        else if (!dropping && character === "\n") write("\n");
        pending = "";
        pendingBytes = 0;
        dropping = false;
      } else if (!dropping) {
        pending += character;
        pendingBytes += Buffer.byteLength(character);
        if (pendingBytes > lineLimit) {
          pending = "";
          dropping = true;
          write(omitted + "\n");
        }
      }
    }
  };
  const data = (chunk) => consume(decoder.write(chunk));
  const end = () => {
    if (finished) return;
    consume(decoder.end());
    if (!dropping && pending) write(filter(pending));
    finish(true);
  };
  const finish = (successful = false) => {
    if (finished) return;
    finished = true;
    pending = "";
    stream.off("data", data);
    stream.off("end", end);
    stream.off("error", failed);
    stream.off("close", closed);
    complete(successful);
  };
  const failed = () => {
    write(omitted + "\n");
    finish();
  };
  const closed = () => {
    if (!finished) failed();
  };
  stream.on("data", data);
  stream.once("end", end);
  stream.once("error", failed);
  stream.once("close", closed);
  return {
    done,
    stop() {
      if (finished) return;
      // A descendant can retain its inherited pipe after the original child
      // exits. Discard its partial line and close only our read end; never kill
      // that descendant or delay the original result indefinitely.
      finish();
      stream.destroy();
    },
  };
}

export function captureDesktopDiagnostics(
  arguments_,
  { stdout, stderr, incomplete },
) {
  const filter = desktopDiagnosticFilter(arguments_);
  return (child) => {
    const streams = [
      captureStream(child.stdout, stdout, filter),
      captureStream(child.stderr, stderr, filter),
    ];
    const drained = Promise.all(streams.map((stream) => stream.done));
    let completion;
    return () =>
      (completion ??= (async () => {
        let timer;
        const complete = await Promise.race([
          drained.then((results) => results.every(Boolean)),
          new Promise((resolve) => {
            timer = setTimeout(() => resolve(undefined), drainTimeoutMs);
          }),
        ]);
        clearTimeout(timer);
        if (!complete) {
          for (const stream of streams) stream.stop();
          incomplete(
            complete === undefined ? "output-drain-timeout" : "output-read-failed",
          );
        }
      })());
  };
}
