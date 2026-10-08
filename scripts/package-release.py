#!/usr/bin/env python3
"""Build local LIP source/tool archives without tagging or publishing."""

import argparse
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]
DIRECTORIES = ("cmd", "compiler", "docs", "editors", "examples", "internal", "runtime", "scripts", "tests")
ROOT_FILES = ("VERSION", "go.mod", "go.sum", "LICENSE", "README.md", "README.en.md", "CHANGELOG.md", "RELEASE.md", ".gitignore")
SUFFIXES = {".go", ".md", ".lip", ".json", ".py", ".sh", ".txt", ".vim", ".lua", ".el"}


def source_files(root):
    """Explicit project paths; never package personal configuration or binaries."""
    files = [root / name for name in ROOT_FILES if (root / name).is_file()]
    for name in DIRECTORIES:
        if (root / name).is_symlink():
            raise ValueError(f"source symlinks are not supported: {root / name}")
        for directory, folders, names in os.walk(root / name, followlinks=False):
            folders[:] = sorted(folder for folder in folders if not folder.startswith(".") and folder != "__pycache__")
            for folder in folders:
                if (Path(directory) / folder).is_symlink():
                    raise ValueError(f"source symlinks are not supported: {Path(directory) / folder}")
            for filename in names:
                path = Path(directory) / filename
                if not filename.startswith(".") and path.suffix in SUFFIXES:
                    files.append(path)
    for path in files:
        if path.is_symlink():
            raise ValueError(f"source symlinks are not supported: {path}")
    return sorted(files, key=lambda path: path.relative_to(root).as_posix())


def checksum(data):
    return hashlib.sha256(data).hexdigest()


