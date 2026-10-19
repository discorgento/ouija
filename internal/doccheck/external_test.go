package doccheck

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// externalEnv gates the network-dependent check. Local `make check` stays fast
// and offline; CI runs it as its own job (see .github/workflows/ci.yml).
const externalEnv = "DOCCHECK_EXTERNAL"

// hrefRE catches absolute links written as HTML attributes — the README badges
// are `href`/`src`, not Markdown links.
var hrefRE = regexp.MustCompile(`(?:href|src)="(https?://[^"]+)"`)

// TestExternalLinksReachable verifies that every absolute http(s) link in the
// documentation still resolves. Unlike the rest of the package it needs the
// network, so it only runs when DOCCHECK_EXTERNAL=1 is set.
func TestExternalLinksReachable(t *testing.T) {
	if os.Getenv(externalEnv) == "" {
		t.Skipf("set %s=1 to verify absolute links over the network", externalEnv)
	}

	root := repoRoot(t)
	uses := map[string][]string{} // URL → pages that link to it
	for _, file := range docFiles(t, root) {
		rel, err := filepath.Rel(root, file)
		if err != nil {
			t.Fatalf("rel: %v", err)
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		body := inlineCodeRE.ReplaceAllString(stripNoise(string(raw)), "")

		var targets []string
		for _, m := range linkRE.FindAllStringSubmatch(body, -1) {
			targets = append(targets, m[1])
		}
		for _, m := range hrefRE.FindAllStringSubmatch(body, -1) {
			targets = append(targets, m[1])
		}
		for _, target := range targets {
			if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
				continue
			}
			uses[target] = append(uses[target], rel)
		}
	}

	urls := make([]string, 0, len(uses))
	for u := range uses {
		urls = append(urls, u)
	}
	sort.Strings(urls)

	client := &http.Client{Timeout: 15 * time.Second}
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		failures []string
	)
	sem := make(chan struct{}, 8) // stay polite: at most 8 concurrent requests
	for _, u := range urls {
		u := u
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if err := probe(client, u); err != nil {
				mu.Lock()
				failures = append(failures, fmt.Sprintf("%s → %v (%s)", u, err, strings.Join(uses[u], ", ")))
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(failures) > 0 {
		sort.Strings(failures)
		for _, f := range failures {
			t.Errorf("broken external link: %s", f)
		}
	}
	t.Logf("checked %d unique external links in %d documents", len(urls), len(docFiles(t, root)))
}

// probe fetches url, retrying once on transport errors and 5xx. 401/403/429
// count as reachable: they depend on the caller (bot protection, rate limit),
// not on the documentation being wrong.
func probe(client *http.Client, url string) error {
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return err // malformed URL: retrying cannot help
		}
		req.Header.Set("User-Agent", "ouija-doccheck/1.0 (+https://github.com/discorgento/ouija)")
		req.Header.Set("Accept", "*/*")

		resp, err := client.Do(req)
		if err != nil {
			last = err
			continue
		}
		resp.Body.Close() // only the status matters; never read the body
		code := resp.StatusCode
		switch {
		case code >= 200 && code < 400,
			code == http.StatusUnauthorized,
			code == http.StatusForbidden,
			code == http.StatusTooManyRequests:
			return nil
		case code >= 500:
			last = fmt.Errorf("HTTP %d", code)
		default:
			return fmt.Errorf("HTTP %d", code)
		}
	}
	if last == nil {
		last = fmt.Errorf("gave up")
	}
	return last
}
