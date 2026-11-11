[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
$hostName = 'io.github.parsania80.fluxget'
foreach ($browser in @('chrome','firefox')) {
    $base = if ($browser -eq 'chrome') { 'Google\Chrome' } else { 'Mozilla' }
    $key = "HKCU:\Software\$base\NativeMessagingHosts\$hostName"
    $expected = Join-Path $PSScriptRoot "native-$browser.json"
    if ((Test-Path $key) -and (Get-Item $key).GetValue('') -eq $expected) { Remove-Item $key }
}
$shell = New-Object -ComObject WScript.Shell
foreach ($folder in @([Environment]::GetFolderPath('Desktop'), (Join-Path ([Environment]::GetFolderPath('StartMenu')) 'Programs'))) {
    $path = Join-Path $folder 'FluxGet.lnk'
    if ((Test-Path $path) -and ($shell.CreateShortcut($path).TargetPath -eq (Join-Path $PSScriptRoot 'fluxget-gui.exe'))) { Remove-Item $path }
}
Write-Host 'Removed FluxGet registrations and shortcuts. Close FluxGet and delete its application folder to finish.'
Write-Host 'Downloads, saved progress, settings, browser permissions and extensions were kept.'
