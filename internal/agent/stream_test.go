package agent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStreamParserClaudeEvents(t *testing.T) {
	var emitted []string
	p := NewStreamParser(func(line string) {
		emitted = append(emitted, line)
	})

	events := []string{
		// Protocol metadata & heartbeats: consumed silently.
		`{"type":"system","subtype":"init","session_id":"s-123"}`,
		`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"}}`,
		`{"type":"tool_progress","tool_name":"Bash","elapsed_time_seconds":30,"heartbeat":true}`,

		// Assistant tool_use events across common tools + unknown tool.
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"contracts/lock.rs"}},{"type":"tool_use","name":"Write","input":{"file_path":"poc/exploit.rs"}},{"type":"tool_use","name":"Edit","input":{"file_path":"poc/exploit.rs"}},{"type":"tool_use","name":"Bash","input":{"command":"cargo test --nocapture"}},{"type":"tool_use","name":"Grep","input":{"pattern":"verify_signature","path":"contracts"}},{"type":"tool_use","name":"Glob","input":{"pattern":"**/*.rs"}},{"type":"tool_use","name":"CustomTool","input":{"foo":"bar"}}]}}`,

		// Successful tool_result: consumed silently.
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"u1","is_error":false,"content":"ok"}]}}`,

		// Permission denial as string content.
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"u2","is_error":true,"content":"Claude requested permissions to use Bash, but you haven't granted it yet.\nExtra line"}]}}`,

		// Permission denial as []{type,text} content array.
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"u3","is_error":true,"content":[{"type":"text","text":"Tool not allowed in dontAsk mode\nsecond line"}]}]}}`,

		// Non-permission tool error.
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"u4","is_error":true,"content":"File does not exist: missing.rs\ntrace"}]}}`,

		// Assistant text block.
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Inspecting lock script...\n\nFINDING F1 FIRED\nCKB-WHITEHAT-RESULT: {\"verdict\":\"vulnerable\"}"}]}}`,

		// Result repeating the already-captured assistant text: must not duplicate.
		`{"type":"result","subtype":"success","is_error":false,"result":"Inspecting lock script...\n\nFINDING F1 FIRED\nCKB-WHITEHAT-RESULT: {\"verdict\":\"vulnerable\"}"}`,
	}

	for _, line := range events {
		p.FeedLine(line)
	}

	wantEmitted := []string{
		"→ Read contracts/lock.rs",
		"→ Write poc/exploit.rs",
		"→ Edit poc/exploit.rs",
		"→ Bash cargo test --nocapture",
		"→ Grep verify_signature contracts",
		"→ Glob **/*.rs",
		"→ CustomTool",
		"⚠ permission denied: Claude requested permissions to use Bash, but you haven't granted it yet.",
		"⚠ permission denied: Tool not allowed in dontAsk mode",
		"✗ tool error: File does not exist: missing.rs",
		"Inspecting lock script...",
		"FINDING F1 FIRED",
		`CKB-WHITEHAT-RESULT: {"verdict":"vulnerable"}`,
	}
	if !reflect.DeepEqual(emitted, wantEmitted) {
		t.Fatalf("emitted lines:\ngot:  %#v\nwant: %#v", emitted, wantEmitted)
	}

	wantPlain := "Inspecting lock script...\n\nFINDING F1 FIRED\nCKB-WHITEHAT-RESULT: {\"verdict\":\"vulnerable\"}\n"
	if p.PlainText() != wantPlain {
		t.Fatalf("PlainText:\ngot:  %q\nwant: %q", p.PlainText(), wantPlain)
	}

	wantActivity := strings.Join([]string{
		"→ Read contracts/lock.rs",
		"→ Write poc/exploit.rs",
		"→ Edit poc/exploit.rs",
		"→ Bash cargo test --nocapture",
		"→ Grep verify_signature contracts",
		"→ Glob **/*.rs",
		"→ CustomTool",
		"⚠ permission denied: Claude requested permissions to use Bash, but you haven't granted it yet.",
		"⚠ permission denied: Tool not allowed in dontAsk mode",
		"✗ tool error: File does not exist: missing.rs",
	}, "\n") + "\n"
	if p.Activity() != wantActivity {
		t.Fatalf("Activity:\ngot:  %q\nwant: %q", p.Activity(), wantActivity)
	}
}

