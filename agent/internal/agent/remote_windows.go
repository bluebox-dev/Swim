//go:build windows

package agent

// One-click Remote Desktop ("remote_session"). The server sends a random one-time
// password sealed to this machine's key; the agent puts it on a dedicated local
// administrator account (swim-remote, hidden from the sign-in screen), turns Remote
// Desktop on with the firewall open for the SWIM server only, and disables the account
// again when the time is up. A session that is already open keeps running; a new
// connection needs a new command. The password never appears on a command line, in the
// agent log or in the command result.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"swim/pkg/manifest"
)

const remoteAccount = "swim-remote"

var (
	remoteUntil  atomic.Int64 // unix time the account stays enabled until (latest command wins)
	remotePassRe = regexp.MustCompile(`^[A-Za-z0-9!#%+=_-]{16,64}$`)
)

func remoteSession(ctx context.Context, st *State, a manifest.RemoteSessionArgs, lg interface{ Printf(string, ...any) }) (string, any, error) {
	plain, err := openSealed(st, a.Sealed)
	if err != nil {
		return "", nil, err
	}
	var sec manifest.RemoteSecrets
	if json.Unmarshal(plain, &sec) != nil || sec.User != remoteAccount || !remotePassRe.MatchString(sec.Password) {
		return "", nil, errors.New("invalid remote session secrets")
	}
	from := a.AllowFrom
	if from != "LocalSubnet" && net.ParseIP(from) == nil {
		return "", nil, errors.New("invalid allow_from")
	}
	ttl := time.Duration(min(max(a.TTLS, 120), 3600)) * time.Second

	// remember whether Remote Desktop was off before, so it is closed again afterwards
	if _, err := os.Stat(rdpPrevPath()); err != nil {
		prev, _ := ps(ctx, 30*time.Second, `$d=(Get-ItemProperty 'HKLM:\System\CurrentControlSet\Control\Terminal Server').fDenyTSConnections
$f=@(Get-NetFirewallRule -Group '@FirewallAPI.dll,-28752' -Direction Inbound | Where-Object { "$($_.Enabled)" -eq 'True' }).Count
"$d|$f"`)
		_ = os.WriteFile(rdpPrevPath(), []byte(strings.TrimSpace(prev)), 0o600)
	}
	script := fmt.Sprintf(`
$u = '%[1]s'
$p = ConvertTo-SecureString $env:SWIM_RS_PW -AsPlainText -Force
if (Get-LocalUser -Name $u -ErrorAction SilentlyContinue) { Set-LocalUser -Name $u -Password $p }
else { $null = New-LocalUser -Name $u -Password $p -FullName 'SWIM Remote' -Description 'SWIM remote sessions (managed by the SWIM agent)' }
Set-LocalUser -Name $u -PasswordNeverExpires $true
Enable-LocalUser -Name $u
foreach ($sid in 'S-1-5-32-544','S-1-5-32-555') {
  try { Add-LocalGroupMember -SID $sid -Member $u -ErrorAction Stop } catch { if ("$($_.FullyQualifiedErrorId)" -notmatch 'MemberExists') { throw } }
}
$k = 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon\SpecialAccounts\UserList'
$null = New-Item $k -Force
Set-ItemProperty $k -Name $u -Value 0 -Type DWord
Set-ItemProperty 'HKLM:\System\CurrentControlSet\Control\Terminal Server' -Name fDenyTSConnections -Value 0
Set-ItemProperty 'HKLM:\System\CurrentControlSet\Control\Terminal Server\WinStations\RDP-Tcp' -Name UserAuthentication -Value 1
Get-NetFirewallRule -Group '@FirewallAPI.dll,-28752' | Set-NetFirewallRule -Enabled True -Profile Any -RemoteAddress '%[2]s'
Set-Service TermService -StartupType Manual; Start-Service TermService -ErrorAction SilentlyContinue
'ok'`, remoteAccount, from)
	c, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := command(c, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command",
		"$ErrorActionPreference='Stop'; "+script)
	cmd.Env = append(os.Environ(), "SWIM_RS_PW="+sec.Password)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", nil, fmt.Errorf("prepare remote session: %v: %s", err, strings.TrimSpace(string(out)))
	}
	until := time.Now().Add(ttl)
	remoteUntil.Store(until.Unix())
	go func() {
		time.Sleep(ttl + time.Second)
		if time.Now().Unix() >= remoteUntil.Load() {
			disableRemoteAccount(lg)
		}
	}()
	msg := fmt.Sprintf("Remote Desktop is ready: account %s enabled until %s, RDP allowed from %s only",
		remoteAccount, until.Format("15:04:05"), from)
	// the person at the machine: tell them, then disconnect (not sign out) their session,
	// so Windows does not ask them to allow the connection and their work stays open
	data := map[string]any{"console_user": "", "console_locked": false}
	if u, id, ok := consoleUser(ctx); ok && !strings.EqualFold(u, remoteAccount) {
		data["console_user"] = u
		if err := lockConsole(ctx, id); err != nil {
			msg += fmt.Sprintf("; could not lock the screen of %s: %v", u, err)
		} else {
			data["console_locked"] = true
			msg += fmt.Sprintf("; %s was told and their session disconnected (work kept open)", u)
		}
	}
	go watchConsole(lg)
	return msg, data, nil
}

