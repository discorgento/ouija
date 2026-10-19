package chat

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
)

// ─── Syntax highlighting of diff lines ────────────────────────────────

// codeStyle is the palette of highlighted code — the one the reply's code
// blocks use (ouijaStyleJSON), with no background of its own.
var codeStyle = chroma.MustNewStyle("ouija", chroma.StyleEntries{
	chroma.Comment:             "italic #6c6c8a",
	chroma.CommentPreproc:      "#ff87d7",
	chroma.Keyword:             "bold #af87ff",
	chroma.KeywordNamespace:    "#ff5fd7",
	chroma.KeywordType:         "#87afff",
	chroma.Operator:            "#d7afff",
	chroma.Punctuation:         "#9e9e9e",
	chroma.NameBuiltin:         "#ff87d7",
	chroma.NameTag:             "#af87ff",
	chroma.NameAttribute:       "#87d7ff",
	chroma.NameClass:           "bold #ffd787",
	chroma.NameDecorator:       "#ffd787",
	chroma.NameFunction:        "#87d7ff",
	chroma.LiteralNumber:       "#ffaf5f",
	chroma.LiteralString:       "#afd787",
	chroma.LiteralStringEscape: "#87ffd7",
})

// lexerFor is the highlighter for a file, by its name (nil: none known).
func lexerFor(path string) chroma.Lexer {
	if path == "" {
		return nil
	}
	l := lexers.Match(path)
	if l == nil {
		return nil
	}
	return chroma.Coalesce(l)
}

// highlightCode highlights one line of code ("" when there is no lexer or it
// fails: the caller shows it plain). A line is lexed alone, so a construct
// spanning lines (a block comment) may color off — a diff shows fragments.
func highlightCode(lexer chroma.Lexer, code string) string {
	if lexer == nil || strings.TrimSpace(code) == "" {
		return ""
	}
	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		return ""
	}
	var b strings.Builder
	if err := formatters.TTY16m.Format(&b, codeStyle, it); err != nil {
		return ""
	}
	return strings.TrimRight(b.String(), "\n")
}
