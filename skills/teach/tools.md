# Tools on any agent

The teach and visualize skills name four capabilities: `quiz`, `ask_user_question`, `researcher`, and the `md-log` file. pi gets them from the extensions in this repo. Other agents may not have them. Use this table to find the replacement. Do not skip a phase of the skill because a tool is missing.

| Capability | pi | Other agent |
|---|---|---|
| `quiz` | `quiz` tool | Your own multiple-choice question tool if you have one, else the chat protocol below |
| `ask_user_question` | `ask_user_question` tool | Your own question tool (Claude Code: `AskUserQuestion`), else ask in chat |
| `researcher` | `researcher` subagent | A `researcher` subagent if your agent supports subagents, else do the research yourself with your web search and fetch tools |
| `md-log` | `/md-log <file>` | The log protocol below |

## Quiz protocol (chat)

Use this when you have no `quiz` tool. Your own question tool (for example Claude Code `AskUserQuestion`) can show the options, but you still do the grading.

1. Decide the correct answer and the explanation first. Keep both to yourself.
2. Build the options with the construction procedure in `SKILL.md`. Shuffle them, so the correct answer is not in a fixed position.
3. Send one question per message. Number the options `1`, `2`, ... and add `0. I don't know` as the last line. For multi-select, say "Select all that apply".
4. Stop. Do not show the answer, a hint, or the explanation in that message.
5. When the user replies, grade it: `✓ Correct` or `✗ Incorrect — correct answer: <n>. <label>`. Then give the explanation.
6. Treat `0` as "I don't know". It is an honest gap, not a wrong guess. Do not grade it as wrong. Teach the gap.

If the user adds a note to the answer, read the note. It often shows the misconception.

## Log protocol (markdown file)

Use this when you have no `md-log` extension. The user starts it with "log to `<file>.md`" and stops it with "stop logging".

- After each reply, append that reply to the file, unchanged: lesson text, math, and `![[viz-...png|500]]` embeds.
- Append each user prompt as a `> **You:** ...` quote block.
- For each quiz, append the question and options before the user answers. Append the answer, the grade, and the explanation only after the user answers.
- Do not log tool calls, file reads, or shell output.
- Append only. Never rewrite earlier content in the file.
