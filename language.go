// Package license implements a Gazelle extension that tags every Bazel
// package in a subtree with a default package_metadata target.
//
// Usage, in a BUILD file:
//
//	# gazelle:license_default //enterprise:package_metadata
//
// Every package at or below that directory which already has a BUILD file
// gets `package(default_applicable_licenses = ["//enterprise:package_metadata"])`.
// A nested `# gazelle:license_default //other:meta` overrides the parent, and
// `# gazelle:license_default none` stops tagging for the subtree.
//
// When BUILD files can't be edited (for example in an external repository
// that Gazelle generates), subtrees can be assigned from the root instead:
//
//	# gazelle:license_subtree enterprise //enterprise:package_metadata
//
// A subtree directive wins over license_default for that directory and below.
//
// The same settings can be given as flags, which is how to append tags to the
// existing BUILD files of a go_repository without rewriting its root BUILD file:
//
//	gazelle -lang=license -license_default=//:mit -license_subtree=enterprise=//ent:meta
//
// A target may be followed by the SPDX identifier it stands for, for example
// `# gazelle:license_default //:mit MIT`.
//
// Strict mode makes Gazelle fail instead of guessing about LICENSE files:
//
//	# gazelle:license_strict true
//
// In strict mode every LICENSE/COPYING file Gazelle finds must match a standard
// SPDX license text, must be covered by an explicit declaration (license_default
// at the repository root, license_subtree or a local license_default in a
// subdirectory), and a declaration that names an SPDX identifier must agree with
// what the file contains. Overriding a declared license is deliberately not
// possible here; that belongs to the concluded license mechanism in
// supply_chain_tools.
//
// The license files scanned are LICENSE, LICENCE, COPYING and UNLICENSE with
// the usual extensions (.txt, .md, .rst), matched case-insensitively. Name the
// files to scan yourself with license_files, where `default` stands for that
// list and names may be globs:
//
//	# gazelle:license_files default NOTICE LICENSE-*
//
// The detector that reads the license files and the confidence it needs are
// configurable, each on its own directive (or flag):
//
//	# gazelle:license_detector licenseclassifier
//	# gazelle:license_confidence licensecheck 95
//	# gazelle:license_confidence licenseclassifier 85
//	# gazelle:license_confidence licensecheck 65 LICENSE
//	# gazelle:license_detector licenseclassifier enterprise/LICENSE
//
// The forms ending in a file apply to that file only (a path relative to the
// BUILD file) and win over the global setting. A file's confidence is the one
// set for the detector that ends up reading it.
//
// licensecheck (the default, 90) uses the share of the file covered by SPDX
// texts; licenseclassifier (default and minimum 80) uses its match confidence.
//
// A declaration whose id starts with LicenseRef- names a custom license that
// has no SPDX text; its license files are not matched against the license list.
//
// Every directory Gazelle visits is tagged; whether new BUILD files may be
// created is left to Gazelle and the caller (for go_repository, its
// build_file_generation attribute).
package license

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/language"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

const (
	languageName = "license"
	directive    = "license_default"
	subtreeDir   = "license_subtree"
	strictDir    = "license_strict"
	filesDir     = "license_files"
	detectorDir  = "license_detector"
	confDir      = "license_confidence"

	customLicensePrefix = "LicenseRef-"
	noneValue           = "none"
	packageKind         = "package"
	attrName            = "default_applicable_licenses"
)

type licenseLang struct {
	language.BaseLang
	flagDefault  string
	flagSubtrees subtreeFlag
	flagStrict   bool
	flagFiles    string
	flagDetector detectorFlag
	flagConf     confFlag
}

// detectorFlag collects repeated -license_detector="name [file]" values.
type detectorFlag []string

func (s *detectorFlag) String() string { return fmt.Sprint(*s) }

func (s *detectorFlag) Set(v string) error {
	if _, _, err := parseDetector(v); err != nil {
		return err
	}
	*s = append(*s, v)
	return nil
}

// parseDetector reads "name [file]".
func parseDetector(v string) (name, file string, err error) {
	fields := strings.Fields(v)
	if len(fields) == 0 || len(fields) > 2 {
		return "", "", fmt.Errorf("want `name [file]`, got %q", v)
	}
	if !validDetector(fields[0]) {
		return "", "", fmt.Errorf("unknown license detector %q", fields[0])
	}
	if len(fields) == 2 {
		file = fields[1]
	}
	return fields[0], file, nil
}

