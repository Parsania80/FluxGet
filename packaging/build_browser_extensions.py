#!/usr/bin/env python3
"""Build plain WebExtension folders; no npm/runtime dependency is required."""
import argparse
import json
from pathlib import Path
import shutil

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--out', type=Path, default=ROOT / 'dist/extensions')
args = parser.parse_args()
for browser in ('chrome', 'firefox', 'opera'):
    target = args.out / browser
    target.mkdir(parents=True, exist_ok=True)
    for source in (ROOT / 'extensions/src').iterdir():
        shutil.copy2(source, target / source.name)
    shutil.copy2(ROOT / f'extensions/manifest.{browser}.json', target / 'manifest.json')
    shutil.copy2(ROOT / 'desktop/assets/fluxget.png', target / 'icon.png')
    (target / 'config.js').write_text(f'const FLUXGET_BROWSER = {json.dumps(browser)};\n')
    print(f'{browser}: {target}')

shutil.copy2(ROOT / "extensions/README.md", args.out / "README.md")
