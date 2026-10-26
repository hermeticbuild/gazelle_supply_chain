package license

import (
	"log"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// licenseFile is a license file found in a declaration's directory. It may be
// a symlink to a file somewhere else in the repository.
type licenseFile struct {
	// name is the file name in the declaration's directory.
	name string
	// pkg is the repo-relative directory holding the real file, and base its
	// name there. They equal the declaration's directory and name unless the
	// file is a symlink.
	pkg, base string
}

// isFileOrFileLink reports whether the directory entry is a regular file or a
// symlink that resolves to one, unless symlinks are ignored.
func isFileOrFileLink(dir string, e os.DirEntry, cfg *licenseConfig) bool {
	if e.Type().IsRegular() {
		return true
	}
	if e.Type()&os.ModeSymlink == 0 || cfg.noSymlinks {
		return false
	}
	fi, err := os.Stat(filepath.Join(dir, e.Name()))
	return err == nil && fi.Mode().IsRegular()
}

// findLicenseFiles lists the license files of the directory rel of the
// repository at repoRoot. A symlink is followed to where the real file lives,
// which is where its license() has to be generated for `text` to name it.
// Symlinks leaving the repository are reported and skipped.
func findLicenseFiles(repoRoot, rel string, cfg *licenseConfig) []licenseFile {
	dir := filepath.Join(repoRoot, filepath.FromSlash(rel))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	root, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		root = repoRoot
	}
	var files []licenseFile
	for _, e := range entries {
		if !isLicenseFile(cfg.files, e.Name()) || !isFileOrFileLink(dir, e, cfg) {
			continue
		}
		f := licenseFile{name: e.Name(), pkg: rel, base: e.Name()}
		if e.Type()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			r, err := filepath.Rel(root, resolved)
			if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
				log.Printf("gazelle license: %s is a symlink to %s, outside the repository; skipped", path.Join(rel, e.Name()), resolved)
				continue
			}
			r = filepath.ToSlash(r)
			f.pkg, f.base = path.Dir(r), path.Base(r)
			if f.pkg == "." {
				f.pkg = ""
			}
		}
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	return files
}

// declaration holds the license() targets generated for one declaration. They
// are computed when the declaration's directory is configured, because a
// symlinked license file puts them in another directory, which is generated
// later and must know about them.
type declaration struct {
	specs []licenseSpec
	// kindID is the LicenseRef- id that needs a license_kind next to its
	// license() targets.
	kindID string
}

// register records the declaration made at its owner directory.
func (l *licenseLang) register(owner string, d *declaration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.decls == nil {
		l.decls = map[string]*declaration{}
	}
	l.decls[owner] = d
}

func (l *licenseLang) declarationOf(owner string) *declaration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.decls[owner]
}

// specsIn lists, in a stable order, the specs of every declaration whose
// license() has to live in the directory rel, with the kind ids they need.
func (l *licenseLang) specsIn(rel string) (specs []licenseSpec, kindIDs []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	owners := make([]string, 0, len(l.decls))
	for o := range l.decls {
		owners = append(owners, o)
	}
	sort.Strings(owners)
	for _, o := range owners {
		d := l.decls[o]
		here := false
		for _, sp := range d.specs {
			if sp.pkg == rel {
				specs = append(specs, sp)
				here = true
			}
		}
		if here && d.kindID != "" {
			kindIDs = append(kindIDs, d.kindID)
		}
	}
	return specs, kindIDs
}
