#!/usr/bin/env python3
"""Paid qualitative task comparison through isolated Core/CLI sessions.

Uses OpenCode Go / DeepSeek V4.1 Flash for real task execution and summaries.
Identical synthetic A-D histories are prepared through a loopback provider and
the normal CLI, not by modifying the history DB. See the results document for
scope and interpretation. Common configuration and deployed hooks are untouched.
"""

import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import shutil
import sqlite3
import tempfile
import threading
from http.server import ThreadingHTTPServer


ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("measurement", ROOT / "scripts/verify-compaction-strategy.py")
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)


A = """リリース手順の設計を確定します。初案のchannel=blueと130ms基準は未採用です。
採用はchannel=amber、strict_order=true、notify=ops-ja。regionの順序はosaka,tokyo。
成果物rollout-plan.jsonはトップレベルにschema,channel,strict_order,notify,regionsだけを持ちます。
schema=rollout-plan/v2。regionsは{name,release,rollback,percent}の配列です。
候補はstatus=passed、p95_ms<=125だけを対象にし、regionごとにbuild番号最大を選びます。
rollback先はosaka=rel-730、tokyo=rel-840で固定。初期percentは両regionとも10。
125msを選んだ根拠の試験IDはPERF-Q17-125で、ピーク時間帯の再試験で130ms案にタイムアウトが出たためです。
この判断は採用済みです。ここでは計画を確認してください。"""
B = """別件の図書館搬入は完了しました。予約番号LIB-6092、搬入口は西口3、担当は森さん。
リリースとは関係ない完了済みの用事です。記録を確認してください。"""
C = """リリースの候補一覧の照合が完了しました。candidates.csvを後で使えます。
例外としてosakaのrel-736はロールバック試験RB-88で不合格です。
CSVのstatusはpassedのままですが、この候補は必ず除外してください。
rel-735は128msなので130msの旧案なら通りますが、採用した125msでは除外です。
検証結果とこの例外を引き継いでください。"""
D = """リリースの最終訂正です。tokyoは監査待ちなので初期percentを10から5に変更。
osakaは10のまま。channel=amber、125ms、rel-736除外、rollback先とregion順序は変更しません。
この訂正は採用済み。次の依頼で合意したschemaの成果物を作ります。"""
TASKS = [
    ("create", "合意済みの仕様・確認結果・最終訂正を使い、candidates.csvを読んでrollout-plan.jsonを完成させてください。必要なファイル操作を実行してください。最後に採用した候補と主な除外理由を簡潔に報告してください。別件の搬入作業は対象外です。"),
    ("update", "tokyoの監査が解除されました。rollout-plan.jsonのtokyoだけpercentを25に変更してください。osakaのpercentと他の全項目は維持してください。実際に保存し、変更点だけ簡潔に報告してください。"),
    ("recall", "過去の会話について確認します。①130ms案ではなく125msを採用した理由と、その根拠の試験ID。②別件で完了した図書館搬入の予約番号・搬入口・担当者。この2点を会話の根拠だけから回答してください。分からない詳細は不明と明記し、推測や外部調査はしないでください。ファイル変更は不要です。"),
]
CSV = """region,build,release,status,p95_ms
osaka,733,rel-733,passed,121
osaka,734,rel-734,passed,123
osaka,735,rel-735,passed,128
osaka,736,rel-736,passed,119
tokyo,841,rel-841,passed,120
tokyo,842,rel-842,passed,124
tokyo,843,rel-843,passed,126
tokyo,844,rel-844,failed,118
"""


def history_reply(letter, text):
    # Plausible bulky archived inspection output; all decision facts are in the
    # actual user tasks above. Synthetic output is identical in both arms.
    lines = [text, "\n添付の過去検査ログ（個別行は作業済み）:"]
    for i in range(110):
        lines.append(f"{letter}-check-{i:03d}: artifact={letter.lower()}-{i:03d}.dat; bytes={8400+i*17}; digest-prefix={hashlib.sha256(f'{letter}:{i}'.encode()).hexdigest()[:12]}; 状態=照合完了; 再処理なし。")
    return "\n".join(lines)


HOOK = r'''
import {promises as fs} from "node:fs";
import {projectRequest as compact,policy} from "./compaction.js";
Object.assign(policy, OVERRIDES);
const visible = messages => messages.map(({providerState,...message})=>message);
export async function projectRequest(input,api) {
  const summaries=[];
  const wrapped={...api,model:{...api.model,generate:async args=>{
    const started=Date.now();
    const result=await api.model.generate(args);
    summaries.push({seconds:(Date.now()-started)/1000,instruction:args.instruction,
      input:visible(args.messages),text:result.text,usage:result.usage});
    return result;
  }}};
  const started=Date.now();
  if (ENABLED) await compact(input,wrapped);
  const compactSeconds=(Date.now()-started)/1000;
  // Detached normal selection: in the OFF arm this observes the same full
  // history the ordinary projector will use, without staging a replacement.
  const selected=await api.context.select();
  const state=await api.context.memory.get({key:"compaction-v2"});
  await fs.writeFile(METRICS+"/"+input.runtime.executionId+"-"+input.modelStep+".json",
    JSON.stringify({executionId:input.runtime.executionId,turn:input.runtime.turnNumber,
      step:input.modelStep,compactSeconds,summaries,lastCompaction:state?.lastCompaction,
      estimatedTokens:selected.estimatedTokens,fits:selected.fits,
      historyStatus:selected.historyStatus,omitted:selected.omitted,
      messages:visible(selected.messages)},null,2)+"\n");
}
'''


