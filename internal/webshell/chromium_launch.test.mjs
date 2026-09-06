import assert from "node:assert/strict";
import test from "node:test";
import { chromiumStartupDiagnostic, chromiumStartupTimeoutMS } from "./chromium_launch.mjs";

test("reports a bounded, redacted early Chromium exit", () => {
  const profile="/tmp/aicrm-owner-handoff-chromium-fixture";
  const diagnostic=chromiumStartupDiagnostic({profile, exitCode:23, stderr:`fatal profile=${profile}\n`});
  assert.match(diagnostic, /exited before remote debugging/);
  assert.match(diagnostic, /exit_code=23/);
  assert.match(diagnostic, /<profile>/);
  assert.doesNotMatch(diagnostic, new RegExp(profile.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")));
});

test("keeps a live Chromium startup failure bounded and distinct", () => {
  const diagnostic=chromiumStartupDiagnostic({stderr:"still initializing"});
  assert.equal(chromiumStartupTimeoutMS, 30_000);
  assert.match(diagnostic, /within 30000ms/);
  assert.match(diagnostic, /process=still_running/);
});

test("reports spawn failures without raw error data", () => {
  const diagnostic=chromiumStartupDiagnostic({launchError:{code:"EAGAIN", message:"secret should not render"}});
  assert.match(diagnostic, /category=EAGAIN/);
  assert.doesNotMatch(diagnostic, /secret/);
});
