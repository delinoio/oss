// The development proxy must not turn cross-origin traffic into trusted RPCs.
export function allowLocalProxy(request: {
  method?: string;
  headers: Record<string, string | string[] | undefined>;
}): boolean {
  const host = request.headers.host;
  return (host === "localhost:46308" || host === "127.0.0.1:46308") &&
    request.headers.origin === `http://${host}` &&
    request.method === "POST" && request.headers["x-ach-api-version"] === "1";
}
