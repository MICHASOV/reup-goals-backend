import assert from "node:assert/strict";
import test from "node:test";

import { callBusinessTool, clearRunAccess, setRunAccess } from "./toolClient.js";

test("business tools retry transient server failures", async () => {
  const originalFetch = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = (async () => {
    calls += 1;
    if (calls === 1) return new Response("temporary", { status: 503 });
    return new Response(JSON.stringify({ ok: true }), { status: 200 });
  }) as typeof fetch;
  setRunAccess("run_retry", "signed-token");
  try {
    const result = await callBusinessTool("run_retry", "get_business_brief", "call_1", {});
    assert.deepEqual(result, { ok: true });
    assert.equal(calls, 2);
  } finally {
    clearRunAccess("run_retry");
    globalThis.fetch = originalFetch;
  }
});

test("business tools do not retry validation or permission failures", async () => {
  const originalFetch = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = (async () => {
    calls += 1;
    return new Response("forbidden", { status: 403 });
  }) as typeof fetch;
  setRunAccess("run_forbidden", "signed-token");
  try {
    await assert.rejects(
      callBusinessTool("run_forbidden", "propose_department", "call_2", {}),
      /business_tool_failed:403/,
    );
    assert.equal(calls, 1);
  } finally {
    clearRunAccess("run_forbidden");
    globalThis.fetch = originalFetch;
  }
});
