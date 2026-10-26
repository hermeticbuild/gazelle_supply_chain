package license

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/language"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

const metaLabel = "//_gazelle_generated:package_metadata"

func symlink(t *testing.T, root, target, link string) {
	t.Helper()
	l := filepath.Join(root, filepath.FromSlash(link))
	if err := os.MkdirAll(filepath.Dir(l), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, l); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func byName(rules []*rule.Rule, kind, name string) *rule.Rule {
	for _, r := range rules {
		if r.Kind() == kind && r.Name() == name {
			return r
		}
	}
	return nil
}

func TestDetectSpecs(t *testing.T) {
	root := t.TempDir()
	write(t, root, "LICENSE", mitText)
	write(t, root, "LICENSE.txt", "Do what Initech says.\n")
	cfg := &licenseConfig{declared: declared{target: metaLabel, spdx: detectValue}}

	d := buildDeclaration(root, "", cfg)
	if d.kindID != "" {
		t.Errorf("unexpected kind id %q", d.kindID)
	}
	// LICENSE.txt matches no standard license and is skipped.
	want := licenseSpec{name: "gazelle_generated_license", file: "LICENSE", kind: "@package_metadata//licenses/spdx:MIT"}
	if len(d.specs) != 1 || d.specs[0] != want {
		t.Errorf("specs = %+v", d.specs)
	}
}

func TestStaticDeclaration(t *testing.T) {
	root := t.TempDir()
	write(t, root, "LICENSE", "x")
	write(t, root, "LICENSE.txt", "x")

	d := buildDeclaration(root, "", &licenseConfig{declared: declared{target: metaLabel, spdx: "MIT"}})
	if d.kindID != "" || len(d.specs) != 2 || d.specs[0].name != "gazelle_generated_license" || d.specs[1].name != "gazelle_generated_license_txt" {
		t.Errorf("declaration = %+v", d)
	}

	d = buildDeclaration(root, "", &licenseConfig{declared: declared{target: metaLabel, spdx: "LicenseRef-Acme"}})
	if d.kindID != "LicenseRef-Acme" || d.specs[0].kind != ":gazelle_generated_kind" {
		t.Errorf("custom declaration = %+v", d)
	}

	d = buildDeclaration(t.TempDir(), "", &licenseConfig{declared: declared{target: metaLabel, spdx: "MIT"}})
	if len(d.specs) != 1 || d.specs[0].file != "" {
		t.Errorf("no license file: %+v", d.specs)
	}
}

func TestFindLicenseFilesFollowsSymlinks(t *testing.T) {
	root := t.TempDir()
	write(t, root, "LICENSE.txt", mitText)
	write(t, root, "server/enterprise/LICENSE", "enterprise")
	symlink(t, root, "server/enterprise/LICENSE", "LICENSE.enterprise")
	cfg := &licenseConfig{files: parseFiles("default LICENSE.enterprise")}

	got := findLicenseFiles(root, "", cfg)
	want := []licenseFile{
		{name: "LICENSE.enterprise", pkg: "server/enterprise", base: "LICENSE"},
		{name: "LICENSE.txt", pkg: "", base: "LICENSE.txt"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("files = %+v, want %+v", got, want)
	}
}

func TestSymlinkOutsideRepositoryIsSkipped(t *testing.T) {
	outside := t.TempDir()
	write(t, outside, "LICENSE", mitText)
	root := t.TempDir()
	symlink(t, root, filepath.Join(outside, "LICENSE"), "LICENSE")

	if got := findLicenseFiles(root, "", &licenseConfig{}); len(got) != 0 {
		t.Errorf("files = %+v", got)
	}
}

func TestBrokenSymlinkIsIgnored(t *testing.T) {
	root := t.TempDir()
	symlink(t, root, "missing", "LICENSE")
	if got := findLicenseFiles(root, "", &licenseConfig{}); len(got) != 0 {
		t.Errorf("files = %+v", got)
	}
}

// A repository whose enterprise license is a symlink at the root to a file in
// a subdirectory: the license() goes next to the real file, and the root
// package_metadata still points at it.
func TestGenerateWithSymlinkedLicense(t *testing.T) {
	root := t.TempDir()
	write(t, root, "LICENSE.txt", mitText)
	write(t, root, "server/enterprise/LICENSE", "enterprise terms")
	symlink(t, root, "server/enterprise/LICENSE", "LICENSE.enterprise")

	l := NewLanguage()
	c := config.New()
	c.RepoRoot = root
	c.Exts = map[string]interface{}{}
	l.Configure(c, "", parse(t, "", "# gazelle:license_files default LICENSE.enterprise\n# gazelle:license_default "+metaLabel+" LicenseRef-Acme\n"))

	configure := func(rel, build string) *config.Config {
		child := *c
		child.Exts = map[string]interface{}{languageName: getConfig(c)}
		l.Configure(&child, rel, parse(t, rel, build))
		return &child
	}

	// The declaration's own directory holds no license() for the symlink.
	rootRes := l.GenerateRules(language.GenerateArgs{Config: c, Rel: "", File: parse(t, "", "")})
	if byName(rootRes.Gen, "license", "gazelle_generated_license_txt") == nil {
		t.Errorf("root lacks the license() of LICENSE.txt: %v", rootRes.Gen)
	}
	if byName(rootRes.Gen, "license", "gazelle_generated_license_enterprise") != nil {
		t.Errorf("root must not hold the license() of the symlink")
	}

	// The real file's directory does, even though it declares nothing itself.
	entCfg := configure("server/enterprise", "# gazelle:license_default none\n")
	entRes := l.GenerateRules(language.GenerateArgs{Config: entCfg, Rel: "server/enterprise", File: parse(t, "server/enterprise", "")})
	lic := byName(entRes.Gen, "license", "gazelle_generated_license_enterprise")
	if lic == nil {
		t.Fatalf("server/enterprise lacks the license(): %v", entRes.Gen)
	}
	if got := lic.AttrString("text"); got != "LICENSE" {
		t.Errorf("text = %q, want LICENSE", got)
	}
	if byName(entRes.Gen, "license_kind", "gazelle_generated_kind") == nil {
		t.Errorf("the custom license_kind must sit next to the license()")
	}

	// The package_metadata applies to the whole repository and lists both.
	genCfg := configure("_gazelle_generated", "")
	existing := parse(t, "_gazelle_generated", `package_metadata(name = "package_metadata", purl = "pkg:golang/x@v1")`)
	genRes := l.GenerateRules(language.GenerateArgs{Config: genCfg, Rel: "_gazelle_generated", File: existing})
	m := byName(genRes.Gen, "package_metadata", "package_metadata")
	if m == nil {
		t.Fatalf("no package_metadata: %v", genRes.Gen)
	}
	attrs := m.AttrStrings("attributes")
	if len(attrs) != 2 || attrs[0] != "//server/enterprise:gazelle_generated_license_enterprise" || attrs[1] != "//:gazelle_generated_license_txt" {
		t.Errorf("attributes = %v", attrs)
	}
	if m.AttrString("purl") != "" {
		t.Errorf("an existing purl must be kept, not rewritten: %q", m.AttrString("purl"))
	}
}

func TestStrictFollowsSymlinks(t *testing.T) {
	root := t.TempDir()
	write(t, root, "real/MIT", mitText)
	symlink(t, root, "real/MIT", "LICENSE")

	ok := licenseConfig{declared: declared{target: "//:mit", spdx: "MIT"}, explicit: true}
	if err := checkDir(root, "", &ok); err != nil {
		t.Errorf("matching symlink: %v", err)
	}
	bad := licenseConfig{declared: declared{target: "//:a", spdx: "Apache-2.0"}, explicit: true}
	if err := checkDir(root, "", &bad); err == nil {
		t.Error("a symlinked MIT file must not satisfy an Apache-2.0 declaration")
	}
}

func TestDetectFollowsSymlinks(t *testing.T) {
	root := t.TempDir()
	write(t, root, "real/MIT", mitText)
	symlink(t, root, "real/MIT", "LICENSE")

	d := buildDeclaration(root, "", &licenseConfig{declared: declared{target: metaLabel, spdx: detectValue}})
	want := licenseSpec{name: "gazelle_generated_license", pkg: "real", file: "MIT", kind: "@package_metadata//licenses/spdx:MIT"}
	if len(d.specs) != 1 || d.specs[0] != want {
		t.Errorf("specs = %+v, want %+v", d.specs, want)
	}
}

func TestIgnoreSymlinks(t *testing.T) {
	root := t.TempDir()
	write(t, root, "LICENSE.txt", mitText)
	write(t, root, "server/enterprise/LICENSE", "enterprise terms")
	symlink(t, root, "server/enterprise/LICENSE", "LICENSE.enterprise")

	l := NewLanguage()
	c := config.New()
	c.RepoRoot = root
	c.Exts = map[string]interface{}{}
	l.Configure(c, "", parse(t, "", "# gazelle:license_files default LICENSE.enterprise\n# gazelle:license_follow_symlinks false\n# gazelle:license_default "+metaLabel+" MIT\n"))

	got := findLicenseFiles(root, "", getConfig(c))
	if len(got) != 1 || got[0].name != "LICENSE.txt" {
		t.Errorf("files = %+v", got)
	}
	res := l.GenerateRules(language.GenerateArgs{Config: c, Rel: "server/enterprise", File: parse(t, "server/enterprise", "")})
	if byName(res.Gen, "license", "gazelle_generated_license_enterprise") != nil {
		t.Errorf("an ignored symlink must not produce a license()")
	}
}

func TestIgnoreSymlinksFlag(t *testing.T) {
	l := NewLanguage().(*licenseLang)
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	l.RegisterFlags(fs, "fix", config.New())
	if err := fs.Parse([]string{"-license_follow_symlinks=false"}); err != nil {
		t.Fatal(err)
	}
	c := config.New()
	if err := l.CheckFlags(fs, c); err != nil {
		t.Fatal(err)
	}
	if !getConfig(c).noSymlinks {
		t.Error("flag not applied")
	}
}

func TestStrictDetectFailsOnUnmatchedFile(t *testing.T) {
	root := t.TempDir()
	write(t, root, "LICENSE", "Do what Initech says.\n")
	cfg := &licenseConfig{declared: declared{target: metaLabel, spdx: detectValue}, explicit: true, strict: strictFull}
	if err := checkDir(root, "", cfg); err == nil {
		t.Error("strict mode must fail on a license file matching no standard license")
	}

	write(t, root, "LICENSE", mitText)
	if err := checkDir(root, "", cfg); err != nil {
		t.Errorf("a recognised license must pass: %v", err)
	}
}

func TestStrictDetectFailsWithoutLicenseFile(t *testing.T) {
	root := t.TempDir()
	write(t, root, "main.go", "package main\n")
	cfg := &licenseConfig{declared: declared{target: metaLabel, spdx: detectValue, owner: ""}, explicit: true, strict: strictFull}
	if err := checkDir(root, "", cfg); err == nil {
		t.Error("strict mode must fail when a detect declaration finds no license file")
	}
	// Directories below the declaration may have no license file.
	write(t, root, "sub/x.go", "package sub\n")
	if err := checkDir(filepath.Join(root, "sub"), "sub", cfg); err != nil {
		t.Errorf("subdirectory: %v", err)
	}
	// An explicit declaration of the lack of license passes.
	cfg.spdx = "LicenseRef-Unlicensed"
	if err := checkDir(root, "", cfg); err != nil {
		t.Errorf("explicit declaration: %v", err)
	}
}

func TestStrictFlag(t *testing.T) {
	for args, want := range map[string]strictness{
		"":                        strictFull,
		"-license_strict=false":   strictOff,
		"-license_strict=relaxed": strictRelaxed,
		"-license_strict":         strictFull,
		"-license_strict=true":    strictFull,
	} {
		l := NewLanguage().(*licenseLang)
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		l.RegisterFlags(fs, "fix", config.New())
		var in []string
		if args != "" {
			in = []string{args}
		}
		if err := fs.Parse(in); err != nil {
			t.Fatal(err)
		}
		c := config.New()
		if err := l.CheckFlags(fs, c); err != nil {
			t.Fatal(err)
		}
		if got := getConfig(c).strict; got != want {
			t.Errorf("%q: strict = %v, want %v", args, got, want)
		}
	}
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(&strings.Builder{})
	l := NewLanguage().(*licenseLang)
	l.RegisterFlags(fs, "fix", config.New())
	if err := fs.Parse([]string{"-license_strict=maybe"}); err == nil {
		t.Error("an unknown level must be rejected")
	}
}

func TestStrictDirective(t *testing.T) {
	for v, want := range map[string]strictness{"true": strictFull, "relaxed": strictRelaxed, "false": strictOff} {
		l := NewLanguage()
		c := config.New()
		c.Exts = map[string]interface{}{}
		l.Configure(c, "", parse(t, "", "# gazelle:license_strict "+v+"\n"))
		if got := getConfig(c).strict; got != want {
			t.Errorf("%s: strict = %v, want %v", v, got, want)
		}
	}
}

// Relaxed mode leaves out what it cannot determine, and still catches a
// declaration the license files contradict.
func TestRelaxedStrictness(t *testing.T) {
	declare := declared{target: metaLabel, spdx: detectValue, owner: ""}

	empty := t.TempDir()
	write(t, empty, "main.go", "package main\n")
	if err := checkDir(empty, "", &licenseConfig{declared: declare, explicit: true, strict: strictRelaxed}); err != nil {
		t.Errorf("no license file: %v", err)
	}

	odd := t.TempDir()
	write(t, odd, "LICENSE", "Do what Initech says.\n")
	if err := checkDir(odd, "", &licenseConfig{declared: declare, explicit: true, strict: strictRelaxed}); err != nil {
		t.Errorf("unmatched license file: %v", err)
	}
	if err := checkDir(odd, "", &licenseConfig{strict: strictRelaxed}); err != nil {
		t.Errorf("undeclared license file: %v", err)
	}
	if err := checkDir(odd, "", &licenseConfig{declared: declared{target: "//:a", spdx: "Apache-2.0"}, explicit: true, strict: strictRelaxed}); err != nil {
		t.Errorf("a file it could not classify is no contradiction: %v", err)
	}

	mit := t.TempDir()
	write(t, mit, "LICENSE", mitText)
	wrong := &licenseConfig{declared: declared{target: "//:a", spdx: "Apache-2.0"}, explicit: true, strict: strictRelaxed}
	if err := checkDir(mit, "", wrong); err == nil {
		t.Error("an Apache-2.0 declaration over an MIT file must fail even when relaxed")
	}
}

// With enforcement off, Configure doesn't check the license files at all (a
// failing check would exit the process).
func TestOffEnforcesNothing(t *testing.T) {
	root := t.TempDir()
	write(t, root, "LICENSE", mitText)
	l := NewLanguage()
	c := config.New()
	c.RepoRoot = root
	c.Exts = map[string]interface{}{}
	l.Configure(c, "", parse(t, "", "# gazelle:license_strict false\n# gazelle:license_default //:a Apache-2.0\n"))
}
