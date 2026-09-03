// This is the only v3-owned browser seam for operation cycles. It supplies a
// narrow, read-only AdminApi.loadDb implementation and then starts the frozen
// donor main -> legacy -> AdminController chain. It must not render, style,
// navigate, toast, or bind an interaction itself.
import { emptyAdminDb, type AdminReadContext } from '../src/api/admin';
import { api } from '../src/shared/api/client';
import type { AdminDb, CycleRun, Tone } from '../src/shared/api/types';

type RecordValue = Record<string, unknown>;
type Strategy = { strategyKey: string; title: string; snapshot: RecordValue };

const object = (value: unknown): RecordValue => value !== null && typeof value === 'object' && !Array.isArray(value) ? value as RecordValue : {};
const array = (value: unknown): unknown[] => Array.isArray(value) ? value : [];
const string = (value: unknown): string => typeof value === 'string' ? value : '';
const requiredString = (value: unknown, field: string): string => {
  const result = string(value);
  if (!result) throw new Error(`运营周期读取响应缺少 ${field}`);
  return result;
};
const tone = (value: unknown): Tone => {
  const result = string(value);
  return result === 'ok' || result === 'blue' || result === 'warn' || result === 'red' || result === 'gray' || result === 'purple' ? result : 'gray';
};

async function get(path: string): Promise<RecordValue> {
  const response = await fetch(path, { credentials: 'same-origin', headers: { Accept: 'application/json' } });
  if (!response.ok) throw new Error(`运营周期读取失败（HTTP ${response.status}）`);
  return object(await response.json());
}

async function listStrategies(): Promise<Strategy[]> {
  const result = await get('/api/admin/operation-cycles/strategies?limit=100&offset=0');
  return array(result.items).map((value, index) => {
    const item = object(value);
    const snapshot = object(item.snapshot);
    if (!snapshot.schema_version || snapshot.schema_version !== 'operation_cycle_snapshot.v1') throw new Error(`运营周期读取响应第 ${index + 1} 项不是已投影快照`);
    return { strategyKey: requiredString(item.strategy_key, 'strategy_key'), title: requiredString(item.title, 'title'), snapshot };
  });
}

function stringList(value: unknown): string[] {
  return array(value).map((item) => requiredString(item, 'display text'));
}

