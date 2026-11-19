import clsx from 'clsx';
import { Box, Copy, Cpu, HardDrive, MemoryStick, Search, Server as ServerIcon } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { get, Server } from '../api';
import PowerButtons from '../components/PowerButtons';
import TemplateIcon from '../components/TemplateIcon';
import { Bar, Empty, ErrorBox, Loading, PageHeader, StatusPill, Tabs, Toggle, useLoad, useUI } from '../components/ui';
import { bytes, copy, cpuPct, mb } from '../lib/format';
import { useAuth } from '../main';

function ServerCard({ s, onChange }: { s: Server; onChange: () => void }) {
  const { toast } = useUI();
  const st = s.state;
  const memPct = s.memory ? (st.memory / (s.memory * 1048576)) * 100 : 0;
  return (
    <div className="card group flex flex-col overflow-hidden transition hover:border-brand-500/40 hover:shadow-glow">
      <Link to={`/server/${s.id}`} className="flex-1 p-5">
        <div className="flex items-start justify-between gap-3">
          <div className="flex min-w-0 items-center gap-3">
            <TemplateIcon slug={s.templateSlug} />
            <div className="min-w-0">
              <h3 className="truncate font-semibold text-ink-100 group-hover:text-white">{s.name}</h3>
              <p className="truncate text-xs text-ink-400">{s.template}{s.owner && ` · ${s.owner}`}</p>
            </div>
          </div>
          {s.suspended ? <span className="rounded-full bg-red-500/15 px-2 py-0.5 text-[11px] font-semibold text-red-300">Suspendu</span> : <StatusPill status={st.status} />}
        </div>
        <div className="mt-5 grid grid-cols-3 gap-3 text-xs">
          <div>
            <div className="mb-1 flex items-center gap-1 text-ink-400"><Cpu className="h-3.5 w-3.5" /> CPU</div>
            <div className="font-mono text-ink-200">{st.status === 'offline' ? '—' : `${cpuPct(st.cpu, s.cpu).toFixed(0)}%`}</div>
            <Bar className="mt-1.5" value={st.cpu} max={s.cpu || 1000} />
          </div>
          <div>
            <div className="mb-1 flex items-center gap-1 text-ink-400"><MemoryStick className="h-3.5 w-3.5" /> RAM</div>
            <div className="font-mono text-ink-200">{st.status === 'offline' ? '—' : `${memPct.toFixed(0)}%`}</div>
            <Bar className="mt-1.5" value={st.memory} max={s.memory * 1048576} />
          </div>
          <div>
            <div className="mb-1 flex items-center gap-1 text-ink-400"><HardDrive className="h-3.5 w-3.5" /> Disque</div>
            <div className="truncate font-mono text-ink-200">{bytes(s.diskUsed, 0)}</div>
            <Bar className="mt-1.5" value={s.diskUsed} max={s.disk * 1048576} />
          </div>
        </div>
      </Link>
      <div className="flex items-center justify-between gap-2 border-t border-ink-700 bg-ink-900/40 px-5 py-3">
        {s.address ? (
          <button onClick={async () => { await copy(s.address); toast('Adresse copiée'); }} className="flex min-w-0 items-center gap-1.5 font-mono text-xs text-ink-300 hover:text-brand-300" title="Copier l'adresse">
            <Copy className="h-3.5 w-3.5 shrink-0" /><span className="truncate">{s.address}</span>
          </button>
        ) : <span className="text-xs text-ink-500">Pas de port public</span>}
        <span className="text-[11px] text-ink-500">{mb(s.memory)} · {s.cpu ? `${s.cpu / 1000} vCPU` : '∞ CPU'}</span>
      </div>
      {s.permissions.includes('power') && !s.suspended && (
        <div className="hidden border-t border-ink-700 px-5 py-3 group-hover:block"><PowerButtons id={s.id} status={st.status} size="sm" onDone={onChange} /></div>
      )}
    </div>
  );
}

export default function Dashboard() {
  const { me } = useAuth();
  const [all, setAll] = useState(false);
  const [q, setQ] = useState('');
  const [filter, setFilter] = useState<'all' | 'online' | 'offline'>('all');
  const { data, error, loading, reload } = useLoad(() => get<Server[]>(`/servers${all ? '?all=1' : ''}`), [all]);

  useEffect(() => {
    const t = setInterval(() => reload(true), 5000);
    return () => clearInterval(t);
  }, [reload]);

  const list = useMemo(() => (data || []).filter((s) => {
    if (q && !`${s.name} ${s.template} ${s.address} ${s.owner}`.toLowerCase().includes(q.toLowerCase())) return false;
    if (filter === 'online') return s.state.status === 'running' || s.state.status === 'starting';
    if (filter === 'offline') return !(s.state.status === 'running' || s.state.status === 'starting');
    return true;
  }), [data, q, filter]);

  const online = (data || []).filter((s) => s.state.status === 'running').length;

  return (
    <>
      <PageHeader
        title={<>Bonjour, <span className="text-gradient">{me?.user.username}</span></>}
        subtitle={data ? `${data.length} serveur${data.length > 1 ? 's' : ''} · ${online} en ligne` : ' '}
        actions={me?.user.isAdmin && <Toggle checked={all} onChange={setAll} label="Voir tous les serveurs" />}
      />
      <div className="mb-5 flex flex-wrap items-center gap-3">
        <div className="relative min-w-[220px] flex-1 sm:max-w-sm">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-ink-400" />
          <input className="input pl-9" placeholder="Rechercher un serveur…" value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        <Tabs value={filter} onChange={setFilter} items={[{ id: 'all', label: 'Tous' }, { id: 'online', label: 'En ligne' }, { id: 'offline', label: 'Hors ligne' }]} />
      </div>
      {error && <ErrorBox error={error} />}
      {loading && !data ? <Loading /> : list.length === 0 ? (
        <div className="card"><Empty icon={<ServerIcon className="h-6 w-6" />} title={data?.length ? 'Aucun résultat' : 'Aucun serveur pour le moment'}>
          {data?.length ? 'Essaie une autre recherche.' : me?.user.isAdmin ? <>Crée ton premier serveur depuis <Link to="/admin/servers" className="text-brand-300 hover:underline">l'administration</Link>.</> : 'Un administrateur doit t\'en attribuer un.'}
        </Empty></div>
      ) : (
        <div className={clsx('grid gap-4', 'sm:grid-cols-2 xl:grid-cols-3')}>
          {list.map((s) => <ServerCard key={s.id} s={s} onChange={() => reload(true)} />)}
        </div>
      )}
      {!loading && data && data.length > 0 && (
        <p className="mt-6 flex items-center gap-2 text-xs text-ink-500"><Box className="h-3.5 w-3.5" /> Chaque serveur tourne dans son propre pod Kubernetes, isolé du reste du cluster.</p>
      )}
    </>
  );
}
