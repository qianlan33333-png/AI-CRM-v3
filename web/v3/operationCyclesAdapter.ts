import { request } from '../src/api/transport';
import { toast, initFeedback } from '../src/shared/ui/feedback';
import { mount, PageBase, type Vals } from '../src/shared/ui/runtime';

type JsonObject = Record<string, unknown>;

const record = (value: unknown): JsonObject => value !== null && typeof value === 'object' && !Array.isArray(value) ? value as JsonObject : {};
const list = (value: unknown): unknown[] => Array.isArray(value) ? value : [];
const text = (value: unknown, fallback = '—'): string => typeof value === 'string' && value.trim() ? value : fallback;
const numberText = (value: unknown, fallback = '0'): string => typeof value === 'number' || typeof value === 'string' ? String(value) : fallback;
const badge = (tone: string): string => {
  const colors: Record<string, string> = { ok: '#2EA121', warn: '#D97917', blue: '#3370ff', gray: '#8F959E' };
  const color = colors[tone] || colors.gray;
  return `display:inline-flex;align-items:center;height:22px;padding:0 8px;border-radius:4px;background:${color}14;color:${color};font-size:12px`;
};

async function json(path: string, init?: RequestInit): Promise<JsonObject> {
  const response = await request(path, init);
  return record(await response.json());
}

function stageTemplate(): { host: HTMLElement; template: string } {
  const host = document.getElementById('stage');
  const template = document.getElementById('tpl') as HTMLTemplateElement | null;
  if (!host || !template) throw new Error('页面模块加载失败');
  return { host, template: template.innerHTML };
}

function snapshotOf(value: JsonObject): JsonObject { return record(value.snapshot); }

class CycleListController extends PageBase {
  rows: JsonObject[] = [];

  async load(): Promise<void> {
    const response = await json('/api/admin/operation-cycles/strategies?limit=100&offset=0');
    const strategies = list(response.items).map(record);
    this.rows = await Promise.all(strategies.map(async (strategy) => {
      const key = text(strategy.strategy_key, '');
      const snapshot = snapshotOf(strategy);
      const definition = record(strategy.definition);
      let run: JsonObject = {};
      if (key) {
        const runs = await json(`/api/admin/operation-cycles/strategies/${encodeURIComponent(key)}/runs?limit=1&offset=0`);
        run = record(list(runs.items)[0]);
      }
      const runKey = text(run.run_key, text(snapshot.run_key, ''));
      const steps = list(snapshot.steps ?? definition.steps).map((raw) => {
        const step = record(raw);
        const dim = step.dim === true;
        return { label: text(step.label), color: text(step.color, dim ? '#C4C7CC' : '#2EA121'), dim, tc: dim ? '#A6AAB0' : '#1F2329' };
      });
      const status = text(strategy.status, 'active');
      const viewDetail = (): void => { if (runKey) location.href = `/admin/operation-cycles?view=detail&id=${encodeURIComponent(runKey)}`; };
      const act = async (): Promise<void> => {
        if (!key || !runKey) return;
        try {
          await json(`/api/admin/operation-cycles/strategies/${encodeURIComponent(key)}/actions/review/start`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'Idempotency-Key': `cycle-ui-${key}-${runKey}` },
            body: JSON.stringify({ run_key: runKey, parent_request_id: '' }),
          });
          await this.load();
        } catch (error) {
          toast(error instanceof Error ? error.message : '请求失败', true);
        }
      };
      return {
        id: key, name: text(strategy.title, key), cron: text(snapshot.cron ?? definition.cron),
        dot: status === 'active' ? '#2EA121' : status === 'paused' ? '#D97917' : '#8F959E',
        steps, action: text(snapshot.action, status === 'active' ? '开始复盘' : '查看进度'), runId: runKey,
        viewDetail, act,
      };
    }));
    this.setState({ loaded: true });
  }

  renderVals(): Vals { return { cycles: { rows: this.rows, total: this.rows.length } }; }
}

