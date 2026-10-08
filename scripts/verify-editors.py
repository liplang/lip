#!/usr/bin/env python3
"""Run LIP editor integration tests in installed Vim, Neovim and Emacs."""

import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def run(arguments, **kwargs):
    result = subprocess.run(arguments, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                            timeout=60, **kwargs)
    if result.returncode:
        raise RuntimeError(f"{arguments[0]} failed ({result.returncode}):\n{result.stdout}")
    return result.stdout


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--compiler", type=Path, help="existing lipc executable; otherwise build one")
    parser.add_argument("--require-all", action="store_true", help="fail if any of the three editors is unavailable")
    args = parser.parse_args()
    env = dict(os.environ)
    run(["go", "run", "./scripts/editor-catalog", "--check"], cwd=ROOT, env=env)
    with tempfile.TemporaryDirectory(prefix="lip editor tests ") as temporary:
        directory = Path(temporary)
        compiler = args.compiler.resolve() if args.compiler else directory / "compiler bin" / ("lipc.exe" if os.name == "nt" else "lipc")
        if not args.compiler:
            compiler.parent.mkdir()
            run(["go", "build", "-buildvcs=false", "-o", str(compiler), "./cmd/lipc"], cwd=ROOT, env=env)
        # Exercise installed packages outside the source tree, with spaces in paths.
        vim = directory / "vim package"
        neovim = directory / "neovim package"
        emacs = directory / "emacs package"
        shutil.copytree(ROOT / "editors/vim", vim)
        shutil.copytree(ROOT / "editors/neovim", neovim)
        shutil.copytree(ROOT / "editors/emacs", emacs)
        # A native wrapper makes cancellation deterministic without relying on
        # POSIX shebangs. It still forwards every check to the real compiler.
        slow_compiler = directory / ("delayed lipc.exe" if os.name == "nt" else "delayed lipc")
        delayed_source = directory / "delayed.go"
        delayed_source.write_text('''package main
import ("fmt"; "os"; "os/exec"; "time")
func main() {
    time.Sleep(300 * time.Millisecond)
    command := exec.Command(''' + json.dumps(str(compiler), ensure_ascii=False) + ''', os.Args[1:]...)
    command.Stdout, command.Stderr = os.Stdout, os.Stderr
    if err := command.Run(); err != nil {
        if status, ok := err.(*exec.ExitError); ok { os.Exit(status.ExitCode()) }
        fmt.Fprintln(os.Stderr, err)
        os.Exit(2)
    }
}
''', encoding="utf-8")
        run(["go", "build", "-buildvcs=false", "-o", str(slow_compiler), str(delayed_source)], cwd=directory, env=env)
        env.update(LIP_EDITOR_VIM=str(vim), LIP_EDITOR_NEOVIM=str(neovim), LIP_TEST_COMPILER=str(compiler),
                   NVIM_LOG_FILE=str(directory / "neovim.log"),
                   LIP_TEST_SLOW_COMPILER=str(slow_compiler),
                   LIP_EDITOR_EXAMPLE=str(ROOT / "editors/tests/example.lip"))
        run([str(compiler), "check", env["LIP_EDITOR_EXAMPLE"]], cwd=directory, env=env)
        checked = []
        for editor in ["vim", "nvim", "emacs"]:
            executable = shutil.which(editor)
            if not executable:
                if args.require_all:
                    raise RuntimeError(f"required editor is unavailable: {editor}")
                print(f"Skipping {editor}: executable unavailable", flush=True)
                continue
            bad = directory / "bad program 中文.lip"
            bad.write_text('flow 坏() -> number {\n\t标签 = "e\u0301你好"; return 未定义\n}\n', encoding="utf-8")
            response = subprocess.run([str(compiler), "check", "--json", str(bad)], cwd=directory,
                                      env=env, stdout=subprocess.PIPE, text=True, timeout=10)
            report = json.loads(response.stdout)
            if response.returncode != 1 or report["ok"]:
                raise RuntimeError("negative editor fixture did not produce a compiler diagnostic")
            diagnostic = report["diagnostics"][0]
            lines = bad.read_text().splitlines(keepends=True)
            line, column = diagnostic["line"], diagnostic["column"]
            prefix = lines[line - 1][:column - 1]
            expected = directory / "expected.json"
            expected.write_text(json.dumps({"line": line, "column": column,
                                           "byte_column": len(prefix.encode()) + 1,
                                           "character_offset": sum(map(len, lines[:line-1])) + len(prefix)}))
            failures = directory / "failures.txt"
            env.update(LIP_EDITOR_BAD=str(bad), LIP_EDITOR_EXPECTED=str(expected), LIP_EDITOR_FAILURES=str(failures))
            if editor == "vim":
                command = [executable, "-Nu", "NONE", "-i", "NONE", "-n", "-es", "-S", str(ROOT / "editors/tests/vim.vim")]
            elif editor == "nvim":
                command = [executable, "--headless", "-u", "NONE", "-i", "NONE", "-n", "-l", str(ROOT / "editors/tests/neovim.lua")]
            else:
                run([executable, "-Q", "--batch", "-L", str(emacs),
                     "--eval", "(setq byte-compile-error-on-warn t)", "-f", "batch-byte-compile",
                     str(emacs / "lip-keywords.el"), str(emacs / "lip-mode.el")], cwd=directory, env=env)
                command = [executable, "-Q", "--batch", "-L", str(emacs), "-l", str(ROOT / "editors/tests/emacs.el")]
            try:
                output = run(command, cwd=directory, env=env)
            except Exception:
                if failures.exists():
                    print(failures.read_text(), flush=True)
                raise
            print(f"Verified {editor}: detection, highlighting, indentation, completion and compiler diagnostics", flush=True)
            if editor == "emacs":
                print(output.strip(), flush=True)
            checked.append(editor)
        if not checked:
            raise RuntimeError("no editor was available to run integration tests")


if __name__ == "__main__":
    main()
