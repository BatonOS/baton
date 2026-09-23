






















import fs from "node:fs";

const write = (word) => {
  const p = process.env.BATON_RUNTIME_STATUS_FILE;
  if (!p) return;

  try { fs.writeFileSync(p, word + "\n"); } catch {  }
};

export default {
  id: "baton-status",
  name: "BATON status bridge",
  description: "Reports the runtime activity word BATON reads",
  register(api) {


    api.agent?.events?.registerAgentEventSubscription?.({
      id: "baton-status",
      streams: ["lifecycle"],
      handle: (ev) => {
        const ph = ev?.data?.phase;
        if (ph === "end") write(ev?.data?.aborted ? "idle" : "done");
        else if (ph === "error") write("errored");
      },
    });
  },
};
