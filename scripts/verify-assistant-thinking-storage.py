#!/usr/bin/env python3
"""S1/S2/S3 assistant storage acceptance through isolated CLI, Core, PTY and SQLite.

Run ONLY when the coordinating owner declares the S3 candidate ready:
  python3 scripts/verify-assistant-thinking-storage.py --binary /path/to/hjh \
    --tui-binary /path/to/hjh-tui \
    --output-dir tmp/assistant-provider-state-ycX6mbUm/acceptance
Without --binary builds both commands, dependencies offline. Every run retains a
fresh child of --output-dir (default above), including failure evidence and DB.

Isolation/PTY plumbing follows verify-usage-storage.py, copied here so that script
is not changed or executed. Only signature-free loopback Chat HTTP is used. The
reasoning_content delta/replay shape follows internal/app/chat_thinking_test.go
and the plan's observed Chat records; malformed-tool retry follows retry_test.go.
Synthetic text is NOT captured original SSE, Responses or live-provider evidence.
No common HOME/config/Core/DB, migration, VACUUM, checkpoint, cache/usage/compaction
changes, full gate, installation or PID discovery. Only our Popen children are
signalled. TUI exits using Ctrl-D detach, never /quit.
"""

import argparse
import base64
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
from urllib.request import build_opener, ProxyHandler, Request

ROOT = Path(__file__).resolve().parent.parent
PREFIX = "THINK_S4_"
FIRST, CHILD, RESUME, TUI_TASK, FAILED, CANCELLED, RECALL_FAIL, RECALL_CANCEL = (
    PREFIX + name for name in ("PARENT_INITIAL", "CHILD_TASK", "RESUME", "TUI_TASK",
                              "FAILED_TASK", "CANCEL_TASK", "RECALL_FAIL", "RECALL_CANCEL"))
PARENT_FINAL, CHILD_FINAL, RESUME_FINAL, TUI_FINAL = (
    PREFIX + name for name in ("PARENT_FINAL", "CHILD_FINAL", "RESUME_FINAL", "TUI_FINAL"))
READ_TEXT = PREFIX + "INDEPENDENT_SOURCE\n"
BAD_BODY, BAD_THINK = PREFIX + "SUPERSEDED_BODY", PREFIX + "SUPERSEDED_THINKING"
FAIL_BODY, CANCEL_BODY = PREFIX + "LAST_FAILED_BODY", PREFIX + "CANCELLED_BODY"
UNVERIFIED = [
    "Live external providers, common-environment restart and real-user session continuation",
    "Responses readable summary/items and opaque replay state (local observed Chat shape only)",
    "Before/after capacity, allocation, process I/O, performance and original captured SSE replay",
    "Result committed then Message append interrupted, length termination and state-free results (focused app tests, not this scenario)",
    "Direct Store inline-state entry points and Session deletion/shared-contents collection (focused history tests)",
    "Authoritative full Go gate and independent review",
]


