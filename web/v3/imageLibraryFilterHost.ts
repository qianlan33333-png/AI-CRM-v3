// The image library is a V3-owned workspace. It reads the existing Media
// contract directly and leaves all mutations on the existing typed DTO and
// MaterialSaveHost path; no donor controller or generated template is mounted.
import { imagePageDto, saveImageItemDto, deleteImageItemDto } from "../src/api/admin";
import { getLegacyImageList } from "../src/api/generated/p4-media-compat/p4-media-compat";
import { apiRequestOptions, unwrapGenerated } from "../src/api/transport";
import type { ImageItem } from "../src/shared/api/types";

const PAGE_SIZE = 20;
const SEARCH_DELAY_MS = 250;

type ImageListResponse = {
  items?: unknown[];
  total?: unknown;
  limit?: unknown;
  offset?: unknown;
  has_more?: unknown;
};

type Dialog =
  | { kind: "upload"; error: string }
  | { kind: "edit"; item: ImageItem; error: string }
  | undefined;

function errorText(error: unknown): string {
  return error instanceof Error && error.message ? error.message : "图片素材读取失败";
}

function button(label: string, kind: "primary" | "secondary" | "danger" = "secondary"): HTMLButtonElement {
  const node = document.createElement("button");
  node.type = "button";
  node.className = `admin-button admin-button--${kind === "danger" ? "secondary" : kind}`;
  node.textContent = label;
  if (kind === "danger") {
    node.style.borderColor = "#FBC4C2";
    node.style.color = "#D83931";
    node.style.background = "#FFF5F5";
  }
  return node;
}

function field(label: string, input: HTMLInputElement): HTMLLabelElement {
  const wrap = document.createElement("label");
  wrap.style.cssText = "display:grid;gap:6px;font-size:12px;color:#646A73;font-weight:500";
  const title = document.createElement("span");
  title.textContent = label;
  input.style.cssText = "height:34px;width:100%;border:1px solid #DEE0E3;border-radius:6px;padding:0 10px;font-size:13px;box-sizing:border-box;background:#fff;color:#1F2329";
  wrap.append(title, input);
  return wrap;
}

class ImageLibraryHost {
  private readonly stage: HTMLElement;
  private readonly scroll: HTMLElement;
  private readonly workspace: HTMLElement;
  private items: ImageItem[] = [];
  private query = "";
  private includeInactive = false;
  private offset = 0;
  private total = 0;
  private loading = false;
  private error = "";
  private hasSuccessfulRead = false;
  private dialog: Dialog;
  private readGeneration = 0;
  private readAbort?: AbortController;
  private searchTimer?: number;

  constructor(stage: HTMLElement) {
    this.stage = stage;
    this.scroll = document.createElement("div");
    // MaterialSaveHost discovers this stable scroll region and mounts its
    // refresh/credential panel ahead of the workspace. Host re-renders never
    // replace this region, so that existing panel keeps its own lifecycle.
    this.scroll.dataset.imageLibraryScrollRegion = "true";
    this.scroll.style.cssText = "flex:1 1 0;min-height:0;overflow:auto;padding:16px 20px;display:grid;grid-template-columns:minmax(0,1fr);gap:12px;align-content:start";
    this.workspace = document.createElement("section");
    this.workspace.dataset.imageLibraryWorkspace = "true";
    this.workspace.style.cssText = "display:grid;grid-template-columns:minmax(0,1fr);gap:12px;align-content:start";
    this.scroll.append(this.workspace);
    this.stage.replaceChildren(this.scroll);
  }

  start(): void {
    this.render();
    void this.load(0);
  }

  private scheduleSearch(value: string): void {
    this.query = value;
    this.offset = 0;
    if (this.searchTimer !== undefined) window.clearTimeout(this.searchTimer);
    this.searchTimer = window.setTimeout(() => {
      this.searchTimer = undefined;
      void this.load(0);
    }, SEARCH_DELAY_MS);
  }

