// V3 owns the image-library read controls. The donor template and its media
// writes stay frozen; this Host adds only the existing list API's query state.
export {};

type ImageReadState = {
  query: string;
  includeInactive: boolean;
  generation: number;
  acceptedDB?: unknown;
  guarded?: boolean;
};
type ImageController = {
  page: string;
  api: { mode: string; loadDb: (...args: unknown[]) => Promise<unknown> };
  db: unknown;
  __render?: () => void;
  init(): Promise<void>;
};

const imageStates = new WeakMap<ImageController, ImageReadState>();
const staleImageRead = Symbol("stale-image-library-read");
const donorFetch = globalThis.fetch.bind(globalThis);
let currentController: ImageController | undefined;
const initialReadState: ImageReadState = { query: "", includeInactive: false, generation: 0 };

function stateFor(controller: ImageController): ImageReadState {
  let state = imageStates.get(controller);
  if (!state) {
    state = { query: "", includeInactive: false, generation: 0 };
    imageStates.set(controller, state);
  }
  return state;
}

function imageReadURL(input: RequestInfo | URL, init?: RequestInit): URL | undefined {
  const request = input instanceof Request ? input : undefined;
  const raw = request ? request.url : String(input);
  const method = String(init?.method || request?.method || "GET").toUpperCase();
  const url = new URL(raw, location.href);
  if (method !== "GET" || url.origin !== location.origin || url.pathname !== "/api/admin/image-library") return;
  return url;
}

globalThis.fetch = (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
  const controller = currentController;
  const url = imageReadURL(input, init);
  if (document.body?.dataset.page !== "images" || !url) return donorFetch(input, init);
  // The donor begins its first read before it mounts and exposes its private
  // controller. Apply the same default query at that point; subsequent reads
  // use the controller-bound state below.
  const state = controller ? stateFor(controller) : initialReadState;
  url.searchParams.set("limit", "100");
  url.searchParams.set("offset", "0");
  url.searchParams.set("enabled_only", state.includeInactive ? "false" : "true");
  if (state.query) url.searchParams.set("q", state.query);
  else url.searchParams.delete("q");
  if (input instanceof Request) return donorFetch(new Request(url, input), init);
  return donorFetch(url, init);
};

function toolbar(): HTMLElement | undefined {
  const input = document.querySelector<HTMLInputElement>(
    '#stage input[placeholder="搜索素材名或标签"]',
  );
  return input?.parentElement || undefined;
}

function controls(): {
  input: HTMLInputElement;
  includeInactive: HTMLInputElement;
  reset: HTMLButtonElement;
} | undefined {
  const input = document.querySelector<HTMLInputElement>(
    '#stage input[placeholder="搜索素材名或标签"]',
  );
  const parent = input?.parentElement;
  const includeInactive = parent?.querySelector<HTMLInputElement>('input[type="checkbox"]');
  const reset = Array.from(parent?.querySelectorAll<HTMLButtonElement>("button") || [])
    .find((button) => button.textContent?.trim() === "重置");
  if (!input || !includeInactive || !reset) return;
  input.dataset.imageLibraryQuery = "true";
  input.setAttribute("aria-label", "搜索图片素材");
  includeInactive.dataset.imageLibraryIncludeInactive = "true";
  includeInactive.setAttribute("aria-label", "包含已停用图片");
  reset.dataset.imageLibraryReset = "true";
  return { input, includeInactive, reset };
}

function feedback(kind: "loading" | "error", value: string): void {
  const host = toolbar();
  if (!host) return;
  let node = host.parentElement?.querySelector<HTMLElement>("[data-image-library-filter-feedback]");
  if (!node) {
    node = document.createElement("p");
    node.dataset.imageLibraryFilterFeedback = "true";
    node.style.cssText = "margin:0;color:#646A73;font-size:13px;line-height:20px";
    host.after(node);
  }
  node.textContent = value;
  node.setAttribute("role", kind === "error" ? "alert" : "status");
  node.style.color = kind === "error" ? "#D83931" : "#646A73";
}

