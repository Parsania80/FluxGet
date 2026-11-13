#!/usr/bin/env python3
"""Verify the installed engine's explicit first-use tool setup in a private cache."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--engine', type=Path, required=True)
parser.add_argument('--proxy', help='Optional explicit proxy for the upstream setup downloads')
args = parser.parse_args()
command = [str(args.engine.resolve()), '-setup-media-tools']
if args.proxy:
    command += ['-proxy', args.proxy]
with tempfile.TemporaryDirectory(prefix='fluxget-installed-media-') as temporary:
    cache = Path(temporary) / 'cache'
    env = dict(os.environ, XDG_CACHE_HOME=str(cache), LOCALAPPDATA=str(cache))
    output = subprocess.check_output(command, env=env, text=True)
    manifest = Path(json.loads(output))
    if not manifest.is_relative_to(cache):
        raise SystemExit('Setup wrote outside its isolated cache')
    tools = json.loads(manifest.read_text())['tools']
    for executable in tools.values():
        if not Path(executable).is_relative_to(manifest.parent) or not Path(executable).is_file():
            raise SystemExit('Installed tools are not self-contained')
    repeated = subprocess.check_output(command, env=env, text=True)
    if json.loads(repeated) != str(manifest):
        raise SystemExit('Repeated setup changed the tool location')
    env['FLUXGET_MEDIA_TOOLS'] = str(manifest)
    env.pop('FLUXGET_MEDIA_PROXY', None)
    subprocess.run(['go', 'test', '-race', '-count=1', '-v', '-run', '^TestNativeMediaTools$', './media'], env=env, check=True)
    print('Installed engine setup, pinned versions, MP4 merge, MP3 conversion and proxy/direct native checks passed')