function displayRun(id: number, value: RecordValue): CycleRun {
  const dossier = object(value.dossier);
  if (Object.keys(dossier).length === 0) throw new Error('运营周期运行档案尚未提供安全投影');
  const delivery = object(dossier.delivery);
  const retro = object(dossier.retro);
  const next = object(dossier.next);
  return {
    id,
    label: requiredString(dossier.label, 'dossier.label'),
    objective: requiredString(dossier.objective, 'dossier.objective'),
    strategy: requiredString(dossier.strategy, 'dossier.strategy'),
    runKey: requiredString(value.run_key, 'run_key'),
    snapshotRev: String(value.revision ?? ''),
    audience: requiredString(dossier.audience, 'dossier.audience'),
    intendedSendAt: requiredString(dossier.intended_send_at, 'dossier.intended_send_at'),
    planScheduledFor: requiredString(dossier.plan_scheduled_for, 'dossier.plan_scheduled_for'),
    firstSentAt: requiredString(dossier.first_sent_at, 'dossier.first_sent_at'),
    lastSentAt: requiredString(dossier.last_sent_at, 'dossier.last_sent_at'),
    attempts: array(dossier.attempts).map((entry) => {
      const attempt = object(entry);
      return { label: requiredString(attempt.label, 'attempt.label'), statusLabel: requiredString(attempt.status_label, 'attempt.status_label'), tone: tone(attempt.tone), summary: requiredString(attempt.summary, 'attempt.summary'), startedAt: requiredString(attempt.started_at, 'attempt.started_at'), finishedAt: requiredString(attempt.finished_at, 'attempt.finished_at'), stages: array(attempt.stages).map((stage) => { const item = object(stage); return { label: requiredString(item.label, 'stage.label'), status: requiredString(item.status, 'stage.status') }; }) };
    }),
    funnel: array(dossier.funnel).map((entry) => { const item = object(entry); return { label: requiredString(item.label, 'funnel.label'), value: requiredString(item.value, 'funnel.value') }; }),
    audienceNote: requiredString(dossier.audience_note, 'dossier.audience_note'),
    reviewStatus: requiredString(dossier.review_status, 'dossier.review_status'),
    reviewTone: tone(dossier.review_tone),
    planVersion: requiredString(dossier.plan_version, 'dossier.plan_version'),
    planStatus: requiredString(dossier.plan_status, 'dossier.plan_status'),
    planSource: requiredString(dossier.plan_source, 'dossier.plan_source'),
    targetCount: requiredString(dossier.target_count, 'dossier.target_count'),
    delivery: { sent: requiredString(delivery.sent, 'delivery.sent'), failed: requiredString(delivery.failed, 'delivery.failed'), retryable: requiredString(delivery.retryable, 'delivery.retryable'), rate: requiredString(delivery.rate, 'delivery.rate'), statusLabel: requiredString(delivery.status_label, 'delivery.status_label'), source: requiredString(delivery.source, 'delivery.source'), failureSummary: requiredString(delivery.failure_summary, 'delivery.failure_summary') },
    windows: array(dossier.windows).map((entry) => { const item = object(entry); return { label: requiredString(item.label, 'window.label'), statusLabel: requiredString(item.status_label, 'window.status_label'), tone: tone(item.tone), metrics: array(item.metrics).map((metric) => { const field = object(metric); return { label: requiredString(field.label, 'metric.label'), value: requiredString(field.value, 'metric.value'), desc: requiredString(field.desc, 'metric.desc') }; }), start: requiredString(item.start, 'window.start'), end: requiredString(item.end, 'window.end'), quality: requiredString(item.quality, 'window.quality'), limitation: requiredString(item.limitation, 'window.limitation') }; }),
    retro: { summary: requiredString(retro.summary, 'retro.summary'), detail: requiredString(retro.detail, 'retro.detail'), findings: stringList(retro.findings), limitations: stringList(retro.limitations) },
    next: { statusLabel: requiredString(next.status_label, 'next.status_label'), tone: tone(next.tone), summary: requiredString(next.summary, 'next.summary'), rationale: requiredString(next.rationale, 'next.rationale'), confirmedAt: requiredString(next.confirmed_at, 'next.confirmed_at'), appliedVersion: requiredString(next.applied_version, 'next.applied_version'), note: requiredString(next.note, 'next.note'), changes: stringList(next.changes) },
    references: array(dossier.references).map((entry) => { const item = object(entry); return { label: requiredString(item.label, 'reference.label'), desc: requiredString(item.desc, 'reference.desc') }; }),
  };
}

async function operationCyclesDb(context: AdminReadContext): Promise<AdminDb> {
  const strategies = await listStrategies();
  const db = emptyAdminDb();
  db.cycleTasks = strategies.map((strategy, index) => ({
    id: index + 1,
    name: string(strategy.snapshot.name) || strategy.title,
    cron: string(strategy.snapshot.cron),
    dot: string(strategy.snapshot.dot),
    action: string(strategy.snapshot.action),
    runId: index + 1,
    steps: array(strategy.snapshot.steps).map((entry) => { const step = object(entry); return { label: requiredString(step.label, 'step.label'), color: requiredString(step.color, 'step.color'), dim: step.dim === true }; }),
  }));
  if (context.page !== 'cyclesDetail') return db;
  const ordinal = Number(context.id || '');
  if (!Number.isSafeInteger(ordinal) || ordinal < 1 || ordinal > strategies.length) throw new Error('运行档案编号无效或已失效');
  const strategy = strategies[ordinal - 1];
  const runKey = requiredString(strategy.snapshot.run_key, 'run_key');
  const result = await get(`/api/admin/operation-cycles/runs/${encodeURIComponent(runKey)}`);
  const snapshot = object(result.snapshot);
  if (snapshot.schema_version !== 'operation_cycle_snapshot.v1' || requiredString(result.run_key, 'run_key') !== runKey) throw new Error('运行档案响应未通过安全投影校验');
  db.cycleRuns[ordinal] = displayRun(ordinal, snapshot);
  return db;
}

const donorLoadDb = api.loadDb.bind(api);
api.loadDb = (context?: AdminReadContext): Promise<AdminDb> => {
  if (context?.page === 'cycles' || context?.page === 'cyclesDetail') return operationCyclesDb(context);
  return donorLoadDb(context);
};

// Dynamic import is deliberate: the binding must be installed before the
// unmodified donor entry reads document.body.dataset.page.
// @ts-expect-error The byte-frozen donor entry is a side-effect-only script.
void import('../src/admin/main');
