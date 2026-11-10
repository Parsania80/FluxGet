#!/usr/bin/env python3
"""Register FluxGet's native host for explicitly selected Linux browsers."""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import shutil
import sys

ROOT = Path(__file__).resolve().parents[1]
NAME = 'io.github.parsania80.fluxget'
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--prefix', type=Path, default=Path.home()/'.local/opt/fluxget')
parser.add_argument('--browsers', nargs='+', choices=('chrome','firefox','opera'), required=True)
parser.add_argument('--uninstall', action='store_true')
args = parser.parse_args()
if not sys.platform.startswith('linux'):
    parser.error('This registration helper currently supports Linux only')
config = Path(os.environ.get('XDG_CONFIG_HOME', Path.home()/'.config'))
manifest = json.loads((ROOT/'extensions/manifest.chrome.json').read_text())
digest = hashlib.sha256(base64.b64decode(manifest['key'])).hexdigest()[:32]
chrome_id = ''.join(chr(97+int(c,16)) for c in digest)
locations = {'chrome': config/'google-chrome/NativeMessagingHosts',
             'firefox': Path.home()/'.mozilla/native-messaging-hosts',
             'opera': config/'opera/NativeMessagingHosts'}
for browser in args.browsers:
    destination = locations[browser]/(NAME+'.json')
    if args.uninstall:
        if destination.exists():
            current = json.loads(destination.read_text())
            if current.get('name') != NAME or current.get('path') != str((args.prefix/f'fluxget-native-{browser}').resolve()):
                raise SystemExit(f'Refusing to remove another registration: {destination}')
            destination.unlink()
        print(f'{browser}: registration removed; extension and downloads kept')
        continue
    executable = (args.prefix/f'fluxget-native-{browser}').resolve()
    if not executable.is_file() or not os.access(executable,os.X_OK):
        raise SystemExit(f'Build/install the native executable first: {executable}')
    value = {'name':NAME,'description':'FluxGet browser download bridge','type':'stdio','path':str(executable)}
    if browser=='firefox': value['allowed_extensions']=['fluxget@parsania80.github.io']
    else: value['allowed_origins']=[f'chrome-extension://{chrome_id}/']
    if destination.exists():
        old=json.loads(destination.read_text())
        if old.get('name')!=NAME:raise SystemExit(f'Unexpected existing manifest: {destination}')
        shutil.copy2(destination,destination.with_suffix('.json.before-fluxget'))
    destination.parent.mkdir(parents=True,exist_ok=True)
    staged=destination.with_suffix('.json.new')
    staged.write_text(json.dumps(value,indent=2)+'\n');staged.chmod(0o600);staged.replace(destination)
    assert json.loads(destination.read_text())==value
    print(f'{browser}: {destination}')
    if browser=='firefox': print('Firefox Snap/Flatpak also require a working native-messaging portal; registration alone does not prove readiness.')
    if browser=='opera': print('Opera requires installed-version native-host discovery testing before support is claimed.')
print('No browser extensions or browser profile databases were changed. Load/approve the extension yourself.')
