[CmdletBinding()]
param(
    [ValidateSet('chrome','firefox')][string[]]$Browsers = @('chrome','firefox'),
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA 'Programs\FluxGet')
)
$ErrorActionPreference = 'Stop'
$source = $PSScriptRoot
$InstallDir = [IO.Path]::GetFullPath($InstallDir)
if (Get-Process 'fluxget-gui','fluxget' -ErrorAction SilentlyContinue) { throw 'Close FluxGet before installing or repairing it.' }
$required = @('fluxget.exe','fluxget-gui.exe','fluxget-native-chrome.exe','fluxget-native-firefox.exe','fluxget-native-opera.exe','fluxget.ico','extensions')
foreach ($file in $required) { if (-not (Test-Path (Join-Path $source $file))) { throw "Missing release file: $file" } }
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
if ($source -ne $InstallDir) {
    foreach ($file in ($required + @('README.md','LICENSE','RELEASE-NOTES.md','MEDIA-TOOLS-NOTICES.md','install.ps1','uninstall.ps1','Install.cmd'))) {
        Copy-Item (Join-Path $source $file) $InstallDir -Recurse -Force
    }
}
$hostName = 'io.github.parsania80.fluxget'
$keys = @{chrome='Software\Google\Chrome\NativeMessagingHosts';firefox='Software\Mozilla\NativeMessagingHosts'}
foreach ($browser in $Browsers) {
    $hostBrowser = $browser
    $manifest = Join-Path $InstallDir "native-$hostBrowser.json"
    $value = @{name=$hostName; description='FluxGet browser download bridge'; type='stdio'; path=(Join-Path $InstallDir "fluxget-native-$hostBrowser.exe")}
    if ($hostBrowser -eq 'firefox') { $value.allowed_extensions = @('fluxget@parsania80.github.io') }
    else { $value.allowed_origins = @('chrome-extension://phfanjlpchehjklocnblfjdhgmlcjoaa/') }
    $json = $value | ConvertTo-Json
    [IO.File]::WriteAllText($manifest, $json, (New-Object Text.UTF8Encoding($false)))
    $key = "HKCU:\$($keys[$browser])\$hostName"
    New-Item -Path $key -Force | Out-Null
    Set-Item -Path $key -Value $manifest
    if ((Get-Item $key).GetValue('') -ne $manifest) { throw 'Native host registration verification failed.' }
}
$shell = New-Object -ComObject WScript.Shell
foreach ($folder in @([Environment]::GetFolderPath('Desktop'), (Join-Path ([Environment]::GetFolderPath('StartMenu')) 'Programs'))) {
    $shortcut = $shell.CreateShortcut((Join-Path $folder 'FluxGet.lnk'))
    $shortcut.TargetPath = Join-Path $InstallDir 'fluxget-gui.exe'
    $shortcut.WorkingDirectory = $InstallDir
    $shortcut.IconLocation = Join-Path $InstallDir 'fluxget.ico'
    $shortcut.Save()
}
Write-Host "Installed FluxGet in $InstallDir"
Write-Host "Browser extension folders: $InstallDir\extensions"
Write-Host 'Load/approve the development extension, then enable Browser downloads in FluxGet Settings.'
Write-Host 'No browser permissions or download data were changed.'
