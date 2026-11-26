// Package contract is what the exam plugin's server half and agent half agree on.
package contract

import (
	"fmt"
	"sort"
	"strings"
)

// Exam mode is an optional module, separate from the install lock and from installs. This
// file is its whole contract: what an admin can set (ExamConfig), the built-in lists, and
// the check both the server and the agent run on a configuration before using it.

// ExamArgs is the "set_exam" command.
type ExamArgs struct {
	On     bool       `json:"on"`
	Config ExamConfig `json:"config"`
}

// ExamConfig is one exam set-up. The zero value restricts nothing.
type ExamConfig struct {
	// Web: "allow" = browsers open only Sites; "block" = everything opens except BlockSites
	// (and the built-in AI list when BlockAISites); "open" = no web limits.
	WebMode      string   `json:"web_mode"`
	Sites        []string `json:"sites"`
	BlockSites   []string `json:"block_sites"`
	BlockAISites bool     `json:"block_ai_sites"`
	// Browser
	NoExtensions bool `json:"no_extensions"`
	NoDevTools   bool `json:"no_devtools"`
	NoPrivate    bool `json:"no_private"` // no InPrivate / Incognito windows
	// Programs: closed when they start, and reported. Names are process names without ".exe".
	AppGroups []string `json:"app_groups"` // built-in lists: "ai", "chat", "remote"
	BlockApps []string `json:"block_apps"`
	// System
	NoCopilot bool `json:"no_copilot"` // Windows Copilot and the Edge sidebar
	BlockUSB  bool `json:"block_usb"`  // USB drives and other removable storage
	// Evidence
	WatchAIWords  bool     `json:"watch_ai_words"`    // built-in words in window titles (ChatGPT, Gemini…)
	Keywords      []string `json:"keywords"`          // more words to look for in window titles
	ShotOnViolate bool     `json:"shot_on_violation"` // a screenshot with every violation
	ShotEveryMin  int      `json:"shot_every_min"`    // a screenshot every N minutes; 0 = none
}

// ExamAISites are blocked in "block" mode when BlockAISites is set.
var ExamAISites = []string{
	"chatgpt.com", "openai.com", "claude.ai", "anthropic.com", "gemini.google.com", "aistudio.google.com", "notebooklm.google.com",
	"copilot.microsoft.com", "perplexity.ai", "poe.com", "deepseek.com", "grok.com", "x.ai", "meta.ai", "mistral.ai",
	"huggingface.co", "you.com", "phind.com", "character.ai", "quillbot.com", "blackbox.ai", "cursor.com", "kimi.com",
	"doubao.com", "tongyi.com", "qwen.ai", "opentyphoon.ai", "chatpdf.com", "wolframalpha.com", "symbolab.com", "photomath.com",
}

// ExamAppGroups are the built-in program lists (process names, lower case, no ".exe").
var ExamAppGroups = map[string][]string{
	"ai":     {"chatgpt", "claude", "copilot", "copilotapp", "copilotapphost", "m365copilot", "ollama", "ollama app", "lm studio", "lmstudio", "gpt4all", "jan", "msty", "cursor", "perplexity"},
	"chat":   {"line", "discord", "telegram", "whatsapp", "messenger", "slack", "ms-teams", "teams", "wechat", "skype", "zoom", "signal"},
	"remote": {"anydesk", "teamviewer", "rustdesk", "ultraviewer", "ultraviewer_desktop", "parsecd", "vncviewer", "tvnviewer", "mstsc", "quickassist", "remoting_host", "sragent", "dwagent"},
}

// ExamAIWords are looked for in window titles when WatchAIWords is set.
var ExamAIWords = []string{"chatgpt", "claude", "gemini", "copilot", "perplexity", "deepseek", "grok", "notebooklm", "poe.com"}

const (
	maxExamSites = 100
	maxExamApps  = 100
	maxExamWords = 50
)

