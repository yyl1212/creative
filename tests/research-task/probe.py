#!/usr/bin/env python3
"""仅隔离调度验收：短期回环接收器，首份原样接收后丢响应。"""
import argparse
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import secrets
import threading
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", required=True)
    parser.add_argument("--seconds", type=int, default=540)
    args = parser.parse_args()
    if not 1 <= args.seconds <= 570:
        parser.error("隔离接收器必须在 570 秒内结束")
    root = Path(args.root).resolve()
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    if (root / "binding.json").exists():
        parser.error("使用新的隔离目录，不能覆盖旧运行")
    token = secrets.token_urlsafe(32)
    state = {"scope": "isolated_only", "started_at": time.time(), "requests": [], "accepted": 0, "sha256": None}
    lock = threading.Lock()

    def write(name, obj):
        path = root / name
        with path.open("w") as output:
            path.chmod(0o600)
            json.dump(obj, output)

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def do_GET(self):
            self.handle_request()

        def do_POST(self):
            self.handle_request()

        def handle_request(self):
            if self.headers.get("Authorization") != "Bearer " + token:
                self.send_error(401)
                return
            size = int(self.headers.get("Content-Length", 0))
            if size > 1024 * 1024:
                self.send_error(413)
                return
            body = self.rfile.read(size)
            with lock:
                state["requests"].append({"method": self.command, "path": self.path, "time": time.time(),
                                          "sha256": hashlib.sha256(body).hexdigest() if body else None})
                code, result = 200, {}
                if self.path == "/api/research/v1/task" and self.command == "GET":
                    result = {"task": {"task_id": "isolated-scheduler", "rules_version": "strong-wtp.v1",
                              "state": "waiting_for_signals" if state["accepted"] else "active",
                              "should_continue": not bool(state["accepted"]), "target_count": 3,
                              "qualified_count": 0, "remaining_gap": 3}}
                elif self.path == "/api/research/v1/batches" and self.command == "POST":
                    digest = hashlib.sha256(body).hexdigest()
                    obj = json.loads(body)
                    if obj.get("task_id") != "isolated-scheduler" or obj.get("candidates") != []:
                        code = 422
                    elif state["sha256"] and digest != state["sha256"]:
                        code = 409
                    elif state["accepted"] == 0:
                        state.update(accepted=1, sha256=digest)
                        write("server-state.json", state)
                        self.close_connection = True
                        return
                    else:
                        code, result = 202, {"batch_id": obj["batch_id"], "status": "pending_validation"}
                elif self.path == "/api/research/v1/batches/isolated-batch" and self.command == "GET":
                    if state["accepted"]:
                        result = {"batch_id": "isolated-batch", "status": "completed", "results": [],
                                  "qualified_count": 0, "target_count": 3, "remaining_gap": 3,
                                  "task_state": "waiting_for_signals", "should_continue": False}
                    else:
                        code = 404
                else:
                    code = 404
                write("server-state.json", state)
            data = json.dumps(result).encode()
            self.send_response(code)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    write("binding.json", {"origin": "http://127.0.0.1:" + str(server.server_port), "token": token,
                           "state_dir": str(root / "outbox")})
    write("payload.json", {"schema_version": "creative.demand-batch.v1", "rules_version": "strong-wtp.v1",
          "task_id": "isolated-scheduler", "batch_id": "isolated-batch", "run_id": "isolated-first-run",
          "submitted_at": "2026-11-09T00:00:00Z", "candidates": [], "run_report": {"state": "waiting_for_signals",
          "sources_checked": [], "unresolved": ["隔离运输验证，不是真实搜寻"], "resume_hint": None, "usage": []}})
    write("server-state.json", state)
    timer = threading.Timer(args.seconds, server.shutdown)
    timer.start()
    print("隔离接收器已就绪；到时自动停止；没有真实候选或模型调用。", flush=True)
    try:
        server.serve_forever()
    finally:
        timer.cancel()
        server.server_close()
        (root / "binding.json").unlink(missing_ok=True)
        print("隔离接收器已停止，临时凭据已删除。", flush=True)


if __name__ == "__main__":
    main()
