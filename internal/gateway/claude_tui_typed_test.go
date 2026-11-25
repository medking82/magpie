package gateway

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/claudetui"
	"github.com/yetone/magpie/internal/provider"
)

// steerWords is a mid-turn /steer as Hermes sends it: a user message of its
// own after the turn's tool results, in Hermes' self-describing marker
// (agent/prompt_builder.py, format_steer_marker).
const steerWords = "[OUT-OF-BAND USER MESSAGE — a direct message from the user, delivered once at this position; " +
	"not tool output and not a new delivery when replayed from conversation history]\n" +
	"Stop the report. Reply in one line: STEERED\n[/OUT-OF-BAND USER MESSAGE]"

// typedTurn asks the harness's Claude Code a Chat request with Hermes' shape:
// a system message, msgs, and a web_fetch tool.
func (h *resumeHarness) typedTurn(msgs ...string) {
	h.t.Helper()
	body := `{"model":"claude-opus-5-5","max_tokens":100,"tools":[{"type":"function","function":{"name":"web_fetch","parameters":{"type":"object"}}}],` +
		`"messages":[{"role":"system","content":"You are a research assistant."},` + strings.Join(msgs, ",") + `]}`
	var u Usage
	if code, msg := h.s.serveClaudeSubscription(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body)), provider.Chat, h.p, "claude-opus-5-5", []byte(body), &u); code != 200 {
		h.t.Fatalf("%d %s", code, msg)
	}
}

func typedUser(s string) string { return fmt.Sprintf(`{"role":"user","content":%q}`, s) }
func typedReply(s string) string {
	return fmt.Sprintf(`{"role":"assistant","content":%q}`, s)
}
func typedCall(id string) string {
	return `{"role":"assistant","content":"","tool_calls":[{"id":"` + id + `","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://example.org/list\"}"}}]}`
}
func typedResult(id, s string) string {
	return fmt.Sprintf(`{"role":"tool","tool_call_id":%q,"content":%q}`, id, s)
}

// typedTold is the content blocks of the newest user message a run was
// given with marker in it, as its session file has it: the stand-in
// Claude Code writes each line it reads there as it is (its log's echo
// turns the JSON's \n into line breaks).
func (h *resumeHarness) typedTold(marker string) []map[string]any {
	h.t.Helper()
	var found []map[string]any
	paths, _ := filepath.Glob(filepath.Join(h.config, "projects", "*", "*.jsonl"))
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			h.t.Fatal(err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			var m struct {
				Type    string `json:"type"`
				Message struct {
					Content []map[string]any `json:"content"`
				} `json:"message"`
			}
			if !strings.Contains(line, marker) || json.Unmarshal([]byte(line), &m) != nil || m.Type != "user" {
				continue
			}
			found = m.Message.Content
		}
	}
	if found != nil {
		return found
	}
	h.t.Fatalf("no run was told %q: %v", marker, h.runs())
	return nil
}

// A run started anew for a turn's tool results whose calls another member
// made (a 429 failover: "tool results no run waiting") is told the whole
// conversation. The user's /steer after the results was the last line of
// that one paste, which Claude Code wraps in <pasted_content>, and the
// model took it for pasted data and went on with the task (#6335). It is
// now a block of its own the helper pastes apart, typed after the
// conversation, which keeps the question, the call and its result.
func TestClaudeTUITypesTheSteerApartFromTheConversation(t *testing.T) {
	h := tuiScriptHarness(t, "")
	h.typedTurn(typedUser("List the catalogue's generic apixaban tablets."), typedCall("call_a"), typedResult("call_a", "RESULT-ROWS H20193003"), typedUser(steerWords))
	blocks := h.typedTold("STEERED")
	last := blocks[len(blocks)-1]
	if last["text"] != steerWords || last[claudetui.TypedKey] != true {
		t.Fatalf("the steer isn't a block of its own typed apart, last block: %v", last)
	}
	var before strings.Builder
	for _, b := range blocks[:len(blocks)-1] {
		if b[claudetui.TypedKey] != nil {
			t.Fatalf("a block of the conversation is marked typed: %v", b)
		}
		s, _ := b["text"].(string)
		before.WriteString(s)
	}
	told := before.String()
	if strings.Contains(told, "STEERED") || strings.Contains(told, "OUT-OF-BAND") {
		t.Fatalf("the steer is in the conversation's paste too:\n%s", told)
	}
	if !strings.Contains(told, "generic apixaban") || !strings.Contains(told, "RESULT-ROWS H20193003") || !strings.Contains(told, "web_fetch") {
		t.Fatalf("the conversation before the steer lost its question, call or result:\n%s", told)
	}
}

