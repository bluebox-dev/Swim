//go:build windows

package examagent

// The exam watcher. The service cannot see the user's desktop (it lives in session 0), so
// while exam mode needs watching it starts a copy of the agent, "exam-watch", inside the
// signed-in user's session. The copy runs as SYSTEM, so the user cannot close it. It
//
//   - closes programs on the blocked list as soon as they start,
//   - looks at the title of the window in front for the watched words,
//   - takes screenshots (with a violation, and every N minutes if asked),
//
// and writes each finding into the spool folder. The service uploads the spool
// (examService) and restarts the watcher if it stops.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"swim/agent/plugin"
)

type examEvent struct {
	At     time.Time         `json:"at"`
	Kind   string            `json:"kind"` // app_blocked, keyword, periodic, watcher_stopped, settings_changed
	Detail map[string]string `json:"detail,omitempty"`
	Shot   string            `json:"shot,omitempty"` // JPEG file beside the event in the spool
}

const (
	examSpoolMax   = 300 // files; beyond it screenshots are dropped, events still recorded
	examExitTwice  = 3   // exit code: another watcher already runs in this session
	examShotsAMin  = 6
	examShotWidth  = 1920
	examShotQ      = 60
	examSameWithin = time.Minute
)

var examSeq, examFound atomic.Int64

func spoolWrite(ev examEvent, jpg []byte) {
	defer func() { _ = recover() }()
	dir := examSpool()
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	if ev.Detail != nil && ev.Detail["user"] != "" { // written by the watcher, inside the user's session
		if n := idleSeconds(); n >= 0 {
			ev.Detail["idle_s"] = strconv.Itoa(n)
		}
	}
	base := fmt.Sprintf("%d-%d-%d", ev.At.UnixNano(), os.Getpid(), examSeq.Add(1))
	if len(jpg) > 0 {
		if n, _ := os.ReadDir(dir); len(n) < examSpoolMax {
			if os.WriteFile(filepath.Join(dir, base+".jpg"), jpg, 0o600) == nil {
				ev.Shot = base + ".jpg"
			}
		}
	}
	examFound.Add(1)
	b, _ := json.Marshal(ev)
	tmp := filepath.Join(dir, base+".tmp")
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, filepath.Join(dir, base+".json")) // the uploader only reads *.json
	}
}

// watchBeat is the watcher's sign of life, written every few seconds. The service reports it,
// so "watching" on the server is something the watcher itself said, not an assumption.
type watchBeat struct {
	At      time.Time `json:"at"`
	Version string    `json:"version"`
	PID     int       `json:"pid"`
	Session uint32    `json:"session"`
	User    string    `json:"user"`
	Rounds  int       `json:"rounds"`
	Found   int       `json:"found"`           // findings written since it started
	Apps    int       `json:"apps"`            // programs on its blocked list
	Words   int       `json:"words"`           // words it looks for
	IdleS   int       `json:"idle_s"`          // seconds since the last key press or mouse move in the session
	Front   string    `json:"front,omitempty"` // program that owns the window in front (its name only, not what it shows)
	LastErr string    `json:"last_error,omitempty"`
}

func beatPath() string { return filepath.Join(examDir(), "watch.json") }

func readBeat() *watchBeat {
	b, err := os.ReadFile(beatPath())
	var w watchBeat
	if err != nil || json.Unmarshal(b, &w) != nil {
		return nil
	}
	return &w
}

func writeBeat(w watchBeat) {
	b, _ := json.Marshal(w)
	_ = os.WriteFile(beatPath(), b, 0o600)
}

// examReport records a finding made by the service itself (no screenshot).
func examReport(ev examEvent) { spoolWrite(ev, nil) }

// ───────── service side ─────────

var (
	examKickCh  = make(chan struct{}, 1)
	examRunning atomic.Bool
)

func examKick() {
	select {
	case examKickCh <- struct{}{}:
	default:
	}
}

func examWatching() bool { return examRunning.Load() }

func examTell(msg string) {
	defer func() { _ = recover() }()
	ctx := context.Background()
	if _, sid, ok := plugin.ConsoleUser(ctx); ok {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_ = plugin.Exec(c, "msg.exe", strconv.Itoa(sid), "/TIME:60", msg).Run()
	}
}

