#!/bin/sh
set -eu
source_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
prefix="${FLUXGET_INSTALL_DIR:-$HOME/.local/opt/fluxget}"
if command -v pgrep >/dev/null && pgrep -u "$(id -u)" -x fluxget-gui >/dev/null; then
    echo 'Close FluxGet before installing or repairing it.' >&2
    exit 1
fi
mkdir -p "$prefix" "$HOME/.local/bin" "$HOME/.local/share/applications" "$HOME/.local/share/icons/hicolor/512x512/apps"
if [ "$source_dir" != "$prefix" ]; then cp -R "$source_dir/." "$prefix/"; fi
ln -sfn "$prefix/fluxget" "$HOME/.local/bin/fluxget"
ln -sfn "$prefix/fluxget-gui" "$HOME/.local/bin/fluxget-gui"
cp "$prefix/fluxget.png" "$HOME/.local/share/icons/hicolor/512x512/apps/io.github.parsania80.fluxget.png"
FLUXGET_PREFIX="$prefix" python3 - <<'PY'
import os
from pathlib import Path
prefix = os.environ['FLUXGET_PREFIX']
def quoted(value):
    return '"' + value.replace('\\', '\\\\').replace('"', '\\"').replace('`', '\\`').replace('$', '\\$').replace('%','%%') + '"'
text = '[Desktop Entry]\nType=Application\nName=FluxGet\nComment=Manage file downloads\nExec=' + quoted(prefix+'/fluxget-gui') + '\nIcon=io.github.parsania80.fluxget\nTerminal=false\nCategories=Network;FileTransfer;\n'
(Path.home()/'.local/share/applications/io.github.parsania80.fluxget.desktop').write_text(text)
PY
python3 "$prefix/packaging/install_browser_bridge.py" --prefix "$prefix" --browsers chrome firefox
printf 'Installed FluxGet in %s\nOpen FluxGet from your applications menu. Browser setup: %s/extensions/README.md\n' "$prefix" "$prefix"
