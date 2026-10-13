"""Build separate Linux/amd64 core and TUI binaries with editable extensions."""
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

ROOT = Path(__file__).resolve().parent.parent

# The engine also contains Math code with its own upstream notices.
MODULE_NOTICES = {"github.com/go-quickjs/go-quickjs": ["internal/fdlibm/LICENSE"]}


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def json_stream(text):
    decoder = json.JSONDecoder()
    while text.strip():
        text = text.lstrip()
        value, end = decoder.raw_decode(text)
        yield value
        text = text[end:]


def source_metadata():
    revision = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    dirty = bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT, text=True).strip())
    return revision, dirty


def package(out):
    source_revision, source_dirty = source_metadata()
    out.mkdir(parents=True, exist_ok=True)
    work = Path(tempfile.mkdtemp(prefix="build-", dir=out))
    app = work / "hjh-linux-amd64"
    app.mkdir()
    env = dict(os.environ, CGO_ENABLED="0", GOOS="linux", GOARCH="amd64", GOAMD64="v1")
    ldflags = " ".join((
        f"-X hjh/internal/buildinfo.SourceRevision={source_revision}",
        f"-X hjh/internal/buildinfo.SourceDirty={str(source_dirty).lower()}",
    ))
    binaries = {}
    for name, target in [("hjh", "./cmd/hjh"), ("hjh-tui", "./cmd/hjh-tui")]:
        binary = app / name
        subprocess.run(["go", "build", "-trimpath", "-ldflags", ldflags, "-o", str(binary), target], cwd=ROOT, env=env, check=True)
        build = subprocess.check_output(["go", "version", "-m", str(binary)], text=True)
        headers = subprocess.check_output(["readelf", "-l", str(binary)], text=True)
        if "INTERP" in headers:
            raise RuntimeError(f"{name} requires an ELF interpreter")
        dependencies = []
        for line in build.splitlines():
            fields = line.split()
            if fields and fields[0] == "dep":
                dependencies.append(dict(zip(["module", "version", "sum"], fields[1:4])))
        binaries[name] = {"build": build, "elfProgramHeaders": headers, "goDependencies": dependencies,
                          "bytes": binary.stat().st_size, "sha256": digest(binary)}
    modules = {m["Path"]: m for m in json_stream(subprocess.check_output(["go", "list", "-m", "-json", "all"], cwd=ROOT, text=True))}
    dependencies = []
    linked = {d["module"]: d for binary in binaries.values() for d in binary["goDependencies"]}
    for name, dependency in sorted(linked.items()):
        version, checksum = dependency["version"], dependency["sum"]
        source = Path(modules[name]["Dir"])
        destination = app / "licenses" / name
        destination.mkdir(parents=True)
        notices = []
        for path in sorted(source.iterdir()):
            if path.is_file() and path.name.upper().startswith(("LICENSE", "COPYING", "NOTICE", "AUTHORS", "CONTRIBUTORS", "PATENTS")):
                shutil.copyfile(path, destination / path.name)
                notices.append(str((destination / path.name).relative_to(app)))
        for relative in MODULE_NOTICES.get(name, []):
            path = source / relative
            notice = destination / relative
            notice.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(path, notice)
            notices.append(str(notice.relative_to(app)))
        if not notices:
            raise RuntimeError(f"No upstream license material: {name}")
        dependencies.append({"module": name, "version": version, "sum": checksum, "notices": notices})
    goroot = Path(subprocess.check_output(["go", "env", "GOROOT"], text=True).strip())
    (app / "licenses/go").mkdir(parents=True)
    for name in ("LICENSE", "PATENTS"):
        shutil.copyfile(goroot / name, app / "licenses/go" / name)
    shutil.copytree(ROOT / "internal/extensions/assets", app / "extensions")
    # These prepared ESM dependencies were verified in S-03. They need no npm
    # or Node at the destination and are optional for the standard tools.
    shutil.copytree(ROOT / "spikes/s01/s03-tools/vendor", app / "extensions/vendor")
    (app / "extensions/modules").mkdir()
    shutil.copyfile(ROOT / "spikes/s01/s03-tools/modules/catalog.js", app / "extensions/modules/catalog.js")
    shutil.copyfile(ROOT / "docs/distribution-linux.md", app / "README.md")
    shutil.copyfile(ROOT / "LICENSE", app / "LICENSE")
    (app / "NOTICE").write_text("hjh Linux/amd64 development build: hjh core and optional hjh-tui client.\nProject license: LICENSE (MIT).\nGo module and runtime licenses: licenses/.\nOptional YAML and semver ESM licenses: extensions/vendor/.\nPer-binary dependencies and file hashes: manifest.json.\n")
    manifest = {"target": "linux/amd64", "source": {"revision": source_revision, "dirty": source_dirty},
                "binaries": binaries, "goDependencies": dependencies,
                "jsDependencies": json.loads((app / "extensions/vendor/manifest.json").read_text())["dependencies"],
                "files": {str(p.relative_to(app)): {"bytes": p.stat().st_size, "sha256": digest(p)} for p in sorted(app.rglob("*")) if p.is_file()}}
    (app / "manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
    archive = work / "hjh-linux-amd64.tar.gz"
    with archive.open("wb") as raw, gzip.GzipFile(fileobj=raw, mode="wb", filename="", mtime=0) as compressed:
        with tarfile.open(fileobj=compressed, mode="w") as tar:
            for path in [app, *sorted(app.rglob("*"))]:
                info = tar.gettarinfo(str(path), str(path.relative_to(work)))
                info.uid = info.gid = info.mtime = 0
                info.uname = info.gname = ""
                if path.is_file():
                    with path.open("rb") as file:
                        tar.addfile(info, file)
                else:
                    tar.addfile(info)
    result = {"archive": str(archive), "sha256": digest(archive), "bytes": archive.stat().st_size,
              "binaries": {name: {"sha256": value["sha256"], "bytes": value["bytes"]} for name, value in binaries.items()}}
    (work / "package.json").write_text(json.dumps(result, indent=2) + "\n")
    return result


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--out", type=Path, default=ROOT / "dist/packages")
    args = parser.parse_args()
    print(json.dumps(package(args.out.resolve()), indent=2))
