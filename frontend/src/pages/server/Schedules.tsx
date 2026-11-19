import { CalendarClock, Pencil, Play, Plus, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { del, get, post, put, Schedule } from '../../api';
import { Badge, Button, Card, Empty, ErrorBox, Field, IconButton, Loading, Modal, Toggle, useLoad, useUI } from '../../components/ui';
import { ago, dateTime } from '../../lib/format';
import { useServer } from './ServerLayout';

const presets = [
  { label: 'Toutes les 30 min', cron: '*/30 * * * *' },
  { label: 'Toutes les heures', cron: '0 * * * *' },
  { label: 'Toutes les 6 h', cron: '0 */6 * * *' },
  { label: 'Chaque jour à 4 h', cron: '0 4 * * *' },
  { label: 'Chaque lundi à 3 h', cron: '0 3 * * 1' },
];

const actions: Record<string, string> = { command: 'Commande', restart: 'Redémarrer', start: 'Démarrer', stop: 'Arrêter', backup: 'Sauvegarde' };

const blank: Partial<Schedule> = { name: '', cron: '0 4 * * *', action: 'restart', payload: '', enabled: true, onlyOnline: false };

export default function Schedules() {
  const { server } = useServer();
  const { toast, confirm } = useUI();
  const base = `/servers/${server.id}/schedules`;
  const { data, error, loading, reload } = useLoad(() => get<{ schedules: Schedule[]; timezone: string }>(base), [server.id]);
  const [edit, setEdit] = useState<Partial<Schedule> | null>(null);
  const [busy, setBusy] = useState(false);

  const save = async () => {
    if (!edit) return;
    setBusy(true);
    try {
      if (edit.id) await put(`${base}/${edit.id}`, edit);
      else await post(base, edit);
      toast('Tâche enregistrée');
      setEdit(null);
      reload(true);
    } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(false); }
  };

  const run = async (s: Schedule) => {
    try { const r = await post<{ result: string }>(`${base}/${s.id}/run`); toast(`${s.name} : ${r.result}`, r.result === 'OK' ? 'ok' : 'info'); reload(true); } catch (e: any) { toast(e.message, 'err'); }
  };
  const remove = async (s: Schedule) => {
    if (!(await confirm({ title: `Supprimer « ${s.name} » ?`, confirm: 'Supprimer', danger: true }))) return;
    try { await del(`${base}/${s.id}`); reload(true); } catch (e: any) { toast(e.message, 'err'); }
  };

  return (
    <>
      <Card padded={false} title={<>Tâches planifiées {data && <span className="ml-2 text-xs font-normal text-ink-400">Fuseau : {data.timezone}</span>}</>}
        actions={<Button size="sm" variant="primary" icon={<Plus className="h-3.5 w-3.5" />} onClick={() => setEdit({ ...blank })}>Nouvelle tâche</Button>}>
        {error ? <div className="p-4"><ErrorBox error={error} /></div> : loading && !data ? <Loading /> : !data?.schedules.length ? (
          <Empty icon={<CalendarClock className="h-6 w-6" />} title="Aucune tâche planifiée">Redémarrages automatiques, sauvegardes quotidiennes, annonces… tout se règle ici.</Empty>
        ) : (
          <ul className="divide-y divide-ink-800">
            {data.schedules.map((s) => (
              <li key={s.id} className="flex flex-wrap items-center gap-4 px-5 py-3.5">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">{s.name}</span>
                    <Badge color="violet">{actions[s.action]}</Badge>
                    {!s.enabled && <Badge>Désactivée</Badge>}
                    {s.onlyOnline && <Badge color="blue">Si en ligne</Badge>}
                  </div>
                  <p className="mt-0.5 text-xs text-ink-400">
                    <code className="rounded bg-ink-800 px-1.5 py-0.5 font-mono text-ink-300">{s.cron}</code>
                    {s.payload && <> · <span className="font-mono">{s.payload}</span></>}
                    {s.enabled && <> · prochaine : <span title={dateTime(s.nextRun)}>{ago(s.nextRun)}</span></>}
                    {s.lastRun > 0 && <> · dernière : {ago(s.lastRun)} ({s.lastResult})</>}
                  </p>
                </div>
                <div className="flex items-center gap-1">
                  <IconButton title="Exécuter maintenant" onClick={() => run(s)}><Play className="h-4 w-4" /></IconButton>
                  <IconButton title="Modifier" onClick={() => setEdit(s)}><Pencil className="h-4 w-4" /></IconButton>
                  <IconButton title="Supprimer" className="hover:!text-red-300" onClick={() => remove(s)}><Trash2 className="h-4 w-4" /></IconButton>
                </div>
              </li>
            ))}
          </ul>
        )}
      </Card>

      <Modal open={!!edit} onClose={() => setEdit(null)} title={edit?.id ? 'Modifier la tâche' : 'Nouvelle tâche'}
        footer={<><Button variant="ghost" onClick={() => setEdit(null)}>Annuler</Button><Button variant="primary" loading={busy} onClick={save}>Enregistrer</Button></>}>
        {edit && (
          <div className="space-y-4">
            <Field label="Nom"><input className="input" value={edit.name} onChange={(e) => setEdit({ ...edit, name: e.target.value })} placeholder="Redémarrage quotidien" /></Field>
            <Field label="Action">
              <select className="input" value={edit.action} onChange={(e) => setEdit({ ...edit, action: e.target.value })}>
                {Object.entries(actions).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
              </select>
            </Field>
            {edit.action === 'command' && <Field label="Commande"><input className="input font-mono" value={edit.payload} onChange={(e) => setEdit({ ...edit, payload: e.target.value })} placeholder="say Redémarrage dans 5 minutes !" /></Field>}
            {edit.action === 'backup' && <Field label="Nom de la sauvegarde (optionnel)" hint="Quand la limite est atteinte, la plus ancienne sauvegarde non verrouillée est supprimée."><input className="input" value={edit.payload} onChange={(e) => setEdit({ ...edit, payload: e.target.value })} /></Field>}
            <Field label="Planification (cron)" hint="minute · heure · jour du mois · mois · jour de la semaine">
              <input className="input font-mono" value={edit.cron} onChange={(e) => setEdit({ ...edit, cron: e.target.value })} />
              <div className="mt-2 flex flex-wrap gap-1.5">
                {presets.map((p) => <button key={p.cron} type="button" onClick={() => setEdit({ ...edit, cron: p.cron })} className="rounded-lg border border-ink-600 px-2 py-1 text-xs text-ink-300 hover:border-brand-500 hover:text-ink-100">{p.label}</button>)}
              </div>
            </Field>
            <div className="flex flex-col gap-3">
              <Toggle checked={!!edit.enabled} onChange={(v) => setEdit({ ...edit, enabled: v })} label="Activée" />
              <Toggle checked={!!edit.onlyOnline} onChange={(v) => setEdit({ ...edit, onlyOnline: v })} label="Seulement si le serveur est en ligne" />
            </div>
          </div>
        )}
      </Modal>
    </>
  );
}
