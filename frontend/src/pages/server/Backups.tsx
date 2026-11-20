import { Download, History, Lock, LockOpen, Plus, RotateCcw, Trash2 } from 'lucide-react';
import { useEffect, useState } from 'react';
import { Backup, del, get, post } from '../../api';
import { Badge, Button, Card, Empty, ErrorBox, Field, IconButton, Loading, Modal, Spinner, Toggle, useLoad, useUI } from '../../components/ui';
import { ago, bytes, dateTime } from '../../lib/format';
import { useServer } from './ServerLayout';
import { t } from '../../i18n';

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
      toast(t('backups.started'));
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
      toast(t('backups.restored'));
      setRestore(null);
    } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(false); }
  };

  const remove = async (b: Backup) => {
    if (!(await confirm({ title: t('files.deleteOne', { name: b.name }), body: t('backups.deleteBody'), confirm: t('common.delete'), danger: true }))) return;
    try { await del(`${base}/${b.id}`); toast(t('backups.deleted')); reload(true); } catch (e: any) { toast(e.message, 'err'); }
  };

  const lock = async (b: Backup) => {
    try { await post(`${base}/${b.id}/lock`); reload(true); } catch (e: any) { toast(e.message, 'err'); }
  };

  const used = data?.backups.length || 0;
  return (
    <>
      <Card padded={false} title={<>{t('tab.backups')} <span className="ml-2 text-xs font-normal text-ink-400">{used} / {data?.limit ?? server.backupLimit}</span></>}
        actions={<Button size="sm" variant="primary" icon={<Plus className="h-3.5 w-3.5" />} disabled={!data || used >= data.limit || pending} onClick={() => setCreating(true)}>{t('backups.new')}</Button>}>
        {error ? <div className="p-4"><ErrorBox error={error} /></div> : loading && !data ? <Loading /> : !data?.backups.length ? (
          <Empty icon={<History className="h-6 w-6" />} title={t('backups.empty')}>{server.backupLimit > 0 ? t('backups.emptyHint') : t('backups.disabled')}</Empty>
        ) : (
          <ul className="divide-y divide-ink-800">
            {data.backups.map((b) => (
              <li key={b.id} className="flex flex-wrap items-center gap-4 px-5 py-3.5">
                <div className="flex h-9 w-9 items-center justify-center rounded-md border border-ink-600 bg-ink-800 text-ink-300">{b.status === 'pending' ? <Spinner className="h-4 w-4" /> : <History className="h-4 w-4" />}</div>
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">{b.name}</span>
                    {b.locked && <Badge color="violet"><Lock className="h-3 w-3" /> {t('backups.locked')}</Badge>}
                    {b.status === 'pending' && <Badge color="blue">{t('backups.pending')}</Badge>}
                    {b.status === 'failed' && <Badge color="red">{t('backups.failed')}</Badge>}
                  </div>
                  <p className="mt-0.5 text-xs text-ink-400" title={dateTime(b.createdAt)}>
                    {ago(b.createdAt)}{b.status === 'completed' && ` · ${bytes(b.size)}`}{b.error && ` · ${b.error}`}
                  </p>
                </div>
                <div className="flex items-center gap-1">
                  {b.status === 'completed' && <>
                    <IconButton title={t('common.download')} onClick={() => { location.href = `/api${base}/${b.id}/download`; }}><Download className="h-4 w-4" /></IconButton>
                    <IconButton title={t('backups.restore')} onClick={() => { setTruncate(false); setRestore(b); }}><RotateCcw className="h-4 w-4" /></IconButton>
                  </>}
                  {b.status !== 'pending' && <IconButton title={b.locked ? t('backups.unlock') : t('backups.lock')} onClick={() => lock(b)}>{b.locked ? <LockOpen className="h-4 w-4" /> : <Lock className="h-4 w-4" />}</IconButton>}
                  {!b.locked && b.status !== 'pending' && <IconButton title={t('common.delete')} className="hover:!text-red-300" onClick={() => remove(b)}><Trash2 className="h-4 w-4" /></IconButton>}
                </div>
              </li>
            ))}
          </ul>
        )}
      </Card>

      <Modal open={creating} onClose={() => setCreating(false)} title={t('backups.new')}
        footer={<><Button variant="ghost" onClick={() => setCreating(false)}>{t('common.cancel')}</Button><Button variant="primary" loading={busy} onClick={create}>{t('backups.launch')}</Button></>}>
        <Field label={t('backups.nameOptional')} hint={t('backups.nameHint')}>
          <input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder={t('backups.namePlaceholder')} autoFocus />
        </Field>
      </Modal>

      <Modal open={!!restore} onClose={() => setRestore(null)} title={t('backups.restoreTitle', { name: restore?.name ?? '' })}
        footer={<><Button variant="ghost" onClick={() => setRestore(null)}>{t('common.cancel')}</Button><Button variant="danger" loading={busy} disabled={state.status !== 'offline'} onClick={doRestore}>{t('backups.restore')}</Button></>}>
        <div className="space-y-4 text-sm text-ink-300">
          {state.status !== 'offline' && <ErrorBox error={t('backups.stopFirst')} />}
          <p>{t('backups.restoreBody')}</p>
          <Toggle checked={truncate} onChange={setTruncate} label={t('backups.truncate')} />
        </div>
      </Modal>
    </>
  );
}
