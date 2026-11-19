import clsx from 'clsx';
import { Activity, ArrowLeft, CalendarClock, Copy, Folder, History, Network as NetIcon, Settings, SquareTerminal, Users, Wrench } from 'lucide-react';
import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react';
import { Link, NavLink, Outlet, useParams } from 'react-router-dom';
import { get, Server, State } from '../../api';
import PowerButtons from '../../components/PowerButtons';
import { ErrorBox, Loading, StatusPill, useUI } from '../../components/ui';
import { copy, duration } from '../../lib/format';
import TemplateIcon from '../../components/TemplateIcon';

type LineListener = (line: string | null) => void; // null = reset (history reloaded)

export class ConsoleBus {
  lines: string[] = [];
  private listeners = new Set<LineListener>();
  push(line: string) {
    this.lines.push(line);
    if (this.lines.length > 3000) this.lines.splice(0, this.lines.length - 3000);
    this.listeners.forEach((l) => l(line));
  }
  reset(lines: string[]) {
    this.lines = lines.slice(-3000);
    this.listeners.forEach((l) => l(null));
  }
  on(l: LineListener) {
    this.listeners.add(l);
    return () => { this.listeners.delete(l); };
  }
}

type Ctx = {
  server: Server;
  reload: () => Promise<void>;
  state: State;
  disk: number;
  bus: ConsoleBus;
  connected: boolean;
  send: (type: 'command' | 'power', data: string) => boolean;
  can: (p: string) => boolean;
};
const ServerCtx = createContext<Ctx>(null!);
export const useServer = () => useContext(ServerCtx);

