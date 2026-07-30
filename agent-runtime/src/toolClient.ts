import type { AgentRuntimeEvent } from "./types.js";

type RunAccess = {
  token: string;
  internalBaseURL: string;
};

const runAccess = new Map<string, RunAccess>();

export function setRunAccess(runId: string, token: string): void {
  const internalBaseURL = (process.env.GO_INTERNAL_URL || "http://127.0.0.1:8080").replace(/\/+$/, "");
  runAccess.set(runId, { token, internalBaseURL });
}

export function clearRunAccess(runId: string): void {
  runAccess.delete(runId);
}

function accessFor(runId: string): RunAccess {
  const access = runAccess.get(runId);
  if (!access) {
    throw new Error("agent_run_access_missing");
  }
  return access;
}

export async function callBusinessTool(
  runId: string,
  toolName: string,
  callId: string,
  input: Record<string, unknown>,
): Promise<unknown> {
  const access = accessFor(runId);
  const response = await fetch(`${access.internalBaseURL}/internal/agent/tools/${encodeURIComponent(toolName)}`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${access.token}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      run_id: runId,
      tool_call_id: callId,
      input,
    }),
  });
  const raw = await response.text();
  if (!response.ok) {
    throw new Error(`business_tool_failed:${response.status}:${raw.slice(0, 500)}`);
  }
  return raw ? JSON.parse(raw) : { ok: true };
}

export async function publishEvent(runId: string, event: AgentRuntimeEvent): Promise<void> {
  const access = accessFor(runId);
  try {
    await fetch(`${access.internalBaseURL}/internal/agent/runs/${encodeURIComponent(runId)}/events`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${access.token}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify(event),
    });
  } catch {
    // Event delivery is best effort. The completed runtime response also carries the event list.
  }
}
