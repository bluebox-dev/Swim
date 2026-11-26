//go:build windows

package agent

// Install lock ("ล็อกการติดตั้งโปรแกรม"): only SWIM, which runs as SYSTEM, can install
// software. Applied by the signed set_policy command and re-applied by the agent itself
// if Windows drifts back (an admin re-added, a policy value removed).
//
//   - interactive accounts leave the local Administrators group, except the built-in
//     Administrator (RID 500), swim-remote, groups, and the accounts named in "keep"
//     (the admin account SWIM created at reimage). Who was removed is saved, so turning
//     the lock off puts exactly them back;
//   - UAC auto-denies elevation for standard users (ConsentPromptBehaviorUser = 0);
//   - Windows Installer: no per-user installs, no AlwaysInstallElevated;
//   - the Microsoft Store is turned off.
//
// Portable programs that need no installation still run — that is the agreed level.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"swim/pkg/manifest"
)

type policyFile struct {
	InstallLock bool        `json:"install_lock"`
	Keep        []string    `json:"keep"`
	Removed     []adminInfo `json:"removed"` // demoted by the lock, restored when it is turned off
	AppliedAt   time.Time   `json:"applied_at"`
	Error       string      `json:"error,omitempty"`
}

type adminInfo struct {
	Name  string `json:"name"`
	SID   string `json:"sid"`
	Class string `json:"class,omitempty"`
	// RemovedAt: when the lock took this account out of Administrators. A session that began
	// before it still carries the administrator token until that user signs out.
	RemovedAt time.Time `json:"removed_at,omitempty"`
}

var policyMu sync.Mutex

func policyPath() string { return filepath.Join(DataDir(), "policy.json") }

func loadPolicy() policyFile {
	var p policyFile
	if b, err := os.ReadFile(policyPath()); err == nil {
		_ = json.Unmarshal(b, &p)
	}
	return p
}

func savePolicy(p policyFile) {
	b, _ := json.MarshalIndent(p, "", "  ")
	_ = os.WriteFile(policyPath(), b, 0o600)
}

// adminsScript lists the members of the local Administrators group (by SID, so it works
// on any Windows language) as JSON: [{name, sid, class}].
const membersScript = `
$gn = (New-Object Security.Principal.SecurityIdentifier '%s').Translate([Security.Principal.NTAccount]).Value.Split('\')[-1]
$g = [ADSI]("WinNT://$env:COMPUTERNAME/$gn,group")
$list = @(foreach ($m in @($g.Invoke('Members'))) {
  $t = $m.GetType()
  $path = $t.InvokeMember('ADsPath','GetProperty',$null,$m,$null)
  $cls = $t.InvokeMember('Class','GetProperty',$null,$m,$null)
  $sid = ''; try { $sid = (New-Object Security.Principal.SecurityIdentifier ($t.InvokeMember('objectSid','GetProperty',$null,$m,$null)), 0).Value } catch {}
  [pscustomobject]@{ name = ($path -split '/')[-1]; sid = $sid; class = $cls }
})
`

func keepAdmin(a adminInfo, keep []string) bool {
	if !strings.EqualFold(a.Class, "user") || strings.HasSuffix(a.SID, "-500") || strings.EqualFold(a.Name, remoteAccount) {
		return true
	}
	for _, k := range keep {
		if k != "" && strings.EqualFold(a.Name, k) {
			return true
		}
	}
	return false
}

const (
	adminsSID = "S-1-5-32-544" // BUILTIN\Administrators
	usersSID  = "S-1-5-32-545" // BUILTIN\Users
)

func listAdmins(ctx context.Context) ([]adminInfo, error) { return listMembers(ctx, adminsSID) }

