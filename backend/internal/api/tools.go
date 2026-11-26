package api

// Ready-made machine tools (เครื่องมือ).
//
// The PowerShell of every tool is written here, on the server. A browser sends only the
// tool's id and its few parameters ({"tool":"shutdown","params":{"t":"300","msg":"…"}});
// the server checks each parameter against a strict rule, builds the script and signs it.
// The API accepts no script text at all, so a stolen sign-in cannot run code of its own
// choosing on the machines: only these tools, with parameters that pass the checks.
//
//	POST /v1/tools/preview {tool, params}                         → {name, body, timeout_s}
//	POST /v1/hosts/{id}/commands {type:"run_script", args:{tool, params}}
//	POST /v1/batches             {type:"run_script", args:{tool, params}, …}
//
// Saved library scripts ({"script_id": …}) still run, but new ones cannot be saved through
// the API any more.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// psq quotes a PowerShell single-quoted string. PowerShell also treats the typographic
// quotes ‘ ’ ‚ ‛ as string delimiters, so they are doubled as well — otherwise a value
// containing ’ would end the string and the rest would run as code.
func psq(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		switch r {
		case '\'', '\u2018', '\u2019', '\u201A', '\u201B':
			b.WriteRune(r)
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

const psUserSession = `$sid = (Get-CimInstance Win32_Process -Filter "Name='explorer.exe'" | Select-Object -First 1).SessionId
if ($null -eq $sid) { 'ไม่มีผู้ใช้ล็อกอินอยู่'; exit 0 }`

func psTable(cols string) string {
	return "| Format-Table " + cols + " -AutoSize | Out-String -Width 220"
}

const psUninstallKeys = `$k = 'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*','HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*'`

// toolParams reads and checks the parameters of one request. The first failed check is kept.
type toolParams struct {
	m      map[string]any
	err    string
	redact bool // build the copy that is stored and shown: secrets replaced by ********
}

func (p *toolParams) fail(format string, a ...any) {
	if p.err == "" {
		p.err = fmt.Sprintf(format, a...)
	}
}

func (p *toolParams) raw(k string) string {
	switch v := p.m[k].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		if v {
			return "on"
		}
	}
	return ""
}

// text: free text on one line (or several when multiline), max runes, no control characters.
func (p *toolParams) text(k, label string, max int, required, multiline bool) string {
	s := strings.TrimSpace(p.raw(k))
	if !utf8.ValidString(s) {
		p.fail("%s: ข้อความไม่ถูกต้อง", label)
		return ""
	}
	for _, r := range s {
		if (unicode.IsControl(r) && !(multiline && (r == '\n' || r == '\r'))) || r == '\u2028' || r == '\u2029' {
			p.fail("%s: มีอักขระที่ใช้ไม่ได้", label)
			return ""
		}
	}
	if required && s == "" {
		p.fail("กรอก%s", label)
	}
	if utf8.RuneCountInString(s) > max {
		p.fail("%s ยาวเกิน %d ตัวอักษร", label, max)
	}
	return s
}

func (p *toolParams) match(k, label string, re *regexp.Regexp) string {
	s := strings.TrimSpace(p.raw(k))
	if !re.MatchString(s) {
		p.fail("%s ไม่ถูกต้อง", label)
		return ""
	}
	return s
}

func (p *toolParams) oneOf(k, label string, allowed ...string) string {
	s := p.raw(k)
	for _, a := range allowed {
		if s == a {
			return s
		}
	}
	p.fail("%s ไม่ถูกต้อง", label)
	return allowed[0]
}

func (p *toolParams) flag(k string) bool {
	switch p.raw(k) {
	case "on", "1", "true":
		return true
	}
	return false
}

// secret: a password typed for the machine. The stored copy shows ******** instead.
func (p *toolParams) secret(k, label string) string {
	s := p.raw(k)
	if n := utf8.RuneCountInString(s); n < 8 || n > 128 || !utf8.ValidString(s) {
		p.fail("%s ต้องมี 8 ถึง 128 ตัวอักษร", label)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			p.fail("%s: มีอักขระที่ใช้ไม่ได้", label)
		}
	}
	if p.redact {
		return "********"
	}
	return s
}

