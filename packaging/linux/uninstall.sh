#!/bin/sh
set -eu
prefix=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
python3 "$prefix/packaging/install_browser_bridge.py" --prefix "$prefix" --browsers chrome firefox --uninstall
for binary in fluxget fluxget-gui; do
    link="$HOME/.local/bin/$binary"
    if [ "$(readlink "$link" || true)" = "$prefix/$binary" ]; then rm "$link"; fi
done
PREFIX="$prefix" python3 - <<'PY'
import os
from pathlib import Path
launcher=Path.home()/'.local/share/applications/io.github.parsania80.fluxget.desktop'
if launcher.exists() and os.environ['PREFIX']+'/fluxget-gui' in launcher.read_text():
    launcher.unlink()
PY
printf 'Removed registrations and launchers. Close FluxGet and delete %s to finish. Downloads, settings and browser extensions were kept.\n' "$prefix"
