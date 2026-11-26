package msi

import (
	"bytes"
	"strings"
	"testing"
)

func TestProductCodeStablePerVersion(t *testing.T) {
	a, b, c := ProductCode("0.2.0"), ProductCode("0.2.0"), ProductCode("0.3.0")
	if a != b || a == c || len(a) != 36 || a[14] != '5' {
		t.Fatalf("unexpected product codes %s %s %s", a, b, c)
	}
}

func TestTemplateEscapes(t *testing.T) {
	var buf bytes.Buffer
	err := tpl.Execute(&buf, map[string]string{"ProductCode": ProductCode("0.2.0"), "UpgradeCode": UpgradeCode,
		"Exe": "/x/a.exe", "Version": "0.2.0", "Server": "https://s", "Pin": "ab", "Token": `t"<&>`, "Site": "BKK & HQ"})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, `t"<&>`) || !strings.Contains(out, "t&quot;&lt;&amp;&gt;") || !strings.Contains(out, "BKK &amp; HQ") {
		t.Fatal("values must be XML-escaped")
	}
}
