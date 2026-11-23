package agent

import (
	"encoding/json"
	"strings"
)

// maxObserverLineRunes caps each line emitted to the live observer so a giant
// tool command or text chunk never floods the TUI or unbounded observer state.
const maxObserverLineRunes = 500

// maxActivityLineRunes caps each structured tool/error line recorded for
// run.log so full Bash commands and permission-denial explanations are
// preserved without letting a pathological payload bloat the log file.
const maxActivityLineRunes = 1000

// StreamParser incrementally decodes newline-delimited stdout from agent CLIs
// (Agy `--output-format stream-json`, Claude Code `--output-format stream-json`,
// and Codex `exec --json`), extracting clean assistant prose into PlainText(),
// recording structured tool calls and tool/permission errors in Activity(), and
// emitting concise single-line activity indicators to onLine.
//
// Any line that is not a JSON object with a recognized event "type" or "event"
// is passed through verbatim as plain text, preserving 100% compatibility with
// plain-text agents and test shell scripts.
//
// StreamParser is not internally synchronized; callers must serialize FeedLine,
// PlainText, and Activity calls (SpawnWithObserver holds the feeder mutex).
type StreamParser struct {
	onLine     func(string)
	plainText  strings.Builder
	activity   strings.Builder
	agyLineBuf strings.Builder
}

// NewStreamParser returns a StreamParser that emits live activity lines to
// onLine (which may be nil).
func NewStreamParser(onLine func(string)) *StreamParser {
	return &StreamParser{onLine: onLine}
}

// PlainText returns the accumulated plain-text assistant transcript (or
// passthrough plain text).
func (p *StreamParser) PlainText() string {
	return p.plainText.String()
}

// Activity returns the accumulated structured tool-call and tool/permission
// error log (empty for plain-text agents that emit no structured events).
func (p *StreamParser) Activity() string {
	return p.activity.String()
}

type streamEnvelope struct {
	Type string `json:"type"`

	// Agy CLI `--output-format stream-json` envelope fields (keyed on "event").
	Event      string         `json:"event,omitempty"`
	StepUpdate *agyStepUpdate `json:"step_update,omitempty"`

	// Message is a JSON object in Claude `assistant`/`user` events and a JSON
	// string in Codex `error` events; decoding as RawMessage handles both.
	Message json.RawMessage `json:"message,omitempty"`

	// Claude `result` event fields. Note: "result" is shared with Agy's
	// result object; decode via RawMessage to support both string & object.
	Subtype string          `json:"subtype,omitempty"`
	IsError bool            `json:"is_error,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`

	// Codex `item.started` / `item.completed` fields.
	Item *codexStreamItem `json:"item,omitempty"`

	// Codex `turn.failed` / `error` fields.
	Error json.RawMessage `json:"error,omitempty"`
}

type agyStepUpdate struct {
	StepIndex int          `json:"step_index"`
	State     string       `json:"state"`
	StepType  string       `json:"step_type"`
	TextDelta string       `json:"text_delta,omitempty"`
	ToolName  string       `json:"tool_name,omitempty"`
	ToolInfo  *agyToolInfo `json:"tool_info,omitempty"`
}

type agyToolInfo struct {
	Name       string          `json:"name"`
	Parameters json.RawMessage `json:"parameters,omitempty"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type agyDeniedAction struct {
	Action      string `json:"action"`
	DisplayName string `json:"display_name,omitempty"`
}

type agyStreamResult struct {
	Status        string            `json:"status"`
	Response      string            `json:"response,omitempty"`
	Error         string            `json:"error,omitempty"`
	DeniedActions []agyDeniedAction `json:"denied_actions,omitempty"`
}

type claudeStreamMessage struct {
	Content []claudeStreamContent `json:"content"`
}

type claudeStreamContent struct {
	Type    string          `json:"type"`
	Text    string          `json:"text,omitempty"`
	Name    string          `json:"name,omitempty"`
	Input   json.RawMessage `json:"input,omitempty"`
	IsError bool            `json:"is_error,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
}

type codexStreamItem struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	Command string `json:"command,omitempty"`
}