var (
	reLocalUser = regexp.MustCompile(`^[A-Za-z0-9._-]{1,20}$`)
	reAccount   = regexp.MustCompile(`^[^"/\\\[\]:;|=,+*?<>@\x00-\x1f]{1,64}$`) // Windows account-name rule
	reHostName  = regexp.MustCompile(`^[A-Za-z0-9-]{1,15}$`)
	reService   = regexp.MustCompile(`^[A-Za-z0-9_.$ -]{1,256}$`)
	reTarget    = regexp.MustCompile(`^[A-Za-z0-9.:_-]{1,253}$`)
	reProcess   = regexp.MustCompile(`^[A-Za-z0-9._ +-]{1,128}$`)
	reDigits    = regexp.MustCompile(`^\d{1,9}$`)
)

type tool struct {
	Timeout int
	// build returns the name shown in the history and the PowerShell text
	build func(p *toolParams) (name, body string)
}

var tools = map[string]tool{
	// ── พลังงาน
	"shutdown": {build: func(p *toolParams) (string, string) {
		t, _ := strconv.Atoi(p.oneOf("t", "เวลา", "300", "60", "900", "0"))
		msg := p.text("msg", "ข้อความ", 200, false, false)
		return fmt.Sprintf("ปิดเครื่อง (%d นาที)", t/60), fmt.Sprintf("shutdown.exe /s /t %d /c %s\n'สั่งปิดเครื่องแล้ว'", t, psq(msg))
	}},
	"abort": {build: func(p *toolParams) (string, string) {
		return "ยกเลิกการปิด/รีบูต", "shutdown.exe /a\nif ($LASTEXITCODE -eq 0) { 'ยกเลิกแล้ว' } else { 'ไม่มีการปิด/รีบูตที่รออยู่' }"
	}},
	"lock": {build: func(p *toolParams) (string, string) {
		return "ล็อกหน้าจอ", psUserSession + "\ntsdiscon.exe $sid\n'ล็อกหน้าจอแล้ว (session ' + $sid + ')'"
	}},
	"logoff": {build: func(p *toolParams) (string, string) {
		return "ออกจากระบบผู้ใช้", psUserSession + "\nlogoff.exe $sid\n'ออกจากระบบแล้ว (session ' + $sid + ')'"
	}},
	// ── ผู้ใช้
	"msg": {build: func(p *toolParams) (string, string) {
		sec := p.oneOf("sec", "เวลาแสดง", "300", "60", "3600")
		text := p.text("text", "ข้อความ", 400, true, true)
		return "ส่งข้อความถึงผู้ใช้", fmt.Sprintf("msg.exe * /TIME:%s %s\n'ส่งข้อความแล้ว'", sec, psq(text))
	}},
	"users": {build: func(p *toolParams) (string, string) {
		return "ดูบัญชีผู้ใช้ในเครื่อง", "Get-LocalUser " + psTable("Name,Enabled,LastLogon,PasswordExpires,Description") +
			"\n'--- Administrators ---'\nGet-LocalGroupMember -Group Administrators " + psTable("Name,ObjectClass,PrincipalSource")
	}},
	"useradd": {build: func(p *toolParams) (string, string) {
		user := p.match("user", "ชื่อผู้ใช้", reLocalUser)
		full := p.text("full", "ชื่อเต็ม", 60, false, false)
		pass := p.secret("pass", "รหัสผ่านชั่วคราว")
		admin := p.flag("admin")
		u := psq(user)
		var b strings.Builder
		fmt.Fprintf(&b, "$p = ConvertTo-SecureString %s -AsPlainText -Force\n", psq(pass))
		fmt.Fprintf(&b, "New-LocalUser -Name %s -FullName %s -Password $p -PasswordNeverExpires:$false | Out-Null\n", u, psq(full))
		fmt.Fprintf(&b, "Add-LocalGroupMember -Group Users -Member %s -ErrorAction SilentlyContinue\n", u)
		tail := ""
		if admin {
			fmt.Fprintf(&b, "Add-LocalGroupMember -Group Administrators -Member %s\n", u)
			tail = " (Administrators)"
		}
		fmt.Fprintf(&b, "net.exe user %s /logonpasswordchg:yes | Out-Null\n", u)
		fmt.Fprintf(&b, "'สร้างบัญชี ' + %s + ' แล้ว%s — ต้องเปลี่ยนรหัสผ่านตอนล็อกอินครั้งแรก'", u, tail)
		return "สร้างบัญชี " + user, b.String()
	}},
	"userpw": {build: func(p *toolParams) (string, string) {
		user := p.match("user", "ชื่อผู้ใช้", reAccount)
		pass := p.secret("pass", "รหัสผ่านชั่วคราว")
		u := psq(user)
		return "ตั้งรหัสผ่านใหม่ให้ " + user, fmt.Sprintf("$p = ConvertTo-SecureString %s -AsPlainText -Force\nSet-LocalUser -Name %s -Password $p\nnet.exe user %s /logonpasswordchg:yes | Out-Null\n'ตั้งรหัสผ่านใหม่ให้ ' + %s + ' แล้ว'",
			psq(pass), u, u, u)
	}},
	"userstate": {build: func(p *toolParams) (string, string) {
		user := p.match("user", "ชื่อผู้ใช้", reAccount)
		verb, th := "Disable", "ปิด"
		if p.oneOf("on", "การกระทำ", "0", "1") == "1" {
			verb, th = "Enable", "เปิด"
		}
		u := psq(user)
		return th + "บัญชี " + user, fmt.Sprintf("%s-LocalUser -Name %s\nGet-LocalUser -Name %s %s", verb, u, u, psTable("Name,Enabled"))
	}},
	// ── โปรแกรม
	"apps": {build: func(p *toolParams) (string, string) {
		return "ดูโปรแกรมที่ติดตั้ง", psUninstallKeys + "\nGet-ItemProperty $k -ErrorAction SilentlyContinue | Where-Object DisplayName | Sort-Object DisplayName -Unique " +
			psTable("DisplayName,DisplayVersion,Publisher")
	}},
	"uninstall": {Timeout: 1800, build: func(p *toolParams) (string, string) {
		app := p.text("app", "ชื่อโปรแกรม", 200, true, false)
		a := psq(app)
		return "ถอนโปรแกรม " + app, psUninstallKeys + `
$apps = @(Get-ItemProperty $k -ErrorAction SilentlyContinue | Where-Object { $_.DisplayName -like (` + a + ` + '*') })
if ($apps.Count -eq 0) { 'ไม่พบโปรแกรม ' + ` + a + `; exit 1 }
if ($apps.Count -gt 1) { 'พบหลายโปรแกรม ระบุชื่อให้ชัดกว่านี้:'; $apps.DisplayName; exit 1 }
$a = $apps[0]; 'ถอน: ' + $a.DisplayName + ' ' + $a.DisplayVersion
if ($a.PSChildName -match '^\{[0-9A-Fa-f-]+\}$' -and $a.UninstallString -match 'msiexec') {
  $p = Start-Process msiexec.exe -ArgumentList '/x', $a.PSChildName, '/qn', '/norestart' -Wait -PassThru; 'msiexec exit ' + $p.ExitCode; exit $p.ExitCode
} elseif ($a.QuietUninstallString) {
  $p = Start-Process cmd.exe -ArgumentList '/c', $a.QuietUninstallString -Wait -PassThru; 'exit ' + $p.ExitCode; exit $p.ExitCode
} else { 'โปรแกรมนี้ไม่มีวิธีถอนแบบเงียบ — ถอนผ่าน Remote Desktop แทน (' + $a.UninstallString + ')'; exit 1 }`
	}},
	"procs": {build: func(p *toolParams) (string, string) {
		return "ดูโปรเซสที่ทำงาน", "Get-Process -IncludeUserName | Sort-Object WS -Descending | Select-Object -First 80 Name,Id,UserName,@{n='MB';e={[int]($_.WS/1MB)}},@{n='CPU(s)';e={[int]$_.CPU}} " + psTable("*")
	}},
	"kill": {build: func(p *toolParams) (string, string) {
		v := strings.TrimSpace(p.raw("p"))
		if reDigits.MatchString(v) {
			n, _ := strconv.Atoi(v)
			return "ปิดโปรเซส " + v, fmt.Sprintf("Stop-Process -Id %d -Force\n'ปิดแล้ว'", n)
		}
		v = p.match("p", "ชื่อโปรเซสหรือ PID", reProcess)
		name := v
		if len(name) > 4 && strings.EqualFold(name[len(name)-4:], ".exe") {
			name = name[:len(name)-4]
		}
		return "ปิดโปรเซส " + v, "$p = @(Get-Process -Name " + psq(name) + " -ErrorAction SilentlyContinue)\nif (!$p) { 'ไม่พบโปรเซส'; exit 1 }\n$p | Stop-Process -Force\n'ปิดแล้ว ' + $p.Count + ' โปรเซส'"
	}},
	// ── ระบบ
	"services": {build: func(p *toolParams) (string, string) {
		return "ดู Service", "Get-Service | Sort-Object Status,Name " + psTable("Status,StartType,Name,DisplayName")
	}},
	"service": {build: func(p *toolParams) (string, string) {
		svc := p.match("svc", "ชื่อ Service", reService)
		act := p.oneOf("act", "การกระทำ", "Restart", "Start", "Stop")
		st := p.oneOf("st", "การเริ่มอัตโนมัติ", "", "Automatic", "Manual", "Disabled")
		q := psq(svc)
		body := ""
		if st != "" {
			body = "Set-Service -Name " + q + " -StartupType " + st + "\n"
		}
		body += act + "-Service -Name " + q + " -Force\nGet-Service -Name " + q + " " + psTable("Status,StartType,Name,DisplayName")
		return map[string]string{"Restart": "รีสตาร์ต", "Start": "เริ่ม", "Stop": "หยุด"}[act] + " service " + svc, body
	}},
	"wu-check": {Timeout: 1200, build: func(p *toolParams) (string, string) {
		return "ตรวจ Windows Update", `$s = (New-Object -ComObject Microsoft.Update.Session).CreateUpdateSearcher()
$r = $s.Search("IsInstalled=0 and IsHidden=0 and Type='Software'")
if ($r.Updates.Count -eq 0) { 'ไม่มีอัปเดตที่ต้องติดตั้ง'; exit 0 }
'มีอัปเดต ' + $r.Updates.Count + ' รายการ:'
foreach ($u in $r.Updates) { ' - ' + $u.Title }`
	}},
	"wu-install": {Timeout: 3600, build: func(p *toolParams) (string, string) {
		return "ติดตั้ง Windows Update", `$ss = New-Object -ComObject Microsoft.Update.Session
$r = $ss.CreateUpdateSearcher().Search("IsInstalled=0 and IsHidden=0 and Type='Software'")
if ($r.Updates.Count -eq 0) { 'ไม่มีอัปเดตที่ต้องติดตั้ง'; exit 0 }
$c = New-Object -ComObject Microsoft.Update.UpdateColl
foreach ($u in $r.Updates) { if (-not $u.EulaAccepted) { $u.AcceptEula() }; [void]$c.Add($u); ' - ' + $u.Title }
$d = $ss.CreateUpdateDownloader(); $d.Updates = $c; [void]$d.Download()
$i = $ss.CreateUpdateInstaller(); $i.Updates = $c; $res = $i.Install()
'ผลการติดตั้ง: ' + @('ยังไม่เริ่ม','กำลังทำ','สำเร็จ','สำเร็จบางส่วน','ล้มเหลว','ยกเลิก')[$res.ResultCode]
if ($res.RebootRequired) { 'ต้องรีบูตเครื่องเพื่อให้อัปเดตมีผล' }`
	}},
	"rename": {build: func(p *toolParams) (string, string) {
		raw := p.match("nn", "ชื่อเครื่องใหม่", reHostName)
		nn := strings.ToUpper(raw)
		body := "Rename-Computer -NewName " + psq(nn) + " -Force -ErrorAction Stop\n'เปลี่ยนชื่อเป็น " + nn + " แล้ว — มีผลหลังรีบูต'"
		if p.flag("rb") {
			body += "\nshutdown.exe /r /t 60 /c 'IT เปลี่ยนชื่อเครื่อง เครื่องจะรีสตาร์ตใน 1 นาที'"
		}
		return "เปลี่ยนชื่อเครื่องเป็น " + raw, body
	}},
	"time": {build: func(p *toolParams) (string, string) {
		tz := p.oneOf("tz", "โซนเวลา", "SE Asia Standard Time", "", "Singapore Standard Time", "Tokyo Standard Time", "UTC")
		name, body := "ตั้งเวลา", ""
		if tz != "" {
			name += " · " + tz
			body += "Set-TimeZone -Id " + psq(tz) + "\n"
		}
		if p.flag("sync") {
			body += "Start-Service w32time -ErrorAction SilentlyContinue\nw32tm.exe /resync /force\n"
		}
		return name, body + "'โซนเวลา: ' + (Get-TimeZone).DisplayName\n'เวลาเครื่อง: ' + (Get-Date).ToString('yyyy-MM-dd HH:mm:ss')"
	}},
	"rdp-off": {build: func(p *toolParams) (string, string) {
		return "ปิด Remote Desktop", `Set-ItemProperty 'HKLM:\System\CurrentControlSet\Control\Terminal Server' -Name fDenyTSConnections -Value 1
Get-NetFirewallRule -Group '@FirewallAPI.dll,-28752' | Set-NetFirewallRule -Enabled False
$deny = (Get-ItemProperty 'HKLM:\System\CurrentControlSet\Control\Terminal Server').fDenyTSConnections
$open = @(Get-NetFirewallRule -Group '@FirewallAPI.dll,-28752' -Direction Inbound | Where-Object Enabled -eq 'True').Count
$acct = Get-LocalUser -Name 'swim-remote' -ErrorAction SilentlyContinue
if ($acct -and $acct.Enabled) { Disable-LocalUser -Name 'swim-remote' }
'Remote Desktop: ' + $(if ($deny -eq 1) { 'ปิดแล้ว' } else { 'ยังเปิดอยู่' })
'กฎ firewall ที่ยังเปิด: ' + $open
'บัญชี swim-remote: ' + $(if (-not $acct) { 'ไม่มี' } else { 'ปิดใช้งานแล้ว' })
if ($deny -ne 1 -or $open -gt 0) { exit 1 }`
	}},
	// read-only: can this machine boot in UEFI mode (needed by SWIM WinPE and by Windows 11)?
	"uefi-check": {build: func(p *toolParams) (string, string) {
		return "ตรวจความพร้อม UEFI และ Windows 11", `$loader = (bcdedit.exe /enum '{current}' | Select-String 'winload') -join ' '
'Firmware ที่ Windows บูตอยู่: ' + $(if ($loader -match 'winload\.efi') { 'UEFI' } elseif ($loader -match 'winload\.exe') { 'Legacy BIOS' } else { 'ไม่ทราบ' })
$d = Get-Disk | Where-Object IsBoot | Select-Object -First 1
'ดิสก์ระบบ: disk ' + $d.Number + ', ' + $d.PartitionStyle + ', ' + [math]::Round($d.Size/1GB) + ' GB, ' + @(Get-Partition -DiskNumber $d.Number).Count + ' partition'
Get-Partition -DiskNumber $d.Number | Select-Object PartitionNumber,DriveLetter,@{n='GB';e={[math]::Round($_.Size/1GB,1)}},Type,IsActive ` + psTable("*") + `
'--- แปลงดิสก์เป็น GPT ได้ไหม (mbr2gpt /validate ไม่แก้อะไรในเครื่อง) ---'
if ($d.PartitionStyle -eq 'MBR') {
  & "$env:SystemRoot\System32\mbr2gpt.exe" /validate /disk:$($d.Number) /allowFullOS 2>&1 | Out-String
  'ผล: ' + $(if ($LASTEXITCODE -eq 0) { 'แปลงเป็น GPT ได้' } else { 'แปลงไม่ได้ (exit ' + $LASTEXITCODE + ')' })
} else { 'ดิสก์เป็น GPT อยู่แล้ว' }
'--- ข้อกำหนดของ Windows 11 ---'
'CPU: ' + (Get-CimInstance Win32_Processor | Select-Object -First 1).Name
'RAM: ' + [math]::Round((Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory/1GB,1) + ' GB'
$tpm = Get-CimInstance -Namespace root\cimv2\Security\MicrosoftTpm -ClassName Win32_Tpm -ErrorAction SilentlyContinue
'TPM: ' + $(if ($tpm) { ($tpm.SpecVersion -split ',')[0] } else { 'ไม่มี' })
'Secure Boot: ' + $(try { if (Confirm-SecureBootUEFI) { 'เปิด' } else { 'ปิด' } } catch { 'ใช้ไม่ได้ในโหมด Legacy BIOS' })`
	}},
	// ── เครือข่าย
	"ipconfig": {build: func(p *toolParams) (string, string) { return "ipconfig /all", "ipconfig.exe /all" }},
	"ping": {build: func(p *toolParams) (string, string) {
		to := p.match("to", "ปลายทาง", reTarget)
		body := "Test-NetConnection -ComputerName " + psq(to)
		if port := strings.TrimSpace(p.raw("port")); port != "" {
			n, err := strconv.Atoi(port)
			if err != nil || n < 1 || n > 65535 {
				p.fail("Port ต้องอยู่ระหว่าง 1 ถึง 65535")
			}
			body += fmt.Sprintf(" -Port %d", n)
		}
		return "ทดสอบการเชื่อมต่อไป " + to, body + " -InformationLevel Detailed | Out-String -Width 200"
	}},
	"dns": {build: func(p *toolParams) (string, string) { return "ล้าง DNS cache", "ipconfig.exe /flushdns" }},
	// ── ดิสก์
	"disk": {build: func(p *toolParams) (string, string) {
		return "ดูพื้นที่ดิสก์", "Get-Volume | Where-Object DriveLetter | Select-Object DriveLetter,FileSystemLabel,FileSystem,@{n='Size(GB)';e={[math]::Round($_.Size/1GB,1)}},@{n='Free(GB)';e={[math]::Round($_.SizeRemaining/1GB,1)}} " + psTable("*")
	}},
	"temp": {Timeout: 1800, build: func(p *toolParams) (string, string) {
		return "ล้างไฟล์ชั่วคราว", `$before = (Get-PSDrive C).Free
$dirs = @("$env:SystemRoot\Temp") + @(Get-ChildItem 'C:\Users' -Directory -ErrorAction SilentlyContinue | ForEach-Object { Join-Path $_.FullName 'AppData\Local\Temp' })
foreach ($d in $dirs) { if (Test-Path $d) { Get-ChildItem $d -Force -ErrorAction SilentlyContinue | Remove-Item -Recurse -Force -ErrorAction SilentlyContinue } }
'คืนพื้นที่ได้ ' + [math]::Round(((Get-PSDrive C).Free - $before)/1MB) + ' MB'`
	}},
	"hiber": {build: func(p *toolParams) (string, string) {
		on := p.oneOf("on", "การกระทำ", "off", "on")
		th := "ปิด"
		if on == "on" {
			th = "เปิด"
		}
		return "Hibernate " + on, "powercfg.exe /h " + on + "\n'Hibernate: " + th + "แล้ว'"
	}},
}

