#!/usr/bin/env python3
"""S-3 operation verifier: isolated Core + connected CLI + PTY TUI + SQLite.

Run ONLY after S-1/S-2 are complete. The first acceptance run passed on 2026-10-15;
see docs/results/usage-storage.md. Example (candidate hjh-tui must accompany candidate hjh):
  python3 scripts/verify-usage-storage.py --binary /path/to/hjh \
      --tui-binary /path/to/hjh-tui --output-dir /path/to/evidence
Without --binary, builds both commands with cached Go dependencies (downloads off).
--output-dir retains a fresh child directory, including DB, isolated HOME, config,
HTTP requests/responses, CLI output, raw PTY transcript, Core logs and report.json,
on BOTH success and failure. Without it all artifacts are temporary.

Uses only a signature-free loopback chat-completions provider and synthetic usage
in the observed prompt/completion/cache/reasoning shape. No live provider, common
DB/config/Core, installation, commit, migration, checkpoint or VACUUM is used.
Only Popen children created here receive signals; TUI exits with detach (Ctrl-D),
never /quit (which would stop Core). POSIX PTY and Python standard library required.

This is functional verification, NOT the plan's before/after capacity/I/O replay,
Responses coverage, live Go Observer/raw ModelResult verification or a full gate.
Those remain separate evidence; a passed report never claims them.
"""

import argparse
from collections import Counter
import errno
import fcntl
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import pty
import re
import select
import shutil
import signal
import sqlite3
import struct
import subprocess
import tempfile
import termios
import threading
import time
from urllib.request import build_opener, ProxyHandler


ROOT = Path(__file__).resolve().parent.parent
FIRST = "USAGE_S3_PARENT_INITIAL"
CHILD = "USAGE_S3_CHILD_TASK"
RESUME = "USAGE_S3_RESUME_AFTER_CORE_REOPEN"
TUI_TASK = "USAGE_S3_TUI_TURN"
READ_TEXT = "USAGE_S3_INDEPENDENT_FILE_RESULT\n"
PARENT_FINAL = "USAGE_S3_PARENT_FINAL"
CHILD_FINAL = "USAGE_S3_CHILD_FINAL"
RESUME_FINAL = "USAGE_S3_RESUMED_FINAL"
TUI_FINAL = "USAGE_S3_TUI_FINAL"
SUMMARY = "USAGE_S3_HOOK_MODEL"
ATTRIBUTION_MARKER = "USAGE_S3_PROVIDER_ATTRIBUTION_MUST_NOT_BE_STORED"
ESTIMATOR = "wire-utf8-bytes/3+framing-estimate"
UNVERIFIED = [
    "Live external providers and Responses protocol (local chat-completions only)",
    "Go Progress Observer and direct ModelResult raw usage (requires focused Go tests)",
    "Before/after byte, allocation, process I/O and performance replay measurements",
    "Authoritative full Go gate and independent review",
]


def require(condition, detail):
    if not condition:
        raise AssertionError(detail)


def encoded(value):
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"))


def write(path, text):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")


def save_json(path, value):
    write(path, json.dumps(value, ensure_ascii=False, indent=2) + "\n")


def stop_child(process):
    # Never discover another PID or invoke Core shutdown/catalog operations.
    if process is not None and process.poll() is None:
        process.send_signal(signal.SIGTERM)
        try:
            process.wait(timeout=20)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()


def command(args, env, cwd, evidence, timeout=120):
    result = subprocess.run([str(arg) for arg in args], env=env, cwd=cwd,
                            capture_output=True, text=True, timeout=timeout)
    write(evidence.with_suffix(".stdout"), result.stdout)
    write(evidence.with_suffix(".stderr"), result.stderr)
    save_json(evidence.with_suffix(".command.json"),
              {"args": [str(arg) for arg in args], "exitCode": result.returncode})
    require(result.returncode == 0,
            f"{args[:2]} exited {result.returncode}; see {evidence}.stderr\n{result.stderr}")
    return result


def tool_result(wire, call_id):
    matches = [m for m in wire["messages"]
               if m.get("role") == "tool" and m.get("tool_call_id") == call_id]
    require(len(matches) == 1, f"missing/duplicate tool result {call_id}")
    return json.loads(matches[0]["content"])


def js_call(call_id, code):
    return {"tool_calls": [{"index": 0, "id": call_id, "type": "function",
                             "function": {"name": "run_js", "arguments": encoded({"code": code})}}]}


