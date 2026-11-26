#!/usr/bin/env bash
# Lab only: make a test client in minutes from the Golden Master, without an ISO.
#   ./create-test-clone.sh <name> [golden-master-vm]
# - linked clone (qcow2 backing file): the Golden Master disk is never written
#   (do not boot the Golden Master while clones exist — it would corrupt the clones)
# - unattend.xml is placed offline in C:\Windows\Panther (computer name, local admin, OOBE skipped)
# - if the master is not generalized (gm-win11 never was), the clone's first boot runs
#   sysprep /generalize /oobe /reboot with that unattend (Setup CmdLine, same trick as the
#   builder's reseal), so every clone gets its own SID, name and admin — ~3 extra minutes
# - SWIM agent installed offline (exe + service + bootstrap.json with a one-use token)
# Credentials go to /srv/swim/vms/<name>.credentials (root only).
set -euo pipefail
NAME=${1:?name}; GM=${2:-gm-win11}
HERE=$(cd "$(dirname "$0")/.." && pwd)
set -a; . "$HERE/.env"; set +a
DATA=${SWIM_DATA:-/srv/swim}; SERVER=${SERVER:-https://$(hostname -I | awk '{print $1}')}
[ "$(sudo virsh domstate "$GM")" = "shut off" ] || { echo "$GM must be shut off"; exit 1; }
sudo virsh dominfo "$NAME" >/dev/null 2>&1 && { echo "$NAME exists"; exit 1; }
BASE=$(sudo virsh domblklist "$GM" --details | awk '$2=="disk"{print $4; exit}')
DISK=$DATA/vms/$NAME.qcow2
T1=$(echo "$SWIM_ADMIN_TOKENS" | cut -d, -f1 | cut -d: -f2); API=(-sk -H "Authorization: Bearer $T1" -H "Content-Type: application/json")
PIN=$(curl "${API[@]}" https://127.0.0.1/v1/enroll/info | jq -r .cert_sha256)
TOKEN=$(curl "${API[@]}" -d '{"ttl_hours":24,"max_uses":1}' https://127.0.0.1/v1/enrollment-tokens | jq -r .token)
PASS="Sw!$(openssl rand -hex 8)"
echo "user=swimtest password=$PASS" | sudo tee "$DATA/vms/$NAME.credentials" >/dev/null; sudo chmod 600 "$DATA/vms/$NAME.credentials"
_unused_PS="\$SwimServer='$SERVER';\$SwimToken='$TOKEN';\$SwimPin='$PIN';[Net.ServicePointManager]::SecurityProtocol='Tls12';[Net.ServicePointManager]::ServerCertificateValidationCallback={param(\$a,\$c) ([BitConverter]::ToString([Security.Cryptography.SHA256]::Create().ComputeHash(\$c.GetRawCertData())) -replace '-','') -eq '$PIN'};iex (New-Object Net.WebClient).DownloadString(\$SwimServer+'/dl/agent/install.ps1')"
COMP='processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS"'

sudo qemu-img create -q -f qcow2 -b "$BASE" -F qcow2 "$DISK"
WORK=$(mktemp -d)
cat > "$WORK/unattend.xml" <<XML
<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
  <settings pass="specialize"><component name="Microsoft-Windows-Shell-Setup" $COMP><ComputerName>${NAME^^}</ComputerName><TimeZone>SE Asia Standard Time</TimeZone></component>
    <component name="Microsoft-Windows-SecureStartup-FilterDriver" $COMP><PreventDeviceEncryption>true</PreventDeviceEncryption></component></settings>
  <settings pass="oobeSystem">
    <component name="Microsoft-Windows-International-Core" $COMP><InputLocale>0409:00000409</InputLocale><SystemLocale>en-US</SystemLocale><UILanguage>en-US</UILanguage><UserLocale>en-US</UserLocale></component>
    <component name="Microsoft-Windows-Shell-Setup" $COMP>
      <OOBE><HideEULAPage>true</HideEULAPage><HideOnlineAccountScreens>true</HideOnlineAccountScreens><HideWirelessSetupInOOBE>true</HideWirelessSetupInOOBE><HideOEMRegistrationScreen>true</HideOEMRegistrationScreen><ProtectYourPC>3</ProtectYourPC></OOBE>
      <UserAccounts><LocalAccounts><LocalAccount wcm:action="add"><Name>swimtest</Name><Group>Administrators</Group><Password><Value>$PASS</Value><PlainText>true</PlainText></Password></LocalAccount></LocalAccounts></UserAccounts>
      <AutoLogon><Enabled>true</Enabled><LogonCount>1</LogonCount><Username>swimtest</Username><Password><Value>$PASS</Value><PlainText>true</PlainText></Password></AutoLogon>
    </component>
  </settings>
</unattend>
XML
NBD=/dev/nbd7; sudo qemu-nbd -c $NBD "$DISK"; sleep 2
P=$(for p in ${NBD}p*; do [ "$(sudo blkid -o value -s TYPE $p || true)" = ntfs ] && echo "$(sudo blockdev --getsize64 $p) $p"; done | sort -n | tail -1 | cut -d' ' -f2)
sudo mkdir -p /mnt/clone; sudo mount -t ntfs-3g "$P" /mnt/clone
sudo mkdir -p /mnt/clone/Windows/Panther; sudo cp "$WORK/unattend.xml" /mnt/clone/Windows/Panther/unattend.xml
STATE=$(sudo cat /mnt/clone/Windows/Setup/State/State.ini 2>/dev/null | tr -d '\000\r' | sed -n 's/^ImageState=//p')
RESEAL=""
if [ "$STATE" != "IMAGE_STATE_GENERALIZE_RESEAL_TO_OOBE" ]; then
  echo "$GM is not generalized ($STATE): the clone runs sysprep on its first boot"
  sudo mkdir -p /mnt/clone/Windows/Setup/Scripts
  printf '%s\r\n' '@echo off' \
    'reg add HKLM\SYSTEM\Setup /v SetupType /t REG_DWORD /d 0 /f >nul' \
    'reg add HKLM\SYSTEM\Setup /v CmdLine /t REG_SZ /d "" /f >nul' \
    'C:\Windows\System32\Sysprep\sysprep.exe /generalize /oobe /reboot /quiet /unattend:C:\Windows\Panther\unattend.xml' \
    'del "%~f0"' | sudo tee /mnt/clone/Windows/Setup/Scripts/swim-clone-reseal.cmd >/dev/null
  RESEAL='
[HKEY_LOCAL_MACHINE\SYSTEM\Setup]
"SetupType"=dword:00000002
"CmdLine"="cmd.exe /c C:\\Windows\\Setup\\Scripts\\swim-clone-reseal.cmd"
'
fi
# agent installed offline (same as swim-pe does): exe + service + bootstrap with a one-use token
LATEST=$(jq -r .file "$DATA/storage/agent/latest.json")
sudo mkdir -p "/mnt/clone/Program Files/SWIM" /mnt/clone/ProgramData/SWIM
sudo cp "$DATA/storage/agent/$LATEST" "/mnt/clone/Program Files/SWIM/swim-agent.exe"
printf '{"server":"%s","cert_sha256":"%s","token":"%s"}' "$SERVER" "$PIN" "$TOKEN" | sudo tee /mnt/clone/ProgramData/SWIM/bootstrap.json >/dev/null
cat > "$WORK/svc.reg" <<'REG'
Windows Registry Editor Version 5.00

[HKEY_LOCAL_MACHINE\SYSTEM\ControlSet001\Services\SWIMAgent]
"Type"=dword:00000010
"Start"=dword:00000002
"ErrorControl"=dword:00000001
"ImagePath"=str(2):"\"C:\\Program Files\\SWIM\\swim-agent.exe\" run"
"ObjectName"=str(1):"LocalSystem"
"DisplayName"=str(1):"SWIM Agent"

[HKEY_LOCAL_MACHINE\SYSTEM\ControlSet001\Control\BitLocker]
"PreventDeviceEncryption"=dword:00000001
REG
printf '%s' "$RESEAL" >> "$WORK/svc.reg"
sudo hivexregedit --merge --prefix 'HKEY_LOCAL_MACHINE\SYSTEM' /mnt/clone/Windows/System32/config/SYSTEM "$WORK/svc.reg"
sudo umount /mnt/clone; sudo qemu-nbd -d $NBD >/dev/null; rm -rf "$WORK"

sudo virt-install --connect qemu:///system --name "$NAME" --osinfo win11 --memory 4096 --vcpus 2 --cpu host-passthrough --machine q35 \
  --boot uefi,firmware.feature0.name=secure-boot,firmware.feature0.enabled=yes,firmware.feature1.name=enrolled-keys,firmware.feature1.enabled=yes \
  --tpm backend.type=emulator,backend.version=2.0,model=tpm-crb \
  --disk path="$DISK",format=qcow2,bus=sata,discard=unmap --network network=default,model=e1000e \
  --graphics vnc,listen=127.0.0.1 --video qxl --import --noautoconsole >/dev/null
echo "$NAME started from $GM (linked clone). Credentials: $DATA/vms/$NAME.credentials"
