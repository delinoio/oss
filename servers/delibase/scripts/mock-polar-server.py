#!/usr/bin/env python3
"""Serve the Polar product fixture used by the delibase image smoke test."""

import http.server
import json
import ssl
import sys


PRODUCT_ID = "product_monthly_10_usd"
PRODUCT = {
    "id": PRODUCT_ID,
    "recurring_interval": "month",
    "recurring_interval_count": 1,
    "is_recurring": True,
    "is_archived": False,
    "prices": [
        {
            "amount_type": "fixed",
            "price_currency": "usd",
            "price_amount": 1000,
            "is_archived": False,
            "product_id": PRODUCT_ID,
        }
    ],
}


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self) -> None:
        if self.path != f"/v1/products/{PRODUCT_ID}":
            self.send_error(http.HTTPStatus.NOT_FOUND)
            return
        payload = json.dumps(PRODUCT, separators=(",", ":")).encode()
        self.send_response(http.HTTPStatus.OK)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, message: str, *args: object) -> None:
        sys.stderr.write(f"{self.address_string()} - {message % args}\n")


def main() -> None:
    if len(sys.argv) != 4:
        raise SystemExit("usage: mock-polar-server.py PORT CERT KEY")
    server = http.server.ThreadingHTTPServer(("0.0.0.0", int(sys.argv[1])), Handler)
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.load_cert_chain(sys.argv[2], sys.argv[3])
    server.socket = tls.wrap_socket(server.socket, server_side=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