export default function ServerLayout() {
  const { id } = useParams();
  const { toast } = useUI();
  const [server, setServer] = useState<Server | null>(null);
  const [error, setError] = useState('');
  const [state, setState] = useState<State | null>(null);
  const [disk, setDisk] = useState(0);
  const [connected, setConnected] = useState(false);
  const bus = useRef(new ConsoleBus()).current;
  const ws = useRef<WebSocket | null>(null);
  const [, setTick] = useState(0);

  const reload = useCallback(async () => {
    try {
      const s = await get<Server>(`/servers/${id}`);
      setServer(s);
      setState((prev) => prev ?? s.state);
      setDisk((d) => d || s.diskUsed);
    } catch (e: any) {
      setError(e.message);
    }
  }, [id]);

  useEffect(() => { reload(); }, [reload]);

  // uptime ticker
  useEffect(() => {
    const t = setInterval(() => setTick((x) => x + 1), 1000);
    return () => clearInterval(t);
  }, []);

  const canConsole = !!server?.permissions.includes('console');

  // WebSocket with auto-reconnect
  useEffect(() => {
    if (!server || !canConsole) return;
    let closed = false;
    let retry = 0;
    let timer: ReturnType<typeof setTimeout>;
    const connect = () => {
      const proto = location.protocol === 'https:' ? 'wss' : 'ws';
      const sock = new WebSocket(`${proto}://${location.host}/api/servers/${server.id}/ws`);
      ws.current = sock;
      sock.onopen = () => { retry = 0; setConnected(true); };
      sock.onmessage = (ev) => {
        const m = JSON.parse(ev.data);
        switch (m.type) {
          case 'history': bus.reset(m.lines || []); break;
          case 'line': bus.push(m.data); break;
          case 'status': case 'stats':
            if (m.state) setState(m.state);
            if (m.disk !== undefined) setDisk(m.disk);
            break;
          case 'error': toast(m.data, 'err'); break;
        }
      };
      sock.onclose = () => {
        setConnected(false);
        if (closed) return;
        retry++;
        timer = setTimeout(connect, Math.min(10000, 500 * 2 ** retry));
      };
    };
    connect();
    return () => { closed = true; clearTimeout(timer); ws.current?.close(); };
  }, [server?.id, canConsole, bus, toast]);

  // without console permission, poll the state
  useEffect(() => {
    if (!server || canConsole) return;
    const t = setInterval(async () => {
      try {
        const s = await get<Server>(`/servers/${server.id}`);
        setState(s.state);
        setDisk(s.diskUsed);
      } catch { /* ignore */ }
    }, 5000);
    return () => clearInterval(t);
  }, [server?.id, canConsole]);

  const send = useCallback((type: 'command' | 'power', data: string) => {
    if (ws.current?.readyState !== WebSocket.OPEN) return false;
    ws.current.send(JSON.stringify({ type, data }));
    return true;
  }, []);

  if (error) return <ErrorBox error={error} />;
  if (!server || !state) return <Loading />;

  const can = (p: string) => server.permissions.includes(p);
  const tabs = [
    { to: '', label: 'Console', icon: SquareTerminal, perm: 'console' },
    { to: 'files', label: 'Fichiers', icon: Folder, perm: 'files' },
    { to: 'backups', label: 'Sauvegardes', icon: History, perm: 'backups' },
    { to: 'schedules', label: 'Tâches', icon: CalendarClock, perm: 'schedules' },
    { to: 'network', label: 'Réseau', icon: NetIcon, perm: 'network' },
    { to: 'startup', label: 'Démarrage', icon: Wrench, perm: 'startup' },
    { to: 'users', label: 'Utilisateurs', icon: Users, perm: 'users' },
    { to: 'activity', label: 'Activité', icon: Activity, perm: 'activity' },
    { to: 'settings', label: 'Paramètres', icon: Settings, perm: 'settings' },
  ].filter((t) => can(t.perm));

  const uptime = state.status === 'running' && state.startedAt ? duration(Date.now() / 1000 - state.startedAt) : null;

  return (
    <ServerCtx.Provider value={{ server, reload, state, disk, bus, connected, send, can }}>
      <Link to="/" className="mb-4 inline-flex items-center gap-1.5 text-xs text-ink-400 hover:text-ink-200"><ArrowLeft className="h-3.5 w-3.5" /> Mes serveurs</Link>
      <div className="card mb-5 p-5">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div className="flex min-w-0 items-center gap-4">
            <TemplateIcon slug={server.templateSlug} size="lg" />
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2.5">
                <h1 className="truncate text-xl font-semibold">{server.name}</h1>
                {server.suspended ? <span className="rounded-full bg-red-500/15 px-2 py-0.5 text-[11px] font-semibold text-red-300">Suspendu</span> : <StatusPill status={state.status} />}
              </div>
              <div className="mt-1 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-ink-400">
                <span>{server.template}</span>
                {server.address && (
                  <button onClick={async () => { await copy(server.address); toast('Adresse copiée'); }} className="inline-flex items-center gap-1 font-mono hover:text-brand-300">
                    <Copy className="h-3 w-3" /> {server.address}
                  </button>
                )}
                {uptime && <span>En ligne depuis {uptime}</span>}
                <span className="font-mono text-ink-500">#{server.id}</span>
              </div>
            </div>
          </div>
          {can('power') && !server.suspended && <PowerButtons id={server.id} status={state.status} />}
        </div>
        {state.reason && state.status !== 'running' && (
          <p className={clsx('mt-4 rounded-xl px-3 py-2 text-xs', state.status === 'crashed' ? 'bg-red-500/10 text-red-300' : 'bg-amber-500/10 text-amber-200')}>{state.reason}</p>
        )}
        {state.installError && state.status === 'offline' && (
          <p className="mt-4 rounded-xl bg-red-500/10 px-3 py-2 text-xs text-red-300">La dernière installation a échoué. Consulte la console puis réinstalle depuis Paramètres.</p>
        )}
      </div>
      <nav className="mb-5 flex gap-1 overflow-x-auto border-b border-ink-700">
        {tabs.map((t) => (
          <NavLink key={t.to} to={t.to} end={t.to === ''}
            className={({ isActive }) => clsx('flex shrink-0 items-center gap-2 border-b-2 px-3.5 py-2.5 text-sm font-medium transition',
              isActive ? 'border-brand-400 text-ink-100' : 'border-transparent text-ink-400 hover:text-ink-200')}>
            <t.icon className="h-4 w-4" /> {t.label}
          </NavLink>
        ))}
      </nav>
      <div className="animate-fade-in"><Outlet /></div>
    </ServerCtx.Provider>
  );
}