  private async load(offset = this.offset): Promise<void> {
    if (this.searchTimer !== undefined) {
      window.clearTimeout(this.searchTimer);
      this.searchTimer = undefined;
    }
    this.readAbort?.abort();
    const abort = new AbortController();
    this.readAbort = abort;
    const generation = ++this.readGeneration;
    this.offset = offset;
    this.loading = true;
    this.error = "";
    this.render();
    try {
      const payload = unwrapGenerated(await getLegacyImageList({
        limit: String(PAGE_SIZE),
        offset: String(offset),
        enabled_only: this.includeInactive ? "false" : "true",
        ...(this.query.trim() ? { q: this.query.trim() } : {}),
      }, apiRequestOptions({ signal: abort.signal }))) as ImageListResponse;
      if (generation !== this.readGeneration) return;
      const rawItems = Array.isArray(payload.items) ? payload.items : [];
      const total = Number(payload.total);
      const responseLimit = Number(payload.limit);
      const responseOffset = Number(payload.offset);
      if (!Number.isSafeInteger(total) || total < 0 || responseLimit !== PAGE_SIZE || responseOffset !== offset || rawItems.length > PAGE_SIZE) {
        throw new Error("图片素材分页响应无效");
      }
      this.items = rawItems.map(imagePageDto);
      this.total = total;
      this.offset = offset;
      this.hasSuccessfulRead = true;
      this.error = "";
    } catch (error) {
      if (generation !== this.readGeneration || (error instanceof DOMException && error.name === "AbortError")) return;
      this.error = errorText(error);
    } finally {
      if (generation !== this.readGeneration) return;
      this.loading = false;
      this.render();
    }
  }

  private render(): void {
    const preservedScrollTop = this.scroll.scrollTop;
    this.workspace.replaceChildren();
    this.workspace.append(this.header(), this.toolbar(), this.stateLine(), this.cards(), this.pagination());
    if (this.dialog) this.workspace.append(this.modal());
    this.scroll.scrollTop = preservedScrollTop;
  }

  private header(): HTMLElement {
    const header = document.createElement("header");
    header.style.cssText = "display:flex;align-items:center;justify-content:space-between;gap:16px;min-height:52px;padding:0 20px;background:#fff;border:1px solid #DEE0E3;border-radius:8px";
    const titles = document.createElement("div");
    titles.style.minWidth = "0";
    const crumb = document.createElement("div");
    crumb.textContent = "客户管理后台 / 素材";
    crumb.style.cssText = "font-size:12px;color:#8F959E;line-height:14px";
    const title = document.createElement("h1");
    title.textContent = "图片素材库";
    title.style.cssText = "margin:3px 0 0;font-size:16px;font-weight:600;line-height:22px;color:#1F2329";
    titles.append(crumb, title);
    const upload = button("上传图片", "primary");
    upload.addEventListener("click", () => { this.dialog = { kind: "upload", error: "" }; this.render(); });
    header.append(titles, upload);
    return header;
  }

  private toolbar(): HTMLElement {
    const toolbar = document.createElement("section");
    toolbar.className = "admin-toolbar";
    toolbar.style.cssText = "background:#fff;border:1px solid #DEE0E3;border-radius:8px;padding:12px 16px;display:flex;align-items:center;gap:12px;flex-wrap:wrap";
    const input = document.createElement("input");
    input.type = "search";
    input.value = this.query;
    input.placeholder = "搜索素材名或标签";
    input.dataset.imageLibraryQuery = "true";
    input.setAttribute("aria-label", "搜索图片素材");
    input.style.cssText = "height:32px;flex:1 1 240px;border:1px solid #DEE0E3;border-radius:6px;padding:0 10px;font-size:13px;background:#fff";
    input.addEventListener("input", () => this.scheduleSearch(input.value));
    const includeLabel = document.createElement("label");
    includeLabel.style.cssText = "display:flex;align-items:center;gap:6px;font-size:13px;color:#646A73;margin-left:auto;cursor:pointer";
    const include = document.createElement("input");
    include.type = "checkbox";
    include.checked = this.includeInactive;
    include.dataset.imageLibraryIncludeInactive = "true";
    include.setAttribute("aria-label", "包含已停用图片");
    include.addEventListener("change", () => { this.includeInactive = include.checked; this.offset = 0; void this.load(0); });
    includeLabel.append(include, document.createTextNode("含已停用"));
    const reset = button("重置");
    reset.dataset.imageLibraryReset = "true";
    reset.addEventListener("click", () => {
      this.query = "";
      this.includeInactive = false;
      this.offset = 0;
      void this.load(0);
    });
    toolbar.append(input, includeLabel, reset);
    return toolbar;
  }