def write_archive(path, prefix, entries, epoch):
    # entries contain (relative path, immutable bytes, POSIX mode).
    if path.suffix == ".zip":
        with zipfile.ZipFile(path, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
            for name, data, mode in entries:
                info = zipfile.ZipInfo(f"{prefix}/{name}", date_time=(1980, 1, 1, 0, 0, 0))
                info.create_system = 3
                info.external_attr = (0o100000 | mode) << 16
                info.compress_type = zipfile.ZIP_DEFLATED
                archive.writestr(info, data)
        return
    with path.open("wb") as output:
        with gzip.GzipFile(fileobj=output, mode="wb", filename="", mtime=epoch, compresslevel=9) as compressed:
            with tarfile.open(fileobj=compressed, mode="w", format=tarfile.PAX_FORMAT) as archive:
                for name, data, mode in entries:
                    info = tarfile.TarInfo(f"{prefix}/{name}")
                    info.size, info.mode, info.mtime = len(data), mode, epoch
                    info.uid = info.gid = 0
                    info.uname = info.gname = ""
                    archive.addfile(info, io.BytesIO(data))


def command(*args, **kwargs):
    return subprocess.check_output(args, cwd=ROOT, text=True, **kwargs).strip()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=ROOT / "dist")
    parser.add_argument("--target", action="append", help="Go platform, e.g. linux/amd64; repeat to build multiple platforms")
    args = parser.parse_args()
    version = (ROOT / "VERSION").read_text().strip()
    if not re.fullmatch(r"\d+\.\d+\.\d+", version):
        parser.error("VERSION must contain a semantic patch version")
    epoch = int(os.environ.get("SOURCE_DATE_EPOCH", "0"))
    if not 0 <= epoch <= 0xFFFFFFFF:
        parser.error("SOURCE_DATE_EPOCH must be a nonnegative 32-bit timestamp")
    native = command("go", "env", "GOHOSTOS", "GOHOSTARCH").replace("\n", "/")
    targets = sorted(set(args.target or [native]))
    supported = set(command("go", "tool", "dist", "list").splitlines())
    for target in targets:
        if target not in supported:
            parser.error(f"unsupported Go target: {target}")
    args.output = args.output.resolve()
    args.output.mkdir(parents=True, exist_ok=True)
    # Snapshot all source bytes once. The same snapshot goes into every archive.
    entries = []
    hashes = {}
    for path in source_files(ROOT):
        relative = path.relative_to(ROOT).as_posix()
        data = path.read_bytes()
        entries.append((relative, data, 0o755 if path.suffix in {".sh", ".py"} else 0o644))
        hashes[relative] = checksum(data)
    version_source = next(data.decode() for name, data, _ in entries if name == "runtime/version.go")
    runtime_version = re.search(r'const Version = "([^"]+)"', version_source)
    if runtime_version is None or runtime_version[1] != version:
        raise ValueError("Runtime version differs from VERSION")
    manifest = {"schema": "lip.release.v1", "version": version, "go": command("go", "version"),
                "targets": targets, "source_date_epoch": epoch, "sources": hashes, "artifacts": {}}
    with tempfile.TemporaryDirectory(prefix=".lip-package-", dir=args.output) as temporary:
        staging = Path(temporary)
        # Build from the captured source, so binary and source packages agree even
        # if the working tree changes while packaging.
        snapshot = staging / "source"
        snapshot.mkdir()
        for name, data, mode in entries:
            destination = snapshot / name
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_bytes(data)
            destination.chmod(mode)
        prefix = f"lip-{version}-source"
        source_archive = staging / f"{prefix}.tar.gz"
        write_archive(source_archive, prefix, entries, epoch)
        artifacts = [source_archive]
        for target in targets:
            goos, goarch = target.split("/")
            binary_name = "lipc.exe" if goos == "windows" else "lipc"
            binary = staging / binary_name
            env = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED="0", GOWORK="off")
            subprocess.run(["go", "build", "-trimpath", "-buildvcs=false", "-o", str(binary), "./cmd/lipc"],
                           cwd=snapshot, env=env, check=True)
            if target == native:
                actual = subprocess.check_output([str(binary), "version"], text=True).strip()
                if actual != version:
                    raise ValueError(f"binary version {actual} differs from VERSION {version}")
            prefix = f"lip-{version}-{goos}-{goarch}"
            archive = staging / (prefix + (".zip" if goos == "windows" else ".tar.gz"))
            readme = (f"LIP {version} ({target})\n\n"
                      "Run ./lipc version, ./lipc help or ./lipc learn (Windows: .\\lipc.exe).\n"
                      "check/inspect need no Go installation; run/build/repl/learn execution need Go 1.27+.\n"
                      "Core programs built by lipc run without Go; Python programs additionally need Python.\n"
                      "examples/, docs/ and editors/ are included. See docs/QUICKSTART.md, docs/EDITORS.md and RELEASE.md.\n").encode()
            payload = [(binary_name, binary.read_bytes(), 0o755), ("INSTALL.txt", readme, 0o644)]
            payload += [entry for entry in entries if entry[0].startswith(("docs/", "editors/", "examples/")) or entry[0] in {"VERSION", "README.md", "README.en.md", "CHANGELOG.md", "RELEASE.md"}]
            write_archive(archive, prefix, payload, epoch)
            artifacts.append(archive)
        for artifact in artifacts:
            manifest["artifacts"][artifact.name] = checksum(artifact.read_bytes())
        manifest_file = staging / f"lip-{version}-manifest.json"
        manifest_file.write_text(json.dumps(manifest, ensure_ascii=False, indent=2, sort_keys=True) + "\n")
        artifacts.append(manifest_file)
        checksums = staging / f"lip-{version}-SHA256SUMS"
        checksums.write_text("".join(f"{checksum(path.read_bytes())}  {path.name}\n" for path in artifacts))
        artifacts.append(checksums)
        for artifact in artifacts:
            os.replace(artifact, args.output / artifact.name)
    print(f"Packaged LIP {version}: {', '.join(targets)} -> {args.output.resolve()}")


if __name__ == "__main__":
    main()