// toolScript is one built tool request: what is signed and sent, and what is stored and shown.
type toolScript struct {
	ID      string
	Name    string
	Body    string // sent to the machine
	Shown   string // stored in the history: passwords replaced by ********
	Timeout int
	Params  map[string]any // for the activity log (passwords replaced)
}

func buildTool(id string, params map[string]any) (*toolScript, string) {
	t, ok := tools[id]
	if !ok {
		return nil, "ไม่รู้จักเครื่องมือนี้"
	}
	if params == nil {
		params = map[string]any{}
	}
	real := &toolParams{m: params}
	name, body := t.build(real)
	if real.err != "" {
		return nil, real.err
	}
	shown := &toolParams{m: params, redact: true}
	_, shownBody := t.build(shown)
	logged := map[string]any{}
	for k, v := range params {
		if k == "pass" {
			v = "********"
		}
		if s, ok := v.(string); ok && len(s) > 400 {
			v = s[:400]
		}
		logged[k] = v
	}
	timeout := t.Timeout
	if timeout == 0 {
		timeout = 600
	}
	return &toolScript{ID: id, Name: name, Body: body, Shown: shownBody, Timeout: timeout, Params: logged}, ""
}

// scriptRequest checks what a browser may ask for with run_script: a tool with parameters,
// or a saved script by id. Script text from outside is refused. For a tool it returns the
// arguments planCommand signs.
func scriptRequest(raw json.RawMessage) (json.RawMessage, *toolScript, *cmdErr) {
	var a struct {
		ScriptID string          `json:"script_id"`
		Tool     string          `json:"tool"`
		Params   map[string]any  `json:"params"`
		Body     json.RawMessage `json:"body"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, nil, &cmdErr{http.StatusBadRequest, "run_script: invalid arguments"}
		}
	}
	if len(a.Body) > 0 && string(a.Body) != `""` && string(a.Body) != "null" {
		return nil, nil, &cmdErr{http.StatusForbidden, "สคริปต์ที่พิมพ์เองถูกปิดไว้เพื่อความปลอดภัย ใช้เครื่องมือสำเร็จรูปหรือสคริปต์ที่บันทึกไว้บน server"}
	}
	if a.Tool != "" {
		ts, msg := buildTool(a.Tool, a.Params)
		if ts == nil {
			return nil, nil, &cmdErr{http.StatusBadRequest, msg}
		}
		out, _ := json.Marshal(map[string]any{"name": ts.Name, "body": ts.Body, "timeout_s": ts.Timeout})
		return out, ts, nil
	}
	if a.ScriptID == "" {
		return nil, nil, &cmdErr{http.StatusBadRequest, "เลือกเครื่องมือหรือสคริปต์ที่บันทึกไว้"}
	}
	out, _ := json.Marshal(map[string]string{"script_id": a.ScriptID})
	return out, nil, nil
}

// applyTool swaps what is stored and logged for a tool command: the tool's id and checked
// parameters, and the script text with passwords hidden. The signed envelope keeps the real text.
func (p *cmdPlan) applyTool(ts *toolScript) {
	if ts == nil {
		return
	}
	sum := sha256.Sum256([]byte(ts.Body))
	sha := hex.EncodeToString(sum[:])
	p.ArgsJSON, _ = json.Marshal(map[string]any{"script_id": "", "tool": ts.ID, "name": ts.Name, "sha256": sha,
		"body": ts.Shown, "timeout_s": ts.Timeout})
	p.AuditArgs = map[string]any{"tool": ts.ID, "params": ts.Params, "name": ts.Name, "sha256": sha, "timeout_s": ts.Timeout}
}

// toolPreview shows the script a tool would run, before anything is sent.
func (s *Server) toolPreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Tool   string         `json:"tool"`
		Params map[string]any `json:"params"`
	}
	if !decode(w, r, &in) {
		return
	}
	ts, msg := buildTool(in.Tool, in.Params)
	if ts == nil {
		writeErr(w, http.StatusBadRequest, "SWIM-API-400", msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tool": ts.ID, "name": ts.Name, "body": ts.Shown, "timeout_s": ts.Timeout})
}
