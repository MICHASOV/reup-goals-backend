import assert from "node:assert/strict";
import { test } from "node:test";

import { normalizeProxyURL } from "./provider.js";

test("normalizeProxyURL disables proxy for direct transport", () => {
  assert.equal(normalizeProxyURL("direct"), "");
  assert.equal(normalizeProxyURL(" DIRECT "), "");
  assert.equal(normalizeProxyURL(""), "");
  assert.equal(normalizeProxyURL(undefined), "");
});

test("normalizeProxyURL preserves an explicit SOCKS proxy", () => {
  assert.equal(
    normalizeProxyURL(" socks5://127.0.0.1:10808 "),
    "socks5://127.0.0.1:10808",
  );
});