function syncControls(controller: ImageController): void {
  const current = controls();
  if (!current) return;
  const state = stateFor(controller);
  current.input.value = state.query;
  current.includeInactive.checked = state.includeInactive;
}

function guardRead(controller: ImageController, state: ImageReadState): void {
  if (state.guarded) return;
  state.guarded = true;
  const api = controller.api;
  const load = api.loadDb.bind(api);
  api.loadDb = async (...args: unknown[]) => {
    const generation = state.generation;
    const db = await load(...args);
    if (generation !== state.generation) throw staleImageRead;
    return db;
  };
}

function attachController(controller: ImageController): void {
  if (controller.page !== "images" || controller.api?.mode !== "http") return;
  currentController = controller;
  const state = stateFor(controller);
  state.acceptedDB = controller.db;
  guardRead(controller, state);
  const donorInit = controller.init.bind(controller);
  controller.init = async (): Promise<void> => {
    const generation = state.generation;
    try {
      await donorInit();
    } catch (error) {
      if (error !== staleImageRead) throw error;
      if (state.acceptedDB !== undefined) {
        controller.db = state.acceptedDB;
        controller.__render?.();
        syncControls(controller);
      }
      return;
    }
    if (generation !== state.generation) {
      if (state.acceptedDB !== undefined) {
        controller.db = state.acceptedDB;
        controller.__render?.();
        syncControls(controller);
      }
      return;
    }
    state.acceptedDB = controller.db;
    syncControls(controller);
  };
  // `mount()` renders just after it assigns `__render`; wait until that frame
  // exists before marking the frozen donor controls for delegated handling.
  queueMicrotask(() => syncControls(controller));
}

// `mount()` keeps the donor controller private. Its only cross-bundle seam is
// assigning `controller.__render`; observe that one assignment just long
// enough to attach the V3 read Host, then restore Object.prototype unchanged.
const previousRender = Object.getOwnPropertyDescriptor(Object.prototype, "__render");
Object.defineProperty(Object.prototype, "__render", {
  configurable: true,
  get: previousRender?.get,
  set(value: unknown) {
    const candidate = this as Partial<ImageController>;
    Object.defineProperty(candidate, "__render", {
      configurable: true,
      enumerable: true,
      writable: true,
      value,
    });
    if (candidate.page !== "images" || !candidate.api || typeof candidate.init !== "function") return;
    if (previousRender) Object.defineProperty(Object.prototype, "__render", previousRender);
    else delete (Object.prototype as { __render?: unknown }).__render;
    attachController(candidate as ImageController);
  },
});

function reload(query: string, includeInactive: boolean): void {
  const controller = currentController;
  if (!controller || controller.page !== "images") return;
  const state = stateFor(controller);
  state.query = query.trim();
  state.includeInactive = includeInactive;
  const generation = ++state.generation;
  feedback("loading", "正在筛选图片素材…");
  void controller.init().then(
    () => {
      if (state.generation === generation) syncControls(controller);
    },
    (error: unknown) => {
      if (state.generation !== generation) return;
      const detail = error instanceof Error && error.message ? `：${error.message}` : "";
      feedback("error", `图片素材读取失败${detail}`);
      syncControls(controller);
    },
  );
}

document.addEventListener("input", (event) => {
  const input = event.target;
  if (!(input instanceof HTMLInputElement) || input.dataset.imageLibraryQuery !== "true") return;
  const current = controls();
  if (!current) return;
  reload(input.value, current.includeInactive.checked);
});

document.addEventListener("change", (event) => {
  const input = event.target;
  if (!(input instanceof HTMLInputElement) || input.dataset.imageLibraryIncludeInactive !== "true") return;
  const current = controls();
  if (!current) return;
  reload(current.input.value, input.checked);
});

document.addEventListener("click", (event) => {
  const target = event.target;
  if (!(target instanceof Element)) return;
  const button = target.closest<HTMLButtonElement>("button[data-image-library-reset]");
  if (!button) return;
  event.preventDefault();
  const current = controls();
  if (!current) return;
  current.input.value = "";
  current.includeInactive.checked = false;
  reload("", false);
});