// disableRemoteAccount closes the door again; also run at agent start, because a
// restart loses the timer.
func disableRemoteAccount(lg interface{ Printf(string, ...any) }) {
	signOutLeftoverRemote(lg)
	out, err := ps(context.Background(), time.Minute, fmt.Sprintf(
		`if (Get-LocalUser -Name '%[1]s' -ErrorAction SilentlyContinue | Where-Object Enabled) { Disable-LocalUser -Name '%[1]s'; 'disabled' }`, remoteAccount))
	if err != nil {
		lg.Printf("remote account: %v", err)
	} else if strings.Contains(out, "disabled") {
		lg.Printf("remote account %s disabled", remoteAccount)
		requestInventory()
	}
	// close Remote Desktop again if it was closed before the session (and nobody is connected)
	if b, err := os.ReadFile(rdpPrevPath()); err == nil {
		prev := strings.TrimSpace(string(b))
		busy := false
		for _, s := range sessions(context.Background()) {
			if strings.EqualFold(s.User, remoteAccount) && strings.EqualFold(s.State, "Active") {
				busy = true
			}
		}
		if busy { // the admin is still working: check again later
			go func() { time.Sleep(5 * time.Minute); disableRemoteAccount(lg) }()
			return
		}
		if strings.HasPrefix(prev, "1|") {
			_, err := ps(context.Background(), time.Minute, `Set-ItemProperty 'HKLM:\System\CurrentControlSet\Control\Terminal Server' -Name fDenyTSConnections -Value 1
Get-NetFirewallRule -Group '@FirewallAPI.dll,-28752' | Set-NetFirewallRule -Enabled False
'ok'`)
			if err != nil {
				lg.Printf("could not close Remote Desktop again: %v", err)
				return
			}
			lg.Printf("Remote Desktop closed again (it was off before the remote session)")
		}
		_ = os.Remove(rdpPrevPath())
		requestInventory()
	}
}

const takeoverNotice = "IT กำลังดูแลเครื่องนี้ผ่าน SWIM หน้าจอจะถูกล็อกชั่วคราว งานที่เปิดไว้ยังอยู่"

type winSession struct {
	User, Name, State string
	ID                int
}

// sessions parses quser: USERNAME SESSIONNAME ID STATE … (SESSIONNAME is empty when disconnected).
func sessions(ctx context.Context) []winSession {
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, _ := command(c, "quser.exe").CombinedOutput() // exit 1 when nobody is signed in
	var ss []winSession
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	first := true
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(sc.Text()), ">"))
		if len(f) < 3 {
			continue
		}
		w := winSession{User: f[0]}
		if id, err := strconv.Atoi(f[1]); err == nil { // no session name (disconnected)
			w.ID, w.State = id, f[2]
		} else if len(f) >= 4 {
			w.Name, w.State = f[1], f[3]
			w.ID, _ = strconv.Atoi(f[2])
		}
		ss = append(ss, w)
	}
	return ss
}

