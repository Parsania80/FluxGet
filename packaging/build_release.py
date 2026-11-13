#!/usr/bin/env python3
"""Build the matching desktop, engine, browser hosts and versioned native release installer."""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
VERSION = (ROOT / 'internal/buildinfo/version.txt').read_text().strip()
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--platform', choices=('linux', 'windows'), default='windows' if sys.platform == 'win32' else 'linux')
p.add_argument('--bin-dir', type=Path, help='Package existing, matching binaries instead of building')
p.add_argument('--out', type=Path, default=ROOT / 'dist/release')
args = p.parse_args()
args.out = args.out.resolve()
if args.bin_dir:
    args.bin_dir = args.bin_dir.resolve()
if os.environ.get('GOARCH', 'amd64') != 'amd64':
    p.error('Release packages target amd64')
args.out.mkdir(parents=True, exist_ok=True)
name = f'fluxget-{VERSION}-{args.platform}-amd64'
stage = args.out / 'stage' / name
if stage.exists():
    shutil.rmtree(stage)
stage.mkdir(parents=True)
exe = '.exe' if args.platform == 'windows' else ''

def run(command, cwd=ROOT, env=None):
    subprocess.run(command, cwd=cwd, env=env, check=True)

if args.bin_dir:
    for binary in ('fluxget', 'fluxget-gui', 'fluxget-native-chrome'):
        shutil.copy2(args.bin_dir / (binary + exe), stage / (binary + exe))
else:
    env = dict(os.environ, GOOS=args.platform, GOARCH='amd64')
    plain = dict(env, CGO_ENABLED='0')
    run(['go', 'build', '-trimpath', '-ldflags=-s -w', '-o', str(stage / ('fluxget' + exe)), '.'], env=plain)
    run(['go', 'build', '-trimpath', '-ldflags=-s -w', '-o', str(stage / ('fluxget-native-chrome' + exe)), './cmd/fluxget-native-host'], env=plain)
    flags = '-s -w' + (' -H=windowsgui' if exe else '')
    resource = ROOT / 'desktop/fluxget_windows_amd64.syso'
    try:
        if exe:
            run([os.environ.get('WINDRES', 'windres'), '-i', 'icon.rc', '-o', str(resource)], ROOT / 'packaging/windows')
        run(['go', 'build', '-trimpath', '-ldflags=' + flags, '-o', str(stage / ('fluxget-gui' + exe)), '.'], ROOT / 'desktop', dict(env, CGO_ENABLED='1'))
    finally:
        if exe:
            resource.unlink(missing_ok=True)
for browser in ('firefox', 'opera'):
    shutil.copy2(stage / ('fluxget-native-chrome' + exe), stage / ('fluxget-native-' + browser + exe))
if subprocess.check_output([str(stage / ('fluxget' + exe)), '-version'], text=True).strip() != f'FluxGet {VERSION}':
    raise SystemExit('Packaged engine and source versions differ')
run([sys.executable, 'packaging/build_browser_extensions.py', '--out', str(stage / 'extensions')])
for browser in ('chrome', 'firefox', 'opera'):
    manifest = json.loads((stage / f'extensions/{browser}/manifest.json').read_text())
    if browser != 'firefox':
        digest = hashlib.sha256(base64.b64decode(manifest['key'])).hexdigest()[:32]
        extension_id = ''.join(chr(97 + int(c, 16)) for c in digest)
        if extension_id != 'phfanjlpchehjklocnblfjdhgmlcjoaa':
            raise SystemExit('Chromium extension ID differs from the native allowlist')
    elif manifest['browser_specific_settings']['gecko']['id'] != 'fluxget@parsania80.github.io':
        raise SystemExit('Firefox extension ID differs from the native allowlist')
    if manifest['version'] != VERSION:
        raise SystemExit('Extension and app versions differ')
for source in ('README.md', 'LICENSE'):
    shutil.copy2(ROOT / source, stage / source)
shutil.copy2(ROOT / 'packaging/RELEASE-NOTES.md', stage / 'RELEASE-NOTES.md')
shutil.copy2(ROOT / 'packaging/MEDIA-TOOLS-NOTICES.md', stage / 'MEDIA-TOOLS-NOTICES.md')
shutil.copy2(ROOT / 'desktop/assets/fluxget.png', stage / 'fluxget.png')
if exe:
    for source in ('install.ps1', 'uninstall.ps1', 'Install.cmd'):
        shutil.copy2(ROOT / 'packaging/windows' / source, stage / source)
    shutil.copy2(ROOT / 'desktop/assets/fluxget.ico', stage / 'fluxget.ico')
    compiler = os.environ.get('ISCC') or shutil.which('ISCC.exe') or shutil.which('iscc')
    if not compiler:
        candidate = Path(os.environ.get('ProgramFiles(x86)', 'C:/Program Files (x86)')) / 'Inno Setup 6/ISCC.exe'
        if candidate.is_file():
            compiler = str(candidate)
    if not compiler:
        raise SystemExit('Install Inno Setup 6 to build the Windows setup executable')
    run([compiler, f'/DAppVersion={VERSION}', f'/DStageDir={stage}', f'/DReleaseDir={args.out}', str(ROOT / 'packaging/windows/fluxget.iss')])
    archive = args.out / f'fluxget-{VERSION}-windows-amd64-setup.exe'

else:
    shutil.copy2(ROOT / 'packaging/linux/install.sh', stage / 'install.sh')
    shutil.copy2(ROOT / 'packaging/linux/uninstall.sh', stage / 'uninstall.sh')
    helper = stage / 'packaging'
    helper.mkdir()
    shutil.copy2(ROOT / 'packaging/install_browser_bridge.py', helper / 'install_browser_bridge.py')
    for file in [*stage.glob('fluxget*'), stage / 'install.sh', stage / 'uninstall.sh']:
        if file.suffix not in ('.png', '.ico'):
            file.chmod(0o755)
    run([sys.executable, 'packaging/build_deb.py', '--stage', str(stage), '--out', str(args.out)])
    archive = args.out / f'fluxget_{VERSION}_amd64.deb'

checksum = hashlib.sha256(archive.read_bytes()).hexdigest()
(archive.parent / (archive.name + '.sha256')).write_text(f'{checksum}  {archive.name}\n', newline='\n')
print(f'Release artifact: {archive}')
