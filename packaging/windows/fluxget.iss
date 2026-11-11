#ifndef AppVersion
  #error AppVersion must be supplied by the release builder
#endif
#ifndef StageDir
  #error StageDir must be supplied by the release builder
#endif
#ifndef ReleaseDir
  #error ReleaseDir must be supplied by the release builder
#endif

[Setup]
AppId={{A4606D9A-6CF1-4F4A-9144-4145A005CF7A}
AppName=FluxGet
AppVersion={#AppVersion}
AppPublisher=FluxGet contributors
AppPublisherURL=https://github.com/Parsania80/FluxGet
DefaultDirName={localappdata}\Programs\FluxGet
DefaultGroupName=FluxGet
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0.17763
OutputDir={#ReleaseDir}
OutputBaseFilename=fluxget-{#AppVersion}-windows-amd64-setup
SetupIconFile={#StageDir}\fluxget.ico
UninstallDisplayIcon={app}\fluxget.ico
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
CloseApplications=yes
DisableProgramGroupPage=yes
LicenseFile={#StageDir}\LICENSE

[Files]
Source: "{#StageDir}\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs

[Run]
Filename: "{app}\fluxget-gui.exe"; Description: "Open FluxGet"; Flags: nowait postinstall skipifsilent

[UninstallRun]
Filename: "{sys}\WindowsPowerShell\v1.0\powershell.exe"; Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\uninstall.ps1"""; Flags: runhidden waituntilterminated skipifdoesntexist; RunOnceId: "FluxGetBrowserCleanup"

[Code]
procedure CurStepChanged(CurStep: TSetupStep);
var
  ResultCode: Integer;
  Parameters: String;
begin
  if CurStep = ssPostInstall then
  begin
    Parameters := '-NoProfile -ExecutionPolicy Bypass -File "' +
      ExpandConstant('{app}\install.ps1') + '" -InstallDir "' +
      ExpandConstant('{app}') + '"';
    if not Exec(ExpandConstant('{sys}\WindowsPowerShell\v1.0\powershell.exe'),
      Parameters, '', SW_HIDE, ewWaitUntilTerminated, ResultCode) then
      RaiseException('Could not configure FluxGet browser hosts and shortcuts.');
    if ResultCode <> 0 then
      RaiseException('FluxGet setup could not configure browser hosts and shortcuts. Close FluxGet and run setup again.');
  end;
end;

[UninstallDelete]
Type: files; Name: "{app}\native-chrome.json"
Type: files; Name: "{app}\native-firefox.json"
