import { KeyRound, Plus, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { del, get, post } from '../api';
import { t } from '../i18n';
import { ago } from '../lib/format';
import { Badge, Button, Card, Empty, ErrorBox, Field, IconButton, Loading, Modal, useLoad, useUI } from './ui';

type SSHKey = { id: number; name: string; fingerprint: string; type: string; lastUsedAt: number; lastIp: string; createdAt: number };
type Form = { name: string; publicKey: string; password: string };

/** SSH keys of the account, for SFTP (required when two-factor authentication is on). */
export default function SSHKeysCard() {
  const { toast, confirm } = useUI();
  const { data, error, reload } = useLoad(() => get<SSHKey[]>('/account/ssh-keys'));
  const [form, setForm] = useState<Form | null>(null);
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);

  const add = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form) return;
    setBusy(true);
    setErr('');
    try { await post('/account/ssh-keys', form); setForm(null); toast(t('sshkeys.added')); reload(true); } catch (e: any) { setErr(e.message); } finally { setBusy(false); }
  };
  const remove = async (k: SSHKey) => {
    if (!(await confirm({ title: t('sshkeys.deleteTitle', { name: k.name }), body: t('sshkeys.deleteBody'), confirm: t('common.delete'), danger: true }))) return;
    try { await del(`/account/ssh-keys/${k.id}`); toast(t('sshkeys.deleted')); reload(true); } catch (e: any) { toast(e.message, 'err'); }
  };

  return (
    <Card padded={false} className="lg:col-span-2" title={t('sshkeys.title')} description={t('sshkeys.description')}
      actions={<Button size="sm" variant="primary" icon={<Plus />} onClick={() => { setErr(''); setForm({ name: '', publicKey: '', password: '' }); }}>{t('sshkeys.add')}</Button>}>
      {error ? <div className="p-4"><ErrorBox error={error} /></div> : !data ? <Loading /> : !data.length ? (
        <Empty icon={<KeyRound />} title={t('sshkeys.empty')}>{t('sshkeys.emptyHint')}</Empty>
      ) : (
        <ul className="divide-y divide-ink-800">
          {data.map((k) => (
            <li key={k.id} className="flex flex-wrap items-center gap-4 px-5 py-3.5">
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium">{k.name}</span>
                  <Badge>{k.type.replace(/^ssh-|^sk-/, '').replace('@openssh.com', '')}</Badge>
                </div>
                <p className="mt-1 truncate font-mono text-xs text-ink-400">{k.fingerprint}</p>
                <p className="mt-0.5 text-xs text-ink-500">{k.lastUsedAt ? t('apikeys.lastUsed', { when: ago(k.lastUsedAt), ip: k.lastIp }) : t('apikeys.neverUsed')}</p>
              </div>
              <IconButton title={t('common.delete')} className="hover:!text-red-300" onClick={() => remove(k)}><Trash2 /></IconButton>
            </li>
          ))}
        </ul>
      )}
      <Modal open={!!form} onClose={() => setForm(null)} wide title={t('sshkeys.add')}
        footer={<><Button variant="ghost" onClick={() => setForm(null)}>{t('common.cancel')}</Button><Button type="submit" form="sshkey-form" variant="primary" loading={busy}>{t('sshkeys.add')}</Button></>}>
        {form && (
          <form id="sshkey-form" onSubmit={add} className="space-y-4">
            {err && <ErrorBox error={err} />}
            <Field label={t('common.name')}>
              <input className="input" value={form.name} maxLength={60} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder={t('sshkeys.namePlaceholder')} autoFocus required />
            </Field>
            <Field label={t('sshkeys.publicKey')} hint={t('sshkeys.publicKeyHint')}>
              <textarea className="input min-h-[90px] font-mono text-xs" value={form.publicKey} onChange={(e) => setForm({ ...form, publicKey: e.target.value })} placeholder="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA… me@laptop" spellCheck={false} required />
            </Field>
            <Field label={t('account.currentPassword')}>
              <input type="password" className="input" autoComplete="current-password" value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} required />
            </Field>
          </form>
        )}
      </Modal>
    </Card>
  );
}