def thinking(label):
    return PREFIX + "THINKING_" + label


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
        self.requests, self.errors = [], []
        self.steps = Counter()
        self.child_id = None
        self.recall_sources = {}
        self.cancel_release = threading.Event()
        self.tui_release = threading.Event()

    def next(self, wire):
        require(wire["model"] == "s4-local-model" and wire["stream"], "wrong model/stream")
        tasks = (FIRST, CHILD, RESUME, TUI_TASK, FAILED, CANCELLED, RECALL_FAIL, RECALL_CANCEL)
        task = next((m["content"] for m in reversed(wire["messages"])
                     if m.get("role") == "user" and m.get("content") in tasks), None)
        require(task is not None, "unrecognized task")
        self.steps[task] += 1
        step = self.steps[task]
        if task == FIRST:
            if step == 1:
                # Same invalid tool JSON already exercised by app/retry_test.go.
                return "retry-superseded", {"content": BAD_BODY, "tool_calls": [{
                    "index": 0, "id": "s4-invalid", "type": "function", "function": {
                        "name": "read_file", "arguments": '{"path":"input.txt"'}}]}
            if step == 2:
                return "parent-work", js_call("s4-work", '''// Start independent child work, then read the parent's source.
const child = await tools.spawn_agent({task:"THINK_S4_CHILD_TASK",tools:[]});
const source = await tools.read_file({path:"input.txt"});
return {child,source};''')
            require(BAD_BODY not in encoded(wire) and BAD_THINK not in encoded(wire),
                    "superseded response entered continued input")
            work = tool_result(wire, "s4-work")
            require(work["source"]["text"] == READ_TEXT, "parent source result changed")
            self.child_id = work["child"]["executionId"]
            if step == 3:
                return "parent-collect", js_call("s4-collect", f'''// Collect the independent child when its answer is needed.
return await tools.collect_agent({{executionId:{encoded(self.child_id)}}});''')
            collected = tool_result(wire, "s4-collect")
            require(collected["executionId"] == self.child_id and collected["outcome"] == "completed"
                    and collected["finalText"] == CHILD_FINAL, "child collection lost answer")
            if step == 4:
                return "parent-status", js_call("s4-status", f'''// Read saved reports and watermark after collection; do not poll completion.
return await tools.agent_status({{executionId:{encoded(self.child_id)}}});''')
            require(step == 5, f"unexpected parent step {step}")
            status = tool_result(wire, "s4-status")
            require(status["executionId"] == self.child_id and status["state"] == "completed"
                    and CHILD_FINAL in encoded(status["reports"]), "status lost child report")
            require("providerState" not in encoded(status), "status included child private state")
            return "parent-final", {"content": PARENT_FINAL}
        if task == FAILED:
            require(step <= 2, "failure exceeded existing one-retry budget")
            return "failure-" + str(step), {"content": PREFIX + "FIRST_FAILED_BODY" if step == 1 else FAIL_BODY,
                "tool_calls": [{"index": 0, "id": "s4-final-invalid", "type": "function",
                                "function": {"name": "read_file", "arguments": '{"path":"input.txt"'}}]}
        require(step == 1, f"unexpected additional request for {task}")
        if task in (RESUME, TUI_TASK):
            require(tool_result(wire, "s4-work")["source"]["text"] == READ_TEXT,
                    "reopened canonical tool Message lost")
            require(tool_result(wire, "s4-collect")["finalText"] == CHILD_FINAL,
                    "reopened collected child Message lost")
            for label, call_id, answer in (("parent-work", "s4-work", None),
                    ("parent-collect", "s4-collect", None), ("parent-status", "s4-status", None),
                    ("parent-final", None, PARENT_FINAL)):
                matches = [m for m in wire["messages"] if m.get("role") == "assistant" and
                           ((call_id and any(c.get("id") == call_id for c in m.get("tool_calls", []))) or
                            (answer and m.get("content") == answer))]
                require(len(matches) == 1 and matches[0].get("reasoning_content") == thinking(label),
                        f"persisted Message/providerState not replayed exactly: {label}")
            if task == TUI_TASK:
                require(any(m.get("content") == RESUME_FINAL and
                            m.get("reasoning_content") == thinking("resume") for m in wire["messages"]),
                        "TUI continuation lost resumed Message/state")
                return "tui", {"content": TUI_FINAL}
            return "resume", {"content": RESUME_FINAL}
        if task == CANCELLED:
            return "cancel", {"content": CANCEL_BODY}
        if task in (RECALL_FAIL, RECALL_CANCEL):
            source, body = self.recall_sources[task]
            reference = [m for m in wire["messages"] if m.get("role") == "user" and
                         body in m.get("content", "") and "assistant_partial" in m.get("content", "")]
            require(len(reference) == 1 and source in reference[0]["content"],
                    "recall lost partial answer/source identity")
            require("providerState" not in reference[0]["content"] and
                    "reasoning_content" not in reference[0]["content"], "recall leaked provider state")
            require(PREFIX + "FIRST_FAILED_BODY" not in reference[0]["content"],
                    "recall included superseded attempt")
            return "recall-" + ("fail" if task == RECALL_FAIL else "cancel"), {"content": task + "_FINAL"}
        return "child", {"content": CHILD_FINAL}


class Provider(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_CONNECT(self):
        self.server.scenario.errors.append("unexpected external HTTPS destination")
        self.send_error(502, "loopback provider only")

    def do_GET(self):
        self.server.scenario.errors.append("unexpected provider GET")
        self.send_error(502, "loopback provider only")

    def do_POST(self):
        scenario = self.server.scenario
        entry = None
        try:
            require(self.path == "/chat/completions", f"unexpected provider path {self.path}")
            require(not self.headers.get("Authorization"), "local provider must be signature-free")
            wire = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            with scenario.lock:
                entry = {"wire": wire, "receivedMonotonic": time.monotonic()}
                scenario.requests.append(entry)
                label, delta = scenario.next(wire)
                reason = BAD_THINK if label == "retry-superseded" else thinking(label)
                midpoint = len(reason) // 2
                events = [{"choices": [{"index": 0, "delta": {"reasoning_content": part}}]}
                          for part in (reason[:midpoint], reason[midpoint:])]
                final = {"choices": [{"index": 0, "delta": delta,
                         "finish_reason": "tool_calls" if "tool_calls" in delta else "stop"}]}
                entry.update({"label": label, "reasoning": reason, "events": events + [final]})
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Connection", "close")
            self.end_headers()
            self.close_connection = True
            for event in events:
                self.wfile.write(("data: " + encoded(event) + "\n\n").encode())
                self.wfile.flush()
            if label == "tui":
                require(scenario.tui_release.wait(45), "TUI never observed live streamed thinking")
            if label == "cancel":
                partial = {"choices": [{"index": 0, "delta": delta}]}
                entry["events"] = events + [partial]  # No invented finish event on cancellation.
                self.wfile.write(("data: " + encoded(partial) + "\n\n").encode())
                self.wfile.flush()
                require(scenario.cancel_release.wait(45), "cancel scenario never released")
                return
            self.wfile.write(("data: " + encoded(final) + "\n\ndata: [DONE]\n\n").encode())
            self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError) as error:
            if entry is None or entry.get("label") != "cancel":
                scenario.errors.append(str(error))
        except Exception as error:
            scenario.errors.append(str(error))
            if entry is not None:
                entry["error"] = str(error)
            self.send_error(400, str(error))


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