// examService keeps the watcher running while exam mode needs it and uploads what it found.
// It never returns an error to the agent: exam mode must not be able to stop anything else.
func examService(ctx context.Context, env plugin.Env) {
	lg := env.Log
	var (
		proc     windows.Handle
		session  = -1
		external time.Time // another watcher holds the session (started before a service restart)
	)
	stop := func() {
		if proc != 0 {
			_ = windows.TerminateProcess(proc, 0)
			_ = windows.CloseHandle(proc)
			proc, session = 0, -1
		}
		examRunning.Store(false)
	}
	defer stop()
	_ = os.MkdirAll(examDir(), 0o700)
	_ = os.WriteFile(filepath.Join(examDir(), "service.ver"), []byte(plugin.Version()), 0o600) // an older watcher sees it and ends
	uploadFailed := false
	tick := func() {
		defer func() {
			if r := recover(); r != nil {
				lg.Printf("exam: recovered: %v", r)
			}
		}()
		e := loadExam()
		need := e.On && e.Config.Watches()
		if !need {
			stop()
		} else {
			user, sid, ok := plugin.ConsoleUser(ctx)
			alive := false
			if proc != 0 {
				if ev, _ := windows.WaitForSingleObject(proc, 0); ev == uint32(windows.WAIT_TIMEOUT) {
					alive = true
				} else {
					var code uint32
					_ = windows.GetExitCodeProcess(proc, &code)
					_ = windows.CloseHandle(proc)
					proc = 0
					switch {
					case code == examExitTwice:
						lg.Printf("exam: another watcher already runs in session %d", session)
						external = time.Now()
					case code != 0 && ok && sid == session:
						lg.Printf("exam: watcher stopped (exit %d), starting it again", code)
						examReport(examEvent{Kind: "watcher_stopped", Detail: map[string]string{"exit_code": strconv.Itoa(int(code)), "user": user}})
					}
				}
			}
			if alive && (!ok || sid != session) { // the user signed out or another one is at the console
				stop()
				alive = false
			}
			if !alive && ok && time.Since(external) > time.Minute {
				h, err := startWatcher(uint32(sid), user)
				if err != nil {
					lg.Printf("exam: cannot start the watcher in session %d: %v", sid, err)
				} else {
					lg.Printf("exam: watcher started in session %d for %s", sid, user)
					proc, session = h, sid
					alive = true
				}
			}
			examRunning.Store(alive || time.Since(external) <= time.Minute)
		}
		if err := examUpload(env); err != nil {
			if !uploadFailed {
				lg.Printf("exam: cannot send findings, keeping them: %v", err)
			}
			uploadFailed = true
		} else {
			uploadFailed = false
		}
	}
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		tick()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-examKickCh:
		}
	}
}

// startWatcher runs "<copy of the agent> exam-watch" as SYSTEM inside the user's session. A
// copy, so the agent's own file stays free for self-updates while the watcher runs.
func startWatcher(session uint32, user string) (windows.Handle, error) {
	self, err := os.Executable()
	if err != nil {
		return 0, err
	}
	exe := filepath.Join(examDir(), "swim-exam-watch-"+plugin.Version()+".exe")
	if _, err := os.Stat(exe); err != nil {
		_ = os.MkdirAll(examDir(), 0o700)
		if err := copyExe(self, exe); err != nil {
			return 0, err
		}
		if old, _ := filepath.Glob(filepath.Join(examDir(), "swim-exam-watch-*.exe")); len(old) > 1 {
			for _, o := range old {
				if o != exe {
					_ = os.Remove(o) // fails while an old watcher still runs; removed next time
				}
			}
		}
	}
	var mine, dup windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_ASSIGN_PRIMARY|
		windows.TOKEN_ADJUST_SESSIONID|windows.TOKEN_ADJUST_DEFAULT, &mine); err != nil {
		return 0, fmt.Errorf("open token: %w", err)
	}
	defer mine.Close()
	if err := windows.DuplicateTokenEx(mine, windows.MAXIMUM_ALLOWED, nil, windows.SecurityIdentification, windows.TokenPrimary, &dup); err != nil {
		return 0, fmt.Errorf("duplicate token: %w", err)
	}
	defer dup.Close()
	if err := windows.SetTokenInformation(dup, windows.TokenSessionId, (*byte)(unsafe.Pointer(&session)), 4); err != nil {
		return 0, fmt.Errorf("set session: %w", err)
	}
	line, err := windows.UTF16PtrFromString(`"` + exe + `" exam-watch --user "` + strings.ReplaceAll(user, `"`, "") + `"`)
	if err != nil {
		return 0, err
	}
	desk, _ := windows.UTF16PtrFromString(`winsta0\default`)
	si := windows.StartupInfo{Desktop: desk}
	si.Cb = uint32(unsafe.Sizeof(si))
	var pi windows.ProcessInformation
	if err := windows.CreateProcessAsUser(dup, nil, line, nil, nil, false, windows.CREATE_NO_WINDOW, nil, nil, &si, &pi); err != nil {
		return 0, fmt.Errorf("create process: %w", err)
	}
	_ = windows.CloseHandle(pi.Thread)
	return pi.Process, nil
}

