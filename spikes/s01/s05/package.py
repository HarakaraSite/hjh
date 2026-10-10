"""Build a local, static Linux/amd64 spike archive; no publishing."""
import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile

HERE = Path(__file__).resolve().parent
MODULE = HERE.parent


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write_json(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n")


def json_stream(text):
    decoder = json.JSONDecoder()
    while text.strip():
        value, end = decoder.raw_decode(text.lstrip())
        yield value
        text = text.lstrip()[end:]


def package(out):
    out.mkdir(parents=True, exist_ok=True)
    workspace = Path(tempfile.mkdtemp(prefix="build-", dir=out))
    app = workspace / "hjh-linux-amd64"
    app.mkdir()
    env = dict(os.environ, CGO_ENABLED="0", GOOS="linux", GOARCH="amd64", GOAMD64="v1")
    subprocess.run(["go", "build", "-trimpath", "-o", str(app / "hjh"), "./cmd/s05"],
                   cwd=MODULE, env=env, check=True)
    build = subprocess.check_output(["go", "version", "-m", str(app / "hjh")], text=True)
    readelf = subprocess.check_output(["readelf", "-l", str(app / "hjh")], text=True)
    if "INTERP" in readelf:
        raise RuntimeError("Linux binary has an ELF interpreter")
    modules = {m["Path"]: m for m in json_stream(subprocess.check_output(
        ["go", "list", "-m", "-json", "all"], cwd=MODULE, text=True))}
    dependencies = []
    for line in build.splitlines():
        fields = line.split()
        if fields and fields[0] == "dep":
            name, version, checksum = fields[1:4]
            source = Path(modules[name]["Dir"])
            target = app / "licenses" / name
            target.mkdir(parents=True, exist_ok=True)
            # Includes LICENSE-LIBQUICKJS and other upstream notices present in the module.
            notices = []
            for path in sorted(source.iterdir()):
                if path.is_file() and path.name.upper().startswith(("LICENSE", "COPYING", "NOTICE", "AUTHORS", "CONTRIBUTORS", "PATENTS")):
                    shutil.copyfile(path, target / path.name)
                    notices.append(str((target / path.name).relative_to(app)))
            if not notices:
                raise RuntimeError(f"No license material in {name}")
            dependencies.append({"module": name, "version": version, "sum": checksum, "notices": notices})
    goroot = Path(subprocess.check_output(["go", "env", "GOROOT"], text=True).strip())
    (app / "licenses/go").mkdir(parents=True)
    for name in ("LICENSE", "PATENTS"):
        shutil.copyfile(goroot / name, app / "licenses/go" / name)
    shutil.copytree(MODULE / "s03-tools", app / "tools")
    with (app / "tools/index.js").open("a") as file:
        file.write("\nexport async function command(args, api) { return api.command(args); }\n")
    (app / "tools/hook-config.yaml").write_text("note: distributed\n")
    write_json(app / "settings.json", {"model": "spike-only-no-model-call", "instruction": "distributed instruction one",
                                      "extensions": "tools", "hooks": "hooks.js"})
    for name in ("command.js", "http.js", "catalog.yaml"):
        shutil.copyfile(HERE / name, app / name)
    shutil.copyfile(HERE / "README-package.md", app / "README.md")
    manifest = {"target": "linux/amd64", "build": build, "elfProgramHeaders": readelf,
                "goDependencies": dependencies,
                "jsDependencies": json.loads((app / "tools/vendor/manifest.json").read_text())["dependencies"],
                "files": {str(p.relative_to(app)): {"bytes": p.stat().st_size, "sha256": digest(p), "mode": oct(p.stat().st_mode & 0o777)}
                          for p in sorted(app.rglob("*")) if p.is_file()}}
    write_json(app / "manifest.json", manifest)
    archive = workspace / "hjh-linux-amd64.tar.gz"
    # Stable metadata; editing external sources is intentionally allowed after extraction.
    with archive.open("wb") as raw, gzip.GzipFile(fileobj=raw, mode="wb", filename="", mtime=0) as compressed:
        with tarfile.open(fileobj=compressed, mode="w") as tar:
            for path in [app, *sorted(app.rglob("*"))]:
                info = tar.gettarinfo(str(path), str(path.relative_to(workspace)))
                info.uid = info.gid = info.mtime = 0
                info.uname = info.gname = ""
                if path.is_file():
                    with path.open("rb") as file:
                        tar.addfile(info, file)
                else:
                    tar.addfile(info)
    result = {"archive": str(archive), "sha256": digest(archive), "bytes": archive.stat().st_size,
              "binarySHA256": digest(app / "hjh"), "binaryBytes": (app / "hjh").stat().st_size,
              "files": len(manifest["files"]) + 1, "manifest": str(app / "manifest.json")}
    write_json(workspace / "package.json", result)
    return result


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--out", type=Path, default=MODULE.parent.parent / "dist/s05-packages")
    args = parser.parse_args()
    print(json.dumps(package(args.out.resolve()), indent=2))