// FeedLine processes one completed stdout line (newline stripped).
func (p *StreamParser) FeedLine(line string) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "{") {
		p.passThrough(line)
		return
	}

	var env streamEnvelope
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil || (env.Type == "" && env.Event == "") {
		p.passThrough(line)
		return
	}

	if env.Event != "" && env.Type == "" {
		if p.handleAgyEvent(env) {
			return
		}
		p.passThrough(line)
		return
	}

	switch env.Type {
	// Claude Code stream-json events.
	case "assistant":
		p.handleClaudeAssistant(env.Message)
	case "user":
		p.handleClaudeUser(env.Message)
	case "result":
		p.handleClaudeResult(env)
	case "system", "stream_event", "rate_limit_event", "tool_progress":
		// Recognized Claude protocol metadata/heartbeats; consume silently.

	// Codex CLI --json events.
	case "item.started":
		p.handleCodexItemStarted(env.Item)
	case "item.completed":
		p.handleCodexItemCompleted(env.Item)
	case "error", "turn.failed":
		p.handleCodexError(env)
	case "thread.started", "turn.started", "turn.completed", "item.updated":
		// Recognized Codex protocol metadata; consume silently.

	default:
		// Unrecognized JSON object: preserve verbatim as plain text.
		p.passThrough(line)
	}
}

func (p *StreamParser) passThrough(line string) {
	p.plainText.WriteString(line)
	p.plainText.WriteByte('\n')
	p.emit(line)
}

func (p *StreamParser) emit(line string) {
	if p.onLine == nil {
		return
	}
	p.onLine(truncateRunes(line, maxObserverLineRunes))
}

// recordActivity appends a structured tool/error event to Activity() (even
// when onLine is nil) and forwards it to the live observer.
func (p *StreamParser) recordActivity(line string) {
	p.activity.WriteString(truncateRunes(line, maxActivityLineRunes))
	p.activity.WriteByte('\n')
	p.emit(line)
}

func (p *StreamParser) appendPlainText(text string) {
	if text == "" {
		return
	}
	p.plainText.WriteString(text)
	if !strings.HasSuffix(text, "\n") {
		p.plainText.WriteByte('\n')
	}
}

func (p *StreamParser) emitTextLines(text string) {
	if p.onLine == nil || text == "" {
		return
	}
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSuffix(l, "\r")
		if strings.TrimSpace(l) == "" {
			continue
		}
		p.emit(l)
	}
}

func (p *StreamParser) handleClaudeAssistant(rawMsg json.RawMessage) {
	if len(rawMsg) == 0 {
		return
	}
	var msg claudeStreamMessage
	if err := json.Unmarshal(rawMsg, &msg); err != nil {
		return
	}
	for _, c := range msg.Content {
		switch c.Type {
		case "text":
			if c.Text != "" {
				p.appendPlainText(c.Text)
				p.emitTextLines(c.Text)
			}
		case "tool_use":
			if summary := formatToolUse(c.Name, c.Input); summary != "" {
				p.recordActivity(summary)
			}
		}
	}
}

func (p *StreamParser) handleClaudeUser(rawMsg json.RawMessage) {
	if len(rawMsg) == 0 {
		return
	}
	var msg claudeStreamMessage
	if err := json.Unmarshal(rawMsg, &msg); err != nil {
		return
	}
	for _, c := range msg.Content {
		if c.Type != "tool_result" || !c.IsError {
			continue
		}
		text := extractToolResultText(c.Content)
		summary := firstNonEmptyLine(text)
		if summary == "" {
			summary = "unknown error"
		}
		if isPermissionError(text) {
			p.recordActivity("⚠ permission denied: " + summary)
		} else {
			p.recordActivity("✗ tool error: " + summary)
		}
	}
}

func (p *StreamParser) handleClaudeResult(env streamEnvelope) {
	var resultText string
	if len(env.Result) > 0 {
		_ = json.Unmarshal(env.Result, &resultText)
	}
	trimmed := strings.TrimSpace(resultText)
	if env.IsError {
		if trimmed != "" {
			if !strings.Contains(p.plainText.String(), trimmed) {
				p.appendPlainText(resultText)
			}
			p.recordActivity("✗ agent error: " + firstNonEmptyLine(resultText))
		} else if env.Subtype != "" {
			p.recordActivity("✗ agent error: " + sanitizeSingleLine(env.Subtype))
		}
		return
	}
	if trimmed != "" && !strings.Contains(p.plainText.String(), trimmed) {
		p.appendPlainText(resultText)
		p.emitTextLines(resultText)
	}
}