def post(core, path, body, name):
    opener = build_opener(ProxyHandler({}))
    data = encoded(body).encode() if body is not None else b""
    request = Request(core.endpoint + "/api/v2/" + path, data=data,
                      headers={"Content-Type": "application/json"}, method="POST")
    with opener.open(request, timeout=25) as response:
        value = json.load(response)
    save_json(core.artifacts / (name + ".json"), value)
    return value


def wait_until(read, predicate, description, timeout=30):
    deadline = time.monotonic() + timeout
    while True:
        value = read()
        if predicate(value):
            return value
        require(time.monotonic() < deadline, "timeout: " + description)
        time.sleep(0.05)


def verify_tui(binary, core, session, env, workspace, artifacts, scenario, replay_only=False):
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 120, 240, 0, 0))
    process, captured = None, bytearray()
    name = "tui-replay" if replay_only else "tui-live"

    def wait_for(predicate, description, timeout=35):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if select.select([master], [], [], 0.1)[0]:
                try:
                    block = os.read(master, 65536)
                except OSError as error:
                    if error.errno != errno.EIO:
                        raise
                    block = b""
                captured.extend(block)
                if b"\x1b[6n" in block:
                    os.write(master, b"\x1b[1;1R")
            text = plain_pty(captured)
            if predicate(text):
                return text
            require(process.poll() is None, f"PTY exited before {description}: {text[-2000:]}")
        raise AssertionError(f"PTY timeout: {description}; see {name}.raw/.txt")

    try:
        process = subprocess.Popen([str(binary), "--endpoint", core.endpoint,
            "--workspace", str(workspace), "--session", session], stdin=slave, stdout=slave,
            stderr=slave, env={**env, "TERM": "xterm-256color"}, cwd=workspace, start_new_session=True)
        os.close(slave)
        slave = None
        expected = (TUI_FINAL, thinking("tui")) if replay_only else (RESUME_FINAL, thinking("resume"))
        wait_for(lambda t: all(marker in t for marker in expected), "saved answer and thinking replay")
        require(BAD_THINK not in plain_pty(captured), "TUI replay displayed superseded thinking")
        if not replay_only:
            offset = len(captured)
            os.write(master, TUI_TASK.encode() + b"\r")
            wait_for(lambda _: thinking("tui") in plain_pty(captured[offset:]), "live streamed thinking")
            # Provider is held before its content/finish event; this proves live,
            # not merely a final replay that happens to contain thinking text.
            require(TUI_FINAL not in plain_pty(captured[offset:]), "answer arrived before live check")
            scenario.tui_release.set()
            wait_for(lambda _: TUI_FINAL in plain_pty(captured[offset:]), "TUI final answer")
            identity = core.get("core", "tui-core-identity")
            state = wait_until(lambda: core.get("sessions/" + session + "/context?coreId=" +
                    identity["coreId"], "tui-adoption"),
                lambda s: s["canonical"]["turnCount"] == 3 and not s.get("activeRunId") and
                    s["latestRootExecution"]["lifecycle"] == "settled", "TUI canonical adoption")
            execution = state["latestRootExecution"]["executionId"]
        else:
            execution = None
        os.write(master, b"\x04")  # detach only; /quit would stop the Core
        wait_for(lambda _: process.poll() is not None, "TUI detach", 20)
        require(process.returncode == 0 and core.process.poll() is None, "detach failed or stopped Core")
        return {"replayOnly": replay_only, "expectedText": expected, "executionId": execution,
                "liveThinkingBeforeFinal": not replay_only}
    finally:
        scenario.tui_release.set()
        stop_child(process)
        (artifacts / (name + ".raw")).write_bytes(captured)
        write(artifacts / (name + ".txt"), plain_pty(captured))
        os.close(master)
        if slave is not None:
            os.close(slave)


def entity_values(view):
    # Ignore process-owned viewId/revision; IDs, locators and positions are durable.
    return [view["entities"][key] for key in view["order"]]


