import clsx from 'clsx';
import { LayoutDashboard, LogOut, Menu, Shield, User, X } from 'lucide-react';
import { useState } from 'react';
import { NavLink, Outlet, useLocation } from 'react-router-dom';
import { post } from '../api';
import { useAuth } from '../main';

export function Logo({ name }: { name: string }) {
  return (
    <div className="flex items-center gap-2.5">
      <img src="/kubepilot.svg" alt="" className="h-8 w-8" />
      <span className="text-lg font-semibold tracking-tight"><span className="text-gradient">{name}</span></span>
    </div>
  );
}

const linkCls = ({ isActive }: { isActive: boolean }) =>
  clsx('flex items-center gap-3 rounded-xl px-3 py-2 text-sm font-medium transition',
    isActive ? 'bg-brand-500/15 text-brand-200 ring-1 ring-inset ring-brand-500/30' : 'text-ink-300 hover:bg-ink-800 hover:text-ink-100');

export default function Layout() {
  const { me, setMe } = useAuth();
  const [open, setOpen] = useState(false);
  const loc = useLocation();
  const logout = async () => {
    await post('/auth/logout').catch(() => {});
    setMe(null);
  };

  const nav = (
    <nav className="flex flex-col gap-1" onClick={() => setOpen(false)}>
      <NavLink to="/" end className={linkCls}><LayoutDashboard className="h-4 w-4" /> Mes serveurs</NavLink>
      {me?.user.isAdmin && <NavLink to="/admin" className={linkCls}><Shield className="h-4 w-4" /> Administration</NavLink>}
      <NavLink to="/account" className={linkCls}><User className="h-4 w-4" /> Mon compte</NavLink>
    </nav>
  );

  return (
    <div className="app-bg flex min-h-full">
      {/* Sidebar desktop */}
      <aside className="sticky top-0 hidden h-screen w-60 shrink-0 flex-col border-r border-ink-700/70 bg-ink-900/60 px-4 py-5 backdrop-blur lg:flex">
        <div className="mb-8 px-1"><Logo name={me?.panelName || 'KubePilot'} /></div>
        {nav}
        <div className="mt-auto rounded-2xl border border-ink-700 bg-ink-850 p-3">
          <div className="flex items-center gap-3">
            <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-gradient-to-br from-brand-500 to-sky2-500 text-sm font-semibold text-white">
              {me?.user.username.slice(0, 2).toUpperCase()}
            </div>
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-medium">{me?.user.username}</p>
              <p className="truncate text-xs text-ink-400">{me?.user.isAdmin ? 'Administrateur' : 'Utilisateur'}</p>
            </div>
            <button onClick={logout} title="Se déconnecter" className="rounded-lg p-2 text-ink-400 hover:bg-ink-700 hover:text-ink-100"><LogOut className="h-4 w-4" /></button>
          </div>
        </div>
        <p className="mt-3 px-1 text-[11px] text-ink-500">KubePilot v{me?.version} · Kubernetes</p>
      </aside>

      {/* Mobile top bar */}
      <div className="fixed inset-x-0 top-0 z-40 flex h-14 items-center justify-between border-b border-ink-700 bg-ink-900/90 px-4 backdrop-blur lg:hidden">
        <Logo name={me?.panelName || 'KubePilot'} />
        <button onClick={() => setOpen(!open)} className="rounded-lg p-2 text-ink-300 hover:bg-ink-800">{open ? <X className="h-5 w-5" /> : <Menu className="h-5 w-5" />}</button>
      </div>
      {open && (
        <div className="fixed inset-x-0 top-14 z-40 border-b border-ink-700 bg-ink-900 p-4 lg:hidden">
          {nav}
          <button onClick={logout} className="mt-2 flex w-full items-center gap-3 rounded-xl px-3 py-2 text-sm text-ink-300 hover:bg-ink-800"><LogOut className="h-4 w-4" /> Se déconnecter</button>
        </div>
      )}

      <main key={loc.pathname.split('/').slice(0, 3).join('/')} className="min-w-0 flex-1 px-4 pb-10 pt-20 sm:px-6 lg:px-10 lg:pt-8">
        <div className="mx-auto max-w-7xl"><Outlet /></div>
      </main>
    </div>
  );
}
