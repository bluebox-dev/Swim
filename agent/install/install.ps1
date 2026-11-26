# SWIM agent bootstrap. Served publicly at https://<server>/dl/agent/install.ps1 and run by the
# one-line command the "Add machine" page generates. That command sets, before running this file:
#   $SwimServer  server URL          $SwimToken  short-lived enrollment token
#   $SwimPin     SHA-256 of the server TLS cert (the command already pins the connection to it)
# Messages are ASCII on purpose: Windows PowerShell 5.1 can mangle UTF-8 text fetched via WebClient.
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
function Say($m) { Write-Host "[SWIM] $m" -ForegroundColor Cyan }

$me = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $me.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'Run this in PowerShell opened with "Run as administrator".'
}
foreach ($v in 'SwimServer', 'SwimToken', 'SwimPin') {
    if (-not (Get-Variable $v -ValueOnly -ErrorAction SilentlyContinue)) { throw "Missing `$$v - copy the full command from the SWIM web page." }
}
if (-not [Environment]::Is64BitOperatingSystem) { throw 'SWIM agent needs 64-bit Windows.' }

$wc = New-Object Net.WebClient
Say "Server $SwimServer"
$latest = $wc.DownloadString("$SwimServer/dl/agent/latest.json") | ConvertFrom-Json
Say "Downloading agent $($latest.version)"
$tmp = Join-Path $env:TEMP "swim-agent-$($latest.version).exe"
$wc.DownloadFile("$SwimServer/dl/agent/$($latest.file)", $tmp)
$hash = (Get-FileHash $tmp -Algorithm SHA256).Hash.ToLower()
if ($hash -ne $latest.sha256) { Remove-Item $tmp -Force; throw "SHA256 mismatch (got $hash) - download refused." }
Say "SHA256 verified"

$bin = Join-Path $env:ProgramFiles 'SWIM'
$data = Join-Path $env:ProgramData 'SWIM'
New-Item -ItemType Directory -Force $bin, $data | Out-Null
# Only SYSTEM and Administrators may read the agent's state (sealed host token, keys).
icacls $data /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' | Out-Null

if (Get-Service SWIMAgent -ErrorAction SilentlyContinue) {
    Say 'Stopping existing SWIM Agent service'
    Stop-Service SWIMAgent -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 2
}
Copy-Item $tmp (Join-Path $bin 'swim-agent.exe') -Force
Remove-Item $tmp -Force
$exe = Join-Path $bin 'swim-agent.exe'

Say 'Enrolling this machine'
& $exe enroll --server $SwimServer --token $SwimToken --pin $SwimPin
if ($LASTEXITCODE -ne 0) { throw 'Enrollment failed (see message above).' }

Say 'Installing Windows service'
& $exe install
if ($LASTEXITCODE -ne 0) { throw 'Service install failed.' }

Start-Sleep -Seconds 3
& $exe status
Say 'Done. This machine now appears on the SWIM web page under Hosts.'
