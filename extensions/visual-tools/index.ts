/**
 * visual-tools
 *
 * A self-contained pi extension that registers custom subagent tools with the
 * globally-loaded `interactive-subagents` extension — and does nothing else:
 *
 *   • write_mermaid / edit_mermaid / render_mermaid — the mermaid-maker's loop
 *   • write_svg / edit_svg / render_svg — the svg-maker's loop
 *
 * All six live in tools/visual_tools.ts, a thin wrapper over the `learn-visual`
 * Go binary (see ../../visual), which renders with headless Firefox.
 *
 * ── How registration reaches interactive-subagents ──────────────────────────
 * The global `interactive-subagents` extension exposes `registerToolExtension`
 * on `globalThis.__pi_interactive_subagents`. A child subagent is launched with
 * `--no-extensions` plus an explicit `-e <path>` only for tools whose name →
 * path mapping it knows; this extension teaches it about the six names above so
 * mermaid-maker / svg-maker (which list them in their `tools:` frontmatter) get
 * them loaded into their child process.
 *
 * pi loads PROJECT-local extensions (this one) BEFORE global ones, so
 * `globalThis.__pi_interactive_subagents` does not exist yet when this factory
 * runs. We defer registration to `session_start`, which fires once after every
 * extension's factory has run. Registration is idempotent (same name+path is a
 * no-op), so `/reload` or a "reload"/"new"/"resume" session_start is harmless.
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent"
import * as fs from "node:fs"
import * as path from "node:path"
import { fileURLToPath } from "node:url"

const EXT_DIR = path.dirname(fileURLToPath(import.meta.url))
const VISUAL_TOOLS = path.join(EXT_DIR, "tools", "visual_tools.ts")

interface InteractiveSubagentsApi {
  registerToolExtension: (name: string, extensionPath: string) => void
}

function registerToolExtensions(): void {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const api = (globalThis as any).__pi_interactive_subagents as InteractiveSubagentsApi | undefined
  if (!api?.registerToolExtension) return // interactive-subagents not loaded — no-op

  if (!fs.existsSync(VISUAL_TOOLS)) return
  for (const name of ["write_mermaid", "edit_mermaid", "render_mermaid", "write_svg", "edit_svg", "render_svg"]) {
    try {
      api.registerToolExtension(name, VISUAL_TOOLS)
    } catch {
      // Already registered under a different path, or re-registered — ignore.
    }
  }
}

export default function (pi: ExtensionAPI) {
  pi.on("session_start", async () => {
    registerToolExtensions()
  })
}
