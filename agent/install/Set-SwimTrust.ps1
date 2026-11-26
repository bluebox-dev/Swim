# Set-SwimTrust.ps1 — make one Windows machine trust the SWIM server, the same way the
# organisation GPO does (docs/deploy/windows-trust-policy.md). Run as Administrator.
#
# Expects (set by the one-line command on the "Add machine" page, or edit below):
#   $SwimServer  e.g. https://swim.example.internal      $SwimPin  SHA-256 of the server certificate
#   $SwimTrustRemove = $true   to undo everything this script set
#
# What it sets (all machine-wide policy, reversible):
#   1. Server certificate → LocalMachine\Root (only if it matches the pin)  → no browser TLS warning
#   2. Server URL → Local intranet zone (Site to Zone Assignment List)        → Windows SmartScreen
#      does not prompt for files downloaded from it
#   3. Edge  SmartScreenAllowListDomains / Chrome SafeBrowsingAllowlistDomains → no download warning
# Messages are ASCII: Windows PowerShell 5.1 can mangle UTF-8 text fetched over the web.
$ErrorActionPreference = 'Stop'
function Say($m) { Write-Host "[SWIM] $m" -ForegroundColor Cyan }

$me = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $me.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'Run as Administrator.' }
if (-not $SwimServer -or -not $SwimPin) { throw 'Set $SwimServer and $SwimPin first (copy the full command from the SWIM web page).' }

$uri    = [Uri]$SwimServer
$origin = "$($uri.Scheme)://$($uri.Authority)"
$hostName = $uri.Host
$pin    = $SwimPin.ToUpper()
$ie     = 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\CurrentVersion\Internet Settings'
$zmk    = "$ie\ZoneMapKey"
# Machine-wide zone map in the policy location (what the GPO engine produces from the
# Site to Zone Assignment List). ZoneMapKey alone is NOT read by Windows; tested.
$zmBase = "$ie\ZoneMap"
if ($hostName -match '^\d{1,3}(\.\d{1,3}){3}$') {
    $zmKey = "$zmBase\Ranges\SWIM-$hostName"; $zmRange = $hostName
} else {
    $labels = $hostName.Split('.')
    if ($labels.Count -ge 3) { $zmKey = "$zmBase\Domains\$($labels[-2..-1] -join '.')\$($labels[0..($labels.Count-3)] -join '.')" }
    else { $zmKey = "$zmBase\Domains\$hostName" }
    $zmRange = $null
}
$lists  = @('HKLM:\SOFTWARE\Policies\Microsoft\Edge\SmartScreenAllowListDomains',
            'HKLM:\SOFTWARE\Policies\Google\Chrome\SafeBrowsingAllowlistDomains')

function Remove-ListValue($key, $value) {
    if (-not (Test-Path $key)) { return }
    $p = Get-ItemProperty $key
    foreach ($n in $p.PSObject.Properties.Name) { if ($n -match '^\d+$' -and $p.$n -eq $value) { Remove-ItemProperty $key -Name $n } }
}
function Add-ListValue($key, $value) {
    New-Item -Path $key -Force -ErrorAction SilentlyContinue | Out-Null
    $p = Get-ItemProperty $key
    $names = @($p.PSObject.Properties.Name | Where-Object { $_ -match '^\d+$' })
    foreach ($n in $names) { if ($p.$n -eq $value) { return } }
    $next = 1; while ($names -contains "$next") { $next++ }
    New-ItemProperty -Path $key -Name "$next" -Value $value -PropertyType String -Force | Out-Null
}
function Get-Sha256Hex([byte[]]$b) { ([BitConverter]::ToString([Security.Cryptography.SHA256]::Create().ComputeHash($b)) -replace '-', '') }

