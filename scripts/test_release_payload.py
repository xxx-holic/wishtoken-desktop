"""Release guard regressions use synthetic files, never runtime account data."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("release_guard", Path(__file__).with_name("check-linux-release.py"))
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)


class PayloadTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        prior = guard.DESKTOP
        self.addCleanup(setattr, guard, "DESKTOP", prior)
        guard.DESKTOP = Path(self.temp.name)
        package = {"name": "wishtoken-desktop", "version": guard.VERSION, "dependencies": {"playwright-core": "1.63.0"}}
        self.payload = {
            "package.json": json.dumps(package).encode(),
            "main.cjs": b"main", "preload.cjs": b"preload", "assets/icon.png": b"fixture",
            "lib/work.cjs": b"fixture", "renderer/index.html": b"fixture",
            "node_modules/playwright-core/package.json": b'{"name":"playwright-core","version":"1.63.0"}',
            "node_modules/playwright-core/index.js": b"fixture-runtime",
            "node_modules/playwright-core/LICENSE": b"fixture-license",
        }
        for name, data in self.payload.items():
            path = guard.DESKTOP / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(data)
        (guard.DESKTOP / "package-lock.json").write_text(json.dumps({"packages": {"node_modules/playwright-core": {"version": "1.63.0"}}}), encoding="utf-8")
        for name in ("README.md", "index.d.ts"):
            (guard.DESKTOP / "node_modules/playwright-core" / name).write_bytes(b"omitted by packager")

    def test_exact_locked_runtime_is_accepted(self):
        guard.audit_payload(self.payload)

    def test_missing_and_unexpected_files_are_rejected(self):
        for name in ("node_modules/playwright-core/index.js", "lib/work.cjs"):
            payload = dict(self.payload)
            del payload[name]
            with self.assertRaisesRegex(RuntimeError, "allowlist mismatch"):
                guard.audit_payload(payload)
        payload = {**self.payload, "node_modules/unreviewed/index.js": b"unexpected"}
        with self.assertRaisesRegex(RuntimeError, "allowlist mismatch"):
            guard.audit_payload(payload)

    def test_modified_runtime_is_rejected(self):
        payload = {**self.payload, "node_modules/playwright-core/index.js": b"changed"}
        with self.assertRaisesRegex(RuntimeError, "Runtime source mismatch"):
            guard.audit_payload(payload)

    def test_wrong_installed_version_is_rejected(self):
        path = guard.DESKTOP / "node_modules/playwright-core/package.json"
        path.write_bytes(b'{"version":"0.0.0"}')
        with self.assertRaisesRegex(RuntimeError, "version mismatch"):
            guard.audit_payload(self.payload)


class DebMetadataTests(unittest.TestCase):
    def test_stable_and_prerelease_versions_are_accepted(self):
        for version, deb_version in (("0.8.3", "0.8.3"), ("0.9.0-kilo-preview.2", "0.9.0~kilo~preview.2")):
            with self.subTest(version=version):
                guard.audit_deb_metadata(f"Version: {deb_version}\nArchitecture: amd64\n", version)

    def test_wrong_version_and_partial_matches_are_rejected(self):
        for version in ("0.9.0-kilo-preview.2", "0.9.0~kilo~preview.1", "0.9.0~kilo~preview.20"):
            with self.subTest(version=version), self.assertRaisesRegex(RuntimeError, "deb version mismatch"):
                guard.audit_deb_metadata(f"Version: {version}\nArchitecture: amd64\n", "0.9.0-kilo-preview.2")

    def test_missing_and_wrong_architecture_are_rejected(self):
        for fields in ("Version: 0.8.3\n", "Version: 0.8.3\nArchitecture: arm64\n", "Version: 0.8.3\nArchitecture: not-amd64\n"):
            with self.subTest(fields=fields), self.assertRaisesRegex(RuntimeError, "deb architecture mismatch"):
                guard.audit_deb_metadata(fields, "0.8.3")


if __name__ == "__main__":
    unittest.main()