def inspect_storage(db_path, core, session, scenario, public_records, public_messages, artifacts):
    with sqlite3.connect(db_path.as_uri() + "?mode=ro", uri=True) as db:
        db.execute("PRAGMA query_only=ON")
        db.row_factory = sqlite3.Row
        records = [dict(r) for r in db.execute("SELECT record_id,execution_id,ordinal,kind,payload_json,"
                   "content_digest FROM semantic_records ORDER BY execution_id,ordinal")]
        by_id = {r["record_id"]: r for r in records}
        for row in records:
            row["payload"] = json.loads(row.pop("payload_json"))
        # S3's immutable result is the first owner, independent of later Messages.
        results, result_owners = {}, []
        for row in records:
            if row["kind"] != "model_result":
                continue
            raw, rid = row["payload"], row["record_id"]
            require("result" not in raw, "model_result still embeds result: " + rid)
            if "resultRef" not in raw:
                # Retry/terminal markers without a provider result have no owner blob.
                continue
            require(raw["resultRef"] == row["content_digest"], "resultRef differs from owner FK: " + rid)
            stored = db.execute("SELECT content_bytes FROM contents WHERE content_digest=?",
                                (row["content_digest"],)).fetchone()
            require(stored is not None, "result owner contents missing: " + rid)
            blob = stored[0]
            if isinstance(blob, str):
                blob = blob.encode()
            require("sha256:" + hashlib.sha256(blob).hexdigest() == row["content_digest"],
                    "result contents digest mismatch: " + rid)
            result = json.loads(blob)
            require(isinstance(result, dict), "result owner is not an object: " + rid)
            state = result.get("providerState")
            require(isinstance(state, dict) and
                    state.get("reasoning", {}).get("field") == "reasoning_content",
                    "local Chat result lost original provider state: " + rid)
            require(any(entry.get("reasoning") == state["reasoning"]["text"] for entry in scenario.requests),
                    "result owner state differs from received SSE: " + rid)
            expected = {key: value for key, value in raw.items() if key != "resultRef"}
            expected["result"] = result
            public = public_records[rid]
            require(public["payload"] == expected, "public model_result did not restore exact result/metadata: " + rid)
            require(public.get("content") is None, "public model_result exposes owner blob as Record.Content: " + rid)
            results[rid] = result
            result_owners.append({"record": rid, "contentDigest": row["content_digest"],
                                  "resultBytes": len(blob), "request": raw["request"]})
        require(results, "no S3 result owners")

        messages = [dict(r) for r in db.execute("SELECT m.execution_id,m.message_ordinal,m.content_digest,"
                    "c.content_bytes FROM messages m JOIN contents c ON c.content_digest=m.content_digest "
                    "ORDER BY m.execution_id,m.message_ordinal")]
        restored, state_references = {}, []
        for message in messages:
            envelope = json.loads(message.pop("content_bytes"))
            message["envelope"] = envelope
            if envelope.get("role") != "assistant":
                continue
            key = (message["execution_id"], message["message_ordinal"])
            require("providerState" not in envelope and envelope.get("providerStateRecordId"),
                    "normal app Message did not reference provider state: " + str(key))
            source_id = envelope["providerStateRecordId"]
            require(source_id in results, "Message provider-state source is not a result owner: " + source_id)
            source = by_id[source_id]
            require(source["execution_id"] == message["execution_id"],
                    "Message state source belongs to another execution: " + source_id)
            expected = {name: value for name, value in envelope.items() if name != "providerStateRecordId"}
            expected["providerState"] = results[source_id]["providerState"]
            require(public_messages.get(key) == expected,
                    "Inspect Message did not restore complete original envelope/state: " + str(key))
            restored[key] = (expected, source)
            state_references.append({"execution": key[0], "messageOrdinal": key[1], "source": source_id})
        require(state_references, "no S3 Message provider-state references")
        require(sum(m.get("role") == "assistant" for m in public_messages.values()) == len(restored),
                "public assistant Messages and raw referenced Messages differ")
        canonical = core.get("sessions/" + session + "/conversation", "conversation-s3-restored")
        canonical_assistants = [m for m in canonical if m["json"].get("role") == "assistant"]
        require(canonical_assistants, "no canonical assistant Messages")
        for message in canonical_assistants:
            key = (message["executionId"], message["ordinal"])
            require(key in restored and message["json"] == restored[key][0],
                    "canonical Conversation did not restore complete Message/state: " + str(key))

        mirrors = [r for r in records if r["kind"] == "assistant_message" and r["content_digest"]]
        require(mirrors, "no persisted assistant mirrors")
        referenced, checked_messages = [], set()
        for row in mirrors:
            raw, rid = row["payload"], row["record_id"]
            require("content" not in raw and "providerState" not in raw and
                    "providerStateRecordId" not in raw,
                    "new mirror still stores content/providerState: " + rid)
            envelope_bytes = db.execute("SELECT content_bytes FROM contents WHERE content_digest=?",
                                        (row["content_digest"],)).fetchone()[0]
            envelope = json.loads(envelope_bytes)
            require(envelope["role"] == "assistant", "mirror FK is not an assistant Message")
            key = (row["execution_id"], raw["locator"]["messageOrdinal"])
            require(key in restored, "mirror Message is not a restored assistant: " + rid)
            envelope, result_source = restored[key]
            checked_messages.add(key)
            require(result_source["ordinal"] < row["ordinal"] and
                    result_source["payload"]["request"] == raw["request"] and
                    raw["request"].get("physicalRequestId"),
                    "Message state source is not the preceding same physical request: " + rid)
            message = db.execute("SELECT content_digest FROM messages WHERE execution_id=? AND message_ordinal=?",
                (row["execution_id"], raw["locator"]["messageOrdinal"])).fetchone()
            require(message and message[0] == row["content_digest"], "mirror FK differs from original Message")
            public = public_records[rid]
            require("providerStateRecordId" not in public["payload"], "public mirror exposes state reference: " + rid)
            require(public["payload"]["content"] == envelope["content"] and
                    public["payload"]["providerState"] == envelope["providerState"],
                    "public detail did not restore exact original content/providerState: " + rid)
            require(json.loads(base64.b64decode(public["content"])) == envelope,
                    "Record.Content is not original full Message: " + rid)
            for key, value in raw.items():
                if key not in ("thinkingRecordId",):
                    require(public["payload"].get(key) == value, "hydration changed mirror metadata " + key)
            if raw["conversation"]["entityKind"] == "thinking":
                require("text" not in raw and raw.get("thinkingRecordId"),
                        "thinking mirror did not replace text with source ID")
                source = by_id[raw["thinkingRecordId"]]
                sp = source["payload"]
                require(source["execution_id"] == row["execution_id"] and source["ordinal"] < row["ordinal"],
                        "thinking source not an earlier same-execution record")
                require(not source["content_digest"] and sp.get("text"), "source no longer owns thinking text")
                require(sp["conversation"]["localKey"] == raw["conversation"]["localKey"] and
                        sp["request"] == raw["request"], "source physical attempt/localKey mismatch")
                require(public["payload"]["text"] == sp["text"] == envelope["providerState"]["reasoning"]["text"],
                        "source, Message and public thinking Text differ")
                require(public["payload"]["thinkingKind"] == sp["thinkingKind"], "thinking kind changed")
                referenced.append({"mirror": rid, "source": source["record_id"],
                                   "utf8Bytes": len(sp["text"].encode())})
        require(referenced, "no source-referenced thinking mirrors")
        retry_rows = [r for r in records if r["payload"].get("text") == BAD_THINK]
        require(any(r["payload"].get("phase") == "superseded" for r in retry_rows),
                "first retry thinking missing from superseded detail")
        require(any(r["payload"].get("request", {}).get("retryAttempt") == 1 for r in mirrors),
                "regenerated mirror lost retryAttempt=1")
        require(db.execute("SELECT COUNT(*) FROM assistant_text_states").fetchone()[0] == 0,
                "settled executions retain active assistant state")
        relations = [dict(r) for r in db.execute("SELECT * FROM semantic_relations")]
        require(any(r["relation"] == "parent_call" and r["resolved"] and
                    by_id[r["record_id"]]["execution_id"] == scenario.child_id and
                    by_id[r["target_record_id"]]["execution_id"] != scenario.child_id for r in relations),
                "child admission lost parent relation")
        require(any(r["relation"] == "child_terminal" and r["resolved"] and
                    by_id[r["target_record_id"]]["execution_id"] == scenario.child_id for r in relations),
                "collect lost child terminal relation")
        raw_bytes = sum(len(encoded(r["payload"]).encode()) for r in mirrors)
        public_bytes = sum(len(encoded(public_records[r["record_id"]]["payload"]).encode()) for r in mirrors)
        result = {"slices": ["S-1", "S-2", "S-3"],
                  "resultOwners": result_owners, "providerStateReferences": state_references,
                  "canonicalAssistantMessagesRestored": len(canonical_assistants),
                  "assistantMirrors": len(mirrors), "thinkingReferences": referenced,
                  "rawMirrorReencodedBytes": raw_bytes, "publicMirrorReencodedBytes": public_bytes,
                  "note": "Encoding comparison in this candidate only; NOT a before/after physical DB measurement."}
        require(set(restored) <= checked_messages,
                "referenced Message result was not checked through its mirror")
        save_json(artifacts / "raw-messages.json", messages)
        save_json(artifacts / "raw-records.json", records)
        save_json(artifacts / "raw-relations.json", relations)
        save_json(artifacts / "storage-check.json", result)
        return result


