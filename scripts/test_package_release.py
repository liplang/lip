"""Tests for release contents and reproducible archives; no third-party packages."""

import importlib.util
from pathlib import Path
import tarfile
import tempfile
import unittest
import zipfile

spec = importlib.util.spec_from_file_location("package_release", Path(__file__).with_name("package-release.py"))
package = importlib.util.module_from_spec(spec)
spec.loader.exec_module(package)


class ReleaseTests(unittest.TestCase):
    def test_source_allowlist(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            for name in ["VERSION", "go.mod", "cmd/lipc/main.go", "cmd/lipc/learn/harness.go.txt", "runtime/python_worker.py",
                         "docs/SPEC.md", "runtime/.credentials.json", "runtime/__pycache__/cache.py", ".aws/config",
                         "dist/output.go", "lipc", "runtime/output.exe", "personal.json",
                         "editors/vim/ftplugin/lip.vim", "editors/neovim/lua/lip/init.lua", "editors/emacs/lip-mode.el",
                         "editors/emacs/lip-mode.elc", "editors/neovim/nvim.log", "editors/.cache/private.lua"]:
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("fixture")
            actual = [path.relative_to(root).as_posix() for path in package.source_files(root)]
            self.assertEqual(actual, ["VERSION", "cmd/lipc/learn/harness.go.txt", "cmd/lipc/main.go", "docs/SPEC.md",
                                     "editors/emacs/lip-mode.el", "editors/neovim/lua/lip/init.lua",
                                     "editors/vim/ftplugin/lip.vim", "go.mod", "runtime/python_worker.py"])

    def test_reject_source_symlink(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "runtime").mkdir()
            target = root / "private.go"
            target.write_text("private data")
            try:
                (root / "runtime" / "linked.go").symlink_to(target)
            except (OSError, NotImplementedError):
                self.skipTest("symlinks unavailable")
            with self.assertRaisesRegex(ValueError, "symlinks"):
                package.source_files(root)

    def test_deterministic_archives_preserve_bytes_and_modes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            entries = [("lipc", b"binary", 0o755), ("docs/\u89c4\u8303.md", "你好".encode(), 0o644)]
            for extension in [".tar.gz", ".zip"]:
                a, b = root / ("a" + extension), root / ("b" + extension)
                package.write_archive(a, "lip-0.6.4", entries, 0)
                package.write_archive(b, "lip-0.6.4", entries, 0)
                self.assertEqual(a.read_bytes(), b.read_bytes())
                if extension == ".zip":
                    with zipfile.ZipFile(a) as archive:
                        self.assertEqual(archive.read("lip-0.6.4/lipc"), b"binary")
                        self.assertEqual(archive.getinfo("lip-0.6.4/lipc").external_attr >> 16 & 0o777, 0o755)
                else:
                    with tarfile.open(a) as archive:
                        self.assertEqual(archive.extractfile("lip-0.6.4/lipc").read(), b"binary")
                        self.assertEqual(archive.getmember("lip-0.6.4/lipc").mode, 0o755)


if __name__ == "__main__":
    unittest.main()
