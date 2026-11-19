import clsx from 'clsx';
import { CircleAlert, CircleCheck, Info, LoaderCircle, X } from 'lucide-react';
import React, { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react';
import { Status } from '../api';
import { statusLabel } from '../lib/format';

// ---------- Button

type BtnVariant = 'primary' | 'secondary' | 'ghost' | 'danger' | 'success';
const btnStyles: Record<BtnVariant, string> = {
  primary: 'bg-brand-600 text-white border border-brand-500/60 hover:bg-brand-500 disabled:bg-ink-800 disabled:border-ink-700 disabled:text-ink-500 disabled:hover:bg-ink-800',
  secondary: 'bg-ink-800 text-ink-100 border border-ink-600 hover:bg-ink-750 hover:border-ink-500',
  ghost: 'text-ink-300 border border-transparent hover:bg-ink-800 hover:text-ink-100',
  danger: 'bg-ink-800 text-red-300 border border-ink-600 hover:border-red-500/50 hover:bg-red-500/10',
  success: 'bg-ink-800 text-emerald-300 border border-ink-600 hover:border-emerald-500/50 hover:bg-emerald-500/10',
};

export function Button({ variant = 'secondary', size = 'md', loading, icon, className, children, ...rest }:
  React.ButtonHTMLAttributes<HTMLButtonElement> & { variant?: BtnVariant; size?: 'sm' | 'md'; loading?: boolean; icon?: React.ReactNode }) {
  return (
    <button
      {...rest}
      disabled={rest.disabled || loading}
      className={clsx(
        'inline-flex items-center justify-center gap-1.5 rounded-md font-medium transition-colors disabled:opacity-60 disabled:cursor-not-allowed whitespace-nowrap select-none [&_svg]:h-3.5 [&_svg]:w-3.5',
        size === 'sm' ? 'h-7 px-2.5 text-xs' : 'h-8 px-3 text-[13px]',
        btnStyles[variant], className,
      )}
    >
      {loading ? <LoaderCircle className="animate-spin" /> : icon}
      {children}
    </button>
  );
}

export function IconButton({ title, className, children, ...rest }: React.ButtonHTMLAttributes<HTMLButtonElement> & { title: string }) {
  return (
    <button {...rest} title={title} aria-label={title}
      className={clsx('inline-flex h-7 w-7 items-center justify-center rounded-md text-ink-400 transition-colors hover:bg-ink-750 hover:text-ink-100 disabled:opacity-40 [&_svg]:h-4 [&_svg]:w-4', className)}>
      {children}
    </button>
  );
}

// ---------- Layout bits

export function Card({ className, children, title, actions, padded = true, description }: { className?: string; children: React.ReactNode; title?: React.ReactNode; actions?: React.ReactNode; padded?: boolean; description?: React.ReactNode }) {
  return (
    <section className={clsx('card', className)}>
      {(title || actions) && (
        <header className="flex min-h-[52px] items-center justify-between gap-3 border-b border-ink-700 px-4 py-2.5">
          <div className="min-w-0">
            <h3 className="text-[13px] font-semibold text-ink-100">{title}</h3>
            {description && <p className="mt-0.5 text-xs text-ink-400">{description}</p>}
          </div>
          <div className="flex shrink-0 items-center gap-2">{actions}</div>
        </header>
      )}
      <div className={clsx(padded && 'p-4')}>{children}</div>
    </section>
  );
}

export function PageHeader({ title, subtitle, actions }: { title: React.ReactNode; subtitle?: React.ReactNode; actions?: React.ReactNode }) {
  return (
    <div className="mb-5 flex flex-wrap items-end justify-between gap-4">
      <div className="min-w-0">
        <h1 className="text-xl font-semibold tracking-tight text-ink-100">{title}</h1>
        {subtitle && <p className="mt-1 text-[13px] text-ink-400">{subtitle}</p>}
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
      {hint && <span className="mt-1.5 block text-xs text-ink-400">{hint}</span>}
    </label>
  );
}

export function Toggle({ checked, onChange, label }: { checked: boolean; onChange: (v: boolean) => void; label?: string }) {
  return (
    <button type="button" role="switch" aria-checked={checked} onClick={() => onChange(!checked)} className="inline-flex items-center gap-2.5 text-[13px] text-ink-200">
      <span className={clsx('relative h-[18px] w-8 rounded-full border transition-colors', checked ? 'border-brand-500 bg-brand-600' : 'border-ink-600 bg-ink-750')}>
        <span className={clsx('absolute top-[2px] h-3 w-3 rounded-full bg-white transition-all', checked ? 'left-[16px]' : 'left-[2px] bg-ink-300')} />
      </span>
      {label}
    </button>
  );
}

export function Spinner({ className }: { className?: string }) {
  return <LoaderCircle className={clsx('h-4 w-4 animate-spin text-ink-400', className)} />;
}

export function Loading({ label = 'Chargement…' }: { label?: string }) {
  return <div className="flex items-center justify-center gap-2.5 py-14 text-[13px] text-ink-400"><Spinner /> {label}</div>;
}

export function Empty({ icon, title, children }: { icon?: React.ReactNode; title: string; children?: React.ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center px-6 py-12 text-center">
      {icon && <div className="mb-3 flex h-10 w-10 items-center justify-center rounded-md border border-ink-700 bg-ink-800 text-ink-400 [&_svg]:h-5 [&_svg]:w-5">{icon}</div>}
      <p className="text-[13px] font-medium text-ink-100">{title}</p>
      {children && <div className="mt-1 max-w-md text-[13px] text-ink-400">{children}</div>}
    </div>
  );
}

export function ErrorBox({ error }: { error: string }) {
  return <div className="flex items-start gap-2 rounded-md border border-red-500/30 bg-red-500/[.07] px-3 py-2.5 text-[13px] text-red-200"><CircleAlert className="mt-0.5 h-4 w-4 shrink-0 text-red-400" />{error}</div>;
}

type BadgeColor = 'gray' | 'green' | 'red' | 'yellow' | 'violet' | 'blue';
const badgeStyles: Record<BadgeColor, string> = {
  gray: 'bg-ink-750 text-ink-300 ring-ink-600',
  green: 'bg-emerald-500/10 text-emerald-300 ring-emerald-500/25',
  red: 'bg-red-500/10 text-red-300 ring-red-500/25',
  yellow: 'bg-amber-500/10 text-amber-300 ring-amber-500/25',
  violet: 'bg-brand-500/10 text-brand-300 ring-brand-500/25',
  blue: 'bg-sky2-500/10 text-sky2-400 ring-sky2-500/25',
};

export function Badge({ color = 'gray', children }: { color?: BadgeColor; children: React.ReactNode }) {
  return <span className={clsx('inline-flex h-5 items-center gap-1 whitespace-nowrap rounded px-1.5 text-2xs font-medium ring-1 ring-inset [&_svg]:h-3 [&_svg]:w-3', badgeStyles[color])}>{children}</span>;
}

export const statusColor: Record<Status, string> = {
  running: 'bg-emerald-400', starting: 'bg-amber-400', stopping: 'bg-amber-400', installing: 'bg-sky2-400', offline: 'bg-ink-500', crashed: 'bg-red-500',
};

export function StatusDot({ status, className }: { status: Status; className?: string }) {
  const transitional = status === 'starting' || status === 'stopping' || status === 'installing';
  return <span className={clsx('inline-block h-2 w-2 shrink-0 rounded-full', statusColor[status], transitional && 'animate-pulse', className)} />;
}

const statusBadge: Record<Status, BadgeColor> = { running: 'green', starting: 'yellow', stopping: 'yellow', installing: 'blue', offline: 'gray', crashed: 'red' };

export function StatusPill({ status }: { status: Status }) {
  return <Badge color={statusBadge[status]}><StatusDot status={status} className="!h-1.5 !w-1.5" />{statusLabel[status]}</Badge>;
}

export function Bar({ value, max, className }: { value: number; max: number; className?: string }) {
  const pct = max > 0 ? Math.min(100, (value / max) * 100) : 0;
  return (
    <div className={clsx('h-1 overflow-hidden rounded-full bg-ink-700', className)}>
      <div className={clsx('h-full rounded-full transition-[width] duration-700', pct > 90 ? 'bg-red-500' : pct > 75 ? 'bg-amber-400' : 'bg-brand-500')} style={{ width: `${pct}%` }} />
    </div>
  );
}

export function Tabs<T extends string>({ value, onChange, items }: { value: T; onChange: (v: T) => void; items: { id: T; label: React.ReactNode }[] }) {
  return (
    <div className="inline-flex h-9 items-center rounded-md border border-ink-600 bg-ink-850 p-0.5">
      {items.map((it) => (
        <button key={it.id} onClick={() => onChange(it.id)}
          className={clsx('h-full rounded px-3 text-xs font-medium transition-colors', value === it.id ? 'bg-ink-700 text-ink-100' : 'text-ink-400 hover:text-ink-200')}>
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
    <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/70 p-4 sm:items-center" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div role="dialog" aria-modal="true" className={clsx('my-8 w-full animate-fade-in rounded-lg border border-ink-600 bg-ink-850 shadow-2xl shadow-black/50', wide === 'xl' ? 'max-w-5xl' : wide ? 'max-w-3xl' : 'max-w-lg')}>
        <div className="flex items-center justify-between border-b border-ink-700 px-5 py-3">
          <h3 className="text-[15px] font-semibold">{title}</h3>
          <IconButton title="Fermer" onClick={onClose}><X /></IconButton>
        </div>
        <div className="p-5">{children}</div>
        {footer && <div className="flex justify-end gap-2 rounded-b-lg border-t border-ink-700 bg-ink-900/40 px-5 py-3">{footer}</div>}
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
      <div className="pointer-events-none fixed bottom-4 right-4 z-[60] flex w-[min(360px,calc(100vw-2rem))] flex-col gap-2">
        {toasts.map((t) => (
          <div key={t.id} className="pointer-events-auto flex animate-fade-in items-start gap-2.5 rounded-md border border-ink-600 bg-ink-800 px-3.5 py-2.5 text-[13px] shadow-xl shadow-black/40">
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
        <div className="text-[13px] leading-relaxed text-ink-300">{conf?.body}</div>
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
