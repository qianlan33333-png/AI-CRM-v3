import {
  readGroupOpsHistoryPlans, readGroupOpsHistoryDirectory, readGroupOpsHistoryGroups, readGroupOpsHistoryNodes,
  requireGroupOpsHistoryPlanID,
} from '../../api/groupOpsHistory';
import type {
  GroupOpsHistoryPlan,
  GroupOpsHistoryDirectory,
  GroupOpsHistoryGroup,
  GroupOpsHistoryNode,
} from "../../api/generated/health.schemas";
import { ApiError } from '../../api/transport';
import { esc } from './util';

const cell = (value: unknown): string => esc(value === null ? 'NULL（源未记录／关联缺失）' : value === '' ? '""（源空字符串）' : value);
const field = (label: string, value: unknown): string => `<div><span style="color:#667085">${esc(label)}：</span><span style="white-space:pre-wrap">${cell(value)}</span></div>`;
const record = (body: string): string => `<article style="border-top:1px solid #EEF0F3;padding:12px 0;display:grid;gap:5px;overflow-wrap:anywhere">${body}</article>`;

type HistoricalNodeContent = { action_title?: unknown; text_content?: unknown; attachments?: unknown };
type ReadonlyContentRenderer = { renderFull(detail: { content_text: string; content_basis_label: string; attachment_basis_label: string; attachments: Array<{ type_label: string; name: string; description: string }> }): string };

function readonlyContentRenderer(): ReadonlyContentRenderer | undefined {
  return (window as Window & { AICRMSendContentReadonlyDetail?: ReadonlyContentRenderer }).AICRMSendContentReadonlyDetail;
}

function attachmentSummary(value: unknown): Array<{ type_label: string; name: string; description: string }> {
  if (!Array.isArray(value)) return [];
  return value.map((item, index) => {
    const row = item && typeof item === 'object' && !Array.isArray(item) ? item as Record<string, unknown> : {};
    const kind = typeof row.kind === 'string' && row.kind !== '' ? row.kind : '附件';
    const id = row.id === undefined || row.id === null || row.id === '' ? `#${index + 1}` : `#${String(row.id)}`;
    return { type_label: `历史素材 · ${kind}`, name: `${kind} ${id}`, description: '原始附件引用（仅展示）' };
  });
}

function readonlyNodeContent(row: GroupOpsHistoryNode): string {
  const source = row as GroupOpsHistoryNode & HistoricalNodeContent;
  const title = typeof source.action_title === 'string' ? source.action_title : '';
  const text = typeof source.text_content === 'string' ? source.text_content : '';
  const attachments = attachmentSummary(source.attachments);
  const detail = { content_text: text, content_basis_label: '历史正文（仅展示，不执行）', attachment_basis_label: '历史附件引用（仅展示，不读取素材）', attachments };
  const rendered = readonlyContentRenderer()?.renderFull(detail);
  const fallback = `<section aria-label="历史内容" style="display:grid;gap:8px"><div>${field('历史正文', text)}</div><div>${field('历史附件', attachments.length ? attachments.map((item) => item.name).join('、') : '无')}</div></section>`;
  return `${field('原操作标题', title)}${rendered || fallback}`;
}

function plans(rows: GroupOpsHistoryPlan[]): string {
  return rows.map((r) => record(`<a href="groupopsDetail.html?history=1&id=${encodeURIComponent(r.plan_id)}" style="color:#245BDB">${cell(r.name)} · 查看历史计划群与节点</a>${field('V2 plan_id / 源计划 ID', `${r.plan_id} / ${r.source_plan_id}`)}${field('源 code', r.source_code)}${field('源计划类型', r.plan_type)}${field('源状态', r.original_status)}${field('V2 只读状态 / revision', `${r.status} / ${r.revision}`)}${field('负责人 staff_id', r.owner_staff_id)}${field('创建时间', r.created_at)}${field('更新时间', r.updated_at)}${field('归档时间', r.archived_at)}`)).join('');
}

function directory(rows: GroupOpsHistoryDirectory[]): string {
  return (['group_chats', 'wecom_group_chat_snapshots'] as const).map((source) => `<h3 style="font-size:14px;margin:14px 0 4px">${source}（本页）</h3>${rows.filter((r) => r.source_kind === source).map((r) => record(`${field('V2 ID', r.id)}${field('源 ID', r.source_id)}${field('源群引用', r.chat_reference)}${field('源群名称', r.display_name)}${field('负责人 staff_id', r.owner_staff_id)}${field('源负责人名称', r.owner_name)}${field('源成员数', r.member_count)}${field('源内部成员数', r.internal_member_count)}${field('源外部成员数', r.external_member_count)}${field('源状态', r.original_status)}${field('记录时间', r.recorded_at)}`)).join('') || '<p>本页无该来源记录</p>'}`).join('');
}

