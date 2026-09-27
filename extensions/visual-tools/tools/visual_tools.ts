/**
 * The mermaid-maker and svg-maker authoring tools for pi:
 *
 *   write_mermaid / edit_mermaid / render_mermaid
 *   write_svg     / edit_svg     / render_svg
 *
 * The `learn-visual` Go binary owns the tool definitions, the managed source
 * file, the edits, the Firefox render, and the publish into <cwd>/viz. This
 * file registers the tools that `learn-visual tools` lists and forwards each
 * call to `learn-visual call <tool>`.
 *
 * The binary is found at $LEARN_VISUAL_BIN, then <repo>/bin/learn-visual,
 * then `learn-visual` on PATH. Session state lives in a temp dir keyed by pid,
 * so parallel makers (different child pi processes) do not share a file.
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent"
import { Type } from "@sinclair/typebox"
import { execFileSync, spawn } from "node:child_process"
import { existsSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"

const REPO_BIN = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "..", "bin", "learn-visual")
const BIN = process.env.LEARN_VISUAL_BIN || (existsSync(REPO_BIN) ? REPO_BIN : "learn-visual")
const STATE_DIR = join(tmpdir(), "pi-visual-tools", String(process.pid))

interface ToolDef {
  name: string
  label: string
  description: string
  params: Array<{ name: string; description: string; optional?: boolean }>
}

type Content = { type: "text"; text: string } | { type: "image"; data: string; mimeType: string }

function call(name: string, params: unknown): Promise<{ content: Content[]; isError?: boolean }> {
  return new Promise((resolve, reject) => {
    const child = spawn(BIN, ["call", name, "--state", STATE_DIR], { cwd: process.cwd() })
    let stdout = ""
    let stderr = ""
    child.stdout.on("data", (d) => (stdout += d.toString()))
    child.stderr.on("data", (d) => (stderr += d.toString()))
    child.on("error", reject)
    child.on("close", (code) => {
      if (code !== 0) return reject(new Error(stderr.trim() || `learn-visual exited with code ${code}`))
      try {
        resolve(JSON.parse(stdout))
      } catch (err) {
        reject(new Error(`learn-visual returned bad JSON: ${err}`))
      }
    })
    child.stdin.end(JSON.stringify(params ?? {}))
  })
}

export default function visualToolsExtension(pi: ExtensionAPI) {
  let defs: ToolDef[]
  try {
    defs = JSON.parse(execFileSync(BIN, ["tools"], { encoding: "utf8" }))
  } catch (err) {
    console.error(`visual-tools: cannot run ${BIN} (build it with \`go -C visual build -o ../bin/learn-visual .\`): ${err}`)
    return
  }

  for (const def of defs) {
    const props: Record<string, ReturnType<typeof Type.String> | ReturnType<typeof Type.Optional>> = {}
    for (const p of def.params) {
      const s = Type.String({ description: p.description })
      props[p.name] = p.optional ? Type.Optional(s) : s
    }
    pi.registerTool({
      name: def.name,
      label: def.label,
      description: def.description,
      parameters: Type.Object(props),
      async execute(_id, params) {
        const res = await call(def.name, params)
        if (res.isError) {
          throw new Error(res.content.map((c) => (c.type === "text" ? c.text : "")).join("\n"))
        }
        return { content: res.content, details: { ok: true } }
      },
    })
  }
}
