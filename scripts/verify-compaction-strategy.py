#!/usr/bin/env python3
"""Compare compaction hooks through isolated Core/CLI/canonical history.

Default is deterministic loopback verification. --live opts into paid
opencode-go-chat / deepseek-v4.1-flash / auto requests. Preparation uses a local
provider through the same CLI/Core; it is excluded from measured live time.
--baseline-hook must point to the retained pre-change compaction.js for old/new
comparisons. All DBs, logs and numeric hook metrics remain in --output-dir.
No common Core, DB, configuration, deployed hooks or credential files are edited.
"""

import argparse
from collections import defaultdict
from datetime import datetime
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import sqlite3
import statistics
import subprocess
import tempfile
import threading
import time
from urllib.request import urlopen


ROOT = Path(__file__).resolve().parent.parent
EXPECTED = ("AX-314", "BY-271", "CZ-161", "amber")
UNRELATED = "UNRELATED_MUSEUM_998"
DROPPED = "RESTORE_EXCLUDED_ARCHIVE"
ANSWER = "AX-314 BY-271 CZ-161 amber"
TASK = ("継続中のrelease作業について、ALPHA_CODE・BETA_CODE・GAMMA_CODE・CHANNELの値を"
        "会話から引き継ぎ、その4つの値だけ回答してください。完了した博物館の別件は不要です。"
        "ファイルの再読取りは不要です。")
WITHIN_TASK = ("release作業です。e1.txtをread_fileで読み、NEXT_FILEがあればそのファイルを"
               "次のread_fileで順に読んでください。各ファイルはpathだけ指定して全文を1回ずつ読んでください。"
               "最後にALPHA_CODE・BETA_CODE・GAMMA_CODE・"
               "CHANNELの4つの値だけ回答してください。別件の博物館情報は不要です。")


def require(ok, message):
    if not ok:
        raise AssertionError(message)