// confFlag collects repeated -license_confidence="detector=percent [file]" values.
type confFlag []string

func (s *confFlag) String() string { return fmt.Sprint(*s) }

func (s *confFlag) Set(v string) error {
	if _, _, _, err := parseConfFlag(v); err != nil {
		return err
	}
	*s = append(*s, v)
	return nil
}

// parseConfFlag reads "detector=percent [file]".
func parseConfFlag(v string) (detector string, percent float64, file string, err error) {
	fields := strings.Fields(v)
	if len(fields) == 0 || len(fields) > 2 {
		return "", 0, "", fmt.Errorf("want detector=percent [file], got %q", v)
	}
	detector, pct, ok := strings.Cut(fields[0], "=")
	if !ok {
		return "", 0, "", fmt.Errorf("want detector=percent [file], got %q", v)
	}
	percent, err = parseConfidence(detector, pct)
	if len(fields) == 2 {
		file = fields[1]
	}
	return detector, percent, file, err
}

// parseConfidence validates "<percent>" for a detector.
func parseConfidence(detector, percent string) (float64, error) {
	f, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(percent), "%"), 64)
	if err != nil {
		return 0, fmt.Errorf("bad license confidence %q: %v", percent, err)
	}
	return f, validateConfidence(detector, f)
}

// subtreeFlag collects repeated -license_subtree=dir=label values.
type subtreeFlag []string

func (s *subtreeFlag) String() string { return fmt.Sprint(*s) }

func (s *subtreeFlag) Set(v string) error {
	if dir, rest, ok := strings.Cut(v, "="); !ok || dir == "" || len(strings.Fields(rest)) == 0 {
		return fmt.Errorf("want dir=label [spdx], got %q", v)
	}
	*s = append(*s, v)
	return nil
}

func (l *licenseLang) RegisterFlags(fs *flag.FlagSet, cmd string, c *config.Config) {
	fs.StringVar(&l.flagDefault, "license_default", "", "default license target for every package, optionally followed by its SPDX id")
	fs.Var(&l.flagSubtrees, "license_subtree", "dir=label [spdx]: license target for a directory and below (repeatable)")
	fs.Var(&l.flagDetector, "license_detector", "name [file]: license detector, licensecheck (default) or licenseclassifier, for everything or for one file (repeatable)")
	fs.Var(&l.flagConf, "license_confidence", "detector=percent: minimum confidence for a detector (repeatable)")
	fs.StringVar(&l.flagFiles, "license_files", "", "license file names to scan, space or comma separated; `default` stands for the standard names")
	fs.BoolVar(&l.flagStrict, "license_strict", false, "fail on unmatched, undeclared or mismatching LICENSE files")
}

func (l *licenseLang) CheckFlags(fs *flag.FlagSet, c *config.Config) error {
	cfg := &licenseConfig{strict: l.flagStrict}
	if l.flagFiles != "" {
		cfg.files = parseFiles(l.flagFiles)
	}
	for _, v := range l.flagDetector {
		name, file, err := parseDetector(v)
		if err != nil {
			return err
		}
		cfg.setDetector(name, file, "")
	}
	for _, v := range l.flagConf {
		name, pct, file, err := parseConfFlag(v)
		if err != nil {
			return err
		}
		cfg.setConfidence(name, pct, file, "")
	}
	cfg.declared = parseDeclared(l.flagDefault)
	for _, v := range l.flagSubtrees {
		dir, rest, _ := strings.Cut(v, "=")
		if cfg.subtrees == nil {
			cfg.subtrees = map[string]declared{}
		}
		cfg.subtrees[path.Clean(dir)] = parseDeclared(rest)
	}
	c.Exts[languageName] = cfg
	return nil
}

// NewLanguage is the entry point used by gazelle_binary.
func NewLanguage() language.Language {
	return &licenseLang{}
}

func (*licenseLang) Name() string { return languageName }

func (*licenseLang) KnownDirectives() []string {
	return []string{directive, subtreeDir, strictDir, detectorDir, confDir, filesDir}
}

// declared is a license target and, optionally, the SPDX id it stands for.
type declared struct {
	target string
	spdx   string
}

// parseDeclared reads "label [spdx]"; "none" or an empty value clears it.
func parseDeclared(v string) declared {
	f := strings.Fields(v)
	if len(f) == 0 || f[0] == noneValue {
		return declared{}
	}
	d := declared{target: f[0]}
	if len(f) > 1 {
		d.spdx = f[1]
	}
	return d
}

