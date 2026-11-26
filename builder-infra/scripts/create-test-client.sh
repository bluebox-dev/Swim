#!/usr/bin/env bash
# Lab only: create a Windows 11 test client that installs itself unattended and, at first
# logon, runs the same one-line agent install command the "Add machine" page produces.
#   ./create-test-client.sh swim-test01
# Needs: Windows ISO in /srv/swim/vms/iso/, genisoimage, the stack running (.env present).
set -euo pipefail
NAME=${1:-swim-test01}
HERE=$(cd "$(dirname "$0")/.." && pwd)
set -a; . "$HERE/.env"; set +a
DATA=${SWIM_DATA:-/srv/swim}
ISO=${ISO:-$DATA/vms/iso/Windows11.iso}
SERVER=${SERVER:-https://$(hostname -I | awk '{print $1}')}
EDITION=${EDITION:-Windows 11 Pro}
T1=$(echo "$SWIM_ADMIN_TOKENS" | cut -d, -f1 | cut -d: -f2)
API=(-sk -H "Authorization: Bearer $T1" -H "Content-Type: application/json")

if sudo virsh dominfo "$NAME" >/dev/null 2>&1; then echo "$NAME exists"; exit 1; fi

PIN=$(curl "${API[@]}" https://127.0.0.1/v1/enroll/info | jq -r .cert_sha256)
TOKEN=$(curl "${API[@]}" -d '{"ttl_hours":24,"max_uses":1}' https://127.0.0.1/v1/enrollment-tokens | jq -r .token)
[ -n "$PIN" ] && [ "$TOKEN" != null ] || { echo "cannot get pin/token"; exit 1; }

# Exactly the command the web page generates (frontend/app.js psCommand)
PS="\$SwimServer='$SERVER';\$SwimToken='$TOKEN';\$SwimPin='$PIN';[Net.ServicePointManager]::SecurityProtocol='Tls12';[Net.ServicePointManager]::ServerCertificateValidationCallback={param(\$a,\$c) ([BitConverter]::ToString([Security.Cryptography.SHA256]::Create().ComputeHash(\$c.GetRawCertData())) -replace '-','') -eq '$PIN'};iex (New-Object Net.WebClient).DownloadString(\$SwimServer+'/dl/agent/install.ps1')"

PASS="Sw!$(openssl rand -hex 8)"
CRED=$DATA/vms/$NAME.credentials
echo "user=swimtest password=$PASS" | sudo tee "$CRED" >/dev/null; sudo chmod 600 "$CRED"

WORK=$(mktemp -d)
cat > "$WORK/autounattend.xml" <<XML
<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
  <settings pass="windowsPE">
    <component name="Microsoft-Windows-International-Core-WinPE" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <SetupUILanguage><UILanguage>en-US</UILanguage></SetupUILanguage>
      <InputLocale>en-US</InputLocale><SystemLocale>en-US</SystemLocale><UILanguage>en-US</UILanguage><UserLocale>en-US</UserLocale>
    </component>
    <component name="Microsoft-Windows-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <DiskConfiguration>
        <Disk wcm:action="add">
          <DiskID>0</DiskID><WillWipeDisk>true</WillWipeDisk>
          <CreatePartitions>
            <CreatePartition wcm:action="add"><Order>1</Order><Type>EFI</Type><Size>260</Size></CreatePartition>
            <CreatePartition wcm:action="add"><Order>2</Order><Type>MSR</Type><Size>16</Size></CreatePartition>
            <CreatePartition wcm:action="add"><Order>3</Order><Type>Primary</Type><Extend>true</Extend></CreatePartition>
          </CreatePartitions>
          <ModifyPartitions>
            <ModifyPartition wcm:action="add"><Order>1</Order><PartitionID>1</PartitionID><Format>FAT32</Format><Label>System</Label></ModifyPartition>
            <ModifyPartition wcm:action="add"><Order>2</Order><PartitionID>3</PartitionID><Format>NTFS</Format><Label>Windows</Label><Letter>C</Letter></ModifyPartition>
          </ModifyPartitions>
        </Disk>
      </DiskConfiguration>
      <ImageInstall><OSImage>
        <InstallFrom><MetaData wcm:action="add"><Key>/IMAGE/NAME</Key><Value>$EDITION</Value></MetaData></InstallFrom>
        <InstallTo><DiskID>0</DiskID><PartitionID>3</PartitionID></InstallTo>
      </OSImage></ImageInstall>
      <UserData><AcceptEula>true</AcceptEula><ProductKey><Key>VK7JG-NPHTM-C97JM-9MPGT-3V66T</Key><WillShowUI>OnError</WillShowUI></ProductKey></UserData>
    </component>
  </settings>
  <settings pass="specialize">
    <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <ComputerName>${NAME^^}</ComputerName><TimeZone>SE Asia Standard Time</TimeZone>
    </component>
  </settings>
  <settings pass="oobeSystem">
    <component name="Microsoft-Windows-International-Core" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <InputLocale>0409:00000409</InputLocale><SystemLocale>en-US</SystemLocale><UILanguage>en-US</UILanguage><UserLocale>en-US</UserLocale>
    </component>
    <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <OOBE><HideEULAPage>true</HideEULAPage><HideOnlineAccountScreens>true</HideOnlineAccountScreens><HideWirelessSetupInOOBE>true</HideWirelessSetupInOOBE><ProtectYourPC>3</ProtectYourPC></OOBE>
      <UserAccounts><LocalAccounts><LocalAccount wcm:action="add">
        <Name>swimtest</Name><Group>Administrators</Group><Password><Value>$PASS</Value><PlainText>true</PlainText></Password>
      </LocalAccount></LocalAccounts></UserAccounts>
      <AutoLogon><Enabled>true</Enabled><LogonCount>1</LogonCount><Username>swimtest</Username><Password><Value>$PASS</Value><PlainText>true</PlainText></Password></AutoLogon>
      <FirstLogonCommands>
        <SynchronousCommand wcm:action="add"><Order>1</Order><Description>Install SWIM agent</Description>
          <CommandLine>cmd /c powershell -NoProfile -ExecutionPolicy Bypass -Command "$PS" &gt; C:\Windows\Temp\swim-install.log 2&gt;&amp;1</CommandLine>
        </SynchronousCommand>
      </FirstLogonCommands>
    </component>
  </settings>
</unattend>
XML
sudo genisoimage -quiet -J -r -V UNATTEND -o "$DATA/vms/iso/$NAME-unattend.iso" "$WORK/autounattend.xml"
rm -rf "$WORK"

sudo virt-install --connect qemu:///system --name "$NAME" --osinfo win11 \
  --memory 4096 --vcpus 2 --cpu host-passthrough --machine q35 \
  --boot uefi,firmware.feature0.name=secure-boot,firmware.feature0.enabled=yes,firmware.feature1.name=enrolled-keys,firmware.feature1.enabled=yes \
  --tpm backend.type=emulator,backend.version=2.0,model=tpm-crb \
  --disk path="$DATA/vms/$NAME.qcow2",size=64,format=qcow2,bus=sata,discard=unmap \
  --disk path="$ISO",device=cdrom,bus=sata \
  --disk path="$DATA/vms/iso/$NAME-unattend.iso",device=cdrom,bus=sata \
  --network network=default,model=e1000e --graphics vnc,listen=127.0.0.1 --video qxl \
  --noautoconsole --import >/dev/null
sudo virt-xml --connect qemu:///system "$NAME" --edit --boot cdrom,hd >/dev/null
sudo virsh destroy "$NAME" >/dev/null 2>&1 || true
sudo virsh start "$NAME" >/dev/null
for _ in $(seq 1 20); do sudo virsh send-key "$NAME" KEY_ENTER >/dev/null 2>&1; sleep 0.5; done
echo "$NAME installing (token max 1 use, 24 h). Credentials: $CRED"
