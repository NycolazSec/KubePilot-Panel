import { Copy, Network as NetIcon, Plus, Star, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { Allocation, del, get, post, put } from '../../api';
import { Badge, Button, Card, Empty, ErrorBox, IconButton, Loading, useLoad, useUI } from '../../components/ui';
import { copy } from '../../lib/format';
import { useServer } from './ServerLayout';

export default function Network() {
  const { server, reload: reloadServer } = useServer();
  const { toast, confirm } = useUI();
  const base = `/servers/${server.id}/network`;
  const { data, error, loading, reload } = useLoad(() => get<{ host: string; allocations: Allocation[]; limit: number }>(base), [server.id]);
  const [proto, setProto] = useState('tcp');
  const [busy, setBusy] = useState(false);

  const add = async () => {
    setBusy(true);
    try { const a = await post<Allocation>(base, { protocol: proto }); toast(`Port ${a.port} ajouté`); reload(true); } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(false); }
  };
  const remove = async (a: Allocation) => {
    if (!(await confirm({ title: `Retirer le port ${a.port} ?`, confirm: 'Retirer', danger: true }))) return;
    try { await del(`${base}/${a.port}`); reload(true); } catch (e: any) { toast(e.message, 'err'); }
  };
  const primary = async (a: Allocation) => {
    try { await put(`${base}/${a.port}`, { primary: true }); toast('Port principal changé : redémarre le serveur pour l\'appliquer', 'info'); reload(true); reloadServer(); } catch (e: any) { toast(e.message, 'err'); }
  };
  const notes = async (a: Allocation, v: string) => {
    if (v === a.notes) return;
    try { await put(`${base}/${a.port}`, { notes: v }); } catch (e: any) { toast(e.message, 'err'); }
  };

  const extra = data ? data.allocations.filter((a) => !a.isPrimary).length : 0;
  return (
    <Card padded={false} title="Ports"
      actions={data && (data.limit > 0 || server.permissions.includes('settings')) && (
        <div className="flex items-center gap-2">
          <select className="input !w-auto !py-1.5 text-xs" value={proto} onChange={(e) => setProto(e.target.value)}>
            <option value="tcp">TCP</option><option value="udp">UDP</option><option value="tcp+udp">TCP + UDP</option>
          </select>
          <Button size="sm" variant="primary" icon={<Plus className="h-3.5 w-3.5" />} loading={busy} onClick={add}>Ajouter un port</Button>
        </div>
      )}>
      {error ? <div className="p-4"><ErrorBox error={error} /></div> : loading && !data ? <Loading /> : !data?.allocations.length ? (
        <Empty icon={<NetIcon className="h-6 w-6" />} title="Aucun port public">Ce serveur n'est pas joignable depuis Internet.</Empty>
      ) : (
        <>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead><tr className="border-b border-ink-700 text-left text-xs text-ink-400">
                <th className="px-5 py-2 font-medium">Adresse publique</th><th className="px-3 py-2 font-medium">Protocole</th><th className="px-3 py-2 font-medium">Port interne</th><th className="px-3 py-2 font-medium">Note</th><th className="w-28" />
              </tr></thead>
              <tbody>
                {data.allocations.map((a) => (
                  <tr key={a.port} className="border-b border-ink-800">
                    <td className="px-5 py-3">
                      <button onClick={async () => { await copy(`${data.host}:${a.port}`); toast('Adresse copiée'); }} className="inline-flex items-center gap-2 font-mono text-ink-100 hover:text-brand-300">
                        <Copy className="h-3.5 w-3.5 text-ink-400" />{data.host}:{a.port}
                      </button>
                      {a.isPrimary && <Badge color="violet"><Star className="h-3 w-3" /> Principal</Badge>}
                    </td>
                    <td className="px-3 py-3 uppercase text-ink-300">{a.protocol}</td>
                    <td className="px-3 py-3 font-mono text-ink-400">{a.targetPort}</td>
                    <td className="px-3 py-2"><input className="input !py-1.5 text-xs" defaultValue={a.notes} placeholder="ex : Dynmap, voice chat…" onBlur={(e) => notes(a, e.target.value)} /></td>
                    <td className="px-3 py-2">
                      <div className="flex justify-end gap-1">
                        {!a.isPrimary && <IconButton title="Définir comme principal" onClick={() => primary(a)}><Star className="h-4 w-4" /></IconButton>}
                        {!a.isPrimary && <IconButton title="Retirer" className="hover:!text-red-300" onClick={() => remove(a)}><Trash2 className="h-4 w-4" /></IconButton>}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className="px-5 py-3 text-xs text-ink-400">
            Ports supplémentaires : {extra} / {data.limit}. Un port ajouté est exposé avec le même numéro à l'intérieur du conteneur : configure ton plugin pour écouter dessus.
          </p>
        </>
      )}
    </Card>
  );
}
