import { Pencil, Plus, Trash2, Users } from 'lucide-react';
import { useState } from 'react';
import { del, get, post, put, SubUser } from '../../api';
import { Badge, Button, Card, Empty, ErrorBox, Field, IconButton, Loading, Modal, useLoad, useUI } from '../../components/ui';
import { ago } from '../../lib/format';
import { useServer } from './ServerLayout';

export const permLabels: Record<string, string> = {
  console: 'Console', power: 'Démarrer / arrêter', files: 'Fichiers', backups: 'Sauvegardes', schedules: 'Tâches planifiées',
  startup: 'Démarrage & variables', network: 'Réseau', activity: 'Journal d\'activité',
};

export default function SubUsers() {
  const { server } = useServer();
  const { toast, confirm } = useUI();
  const base = `/servers/${server.id}/users`;
  const { data, error, loading, reload } = useLoad(() => get<{ users: SubUser[]; permissions: string[] }>(base), [server.id]);
  const [edit, setEdit] = useState<{ userId?: number; username: string; permissions: string[] } | null>(null);
  const [busy, setBusy] = useState(false);

  const save = async () => {
    if (!edit) return;
    setBusy(true);
    try {
      if (edit.userId) await put(`${base}/${edit.userId}`, edit);
      else await post(base, edit);
      toast('Accès enregistré');
      setEdit(null);
      reload(true);
    } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(false); }
  };
  const remove = async (u: SubUser) => {
    if (!(await confirm({ title: `Retirer l'accès de ${u.username} ?`, confirm: 'Retirer', danger: true }))) return;
    try { await del(`${base}/${u.userId}`); reload(true); } catch (e: any) { toast(e.message, 'err'); }
  };

  const toggle = (p: string) => edit && setEdit({ ...edit, permissions: edit.permissions.includes(p) ? edit.permissions.filter((x) => x !== p) : [...edit.permissions, p] });

  return (
    <>
      <Card padded={false} title="Utilisateurs ayant accès" actions={<Button size="sm" variant="primary" icon={<Plus className="h-3.5 w-3.5" />} onClick={() => setEdit({ username: '', permissions: ['console', 'power', 'files'] })}>Ajouter</Button>}>
        {error ? <div className="p-4"><ErrorBox error={error} /></div> : loading && !data ? <Loading /> : !data?.users.length ? (
          <Empty icon={<Users className="h-6 w-6" />} title="Personne d'autre n'a accès">Donne accès à un ami ou un modérateur, avec des permissions précises.</Empty>
        ) : (
          <ul className="divide-y divide-ink-800">
            {data.users.map((u) => (
              <li key={u.userId} className="flex flex-wrap items-center gap-4 px-5 py-3.5">
                <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-ink-700 text-xs font-semibold">{u.username.slice(0, 2).toUpperCase()}</div>
                <div className="min-w-0 flex-1">
                  <p className="font-medium">{u.username} <span className="text-xs font-normal text-ink-400">· ajouté {ago(u.createdAt)}</span></p>
                  <div className="mt-1 flex flex-wrap gap-1">{u.permissions.map((p) => <Badge key={p}>{permLabels[p] || p}</Badge>)}</div>
                </div>
                <IconButton title="Modifier" onClick={() => setEdit({ userId: u.userId, username: u.username, permissions: u.permissions })}><Pencil className="h-4 w-4" /></IconButton>
                <IconButton title="Retirer" className="hover:!text-red-300" onClick={() => remove(u)}><Trash2 className="h-4 w-4" /></IconButton>
              </li>
            ))}
          </ul>
        )}
      </Card>
      <Modal open={!!edit} onClose={() => setEdit(null)} title={edit?.userId ? `Permissions de ${edit.username}` : 'Donner accès'}
        footer={<><Button variant="ghost" onClick={() => setEdit(null)}>Annuler</Button><Button variant="primary" loading={busy} onClick={save}>Enregistrer</Button></>}>
        {edit && (
          <div className="space-y-4">
            {!edit.userId && <Field label="Nom d'utilisateur ou email" hint="La personne doit déjà avoir un compte sur le panel."><input className="input" value={edit.username} onChange={(e) => setEdit({ ...edit, username: e.target.value })} autoFocus /></Field>}
            <div className="grid gap-2 sm:grid-cols-2">
              {(data?.permissions || []).map((p) => (
                <label key={p} className="flex cursor-pointer items-center gap-2.5 rounded-xl border border-ink-700 px-3 py-2.5 text-sm hover:border-ink-500">
                  <input type="checkbox" className="accent-brand-500" checked={edit.permissions.includes(p)} onChange={() => toggle(p)} />
                  {permLabels[p] || p}
                </label>
              ))}
            </div>
          </div>
        )}
      </Modal>
    </>
  );
}
