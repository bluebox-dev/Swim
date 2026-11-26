//go:build windows

package examagent

// Package examagent is the agent half of the exam plugin ("โหมดสอบ"). It is in the agent
// only when the plugin is enabled at build time; the core never refers to it. Every entry
// point recovers from its own failures, so a fault here cannot stop the agent.
//
// This file applies the settings (machine policies in HKLM, which a standard user cannot
// change). watch_windows.go is the watcher that closes blocked programs and takes
// the screenshots; the same file sends its findings to the server.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"swim/agent/plugin"
	"swim/plugins/exam/contract"
)

type examFile struct {
	On        bool                `json:"on"`
	Config    contract.ExamConfig `json:"config"`
	AppliedAt time.Time           `json:"applied_at"`
	Error     string              `json:"error,omitempty"`
}

var examMu sync.Mutex

func init() {
	plugin.Register(plugin.Plugin{
		Name: "exam",
		Commands: map[string]func(context.Context, json.RawMessage) (string, any, error){
			"set_exam": func(ctx context.Context, raw json.RawMessage) (string, any, error) {
				var a contract.ExamArgs
				if err := json.Unmarshal(raw, &a); err != nil {
					return "", nil, errors.New("invalid arguments")
				}
				return setExam(ctx, a)
			},
		},
		// reported as inventory.plugins.exam; re-applies the settings if they were removed
		Inventory: func(ctx context.Context) any {
			st := readExamState(ctx)
			if examLog != nil {
				go reconcileExam(context.Background(), st, examLog)
			}
			return st
		},
		Volatile: []string{"checked_at", "watcher"},
		Service: func(ctx context.Context, env plugin.Env) {
			examLog = env.Log
			examService(ctx, env)
		},
		// the agent is being removed: lift every limit exam mode set
		Uninstall: func(ctx context.Context) string {
			if !loadExam().On {
				return ""
			}
			examMu.Lock()
			defer examMu.Unlock()
			if err := applyExam(ctx, false, contract.ExamConfig{}); err != nil {
				return "exam mode: could not lift all limits: " + err.Error()
			}
			saveExam(examFile{On: false, AppliedAt: time.Now().UTC()}) // the watcher sees it and ends
			return "exam mode off: browsers, programs and devices are back to normal"
		},
		Subcommands: map[string]func([]string) int{
			// the watcher, started by the service inside the signed-in user's session
			"exam-watch": func(args []string) int {
				fs := flag.NewFlagSet("exam-watch", flag.ContinueOnError)
				user := fs.String("user", "", "signed-in user")
				_ = fs.Parse(args)
				return examWatch(*user)
			},
		},
	})
}

var examLog interface{ Printf(string, ...any) }

func examDir() string   { return plugin.DataDir("exam") }
func examPath() string  { return filepath.Join(examDir(), "exam.json") }
func examSpool() string { return filepath.Join(examDir(), "spool") }

func loadExam() examFile {
	e, _ := readExam()
	return e
}

