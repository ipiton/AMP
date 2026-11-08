# Webhook sink for run.sh: prints one line per POST, "SINK <alertnames>",
# so run.sh counts notifications from `docker compose logs sink`.
import http.server
import json


class Handler(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        try:
            alerts = json.loads(body).get("alerts", [])
            names = ",".join(sorted(a["labels"].get("alertname", "") for a in alerts))
        except (ValueError, KeyError, AttributeError):
            names = "<unparsable>"
        print(f"SINK {names}", flush=True)
        self.send_response(200)
        self.end_headers()

    def log_message(self, *args):
        pass


http.server.HTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
