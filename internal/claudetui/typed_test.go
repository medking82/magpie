package claudetui

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// A text block marked TypedKey is pasted apart from the words before it,
// with no line break before it, so a short one is taken in as typed words
// and not as one more line of the conversation's paste (#6335). Blocks
// with no mark are joined as before.
func TestUserPiecesPasteTypedWordsApart(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	text := func(s string) map[string]any { return map[string]any{"type": "text", "text": s} }
	typed := func(s string) map[string]any { return map[string]any{"type": "text", "text": s, TypedKey: true} }
	image := map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": png}}
	cases := []struct {
		name   string
		blocks []map[string]any
		want   []string
	}{
		{"after the conversation", []map[string]any{text("Human: a\n\nAssistant: b"), typed("STEER")},
			[]string{"Human: a\n\nAssistant: b\n\n", "STEER"}},
		{"after chunks of the conversation", []map[string]any{text("one"), text("two"), typed("STEER")},
			[]string{"one\n\ntwo\n\n", "STEER"}},
		{"after an image", []map[string]any{text("look"), image, typed("STEER")},
			[]string{"look\n\n", "<image>", "STEER"}},
		{"none marked", []map[string]any{text("one"), text("two")},
			[]string{"one\n\ntwo"}},
		{"folding, own words after", []map[string]any{text("conversation"), typed("line 1\nline 2\nline 3\nline 4")},
			[]string{"conversation\n\n", "line 1\nline 2\nline 3\nline 4", ownWords}},
		{"over 800, own words after", []map[string]any{typed(strings.Repeat("字", 801))},
			[]string{strings.Repeat("字", 801), ownWords}},
		{"two line breaks stay typed", []map[string]any{typed("a\nb\nc")},
			[]string{"a\nb\nc"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, _ := json.Marshal(c.blocks)
			s := &shim{}
			pieces, err := s.userPieces(raw)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, p := range pieces {
				if p.image != "" {
					got = append(got, "<image>")
				} else {
					got = append(got, p.text)
				}
			}
			if !slices.Equal(got, c.want) {
				t.Fatalf("pieces %q, want %q", got, c.want)
			}
		})
	}
}
