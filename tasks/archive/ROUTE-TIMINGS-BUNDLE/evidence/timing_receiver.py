#!/usr/bin/env python3
"""ROUTE-TIMINGS-BUNDLE smoke: webhook sink that logs arrival time per path.

Each POST appends one line to stdout: "<unix_ts_ms> <path> <alertnames>".
Usage: python3 timing_receiver.py <port>
"""
import json
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", 0) or 0))
        names = []
        try:
            names = sorted({a["labels"].get("alertname", "") for a in json.loads(body).get("alerts", [])})
        except Exception:  # noqa: BLE001 - sink must never fail the sender
            pass
        print(f"{int(time.time() * 1000)} {self.path} {','.join(names)}", flush=True)
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"ok")

    def log_message(self, *args):
        pass


ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), Handler).serve_forever()
