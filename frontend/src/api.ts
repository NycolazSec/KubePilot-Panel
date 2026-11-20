import { lang, t } from './i18n';
export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

type Opts = { method?: string; body?: unknown; raw?: BodyInit; headers?: Record<string, string>; text?: boolean };

export async function api<T = any>(path: string, opts: Opts = {}): Promise<T> {
  const headers: Record<string, string> = { 'X-KubePilot': '1', 'X-KubePilot-Lang': lang, ...(opts.headers || {}) };
  let body: BodyInit | undefined = opts.raw;
  if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json';
    body = JSON.stringify(opts.body);
  }
  const res = await fetch('/api' + path, { method: opts.method || (body ? 'POST' : 'GET'), headers, body, credentials: 'same-origin' });
  if (res.status === 401 && !path.startsWith('/auth/')) {
    window.dispatchEvent(new Event('kubepilot:unauthorized'));
  }
  if (!res.ok) {
    let msg = t('error.http', { status: res.status });
    try {
      const j = await res.json();
      if (j.error) msg = j.error;
    } catch { /* ignore */ }
    throw new ApiError(res.status, msg);
  }
  if (opts.text) return (await res.text()) as T;
  const ct = res.headers.get('content-type') || '';
  return (ct.includes('json') ? await res.json() : await res.text()) as T;
}

export const get = <T = any>(p: string) => api<T>(p);
export const post = <T = any>(p: string, body?: unknown) => api<T>(p, { method: 'POST', body: body ?? {} });
export const put = <T = any>(p: string, body?: unknown) => api<T>(p, { method: 'PUT', body: body ?? {} });
export const del = <T = any>(p: string) => api<T>(p, { method: 'DELETE' });

// ---------- Types

export interface User { id: number; username: string; email: string; isAdmin: boolean; twoFactor: boolean; createdAt: number; servers?: number }
export interface Me { user: User; panelName: string; version: string; twoFactorRequired: boolean }

export type Status = 'offline' | 'installing' | 'starting' | 'running' | 'stopping' | 'crashed';
export interface State { status: Status; reason?: string; startedAt?: number; restarts: number; installError?: boolean; cpu: number; memory: number }

export interface Allocation { port: number; serverId: string; protocol: string; name: string; targetPort: number; isPrimary: boolean; notes: string }

export interface Server {
  id: string; name: string; description: string; ownerId: number; templateId: number; image: string; startup: string;
  env: Record<string, string>; memory: number; cpu: number; disk: number; backupLimit: number; allocationLimit: number;
  suspended: boolean; createdAt: number;
  template: string; templateSlug: string; consoleInput: boolean; owner: string; state: State; diskUsed: number;
  address: string; allocations: Allocation[]; permissions: string[]; features: string[];
}

export interface FileEntry { name: string; dir: boolean; size: number; mode: string; modified: number; symlink: boolean }
export interface Backup { id: string; name: string; size: number; status: 'pending' | 'completed' | 'failed'; error: string; locked: boolean; createdAt: number; completedAt: number }
export interface Schedule { id: number; name: string; cron: string; action: string; payload: string; enabled: boolean; onlyOnline: boolean; lastRun: number; nextRun: number; lastResult: string }
export interface Activity { id: number; serverId: string; userId: number; username: string; action: string; meta: string; ip: string; apiKey?: string; createdAt: number }
export interface SubUser { userId: number; username: string; email: string; permissions: string[]; createdAt: number }

export interface TemplateVar { key: string; name: string; description: string; default: string; userViewable: boolean; userEditable: boolean; options?: string[]; pattern?: string }
export interface TemplatePort { name: string; port: number; protocol: string }
export interface ImageOption { name: string; image: string }
export interface Template {
  id: number; slug: string; name: string; description: string; category: string; images: ImageOption[]; startup: string;
  dataPath: string; vars: TemplateVar[]; ports: TemplatePort[]; stopGrace: number; readyProbe: boolean; runAsUser: number;
  consoleInput: boolean; installImage: string; installScript: string; defaultMemory: number; defaultCpu: number; defaultDisk: number; updatedAt?: number;
  features?: string[];
}

export interface Settings { panelName: string; publicHost: string; portMin: number; portMax: number; timezone: string; twoFactorPolicy: '' | 'admins' | 'everyone' }
export interface NodeInfo { name: string; ready: boolean; version: string; os: string; cpuCap: number; memCap: number; cpu: number; mem: number; pods: number; address: string }

const templateOrder = ['minecraft-java', 'minecraft-bedrock', 'nodejs', 'python'];
export function sortTemplates<T extends { slug: string; name: string }>(list: T[]): T[] {
  const rank = (t: T) => { const i = templateOrder.indexOf(t.slug); return i === -1 ? (t.slug === 'custom' ? 99 : 50) : i; };
  return [...list].sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name));
}
