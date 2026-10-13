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
    for (const value of raw.flatMap((value) => [
      value,
      ...value.split(/[\r\n]+/),
    ])) {
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

// Final state of one captured stream. Only `Complete` means every accepted
// byte was settled by its destination.
export const CaptureOutcome = Object.freeze({
  Complete: "complete",
  ReadFailed: "read-failed",
  WriteFailed: "write-failed",
  TimedOut: "timed-out",
});

// Closed, safe classifications passed to the `incomplete` callback.
export const IncompleteCode = Object.freeze({
  DrainTimeout: "output-drain-timeout",
  ReadFailed: "output-read-failed",
  WriteFailed: "output-write-failed",
});

function captureStream(stream, destination, filter) {
  if (!stream)
    return {
      done: Promise.resolve(CaptureOutcome.Complete),
      writeFailed: () => false,
      stop() {},
    };
  const writable = typeof destination === "function" ? undefined : destination;
  const decoder = new StringDecoder("utf8");
  let pending = "";
  let pendingBytes = 0;
  let dropping = false;
  let finished = false;
  let carriageReturn = false;
  let waitingForDrain = false;
  let queued = "";
  let inputEnded = false;
  let tailWritten = false;
  // Accepted destination writes whose callbacks have not run yet.
  let pendingWrites = 0;
  let sinkFailed = false;
  // The child pipe failed; the omission marker is the last accepted write.
  let readFailed = false;
  // The destination is an observed resource: own its errors from before the
  // first write until no outstanding write can still report one.
  let sinkErrorOwned = false;
  let complete;
  const done = new Promise((resolve) => {
    complete = resolve;
  });
  const disownSink = () => {
    if (!sinkErrorOwned) return;
    sinkErrorOwned = false;
    writable.off("error", sinkError);
  };
  const releaseSink = () => {
    if (finished && pendingWrites === 0 && !sinkFailed) disownSink();
  };
  const sinkError = () => {
    if (sinkFailed) return;
    sinkFailed = true;
    waitingForDrain = false;
    finish(CaptureOutcome.WriteFailed);
    // A failed destination may still emit its error after the write callback,
    // so keep the listener until it closes. Keep discarding child output so a
    // failed sink cannot block the child on a full pipe.
    if (writable.closed) disownSink();
    else writable.once("close", disownSink);
    stream.resume();
  };
  const settled = (error) => {
    pendingWrites--;
    if (error) sinkError();
    else if (finished) releaseSink();
    else if (readFailed) finishAfterReadFailure();
    else finishInput();
  };
  const send = (value) => {
    if (sinkFailed) return true;
    if (!writable) {
      destination(value);
      return true;
    }
    pendingWrites++;
    try {
      return writable.write(value, settled);
    } catch {
      pendingWrites--;
      sinkError();
      return true;
    }
  };
  const resume = () => {
    if (finished || readFailed || !waitingForDrain) return;
    waitingForDrain = false;
    const remainder = queued;
    queued = "";
    if (remainder) consume(remainder);
    if (finished || waitingForDrain) return;
    if (inputEnded) finishInput();
    else stream.resume();
  };
  const writeOutput = (value) => {
    if (send(value) !== false || !writable || sinkFailed) return false;
    waitingForDrain = true;
    stream.pause();
    writable.once("drain", resume);
    return true;
  };
  const consume = (value) => {
    for (let index = 0; index < value.length; index++) {
      const character = value[index];
      if (character === "\n" && carriageReturn) {
        carriageReturn = false;
        continue;
      }
      carriageReturn = character === "\r";
      if (character === "\n" || character === "\r") {
        let paused = false;
        if (!dropping && pending) paused = writeOutput(filter(pending) + "\n");
        else if (!dropping && character === "\n") paused = writeOutput("\n");
        pending = "";
        pendingBytes = 0;
        dropping = false;
        if (paused) {
          queued = value.slice(index + 1);
          return;
        }
      } else if (!dropping) {
        pending += character;
        pendingBytes += Buffer.byteLength(character);
        if (pendingBytes > lineLimit) {
          pending = "";
          dropping = true;
          if (writeOutput(omitted + "\n")) {
            queued = value.slice(index + 1);
            return;
          }
        }
      }
    }
  };
  const data = (chunk) => consume(decoder.write(chunk));
  const finishInput = () => {
    if (finished || readFailed || !inputEnded || waitingForDrain) return;
    if (!tailWritten) {
      tailWritten = true;
      // The unterminated tail is tracked like any other accepted write.
      if (!dropping && pending) send(filter(pending));
      if (finished) return;
    }
    // Complete only after every accepted write callback has settled.
    if (pendingWrites === 0) finish(CaptureOutcome.Complete);
  };
  const end = () => {
    if (finished || inputEnded) return;
    inputEnded = true;
    const remainder = decoder.end();
    if (waitingForDrain) queued += remainder;
    else consume(remainder);
    finishInput();
  };
  const finish = (outcome) => {
    if (finished) return;
    finished = true;
    pending = "";
    stream.off("data", data);
    stream.off("end", end);
    stream.off("error", failed);
    stream.off("close", closed);
    writable?.off("drain", resume);
    queued = "";
    releaseSink();
    complete(outcome);
  };
  const ignoreSourceError = () => {};
  // A read failure completes only after the omission marker and every earlier
  // accepted write has settled. A slow or failing destination therefore keeps
  // the capture pending until its callback runs or the drain bound expires.
  const finishAfterReadFailure = () => {
    if (!finished && pendingWrites === 0) finish(CaptureOutcome.ReadFailed);
  };
  const failed = () => {
    if (finished || readFailed) return;
    readFailed = true;
    pending = "";
    queued = "";
    waitingForDrain = false;
    stream.off("data", data);
    stream.off("end", end);
    stream.off("close", closed);
    writable?.off("drain", resume);
    send(omitted + "\n");
    finishAfterReadFailure();
  };
  const closed = () => {
    if (!finished && !inputEnded) failed();
  };
  if (writable) {
    writable.on("error", sinkError);
    sinkErrorOwned = true;
  }
  // The launcher owns this child pipe for its whole lifetime. After the sink
  // fails the pipe is still resumed and discarded, so a later read error must
  // not become an uncaught exception that replaces the child's result.
  stream.on("error", ignoreSourceError);
  stream.on("data", data);
  stream.once("end", end);
  stream.once("error", failed);
  stream.once("close", closed);
  return {
    done,
    writeFailed: () => sinkFailed,
    stop() {
      if (finished) return;
      // A descendant can retain its inherited pipe after the original child
      // exits. Discard its partial line and close only our read end; never kill
      // that descendant or delay the original result indefinitely.
      finish(CaptureOutcome.TimedOut);
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
        const outcomes = await Promise.race([
          drained,
          new Promise((resolve) => {
            timer = setTimeout(() => resolve(undefined), drainTimeoutMs);
          }),
        ]);
        clearTimeout(timer);
        if (outcomes?.every((outcome) => outcome === CaptureOutcome.Complete))
          return;
        const timedOut = outcomes === undefined;
        if (timedOut) for (const stream of streams) stream.stop();
        // A write that is still incomplete at the bound is reported as a
        // timeout; the process exit code and signal are never changed here.
        if (timedOut) incomplete(IncompleteCode.DrainTimeout);
        else if (outcomes.includes(CaptureOutcome.ReadFailed))
          incomplete(IncompleteCode.ReadFailed);
        if (streams.some((stream) => stream.writeFailed()))
          incomplete(IncompleteCode.WriteFailed);
      })());
  };
}
