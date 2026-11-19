import { Download, History, Lock, LockOpen, Plus, RotateCcw, Trash2 } from 'lucide-react';
import { useEffect, useState } from 'react';
import { Backup, del, get, post } from '../../api';
import { Badge, Button, Card, Empty, ErrorBox, Field, IconButton, Loading, Modal, Spinner, Toggle, useLoad, useUI } from '../../components/ui';
import { ago, bytes, dateTime } from '../../lib/format';
import { useServer } from './ServerLayout';

export default function Backups() {
  const { server, state } = useServer();
  const { toast, confirm } = useUI();
  const base = `/servers/${server.id}/backups`;
  const { data, error, loading, reload } = useLoad(() => get<{ backups: Backup[]; limit: number }>(base), [server.id]);
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState('');
  const [restore, setRestore] = useState<Backup | null>(null);
  const [truncate, setTruncate] = useState(false);
  const [busy, setBusy] = useState(false);

  const pending = data?.backups.some((b) => b.status === 'pending');
  useEffect(() => {
    if (!pending) return;
    const t = setInterval(() => reload(true), 2500);
    return () => clearInterval(t);
  }, [pending, reload]);

  const create = async () => {
    setBusy(true);
    try {
      await post(base, { name });
      toast('Sauvegarde lancée');
      setCreating(false);
      setName('');
      reload(true);
    } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(false); }
  };

  const doRestore = async () => {
    if (!restore) return;
    setBusy(true);
    try {
      await post(`${base}/${restore.id}/restore`, { truncate });
      toast('Sauvegarde restaurée');
      setRestore(null);
    } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(false); }
  };

  const remove = async (b: Backup) => {
    if (!(await confirm({ title: `Supprimer « ${b.name} » ?`, body: 'La sauvegarde sera définitivement effacée.', confirm: 'Supprimer', danger: true }))) return;
    try { await del(`${base}/${b.id}`); toast('Sauvegarde supprimée'); reload(true); } catch (e: any) { toast(e.message, 'err'); }
  };

  const lock = async (b: Backup) => {
    try { await post(`${base}/${b.id}/lock`); reload(true); } catch (e: any) { toast(e.message, 'err'); }
  };

  const used = data?.backups.length || 0;
  return (
    <>
      <Card padded={false} title={<>Sauvegardes <span className="ml-2 text-xs font-normal text-ink-400">{used} / {data?.limit ?? server.backupLimit}</span></>}
        actions={<Button size="sm" variant="primary" icon={<Plus className="h-3.5 w-3.5" />} disabled={!data || used >= data.limit || pending} onClick={() => setCreating(true)}>Nouvelle sauvegarde</Button>}>
        {error ? <div className="p-4"><ErrorBox error={error} /></div> : loading && !data ? <Loading /> : !data?.backups.length ? (
          <Empty icon={<History className="h-6 w-6" />} title="Aucune sauvegarde">{server.backupLimit > 0 ? 'Crée une sauvegarde complète des fichiers du serveur en un clic.' : 'Les sauvegardes sont désactivées pour ce serveur.'}</Empty>
        ) : (
          <ul className="divide-y divide-ink-800">
            {data.backups.map((b) => (
              <li key={b.id} className="flex flex-wrap items-center gap-4 px-5 py-3.5">
                <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-ink-800 text-brand-300">{b.status === 'pending' ? <Spinner className="h-4 w-4" /> : <History className="h-4 w-4" />}</div>
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">{b.name}</span>
                    {b.locked && <Badge color="violet"><Lock className="h-3 w-3" /> Verrouillée</Badge>}
                    {b.status === 'pending' && <Badge color="blue">En cours…</Badge>}
                    {b.status === 'failed' && <Badge color="red">Échec</Badge>}
                  </div>
                  <p className="mt-0.5 text-xs text-ink-400" title={dateTime(b.createdAt)}>
                    {ago(b.createdAt)}{b.status === 'completed' && ` · ${bytes(b.size)}`}{b.error && ` · ${b.error}`}
                  </p>
                </div>
                <div className="flex items-center gap-1">
                  {b.status === 'completed' && <>
                    <IconButton title="Télécharger" onClick={() => { location.href = `/api${base}/${b.id}/download`; }}><Download className="h-4 w-4" /></IconButton>
                    <IconButton title="Restaurer" onClick={() => { setTruncate(false); setRestore(b); }}><RotateCcw className="h-4 w-4" /></IconButton>
                  </>}
                  {b.status !== 'pending' && <IconButton title={b.locked ? 'Déverrouiller' : 'Verrouiller (protège de la rotation et de la suppression)'} onClick={() => lock(b)}>{b.locked ? <LockOpen className="h-4 w-4" /> : <Lock className="h-4 w-4" />}</IconButton>}
                  {!b.locked && b.status !== 'pending' && <IconButton title="Supprimer" className="hover:!text-red-300" onClick={() => remove(b)}><Trash2 className="h-4 w-4" /></IconButton>}
                </div>
              </li>
            ))}
          </ul>
        )}
      </Card>

      <Modal open={creating} onClose={() => setCreating(false)} title="Nouvelle sauvegarde"
        footer={<><Button variant="ghost" onClick={() => setCreating(false)}>Annuler</Button><Button variant="primary" loading={busy} onClick={create}>Lancer</Button></>}>
        <Field label="Nom (optionnel)" hint="Toute l'arborescence du serveur est archivée. Pour Minecraft, le monde est enregistré juste avant (save-all).">
          <input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder="Avant la mise à jour 1.22" autoFocus />
        </Field>
      </Modal>

      <Modal open={!!restore} onClose={() => setRestore(null)} title={`Restaurer « ${restore?.name} »`}
        footer={<><Button variant="ghost" onClick={() => setRestore(null)}>Annuler</Button><Button variant="danger" loading={busy} disabled={state.status !== 'offline'} onClick={doRestore}>Restaurer</Button></>}>
        <div className="space-y-4 text-sm text-ink-300">
          {state.status !== 'offline' && <ErrorBox error="Arrête le serveur avant de restaurer une sauvegarde." />}
          <p>Les fichiers de la sauvegarde remplaceront les fichiers actuels portant le même nom.</p>
          <Toggle checked={truncate} onChange={setTruncate} label="Supprimer d'abord tous les fichiers actuels (restauration propre)" />
        </div>
      </Modal>
    </>
  );
}