def save(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n")


def page(tokens, header):
    """Deterministic UTF-8 data; size is hjh's approximate wire bytes/3 scale."""
    lines = [header + "\n"]
    remaining = max(0, int(tokens * 3) - len(lines[0].encode()))
    index = 0
    while remaining > 0:
        line = f"資料{index:06d}: 項目の確認は完了。保存済みの詳細を逐語引用する必要はありません。\n"
        data = line.encode()[:remaining].decode("utf-8", errors="ignore")
        if not data:
            break
        lines.append(data)
        remaining -= len(data.encode())
        index += 1
    return "".join(lines)


class LocalScenario:
    def __init__(self):
        self.seeds = {}
        self.growth = ""
        self.errors = []

    def next(self, wire):
        systems = "\n".join(m.get("content", "") for m in wire["messages"] if m["role"] == "system")
        if "Summarize this conversation" in systems:
            return {"content": "release継続: ALPHA_CODE=AX-314、BETA_CODE=BY-271、GAMMA_CODE=CZ-161、CHANNEL=amber。確認済みの値を使って回答する。"}
        task = next(m["content"] for m in reversed(wire["messages"]) if m["role"] == "user")
        if task in self.seeds:
            return {"content": self.seeds[task]}
        if task.startswith("GROW_"):
            return {"content": self.growth}
        if task == WITHIN_TASK:
            filename = "e1.txt"
            for message in reversed(wire["messages"]):
                if message["role"] != "tool":
                    continue
                match = re.search(r"NEXT_FILE: (e[23]\.txt)", message["content"])
                if match:
                    filename = match.group(1)
                else:
                    return {"content": ANSWER}
                break
            return {"tool_calls": [{"index": 0, "id": "read-" + filename,
                                     "type": "function", "function": {
                                         "name": "read_file", "arguments": json.dumps({"path": filename})}}]}
        require(task == TASK, "unrecognized local task")
        return {"content": ANSWER}


class Provider(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_POST(self):
        try:
            wire = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            delta = self.server.scenario.next(wire)
            event = {"id": "local-compaction", "model": "fixture", "choices": [{
                "index": 0, "delta": delta,
                "finish_reason": "tool_calls" if "tool_calls" in delta else "stop"}]}
            body = ("data: " + json.dumps(event, ensure_ascii=False) + "\n\ndata: [DONE]\n\n").encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        except Exception as error:
            self.server.scenario.errors.append(str(error))
            self.send_error(500, "local scenario failed")


WRAPPER = r'''
import { promises as fs } from "node:fs";
import { projectRequest as compact, policy } from "./compaction.js";
Object.assign(policy, OVERRIDES);
export async function projectRequest(input, api) {
  const summaries = [], measurements = [], loadedTurns = [];
  let savedState;
  const wrapped = { ...api, context: { ...api.context,
    history: async args => {
      const value = await api.context.history(args);
      if (value) loadedTurns.push(value.number);
      return value;
    },
    measure: async args => {
      const value = await api.context.measure(args);
      measurements.push({ estimatedTokens:value.estimatedTokens, wireBytes:value.wireBytes,
        fits:value.fits, messageCount:value.messageCount, limit:value.budget?.limit });
      return value;
    },
    memory: { ...api.context.memory, set: async args => {
      savedState = args.value;
      return await api.context.memory.set(args);
    } },
  }, model: { ...api.model, generate: async args => {
    const serialized = JSON.stringify(args.messages);
    const result = await api.model.generate(args);
    const u = result.usage ?? {};
    summaries.push({ characters:result.text.length,
      excludedArchiveInInput:serialized.includes("RESTORE_EXCLUDED_ARCHIVE"),
      alphaGuidanceInInput:serialized.includes("AX-314") && serialized.includes("amber"),
      usage:{prompt_tokens:u.prompt_tokens,completion_tokens:u.completion_tokens,
        total_tokens:u.total_tokens,prompt_tokens_details:u.prompt_tokens_details,
        completion_tokens_details:u.completion_tokens_details} });
    return result;
  } } };
  const compactStarted = Date.now();
  await compact(input, wrapped);
  const compactMilliseconds = Date.now()-compactStarted;
  const final = await api.context.get();
  const size = await api.context.measure({messages:final.messages});
  const state = savedState ?? await api.context.memory.get({key:MEMORY_KEY});
  const notes = final.messages.filter(m => m.role === "assistant" &&
    (m.content?.text ?? m.text ?? "").includes("Continuation summary"));
  const noteText = JSON.stringify(notes);
  const stateNotes = state?.summaries ?? (state?.summary ? [state.summary] : []);
  const metrics = {executionId:input.runtime.executionId,turn:input.runtime.turnNumber,
    step:input.modelStep,model:input.runtime.model,loadedTurns,measurements,summaries,compactMilliseconds,
    final:{estimatedTokens:size.estimatedTokens,wireBytes:size.wireBytes,limit:size.budget.limit,
      messageCount:size.messageCount}, lastCompaction:state?.lastCompaction,
    floorTurn:state?.floorTurn,covered:state?.covered,
    references:stateNotes.flatMap(n=>n.sources ?? []),
    // E's instruction to exclude museum information is still a valid current
    // constraint. Detect the discarded task's facts, not that constraint.
    unusedInSummary:/UNRELATED_MUSEUM_998|チケット.{0,12}予約|博物館.{0,12}予約/i.test(noteText),
    hasSummary:notes.length>0};
  await fs.writeFile(METRICS_DIR+"/"+input.runtime.executionId+"-"+input.modelStep+".json",
    JSON.stringify(metrics,null,2)+"\n");
}
'''


class Core:
    def __init__(self, args, directory, config):
        self.args, self.directory = args, directory
        self.db = directory / "history.sqlite3"
        self.workspace = directory / "workspace"
        self.config = config
        self.env = {**os.environ, "XDG_CONFIG_HOME": str(directory / "config"),
                    "XDG_STATE_HOME": str(directory / "state")}
        self.process = None
        self.generation = 0

    def start(self):
        self.generation += 1
        log_path = self.directory / f"core-{self.generation}.log"
        with log_path.open("w") as log:
            self.process = subprocess.Popen([str(self.args.binary), "serve", "--workspace", str(self.workspace),
                "--db", str(self.db), "--config", str(self.config), "--listen", "127.0.0.1:0"],
                env=self.env, cwd=self.workspace, stdout=log, stderr=log)
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            text = log_path.read_text()
            match = re.search(r"Core API listening at (http://127\.0\.0\.1:\d+)", text)
            if match:
                try:
                    with urlopen(match.group(1) + "/api/v2/core", timeout=1):
                        self.endpoint = match.group(1)
                        return
                except OSError:
                    pass
            require(self.process.poll() is None, "Core exited: " + text)
            time.sleep(.05)
        raise RuntimeError("Core startup timeout: " + text)

    def stop(self):
        if self.process is not None and self.process.poll() is None:
            self.process.terminate()
            self.process.wait(timeout=20)
        self.process = None

    def run(self, label, task, session=None, live=False):
        command = [str(self.args.binary), "run", "--connect", self.endpoint, "--max-steps", "8"]
        if session:
            command += ["--session", session]
        command += (["--provider", "opencode-go-chat", "--model", "deepseek-v4.1-flash",
                     "--auth-profile", "opencode-go-api-key", "--effort", "auto"] if live else
                    ["--provider", "measurement-local", "--model", "fixture", "--effort", "auto"])
        started = time.monotonic()
        result = subprocess.run(command + [task], env=self.env, cwd=self.workspace,
                                capture_output=True, text=True, timeout=300)
        elapsed = time.monotonic() - started
        (self.directory / (label + ".stdout")).write_text(result.stdout)
        (self.directory / (label + ".stderr")).write_text(result.stderr)
        require(result.returncode == 0, f"{label} failed; see {self.directory}/{label}.stderr")
        with sqlite3.connect(self.db) as db:
            row = db.execute("SELECT session_correlation,execution_id,turn_number FROM executions "
                             "ORDER BY created_at DESC LIMIT 1").fetchone()
        record = {"sessionId": row[0], "executionId": row[1], "turn": row[2],
                  "seconds": elapsed, "answer": result.stdout.strip()}
        save(self.directory / (label + ".json"), record)
        return record


def hook(ext, source, overrides, metrics, version):
    (ext / "compaction.js").write_text(source)
    wrapper = WRAPPER.replace("OVERRIDES", json.dumps(overrides)).replace(
        "MEMORY_KEY", json.dumps("compaction-v2" if version == "new" else "compaction-v1")).replace(
        "METRICS_DIR", json.dumps(str(metrics)))
    (ext / "hooks.js").write_text(wrapper)


def durations(dbpath, execution):
    starts, hooks, summaries = {}, {}, []
    with sqlite3.connect(dbpath) as db:
        for observed_at, payload in db.execute("SELECT observed_at,payload_json FROM semantic_records "
                                               "WHERE execution_id=? ORDER BY ordinal", (execution,)):
            item = json.loads(payload)
            if item.get("name") not in ("projectRequest", "context.model.generate"):
                continue
            at = datetime.fromisoformat(observed_at.replace("Z", "+00:00")).timestamp()
            if item.get("phase") == "started":
                starts[item["id"]] = (at, item)
            elif item.get("phase") == "completed" and item.get("id") in starts:
                start, _ = starts[item["id"]]
                if item["name"] == "projectRequest":
                    hooks[item["id"]] = at - start
                else:
                    summaries.append({"id": item["id"], "parent": item["parent"], "seconds": at - start})
    grouped = defaultdict(list)
    for summary in summaries:
        grouped[summary["parent"]].append(summary["seconds"])
    return [{"hookId": parent, "hookSeconds": hooks[parent], "summarySeconds": values,
             "summaryTotalSeconds": sum(values), "otherSeconds": hooks[parent] - sum(values)}
            for parent, values in grouped.items()]


def storage_checks(core, metrics, seeds, within):
    with sqlite3.connect(core.db) as db:
        require(db.execute("SELECT count(*) FROM executions WHERE parent_execution_id IS NOT NULL "
                           "AND parent_execution_id!=''").fetchone()[0] == 0, "unexpected child execution")
        for item in metrics:
            for ref in item.get("references", []):
                for first, last in ref.get("messageRanges", []):
                    count = db.execute("SELECT count(*) FROM messages WHERE execution_id=? "
                                       "AND message_ordinal BETWEEN ? AND ?",
                                       (ref["executionId"], first, last)).fetchone()[0]
                    require(count == last - first + 1, "unresolvable source range")
                if "finalMessage" in ref:
                    row = db.execute("SELECT content_bytes FROM messages JOIN contents USING(content_digest) "
                                     "WHERE execution_id=? AND message_ordinal=?",
                                     (ref["executionId"], ref["finalMessage"])).fetchone()
                    message = json.loads(row[0])
                    require(message["role"] == "assistant" and isinstance(message["content"], dict),
                            "final locator is not an ordinary assistant response")
        require(not db.execute("SELECT count(*) FROM conversation_messages cm JOIN messages m ON "
            "m.execution_id=cm.execution_id AND m.message_ordinal=cm.execution_message_ordinal "
            "JOIN contents c USING(content_digest) WHERE CAST(c.content_bytes AS TEXT) LIKE '%Continuation summary:%'"
        ).fetchone()[0], "summary was written into canonical conversation")
        canonical = [json.loads(row[0]) for row in db.execute(
            "SELECT c.content_bytes FROM conversation_messages cm JOIN messages m ON "
            "m.execution_id=cm.execution_id AND m.message_ordinal=cm.execution_message_ordinal "
            "JOIN contents c USING(content_digest)")]
        assistant_texts = [message["content"].get("text") for message in canonical
                           if message["role"] == "assistant" and isinstance(message["content"], dict)]
        require(all(text in assistant_texts for text in seeds.values()), "canonical seed original changed or missing")
        if within:
            read_texts = []
            for message in canonical:
                if message["role"] != "tool":
                    continue
                for result in message["content"]:
                    try:
                        if "textRef" in result:
                            stored = db.execute("SELECT content_bytes FROM contents WHERE content_digest=?",
                                                (result["textRef"],)).fetchone()
                            require(stored is not None, "missing stored tool text")
                            value = json.loads(stored[0])
                        else:
                            value = json.loads(result["text"])
                    except (json.JSONDecodeError, KeyError):
                        continue
                    if isinstance(value, dict) and "text" in value:
                        read_texts.append(value["text"])
            require(all((core.workspace / f"e{number}.txt").read_text() in read_texts
                        for number in (1, 2, 3)), "canonical read_file original changed or missing")
        return {"referencesResolve": True, "originalsPreserved": True, "childExecutions": 0}


def one(args, directory, server, scenario, mode, scale, version, repetition):
    directory.mkdir()
    work, metricsdir = directory / "workspace", directory / "metrics"
    ext = work / ".hjh/extensions"
    shutil.copytree(ROOT / "internal/extensions/assets", ext)
    metricsdir.mkdir()
    source = ((ROOT / "examples/hooks/compaction.js") if version == "new" else args.baseline_hook).read_text()
    # The observed wire adds ~2% newline framing plus ~1,500 tokens of role /
    # read_file definitions. Leave A-D below L while still above its .9 trigger.
    limit = int((scale * 1.025 + 1600) / .94)
    seeds = {}
    if mode != "within":
        if mode == "restore":
            seeds["SEED_Z"] = page(limit * .35, DROPPED + ": 完了済みで不要な遠い資料。")
        for letter, facts in (("A", "ALPHA_CODE: AX-314\nCHANNEL: amber\nこの指針はrelease作業で今後も使用する。"),
                              ("B", UNRELATED + ": 博物館のチケット予約は完了。releaseとは無関係。"),
                              ("C", "BETA_CODE: BY-271\nrelease確認済みの値。"),
                              ("D", "GAMMA_CODE: CZ-161\n次はreleaseの4つの値を回答する。")):
            seeds["SEED_" + letter] = page(scale / 4, facts)
    scenario.seeds = seeds
    scenario.growth = page(limit * .07, ANSWER + "\n継続作業の記録。")
    (work / "e1.txt").write_text(page(limit * .64,
        "ALPHA_CODE: AX-314\nCHANNEL: amber\nNEXT_FILE: e2.txt\n" + UNRELATED + ": 完了した別件。"))
    (work / "e2.txt").write_text(page(limit * .27, "BETA_CODE: BY-271\nNEXT_FILE: e3.txt"))
    (work / "e3.txt").write_text("GAMMA_CODE: CZ-161\nrelease確認は完了。\n")
    config = json.loads((ext / "config.json").read_text())
    live = config["providers"]["opencode-go-chat"]
    config["providers"] = {"opencode-go-chat": live, "measurement-local": {
        "api": "chat-completions", "baseURL": f"http://127.0.0.1:{server.server_port}",
        "path": "/chat/completions", "auth": {"kind": "none"}, "defaultModel": "fixture",
        "models": [{"id": "fixture"}]}}
    config["model"] = {"provider": "measurement-local", "modelId": "fixture", "effort": "auto"}
    config["credentials"] = {"opencode-go-api-key": {
        "env": "OPENCODE_GO_API_KEY", "file": str(args.credential_file)}}
    config["contextBudget"] = {"defaults": {"inputTokens": limit}}
    config["defaultRole"] = "compaction-measurement"
    config["roles"] = {"compaction-measurement": {
        "instruction": "Follow the current task. Retain exact release identifiers and adopted guidance. "
                       "Ignore completed unrelated errands. Read files only when requested. Answer concisely.",
        "tools": ["read_file"]}}
    save(ext / "config.json", config)
    hook(ext, source, {"triggerTokens": 10**9, "triggerRatio": 10**9}, metricsdir, version)
    core = Core(args, directory, ext / "config.json")
    measured, growth = None, []
    try:
        core.start()
        session = None
        for index, task in enumerate(seeds):
            # The Z turn is intentionally beyond the eventual input budget.
            # Disable hooks for that seeding phase, then warm the v2 memory on
            # a new Session's A turn in ordinary between-turn measurements.
            if mode == "restore":
                (ext / "hooks.js").write_text("export function projectRequest() {}\n")
            result = core.run("prepare-" + str(index), task, session)
            session = result["sessionId"]
        overrides = {"summaryCharacters": args.summary_characters}
        hook(ext, source, overrides, metricsdir, version)
        if mode == "restore":
            core.stop()
            core.start()
        measured = core.run("measured", WITHIN_TASK if mode == "within" else TASK, session, args.live)
        require(all(value in measured["answer"] for value in EXPECTED), "continuation lost required values")
        session = measured["sessionId"]
        for number in range(args.growth_turns):
            growth.append(core.run(f"growth-{number+1}", f"GROW_{number+1}: 固定の継続記録を追加。", session))
        core.stop()
        metrics = sorted((json.loads(path.read_text()) for path in metricsdir.glob("*.json")),
                         key=lambda item: (item["turn"], item["step"]))
        selected = [item for item in metrics if item["executionId"] == measured["executionId"]]
        compacted = [item for item in selected if item["summaries"]]
        require(compacted, "no compaction occurred; adjust the observed input/threshold relation")
        if version == "new":
            require(all(not item["unusedInSummary"] for item in compacted), "unrelated detail retained in summary")
            require(all(len(item["summaries"]) <= 2 for item in selected), "more than two summary calls")
            if mode == "restore":
                require(1 in selected[0]["loadedTurns"], "restore fixture never reached the excluded Z turn")
                require(all(not summary["excludedArchiveInInput"] for item in selected
                            for summary in item["summaries"]), "pre-trimmed history entered summarizer")
                require(selected[0]["summaries"][0]["alphaGuidanceInInput"],
                        "restore fixture dropped required A guidance before summarization")
        timings = durations(core.db, measured["executionId"])
        require(len(timings) == len(compacted), "hook timing/compaction count mismatch")
        checks = storage_checks(core, metrics, seeds, mode == "within")
        recurrence = []
        for result in growth:
            steps = [item for item in metrics if item["executionId"] == result["executionId"]]
            recurrence.append({"turn": result["turn"], "summaryCalls": sum(len(s["summaries"]) for s in steps),
                               "finalTokens": steps[-1]["final"]["estimatedTokens"]})
        report = {"mode": mode, "scaleEstimatedTokens": scale, "version": version, "repeat": repetition,
                  "live": args.live, "inputLimit": limit, "triggerTokens": limit * .9,
                  "summaryCharacters": args.summary_characters,
                  "targetTokens": limit // 3 if version == "new" else limit * .6,
                  "measured": measured, "steps": selected, "timings": timings,
                  "recurrence": recurrence, "checks": checks, "evidence": str(directory)}
        report["sourceSha256"] = hashlib.sha256(source.encode()).hexdigest()
        report["binarySha256"] = hashlib.sha256(args.binary.read_bytes()).hexdigest()
        report["timingScope"] = {"hookSeconds": "persisted outer hook including metric wrapper I/O",
                                 "compactMilliseconds": "compact() only; Date.now millisecond wall-clock resolution",
                                 "summarySeconds": "persisted Host call; provider/transport/bridge included"}
        save(directory / "report.json", report)
        return report
    finally:
        core.stop()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--baseline-hook", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--resume", type=Path,
                        help="Resume remaining cases using reports already saved in this evidence directory")
    parser.add_argument("--credential-file", type=Path,
                        default=Path.home() / ".config/hjh/credentials/opencode-go-api-key")
    parser.add_argument("--live", action="store_true")
    parser.add_argument("--sizes", default="30000,120000")
    parser.add_argument("--modes", default="between,within,restore")
    parser.add_argument("--versions", default="old,new")
    parser.add_argument("--repeats", type=int, default=3)
    parser.add_argument("--summary-characters", type=int, default=700,
                        help="Soft summary length used equally for old/new (product default is 1200)")
    parser.add_argument("--growth-turns", type=int, default=0,
                        help="Deterministic local continuation after measurement; separate from live timings")
    args = parser.parse_args()
    args.binary, args.baseline_hook = args.binary.resolve(), args.baseline_hook.resolve()
    args.output_dir.mkdir(parents=True, exist_ok=True)
    output = args.resume.resolve() if args.resume else Path(tempfile.mkdtemp(
        prefix="compaction-", dir=args.output_dir.resolve()))
    require(output.is_dir(), "resume evidence directory does not exist")
    scenario = LocalScenario()
    server = ThreadingHTTPServer(("127.0.0.1", 0), Provider)
    server.scenario = scenario
    threading.Thread(target=server.serve_forever, daemon=True).start()
    reports = json.loads((output / "reports.json").read_text()) if args.resume else []
    completed = {(r["mode"], r["scaleEstimatedTokens"], r["version"], r["repeat"]) for r in reports}
    print(json.dumps({"output": str(output), "live": args.live}), flush=True)
    try:
        for size in map(int, args.sizes.split(",")):
            for mode in args.modes.split(","):
                for repetition in range(1, args.repeats + 1):
                    versions = args.versions.split(",")
                    if repetition % 2 == 0:
                        versions.reverse()
                    for version in versions:
                        if (mode, size, version, repetition) in completed:
                            continue
                        directory = output / f"{mode}-{size}-{repetition}-{version}"
                        attempt = 1
                        while directory.exists():
                            attempt += 1
                            directory = output / f"{mode}-{size}-{repetition}-{version}-attempt{attempt}"
                        report = one(args, directory, server, scenario, mode, size, version, repetition)
                        reports.append(report)
                        save(output / "reports.json", reports)
                        print(json.dumps({"mode": mode, "size": size, "repeat": repetition,
                            "version": version, "hookSeconds": [round(t["hookSeconds"], 3) for t in report["timings"]],
                            "summarySeconds": [round(t["summaryTotalSeconds"], 3) for t in report["timings"]],
                            "finalTokens": report["steps"][-1]["final"]["estimatedTokens"]}), flush=True)
        groups = defaultdict(list)
        for report in reports:
            groups[(report["mode"], report["scaleEstimatedTokens"], report["version"])].extend(
                timing["hookSeconds"] for timing in report["timings"])
        save(output / "aggregate.json", [{"mode": key[0], "scaleEstimatedTokens": key[1], "version": key[2],
            "samples": len(values), "medianSeconds": statistics.median(values),
            "minSeconds": min(values), "maxSeconds": max(values)} for key, values in groups.items()])
        require(not scenario.errors, str(scenario.errors))
    finally:
        server.shutdown()
        server.server_close()
    print(json.dumps({"complete": True, "reports": str(output / "reports.json")}), flush=True)


if __name__ == "__main__":
    main()
