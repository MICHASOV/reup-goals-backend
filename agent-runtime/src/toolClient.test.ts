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
  const accessKey = setRunAccess("run_retry", "signed-token");
  try {
    const result = await callBusinessTool(accessKey, "get_business_brief", "call_1", {});
    assert.deepEqual(result, { ok: true });
    assert.equal(calls, 2);
  } finally {
    clearRunAccess(accessKey);
    globalThis.fetch = originalFetch;
  }
});

test("canceling a run aborts an in-flight business tool without retries", async () => {
  const originalFetch = globalThis.fetch;
  const controller = new AbortController();
  let calls = 0;
  globalThis.fetch = (async (_input, init) => {
    calls += 1;
    return await new Promise<Response>((_resolve, reject) => {
      init?.signal?.addEventListener("abort", () => {
        reject(init.signal?.reason ?? new DOMException("Aborted", "AbortError"));
      }, { once: true });
    });
  }) as typeof fetch;
  const accessKey = setRunAccess("run_canceled", "signed-token", controller.signal);
  try {
    const pending = callBusinessTool(accessKey, "get_business_brief", "call_1", {});
    controller.abort(new DOMException("Stopped by user", "AbortError"));
    await assert.rejects(pending, /Stopped by user|AbortError/);
    assert.equal(calls, 1);
  } finally {
    clearRunAccess(accessKey);
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
  const accessKey = setRunAccess("run_forbidden", "signed-token");
  try {
    await assert.rejects(
      callBusinessTool(accessKey, "propose_department", "call_2", {}),
      /business_tool_failed:403/,
    );
    assert.equal(calls, 1);
  } finally {
    clearRunAccess(accessKey);
    globalThis.fetch = originalFetch;
  }
});

test("identical read tools reuse the result within one run", async () => {
  const originalFetch = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = (async () => {
    calls += 1;
    return new Response(JSON.stringify({ title: "Company context" }), { status: 200 });
  }) as typeof fetch;
  const accessKey = setRunAccess("run_cached", "signed-token");
  try {
    const first = await callBusinessTool(accessKey, "get_business_brief", "call_1", { include_open_questions: true });
    const second = await callBusinessTool(accessKey, "get_business_brief", "call_2", { include_open_questions: true });
    assert.deepEqual(second, first);
    assert.equal(calls, 1);
  } finally {
    clearRunAccess(accessKey);
    globalThis.fetch = originalFetch;
  }
});

test("concurrent identical reads share one in-flight request", async () => {
  const originalFetch = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = (async () => {
    calls += 1;
    await new Promise((resolve) => setTimeout(resolve, 10));
    return new Response(JSON.stringify({ entities: [] }), { status: 200 });
  }) as typeof fetch;
  const accessKey = setRunAccess("run_pending", "signed-token");
  try {
    const input = { entity_type: "task", limit: 20 };
    const [first, second] = await Promise.all([
      callBusinessTool(accessKey, "list_entities", "call_1", input),
      callBusinessTool(accessKey, "list_entities", "call_2", input),
    ]);
    assert.deepEqual(second, first);
    assert.equal(calls, 1);
  } finally {
    clearRunAccess(accessKey);
    globalThis.fetch = originalFetch;
  }
});

test("write tools are never cached", async () => {
  const originalFetch = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = (async () => {
    calls += 1;
    return new Response(JSON.stringify({ ok: true }), { status: 200 });
  }) as typeof fetch;
  const accessKey = setRunAccess("run_writes", "signed-token");
  try {
    await callBusinessTool(accessKey, "propose_task", "call_1", { title: "Task" });
    await callBusinessTool(accessKey, "propose_task", "call_2", { title: "Task" });
    assert.equal(calls, 2);
  } finally {
    clearRunAccess(accessKey);
    globalThis.fetch = originalFetch;
  }
});

test("metric catalog searches stop after the per-run safety limit", async () => {
  const originalFetch = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = (async () => {
    calls += 1;
    return new Response(JSON.stringify({ metrics: [`metric-${calls}`] }), { status: 200 });
  }) as typeof fetch;
  const accessKey = setRunAccess("run_metrics", "signed-token");
  try {
    for (let index = 0; index < 8; index += 1) {
      await callBusinessTool(accessKey, "search_metric_catalog", `call_${index}`, { query: `metric ${index}` });
    }
    const limited = await callBusinessTool(accessKey, "search_metric_catalog", "call_9", { query: "another metric" });
    assert.deepEqual(limited, {
      search_limit_reached: true,
      instruction: "Use the metric catalog results already returned in this run and continue with the requested result.",
    });
    assert.equal(calls, 8);
  } finally {
    clearRunAccess(accessKey);
    globalThis.fetch = originalFetch;
  }
});

test("concurrent attempts of the same run keep independent access tokens", async () => {
  const originalFetch = globalThis.fetch;
  const authorizations: string[] = [];
  globalThis.fetch = (async (_input, init) => {
    authorizations.push(String((init?.headers as Record<string, string>)?.Authorization || ""));
    return new Response(JSON.stringify({ ok: true }), { status: 200 });
  }) as typeof fetch;
  const firstAccess = setRunAccess("shared_run", "first-token");
  const secondAccess = setRunAccess("shared_run", "second-token");
  try {
    await callBusinessTool(firstAccess, "get_business_brief", "call_1", {});
    clearRunAccess(firstAccess);
    await callBusinessTool(secondAccess, "get_business_brief", "call_2", {});
    assert.deepEqual(authorizations, ["Bearer first-token", "Bearer second-token"]);
  } finally {
    clearRunAccess(firstAccess);
    clearRunAccess(secondAccess);
    globalThis.fetch = originalFetch;
  }
});
