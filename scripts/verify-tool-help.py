#!/usr/bin/env python3
"""One-shot S4 acceptance: real CLI/Core/SQLite, deterministic loopback provider.

Run only on the reviewed stable candidate: python3 scripts/verify-tool-help.py
No live provider/model selection is tested. No automatic retries of this harness.
All product state and the built binary live in TemporaryDirectory; JSON evidence
is printed to stdout (redirect it outside the repository if it must be retained).
Requires Go and its dependencies/toolchain already cached; downloads are disabled.
Child spawn/status/steer/collect examples remain covered by focused app tests.
"""

import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import re
import shutil
import signal
import sqlite3
import subprocess
import tempfile
import threading
import time


ROOT = Path(__file__).resolve().parent.parent
FIRST, RESUME = "S4 initial help and parallel reads", "S4 resume edited guidance and help"
POLICIES = ["S4_WORKSPACE_POLICY_ONE\n", "S4_WORKSPACE_POLICY_TWO\n"]
READ_MARKERS = ["S4_READ_HELP_ONE", "S4_READ_HELP_TWO"]
JS_MARKER = "S4_RUN_JS_HELP_ON_DEMAND"
DORMANT = "S4_UNREQUESTED_HELP_NOT_ARCHIVED"


def require(condition, detail):
    if not condition:
        raise AssertionError(detail)


def encoded(value):
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"))


def write(path, text):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")


def command(args, *, env, cwd, timeout=120):
    result = subprocess.run([str(arg) for arg in args], env=env, cwd=cwd,
                            capture_output=True, text=True, timeout=timeout)
    require(result.returncode == 0,
            f"{args[0:2]} exited {result.returncode}\n{result.stdout}\n{result.stderr}")
    return result


def check_schema(wire):
    tools = {entry["function"]["name"]: entry["function"] for entry in wire["tools"]}
    require(set(tools) == {"run_js", "read_file", "write_file"}, "selected tools changed")
    for name, normal_required in (("read_file", "path"), ("run_js", "code")):
        schema = tools[name]["parameters"]
        require(schema["type"] == "object" and "help" in schema["properties"], name)
        branches = schema["anyOf"]
        require(any(branch.get("required") == ["help"] and
                    branch["properties"]["help"].get("enum") == [True]
                    for branch in branches), f"{name}: help-only schema missing")
        require(any(normal_required in branch.get("required", [])
                    for branch in branches), f"{name}: normal required argument lost")
    return tools


