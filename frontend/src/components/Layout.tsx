import clsx from 'clsx';
import { Activity, Boxes, Gauge, Layers, LogOut, Menu, Network, Server, Settings, User, Users, X } from 'lucide-react';
import React, { useState } from 'react';
import { NavLink, Outlet, useLocation } from 'react-router-dom';
import { post } from '../api';
import { useAuth } from '../main';

export function Logo({ name, size = 28 }: { name: string; size?: number }) {
  return (
    <div className="flex items-center gap-2.5">
      <img src="/kubepilot.svg" alt="" width={size} height={size} />
      <span className="text-[15px] font-semibold tracking-tight text-ink-100">{name}</span>
    </div>
  );
}

function Item({ to, icon: Icon, children, end }: { to: string; icon: typeof Server; children: React.ReactNode; end?: boolean }) {
  return (
    <NavLink to={to} end={end}
      className={({ isActive }) => clsx('group flex h-8 items-center gap-2.5 rounded-md px-2.5 text-[13px] font-medium transition-colors',
        isActive ? 'bg-ink-800 text-ink-100' : 'text-ink-300 hover:bg-ink-800/60 hover:text-ink-100')}>
      {({ isActive }) => <><Icon className={clsx('h-4 w-4', isActive ? 'text-brand-400' : 'text-ink-400 group-hover:text-ink-300')} />{children}</>}
    </NavLink>
  );
}

function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="mt-5 first:mt-0">
      <p className="mb-1 px-2.5 text-2xs font-medium uppercase tracking-wider text-ink-500">{label}</p>
      <div className="flex flex-col gap-0.5">{children}</div>
    </div>
  );
}

export default function Layout() {
  const { me, setMe } = useAuth();
  const [open, setOpen] = useState(false);
  const loc = useLocation();
  const logout = async () => {
    await post('/auth/logout').catch(() => {});
    setMe(null);
  };
  const name = me?.panelName || 'KubePilot';

  const nav = (
    <nav onClick={() => setOpen(false)}>
      <Section label="Espace">
        <Item to="/" end icon={Server}>Serveurs</Item>
        <Item to="/account" icon={User}>Mon compte</Item>
      </Section>
      {me?.user.isAdmin && (
        <Section label="Administration">
          <Item to="/admin" end icon={Gauge}>Vue d'ensemble</Item>
          <Item to="/admin/cluster" icon={Network}>Cluster</Item>
          <Item to="/admin/servers" icon={Boxes}>Serveurs</Item>
          <Item to="/admin/users" icon={Users}>Utilisateurs</Item>
          <Item to="/admin/templates" icon={Layers}>Templates</Item>
          <Item to="/admin/settings" icon={Settings}>Réglages</Item>
          <Item to="/admin/activity" icon={Activity}>Journal</Item>
        </Section>
      )}
    </nav>
  );

  const userBox = (
    <div className="flex items-center gap-2.5 rounded-md px-1.5 py-1.5">
      <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md border border-ink-600 bg-ink-800 text-2xs font-semibold text-ink-200">
        {me?.user.username.slice(0, 2).toUpperCase()}
      </div>
      <div className="min-w-0 flex-1 leading-tight">
        <p className="truncate text-[13px] font-medium text-ink-100">{me?.user.username}</p>
        <p className="truncate text-2xs text-ink-400">{me?.user.isAdmin ? 'Administrateur' : 'Utilisateur'}</p>
      </div>
      <button onClick={logout} title="Se déconnecter" aria-label="Se déconnecter" className="rounded-md p-1.5 text-ink-400 hover:bg-ink-800 hover:text-ink-100"><LogOut className="h-4 w-4" /></button>
    </div>
  );

  return (
    <div className="flex min-h-full bg-ink-900">
      <aside className="sticky top-0 hidden h-screen w-60 shrink-0 flex-col border-r border-ink-700 bg-ink-950 lg:flex">
        <div className="flex h-14 items-center border-b border-ink-700 px-4"><Logo name={name} /></div>
        <div className="flex-1 overflow-y-auto px-3 py-4">{nav}</div>
        <div className="border-t border-ink-700 p-2">{userBox}</div>
        <p className="border-t border-ink-700 px-4 py-2 text-2xs text-ink-500">v{me?.version} · Kubernetes</p>
      </aside>

      <div className="fixed inset-x-0 top-0 z-40 flex h-14 items-center justify-between border-b border-ink-700 bg-ink-950 px-4 lg:hidden">
        <Logo name={name} />
        <button onClick={() => setOpen(!open)} aria-label="Menu" className="rounded-md p-2 text-ink-300 hover:bg-ink-800">{open ? <X className="h-5 w-5" /> : <Menu className="h-5 w-5" />}</button>
      </div>
      {open && (
        <div className="fixed inset-x-0 top-14 z-40 max-h-[calc(100vh-3.5rem)] overflow-y-auto border-b border-ink-700 bg-ink-950 p-3 lg:hidden">
          {nav}
          <div className="mt-4 border-t border-ink-700 pt-2">{userBox}</div>
        </div>
      )}

      <main key={loc.pathname.split('/').slice(0, 3).join('/')} className="min-w-0 flex-1 px-4 pb-12 pt-20 sm:px-6 lg:px-8 lg:pt-7">
        <div className="mx-auto max-w-[1280px]"><Outlet /></div>
      </main>
    </div>
  );
}
