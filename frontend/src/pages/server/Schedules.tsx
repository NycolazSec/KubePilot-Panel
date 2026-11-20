import { CalendarClock, Pencil, Play, Plus, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { del, get, post, put, Schedule } from '../../api';
import { Badge, Button, Card, Empty, ErrorBox, Field, IconButton, Loading, Modal, Toggle, useLoad, useUI } from '../../components/ui';
import { ago, dateTime } from '../../lib/format';
import { useServer } from './ServerLayout';
import { t } from '../../i18n';

const presets = [
  { label: t('schedules.preset.every30'), cron: '*/30 * * * *' },
  { label: t('schedules.preset.hourly'), cron: '0 * * * *' },
  { label: t('schedules.preset.every6h'), cron: '0 */6 * * *' },
  { label: t('schedules.preset.daily4'), cron: '0 4 * * *' },
  { label: t('schedules.preset.monday3'), cron: '0 3 * * 1' },
];

const actions: Record<string, string> = { command: t('schedules.action.command'), restart: t('power.restart'), start: t('power.start'), stop: t('power.stop'), backup: t('schedules.action.backup') };

// results are stored as codes by the panel (older ones may be plain text)
function resultLabel(r: string) {
  if (r === 'OK') return t('schedules.resultOk');
  if (r === 'skipped: suspended') return t('schedules.skippedSuspended');
  if (r === 'skipped: offline') return t('schedules.skippedOffline');
  if (r.startsWith('error: ')) return t('schedules.resultError', { error: r.slice(7) });
  return r;
}

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
      toast(t('schedules.saved'));
      setEdit(null);
      reload(true);
    } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(false); }
  };

  const run = async (s: Schedule) => {
    try { const r = await post<{ result: string }>(`${base}/${s.id}/run`); toast(t('schedules.ran', { name: s.name, result: resultLabel(r.result) }), r.result === 'OK' ? 'ok' : 'info'); reload(true); } catch (e: any) { toast(e.message, 'err'); }
  };
  const remove = async (s: Schedule) => {
    if (!(await confirm({ title: t('files.deleteOne', { name: s.name }), confirm: t('common.delete'), danger: true }))) return;
    try { await del(`${base}/${s.id}`); reload(true); } catch (e: any) { toast(e.message, 'err'); }
  };

  return (
    <>
      <Card padded={false} title={<>{t('schedules.title')} {data && <span className="ml-2 text-xs font-normal text-ink-400">{t('schedules.timezone', { tz: data.timezone })}</span>}</>}
        actions={<Button size="sm" variant="primary" icon={<Plus className="h-3.5 w-3.5" />} onClick={() => setEdit({ ...blank })}>{t('schedules.new')}</Button>}>
        {error ? <div className="p-4"><ErrorBox error={error} /></div> : loading && !data ? <Loading /> : !data?.schedules.length ? (
          <Empty icon={<CalendarClock className="h-6 w-6" />} title={t('schedules.empty')}>{t('schedules.emptyHint')}</Empty>
        ) : (
          <ul className="divide-y divide-ink-800">
            {data.schedules.map((s) => (
              <li key={s.id} className="flex flex-wrap items-center gap-4 px-5 py-3.5">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">{s.name}</span>
                    <Badge color="violet">{actions[s.action]}</Badge>
                    {!s.enabled && <Badge>{t('schedules.disabled')}</Badge>}
                    {s.onlyOnline && <Badge color="blue">{t('schedules.ifOnline')}</Badge>}
                  </div>
                  <p className="mt-0.5 text-xs text-ink-400">
                    <code className="rounded bg-ink-800 px-1.5 py-0.5 font-mono text-ink-300">{s.cron}</code>
                    {s.payload && <> · <span className="font-mono">{s.payload}</span></>}
                    {s.enabled && <> · <span title={dateTime(s.nextRun)}>{t('schedules.next', { when: ago(s.nextRun) })}</span></>}
                    {s.lastRun > 0 && <> · {t('schedules.last', { when: ago(s.lastRun), result: resultLabel(s.lastResult) })}</>}
                  </p>
                </div>
                <div className="flex items-center gap-1">
                  <IconButton title={t('schedules.runNow')} onClick={() => run(s)}><Play className="h-4 w-4" /></IconButton>
                  <IconButton title={t('common.edit')} onClick={() => setEdit(s)}><Pencil className="h-4 w-4" /></IconButton>
                  <IconButton title={t('common.delete')} className="hover:!text-red-300" onClick={() => remove(s)}><Trash2 className="h-4 w-4" /></IconButton>
                </div>
              </li>
            ))}
          </ul>
        )}
      </Card>

      <Modal open={!!edit} onClose={() => setEdit(null)} title={edit?.id ? t('schedules.edit') : t('schedules.new')}
        footer={<><Button variant="ghost" onClick={() => setEdit(null)}>{t('common.cancel')}</Button><Button variant="primary" loading={busy} onClick={save}>{t('common.save')}</Button></>}>
        {edit && (
          <div className="space-y-4">
            <Field label={t('common.name')}><input className="input" value={edit.name} onChange={(e) => setEdit({ ...edit, name: e.target.value })} placeholder={t('schedules.namePlaceholder')} /></Field>
            <Field label={t('schedules.action')}>
              <select className="input" value={edit.action} onChange={(e) => setEdit({ ...edit, action: e.target.value })}>
                {Object.entries(actions).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
              </select>
            </Field>
            {edit.action === 'command' && <Field label={t('schedules.command')}><input className="input font-mono" value={edit.payload} onChange={(e) => setEdit({ ...edit, payload: e.target.value })} placeholder={t('schedules.commandPlaceholder')} /></Field>}
            {edit.action === 'backup' && <Field label={t('schedules.backupName')} hint={t('schedules.backupHint')}><input className="input" value={edit.payload} onChange={(e) => setEdit({ ...edit, payload: e.target.value })} /></Field>}
            <Field label={t('schedules.cron')} hint={t('schedules.cronHint')}>
              <input className="input font-mono" value={edit.cron} onChange={(e) => setEdit({ ...edit, cron: e.target.value })} />
              <div className="mt-2 flex flex-wrap gap-1.5">
                {presets.map((p) => <button key={p.cron} type="button" onClick={() => setEdit({ ...edit, cron: p.cron })} className="rounded-lg border border-ink-600 px-2 py-1 text-xs text-ink-300 hover:border-brand-500 hover:text-ink-100">{p.label}</button>)}
              </div>
            </Field>
            <div className="flex flex-col gap-3">
              <Toggle checked={!!edit.enabled} onChange={(v) => setEdit({ ...edit, enabled: v })} label={t('schedules.enabled')} />
              <Toggle checked={!!edit.onlyOnline} onChange={(v) => setEdit({ ...edit, onlyOnline: v })} label={t('schedules.onlyOnline')} />
            </div>
          </div>
        )}
      </Modal>
    </>
  );
}
