import assert from "node:assert/strict";
import fs from "node:fs";
import { JSDOM, VirtualConsole } from "jsdom";
import { buildTestBrowserBundle } from "../scripts/test-browser-bundle.mjs";
const host = await buildTestBrowserBundle("web/v3/productAdapter.ts");

const wait = async (check) => {
  for (let n = 0; n < 100; n++) {
    if (check()) return;
    await new Promise((r) => setTimeout(r, 20));
  }
  throw Error("timeout");
};
const product = {
  id: 101,
  product_code: "fixture",
  name: "测试商品",
  price_minor: 990,
  currency: "CNY",
  stock_quantity: 1,
  description: "",
  paid_order_count: 0,
  refund_order_count: 0,
  sold_count: 0,
  created_at: "2026-09-08T00:00:00Z",
  updated_at: "2026-09-08T00:00:00Z",
  images: [],
  version: 1,
  lifecycle: "enabled",
  enabled: true,
  admin_projection: {
    schema_version: 1,
    status: "active",
    enabled: true,
    buy_button_text: "立即购买",
    require_mobile: false,
    lead_program_id: null,
    lead_channel_id: null,
    lead_qr_title: "",
    lead_qr_subtitle: "",
    completion_redirect_enabled: false,
    completion_redirect_url: "",
    completion_target: null,
    purchase_action_enabled: false,
    purchase_action_mode: "",
    wecom_tagging: {},
    slices: [],
  },
};
const mapping = {
  version: 1,
  fields: [
    {
      key: "phone",
      source: "variable",
      value_type: "string",
      variable: "order.mobile",
    },
    { key: "active", source: "fixed", value_type: "boolean", value: false },
  ],
};
for (const periodic of [false, true])
  for (const mode of ["mapped", "legacy", "fresh"]) {
    const legacy = mode === "legacy";
    const fresh = mode === "fresh";
    const page = fs.readFileSync(
      periodic
        ? "web/dist/admin/spProductForm.html"
        : "web/dist/admin/productForm.html",
      "utf8",
    );
    const route = periodic
      ? "/admin/service-period-products/101/edit"
      : "/admin/wechat-pay/products/101/edit";
    const calls = [];
    const config = {
      product_id: 101,
      product_kind: periodic ? "service_period" : "wechat_pay",
      enabled: true,
      configuration_reference: fresh ? "" : "fixture",
      url: "https://example.test/paid",
      revision: fresh ? 0 : 1,
      type: "paid_notify",
      day: null,
      frequency: null,
      expires_at_ts: null,
      remark: "fixture",
      custom_params: {},
      custom_params_json: "{}",
      field_mapping: legacy || fresh ? null : mapping,
    };
    const dom = new JSDOM(page, {
      url: "https://example.test" + route,
      runScripts: "outside-only",
      pretendToBeVisual: true,
      virtualConsole: new VirtualConsole(),
      beforeParse(w) {
        w.Request = Request;
        w.Response = Response;
        w.Headers = Headers;
        w.fetch = async (input, init = {}) => {
          const url = new URL(
            input instanceof Request ? input.url : String(input),
            w.location.href,
          );
          const method = init.method || "GET";
          const body = init.body ? JSON.parse(init.body) : null;
          calls.push({ path: url.pathname, method, body });
          let value =
            url.pathname === "/api/v1/products"
              ? { items: [product] }
              : { items: [], total: 0 };
          if (url.pathname === "/api/admin/service-period-products/101")
            value = {
              product: {
                ...product,
                service_product_id: 101,
                duration_days: 90,
              },
            };
          else if (url.pathname.endsWith("/preview"))
            value = {
              payload_json: '{"example":true}',
              legacy_payload_json:
                '{"event":"paid","order":{"id":"synthetic"}}',
              synthetic: true,
              real_external_call_executed: false,
            };
          else if (url.pathname.endsWith("/external-push")) value = config;
          else if (url.pathname === "/api/v1/products/101") value = product;
          return new Response(JSON.stringify(value), {
            headers: { "Content-Type": "application/json" },
          });
        };
      },
    });
    dom.window.eval(host);
    dom.window.document.dispatchEvent(new dom.window.Event("DOMContentLoaded"));
    const d = dom.window.document;
    await wait(() =>
      d.querySelector("[data-external-push-configuration-save]"),
    ).catch((e) => {
      console.log(calls, d.body.textContent.slice(-1600));
      throw e;
    });
    await wait(() =>
      d
        .querySelector("[data-external-push-configuration-status]")
        ?.textContent.includes("配置版本"),
    ).catch((e) => {
      console.log(
        calls,
        d.querySelector("[data-external-push-configuration-status]")
          ?.textContent,
      );
      throw e;
    });
    const save = d.querySelector("[data-external-push-configuration-save]");
    if (legacy) {
      save.click();
      await wait(() =>
        calls.some(
          (c) => c.method === "PUT" && c.path.endsWith("/external-push"),
        ),
      );
      assert.equal(
        Object.hasOwn(
          calls.find(
            (c) => c.method === "PUT" && c.path.endsWith("/external-push"),
          ).body,
          "field_mapping",
        ),
        false,
        "legacy save never switches protocol",
      );
      [...d.querySelectorAll("button")]
        .find((b) => b.textContent === "转换为字段映射")
        .click();
      await wait(() => d.querySelector("[data-mapping-conversion]"));
      assert.match(
        d.querySelector("[data-mapping-conversion]").textContent,
        /synthetic/,
      );
      const count = calls.filter(
        (c) => c.method === "PUT" && c.path.endsWith("/external-push"),
      ).length;
      [...d.querySelectorAll("button")]
        .find((b) => b.textContent === "确认转换")
        .click();
      assert.equal(
        calls.filter(
          (c) => c.method === "PUT" && c.path.endsWith("/external-push"),
        ).length,
        count,
        "confirm changes only draft",
      );
    }
    save.click();
    await wait(
      () =>
        calls.filter(
          (c) => c.method === "PUT" && c.path.endsWith("/external-push"),
        ).length === (legacy ? 2 : 1),
    );
    const payload = calls
      .filter((c) => c.method === "PUT" && c.path.endsWith("/external-push"))
      .at(-1).body;
    assert.equal(payload.field_mapping.version, 1);
    if (!legacy && !fresh) assert.deepEqual(payload.field_mapping, mapping);
    if (fresh) {
      assert.deepEqual(payload.field_mapping, { version: 1, fields: [] });
      assert.equal(d.querySelectorAll("[data-mapping-conversion]").length, 0);
    }
    assert.equal(dom.window.location.pathname, route);
    assert.equal(
      calls.some((c) => c.path.endsWith("/test") && c.method !== "GET"),
      false,
      "preview and save never test outbound",
    );
    dom.window.close();
  }
console.log(
  "product mapping Host load/save and explicit legacy conversion: PASS",
);
