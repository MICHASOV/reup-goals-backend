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

test("execute and resume requests accept the configured 120 turn limit", () => {
  const execute = executeRunRequestSchema.safeParse({
    run_id: "run_123",
    workspace_id: 1,
    user_id: 2,
    scope: { type: "strategy", id: 0, label: "Стратегия" },
    message: "Проведи полный анализ",
    model: "gpt-5.6-luna",
    run_token: "signed-token",
    max_turns: 120,
  });
  const resume = resumeRunRequestSchema.safeParse({
    run_id: "run_123",
    model: "gpt-5.6-luna",
    state: "serialized",
    run_token: "signed-token",
    decisions: [{ call_id: "call_123", approved: true }],
    max_turns: 120,
  });

  assert.equal(execute.success, true);
  assert.equal(resume.success, true);
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
