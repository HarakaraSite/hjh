#!/usr/bin/env python3
"""Bounded S9 acceptance through a compiled CLI, private Core, and SQLite.

Run ONCE on the stable candidate, after the coordinating owner builds/packages:
  python3 scripts/verify-stdlib-tools.py --binary /absolute/path/to/hjh \
      --out /absolute/path/to/new-evidence-directory

No build, full gate, live model, real credential, or TUI is run here. The local
OpenAI-compatible provider scripts calls; the actual product executes every tool.
Exa's fixed HTTPS endpoint is NOT replaced: authenticated web_search remains an
app-test/live-acceptance responsibility, not a success claimed by this script.
Requires Python 3.9+, git, sh, and rg or GNU grep on PATH. --out must not exist;
without it, a private temporary directory is created and RETAINED. All config,
HOME/XDG state, database, logs, requests, and public history evidence stay there.
Only processes started by this script are stopped. No running Core is discovered,
stopped, or replaced. A failed attempt is preserved, never automatically retried.
"""

import argparse
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import tempfile
import threading
import time
import traceback
from urllib.request import ProxyHandler, build_opener


FIRST = "S9 create, inspect, compose stdlib tools, and recover from a timer error"
RESUME = "S9 resume the same session after private Core restart"
FIRST_ANSWER = "S9 initial tools and timer recovery completed."
RESUME_ANSWER = "S9 resumed saved history and files after Core restart."
TIMER_ERROR = "S9_UNCAUGHT_TIMER_ERROR"
INITIAL = "alpha one\nbeta two\n"
EDITED = "alpha one\nbeta recovered\n"
HTML = b"<!doctype html><html><body><h1>S9 local page</h1><p>Readable &amp; retained.</p></body></html>"
DOWNLOAD = b"S9 complete download\n" + b"abcdefghij\n" * 7000
TOOLS = ["run_js", "read_file", "write_file", "edit_file", "ls", "find", "wc",
         "grep", "git_inspect", "command", "http", "web_fetch"]


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


def decoded_text(text):
    # Tool errors are plain text, successful tool results are JSON-encoded.
    try:
        return json.loads(text)
    except (ValueError, TypeError):
        return text


def stored_results(history):
    return [result for message in history if message["json"]["role"] == "tool"
            for result in message["json"]["content"]]


