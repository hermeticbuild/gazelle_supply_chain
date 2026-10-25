package license

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/google/licensecheck"
	classifier "github.com/google/licenseclassifier/v2"
	"github.com/google/licenseclassifier/v2/assets"
)

// Names of the available license detectors.
const (
	DetectorLicenseCheck      = "licensecheck"
	DetectorLicenseClassifier = "licenseclassifier"
)

// classifierFloor is the confidence below which licenseclassifier's embedded
// corpus reports nothing; it is fixed when the corpus is built.
const classifierFloor = 80.0

// defaultConfidence is the minimum percentage used for each detector when no
// license_confidence setting is given.
var defaultConfidence = map[string]float64{
	DetectorLicenseCheck:      90,
	DetectorLicenseClassifier: classifierFloor,
}

// Detection is the outcome of matching a license file against known licenses.
type Detection struct {
	// IDs are the distinct SPDX identifiers found, in order of first appearance.
	IDs []string
	// Percent is how much of the file the matches explain, or how sure the
	// detector is of them, depending on the detector.
	Percent float64
	// Standard reports whether Percent reached the requested minimum.
	Standard bool
}

// validateConfidence checks a minimum percentage for a detector.
func validateConfidence(detector string, percent float64) error {
	if !validDetector(detector) {
		return fmt.Errorf("unknown license detector %q (want %s or %s)", detector, DetectorLicenseCheck, DetectorLicenseClassifier)
	}
	if percent <= 0 || percent > 100 {
		return fmt.Errorf("license confidence for %s must be in (0, 100], got %v", detector, percent)
	}
	if detector == DetectorLicenseClassifier && percent < classifierFloor {
		return fmt.Errorf("license confidence for %s can't go below %v", detector, classifierFloor)
	}
	return nil
}

func validDetector(name string) bool {
	_, ok := defaultConfidence[name]
	return ok
}

// Detect matches text with the named detector (empty selects licensecheck).
// minPercent is the confidence needed for the text to count as a standard
// license; a value <= 0 selects the detector's default.
//
//   - licensecheck: the share of the file's words covered by SPDX license
//     texts. The copyright holder and year are variable text in the SPDX
//     templates, so they are ignored.
//   - licenseclassifier: the lowest confidence among the license matches. The
//     matches must also cover minPercent of the file's text lines, so a file
//     that mixes a standard license with custom terms is not standard.
func Detect(detector string, text []byte, minPercent float64) (Detection, error) {
	if detector == "" {
		detector = DetectorLicenseCheck
	}
	if minPercent <= 0 {
		minPercent = defaultConfidence[detector]
	}
	if err := validateConfidence(detector, minPercent); err != nil {
		return Detection{}, err
	}
	if detector == DetectorLicenseClassifier {
		return detectLicenseClassifier(text, minPercent)
	}
	return detectLicenseCheck(text, minPercent), nil
}

func detectLicenseCheck(text []byte, minPercent float64) Detection {
	cov := licensecheck.Scan(text)
	d := Detection{Percent: cov.Percent, Standard: cov.Percent >= minPercent}
	matches := append([]licensecheck.Match(nil), cov.Match...)
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Start < matches[j].Start })
	for _, m := range matches {
		d.IDs = appendUnique(d.IDs, m.ID)
	}
	return d
}

// Building the classifier loads the whole license corpus, so do it once.
var (
	classifierOnce sync.Once
	classifierInst *classifier.Classifier
	classifierErr  error
)

func detectLicenseClassifier(text []byte, minPercent float64) (Detection, error) {
	classifierOnce.Do(func() { classifierInst, classifierErr = assets.DefaultClassifier() })
	if classifierErr != nil {
		return Detection{}, classifierErr
	}
	c := classifierInst
	matches := c.Match(text).Matches

	// Match lines are numbered in the normalized text (copyright notices
	// removed), so measure coverage there.
	lines := bytes.Split(c.Normalize(text), []byte("\n"))
	covered := make([]bool, len(lines)+2)
	lowest := 1.0
	var d Detection
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].StartLine < matches[j].StartLine })
	for _, m := range matches {
		for l := m.StartLine; l <= m.EndLine && l < len(covered); l++ {
			covered[l] = true
		}
		// Copyright lines count toward coverage but aren't a license.
		if m.MatchType == "Copyright" {
			continue
		}
		if m.Confidence < lowest {
			lowest = m.Confidence
		}
		d.IDs = appendUnique(d.IDs, m.Name)
	}
	total, hit := 0, 0
	for i, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		total++
		if covered[i+1] {
			hit++
		}
	}
	if len(d.IDs) == 0 {
		return d, nil
	}
	d.Percent = lowest * 100
	d.Standard = total > 0 && d.Percent >= minPercent && float64(hit)/float64(total)*100 >= minPercent
	return d, nil
}

func appendUnique(ids []string, id string) []string {
	id = strings.TrimSpace(id)
	for _, have := range ids {
		if have == id {
			return ids
		}
	}
	return append(ids, id)
}
