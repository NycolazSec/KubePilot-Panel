import '@xterm/xterm/css/xterm.css';
import { FitAddon } from '@xterm/addon-fit';
import { SearchAddon } from '@xterm/addon-search';
import { WebLinksAddon } from '@xterm/addon-web-links';
import { Terminal } from '@xterm/xterm';
import clsx from 'clsx';
import { ChevronRight, Cpu, Download, Eraser, HardDrive, MemoryStick, Search, Timer } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { IconButton, useUI } from '../../components/ui';
import { bytes, cpuPct, duration } from '../../lib/format';
import { useServer } from './ServerLayout';

function Sparkline({ data, max }: { data: number[]; max: number }) {
  if (data.length < 2) return <div className="h-10" />;
  const w = 200, h = 40;
  const m = Math.max(max, ...data, 1);
  const pts = data.map((v, i) => `${(i / (data.length - 1)) * w},${h - (v / m) * (h - 4) - 2}`).join(' ');
  return (
    <svg viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" className="h-10 w-full">
      <defs>
        <linearGradient id="sg" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stopColor="#8b5cf6" stopOpacity=".35" /><stop offset="1" stopColor="#8b5cf6" stopOpacity="0" /></linearGradient>
      </defs>
      <polygon points={`0,${h} ${pts} ${w},${h}`} fill="url(#sg)" />
      <polyline points={pts} fill="none" stroke="#a78bfa" strokeWidth="1.5" vectorEffect="non-scaling-stroke" />
    </svg>
  );
}

function Stat({ icon, label, value, sub, children }: { icon: React.ReactNode; label: string; value: string; sub?: string; children?: React.ReactNode }) {
  return (
    <div className="card overflow-hidden p-4">
      <div className="flex items-center gap-2 text-xs text-ink-400">{icon}{label}</div>
      <div className="mt-1.5 flex items-baseline gap-2"><span className="font-mono text-lg font-semibold text-ink-100">{value}</span>{sub && <span className="text-xs text-ink-500">{sub}</span>}</div>
      {children && <div className="-mx-4 -mb-4 mt-1">{children}</div>}
    </div>
  );
}

const HISTORY_KEY = 'kubepilot-cmd-history';