func TestStreamParserClaudeResultFallbackAndError(t *testing.T) {
	t.Run("result populates empty plainText", func(t *testing.T) {
		var emitted []string
		p := NewStreamParser(func(line string) { emitted = append(emitted, line) })

		p.FeedLine(`{"type":"system","subtype":"init"}`)
		p.FeedLine(`{"type":"result","subtype":"success","is_error":false,"result":"Final verdict\nCKB-WHITEHAT-RESULT: {\"verdict\":\"clean\"}"}`)

		wantEmitted := []string{
			"Final verdict",
			`CKB-WHITEHAT-RESULT: {"verdict":"clean"}`,
		}
		if !reflect.DeepEqual(emitted, wantEmitted) {
			t.Fatalf("emitted:\ngot:  %#v\nwant: %#v", emitted, wantEmitted)
		}
		if !strings.Contains(p.PlainText(), "Final verdict") || !strings.Contains(p.PlainText(), "CKB-WHITEHAT-RESULT:") {
			t.Fatalf("unexpected PlainText: %q", p.PlainText())
		}
	})

	t.Run("result error emits agent error", func(t *testing.T) {
		var emitted []string
		p := NewStreamParser(func(line string) { emitted = append(emitted, line) })

		p.FeedLine(`{"type":"result","subtype":"error","is_error":true,"result":"API error: 401 unauthorized\ndetails"}`)

		wantEmitted := []string{"✗ agent error: API error: 401 unauthorized"}
		if !reflect.DeepEqual(emitted, wantEmitted) {
			t.Fatalf("emitted:\ngot:  %#v\nwant: %#v", emitted, wantEmitted)
		}
		if !strings.Contains(p.PlainText(), "API error: 401 unauthorized") {
			t.Fatalf("expected error detail in PlainText, got %q", p.PlainText())
		}
	})
}

func TestStreamParserCodexEvents(t *testing.T) {
	var emitted []string
	p := NewStreamParser(func(line string) {
		emitted = append(emitted, line)
	})

	events := []string{
		`{"type":"thread.started","thread_id":"th-1"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.started","item":{"id":"i1","type":"command_execution","command":"cargo test"}}`,
		`{"type":"item.completed","item":{"id":"i1","type":"command_execution","command":"cargo test"}}`,
		`{"type":"item.completed","item":{"id":"i2","type":"agent_message","text":"FINDING F2 FIRED\nCKB-WHITEHAT-RESULT: {\"verdict\":\"vulnerable\"}"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":100,"output_tokens":50}}`,
		`{"type":"error","message":"transient rate limit"}`,
		`{"type":"turn.failed","error":{"message":"sandbox denied write"}}`,
	}

	for _, line := range events {
		p.FeedLine(line)
	}

	wantEmitted := []string{
		"→ Bash cargo test",
		"FINDING F2 FIRED",
		`CKB-WHITEHAT-RESULT: {"verdict":"vulnerable"}`,
		"✗ codex error: transient rate limit",
		"✗ codex error: sandbox denied write",
	}
	if !reflect.DeepEqual(emitted, wantEmitted) {
		t.Fatalf("emitted:\ngot:  %#v\nwant: %#v", emitted, wantEmitted)
	}

	wantPlain := "FINDING F2 FIRED\nCKB-WHITEHAT-RESULT: {\"verdict\":\"vulnerable\"}\n"
	if p.PlainText() != wantPlain {
		t.Fatalf("PlainText:\ngot:  %q\nwant: %q", p.PlainText(), wantPlain)
	}
}

