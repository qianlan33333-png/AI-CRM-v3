import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { formatShanghaiDateTime, shanghaiCalendarDateRange, shanghaiDateTimeLocalToRFC3339 } from './adminDateTime.ts';

const sample = '2026-09-30T16:01:02Z';
assert.equal(formatShanghaiDateTime(sample), '2026-10-01 00:01:02');
assert.equal(formatShanghaiDateTime('2026-10-01T00:01:02+08:00'), '2026-10-01 00:01:02');
assert.equal(formatShanghaiDateTime('2026-10-01'), '2026-10-01');
assert.equal(formatShanghaiDateTime('2026-10-01 00:01:02'), '未提供', 'an unknown naive source must not be treated as browser local or UTC');
assert.equal(formatShanghaiDateTime('2026-10-01 00:01:02', 'shanghai_wall_clock'), '2026-10-01 00:01:02');
assert.equal(shanghaiDateTimeLocalToRFC3339('2026-10-01T00:00'), '2026-09-30T16:00:00.000Z');
assert.equal(shanghaiDateTimeLocalToRFC3339('2026-02-29T00:00'), undefined);
assert.deepEqual(shanghaiCalendarDateRange('2026-10-01'), {
  from: '2026-09-30T16:00:00.000Z', to: '2026-10-01T15:59:59.000Z',
});

const moduleURL = new URL('./adminDateTime.ts', import.meta.url).href;
const probe = `import assert from 'node:assert/strict'; import { formatShanghaiDateTime } from ${JSON.stringify(moduleURL)}; assert.equal(formatShanghaiDateTime(${JSON.stringify(sample)}), '2026-10-01 00:01:02');`;
for (const timezone of ['UTC', 'America/Los_Angeles']) {
  const result = spawnSync(process.execPath, ['--input-type=module', '--eval', probe], {
    env: { ...process.env, TZ: timezone }, encoding: 'utf8',
  });
  assert.equal(result.status, 0, `TZ=${timezone}: ${result.stderr}`);
}

console.log(`admin date/time timezone and Shanghai-boundary checks: PASS (${fileURLToPath(import.meta.url)})`);