func consoleUser(ctx context.Context) (string, int, bool) {
	for _, s := range sessions(ctx) {
		if strings.EqualFold(s.Name, "console") && strings.EqualFold(s.State, "Active") {
			return s.User, s.ID, true
		}
	}
	return "", 0, false
}

func lockConsole(ctx context.Context, id int) error {
	sid := strconv.Itoa(id)
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_ = command(c, "msg.exe", sid, "/TIME:15", takeoverNotice).Run()
	time.Sleep(5 * time.Second)
	if out, err := command(c, "tsdiscon.exe", sid).CombinedOutput(); err != nil {
		return fmt.Errorf("tsdiscon: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

var watching atomic.Bool

// watchConsole looks after the machine for the length of a remote session.
//
// While the admin is CONNECTED (the swim-remote session is Active) someone signing in at
// the machine would throw the admin out, so that person gets the notice and their session
// is disconnected again (their work stays open). As soon as the admin leaves — the browser
// tab is closed, so the swim-remote session is merely "disconnected" — that session is
// signed out. A session left behind kept this watcher sending the person at the machine
// away for the rest of the 10-minute window, so they could not get back to their desktop.
func watchConsole(lg interface{ Printf(string, ...any) }) {
	if !watching.CompareAndSwap(false, true) {
		return
	}
	defer watching.Store(false)
	ctx := context.Background()
	idle := 0 // consecutive checks with the admin's session present but not connected
	for {
		time.Sleep(5 * time.Second)
		var admin *winSession
		for _, s := range sessions(ctx) {
			if strings.EqualFold(s.User, remoteAccount) {
				s := s
				admin = &s
			}
		}
		if admin == nil {
			idle = 0
			if time.Now().Unix() >= remoteUntil.Load() {
				return // window over and nobody is connected
			}
			continue
		}
		if !strings.EqualFold(admin.State, "Active") {
			// the admin left; give a reconnect (page reload) 15 seconds, then sign the session out
			if idle++; idle >= 3 {
				if err := logoffSession(ctx, admin.ID); err != nil {
					lg.Printf("remote session: could not sign out the left-over %s session: %v", remoteAccount, err)
				} else {
					lg.Printf("remote session: admin left; %s signed out, the machine is free again", remoteAccount)
					requestInventory()
				}
				idle = 0
			}
			continue
		}
		idle = 0
		if u, id, ok := consoleUser(ctx); ok && !strings.EqualFold(u, remoteAccount) {
			if err := lockConsole(ctx, id); err != nil {
				lg.Printf("remote takeover: could not disconnect %s: %v", u, err)
			} else {
				lg.Printf("remote takeover: %s signed in at the machine during a remote session; disconnected again", u)
			}
		}
	}
}

func logoffSession(ctx context.Context, id int) error {
	c, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if out, err := command(c, "logoff.exe", strconv.Itoa(id)).CombinedOutput(); err != nil {
		return fmt.Errorf("logoff: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// signOutLeftoverRemote signs out a swim-remote session nobody is connected to (agent start,
// end of the window): it would otherwise sit on the machine with the account already disabled.
func signOutLeftoverRemote(lg interface{ Printf(string, ...any) }) {
	ctx := context.Background()
	for _, s := range sessions(ctx) {
		if strings.EqualFold(s.User, remoteAccount) && !strings.EqualFold(s.State, "Active") {
			if err := logoffSession(ctx, s.ID); err == nil {
				lg.Printf("remote session: left-over %s session signed out", remoteAccount)
			}
		}
	}
}

func rdpPrevPath() string { return filepath.Join(DataDir(), "rdp-before-remote.txt") }