func TestStreamParserPlainTextPassthrough(t *testing.T) {
	var emitted []string
	p := NewStreamParser(func(line string) {
		emitted = append(emitted, line)
	})

	lines := []string{
		"plain status line",
		"",
		`CKB-WHITEHAT-RESULT: {"verdict":"clean"}`,
		"{not valid json",
		`{"no_type_field":true}`,
		`{"type":"unrecognized_custom_event","data":123}`,
	}
	for _, l := range lines {
		p.FeedLine(l)
	}

	if !reflect.DeepEqual(emitted, lines) {
		t.Fatalf("emitted:\ngot:  %#v\nwant: %#v", emitted, lines)
	}
	wantPlain := strings.Join(lines, "\n") + "\n"
	if p.PlainText() != wantPlain {
		t.Fatalf("PlainText:\ngot:  %q\nwant: %q", p.PlainText(), wantPlain)
	}
	if p.Activity() != "" {
		t.Fatalf("plain-text passthrough should not record structured activity, got %q", p.Activity())
	}
}

func TestStreamParserHugeLineAndObserverCap(t *testing.T) {
	var emitted []string
	p := NewStreamParser(func(line string) {
		emitted = append(emitted, line)
	})

	// Build >100KB JSON lines (well beyond bufio.Scanner's default 64KB limit).
	bigCmd := strings.Repeat("x", 100*1024)
	toolLine, err := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []map[string]any{
				{
					"type":  "tool_use",
					"name":  "Bash",
					"input": map[string]string{"command": bigCmd},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	bigText := strings.Repeat("a", 80*1024) + "ENDMARKER"
	textLine, err := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []map[string]any{
				{
					"type": "text",
					"text": bigText,
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	p.FeedLine(string(toolLine))
	p.FeedLine(string(textLine))

	if len(emitted) != 2 {
		t.Fatalf("expected 2 emitted lines, got %d", len(emitted))
	}
	for i, l := range emitted {
		if n := utf8.RuneCountInString(l); n > maxObserverLineRunes {
			t.Fatalf("emitted[%d] exceeded rune cap: got %d > %d", i, n, maxObserverLineRunes)
		}
	}
	if !strings.HasPrefix(emitted[0], "→ Bash ") {
		t.Fatalf("expected Bash activity prefix, got %q", emitted[0][:min(20, len(emitted[0]))])
	}
	// PlainText must NOT truncate the assistant's text so markers stay intact.
	if !strings.Contains(p.PlainText(), "ENDMARKER") || len(p.PlainText()) < len(bigText) {
		t.Fatalf("PlainText was unexpectedly truncated: len=%d", len(p.PlainText()))
	}
}

func TestStreamParserNilObserver(t *testing.T) {
	p := NewStreamParser(nil)
	p.FeedLine(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls"}},{"type":"text","text":"hello"}]}}`)
	p.FeedLine(`{"type":"item.completed","item":{"type":"agent_message","text":"world"}}`)
	p.FeedLine("plain")

	want := "hello\nworld\nplain\n"
	if p.PlainText() != want {
		t.Fatalf("PlainText:\ngot:  %q\nwant: %q", p.PlainText(), want)
	}
	if p.Activity() != "→ Bash ls\n" {
		t.Fatalf("Activity with nil observer:\ngot:  %q\nwant: %q", p.Activity(), "→ Bash ls\n")
	}
}

func TestStreamParserAgyEvents(t *testing.T) {
	var emitted []string
	p := NewStreamParser(func(line string) {
		emitted = append(emitted, line)
	})

	events := []string{
		`{"event":"init","conversation_id":"c1","init":{"cwd":"/ws","permission_mode":"request-review"}}`,
		`{"event":"step_update","step_update":{"step_index":0,"state":"DONE","step_type":"user_input"}}`,
		`{"event":"step_update","step_update":{"step_index":1,"state":"ACTIVE","step_type":"tool","tool_name":"read_file","tool_info":{"name":"read_file","parameters":{"Path":"/target/server/config.js"}}}}`,
		`{"event":"step_update","step_update":{"step_index":1,"state":"DONE","step_type":"tool","tool_name":"read_file","tool_info":{"name":"read_file","output":"ok"}}}`,
		`{"event":"step_update","step_update":{"step_index":2,"state":"ACTIVE","step_type":"tool","tool_name":"run_command","tool_info":{"name":"run_command","parameters":{"CommandLine":"node poc/f1.mjs"}}}}`,
		`{"event":"step_update","step_update":{"step_index":2,"state":"DONE","step_type":"tool","tool_name":"run_command"}}`,
		`{"event":"step_update","step_update":{"step_index":3,"state":"ACTIVE","step_type":"tool","tool_name":"run_command","tool_info":{"name":"run_command","parameters":{"CommandLine":"curl --version"}}}}`,
		`{"event":"step_update","step_update":{"step_index":3,"state":"ERROR","step_type":"tool","tool_name":"run_command","tool_info":{"name":"run_command","error":{"type":"TOOL_ERROR","message":"permission check failed for unsandboxed \"curl --version\": Permission denied."}}}}`,
		`{"event":"step_update","step_update":{"step_index":4,"state":"ACTIVE","step_type":"agent_response","text_delta":"FINDING F1 "}}`,
		`{"event":"step_update","step_update":{"step_index":4,"state":"ACTIVE","step_type":"agent_response","text_delta":"FIRED\nCKB-WHITEHAT-RESULT: "}}`,
		`{"event":"step_update","step_update":{"step_index":4,"state":"DONE","step_type":"agent_response","text_delta":"findings=1 fired=1 report=FINDINGS.md\n"}}`,
		`{"event":"result","result":{"status":"SUCCESS","response":"FINDING F1 FIRED\nCKB-WHITEHAT-RESULT: findings=1 fired=1 report=FINDINGS.md\n"}}`,
	}

	for _, e := range events {
		p.FeedLine(e)
	}

	wantEmitted := []string{
		"→ Read /target/server/config.js",
		"→ Bash node poc/f1.mjs",
		"→ Bash curl --version",
		`⚠ permission denied: permission check failed for unsandboxed "curl --version": Permission denied.`,
		"FINDING F1 FIRED",
		"CKB-WHITEHAT-RESULT: findings=1 fired=1 report=FINDINGS.md",
	}
	if !reflect.DeepEqual(emitted, wantEmitted) {
		t.Fatalf("agy emitted:\ngot:  %#v\nwant: %#v", emitted, wantEmitted)
	}
	wantPlain := "FINDING F1 FIRED\nCKB-WHITEHAT-RESULT: findings=1 fired=1 report=FINDINGS.md\n"
	if p.PlainText() != wantPlain {
		t.Fatalf("agy PlainText:\ngot:  %q\nwant: %q", p.PlainText(), wantPlain)
	}
	wantActivity := strings.Join([]string{
		"→ Read /target/server/config.js",
		"→ Bash node poc/f1.mjs",
		"→ Bash curl --version",
		`⚠ permission denied: permission check failed for unsandboxed "curl --version": Permission denied.`,
	}, "\n") + "\n"
	if p.Activity() != wantActivity {
		t.Fatalf("agy Activity:\ngot:  %q\nwant: %q", p.Activity(), wantActivity)
	}
}

func TestStreamParserAgyHeadlessDeniedActionsWarning(t *testing.T) {
	var emitted []string
	p := NewStreamParser(func(line string) { emitted = append(emitted, line) })

	p.FeedLine(`{"event":"init","conversation_id":"c2","init":{"cwd":"/ws","permission_mode":"request-review"}}`)
	p.FeedLine(`{"event":"result","result":{"status":"SUCCESS","response":"","denied_actions":[{"action":"command","display_name":"Bash"}]}}`)

	want := []string{"⚠ permission denied: agy headless run aborted by unapproved action(s): command (Bash)"}
	if !reflect.DeepEqual(emitted, want) {
		t.Fatalf("emitted:\ngot:  %#v\nwant: %#v", emitted, want)
	}
	if !strings.Contains(p.Activity(), "command (Bash)") {
		t.Fatalf("expected denied_actions recorded in Activity, got %q", p.Activity())
	}
}