// Normalize checks the configuration and puts it in one canonical form (lower case, no
// duplicates, sorted), so two equal set-ups compare equal as JSON.
func (c *ExamConfig) Normalize() error {
	switch c.WebMode {
	case "":
		c.WebMode = "open"
	case "allow", "block", "open":
	default:
		return fmt.Errorf("web_mode %q: use allow, block or open", c.WebMode)
	}
	var bad string
	if c.Sites, bad = NormalizeExamSites(c.Sites); bad != "" {
		return fmt.Errorf("not a site name: %s", bad)
	}
	if c.BlockSites, bad = NormalizeExamSites(c.BlockSites); bad != "" {
		return fmt.Errorf("not a site name: %s", bad)
	}
	if len(c.Sites) > maxExamSites || len(c.BlockSites) > maxExamSites {
		return fmt.Errorf("at most %d sites", maxExamSites)
	}
	switch c.WebMode {
	case "allow":
		if len(c.Sites) == 0 {
			return fmt.Errorf("allow mode needs at least one allowed site")
		}
		c.BlockSites, c.BlockAISites = []string{}, false
	case "block":
		if len(c.BlockSites) == 0 && !c.BlockAISites {
			return fmt.Errorf("block mode needs at least one blocked site, or the AI site list")
		}
		c.Sites = []string{}
	default:
		c.Sites, c.BlockSites, c.BlockAISites = []string{}, []string{}, false
	}
	groups := []string{}
	for _, g := range c.AppGroups {
		g = strings.ToLower(strings.TrimSpace(g))
		if _, ok := ExamAppGroups[g]; !ok {
			return fmt.Errorf("unknown program group %q", g)
		}
		groups = append(groups, g)
	}
	c.AppGroups = uniqSorted(groups)
	apps := []string{}
	for _, a := range c.BlockApps {
		a = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(a)), ".exe")
		if a == "" {
			continue
		}
		if len(a) > 60 || strings.ContainsAny(a, `\/:*?"<>|'`+"`$") || hasControl(a) {
			return fmt.Errorf("not a program name: %s", a)
		}
		apps = append(apps, a)
	}
	if c.BlockApps = uniqSorted(apps); len(c.BlockApps) > maxExamApps {
		return fmt.Errorf("at most %d programs", maxExamApps)
	}
	words := []string{}
	for _, w := range c.Keywords {
		w = strings.ToLower(strings.TrimSpace(w))
		if w == "" {
			continue
		}
		if len([]rune(w)) < 3 || len(w) > 80 || hasControl(w) {
			return fmt.Errorf("a word to watch needs 3 to 80 characters: %s", w)
		}
		words = append(words, w)
	}
	if c.Keywords = uniqSorted(words); len(c.Keywords) > maxExamWords {
		return fmt.Errorf("at most %d words", maxExamWords)
	}
	if c.ShotEveryMin < 0 || c.ShotEveryMin > 120 {
		return fmt.Errorf("shot_every_min: 0 to 120")
	}
	return nil
}

// Restricts reports whether the configuration does anything at all.
func (c ExamConfig) Restricts() bool {
	return c.WebMode == "allow" || c.WebMode == "block" || c.NoExtensions || c.NoDevTools || c.NoPrivate || len(c.AppGroups) > 0 ||
		len(c.BlockApps) > 0 || c.NoCopilot || c.BlockUSB || c.Watches()
}

// Watches reports whether the machine needs the watcher that looks at windows and programs.
func (c ExamConfig) Watches() bool {
	return len(c.AppGroups) > 0 || len(c.BlockApps) > 0 || c.WatchAIWords || len(c.Keywords) > 0 || c.ShotEveryMin > 0
}

// Apps is the full list of programs to close: the chosen groups plus the admin's own names.
func (c ExamConfig) Apps() []string {
	all := append([]string{}, c.BlockApps...)
	for _, g := range c.AppGroups {
		all = append(all, ExamAppGroups[g]...)
	}
	return uniqSorted(all)
}

// Words is the full list of window-title words to watch.
func (c ExamConfig) Words() []string {
	all := append([]string{}, c.Keywords...)
	if c.WatchAIWords {
		all = append(all, ExamAIWords...)
	}
	return uniqSorted(all)
}

// Blocked is the full list of blocked sites in "block" mode.
func (c ExamConfig) Blocked() []string {
	all := append([]string{}, c.BlockSites...)
	if c.BlockAISites {
		all = append(all, ExamAISites...)
	}
	return uniqSorted(all)
}

func uniqSorted(in []string) []string {
	seen, out := map[string]bool{}, []string{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// NormalizeExamSites turns what an admin typed (one site per line, perhaps a full URL) into
// lower-case host names, sorted, without duplicates. It reports the first entry that is not
// a host name. The names go into a script on the machine, so the check is strict.
func NormalizeExamSites(in []string) ([]string, string) {
	out := []string{}
	for _, raw := range in {
		s := strings.ToLower(strings.TrimSpace(raw))
		if i := strings.Index(s, "://"); i >= 0 {
			s = s[i+3:]
		}
		if i := strings.IndexAny(s, "/?#"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimPrefix(strings.TrimPrefix(s, "*."), "www.")
		if s == "" {
			continue
		}
		if !validExamSite(s) {
			return nil, raw
		}
		out = append(out, s)
	}
	return uniqSorted(out), ""
}

func validExamSite(s string) bool {
	if len(s) > 253 || !strings.Contains(s, ".") || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") || strings.Contains(s, "..") {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}
