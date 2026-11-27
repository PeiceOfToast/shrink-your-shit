// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import "testing"

// quoteArg makes an argument safe to paste into a shell: plain words stay bare, anything else is single-quoted, and
// an apostrophe inside is written as '\”.
func TestQuoteArgTable(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "''"},
		{"-progress", "-progress"},
		{"plain.mkv", "plain.mkv"},
		{"a b", "'a b'"},
		{`say "hi"`, `'say "hi"'`},
		{"it's", `'it'\''s'`},
		{"/home/mb/Videos/Bob's clip.mkv", `'/home/mb/Videos/Bob'\''s clip.mkv'`},
		{"$HOME", "'$HOME'"},
		{"a\tb", "'a\tb'"},
	}
	for _, c := range cases {
		if got := quoteArg(c.in); got != c.want {
			t.Errorf("quoteArg(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// commandLine joins the quoted arguments into the line a person can copy.
func TestCommandLineQuotesEachArgument(t *testing.T) {
	got := commandLine([]string{"ffmpeg", "-i", "/media/Bob's clip.mkv", ""})
	want := `ffmpeg -i '/media/Bob'\''s clip.mkv' ''`
	if got != want {
		t.Errorf("commandLine = %q, want %q", got, want)
	}
}