class Scenario:
    """One root turn plus one resumed turn; every next call checks real output."""

    def __init__(self, out, workspace, endpoint):
        self.out, self.workspace, self.endpoint = out, workspace, endpoint
        self.errors, self.requests, self.observed = [], [], {}
        self.steps = {FIRST: 0, RESUME: 0}
        self.plans = {FIRST: self.initial_plan(), RESUME: self.resume_plan()}

    @staticmethod
    def call(call_id, name, arguments):
        return {"id": call_id, "name": name, "arguments": arguments}

    def js(self, call_id, code):
        return self.call(call_id, "run_js", {"code": code})

    def initial_plan(self):
        page = self.endpoint + "/page"
        raw = self.endpoint + "/raw"
        download = self.endpoint + "/download"
        # All tools here are the installed default tools, not harness extensions.
        direct = [
            self.call("s9-write", "write_file", {"path": "notes/direct.txt", "text": INITIAL}),
            self.call("s9-edit", "edit_file", {"path": "notes/direct.txt", "edits": [
                {"oldText": "beta two", "newText": "beta recovered"}]}),
            self.call("s9-read", "read_file", {"path": "notes/direct.txt", "offset": 2, "limit": 1}),
            self.call("s9-ls", "ls", {"path": "notes"}),
            self.call("s9-find", "find", {"path": "notes", "pattern": "*.txt", "type": "file"}),
            self.call("s9-wc", "wc", {"paths": ["notes/direct.txt"]}),
            self.call("s9-grep", "grep", {"path": "notes", "pattern": "recovered", "patternKind": "literal"}),
            self.call("s9-git", "git_inspect", {"op": "status", "paths": ["notes/direct.txt"]}),
            self.call("s9-command", "command", {"program": "sh", "args": ["-c",
                'printf "S9_COMMAND|%s|" "$PWD"; cat; printf S9_STDERR >&2; exit 7'], "stdin": "S9_STDIN"}),
            self.call("s9-http", "http", {"url": raw}),
            self.call("s9-web", "web_fetch", {"url": page}),
        ]
        composed = """// Compose the installed tools and stdlib on real workspace files.
const fs = await import('node:fs');
const path = await import('node:path');
const cp = await import('node:child_process');
const {default:proc} = await import('node:process');
if (proc.cwd() !== EXPECTED_CWD) throw new Error('wrong stdlib cwd');
try { throw new Error('caught normally'); } catch (_) {}
await new Promise(resolve => setTimeout(resolve, 3));
await tools.write_file({path:'notes/composed.txt', text:'composed old\\n'});
const edit = await tools.edit_file({path:'notes/composed.txt', edits:[{oldText:'old',newText:'ready'}]});
if (edit.replacements !== 1) throw new Error('composed edit');
const [read, listed, found, counts, grep, git, command, http, page] = await Promise.all([
  tools.read_file({path:'notes/composed.txt'}), tools.ls({path:'notes'}),
  tools.find({path:'notes',pattern:'*.txt',type:'file'}), tools.wc({paths:['notes/composed.txt']}),
  tools.grep({path:'notes',pattern:'composed ready',patternKind:'literal'}),
  tools.git_inspect({op:'status',paths:['notes/composed.txt']}),
  tools.command({program:'sh',args:['-c','cat'],stdin:'S9_COMPOSED_STDIN'}),
  tools.http({url:RAW_URL}), tools.web_fetch({url:PAGE_URL})
]);
if (read.text !== 'composed ready\\n' || listed.entries.length !== 2 || found.total !== 2 ||
    counts.total.words !== 2 || grep.matches !== 1 || !git.text.includes('notes/composed.txt') ||
    command.exitCode !== 0 || command.stdout !== 'S9_COMPOSED_STDIN' ||
    http.body !== 'S9_RAW_BODY' || !page.includes('Readable & retained.')) throw new Error('composition results');
const native = await cp.execFile('sh',['-c','cat; printf S9_NATIVE_STDERR >&2'],{input:'S9_NATIVE_STDIN'});
if (native.stdout !== 'S9_NATIVE_STDIN' || native.stderr !== 'S9_NATIVE_STDERR') throw new Error('native process');
if (await (await fetch(RAW_URL)).text() !== 'S9_RAW_BODY') throw new Error('native fetch');
await fs.promises.writeFile(path.join(proc.cwd(),'notes/native.txt'),'S9_NATIVE_FILE');
if (await fs.promises.readFile(path.join(proc.cwd(),'notes/native.txt'),'utf8') !== 'S9_NATIVE_FILE') throw new Error('native file');
const bounded = await tools.web_fetch({url:DOWNLOAD_URL});
if (!bounded.includes('truncated: true') || !bounded.includes('[body truncated]')) throw new Error('bounded read');
const saved = await tools.web_fetch({url:DOWNLOAD_URL,save_to:'notes/download.txt'});
if (!saved.includes('Saved: notes/download.txt') ||
    (await fs.promises.readFile(path.join(proc.cwd(),'notes/download.txt'))).length !== DOWNLOAD_BYTES) throw new Error('complete download');
globalThis.s9State = 'kept';
setTimeout(() => {
  globalThis.s9Timer = 'drained';
  fs.writeFileSync(path.join(proc.cwd(),'notes/timer.txt'),'S9_TIMER_DRAINED');
}, 3);
return {ok:true,read:read.text,native:native.stdout};
"""
        for key, value in {"EXPECTED_CWD": str(self.workspace), "RAW_URL": raw, "PAGE_URL": page,
                           "DOWNLOAD_URL": download, "DOWNLOAD_BYTES": len(DOWNLOAD)}.items():
            composed = composed.replace(key, encoded(value))
        failure = """// A thrown timer callback must fail this call and reset the VM.
if (globalThis.s9State !== 'kept' || globalThis.s9Timer !== 'drained') throw new Error('normal call lost state or timer');
globalThis.s9Discard = true;
setInterval(() => { globalThis.s9Leaked = true; }, 10000);
setTimeout(() => { throw new Error('S9_UNCAUGHT_TIMER_ERROR'); }, 3);
return 'must not be reported as success';
"""
        recovery = """// Recover in the same execution using a fresh VM and real tools.
if (globalThis.s9State !== undefined || globalThis.s9Timer !== undefined ||
    globalThis.s9Discard !== undefined || globalThis.s9Leaked !== undefined) throw new Error('VM was not reset');
const fs = await import('node:fs');
await new Promise(resolve => setTimeout(resolve, 3));
await tools.write_file({path:'notes/recovered.txt',text:'S9_RECOVERED_FILE'});
if (await fs.promises.readFile(process.cwd() + '/notes/timer.txt','utf8') !== 'S9_TIMER_DRAINED') throw new Error('drained file lost');
if ((await tools.command({program:'sh',args:['-c','printf S9_RECOVERED_COMMAND']})).stdout !== 'S9_RECOVERED_COMMAND') throw new Error('tools not recovered');
return 'S9_RECOVERED_OK';
"""
        return direct + [self.js("s9-composed", composed), self.js("s9-timer-error", failure),
                         self.js("s9-recovered", recovery)]

    def resume_plan(self):
        return [self.js("s9-resumed", """// Session history and files persist; VM state does not.
if (globalThis.s9State !== undefined) throw new Error('execution VM state persisted');
const fs = await import('node:fs');
await new Promise(resolve => setTimeout(resolve, 3));
const direct = await tools.read_file({path:'notes/direct.txt'});
if (direct.text !== 'alpha one\\nbeta recovered\\n' ||
    await fs.promises.readFile(process.cwd() + '/notes/recovered.txt','utf8') !== 'S9_RECOVERED_FILE') throw new Error('resume lost files');
await tools.edit_file({path:'notes/direct.txt',edits:[{oldText:'recovered',newText:'resumed'}]});
return {ok:true,text:(await tools.read_file({path:'notes/direct.txt'})).text};
""")]

    def check_result(self, call, text):
        call_id = call["id"]
        value = decoded_text(text)
        self.observed[call_id] = text
        if call_id == "s9-timer-error":
            require(TIMER_ERROR in text, f"timer callback failure not returned: {text}")
            return
        if call_id == "s9-write":
            ok = isinstance(value, dict) and value.get("bytes") == len(INITIAL.encode())
        elif call_id == "s9-edit":
            ok = isinstance(value, dict) and value.get("replacements") == 1
        elif call_id == "s9-read":
            ok = isinstance(value, dict) and value.get("text") == "beta recovered\n"
        elif call_id in ("s9-ls", "s9-find"):
            ok = isinstance(value, dict) and len(value.get("entries", [])) == 1 and value["entries"][0]["path"].endswith("notes/direct.txt")
        elif call_id == "s9-wc":
            ok = isinstance(value, dict) and value.get("total") == {"lines": 2, "words": 4, "bytes": len(EDITED.encode())}
        elif call_id == "s9-grep":
            ok = isinstance(value, dict) and value.get("matches") == 1 and value["lines"][0]["text"] == "beta recovered\n"
        elif call_id == "s9-git":
            ok = isinstance(value, dict) and "?? notes/direct.txt" in value.get("text", "")
        elif call_id == "s9-command":
            ok = value == {"exitCode": 7, "stdout": f"S9_COMMAND|{self.workspace}|S9_STDIN", "stderr": "S9_STDERR"}
        elif call_id == "s9-http":
            ok = value == {"status": 200, "body": "S9_RAW_BODY"}
        elif call_id == "s9-web":
            ok = isinstance(value, str) and "S9 local page" in value and "Readable & retained." in value and "<h1>" not in value
        elif call_id == "s9-composed":
            ok = value == {"ok": True, "read": "composed ready\n", "native": "S9_NATIVE_STDIN"}
            require((self.workspace / "notes/timer.txt").read_text() == "S9_TIMER_DRAINED", "call returned before timer drain")
            require((self.workspace / "notes/download.txt").read_bytes() == DOWNLOAD, "download bytes differ")
        elif call_id == "s9-recovered":
            ok = value == "S9_RECOVERED_OK"
        elif call_id == "s9-resumed":
            ok = value == {"ok": True, "text": "alpha one\nbeta resumed\n"}
        else:
            raise AssertionError(f"unhandled call {call_id}")
        require(ok, f"{call_id}: unexpected product result {text}")

    def next(self, wire):
        self.requests.append(wire)
        save_json(self.out / "provider" / f"request-{len(self.requests):02d}.json", wire)
        require(wire.get("model") == "s9-local-model" and wire.get("stream"), "wrong provider/model/stream")
        selected = {entry["function"]["name"] for entry in wire["tools"]}
        require(selected == set(TOOLS), f"selected tools differ: {selected}")
        task = [m["content"] for m in wire["messages"] if m["role"] == "user"][-1]
        require(task in self.plans, f"unexpected task: {task}")
        step = self.steps[task]
        plan = self.plans[task]
        require(step <= len(plan), "extra provider request; no automatic scenario retries")
        if step:
            previous = plan[step - 1]
            results = [m for m in wire["messages"] if m.get("role") == "tool" and
                       m.get("tool_call_id") == previous["id"]]
            require(len(results) == 1, f"missing/duplicate result for {previous['id']}")
            self.check_result(previous, results[0]["content"])
        if task == RESUME and step == 0:
            require(any(m.get("role") == "assistant" and m.get("content") == FIRST_ANSWER
                        for m in wire["messages"]), "resume lost prior final answer")
            require(any(m.get("tool_call_id") == "s9-timer-error" and TIMER_ERROR in m["content"]
                        for m in wire["messages"]), "resume lost saved tool failure")
        self.steps[task] += 1
        if step == len(plan):
            return {"content": RESUME_ANSWER if task == RESUME else FIRST_ANSWER}
        call = plan[step]
        return {"tool_calls": [{"index": 0, "id": call["id"], "type": "function",
                "function": {"name": call["name"], "arguments": encoded(call["arguments"])}}]}