func listMembers(ctx context.Context, group string) ([]adminInfo, error) {
	out, err := ps(ctx, time.Minute, fmt.Sprintf(membersScript, group)+`if ($list.Count -eq 0) { '[]' } else { ConvertTo-Json -InputObject $list -Compress }`)
	if err != nil {
		return nil, err
	}
	var l []adminInfo
	i := strings.IndexAny(out, "[{")
	if i < 0 {
		return nil, fmt.Errorf("admins: unexpected output: %s", out)
	}
	s := strings.TrimSpace(out[i:])
	if strings.HasPrefix(s, "{") {
		s = "[" + s + "]"
	}
	return l, json.Unmarshal([]byte(s), &l)
}

const lockRegistry = `
$sys='HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System'
Set-ItemProperty $sys -Name ConsentPromptBehaviorUser -Value 0 -Type DWord
$mi='HKLM:\SOFTWARE\Policies\Microsoft\Windows\Installer'; $null = New-Item $mi -Force
Set-ItemProperty $mi -Name DisableUserInstalls -Value 1 -Type DWord
Set-ItemProperty $mi -Name AlwaysInstallElevated -Value 0 -Type DWord
$st='HKLM:\SOFTWARE\Policies\Microsoft\WindowsStore'; $null = New-Item $st -Force
Set-ItemProperty $st -Name RemoveWindowsStore -Value 1 -Type DWord
'ok'`

const unlockRegistry = `
$sys='HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System'
Set-ItemProperty $sys -Name ConsentPromptBehaviorUser -Value 3 -Type DWord
Remove-ItemProperty 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\Installer' -Name DisableUserInstalls -ErrorAction SilentlyContinue
Remove-ItemProperty 'HKLM:\SOFTWARE\Policies\Microsoft\WindowsStore' -Name RemoveWindowsStore -ErrorAction SilentlyContinue
'ok'`

func groupMemberOp(ctx context.Context, op, sid string) error {
	return groupOp(ctx, adminsSID, op, sid)
}

// ensureUser puts a demoted account in the Users group. An account made at Windows setup is
// often a member of Administrators only: taken out of it, it is in no group at all, Windows
// hides it from the sign-in screen and refuses its sign-in.
func ensureUser(ctx context.Context, sid string) error {
	users, err := listMembers(ctx, usersSID)
	if err != nil {
		return err
	}
	for _, u := range users {
		if u.SID == sid {
			return nil
		}
	}
	return groupOp(ctx, usersSID, "Add", sid)
}

func groupOp(ctx context.Context, group, op, sid string) error {
	if !strings.HasPrefix(sid, "S-1-") || strings.ContainsAny(sid, "'\"`$ ") {
		return fmt.Errorf("invalid SID %q", sid)
	}
	_, err := ps(ctx, time.Minute, fmt.Sprintf(`$gn = (New-Object Security.Principal.SecurityIdentifier '%s').Translate([Security.Principal.NTAccount]).Value.Split('\')[-1]
$g = [ADSI]("WinNT://$env:COMPUTERNAME/$gn,group"); $g.%s('WinNT://%s'); 'ok'`, group, op, sid))
	return err
}

