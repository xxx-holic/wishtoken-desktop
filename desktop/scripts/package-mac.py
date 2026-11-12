"""Assemble a Mac Electron bundle without host symlink or chmod support.

Mach-O runtime bytes are kept unchanged, including Apple's linker ad-hoc signatures.
ZIP metadata carries the real symlinks and Unix modes. This is not a Developer ID
signed/notarized release and does not imply a successful macOS runtime test.
"""
import argparse
import copy
import hashlib
import json
import plistlib
import shutil
import stat
import struct
import zipfile
from datetime import datetime
from pathlib import Path, PurePosixPath

ROOT = Path(__file__).resolve().parents[2]
NAME = 'WishToken Desktop'
APP_ID = 'app.gptbridge.team'
PREFIX = f'{NAME}.app/Contents/'
CPU = {'arm64': 0x100000C, 'x64': 0x1000007}


def macho_arch(raw):
    if raw[:4] == b'\xcf\xfa\xed\xfe':
        return struct.unpack_from('<I', raw, 4)[0]
    return None


def zip_info(name, mode=0o644):
    item = zipfile.ZipInfo(name, datetime.now().timetuple()[:6])
    item.create_system = 3
    item.external_attr = (stat.S_IFREG | mode) << 16
    item.compress_type = zipfile.ZIP_DEFLATED
    return item


def build(args):
    package = json.loads((ROOT / 'desktop/package.json').read_text(encoding='utf-8'))
    version = package['version']
    backend = ROOT / 'desktop/backend' / f'mac-{args.arch}' / 'gptbridge'
    backend_bytes = backend.read_bytes()
    if macho_arch(backend_bytes) != CPU[args.arch]:
        raise ValueError(f'Backend does not match {args.arch}')
    helper = ROOT / 'desktop/login-helper' / f'mac-{args.arch}' / 'wish-login-http'
    if not helper.is_file() or macho_arch(helper.read_bytes()) != CPU[args.arch]:
        raise ValueError(f'Build the native mac-{args.arch} protocol helper first; cross-compiling Go alone is insufficient')
    output = ROOT / 'desktop/release' / f'WishToken-Desktop-{version}-mac-{args.arch}.zip'
    output.parent.mkdir(parents=True, exist_ok=True)
    temporary = output.with_suffix('.zip.building')
    runtime_hash = hashlib.sha256(args.runtime.read_bytes()).hexdigest()
    runtime_plist = None
    with zipfile.ZipFile(args.runtime) as source, zipfile.ZipFile(temporary, 'w', zipfile.ZIP_DEFLATED, compresslevel=6) as target:
        if any('_CodeSignature/' in n for n in source.namelist()):
            raise ValueError('Signed runtime requires native repackaging and re-signing')
        for item in source.infolist():
            if not item.filename.startswith('Electron.app/'):
                continue
            if item.filename in ('Electron.app/Contents/Resources/default_app.asar', 'Electron.app/Contents/Resources/electron.icns'):
                continue
            name = item.filename.replace('Electron.app/', f'{NAME}.app/', 1)
            name = name.replace('/Electron Helper', f'/{NAME} Helper')
            if name == PREFIX + 'MacOS/Electron':
                name = PREFIX + f'MacOS/{NAME}'
            metadata = copy.copy(item)
            metadata.filename = name
            metadata.create_system = 3
            metadata.compress_type = zipfile.ZIP_DEFLATED
            metadata.flag_bits = 0
            if item.filename.endswith('/Info.plist') and '.framework/' not in item.filename:
                data = plistlib.loads(source.read(item))
                if item.filename == 'Electron.app/Contents/Info.plist':
                    runtime_plist = data.copy()
                    data.update(CFBundleName=NAME, CFBundleDisplayName=NAME, CFBundleExecutable=NAME, CFBundleIdentifier=APP_ID, CFBundleShortVersionString=version, CFBundleVersion=version, CFBundleIconFile='icon.icns', NSHighResolutionCapable=True)
                    data['ElectronAsarIntegrity'] = {'Resources/app.asar': {'algorithm': 'SHA256', 'hash': args.integrity}}
                elif '/Electron Helper' in item.filename:
                    helper = PurePosixPath(name).parts[-3].removesuffix('.app')
                    suffix = helper.removeprefix(NAME + ' Helper').strip().strip('()').lower()
                    data.update(CFBundleName=helper, CFBundleDisplayName=helper, CFBundleExecutable=helper, CFBundleIdentifier=APP_ID + '.helper' + ('.' + suffix if suffix else ''), CFBundleVersion=version)
                target.writestr(metadata, plistlib.dumps(data, sort_keys=False))
            else:
                with source.open(item) as input_file, target.open(metadata, 'w') as output_file:
                    shutil.copyfileobj(input_file, output_file, 1024 * 1024)
        if runtime_plist is None:
            raise ValueError('Missing Electron app Info.plist')
        files = {
            'Resources/app.asar': (args.asar, 0o644),
            'Resources/backend/gptbridge': (backend, 0o755),
            'Resources/login-helper/wish-login-http': (helper, 0o755),
            'Resources/icon.icns': (ROOT / 'desktop/assets/icon.icns', 0o644),
            'Resources/LICENSE.txt': (ROOT / 'LICENSE', 0o644),
            'Resources/NOTICE.md': (ROOT / 'NOTICE.md', 0o644),
        }
        for name, (file, mode) in files.items():
            target.writestr(zip_info(PREFIX + name, mode), file.read_bytes())
        notices = helper.parent / 'licenses'
        if not notices.is_dir():
            raise ValueError('Missing protocol helper dependency notices')
        for notice in sorted(notices.iterdir()):
            if notice.is_file():
                target.writestr(zip_info(PREFIX + 'Resources/login-helper/licenses/' + notice.name), notice.read_bytes())
        for runtime_name, destination in [('LICENSE', 'ELECTRON-LICENSE.txt'), ('LICENSES.chromium.html', 'LICENSES.chromium.html')]:
            if runtime_name in source.namelist():
                target.writestr(zip_info(PREFIX + 'Resources/' + destination), source.read(runtime_name))
        manifest = {
            'version': version, 'architecture': args.arch, 'minimum_macos': runtime_plist.get('LSMinimumSystemVersion'),
            'electron_version': runtime_plist['CFBundleVersion'], 'runtime_archive_sha256': runtime_hash,
            'backend_sha256': hashlib.sha256(backend_bytes).hexdigest(), 'app_asar_sha256': hashlib.sha256(args.asar.read_bytes()).hexdigest(),
            'signing': 'unsigned-development; upstream linker signatures preserved', 'notarized': False, 'macos_runtime_verified': False,
        }
        target.writestr(zip_info('BUILD-INFO.json'), json.dumps(manifest, indent=2) + '\n')
        readme = (ROOT / 'docs/MAC-README.txt').read_text(encoding='utf-8')
        readme = readme.replace('{{VERSION}}', version).replace('{{ARCH}}', args.arch).replace('{{MINIMUM}}', str(runtime_plist.get('LSMinimumSystemVersion', '13.0')))
        target.writestr(zip_info('安装说明.txt'), readme.encode('utf-8'))
    temporary.replace(output)
    print(f'Created {output.name}: {output.stat().st_size} bytes; minimum macOS {runtime_plist.get("LSMinimumSystemVersion")}')
    return output


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--runtime', type=Path, required=True)
    parser.add_argument('--arch', choices=CPU, required=True)
    parser.add_argument('--asar', type=Path, required=True)
    parser.add_argument('--integrity', required=True)
    build(parser.parse_args())
