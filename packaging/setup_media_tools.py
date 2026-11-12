#!/usr/bin/env python3
"""Provision checksum-locked stage-1 tools into an explicit development directory."""
import argparse
import hashlib
import json
from pathlib import Path
import platform
import shutil
import tarfile
import tempfile
import urllib.request
import zipfile


def setup(destination):
    lock = json.loads(Path(__file__).with_name('media-tools.lock.json').read_text())
    system = platform.system().lower()
    if platform.machine().lower() not in ('amd64', 'x86_64'):
        raise ValueError('Only amd64 is pinned for this spike')
    entries = lock['platforms'][system + '-amd64']
    destination.mkdir(parents=True, exist_ok=True)
    suffix = '.exe' if system == 'windows' else ''
    manifest = {'versions': lock['versions'], 'tools': {}}
    for tool, entry in entries.items():
        with tempfile.TemporaryDirectory(prefix='fluxget-media-') as temporary:
            archive = Path(temporary) / entry['name']
            request = urllib.request.Request(entry['url'], headers={'User-Agent': 'FluxGet-media-spike'})
            digest = hashlib.sha256()
            size = 0
            with urllib.request.urlopen(request, timeout=60) as response, archive.open('wb') as output:
                while block := response.read(1024 * 1024):
                    size += len(block)
                    if size > 256 * 1024 * 1024:
                        raise ValueError('Tool archive exceeds size limit')
                    digest.update(block)
                    output.write(block)
            if digest.hexdigest() != entry['sha256']:
                raise ValueError('Checksum mismatch for ' + tool)
            wanted = ['ffmpeg', 'ffprobe'] if tool == 'ffmpeg' else [tool]
            for name in wanted:
                filename = ('yt-dlp' if name == 'yt_dlp' else name) + suffix
                target = destination / filename
                if entry['name'].endswith('.zip'):
                    with zipfile.ZipFile(archive) as bundle:
                        matches = [m for m in bundle.infolist() if Path(m.filename).name == filename]
                        if len(matches) != 1 or matches[0].file_size > 256 * 1024 * 1024:
                            raise ValueError('Unexpected ZIP contents')
                        with bundle.open(matches[0]) as source, target.open('wb') as output:
                            shutil.copyfileobj(source, output)
                elif entry['name'].endswith('.tar.xz'):
                    with tarfile.open(archive) as bundle:
                        matches = [m for m in bundle.getmembers() if m.isfile() and Path(m.name).name == filename]
                        if len(matches) != 1 or matches[0].size > 256 * 1024 * 1024:
                            raise ValueError('Unexpected TAR contents')
                        with bundle.extractfile(matches[0]) as source, target.open('wb') as output:
                            shutil.copyfileobj(source, output)
                else:
                    shutil.copyfile(archive, target)
                target.chmod(0o755)
                manifest['tools'][name] = str(target.resolve())
        print('Verified and installed development tool:', tool, flush=True)
    # This directory is not a distributable package. Production notices/source
    # obligations for the pinned GPL FFmpeg build are a separate release gate.
    (destination / 'tools.json').write_text(json.dumps(manifest, indent=2) + '\n')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory', type=Path, required=True)
    args = parser.parse_args()
    if not args.directory.is_absolute():
        parser.error('--directory must be absolute')
    setup(args.directory)