// applyPolicy brings the machine to the desired install-lock state. Idempotent.
func applyPolicy(ctx context.Context, lock bool, keep []string) (string, map[string]any, error) {
	policyMu.Lock()
	defer policyMu.Unlock()
	p := loadPolicy()
	for i := range p.Removed { // entries written before RemovedAt existed
		if p.Removed[i].RemovedAt.IsZero() {
			p.Removed[i].RemovedAt = p.AppliedAt
		}
	}
	var log []string
	if lock {
		admins, err := listAdmins(ctx)
		if err != nil {
			return "", nil, err
		}
		have := map[string]bool{}
		for _, r := range p.Removed {
			have[r.SID] = true
		}
		for _, a := range admins {
			if keepAdmin(a, keep) {
				continue
			}
			if a.SID == "" {
				log = append(log, "skipped "+a.Name+" (no SID)")
				continue
			}
			if err := ensureUser(ctx, a.SID); err != nil { // first, so the account is never in no group
				return "", nil, fmt.Errorf("add %s to Users: %w", a.Name, err)
			}
			if err := groupMemberOp(ctx, "Remove", a.SID); err != nil {
				return "", nil, fmt.Errorf("remove %s from Administrators: %w", a.Name, err)
			}
			a.RemovedAt = time.Now().UTC()
			if !have[a.SID] {
				p.Removed = append(p.Removed, a)
				have[a.SID] = true
			} else { // demoted before, made an administrator again, demoted again now
				for i := range p.Removed {
					if p.Removed[i].SID == a.SID {
						p.Removed[i].RemovedAt = a.RemovedAt
					}
				}
			}
			log = append(log, "removed "+a.Name+" from Administrators")
			// save after each change: a crash must not lose who to restore
			p.InstallLock, p.Keep = true, keep
			savePolicy(p)
		}
		for _, r := range p.Removed { // accounts demoted by an agent older than 0.5.29
			if err := ensureUser(ctx, r.SID); err != nil {
				log = append(log, "could not add "+r.Name+" to Users: "+err.Error())
			} else {
				log = append(log, r.Name+" is in Users (can sign in as a standard account)")
			}
		}
		if _, err := ps(ctx, time.Minute, lockRegistry); err != nil {
			return "", nil, err
		}
		log = append(log, "UAC denies elevation for standard users; per-user installs and the Store are off")
	} else {
		var left []adminInfo
		for _, r := range p.Removed {
			if err := groupMemberOp(ctx, "Add", r.SID); err != nil && !strings.Contains(err.Error(), "already a member") {
				left = append(left, r)
				log = append(log, "could not restore "+r.Name+": "+err.Error())
				continue
			}
			log = append(log, "restored "+r.Name+" to Administrators")
		}
		p.Removed = left
		if _, err := ps(ctx, time.Minute, unlockRegistry); err != nil {
			return "", nil, err
		}
		log = append(log, "UAC, Windows Installer and Store settings back to Windows defaults")
	}
	p.InstallLock, p.Keep, p.AppliedAt, p.Error = lock, keep, time.Now().UTC(), ""
	savePolicy(p)
	st := policyState(ctx)
	if lock && !st.Applied {
		return strings.Join(log, "\n"), map[string]any{"install_lock": st}, fmt.Errorf("install lock not fully applied: %s", st.Problem)
	}
	return strings.Join(log, "\n"), map[string]any{"install_lock": st}, nil
}

type lockState struct {
	Desired bool     `json:"desired"`
	Applied bool     `json:"applied"` // the machine is in the desired state
	Locked  bool     `json:"locked"`  // all lock settings are in place right now
	Problem string   `json:"problem,omitempty"`
	Admins  []string `json:"admins"`
	Removed []string `json:"removed"`
	// PendingSignOut: demoted accounts still signed in from before the lock. Windows fixes a
	// user's rights at sign-in, so they keep administrator rights until they sign out.
	PendingSignOut []string    `json:"pending_sign_out"`
	CheckedAt      time.Time   `json:"checked_at"`
	Error          string      `json:"error,omitempty"`
	removed        []adminInfo `json:"-"`
}

