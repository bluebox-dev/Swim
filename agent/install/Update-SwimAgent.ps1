# Update-SwimAgent.ps1 — upgrade an agent that predates the update_agent command
# (agent ≤ 0.3). Run through SWIM "run script" (as SYSTEM) on those machines once;
# from agent 0.4 on, use the update_agent command instead.
#
# Reads server + certificate pin from the agent's own state, downloads latest.json and
# the exe over the pinned connection, checks SHA-256 and the reported version, then
# swaps binaries with a detached script that rolls back if the service does not start.
$ErrorActionPreference = 'Stop'
$st = Get-Content (Join-Path $env:ProgramData 'SWIM\state.json') -Raw | ConvertFrom-Json
$server = $st.server.TrimEnd('/'); $pin = $st.cert_sha256.ToUpper()
[Net.ServicePointManager]::SecurityProtocol = 'Tls12'
[Net.ServicePointManager]::ServerCertificateValidationCallback = {
    param($a, $c) ([BitConverter]::ToString([Security.Cryptography.SHA256]::Create().ComputeHash($c.GetRawCertData())) -replace '-', '') -eq $pin }
$wc = New-Object Net.WebClient
$latest = $wc.DownloadString("$server/dl/agent/latest.json") | ConvertFrom-Json
$dir = Join-Path $env:ProgramFiles 'SWIM'
$exe = Join-Path $dir 'swim-agent.exe'; $new = Join-Path $dir 'swim-agent.new.exe'; $old = Join-Path $dir 'swim-agent.old.exe'
$current = (& $exe version) -join ''
if ($current -match [regex]::Escape($latest.version)) { "Already on $current"; exit 0 }
$wc.DownloadFile("$server/dl/agent/$($latest.file)", $new)
$sum = (Get-FileHash $new -Algorithm SHA256).Hash.ToLower()
if ($sum -ne $latest.sha256) { Remove-Item $new -Force; throw "SHA-256 mismatch ($sum) - update refused" }
$v = (& $new version) -join ''
if ($v -notmatch [regex]::Escape($latest.version)) { Remove-Item $new -Force; throw "new binary reports '$v'" }
$script = Join-Path $env:TEMP 'swim-update.cmd'
@"
@echo off
set LOG=%WINDIR%\Temp\swim-update.log
echo %date% %time% update start (script) >> "%LOG%"
ping -n 4 127.0.0.1 >nul
sc stop SWIMAgent >> "%LOG%" 2>&1
ping -n 9 127.0.0.1 >nul
move /y "$exe" "$old" >> "%LOG%" 2>&1
move /y "$new" "$exe" >> "%LOG%" 2>&1
sc start SWIMAgent >> "%LOG%" 2>&1
ping -n 31 127.0.0.1 >nul
sc query SWIMAgent | find "RUNNING" >nul
if errorlevel 1 (
  echo %date% %time% new agent not running - rolling back >> "%LOG%"
  sc stop SWIMAgent >nul 2>&1
  ping -n 6 127.0.0.1 >nul
  move /y "$old" "$exe" >> "%LOG%" 2>&1
  sc start SWIMAgent >> "%LOG%" 2>&1
) else (
  echo %date% %time% update ok >> "%LOG%"
)
(goto) 2>nul & del "%~f0"
"@ | Set-Content -Path $script -Encoding ASCII
Start-Process -FilePath cmd.exe -ArgumentList '/c', "`"$script`"" -WindowStyle Hidden
"$current -> $($latest.version): downloaded, SHA-256 verified, switching in ~10 s (auto-rollback if it does not start)"
