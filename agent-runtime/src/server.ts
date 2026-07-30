import { createServer, type IncomingMessage, type ServerResponse } from "node:http";

import { configureOpenAIProvider } from "./provider.js";
import { executeRun, resumeRun } from "./runtime.js";
import type { ExecuteRunRequest, ResumeRunRequest } from "./types.js";

const port = Number(process.env.PORT || 8091);
const secret = (process.env.AGENT_RUNTIME_SECRET || "").trim();
configureOpenAIProvider();

function writeJSON(response: ServerResponse, status: number, value: unknown): void {
  response.writeHead(status, {
    "Content-Type": "application/json; charset=utf-8",
    "Cache-Control": "no-store",
  });
  response.end(JSON.stringify(value));
}

async function readJSON<T>(request: IncomingMessage): Promise<T> {
  const chunks: Buffer[] = [];
  let size = 0;
  for await (const chunk of request) {
    const buffer = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
    size += buffer.length;
    if (size > 2 * 1024 * 1024) throw new Error("request_too_large");
    chunks.push(buffer);
  }
  return JSON.parse(Buffer.concat(chunks).toString("utf8")) as T;
}

function authorized(request: IncomingMessage): boolean {
  if (!secret) return process.env.NODE_ENV !== "production";
  return request.headers.authorization === `Bearer ${secret}`;
}

const server = createServer(async (request, response) => {
  try {
    if (request.method === "GET" && request.url === "/healthz") {
      writeJSON(response, 200, { ok: true });
      return;
    }
    if (!authorized(request)) {
      writeJSON(response, 401, { error: "unauthorized" });
      return;
    }
    if (request.method === "POST" && request.url === "/v1/runs/execute") {
      const input = await readJSON<ExecuteRunRequest>(request);
      writeJSON(response, 200, await executeRun(input));
      return;
    }
    if (request.method === "POST" && request.url === "/v1/runs/resume") {
      const input = await readJSON<ResumeRunRequest>(request);
      writeJSON(response, 200, await resumeRun(input));
      return;
    }
    writeJSON(response, 404, { error: "not_found" });
  } catch (error) {
    const message = error instanceof Error ? error.message : "agent_runtime_failed";
    writeJSON(response, 500, { error: message.slice(0, 1000) });
  }
});

server.listen(port, "127.0.0.1", () => {
  console.log(`REUP.goals agent runtime listening on 127.0.0.1:${port}`);
});
