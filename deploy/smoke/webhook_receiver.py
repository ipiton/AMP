#!/usr/bin/env python3
"""PHASE-8 smoke e2e -- minimal webhook sink.

Stands in for a real alerting destination so run.sh can assert "webhook
received" with a plain curl instead of grepping container logs. Deliberately
dependency-free (stdlib only) so the smoke stack needs nothing beyond the
base python:3-alpine image already on most CI/dev machines.

Endpoints:
  POST /webhook   -- records one hit + the raw body, always replies 200 "ok"
  GET  /hits      -- text/plain cumulative hit count since container start
  GET  /last      -- application/json: the most recently received body
                     verbatim (empty object "{}" if nothing received yet),
                     so run.sh can assert on actual payload content
                     (alertname, labels, v4 shape fields) rather than only
                     on the hit count.
  GET  /healthz   -- 200, for docker-compose healthcheck / wait_for_http
"""

import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

_lock = threading.Lock()
_hits = 0
_last_body = b"{}"


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        global _hits, _last_body
        length = int(self.headers.get("Content-Length", 0) or 0)
        body = self.rfile.read(length) if length else b""
        with _lock:
            _hits += 1
            count = _hits
            _last_body = body if body else b"{}"
        # Unbuffered-ish stdout line per hit, useful for `docker compose logs`
        # debugging even though run.sh asserts via /hits and /last, not log
        # greps.
        print(f"webhook hit #{count} path={self.path} bytes={len(body)}", flush=True)
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.end_headers()
        self.wfile.write(b"ok")

    def do_GET(self):
        if self.path == "/hits":
            with _lock:
                count = _hits
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.end_headers()
            self.wfile.write(str(count).encode())
            return
        if self.path == "/last":
            with _lock:
                body = _last_body
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(body)
            return
        if self.path == "/healthz":
            self.send_response(200)
            self.end_headers()
            return
        self.send_response(404)
        self.end_headers()

    def log_message(self, fmt, *args):  # noqa: A002 -- stdlib override
        pass  # quiet default access log; do_POST prints its own line


if __name__ == "__main__":
    server = ThreadingHTTPServer(("0.0.0.0", 8090), Handler)
    server.serve_forever()