def verify(directory, args, report):
    workspace, config_dir, artifacts = directory / "workspace", directory / "config", directory / "evidence"
    workspace.mkdir()
    artifacts.mkdir()
    env = {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "LANG": "C.UTF-8"}
    for name in ("HOME", "TMPDIR", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"):
        isolated = directory / name.lower()
        isolated.mkdir()
        env[name] = str(isolated)
    if args.binary:
        binary = Path(args.binary).resolve()
        tui_binary = Path(args.tui_binary).resolve() if args.tui_binary else binary.with_name("hjh-tui")
        require(binary.is_file() and tui_binary.is_file(), "candidate hjh and hjh-tui binaries required")
    else:
        require(not args.tui_binary, "--tui-binary requires --binary; avoid mixed candidates")
        binary, tui_binary = directory / "hjh", directory / "hjh-tui"
        build_env = {**os.environ, "GOPROXY": "off", "GOSUMDB": "off"}
        cache = command(["go", "env", "GOMODCACHE", "GOCACHE"], build_env, ROOT, artifacts / "go-env").stdout.splitlines()
        build_env.update(env)
        build_env.update({"GOMODCACHE": cache[0], "GOCACHE": cache[1]})
        for target, package in ((binary, "./cmd/hjh"), (tui_binary, "./cmd/hjh-tui")):
            command(["go", "build", "-o", target, package], build_env, ROOT, artifacts / (target.name + "-build"), 300)
    report["binarySHA256"] = hashlib.sha256(binary.read_bytes()).hexdigest()
    report["tuiBinarySHA256"] = hashlib.sha256(tui_binary.read_bytes()).hexdigest()
    command([binary, "roles", "--workspace", workspace], env, workspace, artifacts / "install-defaults")
    shutil.copytree(workspace / ".hjh/extensions", config_dir)
    config_path, db_path = config_dir / "config.json", directory / "history.sqlite3"
    config = json.loads(config_path.read_text())
    config["credentials"] = {}
    config["roles"] = {"default": {"revision": "thinking-s4", "instruction": "Execute the local verification task.",
        "tools": ["run_js", "read_file", "spawn_agent", "agent_status", "collect_agent"]}}
    config["defaultRole"] = "default"
    config["model"] = {"provider": "s4-local", "api": "chat-completions", "modelId": "s4-local-model", "effort": "auto"}
    config.pop("hooks", None)  # No hook calls, compaction or usage scenario.
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
    config["providers"] = {"s4-local": {"api": "chat-completions", "baseURL": endpoint,
        "path": "/chat/completions", "auth": {"kind": "none"},
        "adapter": {"reasoningField": "reasoning_content", "reasoningReplay": True},
        "defaultModel": "s4-local-model", "models": [{"id": "s4-local-model", "contextTokens": 100000}]}}
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

    def state_path(run_id, core_id):
        return "runs/" + run_id + "?coreId=" + core_id

    def stopped_recall(task, recall_task, name, body, outcome):
        core_id = core.get("core", name + "-core")["coreId"]
        run_id = "s4-" + name
        started = post(core, "runs", {"coreId": core_id, "runId": run_id,
                                     "task": task, "workspace": str(workspace)}, name + "-start")
        # StartRun acknowledges preparation; Session admission is asynchronous.
        admitted = wait_until(lambda: core.get(state_path(run_id, core_id), name + "-admitted"),
                              lambda s: bool(s.get("sessionId")), name + " session admission")
        stopped_session = admitted["sessionId"]
        if task == CANCELLED:
            # Wait for committed streaming state, not an arbitrary provider sleep.
            wait_until(lambda: core.get("sessions/" + stopped_session + "/view", name + "-live-view"),
                lambda v: any(e.get("text") == body and e.get("status") == "streaming"
                              for e in v["entities"].values()) and
                          any(e.get("text") == thinking("cancel") and e.get("kind") == "thinking"
                              for e in v["entities"].values()), "saved live cancel body/thinking")
            post(core, "runs/" + run_id + "/cancel?coreId=" + core_id, None, name + "-cancel")
            scenario.cancel_release.set()
        settled = wait_until(lambda: core.get(state_path(run_id, core_id), name + "-settled"),
                             lambda s: s["phase"] == "terminal", name + " terminal", 45)
        source = settled["executionId"]
        inspection = core.get("executions/" + source, name + "-inspect")
        require(inspection["execution"]["outcome"] == outcome, "unexpected stopped outcome")
        context = core.get("sessions/" + stopped_session + "/context?coreId=" + core_id, name + "-context")
        require(context["canonical"]["turnCount"] == 0, "stopped execution was canonically adopted")
        require(body in encoded(inspection), "stopped partial answer missing from saved detail")
        scenario.recall_sources[recall_task] = (source, body)
        receipt = post(core, "sessions/" + stopped_session + "/recall", {
            "coreId": core_id, "operationId": "s4-recall-" + name, "action": "prepare", "selector": "latest"},
            name + "-recall-prepare")
        require(receipt["receipt"]["result"] == "prepared" and
                receipt["receipt"]["sourceExecutionId"] == source, "recall source not prepared")
        recalled, target = run(recall_task, name + "-recall-turn", stopped_session)
        require(recalled.stdout.strip() == recall_task + "_FINAL", "recall continuation failed")
        return {"session": stopped_session, "sourceExecution": source, "targetExecution": target,
                "outcome": outcome, "partial": body}

    try:
        core.start()
        first, parent = run(FIRST, "cli-first")
        require(first.stdout.strip() == PARENT_FINAL, "parent final answer changed")
        with sqlite3.connect(db_path.as_uri() + "?mode=ro", uri=True) as db:
            sessions = db.execute("SELECT session_id FROM sessions").fetchall()
        require(len(sessions) == 1, f"unexpected initial Sessions: {sessions}")
        session = sessions[0][0]
        report.update({"session": session, "parentExecution": parent, "childExecution": scenario.child_id})
        before = core.get("executions/" + parent, "inspect-before-restart")
        conversation = core.get("sessions/" + session + "/conversation", "conversation-before-restart")
        view = core.get("sessions/" + session + "/view", "session-view-before-restart")
        expected_thinking = [thinking(label) for label in
                            ("parent-work", "parent-collect", "parent-status", "parent-final", "child")]
        for marker in expected_thinking:
            require(any(e.get("text") == marker and e["kind"] == "thinking" for e in view["entities"].values()),
                    "parent/child SessionView missing thinking: " + marker)
        superseded = [e for e in view["entities"].values() if e.get("text") in (BAD_BODY, BAD_THINK)]
        require({e["text"] for e in superseded} == {BAD_BODY, BAD_THINK} and
                all(e["status"] == "superseded" for e in superseded),
                "SessionView lost superseded detail/status (TUI alone filters its display)")
        core.stop()
        core.start()
        require(len(set(core.pids)) == 2, "Core was not reopened in a different process")
        require(before == core.get("executions/" + parent, "inspect-after-restart"), "Inspect changed on reopen")
        require(conversation == core.get("sessions/" + session + "/conversation", "conversation-after-restart"),
                "canonical Messages/providerState changed on reopen")
        restored_view = core.get("sessions/" + session + "/view", "session-view-after-restart")
        require(entity_values(view) == entity_values(restored_view),
                "SessionView text/identity/position/detail changed on reopen")
        second, resumed = run(RESUME, "cli-resume", session)
        require(second.stdout.strip() == RESUME_FINAL, "resumed final answer changed")
        report["tuiLive"] = verify_tui(tui_binary, core, session, env, workspace, artifacts, scenario)
        # Force replay through a new Core process and a newly attached TUI.
        core.stop()
        core.start()
        report["tuiReplay"] = verify_tui(tui_binary, core, session, env, workspace, artifacts, scenario, True)
        report["failureRecall"] = stopped_recall(FAILED, RECALL_FAIL, "failed", FAIL_BODY, "failed")
        report["cancelRecall"] = stopped_recall(CANCELLED, RECALL_CANCEL, "cancelled", CANCEL_BODY, "cancelled")
        require(not scenario.errors, f"provider findings: {scenario.errors}")
        require(scenario.steps == Counter({FIRST: 5, CHILD: 1, RESUME: 1, TUI_TASK: 1,
                FAILED: 2, CANCELLED: 1, RECALL_FAIL: 1, RECALL_CANCEL: 1}),
                f"unexpected request counts: {scenario.steps}")
        failed, retried = [next(e for e in scenario.requests if e["label"] == label)
                           for label in ("retry-superseded", "parent-work")]
        require(failed["wire"] == retried["wire"] and
                retried["receivedMonotonic"] - failed["receivedMonotonic"] >= 1.9,
                "existing one-retry operation did not resend identical input after its delay")
        detail_records = {}
        for view_name in ("session", "canonical", "detail"):
            result = command([binary, "history", "--connect", core.endpoint, "--session", session,
                              "--view", view_name], env, workspace, artifacts / ("history-" + view_name))
            require(all(marker in result.stdout for marker in (PARENT_FINAL, CHILD_FINAL, RESUME_FINAL, TUI_FINAL)),
                    view_name + " lost saved answers")
            if view_name in ("session", "detail"):
                require(all(marker in result.stdout for marker in expected_thinking + [thinking("tui")]),
                        view_name + " lost thinking")
            if view_name == "detail":
                detail_records = {line["identity"]: line["value"] for line in
                    (json.loads(t) for t in result.stdout.splitlines()) if line["kind"] == "record"}
        execution_ids = [parent, scenario.child_id, resumed, report["tuiLive"]["executionId"]]
        for key in ("failureRecall", "cancelRecall"):
            execution_ids += [report[key]["sourceExecution"], report[key]["targetExecution"]]
        public_records, public_messages = {}, {}
        for execution in execution_ids:
            inspection = core.get("executions/" + execution, "inspect-" + execution)
            for message in inspection["messages"]:
                public_messages[(message["executionId"], message["ordinal"])] = message["json"]
            for record in inspection["records"]:
                public_records[record["id"]] = record
                if execution in execution_ids[:4]:
                    require(record["id"] in detail_records and
                            record["payload"] == detail_records[record["id"]]["payload"],
                            "CLI detail and Inspect payload disagree")
                if record["kind"] in ("assistant_message", "model_result") or record["payload"].get("kind") == "model_result":
                    single = core.get(f"executions/{execution}/records/{record['ordinal']}",
                                      "record-" + record["id"])
                    require(single == record, "standalone Record differs from Inspect: " + record["id"])
        report["storage"] = inspect_storage(db_path, core, session, scenario, public_records,
                                            public_messages, artifacts)
        # Check direct DB CLI readers independently of connected API after close.
        core.stop()
        for view_name in ("session", "canonical", "detail"):
            local = command([binary, "history", "--workspace", workspace, "--db", db_path,
                             "--session", session, "--view", view_name], env, workspace,
                            artifacts / ("history-local-" + view_name))
            remote = (artifacts / ("history-" + view_name + ".stdout")).read_text()
            require(local.stdout == remote, "local and connected history differ: " + view_name)
        report["corePIDs"] = core.pids
        report["checks"] = [
            "Production parent spawn + independent read_file + collect + status reports/relations",
            "Two reasoning_content SSE deltas, saved live thinking, PTY live before content/final",
            "New Core processes preserve Messages/providerState; next-turn HTTP reasoning replay exact",
            "CLI session/canonical/detail, direct DB CLI, Inspect, standalone Record, parent/child SessionView",
            "New Core plus new PTY replays persisted answer/thinking; superseded thinking excluded",
            "Malformed-tool retry succeeds; terminal retry failure and stream cancellation recall partials with source",
            "Raw assistant mirrors omit content/providerState; thinking references exact preceding physical source Text",
            "S3 model_result resultRef owns exact result object through content_digest; public Payload restores result, Content stays absent",
            "S3 raw assistant Messages omit providerState and reference earlier same-execution/physical-request model_result",
            "S3 Inspect Messages and canonical Conversation restore complete state; no providerStateRecordId exposed",
            "Public mirror Payload and Record.Content restore full Message/state without overwriting mirror metadata",
        ]
    finally:
        scenario.cancel_release.set()
        scenario.tui_release.set()
        core.stop()
        server.shutdown()
        server.server_close()
        thread.join()
        save_json(artifacts / "provider-requests-responses.json", scenario.requests)
        save_json(artifacts / "provider-errors.json", scenario.errors)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--binary", help="candidate hjh (otherwise build both commands, dependencies offline)")
    parser.add_argument("--tui-binary", help="candidate hjh-tui; default beside --binary")
    parser.add_argument("--output-dir", type=Path,
                        default=ROOT / "tmp/assistant-provider-state-ycX6mbUm/acceptance",
                        help="retain all evidence in a fresh child directory")
    args = parser.parse_args()
    args.output_dir.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix="assistant-thinking-s4-", dir=args.output_dir.resolve()))
    report = {"status": "failed", "slices": ["S-1", "S-2", "S-3"],
              "realProviderTested": False, "protocol": "chat-completions",
              "artifacts": str(directory), "unverified": UNVERIFIED,
              "inputBasis": "Synthetic reasoning_content string SSE from existing Chat thinking/retry tests; not original captured SSE",
              "startedAt": time.strftime("%Y-%m-%dT%H:%M:%S%z")}
    exit_code = 1
    try:
        verify(directory, args, report)
        report["status"] = "passed"
        exit_code = 0
    except Exception as error:
        report["error"] = f"{type(error).__name__}: {error}"
        report["note"] = "Acceptance incomplete; only checks reached before the error ran. No success inferred."
    finally:
        report["finishedAt"] = time.strftime("%Y-%m-%dT%H:%M:%S%z")
        save_json(directory / "report.json", report)
        print(json.dumps(report, ensure_ascii=False, indent=2))
    return exit_code


if __name__ == "__main__":
    raise SystemExit(main())