func policyState(ctx context.Context) lockState {
	p := loadPolicy()
	st := lockState{Desired: p.InstallLock, CheckedAt: time.Now().UTC(), Admins: []string{}, Removed: []string{}, PendingSignOut: []string{}, Error: p.Error}
	for _, r := range p.Removed {
		st.Removed = append(st.Removed, r.Name)
	}
	out, err := ps(ctx, time.Minute, `
$c=(Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System').ConsentPromptBehaviorUser
$u=(Get-ItemProperty 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\Installer' -ErrorAction SilentlyContinue).DisableUserInstalls
$s=(Get-ItemProperty 'HKLM:\SOFTWARE\Policies\Microsoft\WindowsStore' -ErrorAction SilentlyContinue).RemoveWindowsStore
"$c|$u|$s"`)
	if err != nil {
		st.Problem = "cannot read settings: " + err.Error()
		return st
	}
	f := strings.Split(strings.TrimSpace(out), "|")
	regLocked := len(f) == 3 && f[0] == "0" && f[1] == "1" && f[2] == "1"
	admins, err := listAdmins(ctx)
	if err != nil {
		st.Problem = "cannot list Administrators: " + err.Error()
		return st
	}
	extra := []string{}
	for _, a := range admins {
		st.Admins = append(st.Admins, a.Name)
		if !keepAdmin(a, p.Keep) {
			extra = append(extra, a.Name)
		}
	}
	st.Locked = regLocked && len(extra) == 0
	noGroup := []string{}
	if p.InstallLock && len(p.Removed) > 0 {
		if users, err := listMembers(ctx, usersSID); err == nil {
			in := map[string]bool{}
			for _, u := range users {
				in[u.SID] = true
			}
			for _, r := range p.Removed {
				if r.SID != "" && !in[r.SID] {
					noGroup = append(noGroup, r.Name)
				}
			}
		}
	}
	if p.InstallLock {
		seen := map[string]bool{}
		for _, s := range staleAdminSessions(ctx, p) {
			if !seen[s.Name] {
				seen[s.Name] = true
				st.PendingSignOut = append(st.PendingSignOut, s.Name)
			}
		}
	}
	switch {
	case p.InstallLock && !regLocked:
		st.Problem = "lock settings missing"
	case len(noGroup) > 0:
		st.Problem = "cannot sign in (not in Users): " + strings.Join(noGroup, ", ")
	case p.InstallLock && len(extra) > 0:
		st.Problem = "not allowed in Administrators: " + strings.Join(extra, ", ")
	case !p.InstallLock && len(p.Removed) > 0:
		st.Problem = "accounts not restored yet: " + strings.Join(st.Removed, ", ")
	}
	st.Applied = st.Problem == "" && (p.InstallLock == st.Locked || !p.InstallLock)
	return st
}

type staleSession struct {
	Name    string
	Session int
}

// staleAdminSessions: sessions of demoted accounts that began before the demotion. Read from
// each session's explorer.exe (its owner and start time), which works on any Windows language.
func staleAdminSessions(ctx context.Context, p policyFile) []staleSession {
	if len(p.Removed) == 0 {
		return nil
	}
	out, err := ps(ctx, time.Minute, `
$r = @(Get-CimInstance Win32_Process -Filter "Name='explorer.exe'" | ForEach-Object {
  $o = Invoke-CimMethod -InputObject $_ -MethodName GetOwnerSid
  [pscustomobject]@{ sid = [string]$o.Sid; session = [int]$_.SessionId; since = $_.CreationDate.ToUniversalTime().ToString('o') }
})
if ($r.Count -eq 0) { '[]' } else { ConvertTo-Json -InputObject $r -Compress }`)
	if err != nil {
		return nil
	}
	return matchStaleSessions(out, p)
}

func matchStaleSessions(out string, p policyFile) []staleSession {
	i := strings.IndexAny(out, "[{")
	if i < 0 {
		return nil
	}
	js := strings.TrimSpace(out[i:])
	if strings.HasPrefix(js, "{") {
		js = "[" + js + "]"
	}
	var procs []struct {
		SID     string `json:"sid"`
		Session int    `json:"session"`
		Since   string `json:"since"`
	}
	if json.Unmarshal([]byte(js), &procs) != nil {
		return nil
	}
	var stale []staleSession
	done := map[int]bool{}
	for _, pr := range procs {
		since, err := time.Parse(time.RFC3339Nano, pr.Since)
		if err != nil || done[pr.Session] {
			continue
		}
		for _, r := range p.Removed {
			at := r.RemovedAt
			if at.IsZero() { // policy file written before RemovedAt existed
				at = p.AppliedAt
			}
			if r.SID != "" && r.SID == pr.SID && since.Before(at) {
				done[pr.Session] = true
				stale = append(stale, staleSession{Name: r.Name, Session: pr.Session})
			}
		}
	}
	return stale
}

