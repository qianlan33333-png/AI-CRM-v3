// V3-owned presentation bridge for the frozen Survey list. The underlying
// API already carries both fields: `internalName` is the questionnaire name,
// while `name` is the questionnaire title in the donor DTO.
//
type QuestionnaireListRow = { name: string; internalName?: string };
type SurveyListController = { page: string; db: { rows: { questionnaires: QuestionnaireListRow[] } } };

void (async () => {
  // The controller must be loaded from the same split module graph as main.
  // A separately bundled import would patch a different prototype instance.
  // @ts-ignore Frozen donor view materialized by prepare-donor-source-views.
  const { AdminController } = await import('../src/admin/controller');
  const controller = AdminController.prototype as unknown as {
    renderVals(this: SurveyListController): Record<string, unknown>;
  };
  const donorRenderVals = controller.renderVals;
  controller.renderVals = function renderSurveyListWithQuestionnaireName() {
    if (this.page !== 'questionnaires') return donorRenderVals.call(this);
    const donorRows = this.db.rows.questionnaires;
    // Filtering occurs in the frozen renderer, so the replacement is scoped to
    // that call. It gives the existing list and search the questionnaire name,
    // then restores the unmodified server DTO.
    this.db.rows.questionnaires = donorRows.map((row) => ({
      ...row,
      name: typeof row.internalName === 'string' && row.internalName.trim() ? row.internalName : row.name,
    }));
    try {
      return donorRenderVals.call(this);
    } finally {
      this.db.rows.questionnaires = donorRows;
    }
  };
  // @ts-ignore The donor entry is side-effect-only and must start after the bridge.
  await import('../src/admin/main');
})();