if ($SwimTrustRemove) {
    if (Test-Path $zmk) { Remove-ItemProperty $zmk -Name $origin -ErrorAction SilentlyContinue }
    if (Test-Path $zmKey) { Remove-Item $zmKey -Recurse -Force }
    foreach ($k in $lists) { Remove-ListValue $k $hostName }
    $store = New-Object Security.Cryptography.X509Certificates.X509Store('Root', 'LocalMachine'); $store.Open('ReadWrite')
    foreach ($c in @($store.Certificates)) { if ((Get-Sha256Hex $c.RawData) -eq $pin) { $store.Remove($c); Say "Removed certificate $($c.Subject)" } }
    $store.Close()
    Say "Trust settings for $origin removed."
    return
}

# 1. Fetch the server certificate over a connection that only accepts the pinned certificate.
[Net.ServicePointManager]::SecurityProtocol = 'Tls12'
$prev = [Net.ServicePointManager]::ServerCertificateValidationCallback
[Net.ServicePointManager]::ServerCertificateValidationCallback = { param($s, $c) (Get-Sha256Hex $c.GetRawCertData()) -eq $pin }
try {
    $req = [Net.HttpWebRequest]::Create("$origin/v1/health"); $req.Timeout = 15000
    $req.KeepAlive = $false   # don't let the later check reuse this pinned connection
    $req.GetResponse().Close()
    $cert = New-Object Security.Cryptography.X509Certificates.X509Certificate2 (,$req.ServicePoint.Certificate.GetRawCertData())
} catch { throw "Cannot reach $origin, or its certificate does not match the pin: $($_.Exception.Message)" }
finally { [Net.ServicePointManager]::ServerCertificateValidationCallback = $prev }
if ((Get-Sha256Hex $cert.RawData) -ne $pin) { throw 'Certificate pin mismatch - nothing changed.' }

$store = New-Object Security.Cryptography.X509Certificates.X509Store('Root', 'LocalMachine'); $store.Open('ReadWrite'); $store.Add($cert); $store.Close()
Say "1/3 Trusted server certificate: $($cert.Subject) (expires $($cert.NotAfter.ToString('yyyy-MM-dd')))"

# 2. Local intranet zone (same registry the 'Site to Zone Assignment List' GPO writes)
New-Item -Path $zmk -Force -ErrorAction SilentlyContinue | Out-Null
New-ItemProperty -Path $ie -Name 'ListBox_Support_ZoneMapKey' -Value 1 -PropertyType DWord -Force | Out-Null
New-ItemProperty -Path $zmk -Name $origin -Value '1' -PropertyType String -Force | Out-Null
New-Item -Path $zmKey -Force | Out-Null
if ($zmRange) { New-ItemProperty -Path $zmKey -Name ':Range' -Value $zmRange -PropertyType String -Force | Out-Null }
New-ItemProperty -Path $zmKey -Name $uri.Scheme -Value 1 -PropertyType DWord -Force | Out-Null
Say "2/3 $origin -> Local intranet zone"

# 3. Browser download reputation allowlists
foreach ($k in $lists) { Add-ListValue $k $hostName }
Say "3/3 Edge + Chrome: no SmartScreen / Safe Browsing download warnings for $hostName"

# Verify
# Zone is read in a fresh process (urlmon caches per process) and may lag a moment.
$zone = ''
for ($i = 0; $i -lt 5 -and $zone -ne 'Intranet'; $i++) {
    Start-Sleep -Seconds 2
    $zone = (& powershell.exe -NoProfile -Command "[System.Security.Policy.Zone]::CreateFromUrl('$origin/').SecurityZone" | Out-String).Trim()
}
try { (Invoke-WebRequest "$origin/v1/health" -UseBasicParsing -TimeoutSec 15) | Out-Null; $tls = 'trusted by Windows' } catch { $tls = "NOT trusted: $($_.Exception.Message)" }
Say "Check: zone of $origin = $zone ; TLS certificate $tls"
Say 'Done. Restart Edge/Chrome so they pick up the new policies (edge://policy shows them).'
