package contract

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestNormalizeExamSites(t *testing.T) {
	got, bad := NormalizeExamSites([]string{" https://Exam.Example.ac.th/login?x=1 ", "www.example.org", "*.example.org", "", "10.0.0.5"})
	if bad != "" || !reflect.DeepEqual(got, []string{"10.0.0.5", "exam.example.ac.th", "example.org"}) {
		t.Fatalf("got %v bad %q", got, bad)
	}
	for _, in := range []string{"exa mple.com", "example", "a'b.com", "$(x).com", "a..b", "ไทย.com", "a.com:8080"} {
		if _, bad := NormalizeExamSites([]string{in}); bad == "" {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestExamConfigNormalize(t *testing.T) {
	c := ExamConfig{WebMode: "allow", Sites: []string{"B.example.com", "a.example.com", "a.example.com"}, BlockSites: []string{"x.com"},
		AppGroups: []string{"Remote", "ai"}, BlockApps: []string{"Notepad.EXE", " calc "}, Keywords: []string{" Answer Key "}}
	if err := c.Normalize(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Sites, []string{"a.example.com", "b.example.com"}) || len(c.BlockSites) != 0 ||
		!reflect.DeepEqual(c.AppGroups, []string{"ai", "remote"}) || !reflect.DeepEqual(c.BlockApps, []string{"calc", "notepad"}) ||
		!reflect.DeepEqual(c.Keywords, []string{"answer key"}) {
		t.Fatalf("%+v", c)
	}
	a, _ := json.Marshal(c)
	if err := c.Normalize(); err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(c); string(a) != string(b) {
		t.Fatal("Normalize is not stable")
	}
	if !c.Watches() || !c.Restricts() || len(c.Apps()) <= 2 {
		t.Fatal("expected a watching, restricting config")
	}
	for name, bad := range map[string]ExamConfig{
		"mode":      {WebMode: "strict"},
		"no sites":  {WebMode: "allow"},
		"no blocks": {WebMode: "block"},
		"group":     {AppGroups: []string{"games"}},
		"app":       {BlockApps: []string{`a\b`}},
		"quote":     {BlockApps: []string{"a'b"}},
		"word":      {Keywords: []string{"ab"}},
		"minutes":   {ShotEveryMin: 500},
	} {
		if err := bad.Normalize(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	var zero ExamConfig
	if err := zero.Normalize(); err != nil || zero.Restricts() || zero.WebMode != "open" {
		t.Fatalf("zero config: %v %+v", err, zero)
	}
}
