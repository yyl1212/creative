"""隔离传输测试；不访问外网、模型或真实需求库。"""
import copy
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SCRIPT = Path(__file__).resolve().parents[1] / "scripts/research_transport.py"


def batch():
    return {"schema_version": "creative.demand-batch.v1", "rules_version": "strong-wtp.v1",
            "task_id": "fixture-task", "batch_id": "fixture-batch", "run_id": "fixture-run",
            "submitted_at": "2026-11-09T00:00:00Z", "candidates": [],
            "run_report": {"state": "waiting_for_signals", "sources_checked": [],
                           "unresolved": ["隔离测试，无真实候选"], "resume_hint": None, "usage": []}}


class TransportTests(unittest.TestCase):
    def test_opportunity_rule_preserves_frozen_bytes_and_checks_task_version(self):
        payload = batch()
        payload.update(schema_version="creative.project-package.v1", rules_version="opportunity.v2",
                       project={"verdict": "approved"}, candidates=[{"external_id": "fixture"}])
        filename = self.root / "opportunity.json"
        filename.write_text(json.dumps(payload))
        raw = filename.read_bytes()
        self.client.stage(filename)
        with self.assertRaises(self.module.TransportError):
            self.client.submit("fixture-batch")
        self.assertFalse(any(method == "POST" for method, *_ in self.requests))
        self.task["rules_version"] = "opportunity.v2"
        self.client.submit("fixture-batch")
        self.assertEqual(self.requests[-1][1], "/api/research/v1/projects")
        self.assertEqual(self.accepted, raw)
        self.task.update(rules_version="strong-wtp.v1", state="cancelled", should_continue=False)
        self.assertEqual(self.client.receipt("fixture-batch")["payload"]["status"], "completed")
        self.assertEqual(filename.read_bytes(), raw)

    def test_project_package_uses_fixed_import_route(self):
        payload = batch()
        payload["schema_version"] = "creative.project-package.v1"
        payload["candidates"] = [{"external_id": "fixture"}]
        payload["project"] = {"verdict": "approved"}
        filename = self.root / "project.json"
        filename.write_text(json.dumps(payload))
        transport = self.module.Transport(self.binding)
        transport.stage(filename)
        transport.submit("fixture-batch")
        self.assertEqual(self.requests[-1][1], "/api/research/v1/projects")
        self.assertEqual(self.requests[-1][2], filename.read_bytes())
        transport.catalog()
        self.assertEqual(self.requests[-1][1], "/api/research/v1/catalog")

    def setUp(self):
        self.assertTrue(SCRIPT.exists(), "缺少固定目的地传输工具")
        spec = importlib.util.spec_from_file_location("research_transport", SCRIPT)
        self.module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.module)
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name).resolve()
        self.requests, self.accepted = [], None
        self.lose_response, self.redirect, self.throttle = False, False, False
        self.broken_response = None
        self.task = {"task_id": "fixture-task", "rules_version": "strong-wtp.v1",
                     "state": "active", "should_continue": True}
        case = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_GET(self):
                self.handle_request()

            def do_POST(self):
                self.handle_request()

            def handle_request(self):
                body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
                case.requests.append((self.command, self.path, body, self.headers.get("Authorization")))
                if case.broken_response == "status":
                    self.wfile.write(("invalid " + case.settings["token"] + "\r\n\r\n").encode())
                    self.close_connection = True
                    return
                if case.broken_response == "truncated":
                    self.wfile.write(b'HTTP/1.1 200 OK\r\nContent-Length: 200\r\n\r\n{"task":')
                    self.close_connection = True
                    return
                code, result = 200, {}
                if case.redirect:
                    self.send_response(307)
                    self.send_header("Location", case.origin + "/must-not-follow")
                    self.end_headers()
                    return
                if case.throttle:
                    code, result = 429, {"error": {"code": "rate_limited"}}
                elif self.path.endswith("/task"):
                    result = {"task": case.task}
                elif self.command == "POST":
                    if case.accepted is not None and case.accepted != body:
                        code, result = 409, {"error": {"code": "batch_conflict"}}
                    else:
                        case.accepted = body
                        if case.lose_response:
                            case.lose_response = False
                            self.close_connection = True
                            return
                        code, result = 202, {"batch_id": "fixture-batch", "status": "pending_validation"}
                elif case.accepted is not None:
                    result = {"batch_id": "fixture-batch", "status": "completed", "results": [],
                              "qualified_count": 0, "remaining_gap": 3, "should_continue": False,
                              "task_state": "waiting_for_signals"}
                else:
                    code = 404
                data = json.dumps(result).encode()
                self.send_response(code)
                if code == 429:
                    self.send_header("Retry-After", "17")
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()
        self.addCleanup(self.server.server_close)
        self.addCleanup(self.server.shutdown)
        self.origin = "http://127.0.0.1:" + str(self.server.server_port)
        self.binding = self.root / "binding.json"
        self.settings = {"origin": self.origin, "token": "fixture-credential-never-print",
                         "state_dir": str(self.root / "state")}
        self.write_binding()
        self.client = self.module.Transport(self.binding)
        self.payload = self.root / "input.json"
        self.raw = json.dumps(batch(), ensure_ascii=False, indent=2).encode() + b"\n"
        self.payload.write_bytes(self.raw)

    def write_binding(self):
        self.binding.write_text(json.dumps(self.settings))
        self.binding.chmod(0o600)

    def test_lost_response_restart_retries_exact_bytes(self):
        self.client.stage(self.payload)
        self.lose_response = True
        with self.assertRaises(self.module.TransportError):
            self.client.submit("fixture-batch")
        restored = self.module.Transport(self.binding)
        self.assertEqual(restored.outbox()[0]["batch_id"], "fixture-batch")
        self.assertEqual(restored.submit("fixture-batch")["http_status"], 202)
        self.assertEqual([r[2] for r in self.requests if r[0] == "POST"], [self.raw, self.raw])
        self.assertEqual(restored.receipt("fixture-batch")["payload"]["status"], "completed")
        self.assertEqual(restored.outbox()[0]["status"], "completed")

    def test_batch_id_cannot_overwrite_persisted_bytes(self):
        self.client.stage(self.payload)
        self.client.stage(self.payload)
        self.payload.write_bytes(self.raw + b" ")
        with self.assertRaises(self.module.TransportError):
            self.client.stage(self.payload)
        self.client.submit("fixture-batch")
        self.assertEqual(self.accepted, self.raw)

    def test_cancel_reached_rule_change_or_replaced_task_blocks_new_post(self):
        self.client.stage(self.payload)
        original = copy.deepcopy(self.task)
        for change in [{"state": "cancelled", "should_continue": False},
                       {"state": "target_reached", "should_continue": False},
                       {"state": "paused_service", "should_continue": False},
                       {"state": "waiting_for_signals", "should_continue": False},
                       {"rules_version": "strong-wtp.v2"}, {"task_id": "new-task"}]:
            self.task = original | change
            with self.subTest(change=change), self.assertRaises(self.module.TransportError):
                self.client.submit("fixture-batch")
        self.task = None
        with self.assertRaises(self.module.TransportError):
            self.client.submit("fixture-batch")
        self.assertFalse(any(r[0] == "POST" for r in self.requests))

    def test_receipt_allowed_after_cancel(self):
        self.client.stage(self.payload)
        self.client.submit("fixture-batch")
        self.task.update(state="cancelled", should_continue=False)
        self.assertEqual(self.client.receipt("fixture-batch")["http_status"], 200)

    def test_receipt_query_after_raw_retention_expired(self):
        self.client.stage(self.payload)
        self.client.submit("fixture-batch")
        (self.root / "state" / "fixture-batch.batch.json").unlink()
        self.assertEqual(self.client.receipt("fixture-batch")["payload"]["status"], "completed")

    def test_probe_uses_same_transport_and_removes_fixture_credential(self):
        probe_root = self.root / "probe"
        process = subprocess.Popen([sys.executable, str(SCRIPT.parents[1] / "tests/research-task/probe.py"),
                                    "--root", str(probe_root), "--seconds", "3"],
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            self.assertIn("已就绪", process.stdout.readline())
            transport = self.module.Transport(probe_root / "binding.json")
            transport.stage(probe_root / "payload.json")
            with self.assertRaises(self.module.TransportError):
                transport.submit("isolated-batch")
            self.assertEqual(transport.receipt("isolated-batch")["payload"]["status"], "completed")
            state = json.loads((probe_root / "server-state.json").read_text())
            self.assertEqual(state["accepted"], 1)
            out, err = process.communicate(timeout=8)
            self.assertEqual(process.returncode, 0, err)
            self.assertFalse((probe_root / "binding.json").exists())
        finally:
            if process.poll() is None:
                process.terminate()
                process.communicate(timeout=5)

    def test_redirect_not_followed_and_proxy_not_used(self):
        self.redirect = True
        old = os.environ.get("http_proxy")
        os.environ["http_proxy"] = "http://127.0.0.1:1"
        try:
            self.assertEqual(self.client.task()["http_status"], 307)
        finally:
            if old is None:
                os.environ.pop("http_proxy", None)
            else:
                os.environ["http_proxy"] = old
        self.assertEqual(len(self.requests), 1)
        self.assertEqual(self.requests[0][1], "/api/research/v1/task")
        self.assertEqual(self.requests[0][3], "Bearer " + self.settings["token"])

    def test_rate_limit_is_returned_without_retry_storm(self):
        self.throttle = True
        result = self.client.task()
        self.assertEqual((result["http_status"], result["retry_after"]), (429, "17"))
        self.assertEqual(len(self.requests), 1)

    def test_unsafe_origins_binding_permissions_and_ids_fail_closed(self):
        for origin in ["http://example.com", "https://user:pw@example.com", "https://example.com/path",
                       "https://example.com?next=evil", "https://example.com#x"]:
            self.settings["origin"] = origin
            self.write_binding()
            with self.subTest(origin=origin), self.assertRaises(self.module.TransportError):
                self.module.Transport(self.binding)
        self.settings["origin"] = self.origin
        self.write_binding()
        self.binding.chmod(0o644)
        with self.assertRaises(self.module.TransportError):
            self.module.Transport(self.binding)
        for ident in ["../secret", "x?next=evil", "https://evil.example", ""]:
            with self.subTest(id=ident), self.assertRaises(self.module.TransportError):
                self.client.receipt(ident)
        self.assertEqual(self.requests, [])

    def test_corrupt_or_symlink_outbox_fails_closed(self):
        self.client.stage(self.payload)
        saved = self.root / "state" / "fixture-batch.batch.json"
        original = saved.read_bytes()
        saved.write_bytes(original.replace(b'"sha256": "', b'"sha256": "bad'))
        with self.assertRaises(self.module.TransportError):
            self.client.submit("fixture-batch")
        saved.unlink()
        saved.symlink_to(self.payload)
        with self.assertRaises(self.module.TransportError):
            self.client.outbox()
        self.assertEqual(self.requests, [])

    def test_private_files_and_no_credentials_in_output(self):
        self.client.stage(self.payload)
        for path in (self.root / "state").iterdir():
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            self.assertNotIn(self.settings["token"].encode(), path.read_bytes())
        result = subprocess.run([sys.executable, str(SCRIPT), "--binding", str(self.binding), "outbox"],
                                capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn(self.settings["token"], result.stdout + result.stderr)

    def test_protocol_errors_never_expose_credential_or_traceback(self):
        self.client.stage(self.payload)
        saved = self.root / "state" / "fixture-batch.batch.json"
        before = saved.read_bytes()
        for kind in ["status", "truncated"]:
            with self.subTest(response=kind):
                self.broken_response = kind
                result = subprocess.run([sys.executable, str(SCRIPT), "--binding", str(self.binding), "task"],
                                        capture_output=True, text=True, timeout=10)
                self.assertEqual(result.returncode, 1)
                self.assertNotIn(self.settings["token"], result.stdout + result.stderr)
                self.assertNotIn("Traceback", result.stdout + result.stderr)
                self.assertIn(json.loads(result.stderr)["error"],
                              ("transport_unavailable_outcome_unknown", "invalid_json"))
                self.assertEqual(saved.read_bytes(), before)

    def test_mismatched_or_invalid_receipt_fails_closed_on_recovery(self):
        self.client.stage(self.payload)
        path = self.root / "state" / "fixture-batch.receipt.json"
        valid = {"http_status": 200, "payload": {"batch_id": "fixture-batch", "status": "completed"}}
        invalid = [dict(valid, payload={"batch_id": "other-batch", "status": "completed"}),
                   dict(valid, http_status=500), dict(valid, payload={"status": "completed"}),
                   dict(valid, payload={"batch_id": "fixture-batch", "status": "invented"}),
                   dict(valid, payload={"batch_id": "fixture-batch"})]
        for receipt in invalid:
            with self.subTest(receipt=receipt):
                path.write_text(json.dumps(receipt))
                path.chmod(0o600)
                with self.assertRaises(self.module.TransportError):
                    self.client.outbox()
        self.assertEqual(self.requests, [])

    def test_invalid_duplicate_json_and_oversized_payload_never_staged(self):
        for raw in [b'{"batch_id":"one","batch_id":"two"}', b'{}', b'x' * (1024 * 1024 + 1)]:
            self.payload.write_bytes(raw)
            with self.assertRaises(self.module.TransportError):
                self.client.stage(self.payload)
        self.assertEqual(self.client.outbox(), [])


if __name__ == "__main__":
    unittest.main()
