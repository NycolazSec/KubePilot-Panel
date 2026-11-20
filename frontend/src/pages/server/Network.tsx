import { Copy, Network as NetIcon, Plus, Star, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { Allocation, del, get, post, put } from '../../api';
import { Badge, Button, Card, Empty, ErrorBox, IconButton, Loading, Toggle, useLoad, useUI } from '../../components/ui';
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
    <div className="space-y-5">
    {server.routerDomain && <HostnameCard />}
    {server.internalAddress && (
      <Card title={t('network.internal')} description={t('network.internalHint')}
        actions={<Toggle checked={!server.internalOnly} label={t('network.public')} onChange={async (pub) => {
          try { await put(`/servers/${server.id}/exposure`, { internalOnly: !pub }); toast(t('network.exposureSaved')); reloadServer(); reload(true); } catch (e: any) { toast(e.message, 'err'); }
        }} />}>
        {server.internalOnly && <p className="mb-3 text-[13px] text-amber-300">{t('network.internalOnlyOn')}</p>}
        <button onClick={async () => { await copy(server.internalAddress); toast(t('common.addressCopied')); }}
          className="inline-flex max-w-full items-center gap-2 break-all rounded-md border border-ink-700 bg-ink-950 px-3 py-1.5 text-left font-mono text-sm text-ink-200 hover:border-ink-500">
          <Copy className="h-3.5 w-3.5 shrink-0 text-ink-400" />{server.internalAddress}
        </button>
      </Card>
    )}
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
    </div>
  );
}

// Minecraft router: <hostname>.<domain> on port 25565
function HostnameCard() {
  const { server, reload } = useServer();
  const { toast } = useUI();
  const [label, setLabel] = useState(server.hostname);
  const [busy, setBusy] = useState(false);
  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      const r = await put<{ address: string }>(`/servers/${server.id}/hostname`, { hostname: label.trim().toLowerCase() });
      toast(r.address ? t('router.saved', { address: r.address }) : t('common.saved'));
      reload();
    } catch (err: any) { toast(err.message, 'err'); } finally { setBusy(false); }
  };
  const routed = server.hostname ? `${server.hostname}.${server.routerDomain}` : '';
  return (
    <Card title={t('router.title')} description={t('router.description')}>
      <div className="mb-4 flex flex-wrap items-center gap-3 text-sm">
        {routed ? <>
          <span className="text-ink-400">{t('router.current')}</span>
          <button onClick={async () => { await copy(routed); toast(t('common.addressCopied')); }} className="inline-flex items-center gap-2 rounded-md border border-brand-500/40 bg-brand-500/10 px-3 py-1.5 font-mono text-brand-200 hover:border-brand-400">
            <Copy className="h-3.5 w-3.5" />{routed}
          </button>
          <span className="text-xs text-ink-500">{t('router.fallback', { address: server.nodeAddress })}</span>
        </> : <span className="text-ink-400">{t('router.none', { address: server.nodeAddress })}</span>}
      </div>
      <form onSubmit={save} className="flex flex-wrap items-center gap-2">
        <div className="flex min-w-0 flex-1 items-center overflow-hidden rounded-md border border-ink-600 bg-ink-900 focus-within:border-brand-500">
          <input className="min-w-0 flex-1 bg-transparent px-3 py-2 font-mono text-sm text-ink-100 outline-none" value={label} maxLength={32}
            onChange={(e) => setLabel(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, ''))} placeholder="survie" />
          <span className="shrink-0 border-l border-ink-700 bg-ink-850 px-3 py-2 font-mono text-sm text-ink-400">.{server.routerDomain}</span>
        </div>
        <Button type="submit" variant="primary" loading={busy} disabled={label === server.hostname}>{t('router.save')}</Button>
      </form>
      <p className="mt-2 text-xs text-ink-400">{t('router.hint')}</p>
    </Card>
  );
}
