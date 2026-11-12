"""Verify Linux release identity, source payload, ELF architecture and packaging."""
import hashlib
import json
from pathlib import Path
import stat
import struct
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]
DESKTOP = ROOT / "desktop"
VERSION = json.loads((DESKTOP / "package.json").read_text())["version"]
RELEASE = DESKTOP / "release"


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def asar_files(raw):
    size = struct.unpack_from("<I", raw, 4)[0]
    length = struct.unpack_from("<I", raw, 12)[0]
    tree = json.loads(raw[16:16 + length])
    output = {}

    def visit(files, parent=""):
        for name, item in files.items():
            key = parent + name
            if "files" in item:
                visit(item["files"], key + "/")
            else:
                require("offset" in item and not item.get("unpacked"), "Unexpected ASAR link or unpacked file")
                start = 8 + size + int(item["offset"])
                output[key] = raw[start:start + item["size"]]
    visit(tree["files"])
    return output


def audit_payload(payload):
    expected = {"main.cjs", "preload.cjs", "assets/icon.png", "package.json"}
    for folder in ("lib", "renderer"):
        expected.update(p.relative_to(DESKTOP).as_posix() for p in (DESKTOP / folder).rglob("*") if p.is_file())
    source_package = json.loads((DESKTOP / "package.json").read_text(encoding="utf-8"))
    lock = json.loads((DESKTOP / "package-lock.json").read_text(encoding="utf-8"))
    dependencies = source_package.get("dependencies", {})
    require(set(dependencies) == {"playwright-core"}, "Review new runtime dependencies before packaging")
    for name, version in dependencies.items():
        prefix = "node_modules/" + name
        installed = json.loads((DESKTOP / prefix / "package.json").read_text(encoding="utf-8"))
        require(installed["version"] == version == lock["packages"][prefix]["version"], "Runtime dependency version mismatch")
        # Electron Builder removes package READMEs and TypeScript declarations.
        # Every remaining runtime file must be present and match npm ci's locked source.
        expected.update(p.relative_to(DESKTOP).as_posix() for p in (DESKTOP / prefix).rglob("*")
                        if p.is_file() and p.name != "README.md" and not p.name.endswith(".d.ts"))
    require(set(payload) == expected, "Application file allowlist mismatch")
    pkg = json.loads(payload["package.json"])
    require(pkg["name"] == "wishtoken-desktop" and pkg["version"] == VERSION, "Package identity mismatch")
    require(pkg.get("dependencies", {}) == dependencies, "Packaged runtime dependencies changed")
    for name, raw in payload.items():
        if name.startswith("node_modules/"):
            require(raw == (DESKTOP / name).read_bytes(), "Runtime source mismatch: " + name)
        elif name != "package.json":
            require(raw.replace(b"\r\n", b"\n") == (DESKTOP / name).read_bytes().replace(b"\r\n", b"\n"), "Source mismatch: " + name)


def audit_tree(root):
    asars = list(root.rglob("app.asar"))
    require(len(asars) == 1, "Expected one app.asar")
    app = asars[0].parent.parent
    audit_payload(asar_files(asars[0].read_bytes()))
    for binary in (app / "wishtoken-desktop", app / "resources/backend/gptbridge"):
        raw = binary.read_bytes()
        require(raw[:5] == b"\x7fELF\x02" and struct.unpack_from("<H", raw, 18)[0] == 62, "Expected x86-64 ELF")
        require(binary.stat().st_mode & stat.S_IXUSR, "Executable bit missing")
    denied = {"accounts.json", "auth.json", "config.json", "bridge-identity.json", ".handoff", "sessions"}
    for p in root.rglob("*"):
        require(p.name not in denied, "Private runtime file in package")
    return {name: hashlib.sha256((app / name).read_bytes()).hexdigest() for name in ("resources/app.asar", "resources/backend/gptbridge", "wishtoken-desktop")}


def audit_deb_metadata(fields, version=VERSION):
    metadata = dict(line.split(": ", 1) for line in fields.splitlines() if ": " in line)
    # Electron Builder maps every hyphen to a tilde for Debian prereleases.
    expected_version = version.replace("-", "~")
    require(metadata.get("Version") == expected_version, "deb version mismatch")
    require(metadata.get("Architecture") == "amd64", "deb architecture mismatch")


def main():
    prefix = RELEASE / ("WishToken-Desktop-" + VERSION + "-linux-x64")
    packages = [Path(str(prefix) + ext) for ext in (".AppImage", ".deb", ".tar.gz")]
    reference = audit_tree(RELEASE / "linux-unpacked")
    for package in packages:
        require(package.is_file(), "Missing " + package.name)
        with tempfile.TemporaryDirectory(prefix="wishtoken-package-") as tmp:
            target = Path(tmp)
            if package.suffix == ".deb":
                fields = subprocess.check_output(["dpkg-deb", "-f", str(package), "Version", "Architecture"], text=True)
                audit_deb_metadata(fields)
                subprocess.run(["dpkg-deb", "-x", str(package), str(target)], check=True)
            elif package.suffix == ".AppImage":
                package.chmod(package.stat().st_mode | stat.S_IXUSR)
                offset = subprocess.check_output([str(package), "--appimage-offset"], text=True).strip()
                require(offset.isdigit(), "Invalid AppImage offset")
                subprocess.run(["unsquashfs", "-no-progress", "-o", offset, "-d", str(target / "image"), str(package)], check=True, stdout=subprocess.DEVNULL)
            else:
                with tarfile.open(package) as archive:
                    archive.extractall(target, filter="data")
            require(audit_tree(target) == reference, "Payload differs between package formats")
        print(package.name + ": verified")
    print("Linux release payload verified against source and unpacked application.")


if __name__ == "__main__":
    main()