class Scenario:
    def __init__(self):
        self.lock = threading.Lock()
        self.requests, self.errors, self.successes = [], [], []
        self.steps = Counter()
        self.child_id = None

    def next(self, wire):
        require(wire["model"] == "s3-local-model" and wire["stream"], "wrong model/stream")
        systems = "\n".join(m.get("content", "") for m in wire["messages"] if m["role"] == "system")
        if SUMMARY in systems:
            number = int([m["content"] for m in wire["messages"] if m["role"] == "user"][-1])
            require(number in (1, 2), "unexpected hook model input")
            require(not wire.get("tools"), "hook model received normal tools")
            return f"hook-{number}", 200, {"content": f"SUMMARY_RESULT_{number}"}
        task = [m["content"] for m in wire["messages"] if m["role"] == "user"][-1]
        require(task in (FIRST, CHILD, RESUME, TUI_TASK), f"unexpected task: {task}")
        self.steps[task] += 1
        step = self.steps[task]
        if task == FIRST:
            if step <= 2:
                require("USAGE_S3_RAW_HOOK_USAGE_VERIFIED" in systems, "hook did not verify full raw usage")
            if step == 1:
                return "retry-failed", 500, {"error": {"message": "S3 intentional one-shot HTTP 500"}}
            if step == 2:
                return "parent-work", 200, js_call("s3-work", '''// Spawn the child, then read independent source in the parent.
const child = await tools.spawn_agent({task:"USAGE_S3_CHILD_TASK",tools:[]});
const source = await tools.read_file({path:"input.txt"});
return {child,source};''')
            work = tool_result(wire, "s3-work")
            require(work["source"]["text"] == READ_TEXT, f"read_file result lost: {work}")
            self.child_id = work["child"]["executionId"]
            if step == 3:
                return "parent-collect", 200, js_call("s3-collect", f'''// Collect the independently started child when its answer is needed.
return await tools.collect_agent({{executionId:{encoded(self.child_id)}}});''')
            require(step == 4, f"unexpected parent step {step}")
            collected = tool_result(wire, "s3-collect")
            require(collected["executionId"] == self.child_id and collected["outcome"] == "completed"
                    and collected["finalText"] == CHILD_FINAL, f"child result not collected: {collected}")
            return "parent-final", 200, {"content": PARENT_FINAL}
        require(step == 1, f"unexpected additional {task} request")
        if task in (RESUME, TUI_TASK):
            # Require actual persisted tool Messages, not merely the parent answer.
            require(tool_result(wire, "s3-work")["source"]["text"] == READ_TEXT,
                    "reopened Session lost tool Message")
            require(tool_result(wire, "s3-collect")["finalText"] == CHILD_FINAL,
                    "reopened Session lost collected child Message")
            require(any(m.get("role") == "assistant" and m.get("content") == PARENT_FINAL
                        for m in wire["messages"]), "reopened Session lost parent final answer")
            if task == TUI_TASK:
                require(any(m.get("content") == RESUME_FINAL for m in wire["messages"]),
                        "TUI continuation lost CLI resumed answer")
            return ("resume", 200, {"content": RESUME_FINAL}) if task == RESUME else (
                "tui", 200, {"content": TUI_FINAL})
        return "child", 200, {"content": CHILD_FINAL}


