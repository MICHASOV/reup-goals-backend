import { createServer, type IncomingMessage, type ServerResponse } from "node:http";
import { MaxTurnsExceededError } from "@openai/agents";
import { ZodError } from "zod";

import { configureOpenAIProvider } from "./provider.js";
import { executeRun, resumeRun } from "./runtime.js";
import { executeRunRequestSchema, resumeRunRequestSchema } from "./types.js";

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
      const input = executeRunRequestSchema.parse(await readJSON<unknown>(request));
      const controller = requestAbortController(request, response);
      writeJSON(response, 200, await executeRun(input, controller.signal));
      return;
    }
    if (request.method === "POST" && request.url === "/v1/runs/resume") {
      const input = resumeRunRequestSchema.parse(await readJSON<unknown>(request));
      const controller = requestAbortController(request, response);
      writeJSON(response, 200, await resumeRun(input, controller.signal));
      return;
    }
    writeJSON(response, 404, { error: "not_found" });
  } catch (error) {
    const message = error instanceof Error ? error.message : "agent_runtime_failed";
    const cause = error instanceof Error && error.cause instanceof Error
      ? error.cause.message
      : "";
    console.error(JSON.stringify({
      level: "error",
      event: "agent_runtime_request_failed",
      method: request.method || "",
      path: request.url || "",
      error: message.slice(0, 1000),
      cause: cause.slice(0, 1000),
    }));
    const status = message === "request_too_large"
      ? 413
      : error instanceof SyntaxError || error instanceof ZodError
        ? 400
        : error instanceof MaxTurnsExceededError
          ? 422
          : 500;
    writeJSON(response, status, { error: message.slice(0, 1000) });
  }
});

function requestAbortController(request: IncomingMessage, response: ServerResponse): AbortController {
  const controller = new AbortController();
  request.once("aborted", () => controller.abort());
  response.once("close", () => {
    if (!response.writableEnded) controller.abort();
  });
  return controller;
}

server.listen(port, "127.0.0.1", () => {
  console.log(`REUP.goals agent runtime listening on 127.0.0.1:${port}`);
});
