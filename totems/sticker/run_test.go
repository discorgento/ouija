package sticker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/engine/enginetest"
	"github.com/discorgento/ouija/registry"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectVersions(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "composer.json"), `{"require": {
		"magento/product-enterprise-edition": "^2.4.7",
		"php": "~8.3.0",
		"hyva-themes/magento2-theme-module": "1.1",
		"hyva-themes/magento2-default-theme": "^1.3.9",
		"hyva-themes/magento2-compat-module-fallback": "9.9"}}`)
	writeFile(t, filepath.Join(root, "app/code/Acme/Pay/registration.php"), "<?php")
	writeFile(t, filepath.Join(root, "app/code/Acme/Ship/registration.php"), "<?php")
	writeFile(t, filepath.Join(root, "app/code/Acme/NotAModule/README"), "x")

	for range 10 { // the theme pick used to vary with map order
		d := detectVersions(root)
		if d.platform != "2.4.7" || d.theme != "1.3.9" || d.modules != 2 || !strings.HasPrefix(d.php, "8.3") {
			t.Fatalf("detected %+v", d)
		}
	}
	if d := detectVersions(t.TempDir()); d != (detected{}) {
		t.Fatalf("no composer.json: %+v", d)
	}
}

// Scout on a scripted model → an escaped SVG under docs/sticker/, with the
// versions read from composer.json, not from the model.
func TestRunWritesSticker(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "composer.json"), `{"require": {"magento/product-community-edition": "2.4.6"}}`)
	p := &enginetest.Provider{Respond: func(req engine.ChatRequest) enginetest.Reply {
		return enginetest.Submit(map[string]any{"projectName": "Acme Shop", "projectDescription": "Pets & toys",
			"techTags": []any{"PHP", "Magento"}, "platformVersion": "9.9.9"})
	}}
	out, err := registry.Invoke(context.Background(), enginetest.NewEngine(p, root), &Totem{}, registry.Args{})
	if err != nil {
		t.Fatal(err)
	}
	path := out.(*stickerResult).SVG
	svg, err := os.ReadFile(path)
	if err != nil || !strings.HasSuffix(path, filepath.Join("docs", "sticker", "acme-shop.sticker.svg")) {
		t.Fatalf("svg %s: %v", path, err)
	}
	if !strings.Contains(string(svg), "Pets &amp; toys") || !strings.Contains(string(svg), "2.4.6") || strings.Contains(string(svg), "9.9.9") {
		t.Fatalf("svg content wrong")
	}
}
