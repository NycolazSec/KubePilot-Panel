import '@fontsource-variable/inter';
import '@fontsource/jetbrains-mono/400.css';
import '@fontsource/jetbrains-mono/600.css';
import './index.css';

import React, { createContext, useCallback, useContext, useEffect, useState } from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom';
import { get, Me } from './api';
import { Loading, UIProvider } from './components/ui';
import Layout from './components/Layout';
import Login from './pages/Login';
import ForgotPassword from './pages/ForgotPassword';
import ResetPassword from './pages/ResetPassword';
import Setup from './pages/Setup';
import Dashboard from './pages/Dashboard';
import Account from './pages/Account';
import ServerLayout from './pages/server/ServerLayout';
import Console from './pages/server/Console';
import Files from './pages/server/Files';
import Players from './pages/server/Players';
import Plugins from './pages/server/Plugins';
import Backups from './pages/server/Backups';
import Databases from './pages/server/Databases';
import Schedules from './pages/server/Schedules';
import Network from './pages/server/Network';
import Startup from './pages/server/Startup';
import SubUsers from './pages/server/SubUsers';
import ActivityPage from './pages/server/ActivityPage';
import ServerSettings from './pages/server/ServerSettings';
import AdminLayout from './pages/admin/AdminLayout';
import AdminOverview from './pages/admin/AdminOverview';
import AdminServers from './pages/admin/AdminServers';
import AdminUsers from './pages/admin/AdminUsers';
import AdminTemplates from './pages/admin/AdminTemplates';
import AdminSettings from './pages/admin/AdminSettings';
import AdminActivity from './pages/admin/AdminActivity';
import AdminFirewall from './pages/admin/AdminFirewall';
import AdminNodes from './pages/admin/AdminNodes';

type AuthCtx = { me: Me | null; setMe: (m: Me | null) => void; refresh: () => Promise<void> };
const Auth = createContext<AuthCtx>(null!);
export const useAuth = () => useContext(Auth);

function App() {
  const [me, setMe] = useState<Me | null>(null);
  const [ready, setReady] = useState(false);

  const refresh = useCallback(async () => {
    try {
      setMe(await get<Me>('/auth/me'));
    } catch {
      setMe(null);
    } finally {
      setReady(true);
    }
  }, []);

  useEffect(() => {
    refresh();
    const h = () => setMe(null);
    window.addEventListener('kubepilot:unauthorized', h);
    return () => window.removeEventListener('kubepilot:unauthorized', h);
  }, [refresh]);

  useEffect(() => {
    document.title = me?.panelName ? `${me.panelName} · Panel` : 'KubePilot';
  }, [me?.panelName]);

  if (!ready) return <div className="app-bg h-full"><Loading /></div>;

  return (
    <Auth.Provider value={{ me, setMe, refresh }}>
      <BrowserRouter>
        {!me ? (
          <Routes>
            <Route path="forgot-password" element={<ForgotPassword />} />
            <Route path="reset-password" element={<ResetPassword />} />
            <Route path="setup" element={<Setup />} />
            <Route path="*" element={<Login />} />
          </Routes>
        ) : me.twoFactorRequired ? (
          // 2FA is mandatory and not set up yet: only the account page is reachable
          <Routes>
            <Route element={<Layout />}>
              <Route path="account" element={<Account />} />
              <Route path="*" element={<Navigate to="/account" replace />} />
            </Route>
          </Routes>
        ) : (
          <Routes>
            <Route element={<Layout />}>
              <Route index element={<Dashboard />} />
              <Route path="account" element={<Account />} />
              <Route path="server/:id" element={<ServerLayout />}>
                <Route index element={<Console />} />
                <Route path="players" element={<Players />} />
                <Route path="plugins" element={<Plugins />} />
                <Route path="files" element={<Files />} />
                <Route path="backups" element={<Backups />} />
                <Route path="databases" element={<Databases />} />
                <Route path="schedules" element={<Schedules />} />
                <Route path="network" element={<Network />} />
                <Route path="startup" element={<Startup />} />
                <Route path="users" element={<SubUsers />} />
                <Route path="activity" element={<ActivityPage />} />
                <Route path="settings" element={<ServerSettings />} />
              </Route>
              {me.user.isAdmin && (
                <Route path="admin" element={<AdminLayout />}>
                  <Route index element={<AdminOverview />} />
                  <Route path="servers" element={<AdminServers />} />
                  <Route path="users" element={<AdminUsers />} />
                  <Route path="templates" element={<AdminTemplates />} />
                  <Route path="settings" element={<AdminSettings />} />
                  <Route path="nodes" element={<AdminNodes />} />
                  <Route path="firewall" element={<AdminFirewall />} />
                  <Route path="activity" element={<AdminActivity />} />
                </Route>
              )}
              <Route path="*" element={<Navigate to="/" replace />} />
            </Route>
          </Routes>
        )}
      </BrowserRouter>
    </Auth.Provider>
  );
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <UIProvider>
      <App />
    </UIProvider>
  </React.StrictMode>,
);
