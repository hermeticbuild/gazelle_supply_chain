package license

import (
	"flag"
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
	c.Exts = map[string]interface{}{languageName: &licenseConfig{target: "//:mit"}}

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
