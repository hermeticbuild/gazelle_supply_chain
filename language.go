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
// Strictness has three levels, set with the license_strict directive or the
// -license_strict flag:
//
//	# gazelle:license_strict true      full enforcement (the default)
//	# gazelle:license_strict relaxed   only declarations contradicting the files fail
//	# gazelle:license_strict false     no enforcement
//
// In relaxed mode a license that can't be determined (no license file, none
// declared, no standard match) is left out, so reports show NOASSERTION and
// concluding a license is up to the report tooling. In full mode every LICENSE/COPYING file Gazelle finds must match a standard
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
	"sync"

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
	symlinksDir  = "license_follow_symlinks"
	detectorDir  = "license_detector"
	confDir      = "license_confidence"

	customLicensePrefix = "LicenseRef-"
	noneValue           = "none"
	// detectValue stands in for the SPDX id of a declaration: the id is read
	// from the license files found next to the declaration instead.
	detectValue     = "detect"
	packageKind     = "package"
	attrName        = "default_applicable_licenses"
	metadataModule  = "package_metadata"
	licenseKind     = "license"
	licenseKindKind = "license_kind"
	metadataKind    = "package_metadata"
)

type licenseLang struct {
	language.BaseLang
	flagDefault  string
	flagSubtrees subtreeFlag
	flagStrict   strictFlag
	flagFollow   bool
	flagFiles    string
	flagDetector detectorFlag
	flagConf     confFlag

	mu    sync.Mutex
	decls map[string]*declaration
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
	fs.BoolVar(&l.flagFollow, "license_follow_symlinks", true, "follow symlinked license files to the real file")
	l.flagStrict = strictFlag{strictFull}
	fs.Var(&l.flagStrict, "license_strict", "enforcement: true (default), relaxed (only fail on declarations contradicting the license files) or false")
}

