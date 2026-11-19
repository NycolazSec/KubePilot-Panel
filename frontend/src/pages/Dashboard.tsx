import clsx from 'clsx';
import { ChevronRight, Copy, Play, RotateCw, Search, Server as ServerIcon, Square } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { get, post, Server } from '../api';
import TemplateIcon from '../components/TemplateIcon';
import { Bar, Empty, ErrorBox, IconButton, Loading, PageHeader, StatusPill, Tabs, Toggle, useLoad, useUI } from '../components/ui';
import { bytes, copy, cpuPct, mb } from '../lib/format';
import { useAuth } from '../main';

function Metric({ value, max, label }: { value: number; max: number; label: string }) {
  return (
    <div className="w-28">
      <div className="mb-1 text-xs tabular-nums text-ink-200">{label}</div>
      <Bar value={value} max={max} />
    </div>
  );
}

function Stat({ label, value, sub }: { label: string; value: React.ReactNode; sub?: React.ReactNode }) {
  return (
    <div className="card px-4 py-3">
      <p className="text-2xs font-medium uppercase tracking-wider text-ink-400">{label}</p>
      <p className="mt-1 text-lg font-semibold tabular-nums">{value}</p>
      {sub && <p className="text-xs text-ink-400">{sub}</p>}
    </div>
  );
}

function QuickPower({ s, onDone }: { s: Server; onDone: () => void }) {
  const { toast } = useUI();
  const [busy, setBusy] = useState(false);
  const st = s.state.status;
  const act = async (e: React.MouseEvent, action: string) => {
    e.stopPropagation();
    setBusy(true);
    try { await post(`/servers/${s.id}/power`, { action }); onDone(); } catch (err: any) { toast(err.message, 'err'); } finally { setBusy(false); }
  };
  if (!s.permissions.includes('power') || s.suspended) return null;
  const up = st === 'running' || st === 'starting';
  return (
    <div className="flex items-center gap-0.5">
      {!up && st !== 'installing' && st !== 'stopping' && <IconButton title="Démarrer" disabled={busy} onClick={(e) => act(e, 'start')}><Play /></IconButton>}
      {up && <IconButton title="Redémarrer" disabled={busy} onClick={(e) => act(e, 'restart')}><RotateCw /></IconButton>}
      {up && <IconButton title="Arrêter" disabled={busy} className="hover:!text-red-300" onClick={(e) => act(e, 'stop')}><Square /></IconButton>}
    </div>
  );
}

