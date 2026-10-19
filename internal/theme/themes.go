package theme

import (
	"fmt"
	"image/color"
	"os"
	"slices"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/discorgento/ouija/internal/ouijaconfig"
)

// palette is one theme's colors; the zero ones keep ouija's.
type palette struct {
	pink, blush, lavender, mauve, violet, purple  string
	silver, gray, muted                           string
	green, greenDeep, amber, orange, red, redDeep string
	diffAddBg, diffDelBg                          string
	gradient                                      []string
}

// themes are the palettes to choose from (OUIJA_THEME, or "theme" in the
// global config — /theme in the chat sets it).
var themes = map[string]palette{
	"ouija": {},
	"catppuccin": {
		pink: "#f5c2e7", blush: "#f2cdcd", lavender: "#b4befe", mauve: "#cba6f7", violet: "#89b4fa", purple: "#cba6f7",
		silver: "#bac2de", gray: "#7f849c", muted: "#6c7086",
		green: "#a6e3a1", greenDeep: "#40a02b", amber: "#f9e2af", orange: "#fab387", red: "#f38ba8", redDeep: "#d20f39",
		diffAddBg: "#1e3a2a", diffDelBg: "#3f1d2b",
		gradient: []string{"#f5c2e7", "#cba6f7", "#89b4fa", "#94e2d5", "#cba6f7", "#f5c2e7"},
	},
	"dracula": {
		pink: "#ff79c6", blush: "#ffb8e0", lavender: "#bd93f9", mauve: "#a580e8", violet: "#8be9fd", purple: "#bd93f9",
		silver: "#d6d6d6", gray: "#7d86b0", muted: "#6272a4",
		green: "#50fa7b", greenDeep: "#2fa84f", amber: "#f1fa8c", orange: "#ffb86c", red: "#ff5555", redDeep: "#c53b3b",
		diffAddBg: "#173d26", diffDelBg: "#45202a",
		gradient: []string{"#ff79c6", "#bd93f9", "#8be9fd", "#50fa7b", "#bd93f9", "#ff79c6"},
	},
	"mono": {
		pink: "#e4e4e4", blush: "#d0d0d0", lavender: "#c6c6c6", mauve: "#a8a8a8", violet: "#bcbcbc", purple: "#dadada",
		silver: "#bcbcbc", gray: "#8a8a8a", muted: "#6c6c6c",
		green: "#87af87", greenDeep: "#5f875f", amber: "#d7d7af", orange: "#d7af87", red: "#d78787", redDeep: "#af5f5f",
		diffAddBg: "#203020", diffDelBg: "#302020",
		gradient: []string{"#ffffff", "#bcbcbc", "#808080", "#bcbcbc", "#ffffff"},
	},
}

// Current is the theme in use.
var Current = "ouija"

// NoMotion stops the animations (the intro, the flowing gradients, the
// shimmer): OUIJA_NO_ANIMATION=1, for a still screen or a screen reader.
var NoMotion = os.Getenv("OUIJA_NO_ANIMATION") != ""

// Names lists the themes, ouija first.
func Names() []string {
	names := make([]string, 0, len(themes))
	for n := range themes {
		if n != "ouija" {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	return append([]string{"ouija"}, names...)
}

// The theme is chosen before any package builds its styles from the colors
// (the packages that import theme initialize after it).
func init() {
	name := os.Getenv("OUIJA_THEME")
	// Tests get ouija's colors, whatever the developer chose.
	if name == "" && !testing.Testing() {
		if cfg, err := ouijaconfig.Load(); err == nil && cfg != nil {
			name = cfg.Theme
		}
	}
	apply(name)
}

// apply sets the colors of the named theme (ouija's for an unknown name).
func apply(name string) {
	p, ok := themes[strings.ToLower(name)]
	if !ok {
		return
	}
	Current = strings.ToLower(name)
	set := func(dst *color.Color, hex string) {
		if hex != "" {
			*dst = lipgloss.Color(hex)
		}
	}
	set(&Pink, p.pink)
	set(&Blush, p.blush)
	set(&Lavender, p.lavender)
	set(&Mauve, p.mauve)
	set(&Violet, p.violet)
	set(&Purple, p.purple)
	set(&Silver, p.silver)
	set(&Gray, p.gray)
	set(&Muted, p.muted)
	set(&Green, p.green)
	set(&GreenDeep, p.greenDeep)
	set(&Amber, p.amber)
	set(&Orange, p.orange)
	set(&Red, p.red)
	set(&RedDeep, p.redDeep)
	set(&DiffAddBg, p.diffAddBg)
	set(&DiffDelBg, p.diffDelBg)
	Rainbow = []color.Color{Pink, Lavender, Violet, Purple, Mauve, Blush}
	if len(p.gradient) > 0 {
		gradientStops = gradientStops[:0]
		for _, h := range p.gradient {
			gradientStops = append(gradientStops, lipgloss.Color(h))
		}
	}
}

// Save makes name the theme of the next start (global config).
func Save(name string) error {
	name = strings.ToLower(name)
	if _, ok := themes[name]; !ok {
		return fmt.Errorf("unknown theme %q (themes: %s)", name, strings.Join(Names(), ", "))
	}
	cfg, err := ouijaconfig.Load()
	if err != nil {
		return err
	}
	if cfg == nil {
		cfg = &ouijaconfig.Config{}
	}
	cfg.Theme = name
	return ouijaconfig.Save(cfg)
}

// Hex renders c as #rrggbb (for styles written as text, like glamour's).
func Hex(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

// Swatch shows the named theme's main colors as blocks, for a picker.
func Swatch(name string) string {
	p, ok := themes[name]
	if !ok {
		return ""
	}
	cols := []string{p.pink, p.lavender, p.violet, p.green, p.amber, p.red}
	defaults := []color.Color{lipgloss.Color("177"), lipgloss.Color("141"), lipgloss.Color("99"), lipgloss.Color("118"), lipgloss.Color("220"), lipgloss.Color("196")}
	var b strings.Builder
	for i, h := range cols {
		c := defaults[i]
		if h != "" {
			c = lipgloss.Color(h)
		}
		b.WriteString(lipgloss.NewStyle().Foreground(c).Render("■"))
	}
	return b.String()
}
