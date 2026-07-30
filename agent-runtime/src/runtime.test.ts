import assert from "node:assert/strict";
import test from "node:test";

import { currentInput } from "./runtime.js";

test("currentInput leaves a new conversation untouched", () => {
  assert.equal(currentInput("Что делать дальше?"), "Что делать дальше?");
});

test("currentInput separates migrated history from the current user message", () => {
  const value = currentInput(
    "Создай проект",
    "Пользователь: Мы развиваем продажи.\nСоветник: Сначала проверим экономику.",
  );

  assert.match(value, /<conversation_continuity>/);
  assert.match(value, /Пользователь: Мы развиваем продажи\./);
  assert.match(value, /<current_user_message>\nСоздай проект/);
  assert.match(value, /outdated assistant suggestions/);
});
