"""Manual product acceptance with real OpenRouter in a minimal Linux root.

Uses the selected credential privately; invokes paid model requests. Evidence
and workspace stay in --out, outside Git. Requires sudo chroot on the host.
"""
import argparse
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import threading

ROOT = Path(__file__).resolve().parent.parent


class Catalog(BaseHTTPRequestHandler):
    def do_GET(self):
        body = b'project: hjh-live\ndependencies:\n  - name: compatible\n    candidate: 1.4.2\n    range: "^1.0.0"\n'
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


def verify(archive, out, credential):
    out.mkdir(parents=True, exist_ok=True)
    root = out / "root"
    root.mkdir()  # Preserve any earlier verification rather than replacing it.
    with tarfile.open(archive) as tar:
        tar.extractall(root, filter="data")
    (root / "hjh-linux-amd64").rename(root / "app")
    app = root / "app"
    manifest = json.loads((app / "manifest.json").read_text())
    for name, metadata in manifest["files"].items():
        assert hashlib.sha256((app / name).read_bytes()).hexdigest() == metadata["sha256"], name
    (root / "work").mkdir()
    (root / "tmp").mkdir()
    (root / "etc/ssl/certs").mkdir(parents=True)
    for source, destination in [("/etc/ssl/certs/ca-certificates.crt", "etc/ssl/certs/ca-certificates.crt"), ("/etc/resolv.conf", "etc/resolv.conf"), ("/etc/hosts", "etc/hosts")]:
        shutil.copyfile(source, root / destination)
    (root / "work/input.txt").write_text("alpha\nbeta\nこんにちは\n")
    key = os.environ.get("OPENROUTER_API_KEY") or credential.read_text().strip()
    env = {"PATH": "/usr/bin:/bin", "HOME": "/tmp", "LANG": "C.UTF-8", "OPENROUTER_API_KEY": key}
    for name in ("HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY"):
        if name in os.environ:
            env[name] = os.environ[name]
    prefix = ["sudo", "-n", "--preserve-env=OPENROUTER_API_KEY,HTTPS_PROXY,HTTP_PROXY,ALL_PROXY,NO_PROXY", "chroot", f"--userspec={os.getuid()}:{os.getgid()}", str(root), "/app/hjh"]
    cases = []

    def command(name, args):
        process = subprocess.run([*prefix, *args], env=env, capture_output=True, timeout=180)
        assert key.encode() not in process.stdout + process.stderr, "credential exposed"
        (out / f"{name}.stdout.txt").write_bytes(process.stdout)
        (out / f"{name}.stderr.txt").write_bytes(process.stderr)
        if process.returncode != 0:
            raise RuntimeError(f"{name}: exit {process.returncode}: {process.stderr.decode()}")
        ids = {}
        for line in process.stderr.decode().splitlines():
            for label in ("Session", "Execution"):
                if line.startswith(label + ": "):
                    ids[label.lower()] = line.split(": ", 1)[1]
        cases.append({"name": name, "exitCode": process.returncode, **ids})
        return process.stdout, ids

    flags = ["--workspace", "/work", "--config", "/app/extensions/config.json"]
    _, first = command("first", ["run", *flags, 'run_jsを使ってinput.txtを読み、各行に「配布: 」を付けてoutput.txtへ書き、read_fileで確認し短く報告してください。探索コマンドは不要です。'])
    assert (root / "work/output.txt").read_text() == "配布: alpha\n配布: beta\n配布: こんにちは\n"
    # Source changes are made after the first process has completely ended.
    hooks = app / "extensions/hooks.js"
    hooks.write_text('export function afterTool({result}) { return result && typeof result === "object" ? {...result,hookRevision:"distributed-v2"} : result; }\n')
    tools = app / "extensions/index.js"
    tools.write_text(tools.read_text().replace('return api.readFile({ path: args.path });', 'return {...await api.readFile({ path: args.path }),toolRevision:"distributed-v2"};'))
    config_path = app / "extensions/config.json"
    config = json.loads(config_path.read_text())
    config["tools"].append({"name": "catalog", "description": "Read a YAML catalog and select compatible semver versions.", "inputSchema": {"type": "object", "properties": {"url": {"type": "string"}}, "required": ["url"]}, "module": "modules/catalog.js", "export": "catalog"})
    config_path.write_text(json.dumps(config, ensure_ascii=False, indent=2) + "\n")
    server = ThreadingHTTPServer(("127.0.0.1", 0), Catalog)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    try:
        task = ('追加探索は不要です。前回のoutput.txtをrun_jsで追加編集します。'
                'const before=await tools.read_file({path:"output.txt"});'
                'const edit=await tools.edit_file({path:"output.txt",oldText:"配布: ",newText:"再開: "});'
                'const after=await tools.read_file({path:"output.txt"});'
                f'const catalog=await tools.catalog({{url:"http://127.0.0.1:{server.server_port}/catalog"}});'
                'return {before,edit,after,catalog}; '
                '以上を実行し、読み戻しのtoolRevisionとhookRevision、catalogの互換versionを短く報告してください。')
        _, second = command("resume-edited", ["run", *flags, "--session", first["session"], task])
    finally:
        server.shutdown()
        server.server_close()
    assert (root / "work/output.txt").read_text() == "再開: alpha\n再開: beta\n再開: こんにちは\n"
    output, _ = command("sessions", ["sessions", "--workspace", "/work"])
    sessions = json.loads(output)
    assert len(sessions) == 1 and sessions[0]["TurnCount"] == 2
    output, _ = command("history", ["history", "--workspace", "/work", "--session", first["session"], "--from-turn", "1", "--to-turn", "1"])
    assert all(message["TurnNumber"] == 1 for message in json.loads(output))
    for label, ids in (("first", first), ("second", second)):
        output, _ = command(f"inspect-{label}", ["inspect", "--workspace", "/work", "--execution", ids["execution"]])
        inspection = json.loads(output)
        assert inspection["execution"]["Outcome"] == "completed"
        assert inspection["execution"]["Adoption"] == "canonical"
        if label == "second":
            assert "distributed-v2" in output.decode() and "1.4.2" in output.decode()
    db = root / "work/.hjh/history.sqlite3"
    assert key.encode() not in db.read_bytes(), "credential in DB"
    summary = {"target": "linux/amd64", "uname": list(os.uname()), "root": str(root), "procMounted": False,
               "developmentTools": [], "sharedLibraries": [], "archive": str(archive),
               "archiveSHA256": hashlib.sha256(archive.read_bytes()).hexdigest(),
               "binarySHA256": hashlib.sha256((app / "hjh").read_bytes()).hexdigest(), "cases": cases,
               "session": first["session"], "executions": [first["execution"], second["execution"]],
               "output": (root / "work/output.txt").read_text(), "credentialExposure": False}
    (out / "summary.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2) + "\n")
    return summary


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("archive", type=Path)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--credential-file", type=Path, default=Path(os.environ.get("XDG_STATE_HOME", str(Path.home() / ".local/state"))) / "henji-harness/v1/credentials/openrouter-api-key")
    args = parser.parse_args()
    result = verify(args.archive.resolve(), args.out.resolve(), args.credential_file)
    print(json.dumps(result, ensure_ascii=False, indent=2))
