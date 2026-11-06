; Windows installer for shrink-your-shit, built with Inno Setup 6 (see build.ps1).
;
; Installs for the current user only (no administrator prompt) into %LOCALAPPDATA%\shrink-your-shit,
; downloads ffmpeg if the computer has none that can encode HEVC, starts the program hidden and
; again at every logon (a "Run" registry entry), adds a Start Menu shortcut and opens the web page.
; Silent use:  shrink-your-shit-setup.exe /VERYSILENT /SUPPRESSMSGBOXES /LIBRARY=D:\Videos /ADDR=127.0.0.1:8484

#ifndef AppVersion
  #define AppVersion "0.0.0-dev"
#endif
#ifndef SourceExe
  #define SourceExe "..\..\dist\windows\shrink-your-shit.exe"
#endif
#ifndef OutDir
  #define OutDir "..\..\dist"
#endif

; ffmpeg is downloaded at install time, not bundled: it is GPL software with its own licence.
; Pinned to one release and checked against this SHA-256 (computed from the downloaded file when
; this was written; update both together).
#define FfmpegUrl "https://github.com/GyanD/codexffmpeg/releases/download/9.0.2/ffmpeg-9.0.2-essentials_build.zip"
#define FfmpegSha256 "60f467265b1e312373dbcd92200c2618a74850f98d3d078e94296bb3fa2047ba"