// readExam tells "exam mode is off" (no file: zero value, nil) from "could not read the file
// right now" (error). The file is replaced while others read it, and on Windows a read can
// fail for a moment at exactly that time; the watcher must not take that for "off".
func readExam() (examFile, error) {
	var e examFile
	var err error
	for try := 0; try < 4; try++ {
		var b []byte
		if b, err = os.ReadFile(examPath()); err == nil {
			if err = json.Unmarshal(b, &e); err == nil {
				return e, nil
			}
		} else if os.IsNotExist(err) {
			return examFile{}, nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return examFile{}, err
}

func saveExam(e examFile) {
	_ = os.MkdirAll(examDir(), 0o700)
	b, _ := json.MarshalIndent(e, "", "  ")
	tmp := examPath() + ".tmp"
	if os.WriteFile(tmp, b, 0o600) != nil {
		return
	}
	for try := 0; try < 10; try++ { // a reader may hold the file for a moment
		if os.Rename(tmp, examPath()) == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = os.WriteFile(examPath(), b, 0o600)
}

func psList(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = "'" + s + "'" // site names: letters, digits, dots and dashes only (checked by Normalize)
	}
	return "@(" + strings.Join(q, ",") + ")"
}

// examClear removes everything exam mode wrote. The marker value SwimExam tells its settings
// from policies somebody else set, which stay untouched.
const examClear = `
foreach ($b in 'Microsoft\Edge','Google\Chrome','BraveSoftware\Brave') {
  $k = "HKLM:\SOFTWARE\Policies\$b"
  if ((Get-ItemProperty $k -ErrorAction SilentlyContinue).SwimExam -eq 1) {
    foreach ($n in 'URLBlocklist','URLAllowlist','ExtensionInstallBlocklist') { Remove-Item "$k\$n" -Recurse -Force -ErrorAction SilentlyContinue }
    foreach ($n in 'DeveloperToolsAvailability','IncognitoModeAvailability','InPrivateModeAvailability','HubsSidebarEnabled','SwimExam') { Remove-ItemProperty $k -Name $n -ErrorAction SilentlyContinue }
  }
}
$f = 'HKLM:\SOFTWARE\Policies\Mozilla\Firefox'
if ((Get-ItemProperty $f -ErrorAction SilentlyContinue).SwimExam -eq 1) {
  Remove-Item "$f\WebsiteFilter" -Recurse -Force -ErrorAction SilentlyContinue
  foreach ($n in 'DisableDeveloperTools','DisablePrivateBrowsing','SwimExam') { Remove-ItemProperty $f -Name $n -ErrorAction SilentlyContinue }
}
$c = 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\WindowsCopilot'
if ((Get-ItemProperty $c -ErrorAction SilentlyContinue).SwimExam -eq 1) { foreach ($n in 'TurnOffWindowsCopilot','SwimExam') { Remove-ItemProperty $c -Name $n -ErrorAction SilentlyContinue } }
$u = 'HKLM:\SOFTWARE\Policies\Microsoft\Windows\RemovableStorageDevices'
if ((Get-ItemProperty $u -ErrorAction SilentlyContinue).SwimExam -eq 1) { foreach ($n in 'Deny_All','SwimExam') { Remove-ItemProperty $u -Name $n -ErrorAction SilentlyContinue } }
`

// examReload makes running browsers read the new settings now. Edge and Chrome do not watch
// the registry: they reload policies when Windows announces a Group Policy refresh, and
// otherwise only every 15 minutes or at their next start.
const examReload = `
try { Start-Process gpupdate.exe -ArgumentList '/target:computer','/wait:0' -WindowStyle Hidden } catch {}
`

func examScript(c contract.ExamConfig) string {
	var b strings.Builder
	b.WriteString(examClear)
	chromium := c.WebMode != "open" || c.NoExtensions || c.NoDevTools || c.NoPrivate || c.NoCopilot
	if chromium {
		fmt.Fprintf(&b, "$allow = %s; $block = %s\n", psList(c.Sites), psList(c.Blocked()))
		b.WriteString(`foreach ($b in 'Microsoft\Edge','Google\Chrome','BraveSoftware\Brave') {
  $k = "HKLM:\SOFTWARE\Policies\$b"; if (!(Test-Path $k)) { $null = New-Item $k -Force }
  Set-ItemProperty $k -Name SwimExam -Value 1 -Type DWord
`)
		switch c.WebMode {
		case "allow":
			b.WriteString(`  $null = New-Item "$k\URLBlocklist" -Force; Set-ItemProperty "$k\URLBlocklist" -Name '1' -Value '*'
  $null = New-Item "$k\URLAllowlist" -Force; $i = 1; foreach ($s in $allow) { Set-ItemProperty "$k\URLAllowlist" -Name "$i" -Value $s; $i++ }
`)
		case "block":
			b.WriteString(`  $null = New-Item "$k\URLBlocklist" -Force; $i = 1; foreach ($s in $block) { Set-ItemProperty "$k\URLBlocklist" -Name "$i" -Value $s; $i++ }
`)
		}
		if c.NoExtensions {
			b.WriteString(`  $null = New-Item "$k\ExtensionInstallBlocklist" -Force; Set-ItemProperty "$k\ExtensionInstallBlocklist" -Name '1' -Value '*'
`)
		}
		if c.NoDevTools {
			b.WriteString("  Set-ItemProperty $k -Name DeveloperToolsAvailability -Value 2 -Type DWord\n")
		}
		if c.NoPrivate {
			b.WriteString("  Set-ItemProperty $k -Name IncognitoModeAvailability -Value 1 -Type DWord\n  Set-ItemProperty $k -Name InPrivateModeAvailability -Value 1 -Type DWord\n")
		}
		if c.NoCopilot {
			b.WriteString("  if ($b -eq 'Microsoft\\Edge') { Set-ItemProperty $k -Name HubsSidebarEnabled -Value 0 -Type DWord }\n")
		}
		b.WriteString("}\n")
		// Firefox
		b.WriteString("$f = 'HKLM:\\SOFTWARE\\Policies\\Mozilla\\Firefox'; if (!(Test-Path $f)) { $null = New-Item $f -Force }; Set-ItemProperty $f -Name SwimExam -Value 1 -Type DWord\n")
		switch c.WebMode {
		case "allow":
			b.WriteString(`$null = New-Item "$f\WebsiteFilter\Block" -Force; $null = New-Item "$f\WebsiteFilter\Exceptions" -Force
Set-ItemProperty "$f\WebsiteFilter\Block" -Name '1' -Value '<all_urls>'
$i = 1; foreach ($s in $allow) { Set-ItemProperty "$f\WebsiteFilter\Exceptions" -Name "$i" -Value "*://$s/*"; $i++; Set-ItemProperty "$f\WebsiteFilter\Exceptions" -Name "$i" -Value "*://*.$s/*"; $i++ }
`)
		case "block":
			b.WriteString(`$null = New-Item "$f\WebsiteFilter\Block" -Force
$i = 1; foreach ($s in $block) { Set-ItemProperty "$f\WebsiteFilter\Block" -Name "$i" -Value "*://$s/*"; $i++; Set-ItemProperty "$f\WebsiteFilter\Block" -Name "$i" -Value "*://*.$s/*"; $i++ }
`)
		}
		if c.NoDevTools {
			b.WriteString("Set-ItemProperty $f -Name DisableDeveloperTools -Value 1 -Type DWord\n")
		}
		if c.NoPrivate {
			b.WriteString("Set-ItemProperty $f -Name DisablePrivateBrowsing -Value 1 -Type DWord\n")
		}
	}
	if c.NoCopilot {
		b.WriteString("$c = 'HKLM:\\SOFTWARE\\Policies\\Microsoft\\Windows\\WindowsCopilot'; if (!(Test-Path $c)) { $null = New-Item $c -Force }; Set-ItemProperty $c -Name TurnOffWindowsCopilot -Value 1 -Type DWord; Set-ItemProperty $c -Name SwimExam -Value 1 -Type DWord\n")
	}
	if c.BlockUSB {
		b.WriteString("$u = 'HKLM:\\SOFTWARE\\Policies\\Microsoft\\Windows\\RemovableStorageDevices'; if (!(Test-Path $u)) { $null = New-Item $u -Force }; Set-ItemProperty $u -Name Deny_All -Value 1 -Type DWord; Set-ItemProperty $u -Name SwimExam -Value 1 -Type DWord\n")
	}
	b.WriteString(examReload + "'ok'")
	return b.String()
}

// applyExam writes the settings; with on=false it only removes them.
func applyExam(ctx context.Context, on bool, c contract.ExamConfig) error {
	if !on {
		_, err := plugin.PowerShell(ctx, time.Minute, examClear+examReload+"'ok'")
		return err
	}
	if err := c.Normalize(); err != nil { // the command is signed; checked again because the names go into a script
		return err
	}
	_, err := plugin.PowerShell(ctx, time.Minute, examScript(c))
	return err
}

type examState struct {
	Desired   bool                `json:"desired"`
	Applied   bool                `json:"applied"` // the machine is in the desired state
	Config    contract.ExamConfig `json:"config"`
	Watching  bool                `json:"watching"` // the watcher answered within the last 30 seconds
	Watcher   *watchBeat          `json:"watcher,omitempty"`
	Spooled   int                 `json:"spooled"` // findings waiting on the machine to be sent
	Problem   string              `json:"problem,omitempty"`
	Error     string              `json:"error,omitempty"`
	CheckedAt time.Time           `json:"checked_at"`
}

// readExamState compares the stored configuration with the marker each product carries.
func readExamState(ctx context.Context) (st examState) {
	defer func() {
		if r := recover(); r != nil {
			st.Problem = fmt.Sprint("exam state: ", r)
		}
	}()
	e := loadExam()
	st = examState{Desired: e.On, Config: e.Config, Error: e.Error, CheckedAt: time.Now().UTC()}
	if e.On {
		if b := readBeat(); b != nil {
			st.Watcher, st.Watching = b, time.Since(b.At) < 30*time.Second
		}
		if f, _ := filepath.Glob(filepath.Join(examSpool(), "*.json")); f != nil {
			st.Spooled = len(f)
		}
	}
	if !e.On {
		st.Applied = true
		return st
	}
	c := e.Config
	if c.WebMode == "open" && !c.NoExtensions && !c.NoDevTools && !c.NoPrivate && !c.NoCopilot {
		st.Applied = true // no browser settings to check
		return st
	}
	out, err := plugin.PowerShell(ctx, time.Minute, `
$k = 'HKLM:\SOFTWARE\Policies\Microsoft\Edge'
$m = (Get-ItemProperty $k -ErrorAction SilentlyContinue).SwimExam
$a = 0; $b = 0 # a missing key must count as 0: @($null).Count is 1
$i = Get-Item "$k\URLAllowlist" -ErrorAction SilentlyContinue; if ($i) { $a = @($i.Property).Count }
$i = Get-Item "$k\URLBlocklist" -ErrorAction SilentlyContinue; if ($i) { $b = @($i.Property).Count }
"$m/$a/$b"`)
	if err != nil {
		st.Problem = "cannot read browser settings: " + err.Error()
		return st
	}
	want := "1/0/0"
	switch c.WebMode {
	case "allow":
		want = fmt.Sprintf("1/%d/1", len(c.Sites))
	case "block":
		want = fmt.Sprintf("1/0/%d", len(c.Blocked()))
	}
	if strings.TrimSpace(out) != want {
		st.Problem = "browser settings missing or changed"
	}
	st.Applied = st.Problem == ""
	return st
}

const examNotice = "เครื่องนี้อยู่ในโหมดสอบ ระบบจำกัดเว็บและโปรแกรมตามที่ผู้คุมสอบกำหนด และบันทึกภาพหน้าจอเป็นหลักฐานเมื่อพบการใช้งานที่ไม่อนุญาต"

func setExam(ctx context.Context, a contract.ExamArgs) (out string, data any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("exam mode: %v", r)
		}
	}()
	examMu.Lock()
	defer examMu.Unlock()
	was := loadExam()
	e := examFile{On: a.On, AppliedAt: time.Now().UTC()}
	if a.On {
		if err := a.Config.Normalize(); err != nil {
			return "", nil, err
		}
		e.Config = a.Config
	}
	err = applyExam(ctx, a.On, e.Config)
	if err != nil {
		e.Error = err.Error()
	}
	saveExam(e)
	examKick()
	st := readExamState(ctx)
	out = "exam mode off: browsers, programs and devices are back to normal"
	if a.On {
		out = "exam mode on: " + describeExam(e.Config)
		if !was.On && err == nil {
			go examTell(examNotice) // the person at the machine is told that it is watched
		}
	}
	if err == nil && !st.Applied {
		err = errors.New("exam mode not fully applied: " + st.Problem)
	}
	return out, map[string]any{"exam": st}, err
}