  private stateLine(): HTMLElement {
    const line = document.createElement("p");
    line.dataset.imageLibraryFilterFeedback = "true";
    line.style.cssText = "margin:0;color:#646A73;font-size:13px;line-height:20px;min-height:20px";
    if (this.error) {
      line.setAttribute("role", "alert");
      line.style.color = "#D83931";
      line.textContent = this.hasSuccessfulRead
        ? `图片素材读取失败，仍显示上一次成功结果：${this.error}`
        : `图片素材读取失败：${this.error}`;
    } else if (this.loading) {
      line.setAttribute("role", "status");
      line.textContent = "正在读取图片素材…";
    } else if (this.hasSuccessfulRead) {
      line.setAttribute("role", "status");
      line.textContent = `显示 ${this.items.length ? this.offset + 1 : 0}-${this.offset + this.items.length} / ${this.total}`;
    }
    return line;
  }

  private cards(): HTMLElement {
    const grid = document.createElement("section");
    grid.dataset.imageLibraryCards = "true";
    grid.style.cssText = "display:grid;grid-template-columns:repeat(auto-fill,minmax(200px,1fr));gap:12px";
    if (!this.loading && !this.error && this.hasSuccessfulRead && this.items.length === 0) {
      const empty = document.createElement("p");
      empty.dataset.imageLibraryEmpty = "true";
      empty.textContent = "没有符合当前筛选条件的图片素材。";
      empty.style.cssText = "grid-column:1/-1;margin:0;padding:24px;border:1px dashed #DEE0E3;border-radius:8px;background:#fff;color:#646A73;text-align:center";
      grid.append(empty);
      return grid;
    }
    for (const item of this.items) grid.append(this.card(item));
    return grid;
  }

  private card(item: ImageItem): HTMLElement {
    const card = document.createElement("article");
    card.style.cssText = `background:#fff;border:1px solid #DEE0E3;border-radius:8px;overflow:hidden;${item.enabled ? "" : "opacity:.55"}`;
    const preview = document.createElement("img");
    preview.src = item.thumbnailUrl || "";
    preview.alt = item.name;
    preview.style.cssText = "display:block;width:100%;height:128px;object-fit:cover;background:#EFF4FF;border-bottom:1px solid #EFF0F1;cursor:pointer";
    preview.addEventListener("click", () => { this.dialog = { kind: "edit", item, error: "" }; this.render(); });
    const body = document.createElement("div");
    body.style.cssText = "padding:10px 12px";
    const name = document.createElement("strong");
    name.textContent = item.name;
    name.style.cssText = "display:block;font-size:13px;font-weight:500;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;cursor:pointer;color:#1F2329";
    name.addEventListener("click", () => { this.dialog = { kind: "edit", item, error: "" }; this.render(); });
    const detail = document.createElement("div");
    detail.style.cssText = "display:flex;align-items:center;justify-content:space-between;gap:8px;margin-top:6px";
    const size = document.createElement("span");
    size.textContent = item.size;
    size.style.cssText = "font-size:12px;color:#A6AAB0;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;white-space:nowrap;overflow:hidden;text-overflow:ellipsis";
    const tag = document.createElement("span");
    tag.textContent = item.tag || "未分类";
    tag.style.cssText = "display:inline-flex;align-items:center;height:20px;padding:0 7px;border-radius:4px;background:#F2F3F5;color:#646A73;font-size:11px;white-space:nowrap";
    detail.append(size, tag);
    const actions = document.createElement("div");
    actions.style.cssText = "display:flex;align-items:center;justify-content:space-between;gap:8px;margin-top:8px;padding-top:8px;border-top:1px solid #F5F6F7";
    const time = document.createElement("span");
    time.textContent = item.uploadedAt;
    time.style.cssText = "font-size:11px;color:#A6AAB0";
    const edit = button("编辑");
    edit.style.cssText = "height:24px;padding:0 8px;border:0;border-radius:4px;background:transparent;color:#245BDB;font-size:12px;cursor:pointer";
    edit.addEventListener("click", () => { this.dialog = { kind: "edit", item, error: "" }; this.render(); });
    actions.append(time, edit);
    body.append(name, detail, actions);
    card.append(preview, body);
    return card;
  }

  private pagination(): HTMLElement {
    const nav = document.createElement("nav");
    nav.dataset.imageLibraryPagination = "true";
    nav.setAttribute("aria-label", "图片素材分页");
    nav.style.cssText = "display:flex;align-items:center;justify-content:flex-end;gap:8px;flex-wrap:wrap";
    const previous = button("上一页");
    previous.disabled = this.loading || this.offset === 0;
    previous.addEventListener("click", () => void this.load(Math.max(0, this.offset - PAGE_SIZE)));
    const next = button("下一页");
    next.disabled = this.loading || this.offset + this.items.length >= this.total;
    next.addEventListener("click", () => void this.load(this.offset + PAGE_SIZE));
    nav.append(previous, next);
    return nav;
  }