// examUpload sends spooled findings, oldest first, a few per round.
func examUpload(env plugin.Env) error {
	lg := env.Log
	files, _ := filepath.Glob(filepath.Join(examSpool(), "*.json"))
	sort.Strings(files)
	for i, f := range files {
		if i >= 8 {
			return nil
		}
		b, err := os.ReadFile(f)
		var ev examEvent
		if err != nil || json.Unmarshal(b, &ev) != nil {
			_ = os.Remove(f)
			continue
		}
		body := map[string]any{"at": ev.At, "kind": ev.Kind, "detail": ev.Detail}
		shot := ""
		if ev.Shot != "" && filepath.Base(ev.Shot) == ev.Shot {
			shot = filepath.Join(examSpool(), ev.Shot)
			if jpg, err := os.ReadFile(shot); err == nil {
				body["shot"] = base64.StdEncoding.EncodeToString(jpg)
			}
		}
		err = env.Post("/v1/agent/exam/events", body, nil)
		if st := plugin.StatusOf(err); err != nil && !(st >= 400 && st < 500 && st != 401 && st != 429) {
			return err // server unreachable or busy: keep the spool, try next round
		}
		if err != nil {
			lg.Printf("exam: server refused a finding (%v), dropped", err)
		}
		_ = os.Remove(f)
		if shot != "" {
			_ = os.Remove(shot)
		}
	}
	return nil
}

// ───────── watcher side (runs in the user's session) ─────────

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	gdi32                   = windows.NewLazySystemDLL("gdi32.dll")
	pGetSystemMetrics       = user32.NewProc("GetSystemMetrics")
	pGetDC                  = user32.NewProc("GetDC")
	pReleaseDC              = user32.NewProc("ReleaseDC")
	pGetForegroundWindow    = user32.NewProc("GetForegroundWindow")
	pGetWindowTextW         = user32.NewProc("GetWindowTextW")
	pGetWindowThreadProcID  = user32.NewProc("GetWindowThreadProcessId")
	pSetProcessDPIAware     = user32.NewProc("SetProcessDPIAware")
	pCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	pCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	pSelectObject           = gdi32.NewProc("SelectObject")
	pBitBlt                 = gdi32.NewProc("BitBlt")
	pGetDIBits              = gdi32.NewProc("GetDIBits")
	pDeleteObject           = gdi32.NewProc("DeleteObject")
	pDeleteDC               = gdi32.NewProc("DeleteDC")
	errExamNoScreen         = errors.New("no screen to capture")
	examShotMu              sync.Mutex
	examShotTimes           []time.Time
	examLastSeen            = map[string]time.Time{}
)

type bitmapInfoHeader struct {
	Size          uint32
	Width, Height int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPels, YPels  int32
	ClrUsed       uint32
	ClrImportant  uint32
}

func metric(i uintptr) int {
	r, _, _ := pGetSystemMetrics.Call(i)
	return int(int32(r))
}

