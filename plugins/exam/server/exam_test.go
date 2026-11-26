package examserver

import (
	"encoding/json"
	"testing"

	"swim/plugins/exam/contract"
)

// The set-up the web page starts from must be valid once the admin adds a site, and must
// compare equal to itself after a round trip — reconcileExam relies on that.
func TestExamRecommended(t *testing.T) {
	c := examRecommended()
	if err := c.Normalize(); err == nil {
		t.Fatal("allow mode without a site must be refused")
	}
	c.Sites = []string{"https://Exam.Example.ac.th/start"}
	if err := c.Normalize(); err != nil {
		t.Fatal(err)
	}
	if !c.Restricts() || !c.Watches() {
		t.Fatal("the recommended set-up must restrict and watch")
	}
	a, _ := json.Marshal(c)
	var back contract.ExamConfig
	if err := json.Unmarshal(a, &back); err != nil {
		t.Fatal(err)
	}
	if err := back.Normalize(); err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(back); string(a) != string(b) {
		t.Fatalf("round trip changed the set-up:\n%s\n%s", a, b)
	}
}