func (p *StreamParser) handleCodexItemStarted(item *codexStreamItem) {
	if item == nil {
		return
	}
	if item.Type == "command_execution" {
		cmd := sanitizeSingleLine(item.Command)
		if cmd == "" {
			p.recordActivity("→ Bash")
		} else {
			p.recordActivity("→ Bash " + cmd)
		}
	}
}

func (p *StreamParser) handleCodexItemCompleted(item *codexStreamItem) {
	if item == nil {
		return
	}
	if item.Type == "agent_message" && item.Text != "" {
		p.appendPlainText(item.Text)
		p.emitTextLines(item.Text)
	}
}

func (p *StreamParser) handleCodexError(env streamEnvelope) {
	var msg string
	if len(env.Error) > 0 {
		var errObj struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(env.Error, &errObj); err == nil && strings.TrimSpace(errObj.Message) != "" {
			msg = errObj.Message
		} else {
			var errStr string
			if err := json.Unmarshal(env.Error, &errStr); err == nil && strings.TrimSpace(errStr) != "" {
				msg = errStr
			}
		}
	}
	if strings.TrimSpace(msg) == "" && len(env.Message) > 0 {
		var s string
		if err := json.Unmarshal(env.Message, &s); err == nil {
			msg = s
		}
	}
	summary := firstNonEmptyLine(msg)
	if summary == "" {
		summary = "unknown error"
	}
	p.recordActivity("✗ codex error: " + summary)
}

func (p *StreamParser) handleAgyEvent(env streamEnvelope) bool {
	switch env.Event {
	case "init":
		return true
	case "step_update":
		if env.StepUpdate == nil {
			return true
		}
		su := env.StepUpdate
		switch su.StepType {
		case "agent_response":
			if su.TextDelta != "" {
				p.plainText.WriteString(su.TextDelta)
				p.feedAgyTextDelta(su.TextDelta)
			}
			if su.State == "DONE" {
				p.flushAgyLineBuf()
				if p.plainText.Len() > 0 && !strings.HasSuffix(p.plainText.String(), "\n") {
					p.plainText.WriteByte('\n')
				}
			}
		case "tool":
			if su.State == "ACTIVE" {
				name := su.ToolName
				var params json.RawMessage
				if su.ToolInfo != nil {
					if su.ToolInfo.Name != "" {
						name = su.ToolInfo.Name
					}
					params = su.ToolInfo.Parameters
				}
				if summary := formatAgyToolUse(name, params); summary != "" {
					p.recordActivity(summary)
				}
			} else if su.State == "ERROR" || (su.ToolInfo != nil && su.ToolInfo.Error != nil) {
				errMsg := ""
				if su.ToolInfo != nil && su.ToolInfo.Error != nil {
					errMsg = su.ToolInfo.Error.Message
				}
				summary := firstNonEmptyLine(errMsg)
				if summary == "" {
					summary = "unknown error"
				}
				if isPermissionError(errMsg) {
					p.recordActivity("⚠ permission denied: " + summary)
				} else {
					p.recordActivity("✗ tool error: " + summary)
				}
			}
		}
		return true
	case "result":
		p.flushAgyLineBuf()
		var res agyStreamResult
		if len(env.Result) > 0 {
			_ = json.Unmarshal(env.Result, &res)
		}
		if res.Status != "" && !strings.EqualFold(res.Status, "SUCCESS") {
			msg := firstNonEmptyLine(firstNonEmpty(res.Error, res.Status))
			p.recordActivity("✗ agent error: " + msg)
			if trimmed := strings.TrimSpace(res.Error); trimmed != "" && !strings.Contains(p.plainText.String(), trimmed) {
				p.appendPlainText(res.Error)
			}
			return true
		}
		if trimmed := strings.TrimSpace(res.Response); trimmed != "" && !strings.Contains(p.plainText.String(), trimmed) {
			p.appendPlainText(res.Response)
			p.emitTextLines(res.Response)
		} else if trimmed == "" && len(res.DeniedActions) > 0 {
			var parts []string
			for _, d := range res.DeniedActions {
				label := strings.TrimSpace(d.Action)
				if dn := strings.TrimSpace(d.DisplayName); dn != "" && !strings.EqualFold(dn, label) {
					label = label + " (" + dn + ")"
				}
				if label != "" {
					parts = append(parts, label)
				}
			}
			if len(parts) == 0 {
				parts = []string{"unknown"}
			}
			p.recordActivity("⚠ permission denied: agy headless run aborted by unapproved action(s): " + strings.Join(parts, ", "))
		}
		return true
	default:
		return false
	}
}

