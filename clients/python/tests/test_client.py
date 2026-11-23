import unittest
import io
import json
import base64
from zygote_harness import ZygoteClient, HarnessError

class TestZygoteClient(unittest.TestCase):
    def test_effect(self):
        # Prepare a mock stdin with a JSON-RPC response
        mock_res = {
            "jsonrpc": "2.0",
            "id": 1,
            "result": {
                "value": base64.b64encode(b"ReplayedAnswer").decode("utf-8")
            }
        }
        stdin = io.StringIO(json.dumps(mock_res) + "\n")
        stdout = io.StringIO()

        client = ZygoteClient(stdin=stdin, stdout=stdout)

        # Call effect
        res_bytes = client.effect("model", "prompt-1", b"MyPrompt")

        # Verify output
        self.assertEqual(res_bytes, b"ReplayedAnswer")

        # Verify what was sent to stdout
        sent = json.loads(stdout.getvalue())
        self.assertEqual(sent["method"], "effect")
        self.assertEqual(sent["params"]["op"], "model")
        self.assertEqual(sent["params"]["key"], "prompt-1")
        self.assertEqual(base64.b64decode(sent["params"]["value"]), b"MyPrompt")

    def test_error(self):
        mock_err = {
            "jsonrpc": "2.0",
            "id": 1,
            "error": {
                "code": -32600,
                "message": "divergence: effect mismatch"
            }
        }
        stdin = io.StringIO(json.dumps(mock_err) + "\n")
        stdout = io.StringIO()

        client = ZygoteClient(stdin=stdin, stdout=stdout)

        with self.assertRaises(HarnessError) as ctx:
            client.step("step-1")

        self.assertEqual(ctx.exception.code, -32600)
        self.assertIn("divergence", ctx.exception.message)

    def test_actor_params(self):
        mock_res1 = {
            "jsonrpc": "2.0",
            "id": 1,
            "result": {"value": base64.b64encode(b"WorkerAnswer").decode("utf-8")}
        }
        mock_res2 = {
            "jsonrpc": "2.0",
            "id": 2,
            "result": {"match": True}
        }
        stdin = io.StringIO(json.dumps(mock_res1) + "\n" + json.dumps(mock_res2) + "\n")
        stdout = io.StringIO()

        client = ZygoteClient(stdin=stdin, stdout=stdout)

        # Call effect with actor
        res_bytes = client.effect("tool:read", "file1", b"path", actor="worker-1")
        self.assertEqual(res_bytes, b"WorkerAnswer")

        # Call step with actor
        res_step = client.step("step-alpha", actor="worker-1")
        self.assertTrue(res_step["match"])

        lines = [json.loads(line) for line in stdout.getvalue().strip().split("\n")]
        self.assertEqual(len(lines), 2)
        self.assertEqual(lines[0]["params"]["actor"], "worker-1")
        self.assertEqual(lines[1]["params"]["actor"], "worker-1")

    def test_nested_step_params(self):
        mock_res = {
            "jsonrpc": "2.0",
            "id": 1,
            "result": {"match": True}
        }
        stdin = io.StringIO(json.dumps(mock_res) + "\n")
        stdout = io.StringIO()

        client = ZygoteClient(stdin=stdin, stdout=stdout)

        # Call step with parent_id
        res_step = client.step("child-step", actor="sub-1", parent_id=2)
        self.assertTrue(res_step["match"])

        sent = json.loads(stdout.getvalue().strip())
        self.assertEqual(sent["params"]["name"], "child-step")
        self.assertEqual(sent["params"]["actor"], "sub-1")
        self.assertEqual(sent["params"]["parent_id"], 2)

if __name__ == '__main__':
    unittest.main()
