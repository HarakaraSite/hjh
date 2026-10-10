"""Run the extracted archive inside a fresh, command-free Linux root."""
import argparse
from datetime import datetime, timezone
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import shutil
import signal
import ssl
import subprocess
import tarfile
import tempfile
import threading

HERE = Path(__file__).resolve().parent
MODULE = HERE.parent


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write_json(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n")


def inventory(root):
    return ["/" + str(p.relative_to(root)) for p in sorted(root.rglob("*")) if p.is_file()]


class Server(BaseHTTPRequestHandler):
    requests = []
    body = (HERE / "catalog.yaml").read_bytes()

    def do_GET(self):
        self.requests.append({"path": self.path, "tls": isinstance(self.connection, ssl.SSLSocket)})
        self.send_response(200)
        self.send_header("Content-Type", "application/yaml")
        self.send_header("Content-Length", str(len(self.body)))
        self.end_headers()
        self.wfile.write(self.body)

    def log_message(self, *args):
        pass


def verify(archive, out, work):
    out.mkdir(parents=True, exist_ok=True)
    work.mkdir(parents=True, exist_ok=True)
    root = Path(tempfile.mkdtemp(prefix="root-", dir=work))
    with tarfile.open(archive) as tar:
        tar.extractall(root, filter="data")
    (root / "hjh-linux-amd64").rename(root / "app")
    (root / "tmp").mkdir()
    app = root / "app"
    package_manifest = json.loads((app / "manifest.json").read_text())
    for name, meta in package_manifest["files"].items():
        if digest(app / name) != meta["sha256"]:
            raise RuntimeError(f"Extracted bytes differ: {name}")
    write_json(out / "package-manifest.json", package_manifest)
    initial_files = inventory(root)
    write_json(out / "initial-root.json", {"root": str(root), "files": initial_files,
                                           "uid": os.getuid(), "gid": os.getgid(), "procMounted": False})
    binary_hash = digest(app / "hjh")
    summary = {"at": datetime.now(timezone.utc).isoformat(), "target": "Linux/amd64",
               "uname": list(os.uname()), "archive": str(archive), "archiveSHA256": digest(archive),
               "binarySHA256": binary_hash, "binaryBytes": (app / "hjh").stat().st_size,
               "root": str(root), "cases": []}

    def command(name, args, expected=0):
        cmd = ["/usr/bin/sudo", "-n", "/usr/sbin/chroot",
               f"--userspec={os.getuid()}:{os.getgid()}", str(root), "/app/hjh", *args]
        # Avoid passing ambient proxy/credential variables into the spike.
        env = {"PATH": "/usr/bin:/bin", "HOME": "/tmp", "LANG": "C.UTF-8"}
        proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env, start_new_session=True)
        try:
            stdout, stderr = proc.communicate(timeout=40)
        except subprocess.TimeoutExpired:
            os.killpg(proc.pid, signal.SIGTERM)
            try:
                stdout, stderr = proc.communicate(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(proc.pid, signal.SIGKILL)
                stdout, stderr = proc.communicate()
            raise RuntimeError(f"Experiment watchdog: {name}: {stderr.decode()}")
        record = {"argv": cmd, "exitCode": proc.returncode, "stderr": stderr.decode(),
                  "output": json.loads(stdout) if stdout.strip() else None}
        write_json(out / (name + ".json"), record)
        if proc.returncode != expected:
            raise RuntimeError(f"{name}: exit={proc.returncode}: {stderr.decode()}")
        return record["output"]

    def run(name, url, extra=(), expected=0):
        return command(name, ["run", "-session", "distributed", "-turn", name, "-text", name, "-url", url, *extra], expected)

    def value(output):
        assert output["activeHostCalls"] == 0
        return output["result"]["worker"]["value"]

    http = ThreadingHTTPServer(("127.0.0.1", 0), Server)
    thread = threading.Thread(target=http.serve_forever, daemon=True)
    thread.start()
    url = f"http://127.0.0.1:{http.server_port}/catalog.yaml"
    tls = None
    try:
        command("create", ["create", "-session", "distributed"])
        first = run("first", url)
        v1 = value(first)
        assert v1["report"]["compatible"] == [{"name": "compatible", "version": "1.4.2"}]
        assert v1["report"]["toolRevision"] == "tool-one" and v1["report"]["hookRevision"] == "hook-one"
        assert v1["historyMessages"] == 0
        command("adopt-first", ["adopt", "-session", "distributed", "-turn", "first"])
        summary["cases"].append({"name": "static-binary-empty-root-http-sqlite", "passed": True,
                                  "pid": first["pid"], "sourceFiles": len(package_manifest["files"]), "sharedLibraries": []})

        for relative, old, new in [("tools/modules/catalog.js", "tool-one", "tool-two"),
                                   ("tools/hooks.js", "hook-one", "hook-two"),
                                   ("tools/hook-config.yaml", "distributed", "edited")]:
            path = app / relative
            path.write_text(path.read_text().replace(old, new))
        settings = json.loads((app / "settings.json").read_text())
        settings["instruction"] = "distributed instruction two"
        write_json(app / "settings.json", settings)
        second = run("second", url)
        v2 = value(second)
        assert first["pid"] != second["pid"] and v2["historyMessages"] == 2 and v2["previous"]
        assert v2["report"]["toolRevision"] == "tool-two" and v2["report"]["hookRevision"] == "hook-two"
        assert v2["report"]["note"] == "edited:hook-two" and v2["instruction"] == settings["instruction"]
        assert digest(app / "hjh") == binary_hash
        assert first["result"]["configuration"] != second["result"]["configuration"]
        command("adopt-second", ["adopt", "-session", "distributed", "-turn", "second"])
        inspected = command("resume-inspection", ["inspect", "-session", "distributed", "-count", "2"])
        assert inspected["session"]["adopted"] == 2 and inspected["session"]["events"] == 80
        assert inspected["configurations"][first["result"]["configuration"]]["settings"]["instruction"] == "distributed instruction one"
        summary["cases"].append({"name": "edited-tool-hook-config-next-process-session-resume", "passed": True,
                                  "pids": [first["pid"], second["pid"]], "binaryUnchanged": True,
                                  "turns": 2, "adoptedPairs": 2, "events": 80})

        # A real TLS handshake with CA validation, hostname lookup, and no curl executable.
        cert_dir = Path(tempfile.mkdtemp(prefix="tls-", dir=work))
        cert, key = cert_dir / "ca.pem", cert_dir / "key.pem"
        subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", str(key),
                        "-out", str(cert), "-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost", "-days", "2"],
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        cert_path = root / "etc/ssl/certs/ca-certificates.crt"
        cert_path.parent.mkdir(parents=True)
        cert_path.write_bytes(cert.read_bytes())
        (root / "etc/hosts").write_text("127.0.0.1 localhost\n")
        tls = ThreadingHTTPServer(("127.0.0.1", 0), Server)
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(cert, key)
        tls.socket = context.wrap_socket(tls.socket, server_side=True)
        threading.Thread(target=tls.serve_forever, daemon=True).start()
        tls_result = run("tls", f"https://localhost:{tls.server_port}/catalog.yaml")
        assert value(tls_result)["report"]["compatible"] == v2["report"]["compatible"]
        assert any(request["tls"] for request in Server.requests)
        summary["cases"].append({"name": "https-with-os-ca-and-hostname-resolution", "passed": True,
                                  "addedResources": ["/etc/hosts", "/etc/ssl/certs/ca-certificates.crt"], "tlsVerified": True})

        # Public HTTPS uses this environment's CA and resolver configuration, not a pinned app bundle.
        cert_path.write_bytes(Path("/etc/ssl/certs/ca-certificates.crt").read_bytes() + cert.read_bytes())
        shutil.copyfile("/etc/resolv.conf", root / "etc/resolv.conf")
        public = run("public-https", "https://bellard.org/quickjs/", ["-js", "/app/http.js"])
        public_value = value(public)
        assert public_value["status"] == 200 and public_value["bytes"] > 0
        summary["cases"].append({"name": "public-https-no-curl", "passed": True,
                                  "url": "https://bellard.org/quickjs/", "status": public_value["status"],
                                  "bytes": public_value["bytes"], "resolver": "/etc/resolv.conf"})

        write_json(app / "command-args.json", {"program": "/bin/echo", "args": ["external command available"]})
        missing = run("command-missing", "", ["-js", "/app/command.js", "-args", "/app/command-args.json"], expected=1)
        assert missing["result"]["worker"]["error"] and not (root / "bin/echo").exists()
        # Copy only the selected external command and its actual ELF dependencies.
        ldd = subprocess.check_output(["ldd", "/bin/echo"], text=True)
        dependencies = [Path("/bin/echo")]
        for line in ldd.splitlines():
            for field in line.split():
                if field.startswith("/") and Path(field).is_file():
                    dependencies.append(Path(field))
                    break
        for path in dependencies:
            target = root / str(path).lstrip("/")
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(path, target)
            target.chmod(path.stat().st_mode & 0o777)
        available = run("command-available", "", ["-js", "/app/command.js", "-args", "/app/command-args.json"])
        assert value(available)["stdout"] == "external command available\n" and value(available)["exitCode"] == 0
        assert digest(app / "hjh") == binary_hash
        summary["cases"].append({"name": "optional-external-command", "passed": True,
                                  "addedFiles": [str(path) for path in dependencies], "ldd": ldd,
                                  "missingCommandRecorded": True, "shellRequired": False})
        final = command("final-inspection", ["inspect", "-session", "distributed", "-count", "6"])
        assert final["session"]["turns"] == 6 and final["session"]["adopted"] == 2 and final["integrity"] == "ok"
        assert final["turns"][4]["outcome"] == "failed" and final["turns"][5]["outcome"] == "completed"
        summary["sqlite"] = final["sqlite"]
        summary["session"] = final["session"]
        summary["requests"] = Server.requests
        write_json(out / "final-root.json", {"files": inventory(root)})
        shutil.copyfile(app / "state/history.sqlite", out / "history.sqlite")
        write_json(out / "summary.json", summary)
        return summary
    finally:
        http.shutdown()
        http.server_close()
        if tls:
            tls.shutdown()
            tls.server_close()


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("archive", type=Path)
    parser.add_argument("--out", type=Path, default=MODULE.parent.parent / "dist/s05-observations")
    parser.add_argument("--work", type=Path, default=MODULE.parent.parent / "dist/s05-work")
    args = parser.parse_args()
    result = verify(args.archive.resolve(), args.out.resolve(), args.work.resolve())
    print(f"All {len(result['cases'])} S-05 scenarios passed. Evidence: {args.out.resolve()}")
