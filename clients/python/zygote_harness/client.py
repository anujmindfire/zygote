import sys
import json
import base64
import threading
from typing import Dict, Any, Optional


class HarnessError(Exception):
    def __init__(self, code: int, message: str, data: Any = None):
        self.code = code
        self.message = message
        self.data = data
        super().__init__(f"[{code}] {message}")


class ZygoteClient:
    def __init__(self, stdin=sys.stdin, stdout=sys.stdout):
        self._stdin = stdin
        self._stdout = stdout
        self._id_counter = 0
        self._lock = threading.Lock()
        self._pending = {}
        self._unclaimed = {}
        self._reader_thread = None
        self._closed = False

    def _ensure_reader(self):
        if self._reader_thread is None:
            self._reader_thread = threading.Thread(target=self._reader_loop, daemon=True)
            self._reader_thread.start()

    def _reader_loop(self):
        while not self._closed:
            try:
                line = self._stdin.readline()
                if not line:
                    break
                line_str = line.strip()
                if not line_str:
                    continue
                try:
                    res = json.loads(line_str)
                except Exception:
                    continue

                if not isinstance(res, dict) or res.get("jsonrpc") != "2.0":
                    continue

                req_id = res.get("id")
                if req_id is not None:
                    with self._lock:
                        event_data = self._pending.get(req_id)
                        if event_data is not None:
                            event, result_box = event_data
                            result_box["response"] = res
                            event.set()
                        else:
                            self._unclaimed[req_id] = res
            except Exception:
                break

        with self._lock:
            for req_id, (event, result_box) in list(self._pending.items()):
                result_box["error"] = RuntimeError("Zygote harness connection closed")
                event.set()

    def _call(self, method: str, params: Dict[str, Any]) -> Any:
        with self._lock:
            self._ensure_reader()
            self._id_counter += 1
            req_id = self._id_counter
            event = threading.Event()
            result_box = {}

            if req_id in self._unclaimed:
                result_box["response"] = self._unclaimed.pop(req_id)
                event.set()
            else:
                self._pending[req_id] = (event, result_box)

            req = {
                "jsonrpc": "2.0",
                "method": method,
                "params": params,
                "id": req_id
            }

            try:
                msg = json.dumps(req)
                self._stdout.write(msg + "\n")
                self._stdout.flush()
            except Exception as e:
                self._pending.pop(req_id, None)
                raise RuntimeError(f"Failed to write to zygote harness: {e}")

        event.wait()

        with self._lock:
            self._pending.pop(req_id, None)

        if "error" in result_box and not ("response" in result_box):
            raise result_box["error"]

        res = result_box.get("response", {})
        if "error" in res and res["error"] is not None:
            err = res["error"]
            raise HarnessError(err.get("code", -1), err.get("message", "Unknown error"), err.get("data"))

        if res.get("id") != req_id:
            raise RuntimeError(f"JSON-RPC ID mismatch: expected {req_id}, got {res.get('id')}")

        return res.get("result")

    def handshake(self, version: int = 1) -> int:
        res = self._call("handshake", {"version": version})
        return res.get("version", version)

    def step(self, name: str, actor: Optional[str] = None) -> Dict[str, Any]:
        params = {"name": name}
        if actor is not None:
            params["actor"] = actor
        return self._call("step", params)

    def effect(self, op: str, key: str, value: Optional[bytes] = None, actor: Optional[str] = None) -> bytes:
        params = {"op": op, "key": key}
        if value is not None:
            params["value"] = base64.b64encode(value).decode("utf-8")
        if actor is not None:
            params["actor"] = actor

        res = self._call("effect", params)
        b64_val = res.get("value", "")
        if b64_val:
            return base64.b64decode(b64_val)
        return b""

    def close(self):
        self._closed = True