// captureScreen returns the whole desktop (all monitors) as a JPEG no wider than examShotWidth.
func captureScreen() ([]byte, error) {
	x, y, w, h := metric(76), metric(77), metric(78), metric(79) // the virtual screen
	if w <= 0 || h <= 0 || w > 20000 || h > 20000 {
		return nil, errExamNoScreen
	}
	hdc, _, _ := pGetDC.Call(0)
	if hdc == 0 {
		return nil, errExamNoScreen
	}
	defer pReleaseDC.Call(0, hdc)
	mem, _, _ := pCreateCompatibleDC.Call(hdc)
	if mem == 0 {
		return nil, errExamNoScreen
	}
	defer pDeleteDC.Call(mem)
	bmp, _, _ := pCreateCompatibleBitmap.Call(hdc, uintptr(w), uintptr(h))
	if bmp == 0 {
		return nil, errExamNoScreen
	}
	defer pDeleteObject.Call(bmp)
	old, _, _ := pSelectObject.Call(mem, bmp)
	const srcCopy, captureBlt = 0x00CC0020, 0x40000000
	ok, _, _ := pBitBlt.Call(mem, 0, 0, uintptr(w), uintptr(h), hdc, uintptr(int32(x)), uintptr(int32(y)), srcCopy|captureBlt)
	pSelectObject.Call(mem, old) // a bitmap must not be selected while GetDIBits reads it
	if ok == 0 {
		return nil, errExamNoScreen
	}
	bi := bitmapInfoHeader{Size: 40, Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}
	buf := make([]byte, w*h*4)
	if n, _, _ := pGetDIBits.Call(hdc, bmp, 0, uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&bi)), 0); n == 0 {
		return nil, errExamNoScreen
	}
	f := 1
	for w/f > examShotWidth {
		f++
	}
	ow, oh := w/f, h/f
	img := image.NewRGBA(image.Rect(0, 0, ow, oh))
	for oy := 0; oy < oh; oy++ {
		for ox := 0; ox < ow; ox++ {
			var r, g, b, n int
			for dy := 0; dy < f; dy++ {
				row := ((oy*f+dy)*w + ox*f) * 4
				for dx := 0; dx < f; dx++ {
					p := row + dx*4
					b, g, r, n = b+int(buf[p]), g+int(buf[p+1]), r+int(buf[p+2]), n+1
				}
			}
			o := img.PixOffset(ox, oy)
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = uint8(r/n), uint8(g/n), uint8(b/n), 255
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: examShotQ}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func foreground() (title, process string) {
	hwnd, _, _ := pGetForegroundWindow.Call()
	if hwnd == 0 {
		return "", ""
	}
	buf := make([]uint16, 512)
	n, _, _ := pGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	title = windows.UTF16ToString(buf[:n])
	var pid uint32
	pGetWindowThreadProcID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid); err == nil {
		defer windows.CloseHandle(h)
		name := make([]uint16, 520)
		size := uint32(len(name))
		if windows.QueryFullProcessImageName(h, 0, &name[0], &size) == nil {
			process = filepath.Base(windows.UTF16ToString(name[:size]))
		}
	}
	return title, process
}

var (
	pGetLastInputInfo = user32.NewProc("GetLastInputInfo")
	pGetTickCount     = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount")
)

// idleSeconds: how long nobody has touched the keyboard or mouse in this session (-1 = unknown).
// Recorded with every finding: a screenshot of a machine nobody is using shows the last thing
// that was on screen, and the reader must be able to tell.
func idleSeconds() int {
	var li struct{ Size, Time uint32 }
	li.Size = 8
	if r, _, _ := pGetLastInputInfo.Call(uintptr(unsafe.Pointer(&li))); r == 0 {
		return -1
	}
	now, _, _ := pGetTickCount.Call()
	return int(uint32(now)-li.Time) / 1000
}

// violation records one finding, at most once a minute for the same thing, with a screenshot
// when the configuration asks for one. shot may already hold a capture taken before a
// program was closed.
func violation(kind, key string, detail map[string]string, wantShot bool, shot []byte) {
	now := time.Now()
	if t, ok := examLastSeen[kind+"|"+key]; ok && now.Sub(t) < examSameWithin {
		return
	}
	examLastSeen[kind+"|"+key] = now
	if wantShot && shot == nil {
		shot = limitedShot()
	}
	spoolWrite(examEvent{At: now.UTC(), Kind: kind, Detail: detail}, shot)
}