class Provider(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_CONNECT(self):
        self.server.scenario.errors.append("unexpected external HTTPS destination")
        self.send_error(502, "local provider only")

    def do_GET(self):
        self.server.scenario.errors.append("unexpected provider GET")
        self.send_error(502, "local provider only")

    def do_POST(self):
        scenario = self.server.scenario
        try:
            require(self.path == "/chat/completions", f"unexpected provider path {self.path}")
            require(not self.headers.get("Authorization"), "local provider must be signature-free")
            wire = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            with scenario.lock:
                entry = {"wire": wire, "receivedMonotonic": time.monotonic()}
                scenario.requests.append(entry)
                label, status, delta = scenario.next(wire)
                entry.update({"label": label, "status": status})
                if status == 200:
                    index = len(scenario.successes) + 1
                    # Intentionally far from hjh estimates to detect provider-token substitution.
                    usage = {"prompt_tokens": 700000 + index, "completion_tokens": 100 + index,
                             "total_tokens": 700100 + 2 * index,
                             "prompt_tokens_details": {"cached_tokens": 0},
                             "completion_tokens_details": {"reasoning_tokens": 7},
                             "attribution": {"items": [{"detail": ATTRIBUTION_MARKER}]}}
                    entry["usage"] = usage
                    scenario.successes.append(entry)
                    delta["reasoning_content"] = "USAGE_S3_THINKING_" + label
                    event = {"id": "s3-" + label, "model": "s3-local-model", "usage": usage,
                             "choices": [{"index": 0, "delta": delta,
                                          "finish_reason": "tool_calls" if "tool_calls" in delta else "stop"}]}
                    body = ("data: " + encoded(event) + "\n\ndata: [DONE]\n\n").encode()
                    entry["event"] = event
                else:
                    body = encoded(delta).encode()
            self.send_response(status)
            self.send_header("Content-Type", "text/event-stream" if status == 200 else "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        except Exception as error:
            scenario.errors.append(str(error))
            self.send_error(400, str(error))


HOOK = '''export async function projectRequest({runtime,modelStep},api) {
  if(runtime.parentExecutionId || runtime.turnNumber!==1 || modelStep!==1) return;
  for(const n of [1,2]) {
    const r=await api.model.generate({instruction:"USAGE_S3_HOOK_MODEL",
      messages:[{role:"user",content:{kind:"text",text:String(n)}}]});
    if(r.text!=="SUMMARY_RESULT_"+n || r.finishReason!=="stop" ||
       !r.usage.attribution.items[0].detail.length ||
       r.usage.prompt_tokens!==700000+n || r.usage.completion_tokens!==100+n ||
       r.usage.total_tokens!==700100+2*n || r.usage.prompt_tokens_details.cached_tokens!==0 ||
       r.usage.completion_tokens_details.reasoning_tokens!==7)
      throw new Error("complete hook model result unavailable");
  }
  const view=await api.context.get();
  await api.context.replace({messages:view.messages,
    instruction:view.instruction+"\\nUSAGE_S3_RAW_HOOK_USAGE_VERIFIED"});
}
'''


class Core:
    def __init__(self, binary, workspace, config, db, env, artifacts):
        self.args = [str(binary), "serve", "--workspace", str(workspace), "--config", str(config),
                     "--db", str(db), "--listen", "127.0.0.1:0", "--max-steps", "8"]
        self.workspace, self.env, self.artifacts = workspace, env, artifacts
        self.process, self.generation, self.pids = None, 0, []

    def start(self):
        self.generation += 1
        log_path = self.artifacts / f"core-{self.generation}.log"
        with log_path.open("w") as log:
            self.process = subprocess.Popen(self.args, env=self.env, cwd=self.workspace, stdout=log, stderr=log)
        self.pids.append(self.process.pid)
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            text = log_path.read_text()
            match = re.search(r"Core API listening at (http://127\.0\.0\.1:\d+)/api/v2", text)
            require(self.process.poll() is None, f"temporary Core failed; see {log_path}\n{text}")
            if match:
                self.endpoint = match.group(1)
                return
            time.sleep(0.05)
        raise AssertionError(f"temporary Core startup timeout; see {log_path}")

    def stop(self):
        stop_child(self.process)

    def get(self, path, name):
        # Do not use inherited system proxies to read the explicitly created Core.
        opener = build_opener(ProxyHandler({}))
        with opener.open(self.endpoint + "/api/v2/" + path, timeout=20) as response:
            value = json.load(response)
        save_json(self.artifacts / (name + ".json"), value)
        return value


def plain_pty(data):
    text = data.decode("utf-8", errors="replace")
    text = re.sub(r"\x1b\][^\x07]*(?:\x07|\x1b\\)", "", text)
    return re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", text).replace("\r", "")


def verify_tui(binary, core, session, env, workspace, artifacts):
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 90, 240, 0, 0))
    process, captured = None, bytearray()

    def wait_for(predicate, description, timeout=35):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if select.select([master], [], [], 0.1)[0]:
                try:
                    block = os.read(master, 65536)
                except OSError as error:
                    if error.errno == errno.EIO:
                        block = b""
                    else:
                        raise
                captured.extend(block)
                # Reply to terminal cursor queries; this is a PTY, not an emulator.
                if b"\x1b[6n" in block:
                    os.write(master, b"\x1b[1;1R")
            text = plain_pty(captured)
            if predicate(text):
                return text
            require(process.poll() is None, f"PTY TUI exited before {description}: {text[-4000:]}")
        raise AssertionError(f"PTY timeout waiting for {description}; see tui.raw/tui.txt")

    try:
        process = subprocess.Popen([str(binary), "--endpoint", core.endpoint, "--workspace", str(workspace),
                                    "--session", session], stdin=slave, stdout=slave, stderr=slave,
                                   env={**env, "TERM": "xterm-256color"}, cwd=workspace, start_new_session=True)
        os.close(slave)
        slave = None
        wait_for(lambda text: "ctx " in text, "restored footer context")
        os.write(master, TUI_TASK.encode() + b"\r")
        wait_for(lambda text: TUI_FINAL in text, "TUI model turn answer")
        # A text delta can precede adoption; wait for the Core's saved turn, not
        # an arbitrary sleep or just the answer substring in a streaming frame.
        identity = core.get("core", "tui-core-identity")
        deadline = time.monotonic() + 20
        while True:
            state = core.get("sessions/" + session + "/context?coreId=" + identity["coreId"], "tui-adoption")
            if (state["canonical"]["turnCount"] == 3 and not state.get("activeRunId")
                    and state["latestRootExecution"]["lifecycle"] == "settled"):
                break
            require(time.monotonic() < deadline, "TUI turn not adopted by Core")
            time.sleep(0.05)
        state = core.get("sessions/" + session + "/context?coreId=" + core.get("core", "core-identity")["coreId"],
                         "tui-session-context")
        budget = state["latestBudget"]
        summary = budget["summary"]
        require(summary["estimator"] == ESTIMATOR, "TUI budget not hjh estimator")
        fact = budget["fact"]
        require(fact["estimatedTokens"] == summary["estimatedTokens"] and
                fact["wireBytes"] == summary["wireBytes"] and fact["estimator"] == ESTIMATOR,
                "TUI saved fact and scalar estimate differ")
        require(summary["estimatedTokens"] == (summary["wireBytes"] + 2) // 3 + 16 +
                8 * summary["selectedMessageCount"], "saved estimate differs from hjh framing formula")
        estimated, limit = summary["estimatedTokens"], summary["budget"]["limit"]
        require(estimated < 700000, "TUI estimate replaced by provider usage")
        expected_footer = f"ctx {estimated * 100 // limit}%" + (
            "*" if summary["budget"].get("limitSource") == "hook" else "")
        # Inspect only subsequent output, not a footer left over from the CLI turn.
        start = len(captured)
        os.write(master, b"/context\r")
        expected_detail = f"Estimated input · {estimated} tokens"
        wait_for(lambda _: expected_detail in plain_pty(captured[start:]) and
                 expected_footer in plain_pty(captured[start:]), "matching /context and ctx footer")
        require(budget["executionId"] == state["latestRootExecution"]["executionId"],
                "summary/child call overwrote root context")
        os.write(master, b"\x04")  # detach only, never /quit
        wait_for(lambda _: process.poll() is not None, "TUI detach", timeout=20)
        require(process.returncode == 0, f"TUI detach exited {process.returncode}")
        require(core.process.poll() is None, "TUI detach stopped Core")
        return {"footer": expected_footer, "detail": expected_detail, "budget": budget}
    finally:
        stop_child(process)
        (artifacts / "tui.raw").write_bytes(captured)
        write(artifacts / "tui.txt", plain_pty(captured))
        os.close(master)
        if slave is not None:
            os.close(slave)


