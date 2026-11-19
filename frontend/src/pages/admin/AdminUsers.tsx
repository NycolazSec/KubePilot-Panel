import { Pencil, Plus, Shield, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { del, get, post, put, User } from '../../api';
import { Badge, Button, Card, ErrorBox, Field, IconButton, Loading, Modal, Toggle, useLoad, useUI } from '../../components/ui';
import { dateTime } from '../../lib/format';
import { useAuth } from '../../main';

type Form = { id?: number; username: string; email: string; password: string; isAdmin: boolean };

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
      toast('Utilisateur enregistré');
      setForm(null);
      reload(true);
    } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(false); }
  };
  const remove = async (u: User) => {
    if (!(await confirm({ title: `Supprimer ${u.username} ?`, body: 'Ses accès aux serveurs partagés seront retirés.', confirm: 'Supprimer', danger: true }))) return;
    try { await del(`/admin/users/${u.id}`); reload(true); } catch (e: any) { toast(e.message, 'err'); }
  };

  return (
    <>
      <Card padded={false} title={`Utilisateurs (${data?.length ?? '…'})`} actions={<Button size="sm" variant="primary" icon={<Plus className="h-3.5 w-3.5" />} onClick={() => setForm({ username: '', email: '', password: '', isAdmin: false })}>Nouvel utilisateur</Button>}>
        {error ? <div className="p-4"><ErrorBox error={error} /></div> : loading || !data ? <Loading /> : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead><tr className="border-b border-ink-700 text-left text-xs text-ink-400">
                <th className="px-5 py-2 font-medium">Utilisateur</th><th className="px-3 py-2 font-medium">Rôle</th><th className="px-3 py-2 font-medium">Serveurs</th><th className="px-3 py-2 font-medium">Créé le</th><th className="w-24" />
              </tr></thead>
              <tbody>
                {data.map((u) => (
                  <tr key={u.id} className="border-b border-ink-800">
                    <td className="px-5 py-3"><div className="font-medium">{u.username}{u.id === me?.user.id && <span className="ml-2 text-xs text-ink-500">(toi)</span>}</div><div className="text-xs text-ink-400">{u.email}</div></td>
                    <td className="px-3 py-3">{u.isAdmin ? <Badge color="violet"><Shield className="h-3 w-3" /> Admin</Badge> : <Badge>Utilisateur</Badge>}</td>
                    <td className="px-3 py-3 text-ink-300">{u.servers}</td>
                    <td className="px-3 py-3 text-xs text-ink-400">{dateTime(u.createdAt)}</td>
                    <td className="px-3 py-2"><div className="flex justify-end gap-1">
                      <IconButton title="Modifier" onClick={() => setForm({ id: u.id, username: u.username, email: u.email, password: '', isAdmin: u.isAdmin })}><Pencil className="h-4 w-4" /></IconButton>
                      {u.id !== me?.user.id && <IconButton title="Supprimer" className="hover:!text-red-300" onClick={() => remove(u)}><Trash2 className="h-4 w-4" /></IconButton>}
                    </div></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      <Modal open={!!form} onClose={() => setForm(null)} title={form?.id ? `Modifier ${form.username}` : 'Nouvel utilisateur'}
        footer={<><Button variant="ghost" onClick={() => setForm(null)}>Annuler</Button><Button variant="primary" loading={busy} onClick={save}>Enregistrer</Button></>}>
        {form && (
          <div className="space-y-4">
            <Field label="Nom d'utilisateur"><input className="input" value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} /></Field>
            <Field label="Email"><input type="email" className="input" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} /></Field>
            <Field label={form.id ? 'Nouveau mot de passe (laisser vide pour ne pas changer)' : 'Mot de passe'} hint="8 caractères minimum.">
              <input type="password" className="input" autoComplete="new-password" value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} />
            </Field>
            <Toggle checked={form.isAdmin} onChange={(v) => setForm({ ...form, isAdmin: v })} label="Administrateur (accès total au panel)" />
          </div>
        )}
      </Modal>
    </>
  );
}