func limitedShot() []byte {
	examShotMu.Lock()
	defer examShotMu.Unlock()
	now := time.Now()
	keep := examShotTimes[:0]
	for _, t := range examShotTimes {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	examShotTimes = keep
	if len(examShotTimes) >= examShotsAMin {
		return nil
	}
	examShotTimes = append(examShotTimes, now)
	jpg, err := captureScreen()
	if err != nil {
		return nil
	}
	return jpg
}

// ExamWatch is the "exam-watch" sub-command. It ends by itself when exam mode is turned
// off, when the agent was updated, or when another watcher already runs in the session.
func examWatch(user string) int {
	var mySession uint32
	_ = windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &mySession)
	name, _ := windows.UTF16PtrFromString(fmt.Sprintf(`Global\SwimExamWatch-%d`, mySession))
	if _, err := windows.CreateMutex(nil, false, name); errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return examExitTwice
	}
	pSetProcessDPIAware.Call()
	var (
		apps     = map[string]bool{}
		words    []string
		cfgAt    time.Time
		lastShot = time.Now()
		round    int
	)
	var e examFile
	lastErr, lastFront := "", ""
	for {
		if got, err := readExam(); err == nil {
			e = got
			if !e.On || !e.Config.Watches() {
				return 0
			}
		} else {
			lastErr = "read set-up: " + err.Error() // keep watching with the set-up we have
			if round == 0 {
				time.Sleep(time.Second)
				continue
			}
		}
		if v, err := os.ReadFile(filepath.Join(examDir(), "service.ver")); err == nil && strings.TrimSpace(string(v)) != plugin.Version() {
			return 0 // the agent was updated: its new watcher takes over
		}
		if round%4 == 0 {
			writeBeat(watchBeat{At: time.Now().UTC(), Version: plugin.Version(), PID: os.Getpid(), Session: mySession, User: user, Rounds: round,
				Found: int(examFound.Load()), Apps: len(apps), Words: len(words), IdleS: idleSeconds(), Front: lastFront, LastErr: lastErr})
		}
		if st, err := os.Stat(examPath()); err == nil && !st.ModTime().Equal(cfgAt) {
			cfgAt = st.ModTime()
			apps = map[string]bool{}
			for _, a := range e.Config.Apps() {
				apps[a] = true
			}
			words = e.Config.Words()
		}
		c := e.Config
		func() {
			defer func() {
				if r := recover(); r != nil {
					lastErr = fmt.Sprint("recovered: ", r)
				}
			}()
			title, proc := foreground()
			lastFront = proc
			if title == "" && proc == "" {
				lastFront = "(none)"
			}
			if title != "" && len(words) > 0 {
				low := strings.ToLower(title)
				for _, w := range words {
					if strings.Contains(low, w) {
						{
							violation("keyword", w, map[string]string{"word": w, "window": clip(title, 200), "process": proc, "user": user}, c.ShotOnViolate, nil)
						}
						break
					}
				}
			}
			if len(apps) > 0 && round%2 == 0 {
				closeBlocked(apps, mySession, user, c.ShotOnViolate)
			}
			if c.ShotEveryMin > 0 && time.Since(lastShot) >= time.Duration(c.ShotEveryMin)*time.Minute {
				lastShot = time.Now()
				if jpg := limitedShot(); jpg != nil {
					spoolWrite(examEvent{Kind: "periodic", Detail: map[string]string{"window": clip(title, 200), "process": proc, "user": user}}, jpg)
				}
			}
		}()
		round++
		time.Sleep(2 * time.Second)
	}
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// appWindow is a window a person can actually see or switch to.
type appWindow struct {
	Title string
	State string // "front" (the window in use), "open" (on screen, perhaps behind another) or "minimized"
}

