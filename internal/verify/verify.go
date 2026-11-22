package verify

import (
	"os"
	"regexp"
	"strconv"
)

type Check struct {
	What string
	OK   bool
}

type Marker struct {
	Findings int
	Fired    int
	Report   string
}

var markerRE = regexp.MustCompile(`CKB-WHITEHAT-RESULT: findings=([0-9]+) fired=([0-9]+) report=(.+)`)
var verdictRE = regexp.MustCompile(`(?m)^FINDING\s+(\S+)\s+(FIRED|UNPROVEN|STRUCTURAL)\b`)

func LastMarker(stdout string) *Marker {
	matches := markerRE.FindAllStringSubmatch(stdout, -1)
	if len(matches) == 0 {
		return nil
	}
	m := matches[len(matches)-1]
	findings, _ := strconv.Atoi(m[1])
	fired, _ := strconv.Atoi(m[2])
	return &Marker{Findings: findings, Fired: fired, Report: m[3]}
}

func VerifyRun(workspace, stdout string, marker *Marker) []Check {
	reportPath := workspace + string(os.PathSeparator) + "FINDINGS.md"
	pocDir := workspace + string(os.PathSeparator) + "poc"
	reportBytes, _ := os.ReadFile(reportPath)
	report := string(reportBytes)
	checks := []Check{
		{What: "report exists and is non-trivial: " + reportPath, OK: len([]rune(trimSpace(report))) > 200},
		{What: "agent printed the CKB-WHITEHAT-RESULT: marker", OK: marker != nil},
	}
	if marker != nil && (marker.Findings > 0 || marker.Fired > 0) {
		entries, _ := os.ReadDir(pocDir)
		files := 0
		for _, entry := range entries {
			if !entry.IsDir() && entry.Name() != "" && entry.Name()[0] != '.' {
				files++
			}
		}
		checks = append(checks,
			Check{What: "at least one PoC file in " + pocDir, OK: files > 0},
			Check{What: "a FINDING <id> verdict line was printed (" + strconv.Itoa(len(verdictRE.FindAllStringSubmatch(stdout, -1))) + ")", OK: verdictRE.MatchString(stdout)},
			Check{What: "the report carries FINDING verdict lines (" + strconv.Itoa(len(verdictRE.FindAllStringSubmatch(report, -1))) + ")", OK: verdictRE.MatchString(report)},
		)
	}
	return checks
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\n' || s[start] == '\r' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\n' || s[end-1] == '\r' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