function normalizeRun(value: JsonObject): JsonObject {
  const snapshot = snapshotOf(value);
  const runKey = text(value.run_key, text(snapshot.runKey));
  const strategy = text(snapshot.strategy, text(value.strategy_key));
  const attempts = list(snapshot.attempts).map((raw) => {
    const item = record(raw); const tone = text(item.tone, 'gray');
    return { ...item, label: text(item.label), statusLabel: text(item.statusLabel), summary: text(item.summary), startedAt: text(item.startedAt), finishedAt: text(item.finishedAt), cs: badge(tone), stages: list(item.stages).map((stageRaw) => { const stage = record(stageRaw); return { ...stage, label: text(stage.label), dot: text(stage.status) === 'ok' ? '#2EA121' : text(stage.status) === 'warn' ? '#D97917' : '#8F959E' }; }) };
  });
  const funnelSource = list(snapshot.funnel);
  const funnel = funnelSource.map((raw, index) => { const item = record(raw); const ratio = Math.max(28, 100 - index * (funnelSource.length > 1 ? 12 : 0)); return { ...item, label: text(item.label), value: numberText(item.value), w: `${ratio}%` }; });
  const windows = list(snapshot.windows).map((raw) => { const item = record(raw); const metrics = list(item.metrics); return { ...item, label: text(item.label), statusLabel: text(item.statusLabel), cs: badge(text(item.tone, 'gray')), metrics, hasMetrics: metrics.length > 0, start: text(item.start), end: text(item.end), quality: text(item.quality), limitation: text(item.limitation) }; });
  const delivery = record(snapshot.delivery);
  const retro = record(snapshot.retro);
  const next = record(snapshot.next);
  return {
    ...snapshot,
    id: runKey,
    label: text(snapshot.label, `${strategy} · ${runKey}`),
    objective: text(snapshot.objective), strategy, runKey,
    snapshotRev: numberText(snapshot.snapshotRev ?? value.snapshot_revision),
    audience: text(snapshot.audience), intendedSendAt: text(snapshot.intendedSendAt), planScheduledFor: text(snapshot.planScheduledFor),
    firstSentAt: text(snapshot.firstSentAt), lastSentAt: text(snapshot.lastSentAt), attempts, funnel,
    audienceNote: text(snapshot.audienceNote), reviewStatus: text(snapshot.reviewStatus), reviewCs: badge(text(snapshot.reviewTone, 'gray')),
    planVersion: text(snapshot.planVersion), planStatus: text(snapshot.planStatus), planSource: text(snapshot.planSource), targetCount: numberText(snapshot.targetCount),
    delivery: { sent: numberText(delivery.sent), failed: numberText(delivery.failed), retryable: numberText(delivery.retryable), rate: numberText(delivery.rate), statusLabel: text(delivery.statusLabel), source: text(delivery.source), failureSummary: text(delivery.failureSummary, '') },
    windows,
    retro: { summary: text(retro.summary), detail: text(retro.detail), findings: list(retro.findings), limitations: list(retro.limitations) },
    next: { statusLabel: text(next.statusLabel), cs: badge(text(next.tone, 'gray')), summary: text(next.summary), rationale: text(next.rationale), confirmedAt: text(next.confirmedAt), appliedVersion: text(next.appliedVersion), note: text(next.note), changes: list(next.changes) },
    references: list(snapshot.references),
  };
}

class CycleDetailController extends PageBase {
  run: JsonObject = normalizeRun({});
  constructor(private readonly runKey: string) { super(); }
  async load(): Promise<void> {
    const value = await json(`/api/admin/operation-cycles/runs/${encodeURIComponent(this.runKey)}`);
    this.run = normalizeRun(value);
    this.setState({ loaded: true });
  }
  renderVals(): Vals { return { run: this.run, go: { cycles: (): void => { location.href = '/admin/operation-cycles'; } } }; }
}

async function main(): Promise<void> {
  initFeedback();
  const { host, template } = stageTemplate();
  const query = new URLSearchParams(location.search);
  if (query.get('view') === 'detail' && query.get('id')) {
    const controller = new CycleDetailController(query.get('id')!);
    mount(host, template, controller);
    await controller.load();
    return;
  }
  const controller = new CycleListController();
  mount(host, template, controller);
  await controller.load();
}

void main().catch((error: unknown) => {
  const host = document.getElementById('stage');
  if (host) host.innerHTML = `<div role="alert" style="margin:32px;padding:24px;border:1px solid #F2B8B5;border-radius:8px;color:#D83931;background:#FFF1F0">${error instanceof Error ? error.message : '页面模块加载失败'}</div>`;
});
