import { timingSafeEqual } from "node:crypto";
import { createServer, type IncomingMessage, type ServerResponse } from "node:http";
import { MaxTurnsExceededError } from "@openai/agents";
import { ZodError } from "zod";

import { configureOpenAIProvider } from "./provider.js";
import { executeRun, resumeRun } from "./runtime.js";
import { isSecureInternalURL } from "./transport.js";
import { executeRunRequestSchema, resumeRunRequestSchema } from "./types.js";

const port = Number(process.env.PORT || 8091);
const listenHost = (process.env.LISTEN_HOST || "127.0.0.1").trim();
const runtimeSecret = (process.env.AGENT_RUNTIME_SECRET || "").trim();
const openAIAPIKey = (process.env.OPENAI_API_KEY || "").trim();
const goInternalURL = (process.env.GO_INTERNAL_URL || "").trim();
validateConfiguration();
configureOpenAIProvider();

function validateConfiguration(): void {
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw new Error("PORT must be a valid TCP port");
  }
  if (process.env.NODE_ENV !== "production") return;
  if (runtimeSecret.length < 32) {
    throw new Error("AGENT_RUNTIME_SECRET must contain at least 32 characters");
  }
  if (!openAIAPIKey) {
    throw new Error("OPENAI_API_KEY is required");
  }
  if (!isSecureInternalURL(goInternalURL)) {
    throw new Error("GO_INTERNAL_URL must use HTTPS or a loopback HTTP tunnel in production");
  }
}

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

function runtimeAuthorized(request: IncomingMessage): boolean {
  if (!runtimeSecret) return process.env.NODE_ENV !== "production";
  const authorization = request.headers.authorization || "";
  if (!authorization.startsWith("Bearer ")) return false;
  const provided = Buffer.from(authorization.slice("Bearer ".length));
  const expected = Buffer.from(runtimeSecret);
  return provided.length === expected.length && timingSafeEqual(provided, expected);
}

const server = createServer(async (request, response) => {
  try {
    if (request.method === "GET" && request.url === "/healthz") {
      writeJSON(response, 200, {
        ok: true,
        runtime: Boolean(runtimeSecret || process.env.NODE_ENV !== "production"),
        openai: Boolean(openAIAPIKey),
      });
      return;
    }
    if (request.method === "GET" && request.url === "/readyz") {
      const controller = new AbortController();
      const timeout = setTimeout(() => controller.abort(), 3_000);
      try {
        const healthURL = new URL("/healthz", goInternalURL);
        const goResponse = await fetch(healthURL, {
          method: "GET",
          signal: controller.signal,
          headers: { Accept: "application/json" },
        });
        if (!goResponse.ok) {
          writeJSON(response, 503, { ok: false, callback: false });
          return;
        }
        writeJSON(response, 200, { ok: true, callback: true });
      } catch {
        writeJSON(response, 503, { ok: false, callback: false });
      } finally {
        clearTimeout(timeout);
      }
      return;
    }
    if (!runtimeAuthorized(request)) {
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

server.listen(port, listenHost, () => {
  console.log(`REUP.goals AI runtime listening on ${listenHost}:${port}`);
});