export default function Dashboard() {
  const { me } = useAuth();
  const nav = useNavigate();
  const { toast } = useUI();
  const [all, setAll] = useState(false);
  const [q, setQ] = useState('');
  const [filter, setFilter] = useState<'all' | 'online' | 'offline'>('all');
  const { data, error, loading, reload } = useLoad(() => get<Server[]>(`/servers${all ? '?all=1' : ''}`), [all]);

  useEffect(() => {
    const t = setInterval(() => reload(true), 5000);
    return () => clearInterval(t);
  }, [reload]);

  const isUp = (s: Server) => s.state.status === 'running' || s.state.status === 'starting';
  const list = useMemo(() => (data || []).filter((s) => {
    if (q && !`${s.name} ${s.template} ${s.address} ${s.owner} ${s.id}`.toLowerCase().includes(q.toLowerCase())) return false;
    if (filter === 'online') return isUp(s);
    if (filter === 'offline') return !isUp(s);
    return true;
  }), [data, q, filter]);

  const servers = data || [];
  const online = servers.filter((s) => s.state.status === 'running').length;
  const memUsed = servers.reduce((n, s) => n + (s.state.memory || 0), 0);
  const memAlloc = servers.reduce((n, s) => n + s.memory, 0);
  const crashed = servers.filter((s) => s.state.status === 'crashed').length;

  return (
    <>
      <PageHeader title="Serveurs" subtitle={all ? 'Tous les serveurs du panel' : 'Les serveurs auxquels tu as accès'}
        actions={me?.user.isAdmin && <Toggle checked={all} onChange={setAll} label="Afficher tous les serveurs" />} />

      {data && (
        <div className="mb-5 grid grid-cols-2 gap-3 lg:grid-cols-4">
          <Stat label="Serveurs" value={servers.length} />
          <Stat label="En ligne" value={<span className="text-emerald-300">{online}</span>} sub={`${servers.length - online} hors ligne`} />
          <Stat label="Mémoire utilisée" value={bytes(memUsed, 1)} sub={`sur ${mb(memAlloc)} allouée`} />
          <Stat label="Incidents" value={<span className={crashed ? 'text-red-300' : ''}>{crashed}</span>} sub={crashed ? 'serveur(s) planté(s)' : 'aucun plantage'} />
        </div>
      )}

      <section className="card">
        <div className="flex flex-wrap items-center gap-2 border-b border-ink-700 p-3">
          <div className="relative min-w-[200px] flex-1 sm:max-w-xs">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-ink-500" />
            <input className="input pl-8" placeholder="Rechercher par nom, adresse, ID…" value={q} onChange={(e) => setQ(e.target.value)} />
          </div>
          <Tabs value={filter} onChange={setFilter} items={[{ id: 'all', label: 'Tous' }, { id: 'online', label: 'En ligne' }, { id: 'offline', label: 'Hors ligne' }]} />
        </div>
        {error ? <div className="p-4"><ErrorBox error={error} /></div> : loading && !data ? <Loading /> : list.length === 0 ? (
          <Empty icon={<ServerIcon />} title={servers.length ? 'Aucun résultat' : 'Aucun serveur'}>
            {servers.length ? 'Modifie la recherche ou le filtre.' : me?.user.isAdmin ? <>Crée un serveur depuis <Link to="/admin/servers" className="text-brand-300 hover:underline">Administration › Serveurs</Link>.</> : 'Un administrateur doit t\'en attribuer un.'}
          </Empty>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead className="border-b border-ink-700">
                <tr>
                  <th className="th">Nom</th>
                  <th className="th">Statut</th>
                  <th className="th hidden md:table-cell">Adresse</th>
                  <th className="th hidden lg:table-cell">CPU</th>
                  <th className="th hidden lg:table-cell">Mémoire</th>
                  <th className="th hidden xl:table-cell">Disque</th>
                  <th className="th w-28" />
                </tr>
              </thead>
              <tbody className="divide-y divide-ink-700/70">
                {list.map((s) => {
                  const st = s.state;
                  const off = st.status === 'offline' || st.status === 'installing';
                  return (
                    <tr key={s.id} onClick={() => nav(`/server/${s.id}`)} className="cursor-pointer transition-colors hover:bg-ink-800/50">
                      <td className="td">
                        <div className="flex items-center gap-3">
                          <TemplateIcon slug={s.templateSlug} />
                          <div className="min-w-0">
                            <p className="truncate font-medium text-ink-100">{s.name}</p>
                            <p className="truncate text-xs text-ink-400">{s.template}{all && s.owner ? ` · ${s.owner}` : ''}</p>
                          </div>
                        </div>
                      </td>
                      <td className="td">{s.suspended ? <span className="text-xs font-medium text-red-300">Suspendu</span> : <StatusPill status={st.status} />}</td>
                      <td className="td hidden md:table-cell">
                        {s.address ? (
                          <button onClick={async (e) => { e.stopPropagation(); await copy(s.address); toast('Adresse copiée'); }} className="group inline-flex items-center gap-1.5 font-mono text-xs text-ink-300 hover:text-ink-100" title="Copier">
                            {s.address}<Copy className="h-3 w-3 opacity-0 group-hover:opacity-100" />
                          </button>
                        ) : <span className="text-xs text-ink-500">—</span>}
                      </td>
                      <td className="td hidden lg:table-cell"><Metric value={off ? 0 : st.cpu} max={s.cpu || 1000} label={off ? '—' : `${cpuPct(st.cpu, s.cpu).toFixed(0)} %`} /></td>
                      <td className="td hidden lg:table-cell"><Metric value={off ? 0 : st.memory} max={s.memory * 1048576} label={off ? `— / ${mb(s.memory)}` : `${bytes(st.memory, 0)} / ${mb(s.memory)}`} /></td>
                      <td className="td hidden xl:table-cell"><Metric value={s.diskUsed} max={s.disk * 1048576} label={`${bytes(s.diskUsed, 0)} / ${mb(s.disk)}`} /></td>
                      <td className="td">
                        <div className="flex items-center justify-end gap-1">
                          <QuickPower s={s} onDone={() => reload(true)} />
                          <ChevronRight className={clsx('h-4 w-4 text-ink-500')} />
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </>
  );
}
