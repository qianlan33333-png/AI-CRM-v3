export {};

/*
 * Public Survey presentation seam.
 *
 * The H5 Controller remains the authority for the Survey Owner's question
 * validation, submission key, retry and result-token flows.  This Host is
 * loaded before that frozen runtime and only adds stable presentation and
 * accessibility hooks to its release template and rendered screen.
 */

const supportedPages = new Set(["auth", "all", "one", "result"]);
const page = document.body.dataset.page || "";

if (supportedPages.has(page)) {
  document.body.dataset.v3PublicSurvey = page;

  const template = document.getElementById("tpl") as HTMLTemplateElement | null;
  if (!template) throw new Error("公开问卷页面缺少冻结运行模板");

  const templateDescendants = <T extends Element>(
    root: ParentNode,
    selector: string,
  ): T[] => {
    const found = new Set<T>(Array.from(root.querySelectorAll<T>(selector)));
    for (const nested of Array.from(
      root.querySelectorAll<HTMLTemplateElement>("template"),
    )) {
      for (const element of templateDescendants<T>(nested.content, selector))
        found.add(element);
    }
    return [...found];
  };

  const markTemplate = (): void => {
    const content = template.content;
    for (const card of templateDescendants<HTMLElement>(
      content,
      "[data-question-id]",
    )) {
      card.dataset.v3SurveyQuestion = "";
    }
    for (const option of templateDescendants<HTMLElement>(
      content,
      "label[data-option-id]",
    )) {
      option.dataset.v3SurveyOption = "";
    }
    for (const control of templateDescendants<HTMLElement>(
      content,
      "[data-h5-submit], [data-h5-next], [data-h5-previous]",
    )) {
      control.dataset.v3SurveyAction = "";
    }
    // The frozen controller already exposes its stable `submitting` render
    // value. Reuse all.html's existing status node, then add the same
    // structural marker to one.html before it mounts. This avoids inspecting
    // user-facing copy and avoids duplicating all.html's screen-reader update.
    let hasSubmittingStatus = false;
    for (const condition of templateDescendants<HTMLTemplateElement>(
      content,
      "template",
    )) {
      if (condition.dataset.scIf !== "{{ submitting }}") continue;
      const feedback =
        condition.content.querySelector<HTMLElement>('[role="status"]');
      if (!feedback) continue;
      feedback.dataset.v3SurveySubmitting = "";
      feedback.setAttribute("aria-live", "polite");
      hasSubmittingStatus = true;
    }
    if (!hasSubmittingStatus) {
      const templateForContent = (
        fragment: Node,
      ): HTMLTemplateElement | null => {
        const visit = (root: ParentNode): HTMLTemplateElement | null => {
          for (const nested of Array.from(
            root.querySelectorAll<HTMLTemplateElement>("template"),
          )) {
            if (nested.content === fragment) return nested;
            const owner = visit(nested.content);
            if (owner) return owner;
          }
          return null;
        };
        return visit(content);
      };
      const control = templateDescendants<HTMLElement>(
        content,
        "[data-h5-submit]",
      )[0];
      const controlTemplate =
        control && templateForContent(control.parentNode || content);
      // `canSubmit` is nested in one.html's action footer. Its feedback must
      // be attached to that footer rather than the conditional template,
      // which disappears at the exact time we need to report submission.
      const target: ParentNode | null = controlTemplate?.dataset.scIf?.includes(
        "canSubmit",
      )
        ? controlTemplate.parentElement
        : control?.parentNode || null;
      if (target) {
        const condition = document.createElement("template");
        condition.dataset.scIf = "{{ submitting }}";
        const feedback = document.createElement("div");
        feedback.dataset.v3SurveySubmitting = "";
        feedback.setAttribute("role", "status");
        feedback.setAttribute("aria-live", "polite");
        feedback.textContent = "正在提交，请勿重复操作…";
        condition.content.append(feedback);
        target.append(condition);
      }
    }
    for (const error of templateDescendants<HTMLElement>(
      content,
      "[data-h5-error]",
    )) {
      error.setAttribute("aria-live", "assertive");
      error.dataset.v3SurveyError = "";
    }
    for (const receipt of templateDescendants<HTMLElement>(
      content,
      "[data-h5-receipt], [data-h5-result]",
    )) {
      receipt.dataset.v3SurveyReceipt = "";
    }
    // result.html's receipt contains a fixed diagnostic-only processing row.
    // This is a template-carrier marker, never a deduction about the current
    // submission state: the Owner still validates `local_only` and
    // `external_executed` before it renders the result at all.
    if (page === "result") {
      for (const label of templateDescendants<HTMLElement>(content, "span")) {
        if (label.textContent?.trim() !== "处理范围") continue;
        label.dataset.v3SurveyInternalReceiptDetail = "";
        const value = label.nextElementSibling;
        if (value instanceof HTMLElement)
          value.dataset.v3SurveyInternalReceiptDetail = "";
      }
    }
  };

  const rendered = (): HTMLElement | null => document.getElementById("screen");

  const improveTransportError = (error: HTMLElement): void => {
    if (error.querySelector("[data-v3-survey-recovery]")) return;
    const rawNodes = Array.from(error.childNodes).filter(
      (node): node is Text =>
        node.nodeType === Node.TEXT_NODE && Boolean(node.textContent?.trim()),
    );
    const raw = rawNodes.map((node) => node.textContent?.trim() || "").join(" ");
    const status = /(?:^|[（(\s])HTTP ([45][0-9]{2})(?:$|[）)\s])/.exec(raw)?.[1];
    // The frozen controller surfaces transport failures as an exact HTTP
    // status. Translate that known transport fact only; validation and Owner
    // domain errors retain their existing wording and behavior.
    if (!status) return;
    for (const node of rawNodes) node.remove();
    const recovery = document.createElement("p");
    recovery.dataset.v3SurveyRecovery = "";
    recovery.textContent =
      page === "result"
        ? "暂时无法查询提交结果，请稍后重试。"
        : "暂时无法完成本次提交。已填写的答案仍会保留，请稍后重试。";
    const detail = document.createElement("small");
    detail.dataset.v3SurveyErrorDetail = "";
    detail.textContent = `问题详情：HTTP ${status}`;
    error.prepend(recovery);
    error.append(detail);
  };

  const decorate = (): void => {
    const screen = rendered();
    if (!screen) return;
    screen.dataset.v3PublicSurveyScreen = page;
    const isSubmitting = !!screen.querySelector("[data-v3-survey-submitting]");
    if (isSubmitting) screen.dataset.v3SurveySubmitting = "true";
    else delete screen.dataset.v3SurveySubmitting;
    for (const button of Array.from(
      screen.querySelectorAll<HTMLButtonElement>("[data-h5-submit]"),
    )) {
      // Keep any Owner-controlled disabled state intact.  The all-in-one
      // template retains its submit control while `submitting` is true, so
      // only that explicit, stable state adds a busy lock here.
      if (isSubmitting) {
        button.disabled = true;
        button.setAttribute("aria-disabled", "true");
        button.setAttribute("aria-busy", "true");
      }
      button.dataset.v3SurveyAction = "";
    }
    for (const card of Array.from(
      screen.querySelectorAll<HTMLElement>("[data-question-id]"),
    ))
      card.dataset.v3SurveyQuestion = "";
    for (const option of Array.from(
      screen.querySelectorAll<HTMLElement>("label[data-option-id]"),
    ))
      option.dataset.v3SurveyOption = "";
    for (const error of Array.from(
      screen.querySelectorAll<HTMLElement>("[data-h5-error]"),
    )) {
      error.setAttribute("aria-live", "assertive");
      error.dataset.v3SurveyError = "";
      improveTransportError(error);
    }
    for (const receipt of Array.from(
      screen.querySelectorAll<HTMLElement>(
        "[data-h5-receipt], [data-h5-result]",
      ),
    ))
      receipt.dataset.v3SurveyReceipt = "";
    for (const detail of Array.from(
      screen.querySelectorAll<HTMLElement>(
        "[data-v3-survey-internal-receipt-detail]",
      ),
    ))
      detail.remove();
  };

  markTemplate();
  const screen = rendered();
  if (screen) {
    const observer = new MutationObserver(decorate);
    observer.observe(screen, {
      childList: true,
      subtree: true,
      characterData: true,
    });
    screen.addEventListener(
      "click",
      (event) => {
        const target =
          event.target instanceof Element
            ? event.target.closest<HTMLButtonElement>("[data-h5-submit]")
            : null;
        if (!target || target.disabled) return;
        // Do not prevent the frozen controller's handler.  The immediate visual
        // lock closes the second-click window while its existing submitting
        // guard and stable submission key retain their authoritative behavior.
        target.disabled = true;
        target.setAttribute("aria-disabled", "true");
        target.setAttribute("aria-busy", "true");
        screen.dataset.v3SurveySubmitting = "true";
      },
      true,
    );
    decorate();
  }
}