class Scenario:
    def __init__(self, workspace, read_help, js_help):
        self.workspace, self.read_help, self.js_help = workspace, read_help, js_help
        self.requests, self.errors, self.code = [], [], None
        self.steps = {FIRST: 0, RESUME: 0}
        self.documents = {}

    def result(self, wire, call_id):
        matches = [message for message in wire["messages"]
                   if message.get("role") == "tool" and message.get("tool_call_id") == call_id]
        require(len(matches) == 1, f"missing/duplicate tool result {call_id}")
        return json.loads(matches[0]["content"])

    def next(self, wire):
        task = [message["content"] for message in wire["messages"] if message["role"] == "user"][-1]
        require(task in self.steps, f"unexpected task {task}")
        self.steps[task] += 1
        step, version = self.steps[task], int(task == RESUME)
        check_schema(wire)
        instruction = "\n".join(message["content"] for message in wire["messages"]
                                if message["role"] == "system")
        require(POLICIES[version].strip() in instruction, "current workspace guidance absent")
        require("S4_USER_POLICY_SHOULD_NOT_WIN" not in instruction, "user policy beat workspace")
        require("S4_CONFIG_DIRECTORY_IS_NOT_WORKSPACE" not in instruction, "config path selected policy")
        require(all(marker not in instruction for marker in [*READ_MARKERS, JS_MARKER, DORMANT]),
                "help eagerly injected into instructions")
        self.requests.append(wire)
        prefix = "resume" if version else "first"
        if step == 1:
            if version:
                require(READ_MARKERS[0] in encoded(wire["messages"]) and
                        JS_MARKER in encoded(wire["messages"]), "resume lost acquired help history")
            return self.call(prefix + "-read-help", "read_file", {"help": True})
        read_result = self.result(wire, prefix + "-read-help")
        require(read_result == {"name": "read_file", "available": True,
                                "text": self.read_help[version]}, "wrong captured read_file help")
        if version:
            require(step == 2, "unexpected resume request")
            return {"content": "S4 resumed: current guidance and edited read_file help verified."}
        if step == 2:
            return self.call("first-js-help", "run_js", {"help": True})
        js_result = self.result(wire, "first-js-help")
        require(js_result == {"name": "run_js", "available": True, "text": self.js_help},
                "wrong run_js help (help must not require code)")
        if step == 3:
            examples = re.findall(r"```(?:js|javascript)\s*\n(.*?)```", js_result["text"], re.S)
            self.code = next((code for code in examples
                              if "Promise.all" in code and "tools.read_file" in code), None)
            require(self.code is not None, "run_js help has no independent-read example")
            paths = re.search(r"const paths\s*=\s*(\[[^;]+\]);", self.code)
            require(paths is not None, "read example no longer declares its source paths; coordinate harness update")
            for index, path in enumerate(json.loads(paths.group(1))):
                require(not Path(path).is_absolute() and ".." not in Path(path).parts,
                        "example must use prepared workspace documents")
                body = f"# S4 document {index}\nExact source: {path}\n"
                self.documents[path] = body
                write(self.workspace / path, body)
            write(self.workspace / "README.md", "S4 isolated tool-help verification workspace\n")
            # Send the actual returned fenced body, not a rewritten approximation.
            return self.call("first-parallel", "run_js", {"code": self.code})
        require(step == 4, "unexpected initial request")
        results = self.result(wire, "first-parallel")
        require(len(results) == len(self.documents), f"parallel read results: {results}")
        for result, (path, text) in zip(results, self.documents.items()):
            require(result["path"] == path and result["offset"] == 1
                    and result["text"] == text, f"wrong parallel read result: {result}")
        return {"content": "S4 initial: help retrieved and documented parallel reads completed."}

    @staticmethod
    def call(call_id, name, arguments):
        return {"tool_calls": [{"index": 0, "id": call_id, "type": "function",
                                "function": {"name": name, "arguments": encoded(arguments)}}]}


