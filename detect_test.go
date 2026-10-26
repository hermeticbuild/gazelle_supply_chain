package license

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bazelbuild/bazel-gazelle/config"
)

const mitText = `MIT License

Copyright (c) 2024 Jane Doe and contributors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
`

func TestDetectIgnoresCopyrightHolder(t *testing.T) {
	for _, holder := range []string{"Jane Doe", "ACME Corp, Inc.", "2010-2025 Someone Else <x@y.z>"} {
		text := strings.Replace(mitText, "2024 Jane Doe and contributors", holder, 1)
		d, err := Detect(DetectorLicenseCheck, []byte(text), 0)
		if err != nil {
			t.Fatal(err)
		}
		if !d.Standard || len(d.IDs) != 1 || d.IDs[0] != "MIT" {
			t.Errorf("%q: got %+v", holder, d)
		}
	}
}

func TestDetectRejectsCustomText(t *testing.T) {
	d, err := Detect(DetectorLicenseCheck, []byte("This software may only be used on Tuesdays by employees of Initech.\n"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if d.Standard || len(d.IDs) != 0 {
		t.Fatalf("got %+v", d)
	}
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckDir(t *testing.T) {
	root := t.TempDir()
	write(t, root, "LICENSE", mitText)
	write(t, root, "custom/LICENSE", "Do what Initech says.\n")
	write(t, root, "plain/README", "no license here\n")

	mit := declared{target: "//:mit", spdx: "MIT"}
	cases := []struct {
		name string
		rel  string
		cfg  licenseConfig
		want string
	}{
		{"declared and matching", "", licenseConfig{declared: mit, explicit: true}, ""},
		{"no license file", "plain", licenseConfig{}, ""},
		{"undeclared", "", licenseConfig{}, "no license_default or license_subtree"},
		{"inherited default is not a declaration", "", licenseConfig{declared: mit}, "no license_default or license_subtree"},
		{"nonstandard text", "custom", licenseConfig{declared: mit, explicit: true}, "does not match a standard license"},
		{"declared another license", "", licenseConfig{declared: declared{target: "//:apache", spdx: "Apache-2.0"}, explicit: true}, "contains MIT"},
		{"declaration lacks spdx id", "", licenseConfig{declared: declared{target: "//:mit"}, explicit: true}, "without an SPDX id"},
	}
	custom := licenseConfig{declared: declared{target: "//:ent", spdx: "LicenseRef-Ent"}, explicit: true}
	if err := checkDir(filepath.Join(root, "custom"), "custom", &custom); err != nil {
		t.Errorf("custom LicenseRef declaration: unexpected error %v", err)
	}
	custom.explicit = false
	if err := checkDir(filepath.Join(root, "custom"), "custom", &custom); err == nil {
		t.Error("custom LicenseRef declaration must still be explicit")
	}
	for _, tc := range cases {
		err := checkDir(filepath.Join(root, tc.rel), tc.rel, &tc.cfg)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: unexpected error %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: error %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestStrictSubdirNeedsSubtree(t *testing.T) {
	root := t.TempDir()
	write(t, root, "LICENSE", mitText)
	write(t, root, "ent/LICENSE", mitText)

	l := NewLanguage()
	c := config.New()
	c.RepoRoot = root
	c.Exts = map[string]interface{}{}
	l.Configure(c, "", parse(t, "", "# gazelle:license_strict true\n# gazelle:license_default //:mit MIT\n# gazelle:license_subtree ent //:ent MIT\n"))
	l.Configure(c, "ent", nil)
	if cfg := getConfig(c); !cfg.explicit || cfg.spdx != "MIT" {
		t.Fatalf("ent config = %+v", cfg)
	}
	// Without the subtree, the root default alone must not cover ent/LICENSE.
	cfg := licenseConfig{declared: declared{target: "//:mit", spdx: "MIT"}, strict: strictFull}
	if err := checkDir(filepath.Join(root, "ent"), "ent", &cfg); err == nil {
		t.Fatal("expected an error for an undeclared subdirectory license")
	}
}

func TestParseDeclared(t *testing.T) {
	if d := parseDeclared("//:mit MIT"); d.target != "//:mit" || d.spdx != "MIT" {
		t.Fatalf("got %+v", d)
	}
	if d := parseDeclared("none"); d != (declared{}) {
		t.Fatalf("got %+v", d)
	}
}

func TestDetectLicenseClassifier(t *testing.T) {
	text := strings.Replace(mitText, "2024 Jane Doe and contributors", "ACME Corp", 1)
	d, err := Detect(DetectorLicenseClassifier, []byte(text), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Standard || len(d.IDs) != 1 || d.IDs[0] != "MIT" {
		t.Fatalf("got %+v", d)
	}

	// A standard license followed by custom terms is not a standard file.
	mixed := text + "\n" + strings.Repeat("Enterprise features may only be used with a paid subscription from Initech.\n", 12)
	d, err = Detect(DetectorLicenseClassifier, []byte(mixed), 0)
	if err != nil {
		t.Fatal(err)
	}
	if d.Standard {
		t.Fatalf("mixed file reported standard: %+v", d)
	}

	d, err = Detect(DetectorLicenseClassifier, []byte("This software may only be used on Tuesdays by employees of Initech.\n"), 0)
	if err != nil || d.Standard || len(d.IDs) != 0 {
		t.Fatalf("got %+v, %v", d, err)
	}
}

func TestDetectConfidenceLimits(t *testing.T) {
	if _, err := Detect(DetectorLicenseClassifier, []byte(mitText), 50); err == nil {
		t.Fatal("licenseclassifier below its floor should be rejected")
	}
	if _, err := Detect("nope", []byte(mitText), 0); err == nil {
		t.Fatal("unknown detector should be rejected")
	}
	// A bar above what the file reaches turns a standard match into a miss.
	d, err := Detect(DetectorLicenseCheck, []byte(mitText), 100)
	if err != nil {
		t.Fatal(err)
	}
	if d.Percent >= 100 && !d.Standard {
		t.Fatalf("got %+v", d)
	}
}

func TestCheckDirPerFileConfidence(t *testing.T) {
	root := t.TempDir()
	// Roughly half MIT, half custom terms.
	write(t, root, "LICENSE", mitText+"\n"+strings.Repeat("Enterprise features need a paid subscription from Initech.\n", 8))
	cfg := licenseConfig{declared: declared{target: "//:mit", spdx: "MIT"}, explicit: true}
	if err := checkDir(root, "", &cfg); err == nil {
		t.Fatal("expected the default confidence to reject a mixed file")
	}
	cfg.fileConfidence = map[string]map[string]float64{"LICENSE": {DetectorLicenseCheck: 50}}
	if err := checkDir(root, "", &cfg); err != nil {
		t.Fatalf("per-file override should accept it: %v", err)
	}
	// An override for another file must not apply.
	cfg.fileConfidence = map[string]map[string]float64{"OTHER": {DetectorLicenseCheck: 50}}
	if err := checkDir(root, "", &cfg); err == nil {
		t.Fatal("override for another file must not apply")
	}
}

func TestCheckDirPerFileDetector(t *testing.T) {
	root := t.TempDir()
	write(t, root, "LICENSE", mitText)
	write(t, root, "COPYING", mitText)
	cfg := licenseConfig{
		declared:     declared{target: "//:mit", spdx: "MIT"},
		explicit:     true,
		fileDetector: map[string]string{"COPYING": DetectorLicenseClassifier},
		// Reachable only by licensecheck: the classifier floor rejects 50.
		fileConfidence: map[string]map[string]float64{"LICENSE": {DetectorLicenseCheck: 50}},
	}
	if err := checkDir(root, "", &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.fileConfidence = map[string]map[string]float64{"COPYING": {DetectorLicenseCheck: 50}}
	if err := checkDir(root, "", &cfg); err != nil {
		t.Fatalf("confidence for a detector the file doesn't use must be ignored: %v", err)
	}
	cfg.fileDetector = map[string]string{"COPYING": DetectorLicenseClassifier}
	cfg.fileConfidence = map[string]map[string]float64{"COPYING": {DetectorLicenseClassifier: 50}}
	if err := checkDir(root, "", &cfg); err == nil {
		t.Fatal("classifier confidence below its floor should error")
	}
}

func TestLicenseFileSelection(t *testing.T) {
	for _, name := range []string{"LICENSE", "license", "License.md", "LICENSE.rst", "LICENCE", "COPYING", "COPYING.txt", "UNLICENSE"} {
		if !isLicenseFile(nil, name) {
			t.Errorf("%s should be scanned by default", name)
		}
	}
	for _, name := range []string{"NOTICE", "PATENTS", "LICENSE-MIT", "license_test.go", "README.md"} {
		if isLicenseFile(nil, name) {
			t.Errorf("%s should not be scanned by default", name)
		}
	}
	files := parseFiles("default, NOTICE LICENSE-*")
	for _, name := range []string{"LICENSE", "notice", "LICENSE-MIT"} {
		if !isLicenseFile(files, name) {
			t.Errorf("%s should be scanned with %v", name, files)
		}
	}
	if isLicenseFile(parseFiles("COPYING"), "LICENSE") {
		t.Error("an explicit list replaces the defaults")
	}

	root := t.TempDir()
	write(t, root, "LICENSE", mitText)
	write(t, root, "NOTICE", "Custom attribution text.\n")
	cfg := licenseConfig{declared: declared{target: "//:mit", spdx: "MIT"}, explicit: true}
	if err := checkDir(root, "", &cfg); err != nil {
		t.Fatalf("NOTICE must not be scanned by default: %v", err)
	}
	cfg.files = parseFiles("default NOTICE")
	if err := checkDir(root, "", &cfg); err == nil {
		t.Fatal("NOTICE should be scanned once listed")
	}
}
