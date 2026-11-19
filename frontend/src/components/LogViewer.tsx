import '@xterm/xterm/css/xterm.css';
import { FitAddon } from '@xterm/addon-fit';
import { SearchAddon } from '@xterm/addon-search';
import { Terminal } from '@xterm/xterm';
import clsx from 'clsx';
import { Download, Eraser, Search, X } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { IconButton, Toggle } from './ui';

export type LogTarget = { namespace: string; pods: { name: string; containers: string[] }[]; pod?: string };

export default function LogViewer({ target, onClose }: { target: LogTarget; onClose: () => void }) {
  const [pod, setPod] = useState(target.pod || target.pods[0]?.name || '');
  const containers = target.pods.find((p) => p.name === pod)?.containers || [];
  const [container, setContainer] = useState(containers[0] || '');
  const [previous, setPrevious] = useState(false);
  const [state, setState] = useState<'connecting' | 'live' | 'ended' | 'error'>('connecting');
  const [error, setError] = useState('');
  const [showSearch, setShowSearch] = useState(false);
  const [needle, setNeedle] = useState('');
  const box = useRef<HTMLDivElement>(null);
  const term = useRef<Terminal | null>(null);
  const search = useRef<SearchAddon | null>(null);
  const buf = useRef<string[]>([]);

  useEffect(() => { setContainer(containers[0] || ''); }, [pod]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const h = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', h);
    return () => window.removeEventListener('keydown', h);
  }, [onClose]);

  useEffect(() => {
    const t = new Terminal({
      disableStdin: true, convertEol: true, scrollback: 20000, fontSize: 12.5, lineHeight: 1.25,
      fontFamily: '"JetBrains Mono", ui-monospace, monospace', allowTransparency: true,
      theme: { background: '#00000000', foreground: '#d6d8df', cursor: '#00000000', selectionBackground: '#7a6bf150' },
    });
    const fit = new FitAddon();
    const s = new SearchAddon();
    t.loadAddon(fit);
    t.loadAddon(s);
    t.open(box.current!);
    fit.fit();
    term.current = t;
    search.current = s;
    const ro = new ResizeObserver(() => { try { fit.fit(); } catch { /* hidden */ } });
    ro.observe(box.current!);
    return () => { ro.disconnect(); t.dispose(); };
  }, []);

  useEffect(() => {
    if (!pod || !term.current) return;
    term.current.reset();
    buf.current = [];
    setState('connecting');
    setError('');
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const qs = new URLSearchParams({ namespace: target.namespace, pod, container, previous: previous ? '1' : '0' });
    const ws = new WebSocket(`${proto}://${location.host}/api/admin/cluster/logs?${qs}`);
    ws.onopen = () => setState('live');
    ws.onmessage = (ev) => {
      const m = JSON.parse(ev.data);
      if (m.type === 'lines') {
        buf.current.push(...m.lines);
        if (buf.current.length > 20000) buf.current.splice(0, buf.current.length - 20000);
        term.current?.write(m.lines.join('\r\n') + '\r\n');
      } else if (m.type === 'end') setState('ended');
      else if (m.type === 'error') { setState('error'); setError(m.data); }
    };
    ws.onclose = () => setState((s) => (s === 'live' || s === 'connecting' ? 'ended' : s));
    return () => ws.close();
  }, [target.namespace, pod, container, previous]);

  const download = () => {
    const a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([buf.current.join('\n').replace(/\x1b\[[0-9;?]*[A-Za-z]/g, '')], { type: 'text/plain' }));
    a.download = `${pod}${container ? '-' + container : ''}.log`;
    a.click();
    URL.revokeObjectURL(a.href);
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div role="dialog" aria-modal="true" className="flex h-[min(82vh,860px)] w-full max-w-6xl animate-fade-in flex-col overflow-hidden rounded-lg border border-ink-600 bg-ink-850 shadow-2xl shadow-black/50">
        <div className="flex flex-wrap items-center gap-2 border-b border-ink-700 px-4 py-2.5">
          <div className="mr-auto min-w-0">
            <p className="text-[13px] font-semibold">Logs</p>
            <p className="truncate font-mono text-2xs text-ink-400">{target.namespace}/{pod}{container && containers.length > 1 ? ` · ${container}` : ''}</p>
          </div>
          {target.pods.length > 1 && (
            <select className="input !h-8 !w-auto max-w-[260px] text-xs" value={pod} onChange={(e) => setPod(e.target.value)}>
              {target.pods.map((p) => <option key={p.name} value={p.name}>{p.name}</option>)}
            </select>
          )}
          {containers.length > 1 && (
            <select className="input !h-8 !w-auto text-xs" value={container} onChange={(e) => setContainer(e.target.value)}>
              {containers.map((c) => <option key={c}>{c}</option>)}
            </select>
          )}
          <Toggle checked={previous} onChange={setPrevious} label="Exécution précédente" />
          <span className="flex items-center gap-1.5 px-1 text-xs text-ink-400">
            <span className={clsx('h-1.5 w-1.5 rounded-full', state === 'live' ? 'bg-emerald-400' : state === 'error' ? 'bg-red-500' : state === 'ended' ? 'bg-ink-500' : 'bg-amber-400 animate-pulse')} />
            {state === 'live' ? (previous ? 'Chargé' : 'En direct') : state === 'ended' ? 'Terminé' : state === 'error' ? 'Erreur' : 'Connexion…'}
          </span>
          {showSearch && (
            <input autoFocus className="input !h-8 !w-44 text-xs" placeholder="Rechercher…" value={needle}
              onChange={(e) => { setNeedle(e.target.value); search.current?.findNext(e.target.value, { incremental: true }); }}
              onKeyDown={(e) => e.key === 'Enter' && search.current?.findNext(needle)} />
          )}
          <IconButton title="Rechercher" onClick={() => setShowSearch(!showSearch)}><Search /></IconButton>
          <IconButton title="Télécharger" onClick={download}><Download /></IconButton>
          <IconButton title="Vider l'affichage" onClick={() => term.current?.clear()}><Eraser /></IconButton>
          <IconButton title="Fermer" onClick={onClose}><X /></IconButton>
        </div>
        {error && <div className="border-b border-red-500/30 bg-red-500/[.07] px-4 py-2 text-xs text-red-200">{error}</div>}
        <div className="relative min-h-0 flex-1 bg-ink-950"><div ref={box} className="absolute inset-0" /></div>
      </div>
    </div>
  );
}
