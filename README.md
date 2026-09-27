# learn

[![video](assets/thumbnail.png)](https://www.youtube.com/watch?v=kzcI5F4tGiU)

My AI learning system from this video: [How I Use AI to Learn Things](https://www.youtube.com/watch?v=kzcI5F4tGiU).

This is a personal system I built for myself, shared as-is. It started as a pi configuration. It now also works with other coding agents: Claude Code, Codex, OpenCode, Cursor, Gemini CLI, and any agent that reads `AGENTS.md` and supports MCP.

## What's in it

- `skills/teach/` — the philosophy and the process
- `skills/teach/tools.md` — replacements for the pi tools on other agents
- `skills/visualize/` — adds a correct, minimal diagram to a lesson when an idea is clearer as a picture
- `extensions/ask-user-question.ts` — (pi) the agent asks you questions through a UI popup
- `extensions/quiz.ts` — (pi) graded questions with instant feedback (✓/✗, correct answer, explanation)
- `extensions/md-log.ts` — (pi) link a markdown file to the session
- `visual/` — `learn-visual`, a Go program that renders Mermaid and SVG to PNG with headless Firefox. It serves the diagram tools over MCP.
- `extensions/visual-tools/` — (pi) thin wrapper that exposes `learn-visual` as pi tools for the visualization subagents
- `agents/` — `researcher`, `svg-maker`, `mermaid-maker`: the subagents the system delegates to
- `AGENTS.md` — entry point for agents other than pi

## Install

Clone the repo as your learning project, and build the diagram tools:

```bash
git clone https://github.com/amosblomqvist/learn
cd learn
go -C visual build -o ../bin/learn-visual .
```

Then open your agent in the `learn` directory.

| Agent | Instructions | Skills | Subagents | Diagram tools (MCP) |
|---|---|---|---|---|
| pi | `AGENTS.md` | `.pi/skills` | `.pi/agents` | `.pi/extensions` (pi extension over `bin/learn-visual`) |
| Claude Code | `AGENTS.md` | `.claude/skills` | `.claude/agents` | `.mcp.json` |
| Codex | `AGENTS.md` | `.agents/skills` | — | `.codex/config.toml` |
| OpenCode | `AGENTS.md` | `.claude/skills` | `.opencode/agent` | `opencode.json` |
| Cursor | `AGENTS.md` | `.agents/skills` | — | `.cursor/mcp.json` |
| Gemini CLI | `.gemini/settings.json` | — | — | `.gemini/settings.json` |

The `.pi`, `.claude/skills`, and `.agents/skills` entries are symlinks to the top-level folders. On Windows, enable symlinks in git (`git config core.symlinks true`) before you clone.

Some agents ask you to trust the project or approve the MCP server on first start. Codex reads `.codex/config.toml` only in a trusted project.

For any other agent, point it at `AGENTS.md` and add this MCP server:

```bash
bin/learn-visual mcp
```

The server writes PNG files to `viz/` in its working directory. Set `LEARN_ROOT` to use a different directory.

The old pi install still works. From your learning project's root:

```bash
git clone https://github.com/amosblomqvist/learn .pi
go -C .pi/visual build -o ../bin/learn-visual .
```

## Requirements

- Go 1.27 or later, to build `learn-visual`
- Firefox. `learn-visual` finds `firefox` on `PATH`. Set `LEARN_FIREFOX` to use a different binary.
- Network access on the first Mermaid render. `learn-visual` downloads `mermaid.min.js` (pinned version, checked with SHA-256) into your user cache directory. Set `LEARN_MERMAID_JS` to use a local copy.

### pi

- [pi](https://github.com/earendil-works/pi)
- A subagent implementation, so the system can spawn the researcher and the visual makers. Recommended: [pi-interactive-subagents](https://github.com/amosblomqvist/pi-interactive-subagents) (tmux only). With it, everything works out of the box. Any other implementation works too, but expect to adapt the agent definitions, e.g. `agents/researcher.md` lists `safe_bash` in its tools, which is specific to that extension.
- `ask-user-question` — use the copy bundled here. If your setup already has an `ask-user-question` extension, use **this** one in its place. Popups from different extensions serialize through a shared UI lock, which only works when it's the same implementation.

### Other agents

The `quiz`, `ask_user_question`, and `md-log` extensions are pi only. On other agents, the skills use the chat protocols in `skills/teach/tools.md`: quizzes in chat, graded by the agent, and a markdown log that the agent appends to. Say "log to `<file>.md`" to start the log.

## Notes

You can run the system without subagents. The main session does the teaching. On pi you lose the researcher (truth verification) and the generated visuals. On other agents, the main session can do those roles itself with its web tools and the MCP diagram tools.

The teaching skill is written for one learner (me). Edit the skill to fit how you learn best.
