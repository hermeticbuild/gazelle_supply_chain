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
// Every directory Gazelle visits is tagged; whether new BUILD files may be
// created is left to Gazelle and the caller (for go_repository, its
// build_file_generation attribute).
package license

import (
	"flag"
	"fmt"
	"path"
	"strings"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/language"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

const (
	languageName = "license"
	directive    = "license_default"
	subtreeDir   = "license_subtree"
	noneValue    = "none"
	packageKind  = "package"
	attrName     = "default_applicable_licenses"
)

type licenseLang struct {
	language.BaseLang
	flagDefault  string
	flagSubtrees subtreeFlag
}

// subtreeFlag collects repeated -license_subtree=dir=label values.
type subtreeFlag []string

func (s *subtreeFlag) String() string { return fmt.Sprint(*s) }

func (s *subtreeFlag) Set(v string) error {
	if dir, label, ok := strings.Cut(v, "="); !ok || dir == "" || label == "" {
		return fmt.Errorf("want dir=label, got %q", v)
	}
	*s = append(*s, v)
	return nil
}

func (l *licenseLang) RegisterFlags(fs *flag.FlagSet, cmd string, c *config.Config) {
	fs.StringVar(&l.flagDefault, "license_default", "", "default license target for every package")
	fs.Var(&l.flagSubtrees, "license_subtree", "dir=label: license target for a directory and below (repeatable)")
}

func (l *licenseLang) CheckFlags(fs *flag.FlagSet, c *config.Config) error {
	cfg := &licenseConfig{target: l.flagDefault}
	for _, v := range l.flagSubtrees {
		dir, label, _ := strings.Cut(v, "=")
		if cfg.subtrees == nil {
			cfg.subtrees = map[string]string{}
		}
		cfg.subtrees[path.Clean(dir)] = label
	}
	c.Exts[languageName] = cfg
	return nil
}

// NewLanguage is the entry point used by gazelle_binary.
func NewLanguage() language.Language {
	return &licenseLang{}
}

func (*licenseLang) Name() string { return languageName }

func (*licenseLang) KnownDirectives() []string { return []string{directive, subtreeDir} }

type licenseConfig struct {
	// target is the label applied as the default license; empty means disabled.
	target string
	// subtrees maps a repo-relative directory to the license target for it.
	subtrees map[string]string
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
	if f != nil {
		for _, d := range f.Directives {
			switch d.Key {
			case directive:
				if d.Value == noneValue || d.Value == "" {
					cfg.target = ""
				} else {
					cfg.target = d.Value
				}
			case subtreeDir:
				fields := strings.Fields(d.Value)
				if len(fields) != 2 {
					continue
				}
				subtrees := make(map[string]string, len(cfg.subtrees)+1)
				for k, v := range cfg.subtrees {
					subtrees[k] = v
				}
				dir := path.Clean(path.Join(rel, fields[0]))
				if fields[1] == noneValue {
					delete(subtrees, dir)
				} else {
					subtrees[dir] = fields[1]
				}
				cfg.subtrees = subtrees
			}
		}
	}
	// The longest matching subtree wins over the inherited default.
	best := -1
	for dir, target := range cfg.subtrees {
		if (rel == dir || strings.HasPrefix(rel, dir+"/")) && len(dir) > best {
			best = len(dir)
			cfg.target = target
		}
	}
	c.Exts[languageName] = &cfg
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
