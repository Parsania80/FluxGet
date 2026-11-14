$ErrorActionPreference = 'Stop'
$folder = Get-Item -LiteralPath $env:FLUXGET_EXTENSION_FOLDER
if (-not $folder.PSIsContainer) { throw 'Extension folder is missing.' }
# Open a new Explorer window even when this folder is already open.
$arguments = '/n,"' + $folder.FullName + '"'
Start-Process -FilePath (Join-Path $env:WINDIR 'explorer.exe') -ArgumentList $arguments -WindowStyle Normal
