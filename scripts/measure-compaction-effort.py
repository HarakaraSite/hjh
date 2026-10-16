#!/usr/bin/env python3
"""Replay an actual compaction summary request with auto/max, using live API.

Uses the production Core/CLI and api.model.generate inheritance path. Within a
series only the parent effort changes. Source prompt comes from observe-compaction-behavior.py's
saved reports.json. Separate workspaces/DBs/configs; common settings untouched.
"""

import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import shutil
import sqlite3
import statistics
import subprocess
import tempfile
import time
import uuid


ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("measurement", ROOT / "scripts/verify-compaction-strategy.py")
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)

HOOK = r'''
import {promises as fs} from "node:fs";
export async function projectRequest(input,api) {
  const fixture=JSON.parse(await fs.readFile(FIXTURE,"utf8"));
  const started=Date.now();
  const result=await api.model.generate(fixture);
  const seconds=(Date.now()-started)/1000;
  await fs.writeFile(RESULT,JSON.stringify({executionId:input.runtime.executionId,
    parentModel:input.runtime.model,seconds,text:result.text,
    characters:result.text.length,usage:result.usage,finishReason:result.finishReason},null,2)+"\n");
  // Finish the normal run with a tiny response. Its time is outside seconds.
  await api.context.replace({instruction:"Reply with exactly DONE.",
    messages:[{role:"user",content:{kind:"text",text:"DONE"}}]});
}
'''


def one(args, directory, fixture, effort, repetition):
    directory.mkdir()
    ext = directory / "workspace/.hjh/extensions"
    shutil.copytree(ROOT / "internal/extensions/assets", ext)
    m.save(directory / "fixture.json", fixture)
    resultpath = directory / "summary.json"
    (ext / "hooks.js").write_text(HOOK.replace("FIXTURE", json.dumps(str(directory / "fixture.json")))
                                 .replace("RESULT", json.dumps(str(resultpath))))
    config = json.loads((ext / "config.json").read_text())
    config["providers"] = {args.provider: config["providers"][args.provider]}
    config["credentials"] = {"opencode-go-api-key": {
        "env": "OPENCODE_GO_API_KEY", "file": str(args.credential_file)}}
    config["model"] = {"provider": args.provider, "modelId": args.model, "effort": effort}
    config["defaultRole"] = "summary-effort-measurement"
    config["roles"] = {"summary-effort-measurement": {"instruction": "Follow the task.", "tools": []}}
    config["contextBudget"] = {"defaults": {"inputTokens": 60000}}
    m.save(ext / "config.json", config)
    core = m.Core(args, directory, ext / "config.json")
    try:
        core.start()
        command = [str(args.binary), "run", "--connect", core.endpoint, "--max-steps", "1",
                   "--provider", args.provider, "--model", args.model,
                   "--auth-profile", "opencode-go-api-key", "--effort", effort, "Measure the saved summary request."]
        started = time.monotonic()
        run = subprocess.run(command, env=core.env, cwd=core.workspace,
                             capture_output=True, text=True, timeout=900)
        (directory / "run.stdout").write_text(run.stdout)
        (directory / "run.stderr").write_text(run.stderr)
        m.require(resultpath.exists(), f"No summary result; see {directory}")
        result = json.loads(resultpath.read_text())
        result.update({"effort": effort, "repeat": repetition, "evidence": str(directory),
                       "wholeRunSeconds": time.monotonic()-started, "runExitCode": run.returncode,
                       "fixtureSha256": hashlib.sha256(json.dumps(fixture, ensure_ascii=False, sort_keys=True).encode()).hexdigest()})
        result["timings"] = m.durations(core.db, result["executionId"])
        with sqlite3.connect(core.db) as db:
            selections = []
            for (raw,) in db.execute("SELECT payload_json FROM semantic_records WHERE execution_id=?",
                                     (result["executionId"],)):
                payload = json.loads(raw)
                if payload.get("name") == "run" and payload.get("phase") == "started":
                    selections.append(payload.get("input", {}).get("model"))
            result["recordedSelections"] = selections
        m.require(result["parentModel"]["effort"] == effort, "Parent effort mismatch")
        m.require(result["parentModel"]["provider"] == args.provider, "Parent provider mismatch")
        m.require(result["parentModel"]["modelId"] == args.model, "Parent model mismatch")
        m.require(len(result["timings"]) == 1 and len(result["timings"][0]["summarySeconds"]) == 1,
                  "Expected exactly one measured summary call")
        m.save(directory / "result.json", result)
        return result
    finally:
        core.stop()


