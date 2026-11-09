#!/usr/bin/env python3
"""定时提示词的固定 API 传输及 outbox；不执行搜索、调度或模型调用。"""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request

LIMIT = 1024 * 1024


class TransportError(Exception):
    pass


def identifier(value):
    if not isinstance(value, str) or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_-]{0,127}", value):
        raise TransportError("invalid_identifier")
    return value


def decode(raw):
    def pairs(items):
        obj = {}
        for key, value in items:
            if key in obj:
                raise TransportError("duplicate_json_key")
            obj[key] = value
        return obj
    try:
        return json.loads(raw.decode("utf-8"), object_pairs_hook=pairs,
                          parse_constant=lambda _: (_ for _ in ()).throw(ValueError()))
    except (ValueError, UnicodeError):
        raise TransportError("invalid_json") from None


def private_read(path, limit=2 * LIMIT):
    try:
        with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW), "rb") as stream:
            mode = os.fstat(stream.fileno())
            if not stat.S_ISREG(mode.st_mode) or mode.st_uid != os.getuid() or mode.st_mode & 0o077:
                raise TransportError("private_file_required")
            raw = stream.read(limit + 1)
            if len(raw) > limit:
                raise TransportError("file_too_large")
            return raw
    except OSError:
        raise TransportError("private_file_unreadable") from None