def walk_usage(value):
    if isinstance(value, dict):
        if "prompt_tokens" in value:
            yield value
        for child in value.values():
            yield from walk_usage(child)
    elif isinstance(value, list):
        for child in value:
            yield from walk_usage(child)


def inspect_storage(db_path, scenario, parent, resumed, artifacts):
    with sqlite3.connect(db_path.as_uri() + "?mode=ro", uri=True) as db:
        db.execute("PRAGMA query_only=ON")
        db.row_factory = sqlite3.Row
        executions = [dict(row) for row in db.execute(
            "SELECT execution_id,parent_execution_id,lifecycle,outcome,adoption FROM executions")]
        children = [row for row in executions if row["parent_execution_id"]]
        require(all(row["adoption"] == "canonical" for row in executions if not row["parent_execution_id"]),
                "root turns were not canonically adopted")
        require(len(executions) == 4 and len(children) == 1, f"unexpected executions: {executions}")
        require(children[0]["execution_id"] == scenario.child_id and children[0]["parent_execution_id"] == parent,
                "child/parent execution linkage differs")
        require(all(row["lifecycle"] == "settled" and row["outcome"] == "completed" for row in executions),
                f"unsettled/failed executions: {executions}")
        require(db.execute("SELECT turn_count FROM sessions").fetchall()[0][0] == 3, "expected three adopted turns")
        records = []
        for row in db.execute("SELECT r.record_id,r.execution_id,r.kind,r.payload_json,c.content_bytes "
                              "FROM semantic_records r LEFT JOIN contents c USING(content_digest) ORDER BY r.execution_id,r.ordinal"):
            item = dict(row)
            item["payload"] = json.loads(item.pop("payload_json"))
            content = item.pop("content_bytes")
            item["content"] = bytes(content).decode() if content is not None else None
            records.append(item)
        by_id = {row["record_id"]: row for row in records}
        relations = [dict(row) for row in db.execute("SELECT * FROM semantic_relations")]
        admissions = {r["record_id"] for r in records if r["execution_id"] == scenario.child_id
                      and r["kind"] == "execution_admission"}
        require(any(r["record_id"] in admissions and r["relation"] == "parent_call" and r["resolved"]
                    and by_id[r["target_record_id"]]["execution_id"] == parent for r in relations),
                "child admission lost parent spawn relation")

        def target(record, kind):
            found = [r for r in relations if r["record_id"] == record["record_id"] and r["relation"] == kind]
            require(len(found) == 1 and found[0]["resolved"] == 1, f"missing {kind}: {record}")
            result = by_id[found[0]["target_record_id"]]
            require(result["execution_id"] == record["execution_id"], "relation crossed executions")
            return result

        usages = [r for r in records if r["payload"].get("kind") == "usage"]
        results = [r for r in records if r["payload"].get("kind") == "model_result"]
        require(len(usages) == len(results) == 6, "normal success usage/model_result count differs")
        normal_expected = [entry for entry in scenario.successes if not entry["label"].startswith("hook-")]
        expected_by_tokens = {entry["usage"]["prompt_tokens"]: entry for entry in normal_expected}
        for usage in usages:
            p = usage["payload"]
            require(usage["kind"] == "model_result" and "attribution" not in p["result"],
                    "normal usage storage kind/projection differs")
            require(p.get("parentExecutionId", "") == (parent if usage["execution_id"] == scenario.child_id else ""),
                    "usage observation lost parent execution attribution")
            expected = expected_by_tokens[p["result"]["prompt_tokens"]]
            require(p["result"] == {k: v for k, v in expected["usage"].items() if k != "attribution"},
                    "provider scalar/detail values were changed")
            start = target(usage, "parent_call")
            require(start["payload"]["kind"] == "model_request" and start["payload"]["id"] == p["parent"],
                    "usage no longer resolves to physical request start")
            matching = [r for r in results if r["execution_id"] == usage["execution_id"]
                        and r["payload"].get("parent") == p["parent"]]
            require(len(matching) == 1, "usage/physical model_result association differs")
            result = matching[0]
            require("usage" not in result["payload"]["result"], "model_result duplicated usage")
            require(target(result, "parent_call")["record_id"] == start["record_id"], "parent_call differs")
            identity = result["payload"]["request"]
            require(identity["executionId"] == usage["execution_id"] and identity["lane"] == "parent"
                    and identity["physicalRequestId"] == p["parent"]
                    and identity["modelStep"] == p["modelStep"]
                    and identity.get("retryAttempt", 0) == p.get("retryAttempt", 0), "request identity lost")

        retries = [r for r in records if r["payload"].get("kind") == "model_retry"]
        require(len(retries) == 1, "expected one retry/superseded record")
        retry = retries[0]
        require(retry["execution_id"] == parent and retry["payload"]["phase"] == "superseded"
                and retry["payload"]["diagnostic"]["httpStatus"] == 500, "retry diagnostic lost")
        failed_start = target(retry, "parent_call")
        retried = [u for u in usages if u["execution_id"] == parent
                   and u["payload"].get("retryAttempt") == 1]
        require(len(retried) == 1 and retried[0]["payload"]["modelStep"] == retry["payload"]["modelStep"]
                and retried[0]["payload"]["parent"] != failed_start["payload"]["id"],
                "retry did not preserve logical step and replace physical ID")
        require(not any(u["execution_id"] == parent and u["payload"]["parent"] == failed_start["payload"]["id"]
                        for u in usages), "failed attempt fabricated usage")

        hosts = [r for r in records if r["payload"].get("name") == "context.model.generate"
                 and r["payload"].get("resultRef")]
        require(len(hosts) == 2 and len({h["payload"]["id"] for h in hosts}) == 2,
                "multiple model.generate Host calls not saved separately")
        hook_parents = set()
        for host in hosts:
            p = host["payload"]
            require(host["kind"] == "effect_observation" and host["execution_id"] == parent,
                    "summary created normal request/execution")
            value = json.loads(host["content"])
            expected = next(e["usage"] for e in scenario.successes
                            if e["usage"]["prompt_tokens"] == value["usage"]["prompt_tokens"])
            require(value["usage"] == {k: v for k, v in expected.items() if k != "attribution"}
                    and value["outputBytes"] > 0 and value["finishReason"] == "stop"
                    and p["phase"] == "completed", "Host metadata differs")
            start = target(host, "call_start")
            require(start["payload"]["id"] == p["id"], "Host call_start ID differs")
            hook = target(host, "parent_call")
            require(hook["payload"]["kind"] == "hook" and hook["payload"]["name"] == "projectRequest",
                    "Host parent is not projectRequest")
            hook_parents.add(hook["record_id"])
        require(len(hook_parents) == 1, "two Host calls were not in the same hook")
        for usage in usages:
            expected_label = expected_by_tokens[usage["payload"]["result"]["prompt_tokens"]]["label"]
            if expected_label == "child":
                require(usage["execution_id"] == scenario.child_id, "child usage assigned to root")
            elif expected_label.startswith("parent-"):
                require(usage["execution_id"] == parent, "parent usage assigned to child")

        # Count actual stored JSON, not public expanded payloads (which rehydrate resultRef).
        stored_values = [r["payload"] for r in records]
        for row in db.execute("SELECT content_bytes FROM contents"):
            text = bytes(row[0]).decode()
            require(ATTRIBUTION_MARKER not in text, "provider attribution leaked into contents")
            try:
                stored_values.append(json.loads(text))
            except json.JSONDecodeError:
                pass
        require(not any(ATTRIBUTION_MARKER in encoded(r["payload"]) for r in records),
                "provider attribution leaked into semantic payload")
        counts = Counter(u["prompt_tokens"] for value in stored_values for u in walk_usage(value))
        require(counts == Counter({e["usage"]["prompt_tokens"]: 1 for e in scenario.successes}),
                f"usage not stored exactly once per successful HTTP response: {counts}")
        require(any("USAGE_S3_THINKING_parent-final" in encoded(r) for r in records), "thinking not persisted")
        latest = {}
        for row in db.execute("SELECT execution_id,wire_body FROM execution_latest_requests"):
            wire = json.loads(row[1])
            task = [m["content"] for m in wire["messages"] if m["role"] == "user"][-1]
            expected = next(e["wire"] for e in reversed(scenario.requests)
                            if e.get("label") in ("parent-final", "child", "resume", "tui")
                            and [m["content"] for m in e["wire"]["messages"] if m["role"] == "user"][-1] == task)
            require(wire == expected, "Host request overwrote latest normal wire")
            latest[row[0]] = task
        require(latest[parent] == FIRST and latest[resumed] == RESUME, "latest root wire identification differs")
        require(db.execute("PRAGMA integrity_check").fetchone()[0] == "ok", "verification DB integrity failed")
        require(not db.execute("PRAGMA foreign_key_check").fetchall(), "verification DB foreign key failure")
        save_json(artifacts / "stored-records.json", records)
        save_json(artifacts / "stored-relations.json", relations)
        return {"executions": executions, "normalUsageRecords": len(usages), "hostUsageRecords": len(hosts),
                "usageOccurrences": dict(counts), "retryRecord": retry["record_id"], "latestRequests": latest}


