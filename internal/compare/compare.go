package compare

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

type Finding struct{ ID, Title string }
type Result struct {
	Baseline, New          []Finding
	Covered, Missed, Novel []Finding
	Sections               []Section
}
type Section struct {
	Name    string
	Present bool
}

var findingRE = regexp.MustCompile(`(?m)^#{2,3}\s+((?:F|S|C)\d+(?:\.\d+)?)\s*[—-]\s*(.+)$`)
var parenSuffixRE = regexp.MustCompile(`\s*\([^()]*\)$`)
var required = []string{"## At a glance", "On-chain evidence", "Layer boundary", "Honest limitations"}

func ParseFindings(md string) []Finding {
	matches := findingRE.FindAllStringSubmatch(md, -1)
	out := make([]Finding, 0, len(matches))
	for _, m := range matches {
		title := strings.TrimSpace(m[2])
		// Node parity: strip only a TRAILING "(...)" suffix ("forged callback
		// (HIGH)" -> "forged callback"); a title ending in "— PROVEN" keeps its
		// full text because the $-anchor requires the closing paren to end it.
		title = parenSuffixRE.ReplaceAllString(title, "")
		out = append(out, Finding{ID: m[1], Title: title})
	}
	return out
}

func keywords(s string) map[string]struct{} {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	out := map[string]struct{}{}
	for _, w := range strings.Fields(b.String()) {
		if utf8.RuneCountInString(w) > 3 {
			out[w] = struct{}{}
		}
	}
	return out
}
func overlap(a, b string) float64 {
	A, B := keywords(a), keywords(b)
	if len(A) == 0 || len(B) == 0 {
		return 0
	}
	if len(A) > len(B) {
		A, B = B, A
	}
	hit := 0
	for w := range A {
		if _, ok := B[w]; ok {
			hit++
		}
	}
	return float64(hit) / float64(len(A))
}
func same(a, b Finding) bool {
	t := overlap(a.Title, b.Title)
	return t >= .45 || (a.ID == b.ID && t >= .25)
}

// Same is the exported content-match used to pair a new-report finding with
// the baseline finding it covers.
func Same(a, b Finding) bool { return same(a, b) }

func Compare(newMD, baseMD string) Result {
	n := ParseFindings(newMD)
	b := ParseFindings(baseMD)
	covered := []Finding{}
	missed := []Finding{}
	novel := []Finding{}
	for _, bf := range b {
		found := false
		for _, nf := range n {
			if same(nf, bf) {
				found = true
				covered = append(covered, bf)
				break
			}
		}
		if !found {
			missed = append(missed, bf)
		}
	}
	for _, nf := range n {
		found := false
		for _, bf := range b {
			if same(nf, bf) {
				found = true
				break
			}
		}
		if !found {
			novel = append(novel, nf)
		}
	}
	sections := make([]Section, len(required))
	for i, s := range required {
		sections[i] = Section{Name: s, Present: strings.Contains(newMD, s)}
	}
	return Result{Baseline: b, New: n, Covered: covered, Missed: missed, Novel: novel, Sections: sections}
}

func CoveragePercent(r Result) int {
	if len(r.Baseline) == 0 {
		return 0
	}
	return int(float64(len(r.Covered))/float64(len(r.Baseline))*100 + .5)
}
func SortFindings(fs []Finding) { sort.Slice(fs, func(i, j int) bool { return fs[i].ID < fs[j].ID }) }
