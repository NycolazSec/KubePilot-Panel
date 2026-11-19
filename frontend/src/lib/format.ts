export function bytes(n: number | undefined | null, digits = 1): string {
  if (n == null || isNaN(n)) return '—';
  const u = ['o', 'Ko', 'Mo', 'Go', 'To'];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(i === 0 ? 0 : digits)} ${u[i]}`;
}

export const mb = (n: number) => (n >= 1024 ? `${(n / 1024).toFixed(n % 1024 === 0 ? 0 : 1)} Go` : `${n} Mo`);

export function cpuPct(milli: number, limit: number): number {
  if (!limit) return milli / 10; // % of one core
  return (milli / limit) * 100;
}

export const cpuLabel = (m: number) => (m === 0 ? 'Illimité' : m >= 1000 ? `${(m / 1000).toFixed(m % 1000 ? 1 : 0)} vCPU` : `${m}m`);

export function duration(sec: number): string {
  if (sec < 0) sec = 0;
  const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60), s = Math.floor(sec % 60);
  if (d) return `${d}j ${h}h ${m}m`;
  if (h) return `${h}h ${m}m`;
  if (m) return `${m}m ${s}s`;
  return `${s}s`;
}

export function ago(ts: number): string {
  if (!ts) return 'jamais';
  const s = Date.now() / 1000 - ts;
  if (s < 0) {
    const f = -s;
    if (f < 60) return `dans ${Math.round(f)}s`;
    if (f < 3600) return `dans ${Math.round(f / 60)} min`;
    if (f < 86400) return `dans ${Math.round(f / 3600)} h`;
    return `dans ${Math.round(f / 86400)} j`;
  }
  if (s < 45) return "à l'instant";
  if (s < 3600) return `il y a ${Math.round(s / 60)} min`;
  if (s < 86400) return `il y a ${Math.round(s / 3600)} h`;
  if (s < 30 * 86400) return `il y a ${Math.round(s / 86400)} j`;
  return new Date(ts * 1000).toLocaleDateString('fr-FR');
}

export const dateTime = (ts: number) =>
  ts ? new Date(ts * 1000).toLocaleString('fr-FR', { dateStyle: 'short', timeStyle: 'short' }) : '—';

export const statusLabel: Record<string, string> = {
  running: 'En ligne', starting: 'Démarrage', stopping: 'Arrêt', offline: 'Hors ligne', installing: 'Installation', crashed: 'Planté',
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
