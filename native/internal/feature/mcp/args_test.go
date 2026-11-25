package mcp

import (
	"reflect"
	"testing"
)

// An edited server must send back the exact arguments it was loaded with.
// Joining with spaces and splitting on whitespace broke any argument that
// contains a space, even when the user only toggled "enabled".
func TestDraftArgsRoundTrip(t *testing.T) {
	original := []string{
		"-y", "srv", "--root=/tmp/My Project", "",
		`C:\Program Files\srv.exe`, "it's", `say "hi"`, `a\b`, `\\server\share`,
	}
	view := ServerView{Name: "srv", Type: "stdio", Command: "npx", Args: original}
	draft := DraftFromServer(view)
	if draft.ArgsInput != JoinArgs(original) {
		t.Fatalf("ArgsInput = %q, want %q", draft.ArgsInput, JoinArgs(original))
	}
	// The user changes nothing but the enabled flag.
	draft.Enabled = false
	payload := DraftPayload(&draft)
	if got := payload["args"]; !reflect.DeepEqual(got, original) {
		t.Fatalf("saved args = %#v, want the loaded args unchanged", got)
	}
	if payload["enabled"] != false {
		t.Fatalf("enabled = %v, want false", payload["enabled"])
	}
}

func TestSplitArgsFollowsQuoting(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty", "", nil},
		{"whitespace only", "   ", nil},
		{"separators", "-y  server\t-x", []string{"-y", "server", "-x"}},
		// A backslash on its own is literal, so Windows paths read as written.
		{"windows path", `serve C:\data\config.json`, []string{"serve", `C:\data\config.json`}},
		{"unc path", `\\server\share\x`, []string{`\\server\share\x`}},
		{"trailing backslash", `C:\dir\ next`, []string{`C:\dir\`, "next"}},
		// Quotes keep whitespace inside one argument, adjacent or not.
		{"attached double quotes", `--root="/tmp/My Project" x`, []string{"--root=/tmp/My Project", "x"}},
		{"single quotes and escapes", `'it'\''s' "a b"`, []string{"it's", "a b"}},
		{"empty argument", `'' empty=""`, []string{"", "empty="}},
		{"escaped quote", `"say \"hi\""`, []string{`say "hi"`}},
		{"non-ascii", "中文 --name=\"zh 文 件\"", []string{"中文", "--name=zh 文 件"}},
		// An unterminated quote keeps the rest of the line.
		{"unterminated quote", `--note "unterminated`, []string{"--note", "unterminated"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SplitArgs(tc.input)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("SplitArgs(%q) = %#v, want %#v", tc.input, got, tc.want)
			}
		})
	}
}

func TestJoinArgsIsRecoveredBySplitArgs(t *testing.T) {
	args := []string{
		"-y", "srv", "/tmp/My Project", "",
		`C:\Program Files\srv.exe`, "it's", `say "hi"`, `a\b`, "line\nbreak", `\\server\share`,
		`"`, `'`, `\`, "  ",
	}
	joined := JoinArgs(args)
	if got := SplitArgs(joined); !reflect.DeepEqual(got, args) {
		t.Fatalf("SplitArgs(JoinArgs(args)) = %#v, want %#v (joined %q)", got, args, joined)
	}
	// Plain arguments are written as they are so an existing server reads
	// naturally; only what needs quoting is quoted.
	if got := JoinArgs([]string{"-y", "srv"}); got != "-y srv" {
		t.Fatalf("plain args = %q, want %q", got, "-y srv")
	}
	if got := JoinArgs([]string{"/tmp/My Project"}); got != "'/tmp/My Project'" {
		t.Fatalf("quoted arg = %q, want %q", got, "'/tmp/My Project'")
	}
}
