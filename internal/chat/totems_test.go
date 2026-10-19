package chat

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/discorgento/ouija/engine"
	_ "github.com/discorgento/ouija/totems" // the totems the chat offers
)

// Every registered totem is a slash command and is listed on the welcome.
func TestTotemsAreOffered(t *testing.T) {
	cmds := slashCommands()
	for _, name := range []string{"/task <what>", "/doc", "/sticker [what]"} {
		found := false
		for _, c := range cmds {
			found = found || strings.HasPrefix(c.cmd, name)
		}
		if !found {
			t.Errorf("no %q command in %v", name, cmds)
		}
	}
	// The welcome points at /task; /help lists every totem.
	if out := ansi.Strip(renderWelcomeBanner(0, 120, -1, welcomeInfo{})); !strings.Contains(out, "/task") {
		t.Errorf("welcome lacks /task:\n%s", out)
	}
	help := ansi.Strip(renderHelp(slashCommands(), 160))
	for _, want := range []string{"/task", "/doc", "/sticker"} {
		if !strings.Contains(help, want) {
			t.Errorf("/help lacks %q:\n%s", want, help)
		}
	}
}

// /learn saves the fact to the project's knowledge and tells the model now.
func TestLearnInTheChat(t *testing.T) {
	root := t.TempDir()
	m := newModel(&switchEngine{cfg: &engine.Config{ProjectRoot: root}}, nil, "base prompt", "")
	m.handleSlash("/learn")
	if !strings.Contains(m.lines[len(m.lines)-1].text, "Nothing learned") {
		t.Fatalf("empty: %q", m.lines[len(m.lines)-1].text)
	}
	m.handleSlash("/learn the API uses snake_case")
	if !strings.Contains(m.system, knownFactsHeading+"\n- the API uses snake_case") || m.welcome.facts != 1 {
		t.Fatalf("system = %q, facts = %d", m.system, m.welcome.facts)
	}
	m.handleSlash("/learn")
	if !strings.Contains(m.lines[len(m.lines)-1].text, "snake_case") {
		t.Fatalf("listing: %q", m.lines[len(m.lines)-1].text)
	}
}
