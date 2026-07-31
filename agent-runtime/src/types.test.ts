import assert from "node:assert/strict";
import test from "node:test";

import { executeRunRequestSchema, resumeRunRequestSchema } from "./types.js";

test("execute request accepts a valid scoped message", () => {
  const result = executeRunRequestSchema.safeParse({
    run_id: "run_123",
    workspace_id: 1,
    user_id: 2,
    scope: { type: "strategy", id: 0, label: "Стратегия" },
    message: "Продолжим",
    model: "gpt-5.4-mini",
    run_token: "signed-token",
  });
  assert.equal(result.success, true);
});

test("execute request rejects unknown fields and invalid scope ids", () => {
  const result = executeRunRequestSchema.safeParse({
    run_id: "run_123",
    workspace_id: 1,
    user_id: 2,
    scope: { type: "project", id: 0, label: "Проект" },
    message: "Продолжим",
    model: "gpt-5.4-mini",
    run_token: "signed-token",
    unexpected: true,
  });
  assert.equal(result.success, false);
});

test("resume request requires explicit bounded decisions", () => {
  const result = resumeRunRequestSchema.safeParse({
    run_id: "run_123",
    model: "gpt-5.4-mini",
    state: "serialized",
    run_token: "signed-token",
    decisions: [],
  });
  assert.equal(result.success, false);
});