func (l *licenseLang) CheckFlags(fs *flag.FlagSet, c *config.Config) error {
	cfg := &licenseConfig{strict: l.flagStrict.level, noSymlinks: !l.flagFollow}
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
	cfg.owner = ""
	for _, v := range l.flagSubtrees {
		dir, rest, _ := strings.Cut(v, "=")
		if cfg.subtrees == nil {
			cfg.subtrees = map[string]declared{}
		}
		d := parseDeclared(rest)
		d.owner = path.Clean(dir)
		cfg.subtrees[d.owner] = d
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
	return []string{directive, subtreeDir, strictDir, detectorDir, confDir, filesDir, symlinksDir}
}

// declared is a license target and, optionally, the SPDX id it stands for.
type declared struct {
	target string
	spdx   string
	// owner is the repo-relative directory the declaration was made for: the
	// license files found there are described by target.
	owner string
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
	// strict is how much of the license files' state is enforced.
	strict strictness
	// detector selects the license detector; empty means licensecheck.
	detector string
	// files are the names (globs, case-insensitive) of the license files to
	// scan; nil selects defaultLicenseFiles.
	files []string
	// noSymlinks ignores symlinked license files instead of following them.
	noSymlinks bool
	// fileDetector overrides detector for one file, keyed by repo-relative path.
	fileDetector map[string]string
	// confidence holds the minimum percentage chosen per detector.
	confidence map[string]float64
	// fileConfidence overrides confidence for one file, keyed by its
	// repo-relative path and then by detector.
	fileConfidence map[string]map[string]float64
	// prefix is the Go import path of the repository root (from the
	// `gazelle:prefix` directive), used to build the purl of generated metadata.
	prefix string
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
func (l *licenseLang) Configure(c *config.Config, rel string, f *rule.File) {
	cfg := *getConfig(c)
	cfg.explicit = rel == "" && cfg.target != ""
	if f != nil {
		for _, d := range f.Directives {
			switch d.Key {
			case directive:
				cfg.declared = parseDeclared(d.Value)
				cfg.owner = rel
				cfg.explicit = cfg.target != ""
			case "prefix":
				cfg.prefix = strings.TrimSpace(d.Value)
			case filesDir:
				cfg.files = parseFiles(d.Value)
			case symlinksDir:
				if v, err := strconv.ParseBool(strings.TrimSpace(d.Value)); err == nil {
					cfg.noSymlinks = !v
				}
			case strictDir:
				level, err := parseStrictness(d.Value)
				if err != nil {
					log.Fatalf("gazelle license: license_strict: %v", err)
				}
				cfg.strict = level
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
					d := parseDeclared(strings.Join(fields[1:], " "))
					d.owner = dir
					subtrees[dir] = d
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
	if cfg.owner == rel && cfg.spdx != "" {
		if _, _, local := localTarget(cfg.target, rel); local {
			l.register(rel, buildDeclaration(c.RepoRoot, rel, &cfg))
		}
	}
	if cfg.strict != strictOff && c.RepoRoot != "" {
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

// detectFile matches one license file of the directory rel (at dir on disk)
// with the detector and confidence configured for it.
func detectFile(dir, rel, name string, cfg *licenseConfig) (Detection, string, error) {
	text, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return Detection{}, "", err
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
	return det, detector, err
}

// strictness is how much Gazelle enforces about the license files it finds.
type strictness int

const (
	// strictOff enforces nothing.
	strictOff strictness = iota
	// strictRelaxed only fails on a declaration that contradicts the license
	// files. What can't be determined is left out of the report.
	strictRelaxed
	// strictFull also requires every license file to match a standard license
	// and to be declared, and every detect declaration to find one.
	strictFull
)

func parseStrictness(v string) (strictness, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "strict", "on", "1", "":
		return strictFull, nil
	case "relaxed":
		return strictRelaxed, nil
	case "false", "off", "0":
		return strictOff, nil
	}
	return strictOff, fmt.Errorf("want true, relaxed or false, got %q", v)
}

// strictFlag is the -license_strict flag. A bare -license_strict means true.
type strictFlag struct{ level strictness }

func (f *strictFlag) String() string {
	switch f.level {
	case strictRelaxed:
		return "relaxed"
	case strictFull:
		return "true"
	}
	return "false"
}

func (f *strictFlag) Set(v string) error {
	level, err := parseStrictness(v)
	f.level = level
	return err
}

func (*strictFlag) IsBoolFlag() bool { return true }

// checkDir enforces strict mode for the license files in one directory.
func checkDir(dir, rel string, cfg *licenseConfig) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, e := range entries {
		if isFileOrFileLink(dir, e, cfg) && isLicenseFile(cfg.files, e.Name()) {
			files = append(files, e.Name())
		}
	}
	where := rel
	if where == "" {
		where = "."
	}
	if len(files) == 0 {
		// Nothing to detect from where a detect declaration was made: that is
		// no license, not an unknown one, so it has to be declared by hand.
		if cfg.strict == strictFull && cfg.spdx == detectValue && cfg.owner == rel {
			return fmt.Errorf("%s has no license file to detect a license from; declare it explicitly, for example `%s LicenseRef-Unlicensed`", where, cfg.target)
		}
		return nil
	}
	// A LicenseRef-* id is an explicit declaration of a custom license, which
	// has no SPDX text to match against.
	if cfg.explicit && strings.HasPrefix(cfg.spdx, customLicensePrefix) {
		return nil
	}
	found := map[string]bool{}
	for _, name := range files {
		det, detector, err := detectFile(dir, rel, name, cfg)
		if err != nil {
			return err
		}
		if !det.Standard {
			if cfg.strict == strictRelaxed {
				continue
			}
			return fmt.Errorf("%s/%s does not match a standard license (%s confidence %.1f%%); it needs an explicit declaration through supply_chain_tools", where, name, detector, det.Percent)
		}
		for _, id := range det.IDs {
			found[id] = true
		}
	}
	if !cfg.explicit || cfg.target == "" {
		if cfg.strict == strictRelaxed {
			return nil
		}
		return fmt.Errorf("%s has a license file but no license_default or license_subtree declares its license", where)
	}
	if cfg.spdx == detectValue {
		return nil
	}
	if cfg.spdx == "" {
		if cfg.strict == strictRelaxed {
			return nil
		}
		return fmt.Errorf("%s declares %s without an SPDX id, so it can't be checked against the license file; write `%s <spdx>`", where, cfg.target, cfg.target)
	}
	// Relaxed mode cannot tell a mismatch from a file it could not classify.
	if !found[cfg.spdx] && (cfg.strict == strictFull || len(found) > 0) {
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
		licenseKind: {
			NonEmptyAttrs:  map[string]bool{"kind": true},
			MergeableAttrs: map[string]bool{"kind": true, "text": true},
		},
		licenseKindKind: {
			NonEmptyAttrs:  map[string]bool{"identifier": true},
			MergeableAttrs: map[string]bool{"identifier": true, "full_name": true},
		},
		// Only attributes is merged: an existing package_metadata keeps its purl.
		metadataKind: {
			MergeableAttrs: map[string]bool{"attributes": true},
		},
	}
}

func (*licenseLang) ApparentLoads(moduleToApparentName func(string) string) []rule.LoadInfo {
	repo := moduleToApparentName(metadataModule)
	if repo == "" {
		repo = metadataModule
	}
	return []rule.LoadInfo{
		{Name: "@" + repo + "//licenses/rules:license.bzl", Symbols: []string{licenseKind}},
		{Name: "@" + repo + "//licenses/rules:license_kind.bzl", Symbols: []string{licenseKindKind}},
		{Name: "@" + repo + "//rules:package_metadata.bzl", Symbols: []string{metadataKind}},
	}
}

// localTarget reports the package and name of a label that lives in the
// repository being scanned (`//pkg:name` or `:name`, with no repository part).
func localTarget(label, rel string) (pkg, name string, ok bool) {
	switch {
	case strings.HasPrefix(label, "//"):
		pkg, name, _ = strings.Cut(label[2:], ":")
		if name == "" {
			name = path.Base(pkg)
		}
		return pkg, name, true
	case strings.HasPrefix(label, ":"):
		return rel, label[1:], true
	}
	return "", "", false
}

func (l *licenseLang) GenerateRules(args language.GenerateArgs) language.GenerateResult {
	cfg := getConfig(args.Config)
	var res language.GenerateResult
	add := func(r *rule.Rule) {
		res.Gen = append(res.Gen, r)
		res.Imports = append(res.Imports, nil)
	}
	// One license() per license file, in the directory of the real file since
	// license.text takes a single file of its own package. That is not the
	// declaration's directory when the license file is a symlink.
	specs, kindIDs := l.specsIn(args.Rel)
	for _, id := range kindIDs {
		add(customKind(id))
	}
	for _, sp := range specs {
		r := rule.NewRule(licenseKind, sp.name)
		r.SetAttr("kind", sp.kind)
		// Referenced from the package_metadata, which may live in another package.
		r.SetAttr("visibility", []string{"//visibility:public"})
		if sp.file != "" {
			r.SetAttr("text", sp.file)
		}
		add(r)
	}
	if cfg.target == "" {
		return res
	}
	// The package go_repository writes its own metadata to must not point at
	// itself. With `detect` the label is that metadata, which REPO.bazel
	// already applies to every package, and tagging directories would create
	// BUILD files that split packages such as testdata/.
	if args.Rel != generatedDir && cfg.spdx != detectValue {
		r := rule.NewRule(packageKind, "")
		r.SetAttr(attrName, []string{cfg.target})
		add(r)
	}
	pkg, name, local := localTarget(cfg.target, cfg.owner)
	d := l.declarationOf(cfg.owner)
	if !local || cfg.spdx == "" || d == nil || args.Rel != pkg || len(d.specs) == 0 {
		return res
	}
	// The license() targets are attached to the package_metadata the label
	// names, which covers the whole declaration even when a license() lives
	// elsewhere. An existing one (go_repository generates it) keeps its purl
	// and gets the licenses added; otherwise it is created, with the purl of
	// the repository's own package_metadata and this directory as its subpath.
	var attrs []string
	for _, sp := range d.specs {
		attrs = append(attrs, "//"+sp.pkg+":"+sp.name)
	}
	m := rule.NewRule(metadataKind, name)
	if existing := findRule(args.File, metadataKind, name); existing != nil {
		attrs = union(existing.AttrStrings("attributes"), attrs)
	} else if args.Rel == cfg.owner {
		m.SetAttr("purl", purl(cfg, args))
		m.SetAttr("visibility", []string{"//visibility:public"})
	} else {
		return res
	}
	m.SetAttr("attributes", attrs)
	add(m)
	return res
}

// licenseSpec describes one license() to generate.
type licenseSpec struct {
	name string
	// pkg is the directory the license() lives in, and file the license file
	// there, empty when the declaration's directory has none.
	pkg, file string
	kind      string
}

// customKind is the license_kind of a LicenseRef- id.
func customKind(id string) *rule.Rule {
	r := rule.NewRule(licenseKindKind, generatedPrefix+"kind")
	r.SetAttr("visibility", []string{"//visibility:public"})
	r.SetAttr("identifier", id)
	r.SetAttr("full_name", strings.TrimPrefix(id, customLicensePrefix))
	return r
}

// buildDeclaration lists the license() targets of the declaration made in the
// directory rel.
func buildDeclaration(repoRoot, rel string, cfg *licenseConfig) *declaration {
	files := findLicenseFiles(repoRoot, rel, cfg)
	if cfg.spdx == detectValue {
		return &declaration{specs: detectSpecs(repoRoot, rel, cfg, files)}
	}
	d := &declaration{}
	kind := "@" + metadataModule + "//licenses/spdx:" + cfg.spdx
	if strings.HasPrefix(cfg.spdx, customLicensePrefix) {
		d.kindID = cfg.spdx
		kind = ":" + generatedPrefix + "kind"
	}
	for _, f := range files {
		d.specs = append(d.specs, licenseSpec{name: licenseTarget(f.name), pkg: f.pkg, file: f.base, kind: kind})
	}
	if len(files) == 0 {
		d.specs = []licenseSpec{{name: licenseTarget(""), pkg: rel, kind: kind}}
	}
	return d
}

// detectSpecs reads the SPDX ids from the license files. A file with several
// ids gets one license() per id, since a license() has a single kind. Files
// that match no standard license are reported and skipped: dependencies are
// not ours to fail on.
func detectSpecs(repoRoot, rel string, cfg *licenseConfig, files []licenseFile) []licenseSpec {
	dir := filepath.Join(repoRoot, filepath.FromSlash(rel))
	var specs []licenseSpec
	for _, f := range files {
		det, detector, err := detectFile(dir, rel, f.name, cfg)
		if err != nil {
			log.Printf("gazelle license: %s: %v", path.Join(rel, f.name), err)
			continue
		}
		if !det.Standard || len(det.IDs) == 0 {
			log.Printf("gazelle license: %s does not match a standard license (%s confidence %.1f%%); no license() generated", path.Join(rel, f.name), detector, det.Percent)
			continue
		}
		for _, id := range det.IDs {
			name := licenseTarget(f.name)
			if len(det.IDs) > 1 {
				name += "_" + licenseSuffix(id)
			}
			specs = append(specs, licenseSpec{name: name, pkg: f.pkg, file: f.base, kind: "@" + metadataModule + "//licenses/spdx:" + id})
		}
	}
	return specs
}

const (
	generatedDir    = "_gazelle_generated"
	generatedPrefix = "gazelle_generated_"
)

// licenseTarget names the license() generated for a file: LICENSE becomes
// gazelle_generated_license, LICENSE.txt gazelle_generated_license_txt.
func licenseTarget(file string) string {
	if file == "" {
		return generatedPrefix + "license"
	}
	return generatedPrefix + licenseSuffix(file)
}

func findRule(f *rule.File, kind, name string) *rule.Rule {
	if f == nil {
		return nil
	}
	for _, r := range f.Rules {
		if r.Kind() == kind && r.Name() == name {
			return r
		}
	}
	return nil
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(append([]string{}, a...), b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// licenseSuffix turns a file name into a target name part: LICENSE becomes
// "license" and LICENSE.txt "license_txt".
func licenseSuffix(file string) string {
	return strings.Trim(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, strings.ToLower(file)), "_")
}

// purl is the repository's own purl (from the package_metadata go_repository
// generated, so it carries the module version) with the directory appended as
// the subpath. Without it, the purl is built from the gazelle:prefix.
func purl(cfg *licenseConfig, args language.GenerateArgs) string {
	base := ""
	if f, err := rule.LoadFile(filepath.Join(args.Config.RepoRoot, generatedDir, "BUILD.bazel"), generatedDir); err == nil {
		if r := findRule(f, metadataKind, "package_metadata"); r != nil {
			base = r.AttrString("purl")
		}
	}
	if base == "" {
		if cfg.prefix == "" {
			base = "pkg:generic/" + args.Config.RepoName
		} else {
			base = "pkg:golang/" + cfg.prefix
		}
	}
	if args.Rel != "" {
		base += "#" + args.Rel
	}
	return base
}
