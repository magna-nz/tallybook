package report

import (
	"strconv"

	"github.com/magna-nz/tallybook/internal/findings"
)

// Defaults for the two report thresholds, applied when a caller passes zero
// so that an unset config value behaves like the rule thresholds do.
const (
	defaultMinSavingUSD = 1.0
	defaultReportLimit  = 5
	// maxInfoLines is how many Info findings the default report lists before
	// the rest are counted as notes held back.
	maxInfoLines = 2
)

// NumberedFinding is a finding together with its 1-based position in the
// full list the findings package returned, which is the number
// `tallybook finding <n>` takes.
type NumberedFinding struct {
	N       int
	Finding findings.Finding
}

// TopFindings is the slice of the full findings list that the default
// report prints: the priced findings worth listing, the notes shown under
// them, and how many of each were held back.
type TopFindings struct {
	Priced       []NumberedFinding
	Info         []NumberedFinding
	HiddenPriced int
	HiddenInfo   int
}

// Hidden is how many findings of either kind were left out.
func (t TopFindings) Hidden() int { return t.HiddenPriced + t.HiddenInfo }

// SelectTop decides what the default report shows. It keeps the order it is
// given, which is the order findings.Run produced: priced findings by
// descending saving, then the Info findings.
//
// A priced finding is listed when its saving reaches minSavingUSD and the
// limit is not yet used up; anything else counts towards HiddenPriced. At
// most maxInfoLines notes are listed and the rest count towards HiddenInfo.
// A zero or negative threshold means "use the default".
func SelectTop(all []findings.Finding, minSavingUSD float64, limit int) TopFindings {
	if minSavingUSD <= 0 {
		minSavingUSD = defaultMinSavingUSD
	}
	if limit <= 0 {
		limit = defaultReportLimit
	}

	var top TopFindings
	for i, f := range all {
		nf := NumberedFinding{N: i + 1, Finding: f}
		if f.Confidence == findings.Info {
			if len(top.Info) < maxInfoLines {
				top.Info = append(top.Info, nf)
			} else {
				top.HiddenInfo++
			}
			continue
		}
		if len(top.Priced) < limit && f.SavingUSD >= minSavingUSD {
			top.Priced = append(top.Priced, nf)
			continue
		}
		top.HiddenPriced++
	}
	return top
}

// moreLine is the one-line pointer to `tallybook findings` shown when
// something was held back. It returns "" when nothing was.
func moreLine(t TopFindings) string {
	const tail = ". Run `tallybook findings` to see them all."
	switch {
	case t.HiddenPriced > 0 && t.HiddenInfo > 0:
		return countOf(t.HiddenPriced, "more finding") + " and " + countOf(t.HiddenInfo, "more note") + tail
	case t.HiddenPriced > 0:
		return countOf(t.HiddenPriced, "more finding") + tail
	case t.HiddenInfo > 0:
		return countOf(t.HiddenInfo, "more note") + tail
	}
	return ""
}

// countOf renders a count with its noun, pluralised: "1 more note",
// "3 more findings".
func countOf(n int, noun string) string {
	return strconv.Itoa(n) + " " + noun + plural(n)
}
