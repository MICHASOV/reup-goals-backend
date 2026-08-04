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

test("instructions stay compact and let tools define execution contracts", () => {
  const instructions = buildInstructions({
    runId: "run_strategy",
    workspaceId: 10,
    userId: 20,
    participantRole: "owner",
    scope: { type: "strategy", id: 8, label: "Стратегия компании" },
    businessBrief: "Компания проверяет устойчивость прибыльного ядра.",
  });

  assert.match(instructions, /самостоятельно выбирай доступные инструменты/i);
  assert.match(instructions, /готовое к подтверждению изменение/i);
  assert.equal(instructions.split(/\n\s*\n/).length, 1);
  assert.ok(instructions.length < 1800);
});
