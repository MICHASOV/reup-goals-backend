import { timingSafeEqual } from "node:crypto";
import { request as httpRequest, type RequestOptions } from "node:http";
import { request as httpsRequest } from "node:https";
import { Transform } from "node:stream";
import type { IncomingHttpHeaders, IncomingMessage, ServerResponse } from "node:http";
import { durationMilliseconds } from "./transport.js";

const allowedRoots = [
  "/v1/responses",
  "/v1/conversations",
  "/v1/files",
  "/v1/vector_stores",
  "/v1/audio/transcriptions",
];

const requestHeaders = new Set([
  "accept",
  "content-encoding",
  "content-length",
  "content-type",
  "openai-beta",
  "user-agent",
]);

const responseHeaders = new Set([
  "content-encoding",
  "content-length",
  "content-type",
  "openai-processing-ms",
  "openai-request-id",
  "retry-after",
  "x-request-id",
]);

export type OpenAIGatewayConfig = {
  secret: string;
  openAIAPIKey: string;
  upstreamBaseURL: string;
  maxRequestBytes: number;
};

export function gatewayAuthorized(authorization: string | undefined, secret: string): boolean {
  if (!secret || !authorization?.startsWith("Bearer ")) return false;
  const provided = Buffer.from(authorization.slice("Bearer ".length));
  const expected = Buffer.from(secret);
  return provided.length === expected.length && timingSafeEqual(provided, expected);
}

export function isAllowedOpenAIPath(pathname: string): boolean {
  return allowedRoots.some((root) => pathname === root || pathname.startsWith(`${root}/`));
}

export function proxyOpenAIRequest(
  request: IncomingMessage,
  response: ServerResponse,
  config: OpenAIGatewayConfig,
): void {
  if (!gatewayAuthorized(request.headers.authorization, config.secret)) {
    writeGatewayError(response, 401, "unauthorized");
    return;
  }
  if (!request.method || !["GET", "POST", "DELETE"].includes(request.method)) {
    writeGatewayError(response, 405, "method_not_allowed");
    return;
  }

  const incomingURL = new URL(request.url || "/", "http://gateway.local");
  const upstreamPath = incomingURL.pathname.slice("/openai".length);
  if (!isAllowedOpenAIPath(upstreamPath)) {
    writeGatewayError(response, 404, "not_found");
    return;
  }

  const contentLength = Number(request.headers["content-length"] || 0);
  if (Number.isFinite(contentLength) && contentLength > config.maxRequestBytes) {
    writeGatewayError(response, 413, "request_too_large");
    return;
  }

  const upstreamBase = new URL(config.upstreamBaseURL);
  const basePath = upstreamBase.pathname.replace(/\/+$/, "");
  const path = `${basePath}${upstreamPath}${incomingURL.search}`;
  const headers = filteredHeaders(request.headers, requestHeaders);
  headers.authorization = `Bearer ${config.openAIAPIKey}`;
  headers.host = upstreamBase.host;

  const options: RequestOptions = {
    protocol: upstreamBase.protocol,
    hostname: upstreamBase.hostname,
    port: upstreamBase.port || undefined,
    method: request.method,
    path,
    headers,
  };
  const send = upstreamBase.protocol === "http:" ? httpRequest : httpsRequest;
  const upstream = send(options, (upstreamResponse) => {
    const outputHeaders = filteredHeaders(upstreamResponse.headers, responseHeaders);
    outputHeaders["cache-control"] = "no-store";
    response.writeHead(upstreamResponse.statusCode || 502, outputHeaders);
    upstreamResponse.pipe(response);
  });

  upstream.setTimeout(
    durationMilliseconds(process.env.OPENAI_GATEWAY_TIMEOUT, 45 * 60 * 1000),
    () => upstream.destroy(new Error("openai_gateway_timeout")),
  );
  upstream.on("error", (error) => {
    console.error(JSON.stringify({
      level: "error",
      event: "openai_gateway_upstream_failed",
      method: request.method || "",
      path: upstreamPath,
      error: error.message.slice(0, 500),
    }));
    if (!response.headersSent) writeGatewayError(response, 502, "openai_gateway_unavailable");
    else response.destroy(error);
  });

  let received = 0;
  const limiter = new Transform({
    transform(chunk: Buffer, _encoding, callback) {
      received += chunk.length;
      if (received > config.maxRequestBytes) {
        callback(new Error("request_too_large"));
        return;
      }
      callback(null, chunk);
    },
  });
  limiter.on("error", (error) => {
    upstream.destroy(error);
    if (!response.headersSent) writeGatewayError(response, 413, "request_too_large");
  });
  request.on("aborted", () => upstream.destroy(new Error("client_aborted")));
  request.pipe(limiter).pipe(upstream);
}

function filteredHeaders(source: IncomingHttpHeaders, allowed: Set<string>): Record<string, string | string[]> {
  const result: Record<string, string | string[]> = {};
  for (const [name, value] of Object.entries(source)) {
    if (!allowed.has(name.toLowerCase()) || value === undefined) continue;
    result[name] = value;
  }
  return result;
}

function writeGatewayError(response: ServerResponse, status: number, error: string): void {
  if (response.headersSent) return;
  response.writeHead(status, {
    "Content-Type": "application/json; charset=utf-8",
    "Cache-Control": "no-store",
  });
  response.end(JSON.stringify({ error }));
}
