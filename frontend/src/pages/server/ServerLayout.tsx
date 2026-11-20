import clsx from 'clsx';
import { Activity, CalendarClock, Gamepad2, ChevronRight, Copy, Folder, History, Network as NetIcon, Settings, SquareTerminal, Users, Wrench } from 'lucide-react';
import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react';
import { Link, NavLink, Outlet, useParams } from 'react-router-dom';
import { get, Server, State } from '../../api';
import PowerButtons from '../../components/PowerButtons';
import { Badge, ErrorBox, Loading, StatusPill, useUI } from '../../components/ui';
import { copy, duration } from '../../lib/format';
import TemplateIcon from '../../components/TemplateIcon';
import { lang, t } from '../../i18n';

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
      const sock = new WebSocket(`${proto}://${location.host}/api/servers/${server.id}/ws?lang=${lang}`);
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
    { to: '', label: t('tab.console'), icon: SquareTerminal, perm: 'console' },
    ...(server.features?.some((f) => f.startsWith('minecraft')) ? [{ to: 'players', label: t('tab.players'), icon: Gamepad2, perm: 'console' }] : []),
    { to: 'files', label: t('tab.files'), icon: Folder, perm: 'files' },
    { to: 'backups', label: t('tab.backups'), icon: History, perm: 'backups' },
    { to: 'schedules', label: t('tab.schedules'), icon: CalendarClock, perm: 'schedules' },
    { to: 'network', label: t('tab.network'), icon: NetIcon, perm: 'network' },
    { to: 'startup', label: t('tab.startup'), icon: Wrench, perm: 'startup' },
    { to: 'users', label: t('tab.users'), icon: Users, perm: 'users' },
    { to: 'activity', label: t('tab.activity'), icon: Activity, perm: 'activity' },
    { to: 'settings', label: t('tab.settings'), icon: Settings, perm: 'settings' },
  ].filter((tab) => can(tab.perm));

  const uptime = state.status === 'running' && state.startedAt ? duration(Date.now() / 1000 - state.startedAt) : null;

  return (
    <ServerCtx.Provider value={{ server, reload, state, disk, bus, connected, send, can }}>
      <nav aria-label={t('common.breadcrumb')} className="mb-3 flex items-center gap-1.5 text-xs text-ink-400">
        <Link to="/" className="hover:text-ink-200">{t('nav.servers')}</Link>
        <ChevronRight className="h-3 w-3 text-ink-500" />
        <span className="truncate text-ink-200">{server.name}</span>
      </nav>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-4">
        <div className="flex min-w-0 items-center gap-3.5">
          <TemplateIcon slug={server.templateSlug} size="lg" />
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2.5">
              <h1 className="truncate text-xl font-semibold tracking-tight">{server.name}</h1>
              {server.suspended ? <Badge color="red">{t('server.suspended')}</Badge> : <StatusPill status={state.status} />}
            </div>
            <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-ink-400">
              <span>{server.template}</span>
              {server.address && <>
                <span className="text-ink-600">•</span>
                <button onClick={async () => { await copy(server.address); toast(t('common.addressCopied')); }} className="inline-flex items-center gap-1 font-mono text-ink-300 hover:text-ink-100" title={t('server.copyAddress')}>
                  {server.address}<Copy className="h-3 w-3" />
                </button>
              </>}
              {uptime && <><span className="text-ink-600">•</span><span className="tabular-nums">{t('server.onlineFor', { duration: uptime })}</span></>}
              <span className="text-ink-600">•</span>
              <span className="font-mono text-ink-500">{server.id}</span>
            </div>
          </div>
        </div>
        {can('power') && !server.suspended && <PowerButtons id={server.id} status={state.status} />}
      </div>
      {state.reason && state.status !== 'running' && (
        <div className={clsx('mb-4 rounded-md border px-3 py-2 text-xs', state.status === 'crashed' ? 'border-red-500/30 bg-red-500/[.07] text-red-200' : 'border-amber-500/30 bg-amber-500/[.07] text-amber-200')}>{state.reason}</div>
      )}
      {state.installError && state.status === 'offline' && (
        <div className="mb-4 rounded-md border border-red-500/30 bg-red-500/[.07] px-3 py-2 text-xs text-red-200">{t('server.installFailed')}</div>
      )}
      <nav className="mb-5 flex gap-0.5 overflow-x-auto border-b border-ink-700">
        {tabs.map((tab) => (
          <NavLink key={tab.to} to={tab.to} end={tab.to === ''}
            className={({ isActive }) => clsx('-mb-px flex h-10 shrink-0 items-center gap-2 border-b-2 px-3 text-[13px] font-medium transition-colors',
              isActive ? 'border-brand-500 text-ink-100' : 'border-transparent text-ink-400 hover:text-ink-200')}>
            <tab.icon className="h-4 w-4" /> {tab.label}
          </NavLink>
        ))}
      </nav>
      <div className="animate-fade-in"><Outlet /></div>
    </ServerCtx.Provider>
  );
}
