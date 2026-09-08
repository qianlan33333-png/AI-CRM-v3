import assert from "node:assert/strict";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import { buildTestBrowserBundle } from "../scripts/test-browser-bundle.mjs";

const root = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../..",
);
const host = await buildTestBrowserBundle(
  path.join(root, "web", "v3", "sidebar", "main.ts"),
);

const intentsByKey = new Map();
const unresolvedByTarget = new Map();
let nextIntentID = 1;
let accepts = 0;
let sendInvocations = 0;

function response(value, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function requestHeaders(init) {
  return new Headers(init?.headers || {});
}

function productScope(body) {
  return body.resource_kind === "product"
    ? String(body.product_type || "")
    : "";
}

function backend(customerID) {
  return async (input, init = {}) => {
    const url = new URL(
      typeof input === "string" ? input : input.url,
      "https://sidebar.test.invalid",
    );
    const method = String(init.method || "GET").toUpperCase();
    if (url.pathname === "/api/sidebar/jssdk-config") {
      return response({
        corp_id: "test-corp",
        agent_id: "test-agent",
        config: {
          timestamp: 1,
          nonceStr: "regular",
          signature: "regular-signature",
        },
        agent_config: {
          timestamp: 1,
          nonceStr: "agent",
          signature: "agent-signature",
        },
      });
    }
    if (url.pathname === "/api/sidebar/v2/bootstrap" && method === "POST") {
      return response({
        state: "ready",
        customer_id: customerID,
        context_token: `context-${customerID}`,
        workbench: {},
      });
    }
    if (url.pathname === "/api/sidebar/v2/send-intents" && method === "POST") {
      const body = JSON.parse(String(init.body || "{}"));
      const idempotencyKey = requestHeaders(init).get("Idempotency-Key");
      const target = `${customerID}:${body.resource_kind}:${productScope(body)}:${body.resource_id}`;
      const exact = intentsByKey.get(idempotencyKey);
      if (exact)
        return response(
          {
            intent_id: exact.id,
            state: exact.state,
            payload: exact.payload,
            replayed: true,
          },
          202,
        );
      const unresolved = unresolvedByTarget.get(target);
      if (unresolved)
        return response(
          {
            intent_id: unresolved.id,
            state: unresolved.state,
            payload: unresolved.payload,
            replayed: true,
          },
          202,
        );
      const intent = {
        id: nextIntentID++,
        key: idempotencyKey,
        target,
        state: "queued",
        grant: `grant-${nextIntentID}`,
        payload: { msgtype: "news", news: { title: "Safe sidebar send" } },
      };
      intentsByKey.set(idempotencyKey, intent);
      unresolvedByTarget.set(target, intent);
      accepts += 1;
      return response(
        {
          intent_id: intent.id,
          state: intent.state,
          grant: intent.grant,
          payload: intent.payload,
        },
        202,
      );
    }
    const completion = url.pathname.match(
      /^\/api\/sidebar\/v2\/send-intents\/(\d+)\/outcome$/,
    );
    if (completion && method === "POST") {
      const intent = [...intentsByKey.values()].find(
        (candidate) => candidate.id === Number(completion[1]),
      );
      const body = JSON.parse(String(init.body || "{}"));
      assert.ok(intent, "completion must reference the accepted intent");
      assert.equal(
        body.grant,
        intent.grant,
        "completion must retain the original one-time grant",
      );
      intent.state = body.outcome;
      if (body.outcome === "outcome_unknown")
        unresolvedByTarget.set(intent.target, intent);
      else unresolvedByTarget.delete(intent.target);
      return response({ intent_id: intent.id, state: intent.state });
    }
    return response({ code: "unexpected" }, 500);
  };
}

function createBridge(customerID, priorSession = []) {
  const dom = new JSDOM('<div id="sidebar-workbench-root"></div>', {
    url: `https://sidebar.test.invalid/sidebar/bind-mobile?external_userid=external-${customerID}`,
    runScripts: "outside-only",
    pretendToBeVisual: true,
    beforeParse(window) {
      window.Response = Response;
      window.Headers = Headers;
      window.fetch = backend(customerID);
      window.wx = {
        config() {
          queueMicrotask(() => ready?.());
        },
        ready(callback) {
          ready = callback;
        },
        error() {},
        agentConfig(options) {
          queueMicrotask(() => options.success?.());
        },
        invoke(method, _payload, callback) {
          if (method === "getCurExternalContact") {
            queueMicrotask(() =>
              callback({
                err_msg: "getCurExternalContact:ok",
                external_userid: `external-${customerID}`,
              }),
            );
            return;
          }
          if (method === "sendChatMessage") {
            sendInvocations += 1;
            queueMicrotask(() => callback({ err_msg: "sendChatMessage:fail" }));
            return;
          }
          queueMicrotask(() => callback({ err_msg: `${method}:fail` }));
        },
      };
      let ready;
    },
  });
  for (const [key, value] of priorSession)
    dom.window.sessionStorage.setItem(key, value);
  dom.window.eval(host);
  dom.window.document.dispatchEvent(new dom.window.Event("DOMContentLoaded"));
  const bridge = dom.window.__AICRMSidebarBridge;
  assert.ok(bridge, "actual SidebarBridge must mount from the compiled Host");
  return { dom, bridge };
}

function sessionEntries(dom) {
  return Array.from(
    { length: dom.window.sessionStorage.length },
    (_, index) => {
      const key = dom.window.sessionStorage.key(index);
      return [key, dom.window.sessionStorage.getItem(key)];
    },
  );
}

const first = createBridge(1);
const card = {
  resource_kind: "product",
  resource_id: "7",
  product_type: "standard",
};
const duplicate = await Promise.allSettled([
  first.bridge.send(card),
  first.bridge.send(card),
]);
assert.equal(
  duplicate.filter((result) => result.status === "rejected").length,
  2,
  "a failed SDK response must fail both duplicate clicks",
);
assert.equal(accepts, 1, "duplicate clicks must accept one durable intent");
assert.equal(sendInvocations, 1, "duplicate clicks must invoke JSSDK once");

const reloaded = createBridge(1, sessionEntries(first.dom));
await assert.rejects(() => reloaded.bridge.send(card), /执行凭据未返回/);
assert.equal(
  accepts,
  1,
  "a reloaded bridge must re-read the unknown intent instead of accepting a new key",
);
assert.equal(
  sendInvocations,
  1,
  "a reloaded bridge must not invoke JSSDK for outcome_unknown",
);

const switchedCustomer = createBridge(2, sessionEntries(reloaded.dom));
await assert.rejects(
  () => switchedCustomer.bridge.send(card),
  /sendChatMessage/,
);
assert.equal(
  accepts,
  2,
  "a customer switch must not inherit another customer's send lock",
);
assert.equal(
  sendInvocations,
  2,
  "the isolated customer may create its own SDK attempt",
);

await assert.rejects(
  () =>
    switchedCustomer.bridge.send({
      resource_kind: "product",
      resource_id: "7",
      product_type: "service_period",
    }),
  /sendChatMessage/,
);
assert.equal(
  accepts,
  3,
  "standard and service-period products with equal numeric IDs must remain separate bindings",
);
assert.equal(
  sendInvocations,
  3,
  "the separate product binding may invoke its own SDK attempt",
);

for (const fixture of [first, reloaded, switchedCustomer])
  fixture.dom.window.close();
console.log(
  "sidebar Host reload, duplicate-send, and customer-scope recovery: PASS",
);