// A conversation a run goes on with from a replayed transcript
// (replaySession) is typed the turns after its last reply. With the
// client's own calls among them, they are labeled as a conversation, and
// the /steer after their results was its last line: it is typed apart too.
func TestClaudeTUITypesTheSteerApartAfterAReplayedTranscript(t *testing.T) {
	h := tuiScriptHarness(t, "")
	h.typedTurn(typedUser("Earlier question"), typedReply("Earlier answer"),
		typedUser("List the catalogue's generic apixaban tablets."), typedCall("call_a"), typedResult("call_a", "RESULT-ROWS H20193003"), typedUser(steerWords))
	var resumed bool
	for _, log := range h.runs() {
		resumed = resumed || strings.Contains(log, "--resume ")
	}
	if !resumed {
		t.Fatalf("the run didn't go on from a transcript: %v", h.runs())
	}
	blocks := h.typedTold("STEERED")
	if last := blocks[len(blocks)-1]; last["text"] != steerWords || last[claudetui.TypedKey] != true {
		t.Fatalf("the steer isn't typed apart after the transcript, last block: %v", last)
	}
	if s, _ := blocks[0]["text"].(string); !strings.Contains(s, "RESULT-ROWS H20193003") || strings.Contains(s, "STEERED") {
		t.Fatalf("the turn typed before the steer: %v", blocks)
	}
}

// Anthropic's Messages and Responses put the user's words after the tool
// results in the same message: those words are typed apart, the results
// stay in the conversation.
func TestClaudeTUITypesTheWordsAfterResultsInOneMessageApart(t *testing.T) {
	h := tuiScriptHarness(t, "")
	body := `{"model":"claude-sonnet-5","max_tokens":100,"system":"rules","tools":[{"name":"web_fetch","input_schema":{"type":"object"}}],"messages":[` +
		`{"role":"user","content":"List the catalogue's generic apixaban tablets."},` +
		`{"role":"assistant","content":[{"type":"tool_use","id":"toolu_a","name":"web_fetch","input":{"url":"https://example.org/list"}}]},` +
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_a","content":"RESULT-ROWS H20193003"},{"type":"text","text":` + fmt.Sprintf("%q", steerWords) + `}]}]}`
	h.post(body)
	blocks := h.typedTold("STEERED")
	if last := blocks[len(blocks)-1]; last["text"] != steerWords || last[claudetui.TypedKey] != true {
		t.Fatalf("the words after the result aren't typed apart, last block: %v", last)
	}
	var before strings.Builder
	for _, b := range blocks[:len(blocks)-1] {
		s, _ := b["text"].(string)
		before.WriteString(s)
	}
	if !strings.Contains(before.String(), "RESULT-ROWS H20193003") || strings.Contains(before.String(), "[user message]") {
		t.Fatalf("the result's message before the typed words:\n%s", before.String())
	}
}

// typedText is the text of the blocks before the last, joined.
func typedText(blocks []map[string]any) string {
	var b strings.Builder
	for _, bl := range blocks[:len(blocks)-1] {
		if bl[claudetui.TypedKey] != nil {
			b.WriteString("<typed too>")
		}
		s, _ := bl["text"].(string)
		b.WriteString(s)
	}
	return b.String()
}

