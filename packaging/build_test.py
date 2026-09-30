"""Run with: python3 packaging/build_test.py (no compiler or real ROMs needed)."""
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
import zipfile


class BuildTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="switchmgr-build-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / "packaging/winres").mkdir(parents=True)
        (self.root / "cmd/switchmgr").mkdir(parents=True)
        shutil.copyfile(Path(__file__).with_name("build.sh"), self.root / "packaging/build.sh")
        (self.root / "README.md").write_text("test readme", encoding="utf-8")
        self.dist = self.root / "dist"
        self.dist.mkdir()
        self.old = self.dist / "switchmgr.exe"
        self.old.write_bytes(b"old release must survive")
        self.bin = self.root / "bin"
        self.bin.mkdir()
        stub = f"#!{sys.executable}\n" + '''import os, pathlib, sys
name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
with (pathlib.Path(sys.argv[0]).parent.parent / 'calls.log').open('a') as f:
    f.write(name + ' ' + ' '.join(args) + '\\n')
if os.environ.get('FAIL_TOOL') == name:
    sys.exit(7)
if name == 'go' and args[0] == 'build':
    if os.environ.get('FAIL_BUILD_TARGET') == os.environ.get('GOOS', '') + '/' + os.environ.get('GOARCH', ''):
        sys.exit(8)
    pathlib.Path(args[args.index('-o') + 1]).write_bytes(b'test binary')
    if os.environ.get('PUBLISH_COLLISION'):
        pathlib.Path('dist/0.4.1').mkdir(exist_ok=True)
'''
        for name in ("go", "node", "go-winres"):
            path = self.bin / name
            path.write_text(stub, encoding="utf-8")
            path.chmod(0o755)
        self.env = dict(os.environ, HOME=str(self.root), PATH=str(self.bin) + os.pathsep + os.environ["PATH"])

    def run_build(self, version="0.4.1", **env):
        result = subprocess.run(
            ["bash", str(self.root / "packaging/build.sh"), version],
            cwd=self.root, env=dict(self.env, **env), text=True, capture_output=True,
        )
        self.assertEqual(self.old.read_bytes(), b"old release must survive")
        self.assertEqual(list(self.dist.glob(".build-*")), [], result.stderr)
        return result

    def test_publish_keeps_old_files_and_verifies_package(self):
        result = self.run_build()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        release = self.dist / "0.4.1"
        package = "my-switch-manager-0.4.1-windows-amd64"
        expected = {"switchmgr.exe", "switchmgr-linux-amd64", "switchmgr-linux-arm64", package + ".zip"}
        self.assertEqual({p.name for p in release.iterdir()}, expected | {"SHA256SUMS"})
        hashes = {}
        for line in (release / "SHA256SUMS").read_text().splitlines():
            digest, name = line.split("  ", 1)
            self.assertEqual(hashlib.sha256((release / name).read_bytes()).hexdigest(), digest)
            hashes[name] = digest
        self.assertEqual(set(hashes), expected)
        with zipfile.ZipFile(release / (package + ".zip")) as archive:
            self.assertIsNone(archive.testzip())
            self.assertEqual(archive.read(package + "/switchmgr.exe"), (release / "switchmgr.exe").read_bytes())
            self.assertEqual(archive.read(package + "/README.md"), b"test readme")
        calls = (self.root / "calls.log").read_text()
        self.assertIn("node --check internal/server/web/app.js", calls)
        self.assertIn("node --test internal/server/preview.test.js", calls)
        self.assertIn("--product-version 0.4.1.0 --file-version 0.4.1.0", calls)
        self.assertIn("-X main.version=0.4.1", calls)

    def test_existing_release_is_rejected_before_any_tools_run(self):
        release = self.dist / "0.4.1"
        release.mkdir()
        result = self.run_build()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.root / "calls.log").exists())
        self.assertEqual(list(release.iterdir()), [])

    def test_dangling_release_symlink_is_rejected(self):
        release = self.dist / "0.4.1"
        release.symlink_to(self.root / "missing")
        self.assertNotEqual(self.run_build().returncode, 0)
        self.assertTrue(release.is_symlink())
        self.assertFalse((self.root / "calls.log").exists())

    def test_invalid_versions_cannot_escape_output_directory(self):
        for version in ("../outside", "1.2", "1.2.3-rc1", "1.2.65536", "1.2.3.4.5", ""):
            with self.subTest(version=version):
                self.assertNotEqual(self.run_build(version).returncode, 0)
                self.assertFalse((self.root / "calls.log").exists())

    def test_failed_checks_or_resources_do_not_publish(self):
        for tool in ("go", "node", "go-winres"):
            with self.subTest(tool=tool):
                self.assertNotEqual(self.run_build(FAIL_TOOL=tool).returncode, 0)
                self.assertFalse((self.dist / "0.4.1").exists())

    def test_partial_build_is_removed_without_publishing(self):
        result = self.run_build(FAIL_BUILD_TARGET="linux/arm64")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.dist / "0.4.1").exists())
        self.assertIn("构建 linux/arm64", result.stdout)

    def test_concurrent_publish_cannot_replace_even_empty_release(self):
        self.assertNotEqual(self.run_build(PUBLISH_COLLISION="1").returncode, 0)
        self.assertEqual(list((self.dist / "0.4.1").iterdir()), [])

    def test_four_part_version_is_preserved(self):
        result = self.run_build("0.4.1.2")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("--file-version 0.4.1.2", (self.root / "calls.log").read_text())
        self.assertTrue((self.dist / "0.4.1.2/switchmgr.exe").is_file())


if __name__ == "__main__":
    unittest.main()
