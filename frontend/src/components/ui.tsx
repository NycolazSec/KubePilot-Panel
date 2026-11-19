import clsx from 'clsx';
import { CircleAlert, CircleCheck, Info, LoaderCircle, X } from 'lucide-react';
import React, { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react';
import { Status } from '../api';
import { statusLabel } from '../lib/format';

// ---------- Button

type BtnVariant = 'primary' | 'secondary' | 'ghost' | 'danger' | 'success';
const btnStyles: Record<BtnVariant, string> = {
  primary: 'bg-gradient-to-r from-brand-600 to-sky2-500 text-white shadow-glow hover:brightness-110',
  secondary: 'bg-ink-800 border border-ink-600 text-ink-100 hover:border-ink-500 hover:bg-ink-700',
  ghost: 'text-ink-300 hover:text-ink-100 hover:bg-ink-800',
  danger: 'bg-red-500/10 border border-red-500/30 text-red-300 hover:bg-red-500/20',
  success: 'bg-emerald-500/10 border border-emerald-500/30 text-emerald-300 hover:bg-emerald-500/20',
};

export function Button({ variant = 'secondary', size = 'md', loading, icon, className, children, ...rest }:
  React.ButtonHTMLAttributes<HTMLButtonElement> & { variant?: BtnVariant; size?: 'sm' | 'md'; loading?: boolean; icon?: React.ReactNode }) {
  return (
    <button
      {...rest}
      disabled={rest.disabled || loading}
      className={clsx(
        'inline-flex items-center justify-center gap-2 rounded-xl font-medium transition disabled:opacity-50 disabled:cursor-not-allowed whitespace-nowrap select-none',
        size === 'sm' ? 'px-2.5 py-1.5 text-xs' : 'px-3.5 py-2 text-sm',
        btnStyles[variant], className,
      )}
    >
      {loading ? <LoaderCircle className="h-4 w-4 animate-spin" /> : icon}
      {children}
    </button>
  );
}

export function IconButton({ title, className, children, ...rest }: React.ButtonHTMLAttributes<HTMLButtonElement> & { title: string }) {
  return (
    <button {...rest} title={title} aria-label={title}
      className={clsx('inline-flex h-8 w-8 items-center justify-center rounded-lg text-ink-300 transition hover:bg-ink-700 hover:text-ink-100 disabled:opacity-40', className)}>
      {children}
    </button>
  );
}

// ---------- Layout bits

export function Card({ className, children, title, actions, padded = true }: { className?: string; children: React.ReactNode; title?: React.ReactNode; actions?: React.ReactNode; padded?: boolean }) {
  return (
    <div className={clsx('card animate-fade-in', className)}>
      {(title || actions) && (
        <div className="flex items-center justify-between gap-3 border-b border-ink-700 px-5 py-3.5">
          <h3 className="text-sm font-semibold text-ink-100">{title}</h3>
          <div className="flex items-center gap-2">{actions}</div>
        </div>
      )}
      <div className={clsx(padded && 'p-5')}>{children}</div>
    </div>
  );
}

export function PageHeader({ title, subtitle, actions }: { title: React.ReactNode; subtitle?: React.ReactNode; actions?: React.ReactNode }) {
  return (
    <div className="mb-6 flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight text-ink-100">{title}</h1>
        {subtitle && <p className="mt-1 text-sm text-ink-400">{subtitle}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

export function Field({ label, hint, children, className }: { label: string; hint?: React.ReactNode; children: React.ReactNode; className?: string }) {
  return (
    <label className={clsx('block', className)}>
      <span className="label">{label}</span>
      {children}
      {hint && <span className="mt-1 block text-xs text-ink-400">{hint}</span>}
    </label>
  );
}

export function Toggle({ checked, onChange, label }: { checked: boolean; onChange: (v: boolean) => void; label?: string }) {
  return (
    <button type="button" onClick={() => onChange(!checked)} className="inline-flex items-center gap-2.5 text-sm text-ink-200">
      <span className={clsx('relative h-5 w-9 rounded-full transition', checked ? 'bg-brand-500' : 'bg-ink-600')}>
        <span className={clsx('absolute top-0.5 h-4 w-4 rounded-full bg-white transition-all', checked ? 'left-[18px]' : 'left-0.5')} />
      </span>
      {label}
    </button>
  );
}

export function Spinner({ className }: { className?: string }) {
  return <LoaderCircle className={clsx('h-5 w-5 animate-spin text-brand-400', className)} />;
}

export function Loading({ label = 'Chargement…' }: { label?: string }) {
  return <div className="flex items-center justify-center gap-3 py-16 text-sm text-ink-400"><Spinner /> {label}</div>;
}

export function Empty({ icon, title, children }: { icon?: React.ReactNode; title: string; children?: React.ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center px-6 py-14 text-center">
      {icon && <div className="mb-3 rounded-2xl bg-ink-800 p-3 text-ink-400">{icon}</div>}
      <p className="font-medium text-ink-200">{title}</p>
      {children && <div className="mt-1 max-w-md text-sm text-ink-400">{children}</div>}
    </div>
  );
}

export function ErrorBox({ error }: { error: string }) {
  return <div className="flex items-start gap-2 rounded-xl border border-red-500/30 bg-red-500/10 px-4 py-3 text-sm text-red-200"><CircleAlert className="mt-0.5 h-4 w-4 shrink-0" />{error}</div>;
}

export function Badge({ color = 'gray', children }: { color?: 'gray' | 'green' | 'red' | 'yellow' | 'violet' | 'blue'; children: React.ReactNode }) {
  const c = {
    gray: 'bg-ink-700 text-ink-200', green: 'bg-emerald-500/15 text-emerald-300', red: 'bg-red-500/15 text-red-300',
    yellow: 'bg-amber-500/15 text-amber-300', violet: 'bg-brand-500/15 text-brand-300', blue: 'bg-sky2-500/15 text-sky2-400',
  }[color];
  return <span className={clsx('inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-semibold', c)}>{children}</span>;
}

export const statusColor: Record<Status, string> = {
  running: 'bg-emerald-400', starting: 'bg-amber-400', stopping: 'bg-amber-400', installing: 'bg-sky2-400', offline: 'bg-ink-500', crashed: 'bg-red-500',
};

export function StatusDot({ status, className }: { status: Status; className?: string }) {
  const pulse = status === 'starting' || status === 'stopping' || status === 'installing';
  return (
    <span className={clsx('relative inline-flex h-2.5 w-2.5', className)}>
      {(pulse || status === 'running') && <span className={clsx('absolute inline-flex h-full w-full rounded-full opacity-60', statusColor[status], pulse ? 'animate-ping' : 'animate-pulse')} />}
      <span className={clsx('relative inline-flex h-2.5 w-2.5 rounded-full', statusColor[status])} />
    </span>
  );
}

export function StatusPill({ status }: { status: Status }) {
  const color = { running: 'green', starting: 'yellow', stopping: 'yellow', installing: 'blue', offline: 'gray', crashed: 'red' }[status] as any;
  return <Badge color={color}><StatusDot status={status} className="!h-1.5 !w-1.5 [&>span]:!h-1.5 [&>span]:!w-1.5" />{statusLabel[status]}</Badge>;
}

export function Bar({ value, max, className }: { value: number; max: number; className?: string }) {
  const pct = max > 0 ? Math.min(100, (value / max) * 100) : 0;
  return (
    <div className={clsx('h-1.5 overflow-hidden rounded-full bg-ink-700', className)}>
      <div className={clsx('h-full rounded-full transition-all duration-700', pct > 90 ? 'bg-red-500' : pct > 70 ? 'bg-amber-400' : 'bg-gradient-to-r from-brand-500 to-sky2-500')} style={{ width: `${pct}%` }} />
    </div>
  );
}

export function Tabs<T extends string>({ value, onChange, items }: { value: T; onChange: (v: T) => void; items: { id: T; label: React.ReactNode }[] }) {
  return (
    <div className="inline-flex rounded-xl border border-ink-700 bg-ink-900 p-1">
      {items.map((it) => (
        <button key={it.id} onClick={() => onChange(it.id)}
          className={clsx('rounded-lg px-3 py-1.5 text-xs font-medium transition', value === it.id ? 'bg-ink-700 text-ink-100' : 'text-ink-400 hover:text-ink-200')}>
          {it.label}
        </button>
      ))}
    </div>
  );
}

// ---------- Modal

export function Modal({ open, onClose, title, children, footer, wide }: { open: boolean; onClose: () => void; title: React.ReactNode; children: React.ReactNode; footer?: React.ReactNode; wide?: boolean | 'xl' }) {
  useEffect(() => {
    if (!open) return;
    const h = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    window.addEventListener('keydown', h);
    return () => window.removeEventListener('keydown', h);
  }, [open, onClose]);
  if (!open) return null;
  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/60 p-4 backdrop-blur-sm sm:items-center" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className={clsx('card my-8 w-full animate-fade-in shadow-2xl', wide === 'xl' ? 'max-w-5xl' : wide ? 'max-w-3xl' : 'max-w-lg')}>
        <div className="flex items-center justify-between border-b border-ink-700 px-5 py-4">
          <h3 className="font-semibold">{title}</h3>
          <IconButton title="Fermer" onClick={onClose}><X className="h-4 w-4" /></IconButton>
        </div>
        <div className="p-5">{children}</div>
        {footer && <div className="flex justify-end gap-2 border-t border-ink-700 px-5 py-3.5">{footer}</div>}
      </div>
    </div>
  );
}

// ---------- Toasts + confirm

type Toast = { id: number; kind: 'ok' | 'err' | 'info'; text: string };
type ConfirmOpts = { title: string; body?: React.ReactNode; confirm?: string; danger?: boolean };
type Ctx = {
  toast: (text: string, kind?: Toast['kind']) => void;
  confirm: (o: ConfirmOpts) => Promise<boolean>;
  prompt: (title: string, initial?: string, label?: string) => Promise<string | null>;
};
const UICtx = createContext<Ctx>(null!);
export const useUI = () => useContext(UICtx);

export function UIProvider({ children }: { children: React.ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [conf, setConf] = useState<(ConfirmOpts & { resolve: (v: boolean) => void }) | null>(null);
  const [prm, setPrm] = useState<{ title: string; label?: string; value: string; resolve: (v: string | null) => void } | null>(null);
  const seq = useRef(0);

  const toast = useCallback((text: string, kind: Toast['kind'] = 'ok') => {
    const id = ++seq.current;
    setToasts((t) => [...t, { id, kind, text }]);
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), kind === 'err' ? 6000 : 3500);
  }, []);
  const confirm = useCallback((o: ConfirmOpts) => new Promise<boolean>((resolve) => setConf({ ...o, resolve })), []);
  const prompt = useCallback((title: string, initial = '', label?: string) => new Promise<string | null>((resolve) => setPrm({ title, label, value: initial, resolve })), []);

  const closeConf = (v: boolean) => { conf?.resolve(v); setConf(null); };
  const closePrm = (v: string | null) => { prm?.resolve(v); setPrm(null); };

  return (
    <UICtx.Provider value={{ toast, confirm, prompt }}>
      {children}
      <div className="pointer-events-none fixed bottom-4 right-4 z-[60] flex w-[min(380px,calc(100vw-2rem))] flex-col gap-2">
        {toasts.map((t) => (
          <div key={t.id} className={clsx('pointer-events-auto flex items-start gap-2.5 rounded-xl border bg-ink-850/95 px-4 py-3 text-sm shadow-2xl backdrop-blur animate-fade-in',
            t.kind === 'err' ? 'border-red-500/40' : t.kind === 'info' ? 'border-sky2-500/40' : 'border-emerald-500/40')}>
            {t.kind === 'err' ? <CircleAlert className="mt-0.5 h-4 w-4 shrink-0 text-red-400" /> : t.kind === 'info' ? <Info className="mt-0.5 h-4 w-4 shrink-0 text-sky2-400" /> : <CircleCheck className="mt-0.5 h-4 w-4 shrink-0 text-emerald-400" />}
            <span className="text-ink-100">{t.text}</span>
          </div>
        ))}
      </div>
      <Modal open={!!conf} onClose={() => closeConf(false)} title={conf?.title}
        footer={<>
          <Button variant="ghost" onClick={() => closeConf(false)}>Annuler</Button>
          <Button variant={conf?.danger ? 'danger' : 'primary'} onClick={() => closeConf(true)} autoFocus>{conf?.confirm || 'Confirmer'}</Button>
        </>}>
        <div className="text-sm text-ink-300">{conf?.body}</div>
      </Modal>
      <Modal open={!!prm} onClose={() => closePrm(null)} title={prm?.title}
        footer={<>
          <Button variant="ghost" onClick={() => closePrm(null)}>Annuler</Button>
          <Button variant="primary" onClick={() => closePrm(prm?.value ?? '')}>Valider</Button>
        </>}>
        <form onSubmit={(e) => { e.preventDefault(); closePrm(prm?.value ?? ''); }}>
          {prm?.label && <span className="label">{prm.label}</span>}
          <input autoFocus className="input" value={prm?.value ?? ''} onChange={(e) => prm && setPrm({ ...prm, value: e.target.value })} />
        </form>
      </Modal>
    </UICtx.Provider>
  );
}

// ---------- Data hook

export function useLoad<T>(fn: () => Promise<T>, deps: unknown[] = []) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const fnRef = useRef(fn);
  fnRef.current = fn;
  const reload = useCallback(async (silent = false) => {
    if (!silent) setLoading(true);
    try {
      setData(await fnRef.current());
      setError('');
    } catch (e: any) {
      setError(e.message || String(e));
    } finally {
      setLoading(false);
    }
  }, []);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => { reload(); }, deps);
  return { data, error, loading, reload, setData };
}