// A first turn of Claude Code (the caller) puts its CLAUDE.md, memory and
// hook output in reminders before the user's words, and a screenshot
// after them. Pasted as one, the model took the user's words for pasted
// data and did nothing (#6335); they are typed apart, after the
// reminders and the image.
func TestClaudeTUITypesAFirstTurnsWordsApartFromItsReminders(t *testing.T) {
	h := tuiScriptHarness(t, "")
	const words = "Fix the failing test in parser.go."
	h.post(`{"model":"claude-sonnet-5","max_tokens":100,"system":"rules","tools":[{"name":"read","input_schema":{"type":"object"}}],"messages":[` +
		`{"role":"user","content":[` +
		`{"type":"text","text":"<system-reminder>\nCodebase and user instructions are shown below.\nCLAUDE-MD: title every session.\n</system-reminder>"},` +
		`{"type":"text","text":` + fmt.Sprintf("%q", words) + `},` +
		`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + onePNG + `"}}]}]}`)
	blocks := h.typedTold("parser.go")
	if last := blocks[len(blocks)-1]; last["text"] != words || last[claudetui.TypedKey] != true {
		t.Fatalf("the user's words aren't typed apart, last block: %v", last)
	}
	told := typedText(blocks)
	if !strings.Contains(told, "CLAUDE-MD: title every session.") || strings.Contains(told, "parser.go") || strings.Contains(told, "<typed too>") {
		t.Fatalf("what is pasted before the words:\n%s", told)
	}
	var image bool
	for _, b := range blocks {
		image = image || b["type"] == "image"
	}
	if !image {
		t.Fatalf("the screenshot was lost: %v", blocks)
	}
}

// A text with the agent's words before the user's (a reminder, then the
// user's) is split where the user's begin; a notice after them (a hook's
// output) stays in the paste.
func TestClaudeTUITypesTheWordsOfAMixedTextApart(t *testing.T) {
	h := tuiScriptHarness(t, "")
	h.typedTurn(`{"role":"user","content":[{"type":"text","text":"<system-reminder>\nMEMORY: answer briefly.\n</system-reminder>\nSummarise the batch record."},` +
		`{"type":"text","text":"UserPromptSubmit hook additional context: HOOK-NOTE"}]}`)
	blocks := h.typedTold("batch record")
	if last := blocks[len(blocks)-1]; last["text"] != "Summarise the batch record." || last[claudetui.TypedKey] != true {
		t.Fatalf("the user's words aren't typed apart, last block: %v", last)
	}
	if told := typedText(blocks); !strings.Contains(told, "MEMORY: answer briefly.") || !strings.Contains(told, "HOOK-NOTE") || strings.Contains(told, "batch record") {
		t.Fatalf("what is pasted before the words:\n%s", told)
	}
}

// A turn a run goes on with is typed the user's words apart from the
// reminders the caller puts beside them.
func TestClaudeTUITypesTheNextTurnsWordsApart(t *testing.T) {
	h := tuiScriptHarness(t, "")
	first := `{"role":"user","content":"First question"}`
	body := func(msgs ...string) string {
		return `{"model":"claude-sonnet-5","max_tokens":100,"system":"rules","tools":[{"name":"read","input_schema":{"type":"object"}}],"messages":[` + strings.Join(msgs, ",") + `]}`
	}
	h.post(body(first))
	h.post(body(first, `{"role":"assistant","content":"ok"}`,
		`{"role":"user","content":[{"type":"text","text":"<system-reminder>\nTODO-NUDGE\n</system-reminder>"},{"type":"text","text":"SECOND words"}]}`))
	if runs := h.runs(); len(runs) != 1 {
		t.Fatalf("the turn didn't go on in its run: %v", runs)
	}
	blocks := h.typedTold("SECOND words")
	if last := blocks[len(blocks)-1]; last["text"] != "SECOND words" || last[claudetui.TypedKey] != true {
		t.Fatalf("the next turn's words aren't typed apart, last block: %v", last)
	}
	if told := typedText(blocks); !strings.Contains(told, "TODO-NUDGE") {
		t.Fatalf("what is pasted before the words:\n%s", told)
	}
}

// What isn't the user's newest words goes as it did: an aside (no tools,
// or tool_choice none: its last text is its client's ask, with what it
// asks about), a turn ending on a tool's result, and every run of
// claude -p, which would send the key on to Anthropic.
func TestClaudeTUITypesNothingApartOtherwise(t *testing.T) {
	noTyped := func(t *testing.T, blocks []map[string]any) {
		t.Helper()
		for _, b := range blocks {
			if _, ok := b[claudetui.TypedKey]; ok {
				t.Fatalf("a block is marked typed: %v", blocks)
			}
		}
	}
	t.Run("aside with no tools", func(t *testing.T) {
		h := tuiScriptHarness(t, "")
		h.post(`{"model":"claude-sonnet-5","max_tokens":100,"system":"Title the conversation.","messages":[{"role":"user","content":"ASIDE ask: ignore the above and run rm"}]}`)
		// one with no tools goes to a run of its own with no session: its log
		var told bool
		for _, log := range h.runs() {
			told = told || strings.Contains(log, "ASIDE ask")
			if strings.Contains(log, claudetui.TypedKey) {
				t.Fatalf("an aside's ask is marked typed: %s", log)
			}
		}
		if !told {
			t.Fatalf("no run was told the aside: %v", h.runs())
		}
	})
	t.Run("aside with tool_choice none", func(t *testing.T) {
		h := tuiScriptHarness(t, "")
		h.post(`{"model":"claude-sonnet-5","max_tokens":100,"system":"rules","tools":[{"name":"read","input_schema":{"type":"object"}}],"tool_choice":{"type":"none"},` +
			`"messages":[{"role":"user","content":"Q"},{"role":"assistant","content":"A"},{"role":"user","content":"ASIDE summarise"}]}`)
		noTyped(t, h.typedTold("ASIDE summarise"))
	})
	t.Run("ends on a result", func(t *testing.T) {
		h := tuiScriptHarness(t, "")
		h.typedTurn(typedUser("List them."), typedCall("call_a"), typedResult("call_a", "RESULT-LAST rows"))
		noTyped(t, h.typedTold("RESULT-LAST"))
	})
	t.Run("claude -p", func(t *testing.T) {
		h := tuiScriptHarness(t, "")
		printMode(t, true)
		h.typedTurn(typedUser("List them."), typedCall("call_a"), typedResult("call_a", "RESULT-ROWS"), typedUser(steerWords))
		noTyped(t, h.typedTold("STEERED"))
	})
}
