#!/usr/bin/env node
// Builds the only runnable sidebar overlay from the immutable dd8 source.
// It intentionally changes only the explicitly audited chat dispatch boundary;
// trusted identity/SDK/read adapters are supplied by the V3 bridge.
import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
const root=path.resolve(path.dirname(new URL(import.meta.url).pathname),"..");
const source=path.join(root,"internal/webshell/static/sidebar_workbench/sidebar_workbench_dd8d60d.js");
const target=path.join(root,"web/dist/sidebar/sidebar_workbench_v3_overlay.js");
let js=fs.readFileSync(source,"utf8");
const want="f20515f3192f3a11048929c7c7b375e1ae274165ae173c0d8415735ffa25424d";
if(crypto.createHash("sha256").update(js).digest("hex")!==want) throw new Error("dd8 sidebar JS source digest mismatch");
for(const anchor of ["function renderProfile()","function renderQuestionnaires()","function renderProducts()","function renderOrders()","function renderCoupons()","function renderMaterials()","function renderRadarLinks(controls)"]){if(js.split(anchor).length!==2)throw new Error(`donor render anchor mismatch: ${anchor}`)}
for(const fragment of ['    ["other_staff_messages", "其他客服聊天"],\n','    other_staff_messages: 9000,\n','      other_staff_messages: null,\n']){if(js.split(fragment).length!==2)throw new Error(`chat removal anchor mismatch: ${fragment}`);js=js.replace(fragment,"")}
// Remove the sole donor branch which fetches/renders archive chat. It is a
// bounded source range; archive ownership and all other consumers remain intact.
const begin='  function renderOtherStaffMessages() {'; const finish='  function renderOwnerPendingWorkbench(message) {';
if(js.split(begin).length!==2||js.split(finish).length!==2)throw new Error("chat renderer range mismatch");
js=js.slice(0,js.indexOf(begin))+js.slice(js.indexOf(finish));
for(const fragment of ['    } else if (tab === "other_staff_messages") {','    if (state.activeTab === "other_staff_messages") renderOtherStaffMessages();']){if(!js.includes(fragment))throw new Error(`chat dispatch anchor mismatch: ${fragment}`)}
// Remaining loader branch is disabled before it can issue the legacy request.
const branchBegin='    } else if (tab === "other_staff_messages") {'; const branchEnd='  async function loadOrders';
const branchAt=js.indexOf(branchBegin), branchAfter=js.indexOf(branchEnd,branchAt);
if(branchAt<0||branchAfter<0) throw new Error("chat loader range mismatch");
js=js.slice(0,branchAt)+'    }\n'+js.slice(branchAfter);

js=js.replace('    if (state.activeTab === "other_staff_messages") renderOtherStaffMessages();\n','');
if(js.includes('other_staff_messages')||js.includes('其他客服聊天'))throw new Error("chat dispatch survived overlay");
fs.mkdirSync(path.dirname(target),{recursive:true});fs.writeFileSync(target,js);
console.log(JSON.stringify({source_sha256:want,target,chat_dispatch_removed:true}));