  private modal(): HTMLElement {
    const dialog = this.dialog;
    if (!dialog) return document.createElement("div");
    const overlay = document.createElement("section");
    overlay.setAttribute("role", "dialog");
    overlay.setAttribute("aria-modal", "true");
    overlay.style.cssText = "position:fixed;inset:0;background:rgba(15,23,42,.34);z-index:80;display:flex;align-items:center;justify-content:center;padding:24px";
    const panel = document.createElement("form");
    panel.dataset.imageLibraryDialog = "true";
    panel.style.cssText = "width:min(520px,100%);background:#fff;border-radius:12px;box-shadow:0 24px 64px rgba(15,23,42,.22);overflow:hidden";
    panel.addEventListener("submit", (event) => {
      event.preventDefault();
      panel.querySelector<HTMLButtonElement>("[data-image-library-dialog-submit]")?.click();
    });
    const heading = document.createElement("header");
    heading.style.cssText = "display:flex;align-items:center;justify-content:space-between;padding:14px 18px;border-bottom:1px solid #EFF0F1";
    const title = document.createElement("strong");
    title.textContent = dialog.kind === "upload" ? "上传图片" : "编辑图片素材";
    const close = button("×");
    close.setAttribute("aria-label", "关闭弹窗");
    close.style.cssText = "width:28px;height:28px;padding:0;border:0;border-radius:6px;background:#F2F3F5;color:#646A73;font-size:14px";
    close.addEventListener("click", () => { this.dialog = undefined; this.render(); });
    heading.append(title, close);
    const fields = document.createElement("div");
    fields.style.cssText = "padding:18px;display:grid;gap:14px";
    if (dialog.kind === "upload") {
      const file = document.createElement("input");
      file.id = "fImgUpFile";
      file.type = "file";
      file.accept = "image/png,image/jpeg,image/gif";
      file.required = true;
      fields.append(field("图片文件", file));
      const name = document.createElement("input");
      name.id = "fImgUpName";
      name.placeholder = "留空则使用文件名";
      fields.append(field("素材名称", name));
      const tags = document.createElement("input");
      tags.id = "fImgUpTags";
      tags.placeholder = "如：直播,预告";
      fields.append(field("标签（逗号分隔）", tags));
    } else {
      const name = document.createElement("input");
      name.id = "fImgName";
      name.required = true;
      name.value = dialog.item.name;
      fields.append(field("素材名称", name));
      const description = document.createElement("input");
      description.id = "fImgDesc";
      description.value = dialog.item.desc;
      description.placeholder = "用途说明，便于同事选择";
      fields.append(field("描述", description));
      const tags = document.createElement("input");
      tags.id = "fImgTags";
      tags.value = dialog.item.tags;
      fields.append(field("标签（逗号分隔）", tags));
      const enabled = document.createElement("input");
      enabled.id = "fImgEnabled";
      enabled.type = "checkbox";
      enabled.checked = dialog.item.enabled;
      const enabledLabel = document.createElement("label");
      enabledLabel.style.cssText = "display:flex;align-items:center;gap:8px;font-size:13px;color:#344054";
      enabledLabel.append(enabled, document.createTextNode("启用此图片素材"));
      fields.append(enabledLabel);
    }
    if (dialog.error) {
      const error = document.createElement("p");
      error.dataset.imageLibraryMutationFeedback = "true";
      error.setAttribute("role", "alert");
      error.textContent = dialog.error;
      error.style.cssText = "margin:0;color:#D83931;font-size:13px;line-height:20px";
      fields.append(error);
    }
    const footer = document.createElement("footer");
    footer.style.cssText = "display:flex;align-items:center;justify-content:space-between;gap:8px;padding:12px 18px;border-top:1px solid #EFF0F1;background:#FAFBFC";
    const left = document.createElement("div");
    if (dialog.kind === "edit") {
      const remove = button("删除", "danger");
      remove.addEventListener("click", () => void this.remove(dialog.item));
      left.append(remove);
    }
    const right = document.createElement("div");
    right.style.cssText = "display:flex;gap:8px";
    const cancel = button("取消");
    cancel.addEventListener("click", () => { this.dialog = undefined; this.render(); });
    const submit = button(dialog.kind === "upload" ? "上传" : "保存", "primary");
    submit.dataset.imageLibraryDialogSubmit = "true";
    // MaterialSaveHost deliberately marks this button busy in capture phase.
    // Bind the write on click (like the frozen donor) so that busy marking does
    // not suppress native form submission before our handler can run.
    submit.addEventListener("click", () => {
      if (dialog.kind === "upload") void this.submitUpload(panel);
      else void this.submitEdit(panel, dialog.item);
    });
    right.append(cancel, submit);
    footer.append(left, right);
    panel.append(heading, fields, footer);
    overlay.append(panel);
    return overlay;
  }

