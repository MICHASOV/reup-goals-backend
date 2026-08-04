import assert from "node:assert/strict";
import test from "node:test";

import { functionTools } from "./tools.js";

test("unified advisor exposes durable document and completion tools", () => {
  const names = functionTools().map((item) => item.name);
  assert.ok(names.includes("propose_department"));
  assert.ok(names.includes("propose_task"));
  assert.ok(names.includes("propose_document"));
  assert.ok(names.includes("update_document"));
  assert.ok(names.includes("complete_task"));
  assert.equal(names.includes("propose_direction"), false);
  assert.equal(names.includes("propose_project"), false);
  assert.equal(names.includes("propose_risk"), false);
  assert.equal(names.includes("propose_hypothesis"), false);
});