var (
	pEnumWindows      = user32.NewProc("EnumWindows")
	pIsWindowVisible  = user32.NewProc("IsWindowVisible")
	pIsIconic         = user32.NewProc("IsIconic")
	pGetWindowLongW   = user32.NewProc("GetWindowLongW")
	pFindWindowExW    = user32.NewProc("FindWindowExW")
	pDwmGetWindowAttr = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmGetWindowAttribute")
	enumResult        map[uint32][]appWindow
	enumCallback      = windows.NewCallback(func(hwnd, _ uintptr) uintptr {
		if v, _, _ := pIsWindowVisible.Call(hwnd); v == 0 {
			return 1
		}
		var cloaked uint32 // a suspended Store app keeps a "visible" window that is not on screen
		pDwmGetWindowAttr.Call(hwnd, 14, uintptr(unsafe.Pointer(&cloaked)), 4)
		const gwlExStyle, wsExToolWindow = ^uintptr(19), 0x80 // GWL_EXSTYLE = -20
		ex, _, _ := pGetWindowLongW.Call(hwnd, gwlExStyle)
		if cloaked != 0 || ex&wsExToolWindow != 0 {
			return 1
		}
		buf := make([]uint16, 512)
		n, _, _ := pGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		title := windows.UTF16ToString(buf[:n])
		if title == "" {
			return 1
		}
		var pid uint32
		pGetWindowThreadProcID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		// a Store app's frame belongs to ApplicationFrameHost; the app itself owns the window inside
		if cls, err := windows.UTF16PtrFromString("Windows.UI.Core.CoreWindow"); err == nil {
			if child, _, _ := pFindWindowExW.Call(hwnd, 0, uintptr(unsafe.Pointer(cls)), 0); child != 0 {
				var cp uint32
				pGetWindowThreadProcID.Call(child, uintptr(unsafe.Pointer(&cp)))
				if cp != 0 {
					pid = cp
				}
			}
		}
		state := "open"
		if fg, _, _ := pGetForegroundWindow.Call(); fg == hwnd {
			state = "front"
		} else if ic, _, _ := pIsIconic.Call(hwnd); ic != 0 {
			state = "minimized"
		}
		enumResult[pid] = append(enumResult[pid], appWindow{Title: title, State: state})
		return 1
	})
)

// visibleWindows lists, per process, the windows a person can see. Called from one goroutine only.
func visibleWindows() map[uint32][]appWindow {
	enumResult = map[uint32][]appWindow{}
	pEnumWindows.Call(enumCallback, 0)
	return enumResult
}

var examQuietClosed = map[string]bool{} // background programs already reported once

// closeBlocked ends blocked programs running in this session.
//
// A program with a window the person could see is a violation: the screenshot is taken
// first, and the finding names the window and whether it was in front. A program that only
// runs in the background (started with Windows, no window) is closed too, but recorded once
// as "app_background" without a screenshot: nobody was using it, so it proves nothing.
func closeBlocked(apps map[string]bool, session uint32, user string, wantShot bool) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(snap)
	type found struct {
		exe  string
		pids []uint32
	}
	hits := map[string]*found{}
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		exe := windows.UTF16ToString(pe.ExeFile[:])
		name := strings.TrimSuffix(strings.ToLower(exe), ".exe")
		if !apps[name] || pe.ProcessID == windows.GetCurrentProcessId() {
			continue
		}
		var s uint32
		if windows.ProcessIdToSessionId(pe.ProcessID, &s) != nil || s != session {
			continue
		}
		if hits[name] == nil {
			hits[name] = &found{exe: exe}
		}
		hits[name].pids = append(hits[name].pids, pe.ProcessID)
	}
	if len(hits) == 0 {
		return
	}
	wins := visibleWindows()
	for name, f := range hits {
		var best *appWindow
		rank := map[string]int{"front": 3, "open": 2, "minimized": 1}
		for _, pid := range f.pids {
			for i := range wins[pid] {
				if w := &wins[pid][i]; best == nil || rank[w.State] > rank[best.State] {
					best = w
				}
			}
		}
		var shot []byte
		if best != nil && wantShot {
			shot = limitedShot() // before the program is closed, so the picture shows it
		}
		for _, pid := range f.pids {
			if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid); err == nil {
				_ = windows.TerminateProcess(h, 1)
				_ = windows.CloseHandle(h)
			}
		}
		if best != nil {
			violation("app_blocked", name, map[string]string{"process": f.exe, "window": clip(best.Title, 200), "window_state": best.State, "user": user}, false, shot)
		} else if !examQuietClosed[name] {
			examQuietClosed[name] = true
			spoolWrite(examEvent{Kind: "app_background", Detail: map[string]string{"process": f.exe, "user": user}}, nil)
		}
	}
}

func copyExe(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.WriteFile(to+".tmp", b, 0o700); err != nil {
		return err
	}
	return os.Rename(to+".tmp", to)
}
