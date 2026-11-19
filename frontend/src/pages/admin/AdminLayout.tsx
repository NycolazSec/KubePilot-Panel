import clsx from 'clsx';
import { Activity, Boxes, Gauge, Layers, Settings, Users } from 'lucide-react';
import { NavLink, Outlet } from 'react-router-dom';
import { PageHeader } from '../../components/ui';

const tabs = [
  { to: '', label: 'Vue d\'ensemble', icon: Gauge },
  { to: 'servers', label: 'Serveurs', icon: Boxes },
  { to: 'users', label: 'Utilisateurs', icon: Users },
  { to: 'templates', label: 'Templates', icon: Layers },
  { to: 'settings', label: 'Réglages', icon: Settings },
  { to: 'activity', label: 'Journal', icon: Activity },
];

export default function AdminLayout() {
  return (
    <>
      <PageHeader title="Administration" subtitle="Serveurs, utilisateurs, templates et cluster Kubernetes." />
      <nav className="mb-6 flex gap-1 overflow-x-auto border-b border-ink-700">
        {tabs.map((t) => (
          <NavLink key={t.to} to={t.to} end={t.to === ''}
            className={({ isActive }) => clsx('flex shrink-0 items-center gap-2 border-b-2 px-3.5 py-2.5 text-sm font-medium transition',
              isActive ? 'border-brand-400 text-ink-100' : 'border-transparent text-ink-400 hover:text-ink-200')}>
            <t.icon className="h-4 w-4" /> {t.label}
          </NavLink>
        ))}
      </nav>
      <div className="animate-fade-in"><Outlet /></div>
    </>
  );
}