def save(path, value, exclusive=False):
    raw = json.dumps(value, ensure_ascii=False).encode()
    fd, name = tempfile.mkstemp(dir=path.parent, prefix=".writing-")
    try:
        with os.fdopen(fd, "wb") as stream:
            stream.write(raw)
            stream.flush()
            os.fsync(stream.fileno())
        if exclusive:
            os.link(name, path)
        else:
            os.replace(name, path)
        directory = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        if os.path.exists(name):
            os.unlink(name)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Transport:
    def __init__(self, binding):
        config = decode(private_read(binding, 16384))
        if not isinstance(config, dict) or set(config) != {"origin", "token", "state_dir"}:
            raise TransportError("invalid_binding")
        try:
            origin = urllib.parse.urlsplit(config["origin"])
            if (origin.scheme not in ("https", "http") or not origin.hostname or origin.username is not None
                    or origin.password is not None or origin.path not in ("", "/") or origin.query or origin.fragment
                    or (origin.scheme == "http" and origin.hostname not in ("127.0.0.1", "localhost", "::1"))):
                raise ValueError()
            origin.port
        except (TypeError, ValueError, AttributeError):
            raise TransportError("invalid_origin") from None
        self.origin = config["origin"].rstrip("/")
        self.token = config["token"]
        if not isinstance(self.token, str) or not self.token or not re.fullmatch(r"[!-~]+", self.token):
            raise TransportError("invalid_credential")
        if not isinstance(config["state_dir"], str) or not Path(config["state_dir"]).is_absolute():
            raise TransportError("absolute_state_directory_required")
        self.root = Path(config["state_dir"])
        # 工件不得经符号链接写进其他目录；系统 /tmp 在 macOS 上须使用 /private/tmp。
        if any(part.is_symlink() for part in [self.root, *self.root.parents]):
            raise TransportError("symlink_state_directory")
        self.root.mkdir(mode=0o700, parents=True, exist_ok=True)
        mode = self.root.stat()
        if mode.st_uid != os.getuid() or mode.st_mode & 0o077:
            raise TransportError("private_state_directory_required")
        self.http = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())

    def _request(self, path, body=None):
        request = urllib.request.Request(self.origin + "/api/research/v1/" + path, data=body,
                                         headers={"Authorization": "Bearer " + self.token,
                                                  "Content-Type": "application/json"})
        try:
            try:
                response = self.http.open(request, timeout=20)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                raw = response.read(2 * LIMIT + 1)
                if len(raw) > 2 * LIMIT:
                    raise TransportError("response_too_large")
                payload = decode(raw) if raw else None
                result = {"http_status": response.code, "payload": payload,
                          "retry_after": response.headers.get("Retry-After")}
                # 包括错误正文在内都不回显凭据，不使用服务器返回的 URL 发请求。
                return json.loads(json.dumps(result).replace(self.token, "[REDACTED]"))
        except TransportError:
            raise
        except (OSError, ValueError, urllib.error.URLError):
            raise TransportError("transport_unavailable_outcome_unknown") from None

    def task(self):
        return self._request("task")

    def _load(self, batch_id):
        envelope = decode(private_read(self.root / (identifier(batch_id) + ".batch.json")))
        try:
            raw = base64.b64decode(envelope["body"], validate=True)
            obj = decode(raw)
            if (len(raw) > LIMIT or hashlib.sha256(raw).hexdigest() != envelope["sha256"]
                    or obj["batch_id"] != batch_id):
                raise ValueError()
            return raw, obj
        except (KeyError, TypeError, ValueError):
            raise TransportError("outbox_corrupt") from None

    def stage(self, filename):
        with open(filename, "rb") as stream:
            raw = stream.read(LIMIT + 1)
        if len(raw) > LIMIT:
            raise TransportError("payload_too_large")
        obj = decode(raw)
        if not isinstance(obj, dict) or obj.get("schema_version") != "creative.demand-batch.v1":
            raise TransportError("invalid_batch")
        for field in ("task_id", "batch_id", "run_id"):
            identifier(obj.get(field))
        if (not isinstance(obj.get("rules_version"), str) or not obj["rules_version"]
                or not isinstance(obj.get("candidates"), list) or len(obj["candidates"]) > 20):
            raise TransportError("invalid_batch")
        digest = hashlib.sha256(raw).hexdigest()
        path = self.root / (obj["batch_id"] + ".batch.json")
        try:
            save(path, {"sha256": digest, "body": base64.b64encode(raw).decode()}, exclusive=True)
        except FileExistsError:
            if self._load(obj["batch_id"])[0] != raw:
                raise TransportError("batch_bytes_conflict") from None
        return {"batch_id": obj["batch_id"], "sha256": digest, "status": "staged"}

    def outbox(self):
        result = []
        for path in sorted(self.root.glob("*.batch.json")):
            ident = path.name.removesuffix(".batch.json")
            raw, obj = self._load(ident)
            receipt = self.root / (ident + ".receipt.json")
            status = "unconfirmed"
            if receipt.exists() or receipt.is_symlink():
                stored = decode(private_read(receipt))
                if not isinstance(stored, dict) or not isinstance(stored.get("payload"), dict):
                    raise TransportError("receipt_corrupt")
                status = stored["payload"].get("status", "unconfirmed")
            result.append({"batch_id": ident, "task_id": obj["task_id"], "rules_version": obj["rules_version"],
                           "sha256": hashlib.sha256(raw).hexdigest(), "status": status})
        return result

    def _remember(self, batch_id, result):
        if result["http_status"] in (200, 202):
            payload = result["payload"]
            if not isinstance(payload, dict) or payload.get("batch_id") != batch_id:
                raise TransportError("receipt_identity_mismatch")
            save(self.root / (batch_id + ".receipt.json"), result)
        return result

    def submit(self, batch_id):
        raw, obj = self._load(batch_id)
        current = self.task()
        task = current["payload"].get("task") if isinstance(current["payload"], dict) else None
        if current["http_status"] != 200:
            return current
        if (not isinstance(task, dict) or task.get("task_id") != obj["task_id"]
                or task.get("rules_version") != obj["rules_version"]
                or task.get("state") != "active" or task.get("should_continue") is not True):
            raise TransportError("task_changed_query_receipt_before_any_new_batch")
        return self._remember(batch_id, self._request("batches", raw))

    def receipt(self, batch_id):
        return self._remember(batch_id, self._request("batches/" + identifier(batch_id)))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binding", required=True, help="私有绑定文件路径，不含命令行凭据")
    parser.add_argument("action", choices=("task", "outbox", "stage", "submit", "receipt"))
    parser.add_argument("value", nargs="?")
    args = parser.parse_args()
    try:
        transport = Transport(args.binding)
        needs_value = args.action in ("stage", "submit", "receipt")
        if needs_value != (args.value is not None):
            raise TransportError("invalid_arguments")
        method = getattr(transport, args.action)
        result = method(args.value) if needs_value else method()
        print(json.dumps(result, ensure_ascii=False).replace(transport.token, "[REDACTED]"))
        return 0 if not isinstance(result, dict) or result.get("http_status", 200) < 300 else 1
    except TransportError as error:
        print(json.dumps({"error": str(error), "outbox_preserved": True}), file=sys.stderr)
    except (OSError, ValueError, TypeError):
        print('{"error":"local_io_or_configuration_error","outbox_preserved":true}', file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