[Setup]
AppId={{6F2C1B1E-6B1F-4D0F-9C1B-5A0B3E7D9A41}
AppName=shrink-your-shit
AppVersion={#AppVersion}
AppPublisher=shrink-your-shit contributors
AppPublisherURL=https://github.com/PeiceOfToast/shrink-your-shit
DefaultDirName={localappdata}\shrink-your-shit
DisableProgramGroupPage=yes
DisableDirPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
OutputDir={#OutDir}
OutputBaseFilename=shrink-your-shit-setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
UninstallDisplayIcon={app}\shrink-your-shit.exe
CloseApplications=no
RestartApplications=no

[Files]
Source: "{#SourceExe}"; DestDir: "{app}"; DestName: "shrink-your-shit.exe"; Flags: ignoreversion
Source: "..\..\LICENSE"; DestDir: "{app}"; Flags: ignoreversion
Source: "extract-ffmpeg.ps1"; Flags: dontcopy

[Icons]
Name: "{autoprograms}\shrink-your-shit"; Filename: "{sys}\wscript.exe"; Parameters: """{app}\launch.vbs"" open"; WorkingDir: "{app}"; Comment: "Start shrink-your-shit and open its web page"
Name: "{autoprograms}\Uninstall shrink-your-shit"; Filename: "{uninstallexe}"

[Registry]
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "shrink-your-shit"; ValueData: """{sys}\wscript.exe"" ""{app}\launch.vbs"""; Flags: uninsdeletevalue

[Run]
Filename: "{sys}\wscript.exe"; Parameters: """{app}\launch.vbs"" wait"; WorkingDir: "{app}"; StatusMsg: "Starting shrink-your-shit..."; Flags: runhidden
Filename: "http://{code:HostForBrowser}"; Description: "Open the shrink-your-shit web page"; Flags: shellexec postinstall nowait skipifsilent

[UninstallRun]
Filename: "{sys}\taskkill.exe"; Parameters: "/F /IM shrink-your-shit.exe"; Flags: runhidden; RunOnceId: "StopApp"

[UninstallDelete]
Type: filesandordirs; Name: "{app}\ffmpeg"
Type: files; Name: "{app}\launch.vbs"
Type: files; Name: "{app}\shrink-your-shit.log"

[Code]
var
  LibraryPage: TInputDirWizardPage;

function Addr: String;
begin
  Result := ExpandConstant('{param:ADDR|}');
  if Result = '' then Result := GetPreviousData('Addr', '127.0.0.1:8484');
end;

function Port: String;
var
  i: Integer;
  a: String;
begin
  a := Addr;
  i := Length(a);
  while (i > 0) and (a[i] <> ':') do i := i - 1;
  if i = 0 then Result := '8484' else Result := Copy(a, i + 1, Length(a));
end;

function HostForBrowser(Param: String): String;
begin
  { the page is opened on this computer, whatever address it listens on }
  Result := '127.0.0.1:' + Port;
end;

function LibraryFolder: String;
begin
  Result := ExpandConstant('{param:LIBRARY|}');
  if Result = '' then Result := LibraryPage.Values[0];
end;

procedure InitializeWizard;
begin
  LibraryPage := CreateInputDirPage(wpWelcome, 'Videos to shrink',
    'Which folder holds your videos?',
    'shrink-your-shit watches this folder and re-encodes new video files in it to save space. ' +
    'It REPLACES the original files once it has checked the result, so try it on a copy first.',
    False, '');
  LibraryPage.Add('');
  LibraryPage.Values[0] := GetPreviousData('Library', ExpandConstant('{%USERPROFILE}\Videos'));
end;

procedure RegisterPreviousData(PreviousDataKey: Integer);
begin
  SetPreviousData(PreviousDataKey, 'Library', LibraryFolder);
  SetPreviousData(PreviousDataKey, 'Addr', Addr);
end;

function HasHevcFfmpegOnPath: Boolean;
var
  OutFile, Cmd: String;
  Code: Integer;
  Text: AnsiString;
begin
  Result := False;
  if (FileSearch('ffmpeg.exe', GetEnv('PATH')) = '') or (FileSearch('ffprobe.exe', GetEnv('PATH')) = '') then Exit;
  OutFile := ExpandConstant('{tmp}\encoders.txt');
  Cmd := '/C ffmpeg -hide_banner -encoders > "' + OutFile + '" 2>&1';
  if Exec(ExpandConstant('{cmd}'), Cmd, '', SW_HIDE, ewWaitUntilTerminated, Code) and (Code = 0) then
    if LoadStringFromFile(OutFile, Text) then
      Result := Pos('libx265', String(Text)) > 0;
end;

function HasOwnFfmpeg: Boolean;
begin
  Result := FileExists(ExpandConstant('{app}\ffmpeg\ffmpeg.exe')) and FileExists(ExpandConstant('{app}\ffmpeg\ffprobe.exe'));
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  Code: Integer;
begin
  Result := '';
  { an older copy may be running: stop it so its file can be replaced }
  Exec(ExpandConstant('{sys}\taskkill.exe'), '/F /IM shrink-your-shit.exe', '', SW_HIDE, ewWaitUntilTerminated, Code);

  if not DirExists(LibraryFolder) then
    if not ForceDirectories(LibraryFolder) then
    begin
      Result := 'The video folder "' + LibraryFolder + '" does not exist and could not be created.';
      Exit;
    end;

  if HasOwnFfmpeg or HasHevcFfmpegOnPath then Exit;
  try
    WizardForm.PreparingLabel.Caption := 'Downloading ffmpeg (about 115 MB, one time)...';
    WizardForm.PreparingLabel.Visible := True;
    DownloadTemporaryFile('{#FfmpegUrl}', 'ffmpeg.zip', '{#FfmpegSha256}', nil);
  except
    Result := 'Could not download ffmpeg, which shrink-your-shit needs: ' + GetExceptionMessage + #13#10 +
      'Check the internet connection and run the installer again, or install ffmpeg yourself ' +
      '(winget install Gyan.FFmpeg) and run the installer again.';
  end;
end;

function Q(S: String): String;
begin
  Result := Chr(34) + S + Chr(34);
end;

function VbsString(S: String): String;
begin
  { a VBScript string literal: double any quotes inside }
  StringChangeEx(S, '"', '""', True);
  Result := '"' + S + '"';
end;

procedure WriteLauncher;
var
  L: TArrayOfString;
begin
  SetArrayLength(L, 38);
  L[0] := 'Option Explicit';
  L[1] := 'Dim sh, env, http, i, q, exe, logf, cmd, probeUrl, wantWait, wantOpen';
  L[2] := 'Set sh = CreateObject("WScript.Shell")';
  L[3] := 'q = Chr(34)';
  L[4] := 'Set env = sh.Environment("Process")';
  L[5] := 'env("SYP_LIBRARIES") = ' + VbsString(LibraryFolder);
  L[6] := 'env("SYP_ADDR") = ' + VbsString(Addr);
  L[7] := 'env("SYP_DATA") = ' + VbsString(ExpandConstant('{app}\data'));
  L[8] := 'env("PATH") = ' + VbsString(ExpandConstant('{app}\ffmpeg')) + ' & ";" & env("PATH")';
  L[9] := 'exe = ' + VbsString(ExpandConstant('{app}\shrink-your-shit.exe'));
  L[10] := 'logf = ' + VbsString(ExpandConstant('{app}\shrink-your-shit.log'));
  L[11] := 'probeUrl = "http://127.0.0.1:' + Port + '/api/status"';
  L[12] := 'wantWait = False : wantOpen = False';
  L[13] := 'If WScript.Arguments.Count > 0 Then';
  L[14] := '  If WScript.Arguments(0) = "wait" Then wantWait = True';
  L[15] := '  If WScript.Arguments(0) = "open" Then wantWait = True : wantOpen = True';
  L[16] := 'End If';
  L[17] := 'Function IsUp()';
  L[18] := '  On Error Resume Next';
  L[19] := '  Set http = CreateObject("MSXML2.ServerXMLHTTP.6.0")';
  L[20] := '  http.setTimeouts 1000, 1000, 1000, 1000';
  L[21] := '  http.Open "GET", probeUrl, False';
  L[22] := '  http.Send';
  L[23] := '  IsUp = (Err.Number = 0 And http.Status = 200)';
  L[24] := '  Err.Clear';
  L[25] := 'End Function';
  L[26] := 'If Not IsUp() Then';
  L[27] := '  sh.CurrentDirectory = ' + VbsString(ExpandConstant('{app}'));
  L[28] := '  cmd = "cmd /c " & q & q & exe & q & " > " & q & logf & q & " 2>&1" & q';
  L[29] := '  sh.Run cmd, 0, False';
  L[30] := 'End If';
  L[31] := 'If wantWait Then';
  L[32] := '  For i = 1 To 40';
  L[33] := '    If IsUp() Then Exit For';
  L[34] := '    WScript.Sleep 500';
  L[35] := '  Next';
  L[36] := 'End If';
  L[37] := 'If wantOpen Then sh.Run "http://127.0.0.1:' + Port + '"';
  SaveStringsToFile(ExpandConstant('{app}\launch.vbs'), L, False);
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  Code: Integer;
  Zip: String;
begin
  if CurStep <> ssPostInstall then Exit;
  Zip := ExpandConstant('{tmp}\ffmpeg.zip');
  if FileExists(Zip) then
  begin
    WizardForm.StatusLabel.Caption := 'Unpacking ffmpeg...';
    ExtractTemporaryFile('extract-ffmpeg.ps1');
    if not Exec(ExpandConstant('{sys}\WindowsPowerShell\v1.0\powershell.exe'),
      '-NoProfile -ExecutionPolicy Bypass -File ' + Q(ExpandConstant('{tmp}\extract-ffmpeg.ps1')) +
      ' -Zip ' + Q(Zip) + ' -Dest ' + Q(ExpandConstant('{app}\ffmpeg')),
      '', SW_HIDE, ewWaitUntilTerminated, Code) or (Code <> 0) then
      RaiseException('Could not unpack ffmpeg (exit code ' + IntToStr(Code) + ').');
  end;
  WriteLauncher;
end;