  private formValue(form: HTMLFormElement, id: string): string {
    return (form.querySelector<HTMLInputElement>(`#${id}`)?.value || "").trim();
  }

  private async submitUpload(form: HTMLFormElement): Promise<void> {
    const file = form.querySelector<HTMLInputElement>("#fImgUpFile")?.files?.[0];
    if (!file) return this.setDialogError("请选择真实图片文件后再上传");
    const name = this.formValue(form, "fImgUpName") || file.name;
    try {
      await saveImageItemDto(null, {
        name,
        file,
        tags: this.formValue(form, "fImgUpTags"),
        desc: "",
        size: String(file.size),
        tag: this.formValue(form, "fImgUpTags").split(/[,，]/)[0] || "未标记",
        tone: "gray",
        bg: "#EFF4FF",
        enabled: true,
        uploadedAt: "刚刚",
      });
      this.dialog = undefined;
      await this.load(this.offset);
    } catch (error) {
      this.setDialogError(errorText(error));
    }
  }

  private async submitEdit(form: HTMLFormElement, item: ImageItem): Promise<void> {
    const name = this.formValue(form, "fImgName");
    if (!name) return this.setDialogError("请输入素材名称");
    try {
      await saveImageItemDto(item.name, {
        ...item,
        resourceId: item.resourceId,
        name,
        desc: this.formValue(form, "fImgDesc"),
        tags: this.formValue(form, "fImgTags"),
        enabled: Boolean(form.querySelector<HTMLInputElement>("#fImgEnabled")?.checked),
      });
      this.dialog = undefined;
      await this.load(this.offset);
    } catch (error) {
      this.setDialogError(errorText(error));
    }
  }

  private async remove(item: ImageItem): Promise<void> {
    if (!window.confirm(`确认删除「${item.name}」？删除后不可恢复。`)) return;
    try {
      // The typed helper is the same existing compatibility path as the donor
      // UI. The Media handler mints a per-request key for legacy delete calls.
      await deleteImageItemDto(item);
      this.dialog = undefined;
      const nextOffset = this.items.length === 1 && this.offset > 0 ? Math.max(0, this.offset - PAGE_SIZE) : this.offset;
      await this.load(nextOffset);
    } catch (error) {
      this.setDialogError(errorText(error));
    }
  }

  private setDialogError(value: string): void {
    if (!this.dialog) return;
    const panel = document.querySelector<HTMLFormElement>("form[data-image-library-dialog]");
    if (panel) {
      let feedback = panel.querySelector<HTMLElement>("[data-image-library-mutation-feedback]");
      if (!feedback) {
        feedback = document.createElement("p");
        feedback.dataset.imageLibraryMutationFeedback = "true";
        feedback.setAttribute("role", "alert");
        feedback.style.cssText = "margin:0;color:#D83931;font-size:13px;line-height:20px";
        panel.querySelector("footer")?.before(feedback);
      }
      feedback.textContent = value;
      // Keep typed field values and the browser-owned file selection intact so
      // the user can correct the request and retry from the same dialog.
      return;
    }
    this.dialog = this.dialog.kind === "upload"
      ? { kind: "upload", error: value }
      : { kind: "edit", item: this.dialog.item, error: value };
    this.render();
  }
}

function boot(): void {
  if (document.body?.dataset.page !== "images") return;
  const stage = document.querySelector<HTMLElement>("main#stage[data-image-library-v3-root]");
  if (!stage || stage.dataset.imageLibraryHostMounted === "true") return;
  stage.dataset.imageLibraryHostMounted = "true";
  new ImageLibraryHost(stage).start();
}

if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", boot, { once: true });
else boot();