def verify(directory, args, report):
    workspace, config_dir, artifacts = directory / "workspace", directory / "config", directory / "evidence"
    workspace.mkdir()
    artifacts.mkdir()
    env = {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "LANG": "C.UTF-8"}
    for name in ("HOME", "TMPDIR", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"):
        path = directory / name.lower()
        path.mkdir()
        env[name] = str(path)
    if args.binary:
        binary = Path(args.binary).resolve()
        tui_binary = Path(args.tui_binary).resolve() if args.tui_binary else binary.with_name("hjh-tui")
        require(binary.is_file() and tui_binary.is_file(), "candidate hjh and hjh-tui binaries are required")
    else:
        require(not args.tui_binary, "--tui-binary requires --binary (avoid mixed build candidates)")
        binary, tui_binary = directory / "hjh", directory / "hjh-tui"
        build_env = {**os.environ, "GOPROXY": "off", "GOSUMDB": "off"}
        cache = command(["go", "env", "GOMODCACHE", "GOCACHE"], build_env, ROOT, artifacts / "go-env").stdout.splitlines()
        build_env.update(env)
        build_env.update({"GOMODCACHE": cache[0], "GOCACHE": cache[1]})
        for target, package in ((binary, "./cmd/hjh"), (tui_binary, "./cmd/hjh-tui")):
            command(["go", "build", "-o", target, package], build_env, ROOT, artifacts / (target.name + "-build"), 300)
    report["binarySHA256"] = hashlib.sha256(binary.read_bytes()).hexdigest()
    report["tuiBinarySHA256"] = hashlib.sha256(tui_binary.read_bytes()).hexdigest()
    # Install only the candidate's own defaults into this disposable workspace.
    command([binary, "roles", "--workspace", workspace], env, workspace, artifacts / "install-defaults")
    shutil.copytree(workspace / ".hjh/extensions", config_dir)
    config_path, db_path = config_dir / "config.json", directory / "history.sqlite3"
    config = json.loads(config_path.read_text())
    config["credentials"] = {}
    config["roles"] = {"default": {"revision": "s3", "instruction": "Execute the local verification task.",
                                    "tools": ["run_js", "read_file", "spawn_agent", "collect_agent"]}}
    config["defaultRole"] = "default"
    config["model"] = {"provider": "s3-local", "api": "chat-completions", "modelId": "s3-local-model", "effort": "auto"}
    config["hooks"] = "hooks.js"
    write(config_dir / "hooks.js", HOOK)
    write(workspace / "input.txt", READ_TEXT)
    scenario = Scenario()
    server = ThreadingHTTPServer(("127.0.0.1", 0), Provider)
    server.scenario = scenario
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    endpoint = f"http://127.0.0.1:{server.server_port}"
    env.update({"HTTP_PROXY": endpoint, "HTTPS_PROXY": endpoint, "ALL_PROXY": endpoint,
                "http_proxy": endpoint, "https_proxy": endpoint, "all_proxy": endpoint,
                "NO_PROXY": "127.0.0.1,localhost", "no_proxy": "127.0.0.1,localhost"})
    config["providers"] = {"s3-local": {"api": "chat-completions", "baseURL": endpoint,
                          "path": "/chat/completions", "auth": {"kind": "none"},
                          "adapter": {"reasoningField": "reasoning_content"},
                          "defaultModel": "s3-local-model", "models": [{"id": "s3-local-model", "contextTokens": 100000}]}}
    write(config_path, encoded(config))
    core = Core(binary, workspace, config_path, db_path, env, artifacts)

    def run(task, name, session=None):
        argv = [binary, "run", "--connect", core.endpoint]
        if session:
            argv += ["--session", session]
        result = command([*argv, task], env, workspace, artifacts / name)
        match = re.search(r"^Execution: (.+)$", result.stderr, re.M)
        require(match is not None, "CLI did not report execution ID")
        return result, match.group(1)

    try:
        core.start()
        first, parent = run(FIRST, "cli-first")
        require(first.stdout.strip() == PARENT_FINAL, first.stdout)
        with sqlite3.connect(db_path.as_uri() + "?mode=ro", uri=True) as db:
            sessions = db.execute("SELECT session_id FROM sessions").fetchall()
        require(len(sessions) == 1, f"unexpected Sessions: {sessions}")
        session = sessions[0][0]
        report.update({"session": session, "parentExecution": parent})
        before = core.get("executions/" + parent, "inspect-before-restart")
        require(CHILD_FINAL in encoded(before), "Core Inspect lost child result")
        core.stop()
        core.start()  # new Popen, same isolated DB; no checkpoint or state copying
        require(core.pids[0] != core.pids[1], "Core was not reopened in another process")
        after = core.get("executions/" + parent, "inspect-after-restart")
        require(before == after, "Inspect differs after Core close/reopen")
        second, resumed = run(RESUME, "cli-resume", session)
        require(second.stdout.strip() == RESUME_FINAL, second.stdout)
        report["tui"] = verify_tui(tui_binary, core, session, env, workspace, artifacts)
        require(not scenario.errors, f"provider findings: {scenario.errors}")
        require(scenario.steps == Counter({FIRST: 4, CHILD: 1, RESUME: 1, TUI_TASK: 1}),
                f"unexpected request steps: {scenario.steps}")
        require(len(scenario.successes) == 8, "expected six normal plus two hook responses")
        failed = next(e for e in scenario.requests if e["label"] == "retry-failed")
        retried = next(e for e in scenario.requests if e["label"] == "parent-work")
        require(failed["wire"] == retried["wire"] and retried["receivedMonotonic"] - failed["receivedMonotonic"] >= 1.9,
                "existing retry did not resend same request after two-second delay")
        for view in ("session", "canonical", "detail"):
            result = command([binary, "history", "--connect", core.endpoint, "--session", session, "--view", view],
                             env, workspace, artifacts / ("history-" + view))
            require(all(marker in result.stdout for marker in (PARENT_FINAL, CHILD_FINAL, RESUME_FINAL, TUI_FINAL)),
                    f"{view} view lost saved answers")
            require(ATTRIBUTION_MARKER not in result.stdout, f"{view} leaked provider attribution")
            if view == "detail":
                detail_records = {line["identity"]: line["value"] for line in
                                  (json.loads(text) for text in result.stdout.splitlines()) if line["kind"] == "record"}
        for execution in (parent, scenario.child_id, resumed, report["tui"]["budget"]["executionId"]):
            inspection = core.get("executions/" + execution, "inspect-" + execution)
            require(ATTRIBUTION_MARKER not in encoded(inspection), "Inspect leaked provider attribution")
            for record in inspection["records"]:
                require(record["id"] in detail_records and record["payload"] == detail_records[record["id"]]["payload"],
                        "CLI detail and Core Inspect records differ")
                if (record["payload"].get("kind") == "usage" or
                        record["payload"].get("name") == "context.model.generate"):
                    single = core.get(f"executions/{execution}/records/{record['ordinal']}", "record-" + str(record["id"]))
                    require(single == record, "Core Record and Inspect disagree")
        report["storage"] = inspect_storage(db_path, scenario, parent, resumed, artifacts)
        report["corePIDs"] = core.pids
        report["checks"] = ["parent run_js/read_file + independent spawn/collect + final answer",
                            "two model.generate calls in one hook retain runtime raw usage",
                            "HTTP 500 existing retry preserves logical/physical identity and superseded detail",
                            "separate Core process resumes persisted tool/child/parent Messages in HTTP input",
                            "connected CLI session/canonical/detail and Core Inspect/Record agree",
                            "PTY TUI turn and /context use hjh estimate, not provider usage",
                            "usage stored once per response; no provider attribution; Host and parent relations"]
    finally:
        core.stop()
        server.shutdown()
        server.server_close()
        thread.join()
        save_json(artifacts / "provider-requests-responses.json", scenario.requests)
        save_json(artifacts / "provider-errors.json", scenario.errors)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--binary", help="candidate hjh executable (otherwise build both commands)")
    parser.add_argument("--tui-binary", help="candidate hjh-tui (default: beside --binary)")
    parser.add_argument("--output-dir", type=Path, help="retain artifacts in a fresh child of this directory")
    args = parser.parse_args()
    temporary = None
    if args.output_dir:
        args.output_dir.mkdir(parents=True, exist_ok=True)
        directory = Path(tempfile.mkdtemp(prefix="usage-storage-s3-", dir=args.output_dir.resolve()))
    else:
        temporary = tempfile.TemporaryDirectory(prefix="hjh-usage-storage-s3-")
        directory = Path(temporary.name)
    report = {"status": "failed", "realProviderTested": False, "protocol": "chat-completions",
              "artifacts": str(directory) if args.output_dir else "temporary (removed on exit)",
              "unverified": UNVERIFIED, "startedAt": time.strftime("%Y-%m-%dT%H:%M:%S%z")}
    exit_code = 1
    try:
        verify(directory, args, report)
        report["status"] = "passed"
        exit_code = 0
    except Exception as error:
        report["error"] = f"{type(error).__name__}: {error}"
        report["note"] = "Acceptance incomplete: only checks reached before this error ran; no success is inferred."
    finally:
        report["finishedAt"] = time.strftime("%Y-%m-%dT%H:%M:%S%z")
        save_json(directory / "report.json", report)
        print(json.dumps(report, ensure_ascii=False, indent=2))
        if temporary:
            temporary.cleanup()
    return exit_code


if __name__ == "__main__":
    raise SystemExit(main())