class Provider(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_CONNECT(self):
        self.server.scenario.errors.append(f"unexpected external HTTPS: {self.path}")
        self.send_error(502, "No external network in S4")

    def do_GET(self):
        self.server.scenario.errors.append(f"unexpected HTTP GET: {self.path}")
        self.send_error(502, "No external network in S4")

    def do_POST(self):
        scenario = self.server.scenario
        try:
            require(self.path == "/chat/completions", f"unexpected provider destination: {self.path}")
            require(not self.headers.get("Authorization"), "local provider received Authorization")
            wire = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            require(wire["model"] == "s4-local-model" and wire["stream"], "wrong model/stream")
            delta = scenario.next(wire)
            reason = "tool_calls" if "tool_calls" in delta else "stop"
            event = {"model": "s4-local-model", "choices": [
                {"index": 0, "delta": delta, "finish_reason": reason}]}
            body = ("data: " + encoded(event) + "\n\ndata: [DONE]\n\n").encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        except Exception as error:
            scenario.errors.append(str(error))
            self.send_error(400, str(error))


def inspect_db(db_path, scenario, policy_path, executions):
    summary = []
    with sqlite3.connect(db_path.as_uri() + "?mode=ro", uri=True) as db:
        require(db.execute("SELECT turn_count FROM sessions").fetchall() == [(2,)], "expected one two-turn Session")
        contents = [bytes(row[0]).decode() for row in db.execute("SELECT content_bytes FROM contents")]
        require(not any(DORMANT in body for body in contents), "unrequested help archived in contents")
        for version, execution in enumerate(executions):
            row = db.execute("SELECT outcome,adoption,configuration_id FROM executions WHERE execution_id=?",
                             (execution,)).fetchone()
            require(row[:2] == ("completed", "canonical"), f"unsettled/noncanonical execution: {row}")
            snapshot = json.loads(db.execute("SELECT c.content_bytes FROM configurations f JOIN contents c "
                                            "ON c.content_digest=f.snapshot_content_digest WHERE configuration_id=?",
                                            (row[2],)).fetchone()[0])
            require("tools" not in snapshot, "configuration archived detailed tool definitions")
            component = next(item for item in snapshot["instructionComponents"]
                             if item["identity"] == "instruction:tool-usage-policy")
            digest = "sha256:" + hashlib.sha256(POLICIES[version].encode()).hexdigest()
            require(component["text"] == POLICIES[version] and
                    component["source"]["path"] == str(policy_path) and
                    component["source"]["digest"] == digest, "instruction component provenance differs")
            resources = db.execute("SELECT r.payload_json,c.content_bytes FROM semantic_records r "
                                   "LEFT JOIN contents c USING(content_digest) "
                                   "WHERE r.execution_id=? AND r.kind='resource_revision'", (execution,)).fetchall()
            require(any(json.loads(payload).get("logicalIdentity") == "instruction:tool-usage-policy" and
                        json.loads(payload).get("sourceLocator") == str(policy_path) and
                        json.loads(payload).get("contentDigest") == digest and
                        bytes(body).decode() == POLICIES[version] for payload, body in resources),
                    "guidance source revision missing")
            for payload, body in resources:
                require(all(marker not in payload + (bytes(body).decode() if body else "")
                            for marker in [*READ_MARKERS, JS_MARKER, DORMANT]), "help archived as resource revision")
            latest = db.execute("SELECT record_id,wire_body FROM execution_latest_requests WHERE execution_id=?",
                                (execution,)).fetchall()
            expected = [wire for wire in scenario.requests if
                        [m["content"] for m in wire["messages"] if m["role"] == "user"][-1] == (RESUME if version else FIRST)]
            require(len(latest) == 1 and json.loads(latest[0][1]) == expected[-1], "latest wire snapshot differs")
            last_record = db.execute("SELECT record_id FROM semantic_records WHERE execution_id=? "
                                     "AND kind='model_request' AND json_extract(payload_json,'$.wireBytes') IS NOT NULL "
                                     "ORDER BY ordinal DESC LIMIT 1", (execution,)).fetchone()[0]
            require(latest[0][0] == last_record, "snapshot is not the latest request")
            summary.append({"execution": execution, "modelRequests": len(expected),
                            "initialWireBytes": len(encoded(expected[0]).encode()),
                            "instructionBytes": sum(len(m["content"].encode()) for m in expected[0]["messages"]
                                                    if m["role"] == "system"),
                            "toolSchemaBytes": len(encoded(expected[0]["tools"]).encode()),
                            "latestWireBytes": len(bytes(latest[0][1])), "policyDigest": digest})
    return summary


def verify(temp):
    # Discover existing build caches before changing HOME; no downloads or paid calls.
    build_env = {**os.environ, "GOPROXY": "off", "GOSUMDB": "off"}
    caches = command(["go", "env", "GOMODCACHE", "GOCACHE"], env=build_env, cwd=ROOT).stdout.splitlines()
    workspace, config_dir = temp / "workspace", temp / "external-config"
    workspace.mkdir()
    env = {"PATH": os.environ["PATH"], "LANG": "C.UTF-8", "HOME": str(temp / "home"),
           "TMPDIR": str(temp / "tmp"), "XDG_CONFIG_HOME": str(temp / "xdg-config"),
           "XDG_STATE_HOME": str(temp / "xdg-state"), "XDG_CACHE_HOME": str(temp / "xdg-cache")}
    for key in ("HOME", "TMPDIR", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"):
        Path(env[key]).mkdir()
    binary, db_path = temp / "hjh", temp / "history.sqlite3"
    command(["go", "build", "-o", binary, "./cmd/hjh"], cwd=ROOT, timeout=300,
            env={**build_env, **env, "GOMODCACHE": caches[0], "GOCACHE": caches[1]})
    # Real default installation, but never into the repository's common .hjh.
    config_path = config_dir / "config.json"
    command([binary, "roles", "--workspace", workspace], env=env, cwd=workspace)
    shutil.copytree(workspace / ".hjh/extensions", config_dir)
    config = json.loads(config_path.read_text())
    role_guideline_bytes = {name: sum(len("\n".join(tool.get("promptGuidelines", [])).encode())
                                    for tool in config["tools"] if tool["name"] in role["tools"])
                            for name, role in config["roles"].items()}
    policy_path = workspace / ".hjh/instructions/tool-usage.md"
    write(policy_path, POLICIES[0])
    write(Path(env["XDG_CONFIG_HOME"]) / "hjh/instructions/tool-usage.md", "S4_USER_POLICY_SHOULD_NOT_WIN\n")
    write(config_dir / ".hjh/instructions/tool-usage.md", "S4_CONFIG_DIRECTORY_IS_NOT_WORKSPACE\n")
    read_path = config_dir / next(tool["helpFile"] for tool in config["tools"] if tool["name"] == "read_file")
    read_help = [read_path.read_text() + "\n" + marker + "\n" for marker in READ_MARKERS]
    write(read_path, read_help[0])
    js_path = config_dir / config["runJSHelpFile"]
    js_help = js_path.read_text() + "\n" + JS_MARKER + "\n"
    write(js_path, js_help)
    dormant_path = config_dir / next(tool["helpFile"] for tool in config["tools"] if tool["name"] == "write_file")
    write(dormant_path, dormant_path.read_text() + "\n" + DORMANT + "\n")
    scenario = Scenario(workspace, read_help, js_help)
    server = ThreadingHTTPServer(("127.0.0.1", 0), Provider)
    server.scenario = scenario
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    endpoint = f"http://127.0.0.1:{server.server_port}"
    # Only a local provider exists. HTTPS metadata is not enabled. Proxies catch
    # unexpected external HTTP(S), rather than silently using live services.
    env.update({"HTTP_PROXY": endpoint, "HTTPS_PROXY": endpoint, "ALL_PROXY": endpoint,
                "NO_PROXY": "127.0.0.1,localhost"})
    config["credentials"] = {}
    config["model"] = {"provider": "s4-local", "api": "chat-completions", "modelId": "s4-local-model", "effort": "auto"}
    config["providers"] = {"s4-local": {"api": "chat-completions", "baseURL": endpoint,
                            "path": "/chat/completions", "auth": {"kind": "none"},
                            "defaultModel": "s4-local-model", "models": [{"id": "s4-local-model", "contextTokens": 100000}]}}
    config["roles"]["default"]["tools"] = ["run_js", "read_file", "write_file"]
    write(config_path, encoded(config))
    core = None
    log_path = temp / "core.stderr"
    try:
        with log_path.open("w") as log:
            core = subprocess.Popen([str(binary), "serve", "--workspace", str(workspace), "--config", str(config_path),
                                     "--db", str(db_path), "--listen", "127.0.0.1:0", "--max-steps", "8"],
                                    env=env, cwd=workspace, stdout=log, stderr=log)
            deadline, connected = time.monotonic() + 20, None
            while time.monotonic() < deadline:
                match = re.search(r"Core API listening at (http://127\.0\.0\.1:\d+/api/v2)", log_path.read_text())
                if match:
                    # --connect takes the Core origin; apiclient adds /api/v2.
                    connected = match.group(1).removesuffix("/api/v2")
                    break
                require(core.poll() is None, "temporary Core failed: " + log_path.read_text())
                time.sleep(0.05)
            require(connected is not None, "temporary Core startup timed out: " + log_path.read_text())
            registrations = list((Path(env["XDG_STATE_HOME"]) / "hjh/cores").glob("*.json"))
            require(len(registrations) == 1, "temporary Core catalog registration missing")
            registration = json.loads(registrations[0].read_text())
            require(registration["pid"] == core.pid and registration["dbPath"] == str(db_path),
                    "Core catalog does not identify our temporary process/database")
            first = command([binary, "run", "--connect", connected, FIRST], env=env, cwd=workspace)
            require(first.stdout.strip() == "S4 initial: help retrieved and documented parallel reads completed.", first.stdout)
            first_id = re.search(r"^Execution: (.+)$", first.stderr, re.M).group(1)
            with sqlite3.connect(db_path.as_uri() + "?mode=ro", uri=True) as db:
                session = db.execute("SELECT session_id FROM sessions").fetchone()[0]
            write(policy_path, POLICIES[1])
            write(read_path, read_help[1])
            second = command([binary, "run", "--connect", connected, "--session", session, RESUME], env=env, cwd=workspace)
            require(second.stdout.strip() == "S4 resumed: current guidance and edited read_file help verified.", second.stdout)
            second_id = re.search(r"^Execution: (.+)$", second.stderr, re.M).group(1)
            require(not scenario.errors, scenario.errors)
            require(scenario.steps == {FIRST: 4, RESUME: 2}, f"unexpected requests: {scenario.steps}")
            history = json.loads(command([binary, "history", "--db", db_path, "--workspace", workspace,
                                          "--session", session, "--json"], env=env, cwd=workspace).stdout)
            stored = encoded(history)
            help_messages = [encoded(message["json"]) for message in history
                             if message["json"].get("role") == "tool"]
            require(all(any(marker in message for message in help_messages)
                        for marker in [*READ_MARKERS, JS_MARKER]),
                    "help missing from normal tool messages in read-only CLI history")
            require(DORMANT not in stored, "unrequested help in history")
            summaries = inspect_db(db_path, scenario, policy_path, [first_id, second_id])
            return {"status": "passed", "scope": "real CLI/Core/SQLite; local deterministic chat-completions only",
                    "realProviderTested": False, "childLifecycle": "delegated to focused app tests",
                    "binarySHA256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                    "session": session, "executions": summaries, "helpCalls": 3,
                    "helpResultBytes": [len(encoded({"name": name, "available": True, "text": text}).encode())
                                        for name, text in [("read_file", read_help[0]), ("run_js", js_help), ("read_file", read_help[1])]],
                    "canonicalHistoryBytes": len(stored.encode()), "executedFencedCode": scenario.code,
                    "shippedRoleExternalToolGuidelineBytes": role_guideline_bytes,
                    "roleMetricNote": "source promptGuidelines only; excludes builtin run_js and composition prefixes",
                    "checks": ["workspace policy and source provenance", "help-only schemas retain normal required args",
                               "read_file help without path", "run_js help without code", "returned parallel-read example",
                               "edited policy/help on resume with prior help history", "latest exact wire per execution",
                               "help persisted as messages, not resource revisions", "unrequested help not archived"]}
    except Exception as error:
        raise RuntimeError(f"{error}\nProvider findings: {scenario.errors}\nCore log:\n{log_path.read_text() if log_path.exists() else ''}") from error
    finally:
        # Signal only the Popen child we created; never discover/stop a real Core.
        if core is not None and core.poll() is None:
            core.send_signal(signal.SIGTERM)
            try:
                core.wait(timeout=20)
            except subprocess.TimeoutExpired:
                core.kill()
                core.wait()
        server.shutdown()
        server.server_close()
        thread.join()


if __name__ == "__main__":
    with tempfile.TemporaryDirectory(prefix="hjh-tool-help-s4-") as directory:
        print(json.dumps(verify(Path(directory)), ensure_ascii=False, indent=2))
