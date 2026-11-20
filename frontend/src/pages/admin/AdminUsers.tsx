import { Pencil, Plus, Shield, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { del, get, post, put, User } from '../../api';
import { Badge, Button, Card, ErrorBox, Field, IconButton, Loading, Modal, Toggle, useLoad, useUI } from '../../components/ui';
import { dateTime } from '../../lib/format';
import { useAuth } from '../../main';
import { t } from '../../i18n';

type Form = { id?: number; username: string; email: string; password: string; isAdmin: boolean; twoFactor?: boolean; reset2fa?: boolean };

export default function AdminUsers() {
  const { me } = useAuth();
  const { toast, confirm } = useUI();
  const { data, error, loading, reload } = useLoad(() => get<User[]>('/admin/users'));
  const [form, setForm] = useState<Form | null>(null);
  const [busy, setBusy] = useState(false);

  const save = async () => {
    if (!form) return;
    setBusy(true);
    try {
      if (form.id) await put(`/admin/users/${form.id}`, form);
      else await post('/admin/users', form);
      toast(t('users.saved'));
      setForm(null);
      reload(true);
    } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(false); }
  };
  const remove = async (u: User) => {
    if (!(await confirm({ title: t('users.deleteTitle', { name: u.username }), body: t('users.deleteBody'), confirm: t('common.delete'), danger: true }))) return;
    try { await del(`/admin/users/${u.id}`); reload(true); } catch (e: any) { toast(e.message, 'err'); }
  };

  return (
    <>
      <Card padded={false} title={t('users.title', { count: data?.length ?? '…' })} actions={<Button size="sm" variant="primary" icon={<Plus className="h-3.5 w-3.5" />} onClick={() => setForm({ username: '', email: '', password: '', isAdmin: false })}>{t('users.new')}</Button>}>
        {error ? <div className="p-4"><ErrorBox error={error} /></div> : loading || !data ? <Loading /> : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead><tr className="border-b border-ink-700">
                <th className="th">{t('users.user')}</th><th className="th">{t('account.role')}</th><th className="th">{t('nav.servers')}</th><th className="th">{t('settings.createdAt')}</th><th className="w-24" />
              </tr></thead>
              <tbody>
                {data.map((u) => (
                  <tr key={u.id} className="border-b border-ink-800">
                    <td className="px-4 py-3"><div className="font-medium">{u.username}{u.id === me?.user.id && <span className="ml-2 text-xs text-ink-500">{t('users.you')}</span>}</div><div className="text-xs text-ink-400">{u.email}</div></td>
                    <td className="px-4 py-3"><div className="flex gap-1">{u.isAdmin ? <Badge color="violet"><Shield className="h-3 w-3" /> {t('users.admin')}</Badge> : <Badge>{t('users.user')}</Badge>}{u.twoFactor && <Badge color="green">{t('users.twofa')}</Badge>}</div></td>
                    <td className="px-4 py-3 text-ink-300">{u.servers}</td>
                    <td className="px-4 py-3 text-xs text-ink-400">{dateTime(u.createdAt)}</td>
                    <td className="px-4 py-2"><div className="flex justify-end gap-1">
                      <IconButton title={t('common.edit')} onClick={() => setForm({ id: u.id, username: u.username, email: u.email, password: '', isAdmin: u.isAdmin, twoFactor: u.twoFactor })}><Pencil className="h-4 w-4" /></IconButton>
                      {u.id !== me?.user.id && <IconButton title={t('common.delete')} className="hover:!text-red-300" onClick={() => remove(u)}><Trash2 className="h-4 w-4" /></IconButton>}
                    </div></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      <Modal open={!!form} onClose={() => setForm(null)} title={form?.id ? t('users.editTitle', { name: form.username }) : t('users.new')}
        footer={<><Button variant="ghost" onClick={() => setForm(null)}>{t('common.cancel')}</Button><Button variant="primary" loading={busy} onClick={save}>{t('common.save')}</Button></>}>
        {form && (
          <div className="space-y-4">
            <Field label={t('account.username')}><input className="input" value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} /></Field>
            <Field label={t('account.email')}><input type="email" className="input" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} /></Field>
            <Field label={form.id ? t('users.newPassword') : t('login.password')} hint={t('account.passwordHint')}>
              <input type="password" className="input" autoComplete="new-password" value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} />
            </Field>
            <Toggle checked={form.isAdmin} onChange={(v) => setForm({ ...form, isAdmin: v })} label={t('users.isAdmin')} />
            {form.twoFactor && <Toggle checked={!!form.reset2fa} onChange={(v) => setForm({ ...form, reset2fa: v })} label={t('users.reset2fa')} />}
          </div>
        )}
      </Modal>
    </>
  );
}
