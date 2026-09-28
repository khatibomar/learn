# Tools on any agent

The teach and visualize skills name four capabilities: `quiz`, `ask_user_question`, `researcher`, and the `md-log` file. pi gets them from the extensions in this repo. On other agents, the `learn-visual` MCP server gives the `quiz` and `md_log` tools, and hooks in this repo write the log. Use this table to find the replacement. Do not skip a phase of the skill because a tool is missing.

| Capability | pi | Other agent |
|---|---|---|
| `quiz` | `quiz` tool | The `quiz` tool of the `learn-visual` MCP server. It shows a form if your agent supports MCP elicitation. If it says the form is not available, use the chat protocol below |
| `ask_user_question` | `ask_user_question` tool | Your own question tool (Claude Code: `AskUserQuestion`), else ask in chat |
| `researcher` | `researcher` subagent | A `researcher` subagent if your agent supports subagents, else do the research yourself with your web search and fetch tools |
| `md-log` | `/md-log <file>` | The `md_log` tool of the `learn-visual` MCP server. See the log section below |

## Quiz protocol (chat)

Use this only when the `quiz` tool says that the form is not available. Do not use `AskUserQuestion` for a quiz: it shows the options in the order you write them, and you must grade.

1. Decide the correct answer and the explanation first. Keep both to yourself.
2. Build the options with the construction procedure in `SKILL.md`. Shuffle them, so the correct answer is not in a fixed position.
3. Send one question per message. Number the options `1`, `2`, ... and add `0. I don't know` as the last line. For multi-select, say "Select all that apply".
4. Stop. Do not show the answer, a hint, or the explanation in that message.
5. When the user replies, grade it: `✓ Correct` or `✗ Incorrect — correct answer: <n>. <label>`. Then give the explanation.
6. Treat `0` as "I don't know". It is an honest gap, not a wrong guess. Do not grade it as wrong. Teach the gap.

If the user adds a note to the answer, read the note. It often shows the misconception.

## Log (markdown file)

The user starts the log with `/md-log <file>` or "log to `<file>.md`", and stops it with `/md-unlog` or "stop logging". Call the `md_log` tool with `action: "link"` and the file, or with `action: "unlink"`. The file must exist.

Hooks then append each user prompt and each reply to the file. The `quiz` tool writes its questions and answers. Do not write to the log file yourself.
