// V3-owned presentation bridge for the frozen Survey list.
//
// A questionnaire archive is a versioned owner command. The row used to open
// the confirmation supplies the immutable target and version; a retry keeps
// the exact body and idempotency key rather than asking the server to delete
// whatever version happens to be current later.

import { request } from '../src/api/transport';
import { confirmBox, toast } from '../src/shared/ui/feedback';

type QuestionnaireListRow = {
  resourceId?: number;
  id?: number;
  name: string;
  internalName?: string;
  title?: string;
  version?: number;
	  action?: string;
  del?: () => void;
  delStyle?: string;
};
type SurveyListController = {
  page: string;
  db: { rows: { questionnaires: QuestionnaireListRow[] } };
  init(): Promise<void>;
  renderVals(): Record<string, unknown>;
};
type ArchiveIntent = { expectedVersion: number; key: string; body: string };

const archiveIntents = new Map<number, ArchiveIntent>();
const archiving = new Set<number>();

function idOf(row: QuestionnaireListRow): number {
  const id = Number(row.resourceId ?? row.id);
  return Number.isSafeInteger(id) && id > 0 ? id : 0;
}

function versionOf(row: QuestionnaireListRow): number {
  const version = Number(row.version);
  return Number.isSafeInteger(version) && version > 0 ? version : 0;
}

function newKey(): string {
  return `survey-archive-${globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(36).slice(2)}`}`;
}

function lockArchivedQuestionnaireDetail(): void {
  queueMicrotask(() => {
    if (document.body.dataset.page !== 'questionnaireDetail') return;
    for (const control of document.querySelectorAll<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>('#questionnaireName,#questionnaireTitle,#questionnaireDescription,#questionnaireDisplay,#questionnaireSlug,#questionnaireDisabled,#questionnaireAssessmentEnabled,#questionnaireQuestions,#questionnaireAssessmentConfig')) {
      control.disabled = true;
      control.setAttribute('aria-disabled', 'true');
    }
    for (const button of document.querySelectorAll<HTMLButtonElement>('button')) {
      if (!/保存定义|保存并发布/.test(button.textContent || '')) continue;
      button.disabled = true;
      button.setAttribute('aria-disabled', 'true');
      button.title = '问卷已归档，定义仅供历史查看。';
    }
  });
}

async function archiveQuestionnaire(controller: SurveyListController, id: number, expectedVersion: number): Promise<void> {
  if (archiving.has(id)) return;
  const prior = archiveIntents.get(id);
  const intent = prior && prior.expectedVersion === expectedVersion
    ? prior
    : { expectedVersion, key: newKey(), body: JSON.stringify({ expected_version: expectedVersion }) };
  archiveIntents.set(id, intent);
  archiving.add(id);
  try {
    await request(`/api/admin/questionnaires/${id}`, {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json', 'Idempotency-Key': intent.key },
      body: intent.body,
    });
    await controller.init();
    if (controller.db.rows.questionnaires.some((row) => idOf(row) === id)) {
      toast('归档已受理，但列表回读仍显示该问卷；请刷新后核对。', true);
      return;
    }
    archiveIntents.delete(id);
    toast('问卷已归档，已停止新的公开提交。');
  } catch (error) {
    // Keep the exact immutable command for retry. A different list version
    // intentionally receives a new confirmation and a new idempotency scope.
    toast(error instanceof Error ? error.message : '问卷归档失败，请重试。', true);
  } finally {
    archiving.delete(id);
  }
}

void (async () => {
  // The controller must be loaded from the same split module graph as main.
  // A separately bundled import would patch a different prototype instance.
  // @ts-ignore Frozen donor view materialized by prepare-donor-source-views.
  const { AdminController } = await import('../src/admin/controller');
  const controller = AdminController.prototype as unknown as SurveyListController;
  const donorRenderVals = controller.renderVals;
  controller.renderVals = function renderSurveyListWithArchiveAction() {
    if (this.page !== 'questionnaires') return donorRenderVals.call(this);
    const donorRows = this.db.rows.questionnaires;
    // The frozen renderer owns pagination/search. Replace fields only during
    // that render pass, then restore the response DTO for all other flows.
    this.db.rows.questionnaires = donorRows.map((row) => ({
      ...row,
      name: typeof row.internalName === 'string' && row.internalName.trim() ? row.internalName : row.name,
    }));
    try {
      // The donor derives its own row actions in a later map. Patch the
      // rendered values (rather than the input DTO) so its old delete action
      // cannot overwrite this archived-version action before the template
      // binds the real DOM click handler.
      const values = donorRenderVals.call(this) as { rows?: { questionnaires?: QuestionnaireListRow[] } };
      const renderedRows = values.rows?.questionnaires;
      const questionnaireForm = (values as { questionnaireFormPage?: { item?: QuestionnaireListRow; save?: () => void; publish?: () => void; title?: string } }).questionnaireFormPage;
      if (questionnaireForm?.item?.action === 'archived') {
        questionnaireForm.title = '已归档问卷';
        questionnaireForm.save = () => toast('问卷已归档，定义仅供历史查看。', true);
        questionnaireForm.publish = questionnaireForm.save;
        lockArchivedQuestionnaireDetail();
      }
      if (!renderedRows) return values;
      return {
        ...values,
        rows: {
          ...values.rows,
          questionnaires: renderedRows.map((row) => {
            const id = idOf(row);
            const expectedVersion = versionOf(row);
            const displayName = typeof row.internalName === 'string' && row.internalName.trim() ? row.internalName : row.name;
            if (!id || !expectedVersion) {
              return {
                ...row,
                delStyle: { fontSize: '13px', cursor: 'not-allowed', color: '#BBBFC4' },
                del: () => toast('问卷版本信息暂不可用，请刷新列表后再归档。', true),
              };
            }
            return {
              ...row,
              delStyle: { fontSize: '13px', cursor: 'pointer', color: '#D83931' },
              del: () => {
                const title = typeof row.title === 'string' && row.title.trim() ? row.title.trim() : displayName;
                confirmBox(
                  '归档问卷',
                  `确认归档“${title}”吗？将停止新的公开提交，并从正常列表移除；已提交答卷、结果快照和审计记录会保留。`,
                  '确认归档',
                  true,
                  () => { void archiveQuestionnaire(this, id, expectedVersion); },
                );
              },
            };
          }),
        },
      };
    } finally {
      this.db.rows.questionnaires = donorRows;
    }
  };
  // @ts-ignore The donor entry is side-effect-only and must start after the bridge.
  await import('../src/admin/main');
})();