type licenseConfig struct {
	// declared is the license applied as the default; an empty target means disabled.
	declared
	// subtrees maps a repo-relative directory to the license declared for it.
	subtrees map[string]declared
	// strict turns unmatched, undeclared or mismatching LICENSE files into errors.
	strict bool
	// detector selects the license detector; empty means licensecheck.
	detector string
	// files are the names (globs, case-insensitive) of the license files to
	// scan; nil selects defaultLicenseFiles.
	files []string
	// fileDetector overrides detector for one file, keyed by repo-relative path.
	fileDetector map[string]string
	// confidence holds the minimum percentage chosen per detector.
	confidence map[string]float64
	// fileConfidence overrides confidence for one file, keyed by its
	// repo-relative path and then by detector.
	fileConfidence map[string]map[string]float64
	// explicit is set when this directory itself is covered by a declaration:
	// a subtree entry, or a license_default written in its own BUILD file.
	// It is not inherited.
	explicit bool
}

// setDetector selects the detector globally or, when file is set, for that
// file only (file is relative to rel).
func (cfg *licenseConfig) setDetector(name, file, rel string) {
	if file == "" {
		cfg.detector = name
		return
	}
	files := make(map[string]string, len(cfg.fileDetector)+1)
	for k, v := range cfg.fileDetector {
		files[k] = v
	}
	files[path.Clean(path.Join(rel, file))] = name
	cfg.fileDetector = files
}

// setConfidence records a minimum for a detector, globally or, when file is
// set, for that file only (file is relative to rel). It copies the maps first
// because configs are shared with parent directories.
func (cfg *licenseConfig) setConfidence(detector string, percent float64, file, rel string) {
	if file == "" {
		cfg.confidence = withConfidence(cfg.confidence, detector, percent)
		return
	}
	key := path.Clean(path.Join(rel, file))
	files := make(map[string]map[string]float64, len(cfg.fileConfidence)+1)
	for k, v := range cfg.fileConfidence {
		files[k] = v
	}
	files[key] = withConfidence(files[key], detector, percent)
	cfg.fileConfidence = files
}

func withConfidence(m map[string]float64, name string, v float64) map[string]float64 {
	out := make(map[string]float64, len(m)+1)
	for k, x := range m {
		out[k] = x
	}
	out[name] = v
	return out
}

func getConfig(c *config.Config) *licenseConfig {
	if cfg, ok := c.Exts[languageName].(*licenseConfig); ok {
		return cfg
	}
	return &licenseConfig{}
}

// Configure runs top-down, so each directory inherits its parent's setting
// unless its own BUILD file carries the directive.
func (*licenseLang) Configure(c *config.Config, rel string, f *rule.File) {
	cfg := *getConfig(c)
	cfg.explicit = rel == "" && cfg.target != ""
	if f != nil {
		for _, d := range f.Directives {
			switch d.Key {
			case directive:
				cfg.declared = parseDeclared(d.Value)
				cfg.explicit = cfg.target != ""
			case filesDir:
				cfg.files = parseFiles(d.Value)
			case strictDir:
				if v, err := strconv.ParseBool(strings.TrimSpace(d.Value)); err == nil {
					cfg.strict = v
				}
			case detectorDir:
				name, file, err := parseDetector(d.Value)
				if err != nil {
					log.Fatalf("gazelle license: license_detector: %v", err)
				}
				cfg.setDetector(name, file, rel)
			case confDir:
				fields := strings.Fields(d.Value)
				if len(fields) < 2 || len(fields) > 3 {
					log.Fatalf("gazelle license: want `license_confidence <detector> <percent> [file]`, got %q", d.Value)
				}
				v, err := parseConfidence(fields[0], fields[1])
				if err != nil {
					log.Fatalf("gazelle license: %v", err)
				}
				file := ""
				if len(fields) == 3 {
					file = fields[2]
				}
				cfg.setConfidence(fields[0], v, file, rel)
			case subtreeDir:
				fields := strings.Fields(d.Value)
				if len(fields) < 2 || len(fields) > 3 {
					continue
				}
				subtrees := make(map[string]declared, len(cfg.subtrees)+1)
				for k, v := range cfg.subtrees {
					subtrees[k] = v
				}
				dir := path.Clean(path.Join(rel, fields[0]))
				if fields[1] == noneValue {
					delete(subtrees, dir)
				} else {
					subtrees[dir] = parseDeclared(strings.Join(fields[1:], " "))
				}
				cfg.subtrees = subtrees
			}
		}
	}
	// The longest matching subtree wins over the inherited default.
	best := -1
	for dir, d := range cfg.subtrees {
		if (rel == dir || strings.HasPrefix(rel, dir+"/")) && len(dir) > best {
			best = len(dir)
			cfg.declared = d
			cfg.explicit = true
		}
	}
	c.Exts[languageName] = &cfg
	if cfg.strict && c.RepoRoot != "" {
		if err := checkDir(filepath.Join(c.RepoRoot, filepath.FromSlash(rel)), rel, &cfg); err != nil {
			log.Fatalf("gazelle license: %v", err)
		}
	}
}

