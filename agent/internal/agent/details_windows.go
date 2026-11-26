//go:build windows

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// detailsScript gathers the full machine profile shown on the host page.
// Everything is best effort: a missing module or access error leaves a field null.
const detailsScript = `[Console]::OutputEncoding=[Text.Encoding]::UTF8
$ErrorActionPreference='SilentlyContinue'
function iso($d){ if($d){ ([datetime]$d).ToUniversalTime().ToString('o') } }
$os=Get-CimInstance Win32_OperatingSystem; $cs=Get-CimInstance Win32_ComputerSystem; $bios=Get-CimInstance Win32_BIOS
$p=Get-CimInstance Win32_ComputerSystemProduct; $cpu=Get-CimInstance Win32_Processor | Select-Object -First 1
$cv=Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion'
$sb=$null; try { $sb=[bool](Confirm-SecureBootUEFI) } catch {}
$tpm=$null; $t=Get-CimInstance -Namespace root/cimv2/Security/MicrosoftTpm -ClassName Win32_Tpm
if($t){ $tpm=[ordered]@{version=(($t.SpecVersion -split ',')[0]).Trim(); enabled=$t.IsEnabled_InitialValue; activated=$t.IsActivated_InitialValue} }
$bl=$null; $v=Get-CimInstance -Namespace root/cimv2/Security/MicrosoftVolumeEncryption -ClassName Win32_EncryptableVolume -Filter "DriveLetter='C:'"
if($v){ $bl=[ordered]@{protection=[int]$v.ProtectionStatus; conversion=[int]$v.ConversionStatus} }
$def=$null; $m=Get-MpComputerStatus
if($m){ $def=[ordered]@{realtime=$m.RealTimeProtectionEnabled; antivirus=$m.AntivirusEnabled; signature_age_days=$m.AntivirusSignatureAge; signature_version=$m.AntivirusSignatureVersion; signature_updated=(iso $m.AntivirusSignatureLastUpdated)} }
$fw=@(Get-NetFirewallProfile | ForEach-Object { [ordered]@{profile=$_.Name; enabled=[bool]$_.Enabled} })
$disks=@(Get-CimInstance Win32_LogicalDisk -Filter 'DriveType=3' | ForEach-Object { [ordered]@{drive=$_.DeviceID; label=$_.VolumeName; fs=$_.FileSystem; size=[int64]$_.Size; free=[int64]$_.FreeSpace} })
$phys=@(Get-PhysicalDisk | ForEach-Object { [ordered]@{model=$_.FriendlyName; media=[string]$_.MediaType; bus=[string]$_.BusType; size=[int64]$_.Size; health=[string]$_.HealthStatus} })
$nics=@(Get-NetAdapter | Where-Object { $_.Status -eq 'Up' -and $_.HardwareInterface } | ForEach-Object {
  $ix=$_.ifIndex; [ordered]@{name=$_.Name; description=$_.InterfaceDescription; mac=$_.MacAddress; speed=[string]$_.LinkSpeed
    ipv4=@(Get-NetIPAddress -InterfaceIndex $ix -AddressFamily IPv4 | ForEach-Object { $_.IPAddress })
    gateway=@(Get-NetRoute -InterfaceIndex $ix -DestinationPrefix '0.0.0.0/0' | ForEach-Object { $_.NextHop })
    dns=@((Get-DnsClientServerAddress -InterfaceIndex $ix -AddressFamily IPv4).ServerAddresses) } })
$bat=Get-CimInstance Win32_Battery | Select-Object -First 1
$pend=(Test-Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending') -or (Test-Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired')
$rdp=$null; try {
  $ts=Get-ItemProperty 'HKLM:\System\CurrentControlSet\Control\Terminal Server'
  $tcp=Get-ItemProperty 'HKLM:\System\CurrentControlSet\Control\Terminal Server\WinStations\RDP-Tcp'
  $rr=@(Get-NetFirewallRule -Group '@FirewallAPI.dll,-28752' -Direction Inbound | Where-Object { "$($_.Enabled)" -eq 'True' -and "$($_.Action)" -eq 'Allow' })
  $from=@($rr | Get-NetFirewallAddressFilter | ForEach-Object { $_.RemoteAddress } | Sort-Object -Unique)
  $acct=Get-LocalUser -Name 'swim-remote'
  $sess=@(quser 2>$null | Where-Object { $_ -match 'rdp-tcp#' }).Count
  $con=$null; foreach($l in @(quser 2>$null | Select-Object -Skip 1)){ $t=($l.Trim() -replace '^>','') -split '\s+'; if($t.Count -ge 4 -and $t[1] -eq 'console'){ $con=[ordered]@{user=$t[0]; state=$t[3]} } }
  $rdp=[ordered]@{enabled=($ts.fDenyTSConnections -eq 0); firewall_open=($rr.Count -gt 0); allowed_from=$from; nla=($tcp.UserAuthentication -eq 1)
    swim_account=$(if(-not $acct){'absent'}elseif($acct.Enabled){'enabled'}else{'disabled'}); sessions=$sess; console=$con; checked_at=(Get-Date).ToUniversalTime().ToString('o')}
} catch {}
$serial=$bios.SerialNumber; if(-not $serial -or $serial -match 'O\.E\.M|Default string'){ $serial='NOSERIAL' }
[ordered]@{
  schema=2; collected_at=(Get-Date).ToUniversalTime().ToString('o')
  smbios_uuid=$p.UUID; serial=$serial; vendor=$cs.Manufacturer; model=$cs.Model; hostname=$env:COMPUTERNAME; arch='amd64'
  firmware=($env:firmware_type).ToLower(); secure_boot=$sb; tpm_version=$tpm.version; tpm=$tpm
  os=[ordered]@{caption=$os.Caption; edition=$cv.EditionID; display_version=$cv.DisplayVersion; build=$cv.CurrentBuild; ubr=$cv.UBR; version=$os.Version
     install_date=(iso $os.InstallDate); last_boot=(iso $os.LastBootUpTime); arch=$os.OSArchitecture; language=$os.MUILanguages[0]}
  uptime_s=[int64]((Get-Date)-$os.LastBootUpTime).TotalSeconds
  user=$cs.UserName; domain=[ordered]@{name=$cs.Domain; joined=[bool]$cs.PartOfDomain}
  cpu=[ordered]@{name=($cpu.Name -replace '\s+',' ').Trim(); cores=$cpu.NumberOfCores; threads=$cpu.NumberOfLogicalProcessors}
  memory=[ordered]@{total=[int64]$cs.TotalPhysicalMemory; free=[int64]$os.FreePhysicalMemory*1024}
  bios=[ordered]@{vendor=$bios.Manufacturer; version=$bios.SMBIOSBIOSVersion; date=(iso $bios.ReleaseDate)}
  disks=$disks; physical_disks=$phys; network=$nics; bitlocker=$bl; defender=$def; firewall=$fw
  pending_reboot=[bool]$pend; timezone=(Get-TimeZone).Id; rdp=$rdp
  battery=$(if($bat){[ordered]@{charge=$bat.EstimatedChargeRemaining; status=$bat.BatteryStatus}}else{$null})
} | ConvertTo-Json -Depth 6 -Compress`

// CollectDetails returns the full inventory (JSON) or an error.
func CollectDetails(ctx context.Context) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	out, err := command(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-Command", detailsScript).Output()
	if err != nil {
		return nil, fmt.Errorf("powershell details: %w", err)
	}
	raw := json.RawMessage(strings.TrimSpace(string(out)))
	if !json.Valid(raw) {
		return nil, fmt.Errorf("details: invalid JSON")
	}
	// images kept on the SWIMSTAGE partition (null when the machine has none)
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) == nil {
		m["staging"], _ = json.Marshal(stagingInfo())
		m["prestage"], _ = json.Marshal(prestageInfo())
		st := policyState(ctx)
		m["policy"], _ = json.Marshal(map[string]any{"install_lock": st})
		if pl := pluginInventory(ctx); pl != nil {
			m["plugins"], _ = json.Marshal(pl)
		}
		if pkgLog != nil {
			go reconcilePolicy(context.Background(), st, pkgLog)
		}
		if b, err := json.Marshal(m); err == nil {
			raw = b
		}
	}
	return raw, nil
}
