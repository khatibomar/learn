// Sends the session messages to `learn-visual hook opencode` at the end of each turn.
export const MdLog = async ({ client, $, directory }: any) => ({
  event: async ({ event }: any) => {
    if (event.type !== "session.idle") return
    const id = event.properties.sessionID
    const res = await client.session.messages({ path: { id } })
    const payload = Buffer.from(JSON.stringify({ session_id: id, cwd: directory, messages: res.data ?? [] }))
    await $`${directory}/bin/learn-visual hook opencode < ${payload}`.quiet().nothrow()
  },
})
