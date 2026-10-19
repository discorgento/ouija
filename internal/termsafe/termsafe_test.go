package termsafe

import "testing"

func TestClean(t *testing.T) {
	for in, want := range map[string]string{
		"plain\n\ttext é 🔮":                 "plain\n\ttext é 🔮",
		"\x1b]52;c;Y3VybCBldmlsfHNo\x07git": "]52;c;Y3VybCBldmlsfHNogit",
		"git\x1b[1A\x1b[2K\r  tools":        "git[1A[2K  tools",
		"a\x7fb\u009bc":                     "abc",
	} {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}