class Provider(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def respond(self, status, content_type, body):
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        pages = {"/raw": ("text/plain; charset=utf-8", b"S9_RAW_BODY"),
                 "/page": ("text/html; charset=utf-8", HTML),
                 "/download": ("text/plain; charset=utf-8", DOWNLOAD)}
        self.server.gets.append(self.path)
        if self.path not in pages:
            self.server.scenario.errors.append(f"unexpected GET {self.path}")
            self.send_error(404)
            return
        content_type, body = pages[self.path]
        self.respond(200, content_type, body)

    def do_POST(self):
        try:
            require(self.path == "/chat/completions", f"unexpected provider POST {self.path}")
            require(not self.headers.get("Authorization"), "local provider unexpectedly received Authorization")
            wire = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            delta = self.server.scenario.next(wire)
            reason = "tool_calls" if "tool_calls" in delta else "stop"
            event = {"model": "s9-local-model", "choices": [
                {"index": 0, "delta": delta, "finish_reason": reason}]}
            body = ("data: " + encoded(event) + "\n\ndata: [DONE]\n\n").encode()
            self.respond(200, "text/event-stream", body)
        except Exception as error:
            self.server.scenario.errors.append(str(error))
            self.send_error(400, str(error))


class Harness:
    def __init__(self, binary, out):
        self.binary, self.out = binary, out
        self.workspace = out / "workspace"
        self.config = out / "config" / "config.json"
        self.db = out / "state" / "history.sqlite3"
        self.env = {"PATH": os.environ.get("PATH", os.defpath), "LANG": "C.UTF-8"}
        for name in ("HOME", "TMPDIR", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"):
            directory = out / "private-env" / name.lower()
            directory.mkdir(parents=True)
            self.env[name] = str(directory)
        self.workspace.mkdir()
        self.db.parent.mkdir()
        self.core, self.core_log = None, None
        self.core_pids = []
        self.opener = build_opener(ProxyHandler({}))

    def command(self, label, args, timeout=120):
        argv = [str(arg) for arg in args]
        try:
            result = subprocess.run(argv, cwd=self.workspace, env=self.env,
                                    capture_output=True, text=True, timeout=timeout)
        except subprocess.TimeoutExpired as error:
            save_json(self.out / f"{label}.timeout.json", {"argv": argv, "timeout": timeout})
            raise RuntimeError(f"{label} timed out after {timeout}s") from error
        write(self.out / f"{label}.stdout", result.stdout)
        write(self.out / f"{label}.stderr", result.stderr)
        save_json(self.out / f"{label}.command.json", {"argv": argv, "exitCode": result.returncode})
        require(result.returncode == 0, f"{label}: exit {result.returncode}; see {self.out}/{label}.stderr")
        return result.stdout

    def api(self, origin, path, evidence):
        with self.opener.open(origin + "/api/v2" + path, timeout=20) as response:
            value = json.load(response)
        save_json(self.out / evidence, value)
        return value

    def start_core(self, label):
        require(self.core is None, "our prior Core has not been stopped")
        self.core_log = (self.out / f"{label}.log").open("w")
        self.core = subprocess.Popen([str(self.binary), "serve", "--workspace", str(self.workspace),
            "--config", str(self.config), "--db", str(self.db), "--listen", "127.0.0.1:0", "--max-steps", "32"],
            cwd=self.workspace, env=self.env, stdout=self.core_log, stderr=self.core_log)
        self.core_pids.append(self.core.pid)
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            log = (self.out / f"{label}.log").read_text()
            require(self.core.poll() is None, f"private Core exited; see {label}.log")
            match = re.search(r"Core API listening at (http://127\.0\.0\.1:\d+)/api/v2", log)
            if match:
                origin = match.group(1)
                self.api(origin, "/core", f"{label}.json")
                return origin
            time.sleep(0.05)
        raise RuntimeError(f"private Core startup timeout; see {label}.log")

    def stop_core(self):
        if self.core is not None:
            if self.core.poll() is None:
                self.core.send_signal(signal.SIGTERM)
                try:
                    self.core.wait(timeout=20)
                except subprocess.TimeoutExpired:
                    self.core.kill()
                    self.core.wait(timeout=10)
            self.core = None
        if self.core_log is not None:
            self.core_log.close()
            self.core_log = None

    def history(self, origin, session, label):
        return json.loads(self.command(label, [self.binary, "history", "--connect", origin,
                          "--session", session, "--json"]))

    def run(self, summary):
        self.command("binary-version", [self.binary, "--version"])
        self.command("git-init", ["git", "init", "--quiet", self.workspace])
        self.command("install-defaults", [self.binary, "roles", "--workspace", self.workspace])
        # Copy the compiled binary's installed defaults, never a source checkout or user config.
        source = self.workspace / ".hjh" / "extensions"
        for path in source.rglob("*"):
            if path.is_file():
                target = self.config.parent / path.relative_to(source)
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(path.read_bytes())
        config = json.loads(self.config.read_text())
        summary["installedSourceSHA256"] = {str(path.relative_to(self.config.parent)): hashlib.sha256(path.read_bytes()).hexdigest()
            for path in sorted(self.config.parent.rglob("*")) if path.is_file()}
        server = ThreadingHTTPServer(("127.0.0.1", 0), Provider)
        endpoint = f"http://127.0.0.1:{server.server_port}"
        scenario = Scenario(self.out, self.workspace, endpoint)
        server.scenario, server.gets = scenario, []
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            config["credentials"] = {}
            config["model"] = {"provider": "s9-local", "api": "chat-completions", "modelId": "s9-local-model", "effort": "auto"}
            config["providers"] = {"s9-local": {"api": "chat-completions", "baseURL": endpoint,
                "path": "/chat/completions", "auth": {"kind": "none"}, "defaultModel": "s9-local-model",
                "models": [{"id": "s9-local-model", "contextTokens": 100000}]}}
            config["roles"]["default"]["tools"] = TOOLS
            config["defaultRole"] = "default"
            save_json(self.config, config)
            save_json(self.out / "scenario.json", scenario.plans)
            origin = self.start_core("core-first")
            answer = self.command("run-first", [self.binary, "run", "--connect", origin, FIRST])
            require(answer.strip() == FIRST_ANSWER, f"wrong initial final answer: {answer}")
            sessions = self.api(origin, "/sessions", "sessions-first.json")
            require(len(sessions) == 1 and sessions[0]["turnCount"] == 1, "expected one one-turn session")
            session = sessions[0]["id"]
            summary["session"] = session
            first_history = self.history(origin, session, "history-first")
            saved = {result["callId"]: result for result in stored_results(first_history)}
            for call in scenario.plans[FIRST]:
                result = saved[call["id"]]
                expected = "error" if call["id"] == "s9-timer-error" else "success"
                require(result["outcome"] == expected, f"persisted {call['id']} outcome differs")
                require(decoded_text(result["text"]) == decoded_text(scenario.observed[call["id"]]),
                        f"persisted {call['id']} result differs from provider")
            self.stop_core()
            # Same database/config/workspace, but a newly started private Core.
            origin = self.start_core("core-resumed")
            restarted_history = self.history(origin, session, "history-after-restart")
            require(restarted_history == first_history, "saved history changed across Core restart")
            answer = self.command("run-resumed", [self.binary, "run", "--connect", origin, "--session", session, RESUME])
            require(answer.strip() == RESUME_ANSWER, f"wrong resumed final answer: {answer}")
            sessions = self.api(origin, "/sessions", "sessions-final.json")
            require(len(sessions) == 1 and sessions[0]["id"] == session and sessions[0]["turnCount"] == 2,
                    "resume did not continue the same two-turn session")
            final_history = self.history(origin, session, "history-final")
            finals = [message["json"]["content"].get("text") for message in final_history
                      if message["json"]["role"] == "assistant" and isinstance(message["json"]["content"], dict)]
            require(FIRST_ANSWER in finals and RESUME_ANSWER in finals, "final answers not persisted")
            resumed = [r for r in stored_results(final_history) if r["callId"] == "s9-resumed"]
            require(len(resumed) == 1 and resumed[0]["outcome"] == "success" and
                    decoded_text(resumed[0]["text"]) == decoded_text(scenario.observed["s9-resumed"]), "resumed result not persisted")
            executions = self.api(origin, f"/sessions/{session}/executions", "executions.json")
            require(len(executions) == 2 and all(not e["parentExecutionId"] and e["outcome"] == "completed" and
                    e["adoption"] == "canonical" for e in executions), "expected two completed canonical root executions")
            for index, execution in enumerate(executions):
                self.api(origin, f"/executions/{execution['id']}?fromOrdinal=0&toOrdinal=-1", f"inspection-{index + 1}.json")
            require(not scenario.errors, f"provider findings: {scenario.errors}")
            require(scenario.steps == {task: len(plan) + 1 for task, plan in scenario.plans.items()}, "scenario incomplete")
            require((self.workspace / "notes/direct.txt").read_text() == "alpha one\nbeta resumed\n", "resumed edit missing")
            require(self.db.read_bytes()[:16] == b"SQLite format 3\x00", "Core did not create SQLite history")
            summary.update({"status": "passed", "executions": executions, "providerRequests": len(scenario.requests),
                "checks": ["direct selected tools", "run_js tool composition", "stdlib modules/fs/process/fetch/timers",
                           "normal timer drain and same-VM state", "uncaught timer error and fresh-VM recovery",
                           "persisted success/error results and final answers", "full download and bounded page read",
                           "private Core restart", "same-session continuation with saved failure history"],
                "corePIDs": self.core_pids})
        finally:
            self.stop_core()
            server.shutdown()
            server.server_close()
            thread.join(timeout=5)
            save_json(self.out / "provider-summary.json", {"errors": scenario.errors, "steps": scenario.steps,
                      "observedResults": scenario.observed, "httpGETs": server.gets})


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--binary", type=Path, required=True, help="already built/packaged hjh CLI; never auto-built")
    parser.add_argument("--out", type=Path, help="new directory for retained workspace/config/state/evidence")
    args = parser.parse_args()
    binary = args.binary.expanduser().resolve()
    require(binary.is_file() and os.access(binary, os.X_OK), f"not an executable CLI: {binary}")
    if args.out:
        out = args.out.expanduser().resolve()
        out.mkdir(parents=True, exist_ok=False)
    else:
        out = Path(tempfile.mkdtemp(prefix="hjh-stdlib-s9-"))
    summary = {"status": "failed", "scope": "deterministic compiled CLI/Core/SQLite acceptance; NOT real-model acceptance",
               "out": str(out), "binary": str(binary), "binarySHA256": hashlib.sha256(binary.read_bytes()).hexdigest(),
               "realModelTested": False, "tuiTested": False, "externalCredentialsUsed": False,
               "webSearch": {"tested": False, "reason": "Exa HTTPS endpoint is fixed; authenticated transport covered by app tests, not faked here"}}
    harness = None
    exit_code = 1
    try:
        harness = Harness(binary, out)
        harness.run(summary)
        exit_code = 0
    except Exception as error:
        summary["error"] = str(error)
        write(out / "failure.txt", traceback.format_exc())
    finally:
        if harness is not None:
            harness.stop_core()
        save_json(out / "summary.json", summary)
        print(json.dumps(summary, ensure_ascii=False, indent=2))
    return exit_code


if __name__ == "__main__":
    raise SystemExit(main())
