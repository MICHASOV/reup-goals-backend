import assert from "node:assert/strict";
import test from "node:test";

import { durationMilliseconds, isSecureInternalURL } from "./transport.js";

test("production internal transport accepts HTTPS and loopback HTTP", () => {
  assert.equal(isSecureInternalURL("https://api.reupgoals.pro"), true);
  assert.equal(isSecureInternalURL("http://127.0.0.1:18080"), true);
  assert.equal(isSecureInternalURL("http://localhost:8080"), true);
  assert.equal(isSecureInternalURL("http://api.example.com"), false);
  assert.equal(isSecureInternalURL("invalid"), false);
});

test("durationMilliseconds parses bounded runtime settings", () => {
  assert.equal(durationMilliseconds("45m", 1), 2_700_000);
  assert.equal(durationMilliseconds("2h", 1), 7_200_000);
  assert.equal(durationMilliseconds("invalid", 123), 123);
});
