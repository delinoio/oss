import net from "node:net";

export function parseDevPort(value, envName) {
  const port = Number(value);
  if (!Number.isInteger(port) || port < 1 || port > 65_535) {
    throw new Error(`${envName} must be an integer from 1 through 65535`);
  }
  return port;
}

function isHostPortAvailable(port, host) {
  return new Promise((resolve, reject) => {
    const server = net.createServer();

    server.once("error", (error) => {
      if (error.code === "EADDRINUSE" || error.code === "EACCES") {
        resolve(false);
        return;
      }
      if (error.code === "EADDRNOTAVAIL" || error.code === "EAFNOSUPPORT") {
        resolve(true);
        return;
      }
      reject(error);
    });

    server.once("listening", () => {
      server.close((error) => {
        if (error) {
          reject(error);
          return;
        }
        resolve(true);
      });
    });

    server.listen({ port, host });
  });
}

export async function isDevPortAvailable(port) {
  // macOS permits a wildcard bind even when a loopback-only listener owns the
  // same port, so probe the IPv4 and IPv6 loopback addresses independently.
  const results = await Promise.all([
    isHostPortAvailable(port, "127.0.0.1"),
    isHostPortAvailable(port, "::1"),
  ]);
  return results.every(Boolean);
}
