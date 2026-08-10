import type { AgentRuntimeEvent } from "./types.js";

type RunAccess = {
  token: string;
  internalBaseURL: string;
  readCache: Map<string, unknown>;
  pendingReads: Map<string, Promise<unknown>>;
  readCounts: Map<string, number>;
  signal?: AbortSignal;
};

const runAccess = new Map<string, RunAccess>();
const cacheableTools = new Set([
  "get_business_brief",
  "list_entities",
  "get_entity",
  "list_workspace_members",
  "get_priority_view",
  "search_metric_catalog",
]);
const metricSearchLimit = 8;

export function setRunAccess(runId: string, token: string, signal?: AbortSignal): void {
  const internalBaseURL = (process.env.GO_INTERNAL_URL || "http://127.0.0.1:8080").replace(/\/+$/, "");
  runAccess.set(runId, {
    token,
    internalBaseURL,
    readCache: new Map(),
    pendingReads: new Map(),
    readCounts: new Map(),
    signal,
  });
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
  if (!cacheableTools.has(toolName)) {
    return callBusinessToolUncached(access, runId, toolName, callId, input);
  }

  const cacheKey = `${toolName}:${stableStringify(input)}`;
  if (access.readCache.has(cacheKey)) {
    return access.readCache.get(cacheKey);
  }
  const pending = access.pendingReads.get(cacheKey);
  if (pending) return pending;

  if (toolName === "search_metric_catalog") {
    const searches = access.readCounts.get(toolName) || 0;
    if (searches >= metricSearchLimit) {
      return {
        search_limit_reached: true,
        instruction: "Use the metric catalog results already returned in this run and continue with the requested result.",
      };
    }
    access.readCounts.set(toolName, searches + 1);
  }

  const request = callBusinessToolUncached(access, runId, toolName, callId, input)
    .then((result) => {
      access.readCache.set(cacheKey, result);
      return result;
    })
    .finally(() => access.pendingReads.delete(cacheKey));
  access.pendingReads.set(cacheKey, request);
  return request;
}

async function callBusinessToolUncached(
  access: RunAccess,
  runId: string,
  toolName: string,
  callId: string,
  input: Record<string, unknown>,
): Promise<unknown> {
  const url = `${access.internalBaseURL}/internal/agent/tools/${encodeURIComponent(toolName)}`;
  const body = JSON.stringify({
    run_id: runId,
    tool_call_id: callId,
    input,
  });
  let lastError: unknown;
  for (let attempt = 0; attempt < 3; attempt += 1) {
    let response: Response;
    try {
      response = await fetch(url, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${access.token}`,
          "Content-Type": "application/json",
        },
        body,
        signal: requestSignal(access.signal, 30_000),
      });
    } catch (error) {
      lastError = error;
      if (attempt === 2) throw error;
      await retryPause(attempt, access.signal);
      continue;
    }
    const raw = await response.text();
    if (response.ok) {
      return raw ? JSON.parse(raw) : { ok: true };
    }
    const error = new Error(`business_tool_failed:${response.status}:${raw.slice(0, 500)}`);
    if (response.status !== 429 && response.status < 500) throw error;
    lastError = error;
    if (attempt === 2) throw error;
    await retryPause(attempt, access.signal);
  }
  throw lastError instanceof Error ? lastError : new Error("business_tool_failed");
}

function stableStringify(value: unknown): string {
  if (Array.isArray(value)) {
    return `[${value.map(stableStringify).join(",")}]`;
  }
  if (value && typeof value === "object") {
    const entries = Object.entries(value as Record<string, unknown>)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([key, item]) => `${JSON.stringify(key)}:${stableStringify(item)}`);
    return `{${entries.join(",")}}`;
  }
  return JSON.stringify(value) ?? "null";
}

function retryPause(attempt: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(signal.reason);
      return;
    }
    const timer = setTimeout(resolve, attempt === 0 ? 250 : 750);
    signal?.addEventListener("abort", () => {
      clearTimeout(timer);
      reject(signal.reason);
    }, { once: true });
  });
}

function requestSignal(signal: AbortSignal | undefined, timeoutMilliseconds: number): AbortSignal {
  const timeout = AbortSignal.timeout(timeoutMilliseconds);
  return signal ? AbortSignal.any([signal, timeout]) : timeout;
}

export async function publishEvent(runId: string, event: AgentRuntimeEvent): Promise<void> {
  const access = accessFor(runId);
  try {
    const response = await fetch(`${access.internalBaseURL}/internal/agent/runs/${encodeURIComponent(runId)}/events`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${access.token}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify(event),
      signal: requestSignal(access.signal, 5_000),
    });
    if (!response.ok) {
      throw new Error(`event_publish_failed:${response.status}`);
    }
  } catch {
    // Event delivery is best effort. The completed runtime response also carries the event list.
  }
}