func (p *StreamParser) feedAgyTextDelta(delta string) {
	p.agyLineBuf.WriteString(delta)
	s := p.agyLineBuf.String()
	for {
		idx := strings.IndexByte(s, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimSuffix(s[:idx], "\r")
		if strings.TrimSpace(line) != "" {
			p.emit(line)
		}
		s = s[idx+1:]
	}
	p.agyLineBuf.Reset()
	p.agyLineBuf.WriteString(s)
}

func (p *StreamParser) flushAgyLineBuf() {
	if p.agyLineBuf.Len() == 0 {
		return
	}
	line := strings.TrimSuffix(p.agyLineBuf.String(), "\r")
	p.agyLineBuf.Reset()
	if strings.TrimSpace(line) != "" {
		p.emit(line)
	}
}

func formatAgyToolUse(name string, rawParams json.RawMessage) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	var in struct {
		CommandLine   string `json:"CommandLine"`
		Command       string `json:"command"`
		Path          string `json:"Path"`
		LowerPath     string `json:"path"`
		AbsolutePath  string `json:"AbsolutePath"`
		TargetFile    string `json:"TargetFile"`
		FilePath      string `json:"file_path"`
		DirectoryPath string `json:"DirectoryPath"`
		SearchPath    string `json:"SearchPath"`
		Query         string `json:"Query"`
		Pattern       string `json:"Pattern"`
		LowerPattern  string `json:"pattern"`
	}
	if len(rawParams) > 0 {
		_ = json.Unmarshal(rawParams, &in)
	}

	label := name
	var detail string
	switch name {
	case "run_command":
		label = "Bash"
		detail = firstNonEmpty(in.CommandLine, in.Command)
	case "read_file", "view_file":
		label = "Read"
		detail = firstNonEmpty(in.Path, in.AbsolutePath, in.FilePath, in.LowerPath)
	case "write_file", "write_to_file":
		label = "Write"
		detail = firstNonEmpty(in.TargetFile, in.Path, in.FilePath, in.LowerPath)
	case "edit_file", "replace_file_content", "multi_replace_file_content":
		label = "Edit"
		detail = firstNonEmpty(in.TargetFile, in.Path, in.FilePath, in.LowerPath)
	case "grep_search":
		label = "Grep"
		detail = in.Query
		if detail != "" && strings.TrimSpace(in.SearchPath) != "" {
			detail += " " + strings.TrimSpace(in.SearchPath)
		}
	case "find_by_name":
		label = "Glob"
		detail = firstNonEmpty(in.Pattern, in.LowerPattern, in.SearchPath)
	case "list_dir":
		label = "LS"
		detail = firstNonEmpty(in.DirectoryPath, in.Path, in.LowerPath)
	}

	detail = sanitizeSingleLine(detail)
	if detail == "" {
		return "→ " + label
	}
	return "→ " + label + " " + detail
}

func formatToolUse(name string, rawInput json.RawMessage) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	var in struct {
		FilePath string `json:"file_path"`
		Path     string `json:"path"`
		Command  string `json:"command"`
		Pattern  string `json:"pattern"`
		Query    string `json:"query"`
		URL      string `json:"url"`
	}
	if len(rawInput) > 0 {
		_ = json.Unmarshal(rawInput, &in)
	}

	var detail string
	switch name {
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit":
		detail = firstNonEmpty(in.FilePath, in.Path)
	case "Bash":
		detail = in.Command
	case "Grep":
		detail = in.Pattern
		if detail != "" && strings.TrimSpace(in.Path) != "" {
			detail += " " + strings.TrimSpace(in.Path)
		}
	case "Glob":
		detail = in.Pattern
	case "WebSearch":
		detail = in.Query
	case "WebFetch":
		detail = in.URL
	}

	detail = sanitizeSingleLine(detail)
	if detail == "" {
		return "→ " + name
	}
	return "→ " + name + " " + detail
}

func extractToolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func isPermissionError(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "permission") ||
		strings.Contains(lower, "denied") ||
		strings.Contains(lower, "not allowed") ||
		strings.Contains(lower, "disallowed") ||
		strings.Contains(lower, "dontask")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstNonEmptyLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			return l
		}
	}
	return ""
}

func sanitizeSingleLine(s string) string {
	s = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(s)
	return strings.TrimSpace(s)
}

func truncateRunes(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}
