#define MyAppName "AwayTrace"
#define MyAppVersion "1.0.2"

[Setup]
AppId={{F7CEEF54-1668-46BA-99C8-EE397A0E20C5}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher=AwayTrace
DefaultDirName={localappdata}\Programs\AwayTrace
DefaultGroupName=AwayTrace
UsePreviousAppDir=no
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
AppMutex=Local\AwayTrace.SingleInstance,Local\ScreenGuard.SingleInstance
UninstallDisplayIcon={app}\AwayTrace.exe
OutputBaseFilename=AwayTrace-Setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern

[Files]
Source: "dist\AwayTrace.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "README.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "THIRD-PARTY.md"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\AwayTrace"; Filename: "{app}\AwayTrace.exe"
Name: "{autoprograms}\Uninstall AwayTrace"; Filename: "{uninstallexe}"

[Registry]
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "AwayTrace"; ValueData: """{app}\AwayTrace.exe"""; Flags: uninsdeletevalue; Check: ShouldEnableStartup

[Run]
Filename: "{app}\AwayTrace.exe"; Description: "Start AwayTrace"; Flags: nowait postinstall skipifsilent

[Code]
function ShouldEnableStartup(): Boolean;
var
  Existing, OldPath: String;
begin
  if RegQueryStringValue(HKCU, 'Software\Microsoft\Windows\CurrentVersion\Run',
                         'AwayTrace', Existing) then begin
    Result := True;
    exit;
  end;
  OldPath := ExpandConstant('{localappdata}\Programs\ScreenGuard\ScreenGuard.exe');
  if RegQueryStringValue(HKCU, 'Software\Microsoft\Windows\CurrentVersion\Run',
                         'ScreenGuard', Existing) then begin
    if (CompareText(Trim(Existing), '"' + OldPath + '"') = 0) or
       (CompareText(Trim(Existing), OldPath) = 0) then begin
      Result := True;
      exit;
    end;
  end;
  Result := not FileExists(ExpandConstant('{localappdata}\AwayTrace\settings.json')) and
            not FileExists(ExpandConstant('{localappdata}\ScreenGuard\settings.json'));
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  OldValue, OldPath: String;
begin
  if CurStep = ssPostInstall then begin
    OldPath := ExpandConstant('{localappdata}\Programs\ScreenGuard\ScreenGuard.exe');
    if RegQueryStringValue(HKCU, 'Software\Microsoft\Windows\CurrentVersion\Run',
                           'ScreenGuard', OldValue) then begin
      if (CompareText(Trim(OldValue), '"' + OldPath + '"') = 0) or
         (CompareText(Trim(OldValue), OldPath) = 0) then
        RegDeleteValue(HKCU, 'Software\Microsoft\Windows\CurrentVersion\Run', 'ScreenGuard');
    end;
  end;
end;

