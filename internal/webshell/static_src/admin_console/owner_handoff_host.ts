import { ownerMigrationRowsFromFile, ownerMigrationTemplateXLSX, ownerMigrationWorkbookXLSX } from "./owner_migration_file";

type Staff = { ID: number; UserID: string; DisplayName: string; Active: boolean };
type Context = { staff: Staff[]; operator: string };
type Row = { Line: number; CustomerID: number; ExpectedOwnerID: number; ExternalUserID: string; CustomerDisplayName: string; CurrentOwnerUserID: string; State: string; Reason: string };
type Preview = { ID: string; Hash: string; ConfirmationPhrase: string; Rows: Row[]; Mode: string };
type BatchLine = { Line: number; CustomerID: number; State: string; TransferStatus: number };
type Batch = { ID: string; State: string; Mode: string; Lines?: BatchLine[] };
type ImportedRow = { Line: number; ExternalUserID: string; MoveFlag: string; CurrentOwnerUserID: string; CustomerDisplayName: string; Remark: string; ParseStatus: string; ParseReason: string };
type DisplayRow = { Line: number; ExternalUserID: string; CustomerDisplayName: string; MoveFlag: string; CurrentOwnerUserID: string; Remark: string; State: string; Reason: string; CustomerID?: number };
type OperationMember = { user_id: string; display_name?: string };
type SharedPicker = { open(options: { scope: string; pageSize: number; includeInactive: boolean; allowRefresh: boolean; title: string; onSelect(member: OperationMember): void }): Promise<void> };

declare global { interface Window { OperationMemberPicker?: SharedPicker } }

