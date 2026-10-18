import test from 'node:test';
import assert from 'node:assert/strict';
import { formatTokens, formatCost, formatBytes, formatVersion } from '../src/lib/format.ts';
import { isThemeMode, nextThemeMode, resolveTheme, themeButtonTitle } from '../src/lib/theme.ts';
import { formatRelativeTime, formatAbsoluteTime, isKnownTime, formatAgo, isTimeZoneMode } from '../src/lib/date.ts';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';

test('formatTokens formats numbers into compact string representations', () => {
  assert.equal(formatTokens(0), '0');
  assert.equal(formatTokens(950), '950');
  assert.equal(formatTokens(1000), '1k');
  assert.equal(formatTokens(1500), '1.5k');
  assert.equal(formatTokens(128400), '128.4k');
  assert.equal(formatTokens(1000000), '1M');
  assert.equal(formatTokens(2500000), '2.5M');
});

test('formatCost formats USD cost accurately', () => {
  assert.equal(formatCost(0), '$0.00');
  assert.equal(formatCost(0.423), '$0.42');
  assert.equal(formatCost(0.004), '$0.004');
  assert.equal(formatCost(12.5), '$12.50');
});

test('formatBytes formats byte sizes into readable units', () => {
  assert.equal(formatBytes(0), '0 B');
  assert.equal(formatBytes(512), '512 B');
  assert.equal(formatBytes(1024), '1.0 KB');
  assert.equal(formatBytes(1048576), '1.0 MB');
});

test('date formatting provides accurate relative and absolute strings', () => {
  const now = new Date();
  const fiveMinAgo = new Date(now.getTime() - 5 * 60 * 1000).toISOString();
  assert.equal(formatRelativeTime(fiveMinAgo), '5m');

  const twoHoursAgo = new Date(now.getTime() - 2 * 3600 * 1000).toISOString();
  assert.equal(formatRelativeTime(twoHoursAgo), '2h');

  const abs = formatAbsoluteTime('2026-09-28T14:30:00Z');
  assert.ok(abs.includes('2026'));
  assert.ok(abs.includes('UTC'));
});

test('formatAbsoluteTime shows UTC or this computer\'s local time with its offset', () => {
  const saved = process.env.TZ;
  try {
    process.env.TZ = 'Asia/Ho_Chi_Minh';
    assert.equal(formatAbsoluteTime('2026-09-28T20:30:05Z'), 'Mon 2026-09-28 20:30:05 UTC');
    assert.equal(formatAbsoluteTime('2026-09-28T20:30:05Z', 'utc'), 'Mon 2026-09-28 20:30:05 UTC');
    assert.equal(formatAbsoluteTime('2026-09-28T20:30:05Z', 'local'), 'Tue 2026-09-29 03:30:05 +07:00');
    process.env.TZ = 'Pacific/Marquesas';
    assert.equal(formatAbsoluteTime('2026-09-28T20:30:05Z', 'local'), 'Mon 2026-09-28 11:00:05 -09:30');
    assert.equal(formatAbsoluteTime('', 'local'), '');
  } finally {
    if (saved === undefined) delete process.env.TZ;
    else process.env.TZ = saved;
  }
  assert.ok(isTimeZoneMode('utc') && isTimeZoneMode('local'));
  assert.ok(!isTimeZoneMode('UTC') && !isTimeZoneMode(null));
});

test('isKnownTime rejects Go zero time and invalid strings', () => {
  assert.equal(isKnownTime('0001-01-01T00:00:00Z'), false);
  assert.equal(isKnownTime(''), false);
  assert.equal(isKnownTime('not a date'), false);
  assert.equal(isKnownTime('2026-09-28T10:00:00Z'), true);
});

test('formatAgo drops "ago" for just now', () => {
  assert.equal(formatAgo(new Date().toISOString()), 'just now');
  assert.equal(formatAgo(new Date(Date.now() - 5 * 60 * 1000).toISOString()), '5m ago');
  assert.equal(formatAgo(''), '');
});

test('formatVersion prefixes release versions with v', () => {
  assert.equal(formatVersion('0.3.2'), 'v0.3.2');
  assert.equal(formatVersion(' 1.2.0-beta.1 '), 'v1.2.0-beta.1');
  assert.equal(formatVersion('v0.3.2'), 'v0.3.2');
  assert.equal(formatVersion('dev'), 'dev');
  assert.equal(formatVersion(''), '');
  assert.equal(formatVersion(undefined), '');
});

test('MockBackendAPI appVersion reports a dev build', async () => {
  assert.equal(await new MockBackendAPI().appVersion(), 'dev');
});

test('nextThemeMode cycles system, light, dark', () => {
  assert.equal(nextThemeMode('system'), 'light');
  assert.equal(nextThemeMode('light'), 'dark');
  assert.equal(nextThemeMode('dark'), 'system');
});

test('resolveTheme follows the OS only in system mode', () => {
  assert.equal(resolveTheme('system', true), 'dark');
  assert.equal(resolveTheme('system', false), 'light');
  assert.equal(resolveTheme('light', true), 'light');
  assert.equal(resolveTheme('dark', false), 'dark');
});

test('isThemeMode accepts only known saved values', () => {
  assert.ok(isThemeMode('system'));
  assert.ok(isThemeMode('dark'));
  assert.ok(!isThemeMode('auto'));
  assert.ok(!isThemeMode(null));
});

test('themeButtonTitle names the current mode and the next one', () => {
  assert.equal(themeButtonTitle('system', 'dark'), 'Theme: System (dark). Click for Light');
  assert.equal(themeButtonTitle('light', 'light'), 'Theme: Light. Click for Dark');
  assert.equal(themeButtonTitle('dark', 'dark'), 'Theme: Dark. Click for System');
});
