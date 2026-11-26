"""Stub of the Nas endpoints `dadi` calls; records every request body in a log file."""
import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

log_path = sys.argv[2]
state = {
    "dwar": {"ANTHROPIC_API_KEY": "sk-ant-abcdef123456", "GEMINI_API_KEY": "", "OPENAI_API_KEY": "", "DEEPGRAM_API_KEY": ""},
    "chaavi": {"CHAAVI_EMAIL": "me@example.com", "CHAAVI_PASSWORD": ""},
}


class Handler(BaseHTTPRequestHandler):
    def reply(self, status, body):
        data = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def body(self):
        length = int(self.headers.get("Content-Length", 0))
        raw = self.rfile.read(length).decode()
        with open(log_path, "a") as f:
            f.write(f"{self.command} {self.path} {raw}\n")
        return json.loads(raw) if raw else {}

    def do_GET(self):
        if self.path == "/access":
            self.reply(200, {"users": [{"name": "ankur", "password_set": True}], "tpm": {"present": True, "enrolled": True, "pcrs": "7"}})
        elif self.path == "/clients":
            self.reply(200, {"clients": [{"node_name": "mac", "online": True, "pending": False, "ip_addresses": ["100.64.0.8"]}, {"node_name": "phone", "online": False, "pending": True, "ip_addresses": []}]})
        elif self.path.startswith("/modules/") and self.path.endswith("/settings"):
            self.reply(200, {"env": state[self.path.split("/")[2]]})
        else:
            self.reply(404, {"error": {"type": "not_found", "message": "no route"}})

    def do_POST(self):
        payload = self.body()
        if self.path == "/access/users":
            self.reply(200, {"username": payload["username"], "created": payload["username"] != "ankur"})
        elif self.path == "/provision":
            if payload["node_name"] == "taken":
                self.reply(409, {"error": {"type": "conflict", "message": "node name taken"}})
            else:
                self.reply(200, {"bundle": "BUNDLE-" + payload["node_name"]})
        else:
            self.reply(404, {"error": {"type": "not_found", "message": "no route"}})

    def do_PUT(self):
        payload = self.body()
        state[self.path.split("/")[2]] = payload["env"]
        self.reply(200, {"ok": True})

    def do_DELETE(self):
        self.body()
        self.reply(200, {"ok": True})

    def log_message(self, *args):
        pass


HTTPServer(("127.0.0.1", int(sys.argv[1])), Handler).serve_forever()
