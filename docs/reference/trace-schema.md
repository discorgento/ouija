🔮 ouija docs · [Index](../index.md)

---

# Trace schema

> Chrome Trace Event (Perfetto) format for `--trace-file <file.json>`. Audience: reference

`--trace-file <file>` writes a **Chrome Trace Event** JSON document — a single
JSON array of complete events, viewable in `chrome://tracing` or
[ui.perfetto.dev](https://ui.perfetto.dev). It works on any totem and on
`ouija chat`. Every run also renders the span tree to stderr; the file is an
additional export.

## Event format

The file is one JSON array; each element is a complete (`ph: "X"`) event.

| Field | Type | Always | Description |
|---|---|---|---|
| `name` | `string` | Yes | Span name: `chat`, `phase2`, `tool.<tool>`, or an agent label |
| `cat` | `string` | Yes | Category: `chat`, `tool`, or `agent` |
| `ph` | `string` | Yes | Event phase — always `"X"` (complete) |
| `ts` | `int` | Yes | Start time, microseconds since the Unix epoch |
| `dur` | `int` | No | Duration in microseconds (omitted when zero) |
| `pid` | `int` | Yes | Process id (always `1`) |
| `tid` | `int` | Yes | Track id — encodes nesting (root `0`, child `tid*100 + i + 1`) |
| `args` | `map[string]string` | No | Attributes (omitted when empty) |

Attribute values are always strings. A `chat`/`phase2` span carries
`input_tokens` and `output_tokens`; a `tool.*` span carries `tool` and, for file
tools, `path`; a failed span adds `error`.

The category is derived from the name: `chat`/`phase2` → `chat`, a `tool.` prefix
→ `tool`, anything else → `agent`.

## Example

```json
[
  {
    "name": "chat",
    "cat": "chat",
    "ph": "X",
    "ts": 1750000000000000,
    "dur": 230000,
    "pid": 1,
    "tid": 0,
    "args": {"input_tokens": "4523", "output_tokens": "891"}
  },
  {
    "name": "tool.read_file",
    "cat": "tool",
    "ph": "X",
    "ts": 1750000000100000,
    "dur": 80000,
    "pid": 1,
    "tid": 1,
    "args": {"tool": "read_file", "path": "engine/trace.go"}
  }
]
```

Open the file with `chrome://tracing` (Load) or drag it onto ui.perfetto.dev.

---

🔮 ouija docs — [Index](../index.md) · [Config schema](config-schema.md)