def aggregate(reports):
    rows = []
    for effort in ("auto", "max"):
        selected = [r for r in reports if r["effort"] == effort]
        if not selected:
            continue
        values = [r["seconds"] for r in selected]
        rows.append({"effort": effort, "samples": len(values), "medianSeconds": statistics.median(values),
                     "minSeconds": min(values), "maxSeconds": max(values),
                     "allSeconds": values,
                     "allReasoningTokens": [r.get("usage", {}).get("completion_tokens_details",
                         r.get("usage", {}).get("output_tokens_details", {})).get("reasoning_tokens") for r in selected]})
    return rows


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--source-reports", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--provider", choices=("opencode-go-chat", "opencode-go-responses"), default="opencode-go-chat")
    parser.add_argument("--model", default="deepseek-v4.1-flash")
    parser.add_argument("--credential-file", type=Path, default=Path.home() / ".config/hjh/credentials/opencode-go-api-key")
    parser.add_argument("--repeats", type=int, default=3)
    parser.add_argument("--cache-mode", choices=("natural", "fresh-prefix"), default="natural",
                        help="fresh-prefix prepends a unique nonsemantic run ID to avoid input-prefix cache reuse")
    args = parser.parse_args()
    args.binary = args.binary.resolve()
    args.output_dir.mkdir(parents=True, exist_ok=True)
    root = Path(tempfile.mkdtemp(prefix="effort-", dir=args.output_dir.resolve()))
    previous = json.loads(args.source_reports.read_text())
    original = next(su for r in previous if r["compaction"] and r["repeat"] == 1
                    for turn in r["turns"] if turn["label"] == "create"
                    for step in turn["steps"] for su in step["summaries"])
    fixture = {"instruction": original["instruction"], "messages": original["input"]}
    m.save(root / "fixture.json", fixture)
    metadata = {"sourceReports": str(args.source_reports.resolve()),
                "fixtureSha256": hashlib.sha256(json.dumps(fixture, ensure_ascii=False, sort_keys=True).encode()).hexdigest(),
                "binarySha256": hashlib.sha256(args.binary.read_bytes()).hexdigest(),
                "summaryCharacters": 1200, "provider": args.provider, "modelId": args.model,
                "cacheMode": args.cache_mode,
                "measurement": "api.model.generate elapsed time including provider/transport/bridge; final DONE excluded"}
    m.save(root / "metadata.json", metadata)
    print(str(root), flush=True)
    reports = []
    for repetition in range(1, args.repeats+1):
        for effort in (("auto", "max") if repetition % 2 else ("max", "auto")):
            measured_fixture = fixture
            if args.cache_mode == "fresh-prefix":
                measured_fixture = {**fixture, "instruction":
                    "Measurement ID: " + uuid.uuid4().hex + ". Ignore this ID in the summary.\n" + fixture["instruction"]}
            report = one(args, root / f"repeat-{repetition}-{effort}", measured_fixture, effort, repetition)
            reports.append(report)
            m.save(root / "reports.json", reports)
            m.save(root / "aggregate.json", aggregate(reports))
            print(json.dumps({k:report[k] for k in ("effort", "repeat", "seconds", "characters", "usage", "runExitCode")}, ensure_ascii=False), flush=True)
    print(json.dumps(aggregate(reports), ensure_ascii=False), flush=True)


if __name__ == "__main__":
    main()
