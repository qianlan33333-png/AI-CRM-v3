// Source-owned admin date/time conversion. API and DB values keep their
// RFC3339/UTC contracts; this file is only for business-facing presentation
// and for converting explicit Shanghai user input back to those contracts.

export type NaiveDateTimeSource = 'shanghai_wall_clock';

const zonedInstant = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(?::\d{2}(?:\.\d{1,9})?)?(?:Z|[+-]\d{2}:?\d{2})$/;
const calendarDate = /^(\d{4})-(\d{2})-(\d{2})$/;
const naiveDateTime = /^(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2})(?::(\d{2}))?$/;
const datetimeLocal = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2}))?$/;

function isCalendarDate(year: number, month: number, day: number): boolean {
  const value = new Date(Date.UTC(year, month - 1, day));
  return value.getUTCFullYear() === year && value.getUTCMonth() === month - 1 && value.getUTCDate() === day;
}

function shanghaiParts(instant: Date): string {
  const parts = new Intl.DateTimeFormat('zh-CN', {
    timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23',
  }).formatToParts(instant);
  const part = (kind: Intl.DateTimeFormatPartTypes): string => parts.find((item) => item.type === kind)?.value || '';
  return `${part('year')}-${part('month')}-${part('day')} ${part('hour')}:${part('minute')}:${part('second')}`;
}

// Zoned timestamps are real instants and always render in Asia/Shanghai.
// Date-only fields retain their calendar day. Naive strings are displayable
// only when their API/source contract explicitly identifies them as Shanghai
// wall-clock values; unknown naive strings are not guessed as UTC or browser
// local time.
export function formatShanghaiDateTime(raw: unknown, source?: NaiveDateTimeSource): string {
  const value = typeof raw === 'string' ? raw.trim() : '';
  if (!value) return '未提供';
  if (calendarDate.test(value)) return value;
  const local = value.match(naiveDateTime);
  if (local) return source === 'shanghai_wall_clock' ? `${local[1]}-${local[2]}-${local[3]} ${local[4]}:${local[5]}:${local[6] || '00'}` : '未提供';
  if (!zonedInstant.test(value)) return '未提供';
  const instant = new Date(value);
  return Number.isNaN(instant.getTime()) ? '未提供' : shanghaiParts(instant);
}

// Converts a datetime-local value entered by an admin as Shanghai local time
// into the existing RFC3339/UTC wire format. It does not inspect browser TZ.
export function shanghaiDateTimeLocalToRFC3339(value: string): string | undefined {
  const match = value.trim().match(datetimeLocal);
  if (!match) return undefined;
  const [year, month, day, hour, minute, second] = match.slice(1).map((part) => Number(part || '0'));
  if (!isCalendarDate(year, month, day) || hour > 23 || minute > 59 || second > 59) return undefined;
  const instant = new Date(`${match[1]}-${match[2]}-${match[3]}T${match[4]}:${match[5]}:${match[6] || '00'}+08:00`);
  return Number.isNaN(instant.getTime()) ? undefined : instant.toISOString();
}

// A pure-date filter means a Shanghai calendar day, not a UTC day. The end is
// inclusive to the final whole second used by existing second-precision admin
// filters.
export function shanghaiCalendarDateRange(value: string): { from: string; to: string } | undefined {
  const match = value.trim().match(calendarDate);
  if (!match || !isCalendarDate(Number(match[1]), Number(match[2]), Number(match[3]))) return undefined;
  const from = shanghaiDateTimeLocalToRFC3339(`${value}T00:00:00`);
  const to = shanghaiDateTimeLocalToRFC3339(`${value}T23:59:59`);
  return from && to ? { from, to } : undefined;
}
