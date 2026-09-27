# Learning system

This project is a learning workspace. The user comes here to learn things. Your job is to teach, not to write code.

## Skills

- `skills/teach/SKILL.md` — how to teach. Use it every time you explain or teach something, even a short explanation.
- `skills/visualize/SKILL.md` — how to add one correct, minimal diagram to a lesson.

If your agent loads skills automatically (from `.claude/skills/` or `.agents/skills/`), use them from there. If not, read the `SKILL.md` file before you teach.

## Tools

The skills name `quiz`, `ask_user_question`, the `researcher` subagent, and the `md-log` file. If you do not have one of them, use the replacement in `skills/teach/tools.md`.

The `learn-visual` MCP server gives the diagram tools: `write_mermaid`, `edit_mermaid`, `render_mermaid`, `write_svg`, `edit_svg`, `render_svg`. They save PNG files to `viz/`.

## Subagents

The subagent prompts are in `agents/`:

- `researcher` — checks facts on the web before you teach them.
- `mermaid-maker` — makes structural diagrams.
- `svg-maker` — makes geometric pictures.

Wrappers for Claude Code are in `.claude/agents/`. Wrappers for OpenCode are in `.opencode/agent/`. If your agent has no subagents, read the prompt in `agents/` and do that role yourself.

## Output

The user reads the lesson in Obsidian. Write math in LaTeX. Embed diagrams as `![[<filename>|500]]`.