function groups(rows: GroupOpsHistoryGroup[]): string {
  return rows.map((r) => record(`${field('V2 ID / 源计划群 ID', `${r.id} / ${r.source_group_id}`)}${field('V2 plan_id / 源计划 ID', `${r.plan_id} / ${r.source_plan_id}`)}${field('源群引用', r.chat_reference)}${field('源群名称', r.display_name)}${field('负责人 staff_id', r.owner_staff_id)}${field('源内部成员数', r.internal_member_count)}${field('源外部成员数', r.external_member_count)}${field('源状态', r.original_status)}${field('创建时间', r.created_at)}${field('移除时间', r.removed_at)}`)).join('');
}

function nodes(rows: GroupOpsHistoryNode[]): string {
  return rows.map((r) => record(`${field('V2 ID / 源节点 ID', `${r.id} / ${r.source_node_id}`)}${field('V2 plan_id / 源计划 ID', `${r.plan_id} / ${r.source_plan_id}`)}${field('源 day_index', r.day_index)}${field('源触发标签', r.trigger_time)}${field('源排序', r.sort_order)}${field('源状态', r.original_status)}${field('创建时间', r.created_at)}${field('更新时间', r.updated_at)}${readonlyNodeContent(r)}`)).join('');
}

async function section<T>(host: HTMLElement, title: string, read: (limit: number, offset: number) => Promise<{ items: T[]; total: number; limit: number; offset: number }>, render: (rows: T[]) => string): Promise<void> {
  const limit = 20;
  async function load(offset: number): Promise<void> {
    host.innerHTML = `<h2 style="font-size:16px">${esc(title)}</h2><p role="status">正在读取历史记录…</p>`;
    try {
      const page = await read(limit, offset);
      host.innerHTML = `<h2 style="font-size:16px">${esc(title)}</h2><div data-history-rows>${page.items.length ? render(page.items) : '<p>暂无历史记录</p>'}</div><div style="display:flex;gap:10px;align-items:center"><button data-prev ${offset === 0 ? 'disabled' : ''}>上一页</button><span>共 ${page.total} 条 · offset=${page.offset} · 每页 ${page.limit} 条</span><button data-next ${offset + page.items.length >= page.total ? 'disabled' : ''}>下一页</button><button data-refresh>刷新本页</button></div>`;
      host.querySelector('[data-prev]')?.addEventListener('click', () => void load(Math.max(0, offset - limit)));
      host.querySelector('[data-next]')?.addEventListener('click', () => void load(offset + limit));
      host.querySelector('[data-refresh]')?.addEventListener('click', () => void load(offset));
    } catch (error) {
      const message = error instanceof ApiError ? error.message : '历史数据读取失败';
      host.innerHTML = `<h2 style="font-size:16px">${esc(title)}</h2><p role="alert">${esc(message)}；未使用演示数据。</p><button data-retry>重试本页（offset=${offset}）</button>`;
      host.querySelector('[data-retry]')?.addEventListener('click', () => void load(offset));
    }
  }
  await load(0);
}

export async function mountGroupOpsHistory(stage: HTMLElement, options: { view: 'list' | 'detail'; planID?: string }): Promise<void> {
  stage.innerHTML = `<div style="overflow:auto;padding:20px;display:grid;gap:16px"><header><h1 style="font-size:20px">群运营 · V1 历史（只读）</h1><a href="groupops.html">返回当前计划管理</a>${options.view === 'detail' ? ' · <a href="groupops.html?history=1">返回历史列表</a>' : ''}<p style="padding:12px;background:#FFF9F0;border:1px solid #F5D6A7;border-radius:8px">仅浏览迁移的历史事实；源状态、时间与 NULL 保留原义。两源目录不合并为当前群目录，不同步、不激活、不发送，不调用 Provider。历史节点不转为当前执行节点。</p></header><section id="group-history-primary" style="background:white;border:1px solid #DEE0E3;border-radius:8px;padding:16px"></section><section id="group-history-secondary" style="background:white;border:1px solid #DEE0E3;border-radius:8px;padding:16px"></section></div>`;
  const first = stage.querySelector<HTMLElement>('#group-history-primary')!;
  const second = stage.querySelector<HTMLElement>('#group-history-secondary')!;
  if (options.view === 'detail') {
    let planID: string;
    try { planID = requireGroupOpsHistoryPlanID(options.planID || ''); }
    catch { first.innerHTML = '<p role="alert">历史计划 ID 无效；未读取任何计划。</p>'; second.remove(); return; }
    await Promise.all([
      section(first, `计划群 · plan_id=${planID}`, (limit, offset) => readGroupOpsHistoryGroups(planID, limit, offset), groups),
      section(second, `历史节点 · plan_id=${planID}`, (limit, offset) => readGroupOpsHistoryNodes(planID, limit, offset), nodes),
    ]);
  } else {
    await Promise.all([
      section(first, '历史计划', readGroupOpsHistoryPlans, plans),
      section(second, '历史目录（两来源共用本节分页，分区只展示本页记录）', readGroupOpsHistoryDirectory, directory),
    ]);
  }
}
