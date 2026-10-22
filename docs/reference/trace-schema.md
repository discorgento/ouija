🔮 ouija docs · [Index](../index.md)

---

# Trace schema

> Chrome Trace Event (Perfetto) format for `--trace-file <file.json>`. Audience: reference

`--trace-file <file>` writes a **Chrome Trace Event** JSON document: one JSON
array of complete events that you can open in `chrome://tracing` or
[ui.perfetto.dev](https://ui.perfetto.dev). Each event is a span, one timed
step of a run (a model call, a tool call, an agent). It works with any totem
and with `ouija chat`. Every run also prints the span tree to stderr; the file
is an extra export.

The file is written with mode `0600` (an existing file is narrowed to it):
spans name the paths and commands of the run. A device such as `/dev/stdout`
is left as it is.

## Event format

The file is one JSON array, and each element is a complete (`ph: "X"`) event.

| Field | Type | Always | Description |
|---|---|---|---|
| `name` | `string` | Yes | Span name: `chat`, `phase2`, `tool.<tool>`, or an agent label |
| `cat` | `string` | Yes | Category: `chat`, `tool`, or `agent` |
| `ph` | `string` | Yes | Event phase — always `"X"` (complete) |
| `ts` | `int` | Yes | Start time, microseconds since the Unix epoch |
| `dur` | `int` | No | Duration in microseconds (omitted when zero) |
| `pid` | `int` | Yes | Process id (always `1`) |
| `tid` | `int` | Yes | Track id, which encodes nesting (root `0`, child `tid*100 + i + 1`) |
| `args` | `map[string]string` | No | Attributes (omitted when empty) |

Attribute values are always strings. A `chat`/`phase2` span carries
`input_tokens` and `output_tokens`, plus `label` when the call has one (a
totem pass) and `submit_result: "true"` when the model answered through
`submit_result`. A `tool.*` span carries `tool` and, when
the call has them, `path`, `pattern` (`grep`, `glob`) and `command` (`bash`:
its first line, cut to 120 bytes). A `bash` span that succeeds also adds
`output`: the tail of what the command printed (its last 12 lines, at most
2,000 bytes). A failed span adds `error`.

The category follows from the name: `chat` and `phase2` get `chat`, a name with
the `tool.` prefix gets `tool`, and anything else gets `agent`.

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

Open the file in `chrome://tracing` (Load) or drag it onto ui.perfetto.dev.

---

🔮 ouija docs — [Index](../index.md) · [Config schema](config-schema.md)
