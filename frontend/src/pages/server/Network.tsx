import { Copy, Network as NetIcon, Plus, Star, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { Allocation, del, get, post, put } from '../../api';
import { Badge, Button, Card, Empty, ErrorBox, IconButton, Loading, useLoad, useUI } from '../../components/ui';
import { copy } from '../../lib/format';
import { useServer } from './ServerLayout';
import { t } from '../../i18n';

export default function Network() {
  const { server, reload: reloadServer } = useServer();
  const { toast, confirm } = useUI();
  const base = `/servers/${server.id}/network`;
  const { data, error, loading, reload } = useLoad(() => get<{ host: string; allocations: Allocation[]; limit: number }>(base), [server.id]);
  const [proto, setProto] = useState('tcp');
  const [busy, setBusy] = useState(false);

  const add = async () => {
    setBusy(true);
    try { const a = await post<Allocation>(base, { protocol: proto }); toast(t('network.added', { port: a.port })); reload(true); } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(false); }
  };
  const remove = async (a: Allocation) => {
    if (!(await confirm({ title: t('network.removeTitle', { port: a.port }), confirm: t('network.remove'), danger: true }))) return;
    try { await del(`${base}/${a.port}`); reload(true); } catch (e: any) { toast(e.message, 'err'); }
  };
  const primary = async (a: Allocation) => {
    try { await put(`${base}/${a.port}`, { primary: true }); toast(t('network.primaryChanged'), 'info'); reload(true); reloadServer(); } catch (e: any) { toast(e.message, 'err'); }
  };
  const notes = async (a: Allocation, v: string) => {
    if (v === a.notes) return;
    try { await put(`${base}/${a.port}`, { notes: v }); } catch (e: any) { toast(e.message, 'err'); }
  };

  const extra = data ? data.allocations.filter((a) => !a.isPrimary).length : 0;
  return (
    <Card padded={false} title={t('network.ports')}
      actions={data && (data.limit > 0 || server.permissions.includes('settings')) && (
        <div className="flex items-center gap-2">
          <select className="input !w-auto !py-1.5 text-xs" value={proto} onChange={(e) => setProto(e.target.value)}>
            <option value="tcp">TCP</option><option value="udp">UDP</option><option value="tcp+udp">TCP + UDP</option>
          </select>
          <Button size="sm" variant="primary" icon={<Plus className="h-3.5 w-3.5" />} loading={busy} onClick={add}>{t('network.add')}</Button>
        </div>
      )}>
      {error ? <div className="p-4"><ErrorBox error={error} /></div> : loading && !data ? <Loading /> : !data?.allocations.length ? (
        <Empty icon={<NetIcon className="h-6 w-6" />} title={t('network.empty')}>{t('network.emptyHint')}</Empty>
      ) : (
        <>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead><tr className="border-b border-ink-700">
                <th className="th">{t('network.publicAddress')}</th><th className="th">{t('network.protocol')}</th><th className="th">{t('network.internalPort')}</th><th className="th">{t('network.note')}</th><th className="w-28" />
              </tr></thead>
              <tbody>
                {data.allocations.map((a) => (
                  <tr key={a.port} className="border-b border-ink-800">
                    <td className="px-4 py-3">
                      <button onClick={async () => { await copy(`${data.host}:${a.port}`); toast(t('common.addressCopied')); }} className="inline-flex items-center gap-2 font-mono text-ink-100 hover:text-brand-300">
                        <Copy className="h-3.5 w-3.5 text-ink-400" />{data.host}:{a.port}
                      </button>
                      {a.isPrimary && <Badge color="violet"><Star className="h-3 w-3" /> {t('network.primary')}</Badge>}
                    </td>
                    <td className="px-4 py-3 uppercase text-ink-300">{a.protocol}</td>
                    <td className="px-4 py-3 font-mono text-ink-400">{a.targetPort}</td>
                    <td className="px-4 py-2"><input className="input !py-1.5 text-xs" defaultValue={a.notes} placeholder={t('network.notePlaceholder')} onBlur={(e) => notes(a, e.target.value)} /></td>
                    <td className="px-4 py-2">
                      <div className="flex justify-end gap-1">
                        {!a.isPrimary && <IconButton title={t('network.setPrimary')} onClick={() => primary(a)}><Star className="h-4 w-4" /></IconButton>}
                        {!a.isPrimary && <IconButton title={t('network.remove')} className="hover:!text-red-300" onClick={() => remove(a)}><Trash2 className="h-4 w-4" /></IconButton>}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className="px-5 py-3 text-xs text-ink-400">
            {t('network.footer', { used: extra, limit: data.limit })}
          </p>
        </>
      )}
    </Card>
  );
}