// signOutStale signs out the demoted accounts that are still signed in from before the lock,
// and nobody else. Unsaved work in those sessions is lost.
func signOutStale(ctx context.Context) (string, any, error) {
	policyMu.Lock()
	p := loadPolicy()
	policyMu.Unlock()
	if !p.InstallLock {
		return "", nil, errors.New("the install lock is off on this machine")
	}
	stale := staleAdminSessions(ctx, p)
	if len(stale) == 0 {
		return "nobody is signed in with rights from before the lock", map[string]any{"install_lock": policyState(ctx)}, nil
	}
	var log []string
	var failed error
	for _, s := range stale {
		if err := logoffSession(ctx, s.Session); err != nil {
			failed = err
			log = append(log, fmt.Sprintf("could not sign out %s (session %d): %v", s.Name, s.Session, err))
			continue
		}
		log = append(log, fmt.Sprintf("signed out %s (session %d)", s.Name, s.Session))
	}
	return strings.Join(log, "\n"), map[string]any{"install_lock": policyState(ctx)}, failed
}

var lastReapply time.Time

// reconcilePolicy re-applies the lock if Windows drifted (called after each inventory).
func reconcilePolicy(ctx context.Context, st lockState, lg interface{ Printf(string, ...any) }) {
	if !st.Desired || st.Applied || time.Since(lastReapply) < 10*time.Minute {
		return
	}
	lastReapply = time.Now()
	p := loadPolicy()
	out, _, err := applyPolicy(ctx, true, p.Keep)
	lg.Printf("install lock drifted (%s), re-applied: %s %v", st.Problem, strings.ReplaceAll(out, "\n", "; "), err)
}

func setPolicy(ctx context.Context, a manifest.SetPolicyArgs) (string, any, error) {
	out, data, err := applyPolicy(ctx, a.InstallLock, a.Keep)
	if err != nil {
		p := loadPolicy()
		p.Error = err.Error()
		savePolicy(p)
		return out, data, err
	}
	if a.InstallLock && a.SignOutS > 0 {
		if stale := staleAdminSessions(ctx, loadPolicy()); len(stale) > 0 {
			wait := time.Duration(min(max(a.SignOutS, 15), 600)) * time.Second
			names := []string{}
			for _, s := range stale {
				names = append(names, s.Name)
			}
			out += fmt.Sprintf("\n%s told to save their work; signed out in %d s so the lock takes effect", strings.Join(names, ", "), int(wait.Seconds()))
			go signOutAfter(wait, stale)
		}
	}
	return out, data, err
}

const signOutNotice = "IT ล็อกการติดตั้งโปรแกรมบนเครื่องนี้ผ่าน SWIM เครื่องจะออกจากระบบใน %d วินาที กรุณาบันทึกงาน แล้วเข้าใช้งานใหม่ได้ทันที"

// signOutAfter warns each session, waits, then signs out the ones that are still stale.
func signOutAfter(wait time.Duration, stale []staleSession) {
	ctx := context.Background()
	for _, s := range stale {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		_ = command(c, "msg.exe", strconv.Itoa(s.Session), fmt.Sprintf("/TIME:%d", int(wait.Seconds())), fmt.Sprintf(signOutNotice, int(wait.Seconds()))).Run()
		cancel()
	}
	time.Sleep(wait)
	policyMu.Lock()
	p := loadPolicy()
	policyMu.Unlock()
	if !p.InstallLock { // unlocked in the meantime
		return
	}
	for _, s := range staleAdminSessions(ctx, p) {
		err := logoffSession(ctx, s.Session)
		if pkgLog != nil {
			pkgLog.Printf("install lock: signed out %s (session %d) %v", s.Name, s.Session, err)
		}
	}
	requestInventory()
}
