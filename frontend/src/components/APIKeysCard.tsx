import { BookOpen, Copy, KeyRound, Plus, Trash2, TriangleAlert } from 'lucide-react';
import { useState } from 'react';
import { del, get, post } from '../api';
import { t } from '../i18n';
import { ago, copy, dateTime } from '../lib/format';
import { Badge, Button, Card, Empty, ErrorBox, Field, IconButton, Loading, Modal, Toggle, useLoad, useUI } from './ui';

export interface APIKey {
  id: number; name: string; prefix: string; permissions: string[]; readOnly: boolean; allowedIps: string[];
  expiresAt: number; lastUsedAt: number; lastIp: string; createdAt: number;
}

const DOCS = 'https://github.com/NycolazSec/KubePilot-Panel/blob/main/docs/API.md';
const scopeLabel = (s: string) => {
  const labels: Record<string, string> = {
    console: t('perm.console'), power: t('perm.power'), files: t('perm.files'), backups: t('perm.backups'), databases: t('perm.databases'), schedules: t('perm.schedules'),
    startup: t('perm.startup'), network: t('perm.network'), activity: t('perm.activity'), users: t('perm.users'), settings: t('perm.settings'), admin: t('perm.admin'),
  };
  return labels[s] || s;
};

type Form = { name: string; permissions: string[]; readOnly: boolean; allowedIps: string; expiresInDays: number; password: string };
const blank: Form = { name: '', permissions: ['console', 'power', 'activity'], readOnly: false, allowedIps: '', expiresInDays: 0, password: '' };

