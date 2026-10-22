🔮 ouija docs · [Index](index.md)

---

# Style guide

> Formatting rules for every page under `docs/`, plus the skeleton for a new
> one. Audience: developer

## Rules

- **Title**: `# Title Case`, no period.
- **Tagline**: `>` blockquote, one line describing the page + `Audience:`
  label — one of `user`, `developer`, `reference`.
- **Sections**: `## Sentence case`.
- **Subsections**: `### Sentence case`.
- **Code**: always a language tag — `go`, `sh`, `json`, `diff`, `console`.
- **Emphasis**: `**bold**` for UI labels, `` `code` `` for filenames, commands
  and types.
- **Lists**: `-` unordered, `1.` ordered.
- **Header**: first line is `🔮 ouija docs · [Index](<relative index>)`.
- **Footer**: `🔮 ouija docs — [Index](<relative index>) · [<Next>](<file>)`,
  pointing at the natural next page. A trailing newline is mandatory.

## Which quadrant does a page belong to?

Write for **one** reader intent — if a page tries to do two, split it:

| Quadrant | Reader asks | Verb |
|---|---|---|
| Tutorial | "teach me" | guided, one path, runnable end to end |
| How-to | "how do I do X" | task-oriented, assumes you know the basics |
| Reference | "what is this" | factual, complete, no opinion, no prose |
| Explanation | "why / how does it work" | context, design decisions, trade-offs |

**Single source rule**: a fact (a flag, a slash command, an env var, a config
field) is documented in exactly one reference page; everything else links to it.

## Skeleton

```markdown
🔮 ouija docs · [Index](../index.md)

---

# Title Case

> Brief description — one line explaining what this document covers.
> Audience: [user | developer | reference]

## Section

Content...

### Subsection

Code example:

```go
func Hello() string {
    return "world"
}
```

---

🔮 ouija docs — [Index](../index.md) · [Related](related.md)
```

## Images

Every screenshot and GIF uses the same markup, centred and 800 pixels wide,
with an `alt` text that says what the image shows:

```html
<p align="center">
  <img src="../../assets/screens/chat-welcome.png" alt="What it shows" width="800">
</p>
```

Screenshots of the chat come from `make screens`, never from a hand-made
capture, so they share one size, font and frame and show the current version.
The banner keeps its own width.

## Links

Relative links only (no absolute paths, no `https://` for internal pages), so
the tree renders on GitHub, in an editor preview and on any future docs site.

Relative links **and** their `#anchors` are checked by `go test ./...`
(`internal/doccheck`) — a link broken by a rename fails CI.

---

🔮 ouija docs — [Index](index.md)
