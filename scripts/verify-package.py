#!/usr/bin/env python3
"""Check local archive contents, hashes and native offline execution."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import zipfile


def digest(data):
    return hashlib.sha256(data).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--directory", type=Path, default=Path(__file__).resolve().parents[1] / "dist")
    parser.add_argument("--version", default=(Path(__file__).resolve().parents[1] / "VERSION").read_text().strip())
    args = parser.parse_args()
    manifest_name = f"lip-{args.version}-manifest.json"
    checksums = {}
    for line in (args.directory / f"lip-{args.version}-SHA256SUMS").read_text().splitlines():
        checksum, name = line.split("  ", 1)
        if Path(name).name != name or name in checksums:
            raise ValueError("invalid or duplicate checksum filename")
        if digest((args.directory / name).read_bytes()) != checksum:
            raise ValueError(f"checksum mismatch: {name}")
        checksums[name] = checksum
    manifest = json.loads((args.directory / manifest_name).read_text())
    if manifest["schema"] != "lip.release.v1" or manifest["version"] != args.version:
        raise ValueError("manifest version/schema mismatch")
    if set(checksums) != set(manifest["artifacts"]) | {manifest_name}:
        raise ValueError("checksum/manifest artifact set mismatch")
    for name, checksum in manifest["artifacts"].items():
        if checksums[name] != checksum:
            raise ValueError(f"manifest checksum mismatch: {name}")
    prefix = f"lip-{args.version}-source/"
    with tarfile.open(args.directory / f"lip-{args.version}-source.tar.gz") as archive:
        names = set()
        for entry in archive:
            if not entry.isfile() or not entry.name.startswith(prefix):
                raise ValueError(f"unexpected source entry: {entry.name}")
            name = entry.name[len(prefix):]
            if name in names or manifest["sources"].get(name) != digest(archive.extractfile(entry).read()):
                raise ValueError(f"source checksum mismatch: {name}")
            names.add(name)
        if names != set(manifest["sources"]):
            raise ValueError("source archive is incomplete")
    # Verify every tool payload, including cross-built archives. Hashing only the
    # archive would miss a packaging rule that accidentally omits editor files.
    tool_sources = {name: checksum for name, checksum in manifest["sources"].items()
                    if name.startswith(("docs/", "editors/", "examples/")) or name in
                    {"VERSION", "README.md", "README.en.md", "CHANGELOG.md", "RELEASE.md"}}
    for target in manifest["targets"]:
        target_os, target_arch = target.split("/")
        tool_prefix = f"lip-{args.version}-{target_os}-{target_arch}/"
        tool_name = tool_prefix[:-1] + (".zip" if target_os == "windows" else ".tar.gz")
        payload = {}
        if target_os == "windows":
            with zipfile.ZipFile(args.directory / tool_name) as archive:
                for entry in archive.infolist():
                    if entry.is_dir() or not entry.filename.startswith(tool_prefix):
                        raise ValueError(f"unexpected tool entry: {entry.filename}")
                    relative = entry.filename[len(tool_prefix):]
                    if relative in payload:
                        raise ValueError(f"duplicate tool entry: {relative}")
                    payload[relative] = digest(archive.read(entry))
        else:
            with tarfile.open(args.directory / tool_name) as archive:
                for entry in archive:
                    if not entry.isfile() or not entry.name.startswith(tool_prefix):
                        raise ValueError(f"unexpected tool entry: {entry.name}")
                    relative = entry.name[len(tool_prefix):]
                    if relative in payload:
                        raise ValueError(f"duplicate tool entry: {relative}")
                    payload[relative] = digest(archive.extractfile(entry).read())
        executable_name = "lipc.exe" if target_os == "windows" else "lipc"
        if set(payload) != set(tool_sources) | {executable_name, "INSTALL.txt"}:
            raise ValueError(f"tool archive is incomplete or has extra files: {target}")
        for name, checksum in tool_sources.items():
            if payload[name] != checksum:
                raise ValueError(f"tool source checksum mismatch: {target}: {name}")
    native = subprocess.check_output(["go", "env", "GOHOSTOS", "GOHOSTARCH"], text=True).strip().replace("\n", "/")
    goos, goarch = native.split("/")
    if native not in manifest["targets"]:
        print(f"Verified LIP {args.version} archives; native {native} smoke check unavailable in this package.")
        return
    filename = f"lip-{args.version}-{goos}-{goarch}" + (".zip" if goos == "windows" else ".tar.gz")
    with tempfile.TemporaryDirectory(prefix="lip-release-smoke-") as temporary:
        directory = Path(temporary)
        if goos == "windows":
            with zipfile.ZipFile(args.directory / filename) as archive:
                archive.extractall(directory)
        else:
            with tarfile.open(args.directory / filename) as archive:
                archive.extractall(directory, filter="data")
        tool_root = directory / f"lip-{args.version}-{goos}-{goarch}"
        binary = tool_root / ("lipc.exe" if goos == "windows" else "lipc")
        project = directory / "independent project"
        project.mkdir()
        env = dict(os.environ, GOPROXY="off", GOWORK="off")
        def run(*arguments, expected=None, executable=binary, environment=env):
            output = subprocess.check_output([str(executable), *map(str, arguments)], cwd=project, env=environment, text=True).strip()
            if expected is not None and output != expected:
                raise ValueError(f"{arguments}: expected {expected!r}, got {output!r}")
            return output
        run("version", expected=args.version)
        run("run", tool_root / "examples/core.lip", "[1,2,3]", expected='{"average":4,"count":3,"total":12,"values":[2,4,6]}')
        source = project / "unused.lip"
        source.write_text(';;;import host "unused"; import go "math"; import python "not_installed"; print(7);;;\n')
        run("check", source)
        run("inspect", source)
        run("run", source, expected="7")
        standalone = project / ("standalone.exe" if goos == "windows" else "standalone")
        run("build", "--output", standalone, source)
        run(expected="7", executable=standalone, environment=dict(env, PATH=""))
    print(f"Verified LIP {args.version}: source/artifact hashes and native {native} offline run/build.")


if __name__ == "__main__":
    main()