export default function APIKeysCard() {
  const { toast, confirm } = useUI();
  const { data, error, reload } = useLoad(() => get<{ keys: APIKey[]; scopes: string[]; rateLimit: number }>('/account/api-keys'));
  const [form, setForm] = useState<Form | null>(null);
  const [created, setCreated] = useState<{ key: APIKey; token: string } | null>(null);
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);

  const create = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form) return;
    setBusy(true);
    setErr('');
    try {
      const r = await post<{ key: APIKey; token: string }>('/account/api-keys', {
        ...form, allowedIps: form.allowedIps.split(/[\s,]+/).filter(Boolean),
      });
      setForm(null);
      setCreated(r);
      reload(true);
    } catch (e: any) { setErr(e.message); } finally { setBusy(false); }
  };
  const revoke = async (k: APIKey) => {
    if (!(await confirm({ title: t('apikeys.revokeTitle', { name: k.name }), body: t('apikeys.revokeBody'), confirm: t('apikeys.revoke'), danger: true }))) return;
    try { await del(`/account/api-keys/${k.id}`); toast(t('apikeys.revoked')); reload(true); } catch (e: any) { toast(e.message, 'err'); }
  };
  const toggleScope = (s: string) => form && setForm({ ...form, permissions: form.permissions.includes(s) ? form.permissions.filter((x) => x !== s) : [...form.permissions, s] });
  const expired = (k: APIKey) => k.expiresAt > 0 && k.expiresAt * 1000 < Date.now();
  const curl = created ? `curl -H "Authorization: Bearer ${created.token}" ${location.origin}/api/servers` : '';

  return (
    <Card padded={false} className="lg:col-span-2" title={t('apikeys.title')} description={t('apikeys.description', { rate: data?.rateLimit ?? 240 })}
      actions={<>
        <a href={DOCS} target="_blank" rel="noopener noreferrer" className="inline-flex h-7 items-center gap-1.5 rounded-md px-2.5 text-xs font-medium text-ink-300 hover:bg-ink-800 hover:text-ink-100"><BookOpen className="h-3.5 w-3.5" />{t('apikeys.docs')}</a>
        <Button size="sm" variant="primary" icon={<Plus />} disabled={!data} onClick={() => { setErr(''); setForm({ ...blank }); }}>{t('apikeys.new')}</Button>
      </>}>
      {error ? <div className="p-4"><ErrorBox error={error} /></div> : !data ? <Loading /> : !data.keys.length ? (
        <Empty icon={<KeyRound />} title={t('apikeys.empty')}>{t('apikeys.emptyHint')}</Empty>
      ) : (
        <ul className="divide-y divide-ink-800">
          {data.keys.map((k) => (
            <li key={k.id} className="flex flex-wrap items-center gap-4 px-5 py-3.5">
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium">{k.name}</span>
                  <code className="rounded bg-ink-800 px-1.5 py-0.5 font-mono text-2xs text-ink-400">{k.prefix}…</code>
                  {k.readOnly && <Badge color="blue">{t('apikeys.readOnly')}</Badge>}
                  {expired(k) && <Badge color="red">{t('apikeys.expired')}</Badge>}
                </div>
                <div className="mt-1 flex flex-wrap gap-1">{k.permissions.map((p) => <Badge key={p}>{scopeLabel(p)}</Badge>)}</div>
                <p className="mt-1 text-xs text-ink-400">
                  {k.lastUsedAt ? t('apikeys.lastUsed', { when: ago(k.lastUsedAt), ip: k.lastIp }) : t('apikeys.neverUsed')}
                  {k.allowedIps.length > 0 && <> · {t('apikeys.ips', { ips: k.allowedIps.join(', ') })}</>}
                  {k.expiresAt > 0 && !expired(k) && <> · <span title={dateTime(k.expiresAt)}>{t('apikeys.expires', { when: ago(k.expiresAt) })}</span></>}
                </p>
              </div>
              <IconButton title={t('apikeys.revoke')} className="hover:!text-red-300" onClick={() => revoke(k)}><Trash2 /></IconButton>
            </li>
          ))}
        </ul>
      )}

      <Modal open={!!form} onClose={() => setForm(null)} wide title={t('apikeys.new')}
        footer={<><Button variant="ghost" onClick={() => setForm(null)}>{t('common.cancel')}</Button><Button type="submit" form="apikey-form" variant="primary" loading={busy}>{t('apikeys.create')}</Button></>}>
        {form && data && (
          <form id="apikey-form" onSubmit={create} className="space-y-4">
            {err && <ErrorBox error={err} />}
            <Field label={t('common.name')} hint={t('apikeys.nameHint')}>
              <input className="input" value={form.name} maxLength={60} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder={t('apikeys.namePlaceholder')} autoFocus required />
            </Field>
            <div>
              <span className="label">{t('apikeys.permissions')}</span>
              <p className="mb-2 text-xs text-ink-400">{t('apikeys.permissionsHint')}</p>
              <div className="grid gap-2 sm:grid-cols-3">
                {data.scopes.map((s) => (
                  <label key={s} className="flex cursor-pointer items-center gap-2.5 rounded-md border border-ink-700 px-3 py-2 text-sm hover:border-ink-500">
                    <input type="checkbox" className="accent-brand-500" checked={form.permissions.includes(s)} onChange={() => toggleScope(s)} />
                    {scopeLabel(s)}
                  </label>
                ))}
              </div>
            </div>
            <Toggle checked={form.readOnly} onChange={(v) => setForm({ ...form, readOnly: v })} label={t('apikeys.readOnlyLabel')} />
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label={t('apikeys.allowedIps')} hint={t('apikeys.allowedIpsHint')}>
                <textarea className="input min-h-[70px] font-mono text-xs" value={form.allowedIps} onChange={(e) => setForm({ ...form, allowedIps: e.target.value })} placeholder={'203.0.113.10\n2001:db8::/64'} spellCheck={false} />
              </Field>
              <Field label={t('apikeys.expiry')}>
                <select className="input" value={form.expiresInDays} onChange={(e) => setForm({ ...form, expiresInDays: +e.target.value })}>
                  <option value={0}>{t('apikeys.never')}</option>
                  {[30, 90, 365].map((d) => <option key={d} value={d}>{t('apikeys.days', { count: d })}</option>)}
                </select>
              </Field>
            </div>
            <Field label={t('account.currentPassword')} hint={t('apikeys.passwordHint')}>
              <input type="password" className="input" autoComplete="current-password" value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} required />
            </Field>
          </form>
        )}
      </Modal>

      <Modal open={!!created} onClose={() => setCreated(null)} wide title={t('apikeys.createdTitle', { name: created?.key.name ?? '' })}
        footer={<Button variant="primary" onClick={() => setCreated(null)}>{t('twofa.saved')}</Button>}>
        {created && (
          <div className="space-y-4">
            <div className="flex items-start gap-2 rounded-md border border-amber-500/30 bg-amber-500/[.07] px-3 py-2.5 text-[13px] text-amber-200">
              <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0 text-amber-400" />{t('apikeys.copyNow')}
            </div>
            <button type="button" onClick={async () => { await copy(created.token); toast(t('twofa.copied')); }}
              className="flex w-full items-center gap-2 break-all rounded-md border border-ink-700 bg-ink-950 px-3 py-2.5 text-left font-mono text-sm text-ink-100 hover:border-ink-500">
              <Copy className="h-4 w-4 shrink-0 text-ink-400" />{created.token}
            </button>
            <div>
              <span className="label">{t('apikeys.example')}</span>
              <button type="button" onClick={async () => { await copy(curl); toast(t('twofa.copied')); }}
                className="block w-full overflow-x-auto whitespace-pre rounded-md border border-ink-700 bg-ink-950 px-3 py-2.5 text-left font-mono text-xs text-ink-300 hover:border-ink-500">{curl}</button>
            </div>
          </div>
        )}
      </Modal>
    </Card>
  );
}