export default function Console() {
  const { server, state, disk, bus, connected, send, can } = useServer();
  const { toast } = useUI();
  const box = useRef<HTMLDivElement>(null);
  const term = useRef<Terminal | null>(null);
  const search = useRef<SearchAddon | null>(null);
  const [cmd, setCmd] = useState('');
  const [hist, setHist] = useState<string[]>(() => { try { return JSON.parse(localStorage.getItem(HISTORY_KEY) || '[]'); } catch { return []; } });
  const [hIdx, setHIdx] = useState(-1);
  const [showSearch, setShowSearch] = useState(false);
  const [needle, setNeedle] = useState('');
  const [cpuHist, setCpuHist] = useState<number[]>([]);
  const [memHist, setMemHist] = useState<number[]>([]);

  // terminal
  useEffect(() => {
    const t = new Terminal({
      disableStdin: true, convertEol: true, scrollback: 5000, fontSize: 13, lineHeight: 1.25,
      fontFamily: '"JetBrains Mono", ui-monospace, monospace', cursorStyle: 'underline', cursorBlink: false,
      theme: {
        background: '#00000000', foreground: '#d7dbea', cursor: '#00000000', selectionBackground: '#8b5cf655',
        black: '#1b2030', red: '#f87171', green: '#4ade80', yellow: '#facc15', blue: '#60a5fa', magenta: '#c084fc', cyan: '#22d3ee', white: '#e7e9f2',
        brightBlack: '#646d8c', brightRed: '#fca5a5', brightGreen: '#86efac', brightYellow: '#fde68a', brightBlue: '#93c5fd', brightMagenta: '#d8b4fe', brightCyan: '#67e8f9', brightWhite: '#ffffff',
      },
      allowTransparency: true,
    });
    const fit = new FitAddon();
    const s = new SearchAddon();
    t.loadAddon(fit);
    t.loadAddon(s);
    t.loadAddon(new WebLinksAddon());
    t.open(box.current!);
    fit.fit();
    term.current = t;
    search.current = s;
    const writeAll = () => { t.reset(); if (bus.lines.length) t.write(bus.lines.join('\r\n') + '\r\n'); };
    writeAll();
    const off = bus.on((line) => (line === null ? writeAll() : t.write(line + '\r\n')));
    const ro = new ResizeObserver(() => { try { fit.fit(); } catch { /* hidden */ } });
    ro.observe(box.current!);
    return () => { off(); ro.disconnect(); t.dispose(); };
  }, [bus]);

  // stats history
  useEffect(() => {
    if (state.status === 'offline') return;
    setCpuHist((h) => [...h.slice(-59), cpuPct(state.cpu, server.cpu)]);
    setMemHist((h) => [...h.slice(-59), state.memory]);
  }, [state, server.cpu]);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    const c = cmd.trim();
    if (!c) return;
    if (!send('command', c)) return toast('Console déconnectée, nouvelle tentative…', 'err');
    term.current?.write(`\x1b[38;5;99m›\x1b[0m \x1b[2m${c}\x1b[0m\r\n`);
    const nh = [c, ...hist.filter((x) => x !== c)].slice(0, 50);
    setHist(nh);
    try { localStorage.setItem(HISTORY_KEY, JSON.stringify(nh)); } catch { /* ignore */ }
    setCmd('');
    setHIdx(-1);
  };

  const onKey = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'ArrowUp' && hist.length) {
      e.preventDefault();
      const i = Math.min(hIdx + 1, hist.length - 1);
      setHIdx(i); setCmd(hist[i]);
    } else if (e.key === 'ArrowDown') {
      e.preventDefault();
      const i = hIdx - 1;
      setHIdx(i); setCmd(i < 0 ? '' : hist[i]);
    }
  };

  const download = () => {
    const strip = bus.lines.map((l) => l.replace(/\x1b\[[0-9;?]*[A-Za-z]/g, '')).join('\n');
    const a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([strip], { type: 'text/plain' }));
    a.download = `${server.name}-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-')}.log`;
    a.click();
    URL.revokeObjectURL(a.href);
  };

  const off = state.status === 'offline';
  const memMax = server.memory * 1048576;
  const inputDisabled = !server.consoleInput || !can('console') || state.status !== 'running' && state.status !== 'starting';

  return (
    <div className="grid gap-5 xl:grid-cols-[1fr_260px]">
      <div className="card flex min-h-[520px] flex-col overflow-hidden">
        <div className="flex items-center justify-between gap-2 border-b border-ink-700 px-3 py-2">
          <div className="flex items-center gap-2 pl-1 text-xs text-ink-400">
            <span className={clsx('h-2 w-2 rounded-full', connected ? 'bg-emerald-400' : 'bg-red-500 animate-pulse')} />
            {connected ? 'Connecté en direct' : 'Reconnexion…'}
          </div>
          <div className="flex items-center gap-1">
            {showSearch && (
              <input autoFocus className="input !w-48 !py-1 text-xs" placeholder="Rechercher…" value={needle}
                onChange={(e) => { setNeedle(e.target.value); search.current?.findNext(e.target.value, { incremental: true }); }}
                onKeyDown={(e) => e.key === 'Enter' && search.current?.findNext(needle)} />
            )}
            <IconButton title="Rechercher" onClick={() => setShowSearch(!showSearch)}><Search className="h-4 w-4" /></IconButton>
            <IconButton title="Télécharger les logs" onClick={download}><Download className="h-4 w-4" /></IconButton>
            <IconButton title="Vider l'affichage" onClick={() => term.current?.clear()}><Eraser className="h-4 w-4" /></IconButton>
          </div>
        </div>
        <div className="relative min-h-0 flex-1 bg-ink-950/60">
          <div ref={box} className="absolute inset-0 px-1 py-1" />
        </div>
        <form onSubmit={submit} className="flex items-center gap-2 border-t border-ink-700 bg-ink-900 px-3 py-2">
          <ChevronRight className="h-4 w-4 shrink-0 text-brand-400" />
          <input value={cmd} onChange={(e) => setCmd(e.target.value)} onKeyDown={onKey} disabled={inputDisabled}
            placeholder={!server.consoleInput ? 'Ce serveur n\'accepte pas de commandes' : inputDisabled ? 'Le serveur doit être démarré pour envoyer des commandes' : 'Tape une commande… (↑ ↓ pour l\'historique)'}
            className="min-w-0 flex-1 bg-transparent font-mono text-sm text-ink-100 outline-none placeholder:text-ink-500 disabled:cursor-not-allowed" />
        </form>
      </div>

      <div className="grid content-start gap-4 sm:grid-cols-2 xl:grid-cols-1">
        <Stat icon={<Cpu className="h-3.5 w-3.5" />} label="Processeur" value={off ? '—' : `${cpuPct(state.cpu, server.cpu).toFixed(1)}%`} sub={server.cpu ? `/ ${server.cpu / 1000} vCPU` : '/ illimité'}>
          <Sparkline data={cpuHist} max={100} />
        </Stat>
        <Stat icon={<MemoryStick className="h-3.5 w-3.5" />} label="Mémoire" value={off ? '—' : bytes(state.memory)} sub={`/ ${bytes(memMax, 0)}`}>
          <Sparkline data={memHist} max={memMax} />
        </Stat>
        <Stat icon={<HardDrive className="h-3.5 w-3.5" />} label="Disque" value={bytes(disk)} sub={`/ ${bytes(server.disk * 1048576, 0)}`} />
        <Stat icon={<Timer className="h-3.5 w-3.5" />} label="Disponibilité" value={state.status === 'running' && state.startedAt ? duration(Date.now() / 1000 - state.startedAt) : '—'}
          sub={state.restarts ? `${state.restarts} redémarrage(s) auto` : undefined} />
      </div>
    </div>
  );
}
