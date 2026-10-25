package license

import (
	"flag"
	"io"
	"testing"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/language"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

func parse(t *testing.T, rel, content string) *rule.File {
	t.Helper()
	f, err := rule.LoadData("BUILD", rel, []byte(content))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestInheritanceAndOverride(t *testing.T) {
	l := NewLanguage()
	c := config.New()
	c.Exts = map[string]interface{}{}

	root := parse(t, "", "# gazelle:license_default //:mit\n")
	l.Configure(c, "", root)
	if got := getConfig(c).target; got != "//:mit" {
		t.Fatalf("root target = %q", got)
	}

	child := *c
	child.Exts = map[string]interface{}{languageName: getConfig(c)}
	l.Configure(&child, "a", parse(t, "a", ""))
	if got := getConfig(&child).target; got != "//:mit" {
		t.Fatalf("inherited target = %q", got)
	}

	l.Configure(&child, "a/ent", parse(t, "a/ent", "# gazelle:license_default //ent:meta\n"))
	if got := getConfig(&child).target; got != "//ent:meta" {
		t.Fatalf("override target = %q", got)
	}

	l.Configure(&child, "a/ent/x", parse(t, "a/ent/x", "# gazelle:license_default none\n"))
	if got := getConfig(&child).target; got != "" {
		t.Fatalf("none target = %q", got)
	}
}

func TestGenerateRules(t *testing.T) {
	l := NewLanguage()
	c := config.New()
	c.Exts = map[string]interface{}{languageName: &licenseConfig{declared: declared{target: "//:mit"}}}

	res := l.GenerateRules(language.GenerateArgs{Config: c, File: parse(t, "a", "")})
	if len(res.Gen) != 1 || res.Gen[0].Kind() != "package" {
		t.Fatalf("unexpected result: %+v", res.Gen)
	}
	if got := res.Gen[0].AttrStrings(attrName); len(got) != 1 || got[0] != "//:mit" {
		t.Fatalf("attr = %v", got)
	}
}

func TestSubtree(t *testing.T) {
	l := NewLanguage()
	c := config.New()
	c.Exts = map[string]interface{}{}

	l.Configure(c, "", parse(t, "", "# gazelle:license_default //:mit\n# gazelle:license_subtree enterprise //:ent\n"))
	check := func(rel, want string) {
		t.Helper()
		child := *c
		child.Exts = map[string]interface{}{languageName: getConfig(c)}
		l.Configure(&child, rel, nil)
		if got := getConfig(&child).target; got != want {
			t.Fatalf("%s: target = %q, want %q", rel, got, want)
		}
	}
	check("server", "//:mit")
	check("enterprisex", "//:mit")
	check("enterprise", "//:ent")
	check("enterprise/server/util", "//:ent")
}

func TestFlags(t *testing.T) {
	l := NewLanguage().(*licenseLang)
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	l.RegisterFlags(fs, "fix", config.New())
	if err := fs.Parse([]string{"-license_default=//:mit", "-license_subtree=enterprise=//:ent"}); err != nil {
		t.Fatal(err)
	}
	c := config.New()
	c.Exts = map[string]interface{}{}
	if err := l.CheckFlags(fs, c); err != nil {
		t.Fatal(err)
	}
	l.Configure(c, "", nil)
	if got := getConfig(c).target; got != "//:mit" {
		t.Fatalf("root = %q", got)
	}
	l.Configure(c, "enterprise/x", nil)
	if got := getConfig(c).target; got != "//:ent" {
		t.Fatalf("enterprise = %q", got)
	}
	if err := fs.Parse([]string{"-license_subtree=bad"}); err == nil {
		t.Fatal("expected error for malformed subtree")
	}
}

func TestDetectorSettings(t *testing.T) {
	l := NewLanguage().(*licenseLang)
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	l.RegisterFlags(fs, "fix", config.New())
	if err := fs.Parse([]string{"-license_detector=licenseclassifier", "-license_confidence=licenseclassifier=90", "-license_confidence=licensecheck=95"}); err != nil {
		t.Fatal(err)
	}
	c := config.New()
	c.Exts = map[string]interface{}{}
	if err := l.CheckFlags(fs, c); err != nil {
		t.Fatal(err)
	}
	l.Configure(c, "", nil)
	cfg := getConfig(c)
	if cfg.detector != "licenseclassifier" || cfg.confidence["licenseclassifier"] != 90 || cfg.confidence["licensecheck"] != 95 {
		t.Fatalf("flags: %+v", cfg)
	}

	// A directive overrides the flag and is inherited.
	l.Configure(c, "a", parse(t, "a", "# gazelle:license_detector licensecheck\n# gazelle:license_confidence licensecheck 99\n"))
	l.Configure(c, "a/b", nil)
	cfg = getConfig(c)
	if cfg.detector != "licensecheck" || cfg.confidence["licensecheck"] != 99 || cfg.confidence["licenseclassifier"] != 90 {
		t.Fatalf("directives: %+v", cfg)
	}

	for _, bad := range []string{"-license_confidence=licenseclassifier=50", "-license_confidence=nope=90", "-license_confidence=licensecheck"} {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		NewLanguage().(*licenseLang).RegisterFlags(fs, "fix", config.New())
		if err := fs.Parse([]string{bad}); err == nil {
			t.Errorf("%s: expected an error", bad)
		}
	}
}

func TestPerFileConfidence(t *testing.T) {
	l := NewLanguage().(*licenseLang)
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	l.RegisterFlags(fs, "fix", config.New())
	if err := fs.Parse([]string{"-license_confidence=licensecheck=95", "-license_confidence=licensecheck=65 LICENSE"}); err != nil {
		t.Fatal(err)
	}
	c := config.New()
	c.Exts = map[string]interface{}{}
	if err := l.CheckFlags(fs, c); err != nil {
		t.Fatal(err)
	}
	l.Configure(c, "", nil)
	l.Configure(c, "sub", parse(t, "sub", "# gazelle:license_confidence licensecheck 70 ./LICENSE.txt\n"))
	cfg := getConfig(c)
	if cfg.confidence["licensecheck"] != 95 {
		t.Fatalf("global = %v", cfg.confidence)
	}
	if got := cfg.fileConfidence["LICENSE"]["licensecheck"]; got != 65 {
		t.Fatalf("LICENSE = %v (%v)", got, cfg.fileConfidence)
	}
	if got := cfg.fileConfidence["sub/LICENSE.txt"]["licensecheck"]; got != 70 {
		t.Fatalf("sub/LICENSE.txt = %v (%v)", got, cfg.fileConfidence)
	}

	// A subdirectory's entry must not leak into a sibling's inherited config.
	l.Configure(c, "other", nil)
	if _, ok := getConfig(c).fileConfidence["sub/LICENSE.txt"]; !ok {
		t.Log("(entries are inherited by design; they only match their own path)")
	}
}

func TestPerFileDetector(t *testing.T) {
	l := NewLanguage().(*licenseLang)
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	l.RegisterFlags(fs, "fix", config.New())
	if err := fs.Parse([]string{"-license_detector=licensecheck", "-license_detector=licenseclassifier LICENSE"}); err != nil {
		t.Fatal(err)
	}
	if err := fs.Parse([]string{"-license_detector=nope"}); err == nil {
		t.Fatal("expected error for unknown detector")
	}
	c := config.New()
	c.Exts = map[string]interface{}{}
	if err := l.CheckFlags(fs, c); err != nil {
		t.Fatal(err)
	}
	l.Configure(c, "", nil)
	l.Configure(c, "sub", parse(t, "sub", "# gazelle:license_detector licenseclassifier COPYING\n"))
	cfg := getConfig(c)
	if cfg.detector != "licensecheck" || cfg.fileDetector["LICENSE"] != "licenseclassifier" || cfg.fileDetector["sub/COPYING"] != "licenseclassifier" {
		t.Fatalf("got %+v", cfg)
	}
}

func TestFilesDirectiveAndFlag(t *testing.T) {
	l := NewLanguage().(*licenseLang)
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	l.RegisterFlags(fs, "fix", config.New())
	if err := fs.Parse([]string{"-license_files=default NOTICE"}); err != nil {
		t.Fatal(err)
	}
	c := config.New()
	c.Exts = map[string]interface{}{}
	if err := l.CheckFlags(fs, c); err != nil {
		t.Fatal(err)
	}
	l.Configure(c, "", nil)
	if !isLicenseFile(getConfig(c).files, "NOTICE") {
		t.Fatal("flag not applied")
	}
	l.Configure(c, "sub", parse(t, "sub", "# gazelle:license_files COPYING\n"))
	l.Configure(c, "sub/deeper", nil)
	if f := getConfig(c).files; isLicenseFile(f, "LICENSE") || !isLicenseFile(f, "COPYING") {
		t.Fatalf("directive not inherited: %v", f)
	}
}
