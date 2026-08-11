import assert from "node:assert/strict";
import { createServer, request as httpRequest, type Server } from "node:http";
import { test } from "node:test";

import { gatewayAuthorized, isAllowedOpenAIPath, proxyOpenAIRequest } from "./openaiGateway.js";

const secret = "g".repeat(64);

async function listen(server: Server): Promise<string> {
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      server.off("error", reject);
      resolve();
    });
  });
  const address = server.address();
  assert(address && typeof address === "object");
  return `http://127.0.0.1:${address.port}`;
}

async function close(server: Server): Promise<void> {
  server.closeAllConnections();
  await new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
}

test("gateway accepts only the configured bearer secret", () => {
  assert.equal(gatewayAuthorized(`Bearer ${secret}`, secret), true);
  assert.equal(gatewayAuthorized("Bearer wrong", secret), false);
  assert.equal(gatewayAuthorized(undefined, secret), false);
});

test("gateway exposes only the OpenAI APIs used by REUP.goals", () => {
  assert.equal(isAllowedOpenAIPath("/v1/responses"), true);
  assert.equal(isAllowedOpenAIPath("/v1/vector_stores/vs_1/files"), true);
  assert.equal(isAllowedOpenAIPath("/v1/models"), false);
});

test("gateway replaces client authorization and streams the response", { timeout: 5_000 }, async () => {
  const upstream = createServer((request, response) => {
    assert.equal(request.headers.authorization, "Bearer openai-test-key");
    assert.equal(request.url, "/v1/responses?include=test");
    response.writeHead(200, { "Content-Type": "application/json", "OpenAI-Request-ID": "req_test" });
    response.end(JSON.stringify({ ok: true }));
  });
  const upstreamURL = await listen(upstream);
  const gateway = createServer((request, response) => proxyOpenAIRequest(request, response, {
    secret,
    openAIAPIKey: "openai-test-key",
    upstreamBaseURL: upstreamURL,
    maxRequestBytes: 1024,
  }));
  const gatewayURL = await listen(gateway);
  try {
    const result = await new Promise<{ body: string; requestID?: string; status: number }>((resolve, reject) => {
      const request = httpRequest(`${gatewayURL}/openai/v1/responses?include=test`, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${secret}`,
          "Content-Length": 2,
          "Content-Type": "application/json",
        },
      }, (response) => {
        const chunks: Buffer[] = [];
        response.on("data", (chunk: Buffer) => chunks.push(chunk));
        response.on("end", () => resolve({
          body: Buffer.concat(chunks).toString("utf8"),
          requestID: response.headers["openai-request-id"] as string | undefined,
          status: response.statusCode ?? 0,
        }));
      });
      request.on("error", reject);
      request.end("{}");
    });
    assert.equal(result.status, 200);
    assert.deepEqual(JSON.parse(result.body), { ok: true });
    assert.equal(result.requestID, "req_test");
  } finally {
    await close(gateway);
    await close(upstream);
  }
});