def install(ext, metrics, enabled, warm=False):
    overrides = {"triggerTokens": 10**9, "targetTokens": 10**9} if warm else {
        "triggerTokens": 10000, "targetTokens": 4500}
    (ext / "hooks.js").write_text(HOOK.replace("OVERRIDES", json.dumps(overrides))
        .replace("ENABLED", "true" if enabled else "false")
        .replace("METRICS", json.dumps(str(metrics))))


def canonical_messages(dbpath, execution):
    with sqlite3.connect(dbpath) as db:
        return [json.loads(row[0]) for row in db.execute(
            "SELECT c.content_bytes FROM messages m JOIN contents c USING(content_digest) "
            "WHERE m.execution_id=? ORDER BY m.message_ordinal", (execution,))]


def artifact_checks(value, updated):
    expected = {"schema": "rollout-plan/v2", "channel": "amber", "strict_order": True,
                "notify": "ops-ja", "regions": [
                    {"name": "osaka", "release": "rel-734", "rollback": "rel-730", "percent": 10},
                    {"name": "tokyo", "release": "rel-842", "rollback": "rel-840", "percent": 25 if updated else 5}]}
    return {"matchesAgreedPlan": value == expected, "expected": expected}


def one(args, directory, scenario, server, enabled, repetition):
    directory.mkdir()
    ext = directory / "workspace/.hjh/extensions"
    shutil.copytree(ROOT / "internal/extensions/assets", ext)
    shutil.copy2(ROOT / "examples/hooks/compaction.js", ext / "compaction.js")
    metrics = directory / "metrics"
    metrics.mkdir()
    config = json.loads((ext / "config.json").read_text())
    config["providers"] = {"opencode-go-chat": config["providers"]["opencode-go-chat"],
        "measurement-local": {"api": "chat-completions", "baseURL": f"http://127.0.0.1:{server.server_port}",
            "path": "/chat/completions", "auth": {"kind": "none"}, "defaultModel": "fixture", "models": [{"id": "fixture"}]}}
    config["model"] = {"provider": "measurement-local", "modelId": "fixture", "effort": "auto"}
    config["credentials"] = {"opencode-go-api-key": {
        "env": "OPENCODE_GO_API_KEY", "file": str(args.credential_file)}}
    config["contextBudget"] = {"defaults": {"inputTokens": 60000}}
    config["defaultRole"] = "behavior-observation"
    config["roles"] = {"behavior-observation": {
        "instruction": "利用者の採用済み方針と最新の訂正に従い、必要なファイル作業を実行する。未採用案と実行済みの結果を区別する。回答は日本語で簡潔に。過去の詳細が不明なら推測せず明記する。",
        "tools": ["read_file", "write_file"]}}
    m.save(ext / "config.json", config)
    (directory / "workspace/candidates.csv").write_text(CSV)
    scenario.seeds = {task: history_reply(letter, "記録済み。\n" + task)
                      for letter, task in zip("ABCD", (A, B, C, D))}
    install(ext, metrics, True, warm=True)
    core = m.Core(args, directory, ext / "config.json")
    report = {"compaction": enabled, "repeat": repetition, "evidence": str(directory), "turns": []}
    try:
        core.start()
        session = None
        for index, task in enumerate(scenario.seeds):
            prepared = core.run(f"prepare-{index+1}", task, session)
            session = prepared["sessionId"]
        install(ext, metrics, enabled)
        for label, task in TASKS:
            result = core.run(label, task, session, live=True)
            session = result["sessionId"]
            messages = canonical_messages(core.db, result["executionId"])
            tool_calls = [item for message in messages if message["role"] == "assistant"
                          and isinstance(message.get("content"), list) for item in message["content"]
                          if item.get("kind") == "tool_call"]
            steps = sorted((json.loads(p.read_text()) for p in metrics.glob(result["executionId"] + "-*.json")),
                           key=lambda value: value["step"])
            entry = {"label": label, "task": task, **result, "toolCalls": tool_calls,
                     "steps": steps, "timings": m.durations(core.db, result["executionId"])}
            artifact = directory / "workspace/rollout-plan.json"
            if artifact.exists():
                text = artifact.read_text()
                (directory / f"{label}-artifact.json").write_text(text)
                try:
                    entry["artifact"] = json.loads(text)
                    entry["checks"] = artifact_checks(entry["artifact"], label != "create")
                except json.JSONDecodeError:
                    entry["artifactText"] = text
                    entry["checks"] = {"matchesAgreedPlan": False}
            report["turns"].append(entry)
            m.save(directory / "report.json", report)
            print(json.dumps({"repeat": repetition, "compaction": enabled, "label": label,
                "seconds": round(result["seconds"], 2), "checks": entry.get("checks"),
                "summaryCalls": sum(len(step["summaries"]) for step in steps)}, ensure_ascii=False), flush=True)
    finally:
        core.stop()
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--credential-file", type=Path, default=Path("/home/agent/.config/hjh/credentials/opencode-go-api-key"))
    parser.add_argument("--repeats", type=int, default=2)
    args = parser.parse_args()
    args.output_dir.mkdir(parents=True, exist_ok=True)
    scenario = m.LocalScenario()
    server = ThreadingHTTPServer(("127.0.0.1", 0), m.Provider)
    server.scenario = scenario
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    root = Path(tempfile.mkdtemp(prefix="behavior-", dir=args.output_dir))
    reports = []
    print(str(root), flush=True)
    try:
        for repetition in range(1, args.repeats + 1):
            for enabled in ([False, True] if repetition % 2 else [True, False]):
                reports.append(one(args, root / f"repeat-{repetition}-{'on' if enabled else 'off'}",
                                   scenario, server, enabled, repetition))
                m.save(root / "reports.json", reports)
        m.require(not scenario.errors, f"preparation provider errors: {scenario.errors}")
    finally:
        server.shutdown()
        server.server_close()


if __name__ == "__main__":
    main()
