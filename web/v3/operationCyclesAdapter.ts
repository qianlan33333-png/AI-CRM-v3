// This is a host binding, not a second implementation of the donor screen.
// The frozen donor templates own every visible string, layout and interaction.
import { request } from '../src/api/transport';
import { toast, initFeedback } from '../src/shared/ui/feedback';
import { mount, PageBase, type Vals } from '../src/shared/ui/runtime';

type RecordValue = Record<string, unknown>;

const object = (value: unknown): RecordValue => value !== null && typeof value === 'object' && !Array.isArray(value) ? value as RecordValue : {};
const array = (value: unknown): unknown[] => Array.isArray(value) ? value : [];
const string = (value: unknown): string => typeof value === 'string' ? value : '';

async function get(path: string): Promise<RecordValue> {
  return object(await (await request(path)).json());
}

function stage(): { host: HTMLElement; template: string } {
  const host = document.getElementById('stage');
  const template = document.getElementById('tpl') as HTMLTemplateElement | null;
  if (!host || !template) throw new Error('页面模块加载失败');
  return { host, template: template.innerHTML };
}

// These are the exact projection steps performed by the frozen AdminController
// before it binds `cycleTasks` and `cycleRuns` to these two templates. The
// adapter only substitutes records returned by the v3 local read API.
const chip = (tone: string): RecordValue => {
  const tones: Record<string, [string, string]> = {
    ok: ['#EBF9EC', '#2EA121'], blue: ['#EFF4FF', '#245BDB'], warn: ['#FFF7E8', '#D97917'],
    red: ['#FDECEE', '#D83931'], gray: ['#F2F3F5', '#646A73'], purple: ['#F4EDFF', '#7F3BF5'],
  };
  const color = tones[tone] || tones.gray;
  return { display: 'inline-flex', alignItems: 'center', height: '22px', padding: '0 8px', borderRadius: '4px', background: color[0], color: color[1], fontSize: '12px', whiteSpace: 'nowrap' };
};

function listRow(strategy: RecordValue): RecordValue {
  const snapshot = object(strategy.snapshot);
  const runKey = string(snapshot.run_key);
  return {
    ...snapshot,
    id: string(strategy.strategy_key),
    name: string(snapshot.name) || string(strategy.title),
    cron: string(snapshot.cron),
    dot: string(snapshot.dot),
    action: string(snapshot.action),
    steps: array(snapshot.steps).map((value) => {
      const step = object(value);
      return { ...step, tc: step.dim === true ? '#A6AAB0' : '#1F2329' };
    }),
    // The donor controller intentionally blocks this button: its execution
    // runtime DTO is not equivalent to the local report/action contracts.
    // Do not mint an idempotency key or start an action from this read screen.
    act: (): void => toast('当前复盘会话壳与 execution-runtime DTO 不等价', true),
    viewDetail: (): void => { if (runKey) location.href = `/admin/operation-cycles?view=detail&id=${encodeURIComponent(runKey)}`; },
  };
}

function dossier(run: RecordValue): RecordValue {
  const snapshot = object(run.snapshot);
  return {
    ...snapshot,
    id: string(run.run_key),
    runKey: string(run.run_key),
    snapshotRev: String(run.snapshot_revision ?? ''),
    reviewCs: chip(string(snapshot.reviewTone)),
    next: { ...object(snapshot.next), cs: chip(string(object(snapshot.next).tone)) },
    windows: array(snapshot.windows).map((value) => {
      const window = object(value);
      return { ...window, cs: chip(string(window.tone)), hasMetrics: array(window.metrics).length > 0 };
    }),
    attempts: array(snapshot.attempts).map((value) => {
      const attempt = object(value);
      return { ...attempt, cs: chip(string(attempt.tone)), stages: array(attempt.stages).map((stageValue) => {
        const entry = object(stageValue);
        return { ...entry, dot: string(entry.status) === 'ok' ? '#2EA121' : '#D97917' };
      }) };
    }),
    funnel: array(snapshot.funnel).map((value, index) => ({ ...object(value), w: `${Math.max(18, 100 - index * 18)}%` })),
  };
}

class ListPage extends PageBase {
  rows: RecordValue[] = [];
  async load(): Promise<void> {
    const result = await get('/api/admin/operation-cycles/strategies?limit=100&offset=0');
    this.rows = array(result.items).map((value) => listRow(object(value)));
    this.setState({ loaded: true });
  }
  renderVals(): Vals { return { cycles: { rows: this.rows, total: this.rows.length } }; }
}

class DetailPage extends PageBase {
  run: RecordValue = {};
  constructor(private readonly key: string) { super(); }
  async load(): Promise<void> {
    this.run = dossier(await get(`/api/admin/operation-cycles/runs/${encodeURIComponent(this.key)}`));
    this.setState({ loaded: true });
  }
  renderVals(): Vals { return { run: this.run, go: { cycles: (): void => { location.href = '/admin/operation-cycles'; } } }; }
}

async function main(): Promise<void> {
  initFeedback();
  const { host, template } = stage();
  const query = new URLSearchParams(location.search);
  const page = query.get('view') === 'detail' && query.get('id') ? new DetailPage(query.get('id')!) : new ListPage();
  mount(host, template, page);
  await page.load();
}

void main().catch((error: unknown) => toast(error instanceof Error ? error.message : '页面模块加载失败', true));
