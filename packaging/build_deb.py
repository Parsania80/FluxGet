#!/usr/bin/env python3
"""Create a system-wide Debian package from an already built release stage."""
import argparse
import json
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--stage', type=Path, required=True)
p.add_argument('--out', type=Path, required=True)
args = p.parse_args()
version = (ROOT / 'internal/buildinfo/version.txt').read_text().strip()
args.out.mkdir(parents=True, exist_ok=True)
artifact = args.out.resolve() / f'fluxget_{version}_amd64.deb'
with tempfile.TemporaryDirectory(prefix='fluxget-deb-') as temp:
    package = Path(temp)
    package.chmod(0o755)
    app = package / 'opt/fluxget'
    shutil.copytree(args.stage, app, ignore=shutil.ignore_patterns('install.sh', 'uninstall.sh', 'packaging'))
    for name in ('fluxget', 'fluxget-gui', 'fluxget-native-chrome', 'fluxget-native-firefox', 'fluxget-native-opera'):
        path = app / name
        if not path.is_file():
            raise SystemExit(f'Missing package executable: {name}')
        path.chmod(0o755)
    binaries = package / 'usr/bin'
    binaries.mkdir(parents=True)
    for name in ('fluxget', 'fluxget-gui'):
        (binaries / name).symlink_to(f'/opt/fluxget/{name}')
    launcher = package / 'usr/share/applications/io.github.parsania80.fluxget.desktop'
    launcher.parent.mkdir(parents=True)
    shutil.copy2(ROOT / 'packaging/linux/io.github.parsania80.fluxget.desktop', launcher)
    icon = package / 'usr/share/pixmaps/io.github.parsania80.fluxget.png'
    icon.parent.mkdir(parents=True)
    shutil.copy2(ROOT / 'desktop/assets/fluxget.png', icon)
    docs = package / 'usr/share/doc/fluxget'
    docs.mkdir(parents=True)
    shutil.copy2(ROOT / 'LICENSE', docs / 'copyright')
    for browser, directory in (('chrome', 'etc/opt/chrome/native-messaging-hosts'), ('firefox', 'usr/lib/mozilla/native-messaging-hosts')):
        path = package / directory / 'io.github.parsania80.fluxget.json'
        path.parent.mkdir(parents=True, exist_ok=True)
        manifest = {'name': 'io.github.parsania80.fluxget', 'description': 'FluxGet browser download bridge', 'type': 'stdio', 'path': f'/opt/fluxget/fluxget-native-{browser}'}
        if browser == 'firefox':
            manifest['allowed_extensions'] = ['fluxget@parsania80.github.io']
        else:
            manifest['allowed_origins'] = ['chrome-extension://phfanjlpchehjklocnblfjdhgmlcjoaa/']
        path.write_text(json.dumps(manifest, indent=2) + '\n')
    installed_size = (sum(f.stat().st_size for f in package.rglob('*') if f.is_file() and not f.is_symlink()) + 1023) // 1024
    control = package / 'DEBIAN/control'
    control.parent.mkdir()
    dependencies = 'libc6 (>= 2.35), libgl1, libx11-6, libxrandr2, libxxf86vm1, libxi6, libxcursor1, libxinerama1, libwayland-client0, libwayland-cursor0, libwayland-egl1, libxkbcommon0'
    control.write_text(f'''Package: fluxget
Version: {version}
Section: net
Priority: optional
Architecture: amd64
Maintainer: FluxGet contributors <parsania80@gmail.com>
Installed-Size: {installed_size}
Depends: {dependencies}
Homepage: https://github.com/Parsania80/FluxGet
Description: Desktop download manager with safe resume and browser integration
 Manage public HTTP and HTTPS downloads, speed limits and file parts.
 Includes the desktop, command-line engine and Chrome/Firefox native hosts.
 Browser extensions require separate user approval.
''')
    executables = {'fluxget', 'fluxget-gui', 'fluxget-native-chrome', 'fluxget-native-firefox', 'fluxget-native-opera'}
    for path in package.rglob('*'):
        if path.is_symlink():
            continue
        path.chmod(0o755 if path.is_dir() or (path.parent == app and path.name in executables) else 0o644)
    subprocess.run(['dpkg-deb', '--root-owner-group', '--build', str(package), str(artifact)], check=True)
print(f'Debian installer: {artifact}')
