// Package gates implements the structured human checkpoints used by the CLI.
package gates

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const prefix = "CKB-WHITEHAT-GATE:"

var gateLine = regexp.MustCompile(`(?m)^CKB-WHITEHAT-GATE:\s*([A-Za-z0-9._-]+)(?:\s+(.*))?$`)

// ansiRe matches ANSI/VT escape sequences (CSI, OSC, and other ESC-introduced
// sequences) so they can be stripped from untrusted agent text before it
// reaches a TTY. A hostile target repo can emit escape bytes in stdout; if we
// printed them raw they'd drive the user's terminal (cursor/erase/OSC) — a
// terminal-injection vector (audit H4.2).
var ansiRe = regexp.MustCompile("\x1b(?:\\[[0-9;?]*[ -/][@-~]|\\][^\x1b\x07]*(?:\x07|\x1b\\())|[\x00-\x08\x0b-\x1f\x7f]")

// SanitizeTerminal strips terminal control/escape sequences from s so it is
// safe to print to a TTY. The gate ID is already constrained to a safe charset
// by gateLine; this protects the free-form Details and the agent's raw output
// lines that the live box and Prompt render.
func SanitizeTerminal(s string) string {
	s = ansiRe.ReplaceAllString(s, "")
	// Drop any remaining non-printable runes (the regex catches the common
	// ones; this is the belt-and-suspenders pass).
	return strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) || r == '\n' {
			return r
		}
		return -1
	}, s)
}

// Gate is a checkpoint requested by the agent. Details is opaque display data.
type Gate struct {
	ID      string `json:"id"`
	Details string `json:"details,omitempty"`
}

// Decision records an answer made at a gate.
type Decision struct {
	GateID    string    `json:"gate_id"`
	Choice    string    `json:"choice"`
	Details   string    `json:"details,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// Parse returns all structured gate requests in output, preserving order.
func Parse(output string) []Gate {
	matches := gateLine.FindAllStringSubmatch(output, -1)
	gates := make([]Gate, 0, len(matches))
	for _, m := range matches {
		gates = append(gates, Gate{ID: m[1], Details: strings.TrimSpace(m[2])})
	}
	return gates
}

// Store persists decisions in the workspace's decisions.json.
type Store struct {
	Path      string     `json:"-"`
	Decisions []Decision `json:"decisions"`
}

func Load(path string) (*Store, error) {
	s := &Store{Path: path, Decisions: []Decision{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("read decisions: %w", err)
	}
	s.Path = path
	return s, nil
}

func (s *Store) Save() error {
	if s.Path == "" {
		return errors.New("decision store path is empty")
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.Path, append(data, '\n'), 0600)
}

func (s *Store) Add(d Decision) error {
	if d.Timestamp.IsZero() {
		d.Timestamp = time.Now().UTC()
	}
	s.Decisions = append(s.Decisions, d)
	return s.Save()
}

// Prompt asks for a choice. In noninteractive mode, or when input is not a
// terminal, it safely returns deny without reading arbitrary agent text.
// The caller owns TTY detection and can pass interactive=false for CI.
func Prompt(in io.Reader, out io.Writer, gate Gate, interactive bool) (Decision, error) {
	d := Decision{GateID: gate.ID, Choice: "deny", Timestamp: time.Now().UTC()}
	if !interactive {
		return d, nil
	}
	if in == nil || out == nil {
		return d, errors.New("interactive gate requires input and output")
	}
	fmt.Fprintf(out, "\nckb-whitehat checkpoint: %s\n", SanitizeTerminal(gate.ID))
	if gate.Details != "" {
		fmt.Fprintf(out, "details: %s\n", SanitizeTerminal(gate.Details))
	}
	fmt.Fprintln(out, "choose: approve, edit, skip, deny, or stop")
	fmt.Fprint(out, "> ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return d, err
	}
	choice := strings.ToLower(strings.TrimSpace(line))
	switch choice {
	case "approve", "edit", "skip", "deny", "stop":
		d.Choice = choice
	default:
		d.Choice = "deny"
	}
	return d, nil
}
