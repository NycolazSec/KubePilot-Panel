import { Copy, Database, KeyRound, Plus, Trash2, TriangleAlert } from 'lucide-react';
import { useState } from 'react';
import { del, get, post } from '../../api';
import { Badge, Button, Card, Empty, ErrorBox, Field, IconButton, Loading, Modal, useLoad, useUI } from '../../components/ui';
import { t } from '../../i18n';
import { ago, copy } from '../../lib/format';
import { useServer } from './ServerLayout';

type DB = { id: number; name: string; username: string; createdAt: number };
type Info = { enabled: boolean; host: string; port: number; limit: number; databases: DB[] };
type Secret = { db: DB; password: string };

export default function Databases() {
  const { server } = useServer();
  const { toast, confirm } = useUI();
  const base = `/servers/${server.id}/databases`;
  const { data, error, reload } = useLoad(() => get<Info>(base), [server.id]);
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState('');
  const [busy, setBusy] = useState('');
  const [secret, setSecret] = useState<Secret | null>(null);

  const create = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy('create');
    try {
      const r = await post<{ database: DB; password: string }>(base, { name });
      setCreating(false);
      setName('');
      setSecret({ db: r.database, password: r.password });
      reload(true);
    } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(''); }
  };
  const rotate = async (d: DB) => {
    if (!(await confirm({ title: t('db.rotateTitle', { name: d.name }), body: t('db.rotateBody'), confirm: t('db.rotate'), danger: true }))) return;
    setBusy('r' + d.id);
    try { const r = await post<{ password: string }>(`${base}/${d.id}/rotate`); setSecret({ db: d, password: r.password }); } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(''); }
  };
  const remove = async (d: DB) => {
    if (!(await confirm({ title: t('db.deleteTitle', { name: d.name }), body: t('db.deleteBody'), confirm: t('common.delete'), danger: true }))) return;
    setBusy('d' + d.id);
    try { await del(`${base}/${d.id}`); toast(t('db.deleted')); reload(true); } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(''); }
  };

  if (error) return <ErrorBox error={error} />;
  if (!data) return <Loading />;
  if (!data.enabled) return <Card><Empty icon={<Database />} title={t('db.disabled')}>{t('db.disabledHint')}</Empty></Card>;
  const full = data.databases.length >= data.limit;
  const addr = `${data.host}:${data.port}`;

  const CopyRow = ({ label, value, secretValue = false }: { label: string; value: string; secretValue?: boolean }) => (
    <div className="flex items-center justify-between gap-3 border-b border-ink-800 py-2 last:border-0">
      <span className="shrink-0 text-sm text-ink-400">{label}</span>
      <button type="button" onClick={async () => { await copy(value); toast(t('twofa.copied')); }}
        className={`inline-flex min-w-0 items-center gap-2 rounded px-1.5 py-0.5 font-mono text-sm hover:bg-ink-800 ${secretValue ? 'text-amber-200' : 'text-ink-100'}`}>
        <span className="truncate">{value}</span><Copy className="h-3.5 w-3.5 shrink-0 text-ink-500" />
      </button>
    </div>
  );

  return (
    <div className="space-y-5">
      <Card padded={false} title={<>{t('db.title')} <span className="ml-1 text-xs font-normal text-ink-400">{data.databases.length} / {data.limit}</span></>}
        description={t('db.description')}
        actions={<Button size="sm" variant="primary" icon={<Plus />} disabled={full} title={full ? t('db.limitReached') : undefined} onClick={() => setCreating(true)}>{t('db.new')}</Button>}>
        {!data.databases.length ? (
          <Empty icon={<Database />} title={t('db.empty')}>{data.limit ? t('db.emptyHint') : t('db.noLimit')}</Empty>
        ) : (
          <ul className="divide-y divide-ink-800">
            {data.databases.map((d) => (
              <li key={d.id} className="flex flex-wrap items-center gap-4 px-5 py-3.5">
                <Database className="h-5 w-5 shrink-0 text-ink-400" />
                <div className="min-w-0 flex-1">
                  <p className="truncate font-mono text-sm text-ink-100">{d.name}</p>
                  <p className="mt-0.5 text-xs text-ink-400">{t('db.user')} <code className="font-mono">{d.username}</code> · {ago(d.createdAt)}</p>
                </div>
                <Button size="sm" icon={<KeyRound />} loading={busy === 'r' + d.id} disabled={!!busy} onClick={() => rotate(d)}>{t('db.rotate')}</Button>
                <IconButton title={t('common.delete')} className="hover:!text-red-300" disabled={!!busy} onClick={() => remove(d)}><Trash2 /></IconButton>
              </li>
            ))}
          </ul>
        )}
        <div className="flex flex-wrap items-center gap-2 border-t border-ink-700 px-5 py-3 text-xs text-ink-400">
          <Badge color="violet">MariaDB</Badge>
          <span>{t('db.host')}</span>
          <button type="button" className="font-mono text-ink-200 hover:underline" onClick={async () => { await copy(addr); toast(t('twofa.copied')); }}>{addr}</button>
          <span className="text-ink-500">· {t('db.internalOnly')}</span>
        </div>
      </Card>

      <Modal open={creating} onClose={() => setCreating(false)} title={t('db.new')}
        footer={<><Button variant="ghost" onClick={() => setCreating(false)}>{t('common.cancel')}</Button><Button type="submit" form="db-form" variant="primary" loading={busy === 'create'}>{t('db.create')}</Button></>}>
        <form id="db-form" onSubmit={create}>
          <Field label={t('common.name')} hint={t('db.nameHint', { prefix: `s${server.id}_` })}>
            <div className="flex items-center gap-1">
              <span className="font-mono text-sm text-ink-500">s{server.id}_</span>
              <input className="input font-mono" value={name} maxLength={32} pattern="[a-z0-9_]{1,32}" onChange={(e) => setName(e.target.value.toLowerCase())} placeholder="luckperms" autoFocus required />
            </div>
          </Field>
        </form>
      </Modal>

      <Modal open={!!secret} onClose={() => setSecret(null)} wide title={t('db.credentials', { name: secret?.db.name ?? '' })}
        footer={<Button variant="primary" onClick={() => setSecret(null)}>{t('twofa.saved')}</Button>}>
        {secret && (
          <div className="space-y-4">
            <div className="flex items-start gap-2 rounded-md border border-amber-500/30 bg-amber-500/[.07] px-3 py-2.5 text-[13px] text-amber-200">
              <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0 text-amber-400" />{t('db.copyNow')}
            </div>
            <div className="rounded-md border border-ink-700 px-3">
              <CopyRow label={t('db.host')} value={data.host} />
              <CopyRow label={t('sftp.port')} value={String(data.port)} />
              <CopyRow label={t('db.database')} value={secret.db.name} />
              <CopyRow label={t('db.user')} value={secret.db.username} />
              <CopyRow label={t('db.password')} value={secret.password} secretValue />
            </div>
            <div>
              <span className="label">{t('db.urls')}</span>
              <div className="rounded-md border border-ink-700 px-3">
                <CopyRow label="JDBC" value={`jdbc:mariadb://${addr}/${secret.db.name}`} />
                <CopyRow label="URL" value={`mysql://${secret.db.username}:${secret.password}@${addr}/${secret.db.name}`} secretValue />
              </div>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}