const donorURL = "/static/admin_console/owner_migration_dd8d60d.html";
const pickerURL = "/static/admin_console/operation_member_picker_dd8d60d.js";
const key = () => `owner-handoff-${crypto.getRandomValues(new Uint32Array(2)).join("-")}`;
const text = (value: unknown) => String(value ?? "").trim();
const esc = (value: unknown) => text(value).replace(/[&<>'"]/g, char => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&#39;", '"': "&quot;" }[char] || char));

type RequestFailure = Error & { httpStatus?: number };
const requestFailure = (message: string, status: number): RequestFailure => {
  const error = new Error(message) as RequestFailure;
  error.httpStatus = status;
  return error;
};
let pickerLoad: Promise<SharedPicker> | undefined;

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  if ((init?.method || "GET").toUpperCase() !== "GET") {
    if (!headers.has("Content-Type")) headers.set("Content-Type", "application/json");
    if (!headers.has("X-CSRF-Token")) {
      const cookie = document.cookie.split(";").map(part => part.trim()).find(part => part.startsWith("aicrm_admin_csrf="));
      if (cookie) headers.set("X-CSRF-Token", decodeURIComponent(cookie.slice("aicrm_admin_csrf=".length)));
    }
  }
  const response = await fetch(path, { credentials: "same-origin", ...init, headers });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw requestFailure(text(body.error) || `请求失败（${response.status}）`, response.status);
  return body as T;
}

function scrubFrozenServerPlaceholders(page: HTMLElement): void {
  const marker = /\{\{|\{%/;
  const replacement = /\{\{[\s\S]*?\}\}|\{%[\s\S]*?%\}/g;
  [page, ...page.querySelectorAll<HTMLElement>("*")].forEach(element => {
    [...element.attributes].forEach(attribute => {
      if (marker.test(attribute.name)) element.removeAttribute(attribute.name);
      else if (marker.test(attribute.value)) element.setAttribute(attribute.name, attribute.value.replace(replacement, ""));
    });
  });
  const walker = document.createTreeWalker(page, NodeFilter.SHOW_TEXT);
  for (let node = walker.nextNode(); node; node = walker.nextNode()) node.nodeValue = (node.nodeValue || "").replace(replacement, "");
}

async function mountFrozenDonor(stage: HTMLElement): Promise<HTMLElement> {
  const response = await fetch(donorURL, { credentials: "same-origin" });
  if (!response.ok) throw requestFailure(`冻结页面资源不可用（${response.status}）`, response.status);
  const source = new DOMParser().parseFromString(await response.text(), "text/html");
  const page = source.querySelector<HTMLElement>("[data-owner-migration-page]");
  const style = source.querySelector("style");
  if (!page || !style) throw new Error("冻结页面不含迁移工作台");
  // dd8d60d supplies the page, field order, copy and hooks. The Host only
  // mounts it, removes unrendered Jinja tokens, and connects stable V3 ports.
  const cloned = page.cloneNode(true) as HTMLElement;
  scrubFrozenServerPlaceholders(cloned);
  stage.replaceChildren(style.cloneNode(true), cloned);
  const mounted = stage.querySelector<HTMLElement>("[data-owner-migration-page]");
  if (!mounted) throw new Error("冻结迁移页面未挂载");
  return mounted;
}

function query<T extends Element>(root: ParentNode, selector: string): T {
  const node = root.querySelector<T>(selector);
  if (!node) throw new Error(`冻结页面缺少 ${selector}`);
  return node;
}

function sharedPicker(): Promise<SharedPicker> {
  if (window.OperationMemberPicker) return Promise.resolve(window.OperationMemberPicker);
  if (!pickerLoad) {
    pickerLoad = new Promise<SharedPicker>((resolve, reject) => {
      const existing = document.querySelector<HTMLScriptElement>('script[data-owner-handoff-shared-picker]');
      const finish = () => window.OperationMemberPicker ? resolve(window.OperationMemberPicker) : reject(new Error("冻结员工选择器未注册"));
      if (existing) { existing.addEventListener("load", finish, { once: true }); existing.addEventListener("error", () => reject(new Error("冻结员工选择器不可用")), { once: true }); return; }
      const script = document.createElement("script");
      script.src = pickerURL;
      script.async = true;
      script.dataset.ownerHandoffSharedPicker = "dd8d60d";
      script.addEventListener("load", finish, { once: true });
      script.addEventListener("error", () => reject(new Error("冻结员工选择器不可用")), { once: true });
      document.head.append(script);
    });
  }
  return pickerLoad;
}

async function installPicker(root: HTMLElement, staff: Staff[]): Promise<void> {
  const picker = await sharedPicker();
  const choose = async (kind: "source" | "target") => {
    await picker.open({
      scope: "owner_migration",
      pageSize: 100,
      includeInactive: kind === "source",
      allowRefresh: false,
      title: kind === "source" ? "选择原负责人" : "选择目标负责人",
      onSelect(member) {
        const memberID = text(member.user_id);
        const selected = staff.find(value => value.UserID === memberID);
        if (!selected || (kind === "target" && !selected.Active)) return;
        query<HTMLInputElement>(root, `[data-owner-userid="${kind}"]`).value = String(selected.ID);
        query<HTMLInputElement>(root, `[data-owner-label="${kind}"]`).value = selected.DisplayName || selected.UserID;
        root.dispatchEvent(new Event("owner-handoff-change"));
      },
    });
  };
  root.querySelectorAll<HTMLButtonElement>("[data-owner-picker]").forEach(button => button.addEventListener("click", () => { void choose(button.dataset.ownerPicker as "source" | "target"); }));
}

function currentMode(root: ParentNode): string { return query<HTMLInputElement>(root, "[data-include-wecom-transfer]").checked ? "wecom_then_crm" : "local_only"; }
function ownerID(root: ParentNode, kind: "source" | "target"): number { return Number(query<HTMLInputElement>(root, `[data-owner-userid="${kind}"]`).value); }
function ownerUserID(root: ParentNode, kind: "source" | "target", staff: Staff[]): string {
  const selected = staff.find(member => member.ID === ownerID(root, kind));
  return text(selected?.UserID);
}
function selectedScope(root: ParentNode): string { return query<HTMLInputElement>(root, 'input[name="scope_type"]:checked').value; }
function transferStatusLabel(status: number): string {
  return ({ 0: "本地迁移", 1: "企微转接已完成", 2: "企微转接处理中", 3: "客户拒绝接替", 4: "目标成员客户上限", 5: "未找到企微转接记录" } as Record<number, string>)[status] || `企微状态 ${status}`;
}
function downloadBlob(filename: string, blob: Blob): void {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  link.hidden = true;
  document.body.append(link);
  link.click();
  // Chromium resolves blob: downloads asynchronously. Keep the URL alive until
  // that hand-off completes instead of revoking it in the click stack.
  window.setTimeout(() => { link.remove(); URL.revokeObjectURL(url); }, 1000);
}

function downloadWorkbook(filename: string, headers: string[], rows: string[][]): void {
  downloadBlob(filename, new Blob([ownerMigrationWorkbookXLSX(headers, rows)], { type: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" }));
}

function normalizeMoveFlag(value: string): [string, boolean] {
  const normalized = text(value).toLowerCase();
  if (new Set(["", "是", "y", "yes", "true", "1", "迁移"]).has(normalized)) return ["是", true];
  if (new Set(["否", "n", "no", "false", "0", "不迁移"]).has(normalized)) return ["否", true];
  return [text(value), false];
}

function normalizeImportedRows(rawRows: string[][], sourceUserID: string): ImportedRow[] {
  const seen = new Set<string>();
  return rawRows.map((row, index) => {
    const external = text(row[0]);
    const [moveFlag, validFlag] = normalizeMoveFlag(text(row[1]));
    const current = text(row[2]);
    let parseStatus = "parsed";
    let parseReason = "";
    if (!external) { parseStatus = "missing_external_userid"; parseReason = "external_userid is required"; }
    else if (!validFlag) { parseStatus = "invalid_move_flag"; parseReason = "是否迁移字段非法"; }
    else if (seen.has(external)) { parseStatus = "duplicate"; parseReason = "duplicate external_userid; first row is kept"; }
    else {
      seen.add(external);
      if (current && current !== sourceUserID) parseReason = "当前负责人userid与选择的原负责人不一致，预览阶段将不可执行";
    }
    return { Line: index + 2, ExternalUserID: external, MoveFlag: moveFlag, CurrentOwnerUserID: current, CustomerDisplayName: text(row[3]), Remark: text(row[4]), ParseStatus: parseStatus, ParseReason: parseReason };
  });
}

function importStats(rows: ImportedRow[]): Record<string, number> {
  const unique = new Set(rows.filter(row => row.ExternalUserID && row.ParseStatus !== "duplicate").map(row => row.ExternalUserID));
  return {
    total_rows: rows.length,
    unique_external_userids: unique.size,
    marked_move: rows.filter(row => row.ParseStatus === "parsed" && row.MoveFlag === "是").length,
    marked_skip: rows.filter(row => row.ParseStatus === "parsed" && row.MoveFlag === "否").length,
    duplicate_rows: rows.filter(row => row.ParseStatus === "duplicate").length,
    invalid_rows: rows.filter(row => row.ParseStatus === "missing_external_userid" || row.ParseStatus === "invalid_move_flag").length,
  };
}

function displayFromServer(row: Row, item: ImportedRow): DisplayRow {
  const mappedState = row.State === "conflict" ? "not_under_source_owner" : row.State === "unresolved" ? "not_found" : row.State;
  return { Line: item.Line, ExternalUserID: row.ExternalUserID || item.ExternalUserID, CustomerDisplayName: row.CustomerDisplayName || item.CustomerDisplayName, MoveFlag: item.MoveFlag, CurrentOwnerUserID: row.CurrentOwnerUserID || item.CurrentOwnerUserID, Remark: item.Remark, State: mappedState, Reason: row.Reason || "已通过预览校验", CustomerID: row.CustomerID };
}

function previewDisplayRows(preview: Preview, scope: string, imported: ImportedRow[], sourceUserID: string): DisplayRow[] {
  if (scope !== "excel_include") return preview.Rows.map(row => ({ Line: row.Line, ExternalUserID: row.ExternalUserID, CustomerDisplayName: row.CustomerDisplayName, MoveFlag: "是", CurrentOwnerUserID: row.CurrentOwnerUserID, Remark: "", State: row.State, Reason: row.Reason || "已通过预览校验", CustomerID: row.CustomerID }));
  const serverRows = new Map(preview.Rows.map(row => [row.ExternalUserID, row]));
  return imported.map(item => {
    if (item.ParseStatus !== "parsed") return { ...item, State: item.ParseStatus, Reason: item.ParseReason };
    if (item.MoveFlag === "否") return { ...item, State: "skipped_by_file", Reason: "Excel marked skip" };
    if (item.CurrentOwnerUserID && item.CurrentOwnerUserID !== sourceUserID) return { ...item, State: "not_under_source_owner", Reason: "当前负责人userid与选择的原负责人不一致" };
    const server = serverRows.get(item.ExternalUserID);
    if (!server) return { ...item, State: "not_found", Reason: "未得到该行的安全预览结果" };
    return displayFromServer(server, item);
  });
}

function renderRows(root: HTMLElement, rows: DisplayRow[], scope: string, source: number, target: number): void {
  const ready = rows.filter(row => row.State === "ready").length;
  const skipped = rows.filter(row => row.State === "skipped_by_file").length;
  const blocked = rows.length - ready - skipped;
  query<HTMLElement>(root, "[data-preview-basic]").textContent = `${scope === "excel_include" ? "Excel 指定名单" : "全部客户"} · 原负责人 #${source} → 目标负责人 #${target} · ${ready} 个可迁移客户；${blocked} 个不可迁移。`;
  const values: Record<string, number> = { total_rows: rows.length, unique_external_userids: new Set(rows.map(row => row.ExternalUserID).filter(Boolean)).size, ready, skipped_by_file: skipped, blocked, crm_updates: ready };
  Object.entries(values).forEach(([name, value]) => { const node = root.querySelector<HTMLElement>(`[data-preview-stat="${name}"]`); if (node) node.textContent = String(value); });
  query<HTMLElement>(root, "[data-preview-rows]").innerHTML = rows.map(row => `<tr><td>${row.Line}</td><td><code>${esc(row.ExternalUserID)}</code></td><td>${esc(row.CustomerDisplayName)}</td><td>${esc(row.MoveFlag)}</td><td>${esc(row.CurrentOwnerUserID)}</td><td><span class="owner-migration-status owner-migration-status--${row.State === "ready" ? "ready" : row.State === "skipped_by_file" ? "skip" : "block"}">${esc(row.State)}</span></td><td>${esc(row.Reason)}</td></tr>`).join("") || '<tr><td colspan="7" class="owner-migration-empty">当前范围没有候选客户。</td></tr>';
  query<HTMLButtonElement>(root, "[data-download-errors]").disabled = blocked === 0;
  query<HTMLButtonElement>(root, "[data-execute]").disabled = ready === 0;
}

function renderPreview(root: HTMLElement, preview: Preview, scope: string, imported: ImportedRow[], sourceUserID: string): DisplayRow[] {
  query<HTMLElement>(root, "[data-preview-empty]").hidden = true;
  query<HTMLElement>(root, "[data-preview-content]").hidden = false;
  const rows = previewDisplayRows(preview, scope, imported, sourceUserID);
  renderRows(root, rows, scope, ownerID(root, "source"), ownerID(root, "target"));
  query<HTMLElement>(root, "[data-confirm-phrase-display]").textContent = preview.ConfirmationPhrase;
  return rows;
}

function renderBatch(root: HTMLElement, batch: Batch): void {
  query<HTMLElement>(root, "[data-execution-log]").textContent = [
    `batch_id=${batch.ID}`,
    `mode=${batch.Mode}`,
    `batch_state=${batch.State}`,
    ...(batch.Lines || []).map(line => `line_no=${line.Line} customer_id=${line.CustomerID} state=${line.State} transfer_status=${line.TransferStatus} (${transferStatusLabel(line.TransferStatus)})`),
  ].join("\n");
}

async function boot(): Promise<void> {
  const stage = document.querySelector<HTMLElement>("[data-owner-handoff-host]");
  if (!stage) return;
  stage.dataset.ownerHandoffInit = "mounting";
  try {
    const root = await mountFrozenDonor(stage);
    stage.dataset.ownerHandoffInit = "donor_loaded";
    const context = await api<Context>("/api/admin/customers/owner-handoffs/context");
    stage.dataset.ownerHandoffInit = "context_loaded";
    await installPicker(root, context.staff || []);
    query<HTMLInputElement>(root, '[data-owner-label="source"]').value = "";
    query<HTMLInputElement>(root, '[data-owner-label="target"]').value = "";
    query<HTMLInputElement>(root, '[data-owner-userid="source"]').value = "";
    query<HTMLInputElement>(root, '[data-owner-userid="target"]').value = "";
    query<HTMLInputElement>(root, "#operator").value = context.operator || "当前登录管理员";
    query<HTMLTextAreaElement>(root, "[data-transfer-welcome-msg]").value = "您好，后续将由新的服务同事继续为您服务。";
    query<HTMLInputElement>(root, "[data-include-wecom-transfer]").checked = true;
    query<HTMLElement>(root, "[data-wecom-pill]").textContent = "企微转接：默认开启";
    query<HTMLElement>(root, "[data-local-only-warning]").hidden = true;
    query<HTMLInputElement>(root, "[data-import-file]").setAttribute("accept", ".xlsx,.xls,.csv");
    const updateWelcomeCount = () => { query<HTMLElement>(root, "[data-welcome-count]").textContent = `${text(query<HTMLTextAreaElement>(root, "[data-transfer-welcome-msg]").value).length} 字`; };
    updateWelcomeCount();
    let preview: Preview | undefined;
    let batch: Batch | undefined;
    let fileExternalIDs: string[] = [];
    let importedRows: ImportedRow[] = [];
    let displayedRows: DisplayRow[] = [];
    const notice = query<HTMLElement>(root, "[data-workbench-notice]");
    const setNotice = (value: string, kind = "") => { notice.textContent = value; notice.className = `owner-migration-hint ${kind}`; };
    const reset = () => {
      preview = undefined; batch = undefined; displayedRows = [];
      query<HTMLElement>(root, "[data-preview-empty]").hidden = false;
      query<HTMLElement>(root, "[data-preview-content]").hidden = true;
      query<HTMLInputElement>(root, "[data-confirm-phrase-input]").value = "";
      query<HTMLButtonElement>(root, "[data-execute]").disabled = true;
      query<HTMLButtonElement>(root, "[data-download-errors]").disabled = true;
      query<HTMLButtonElement>(root, "[data-download-result]").disabled = true;
      const transferReader = root.querySelector<HTMLButtonElement>("[data-read-transfer-result]");
      if (transferReader) transferReader.disabled = true;
      query<HTMLElement>(root, "[data-execution-log]").textContent = "尚未执行。";
    };
    const updateWeComPresentation = () => {
      const enabled = query<HTMLInputElement>(root, "[data-include-wecom-transfer]").checked;
      const pill = query<HTMLElement>(root, "[data-wecom-pill]");
      pill.textContent = enabled ? "企微转接：默认开启" : "企微转接：已关闭";
      pill.classList.toggle("owner-migration-pill--success", enabled);
      pill.classList.toggle("owner-migration-pill--warn", !enabled);
      query<HTMLElement>(root, "[data-local-only-warning]").hidden = enabled;
    };
    root.addEventListener("owner-handoff-change", reset);
    root.querySelectorAll<HTMLElement>("[data-scope-segment]").forEach(segment => segment.addEventListener("click", () => {
      const scope = segment.dataset.scopeSegment || "all";
      root.querySelectorAll<HTMLElement>("[data-scope-segment]").forEach(value => value.classList.toggle("is-active", value === segment));
      query<HTMLElement>(root, "[data-mode-pill]").textContent = scope === "excel_include" ? "模式：Excel 指定名单" : "模式：全量迁移";
      query<HTMLInputElement>(root, `input[name="scope_type"][value="${scope}"]`).checked = true;
      query<HTMLElement>(root, "[data-excel-panel]").hidden = scope !== "excel_include";
      reset();
    }));
    query<HTMLButtonElement>(root, "[data-upload-file]").addEventListener("click", async () => {
      try {
        const source = ownerID(root, "source"); const target = ownerID(root, "target");
        const sourceUserID = ownerUserID(root, "source", context.staff || []);
        if (!source || !target || source === target || !sourceUserID) throw new Error("请先选择不同的原负责人和目标负责人");
        const file = query<HTMLInputElement>(root, "[data-import-file]").files?.[0];
        if (!file) throw new Error("请选择包含旧模板五列的 XLSX、XLS 或 CSV 文件");
        const rawRows = await ownerMigrationRowsFromFile(file);
        const headers = rawRows.shift() || [];
        const expectedHeaders = ["external_userid", "是否迁移", "当前负责人userid", "客户备注名", "备注"];
        if (headers.length !== expectedHeaders.length || headers.some((header, index) => text(header) !== expectedHeaders[index])) throw new Error(`第一行必须且只能是：${expectedHeaders.join("、")}`);
        if (rawRows.some(row => row.length !== expectedHeaders.length)) throw new Error("每一行必须包含旧模板的五列");
        importedRows = normalizeImportedRows(rawRows, sourceUserID);
        fileExternalIDs = importedRows.filter(row => row.ParseStatus === "parsed" && row.MoveFlag === "是" && row.ExternalUserID && (!row.CurrentOwnerUserID || row.CurrentOwnerUserID === sourceUserID)).map(row => row.ExternalUserID);
        const stats = importStats(importedRows);
        query<HTMLElement>(root, "[data-import-summary]").hidden = false;
        query<HTMLElement>(root, "[data-import-filename]").textContent = file.name;
        Object.entries(stats).forEach(([name, value]) => { const node = root.querySelector<HTMLElement>(`[data-import-stat="${name}"]`); if (node) node.textContent = String(value); });
        reset();
        setNotice("旧模板名单已解析；预览会保留每一行的标记、重复和负责人校验结果。", "ok");
      } catch (error) { setNotice(error instanceof Error ? error.message : "文件解析失败", "error"); }
    });
    root.querySelectorAll<HTMLInputElement>('input[name="scope_type"]').forEach(input => input.addEventListener("change", reset));
    query<HTMLInputElement>(root, "[data-include-wecom-transfer]").addEventListener("change", () => { updateWeComPresentation(); reset(); });
    query<HTMLTextAreaElement>(root, "[data-transfer-welcome-msg]").addEventListener("input", () => { updateWelcomeCount(); reset(); });
    query<HTMLButtonElement>(root, "[data-download-template]").addEventListener("click", () => {
      downloadBlob("owner_migration_template.xlsx", new Blob([ownerMigrationTemplateXLSX()], { type: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" }));
    });
    query<HTMLButtonElement>(root, "[data-preview]").addEventListener("click", async () => {
      try {
        const source = ownerID(root, "source"); const target = ownerID(root, "target");
        const sourceUserID = ownerUserID(root, "source", context.staff || []);
        if (!source || !target || source === target || !sourceUserID) throw new Error("请先选择不同的原负责人和目标负责人");
        const scope = selectedScope(root);
        if (scope === "excel_include" && !importedRows.length) throw new Error("请先上传旧模板名单");
        if (scope === "excel_include" && !fileExternalIDs.length) {
          displayedRows = importedRows.map(row => row.ParseStatus === "parsed" && row.MoveFlag === "否" ? { ...row, State: "skipped_by_file", Reason: "Excel marked skip" } : { ...row, State: row.ParseStatus, Reason: row.ParseReason || "没有可执行迁移行" });
          query<HTMLElement>(root, "[data-preview-empty]").hidden = true;
          query<HTMLElement>(root, "[data-preview-content]").hidden = false;
          renderRows(root, displayedRows, scope, source, target);
          setNotice("文件没有可执行迁移行，已保留逐行校验结果，不能确认执行。", "ok");
          return;
        }
        preview = await api<Preview>("/api/admin/customers/owner-handoffs/previews", { method: "POST", body: JSON.stringify({ mode: currentMode(root), scope, source_staff_id: source, target_staff_id: target, customer_ids: [], external_userids: scope === "excel_include" ? fileExternalIDs : [], welcome_message: query<HTMLTextAreaElement>(root, "[data-transfer-welcome-msg]").value, confirmation_phrase: `确认将当前候选客户迁移到 ${target}`, idempotency_key: key() }) });
        displayedRows = renderPreview(root, preview, scope, importedRows, sourceUserID);
        setNotice("预览已生成，请逐字输入确认短语。", "ok");
      } catch (error) { setNotice(error instanceof Error ? error.message : "预览失败", "error"); }
    });
    query<HTMLButtonElement>(root, "[data-execute]").addEventListener("click", async () => {
      try {
        if (!preview) throw new Error("请先生成预览");
        const phrase = query<HTMLInputElement>(root, "[data-confirm-phrase-input]").value;
        if (phrase !== preview.ConfirmationPhrase) throw new Error("确认短语不匹配");
        batch = await api<Batch>("/api/admin/customers/owner-handoffs/confirm", { method: "POST", body: JSON.stringify({ preview_id: preview.ID, preview_hash: preview.Hash, confirmation_phrase: phrase, idempotency_key: key() }) });
        renderBatch(root, batch);
        query<HTMLButtonElement>(root, "[data-download-result]").disabled = false;
        readTransfer.disabled = false;
        setNotice("迁移已受理；结果导出和企微结果读取会显示每一行实际状态。", "ok");
      } catch (error) { setNotice(error instanceof Error ? error.message : "执行失败", "error"); }
    });
    query<HTMLButtonElement>(root, "[data-reset-workbench]").addEventListener("click", reset);
    query<HTMLButtonElement>(root, "[data-download-errors]").addEventListener("click", () => {
      const blocked = displayedRows.filter(row => row.State !== "ready" && row.State !== "skipped_by_file");
      if (!blocked.length) return;
      downloadWorkbook("owner_migration_blocked_rows.xlsx", ["行号", "external_userid", "客户备注名", "Excel 标记", "当前负责人userid", "备注", "状态", "原因"], blocked.map(row => [String(row.Line), row.ExternalUserID, row.CustomerDisplayName, row.MoveFlag, row.CurrentOwnerUserID, row.Remark, row.State, row.Reason]));
    });
    query<HTMLButtonElement>(root, "[data-download-result]").addEventListener("click", async () => {
      if (!batch) { setNotice("请先执行迁移，再导出结果明细。", "error"); return; }
      try {
        batch = await api<Batch>(`/api/admin/customers/owner-handoffs/batches/${encodeURIComponent(batch.ID)}`);
        renderBatch(root, batch);
        const rowsByCustomer = new Map(displayedRows.filter(row => row.CustomerID).map(row => [row.CustomerID as number, row]));
        downloadWorkbook("owner_migration_result.xlsx", ["行号", "external_userid", "客户备注名", "当前负责人userid", "备注", "迁移状态", "企微转接状态"], (batch.Lines || []).map(line => {
          const row = rowsByCustomer.get(line.CustomerID);
          return [String(row?.Line || line.Line), row?.ExternalUserID || "", row?.CustomerDisplayName || "", row?.CurrentOwnerUserID || "", row?.Remark || "", line.State, transferStatusLabel(line.TransferStatus)];
        }));
        setNotice("已导出当前批次结果明细。", "ok");
      } catch (error) { setNotice(error instanceof Error ? error.message : "结果导出失败", "error"); }
    });
    query<HTMLButtonElement>(root, "[data-download-result]").textContent = "下载结果明细";
    const readTransfer = document.createElement("button");
    readTransfer.type = "button"; readTransfer.className = "owner-migration-btn"; readTransfer.dataset.readTransferResult = ""; readTransfer.textContent = "读取企微转接结果"; readTransfer.disabled = true;
    query<HTMLElement>(root, "[data-download-result]").parentElement?.append(readTransfer);
    readTransfer.addEventListener("click", async () => {
      if (!batch) return;
      stage.dataset.ownerHandoffTransferResultStatus = "pending";
      try {
        batch = await api<Batch>(`/api/admin/customers/owner-handoffs/batches/${encodeURIComponent(batch.ID)}/transfer-result`, { method: "POST", body: JSON.stringify({ idempotency_key: key() }) });
        stage.dataset.ownerHandoffTransferResultStatus = "ok";
        renderBatch(root, batch); setNotice("已读取企微转接结果。", "ok");
      } catch (error) {
        const status = error && typeof error === "object" && "httpStatus" in error && typeof error.httpStatus === "number" ? error.httpStatus : 0;
        stage.dataset.ownerHandoffTransferResultStatus = status > 0 ? `http_${status}` : "error";
        setNotice(error instanceof Error ? error.message : "读取失败", "error");
      }
    });
    query<HTMLInputElement>(root, 'input[name="scope_type"][value="all"]').checked = true;
    root.querySelector<HTMLElement>('[data-scope-segment="all"]')?.classList.add("is-active");
    root.querySelector<HTMLElement>('[data-scope-segment="excel_include"]')?.classList.remove("is-active");
    query<HTMLElement>(root, "[data-excel-panel]").hidden = true;
    query<HTMLElement>(root, "[data-mode-pill]").textContent = "模式：全量迁移";
    updateWeComPresentation(); reset();
    setNotice("原负责人可含停用员工，目标负责人只列在职员工。");
    stage.dataset.ownerHandoffInit = "ready";
  } catch (error) {
    const phase = stage.dataset.ownerHandoffInit || "mounting";
    const failure = error as RequestFailure;
    stage.dataset.ownerHandoffInit = phase === "mounting" ? "donor_error" : phase === "donor_loaded" ? "context_error" : "host_error";
    if (failure.httpStatus) stage.dataset.ownerHandoffInitStatus = String(failure.httpStatus);
    else delete stage.dataset.ownerHandoffInitStatus;
    stage.textContent = "负责人迁移页面不可用。";
  }
}

if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", () => { void boot(); }); else void boot();
