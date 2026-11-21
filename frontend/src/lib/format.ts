import { locale, t } from '../i18n';
export function bytes(n: number | undefined | null, digits = 1): string {
  if (n == null || isNaN(n)) return '—';
  const u = [t('unit.b'), t('unit.kb'), t('unit.mb'), t('unit.gb'), t('unit.tb')];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(i === 0 ? 0 : digits)} ${u[i]}`;
}

export const mb = (n: number) => (n >= 1024 ? `${(n / 1024).toFixed(n % 1024 === 0 ? 0 : 1)} ${t('unit.gb')}` : `${n} ${t('unit.mb')}`);

export function cpuPct(milli: number, limit: number): number {
  if (!limit) return milli / 10; // % of one core
  return (milli / limit) * 100;
}

export const cpuLabel = (m: number) => (m === 0 ? t('common.unlimited') : m >= 1000 ? `${(m / 1000).toFixed(m % 1000 ? 1 : 0)} vCPU` : `${m}m`);

export function duration(sec: number): string {
  if (sec < 0) sec = 0;
  const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60), s = Math.floor(sec % 60);
  if (d) return `${d}${t('unit.day')} ${h}h ${m}m`;
  if (h) return `${h}h ${m}m`;
  if (m) return `${m}m ${s}s`;
  return `${s}s`;
}

const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto', style: 'short' });

export function ago(ts: number): string {
  if (!ts) return t('time.never');
  const s = Date.now() / 1000 - ts;
  if (s < 0) {
    const f = -s;
    if (f < 60) return rtf.format(Math.round(f), 'second');
    if (f < 3600) return rtf.format(Math.round(f / 60), 'minute');
    if (f < 86400) return rtf.format(Math.round(f / 3600), 'hour');
    return rtf.format(Math.round(f / 86400), 'day');
  }
  if (s < 45) return t('time.justNow');
  if (s < 3600) return rtf.format(-Math.round(s / 60), 'minute');
  if (s < 86400) return rtf.format(-Math.round(s / 3600), 'hour');
  if (s < 30 * 86400) return rtf.format(-Math.round(s / 86400), 'day');
  return new Date(ts * 1000).toLocaleDateString(locale);
}

export const dateTime = (ts: number) =>
  ts ? new Date(ts * 1000).toLocaleString(locale, { dateStyle: 'short', timeStyle: 'short' }) : '—';

export const statusLabel: Record<string, string> = {
  running: t('status.running'), starting: t('status.starting'), stopping: t('status.stopping'), offline: t('status.offline'), installing: t('status.installing'), crashed: t('status.crashed'),
  sleeping: t('status.sleeping'),
};

export function joinPath(dir: string, name: string) {
  return (dir.endsWith('/') ? dir : dir + '/') + name;
}

export function parentPath(p: string) {
  const parts = p.split('/').filter(Boolean);
  parts.pop();
  return '/' + parts.join('/');
}

export async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    const t = document.createElement('textarea');
    t.value = text;
    document.body.appendChild(t);
    t.select();
    const ok = document.execCommand('copy');
    t.remove();
    return ok;
  }
}
