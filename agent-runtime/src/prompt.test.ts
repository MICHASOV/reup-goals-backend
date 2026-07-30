import assert from "node:assert/strict";
import test from "node:test";

import { buildInstructions } from "./prompt.js";

test("dynamic instructions include scope and compact brief without runtime secrets", () => {
  const instructions = buildInstructions({
    runId: "run_test",
    workspaceId: 10,
    userId: 20,
    participantRole: "owner",
    scope: { type: "project", id: 7, label: "Запуск продукта" },
    businessBrief: "Цель: подтвердить платёжеспособный спрос.",
  });
  assert.match(instructions, /project:7/);
  assert.match(instructions, /подтвердить платёжеспособный спрос/);
  assert.doesNotMatch(instructions, /run_test/);
});