func describeExam(c contract.ExamConfig) string {
	var p []string
	switch c.WebMode {
	case "allow":
		p = append(p, fmt.Sprintf("browsers open only %d listed site(s)", len(c.Sites)))
	case "block":
		p = append(p, fmt.Sprintf("%d site(s) blocked", len(c.Blocked())))
	}
	if n := len(c.Apps()); n > 0 {
		p = append(p, fmt.Sprintf("%d program(s) closed on start", n))
	}
	if c.BlockUSB {
		p = append(p, "USB storage off")
	}
	if c.ShotOnViolate {
		p = append(p, "screenshot with every violation")
	}
	if c.ShotEveryMin > 0 {
		p = append(p, fmt.Sprintf("screenshot every %d min", c.ShotEveryMin))
	}
	if len(p) == 0 {
		return "no limits set"
	}
	return strings.Join(p, "; ")
}

var lastExamReapply time.Time

// reconcileExam re-applies exam mode if its settings were removed.
func reconcileExam(ctx context.Context, st examState, lg interface{ Printf(string, ...any) }) {
	defer func() { _ = recover() }()
	if !st.Desired || st.Applied || st.Error != "" || time.Since(lastExamReapply) < 10*time.Minute {
		return
	}
	lastExamReapply = time.Now()
	examMu.Lock()
	e := loadExam()
	err := applyExam(ctx, e.On, e.Config)
	examMu.Unlock()
	lg.Printf("exam mode drifted (%s), re-applied: %v", st.Problem, err)
	examReport(examEvent{Kind: "settings_changed", Detail: map[string]string{"problem": st.Problem}})
}