// defaultLicenseFiles are the license file names scanned unless license_files
// says otherwise. NOTICE and PATENTS are left out: they are attribution and
// patent grants, not license texts.
var defaultLicenseFiles = []string{
	"LICENSE", "LICENSE.txt", "LICENSE.md", "LICENSE.rst",
	"LICENCE", "LICENCE.txt", "LICENCE.md", "LICENCE.rst",
	"COPYING", "COPYING.txt", "COPYING.md",
	"UNLICENSE", "UNLICENSE.txt",
}

// parseFiles splits a space or comma separated list, expanding `default`.
func parseFiles(v string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == ',' || r == '\t' }) {
		if f == "default" {
			out = append(out, defaultLicenseFiles...)
		} else {
			out = append(out, f)
		}
	}
	return out
}

func isLicenseFile(names []string, file string) bool {
	if names == nil {
		names = defaultLicenseFiles
	}
	for _, n := range names {
		if ok, _ := path.Match(strings.ToLower(n), strings.ToLower(file)); ok {
			return true
		}
	}
	return false
}

// checkDir enforces strict mode for the license files in one directory.
func checkDir(dir, rel string, cfg *licenseConfig) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, e := range entries {
		if e.Type().IsRegular() && isLicenseFile(cfg.files, e.Name()) {
			files = append(files, e.Name())
		}
	}
	if len(files) == 0 {
		return nil
	}
	where := rel
	if where == "" {
		where = "."
	}
	// A LicenseRef-* id is an explicit declaration of a custom license, which
	// has no SPDX text to match against.
	if cfg.explicit && strings.HasPrefix(cfg.spdx, customLicensePrefix) {
		return nil
	}
	found := map[string]bool{}
	for _, name := range files {
		text, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		detector := cfg.detector
		if v, ok := cfg.fileDetector[path.Join(rel, name)]; ok {
			detector = v
		}
		if detector == "" {
			detector = DetectorLicenseCheck
		}
		minPercent := cfg.confidence[detector]
		if v, ok := cfg.fileConfidence[path.Join(rel, name)][detector]; ok {
			minPercent = v
		}
		det, err := Detect(detector, text, minPercent)
		if err != nil {
			return err
		}
		if !det.Standard {
			return fmt.Errorf("%s/%s does not match a standard license (%s confidence %.1f%%); it needs an explicit declaration through supply_chain_tools", where, name, detector, det.Percent)
		}
		for _, id := range det.IDs {
			found[id] = true
		}
	}
	if !cfg.explicit || cfg.target == "" {
		return fmt.Errorf("%s has a license file but no license_default or license_subtree declares its license", where)
	}
	if cfg.spdx == "" {
		return fmt.Errorf("%s declares %s without an SPDX id, so it can't be checked against the license file; write `%s <spdx>`", where, cfg.target, cfg.target)
	}
	if !found[cfg.spdx] {
		ids := make([]string, 0, len(found))
		for id := range found {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return fmt.Errorf("%s declares %s but its license file contains %s; use the concluded license mechanism in supply_chain_tools to override a declaration", where, cfg.spdx, strings.Join(ids, ", "))
	}
	return nil
}

func (*licenseLang) Kinds() map[string]rule.KindInfo {
	return map[string]rule.KindInfo{
		// package() has no name, so match whichever one the file already has.
		packageKind: {
			MatchAny:       true,
			MergeableAttrs: map[string]bool{attrName: true},
		},
	}
}

func (*licenseLang) GenerateRules(args language.GenerateArgs) language.GenerateResult {
	cfg := getConfig(args.Config)
	if cfg.target == "" {
		return language.GenerateResult{}
	}
	r := rule.NewRule(packageKind, "")
	r.SetAttr(attrName, []string{cfg.target})
	return language.GenerateResult{
		Gen:     []*rule.Rule{r},
		Imports: []interface{}{nil},
	}
}
