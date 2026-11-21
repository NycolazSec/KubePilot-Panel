import { Copy, Rocket } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { get, Me, post } from '../api';
import AuthShell from '../components/AuthShell';
import { Button, ErrorBox, Field, Loading, useUI } from '../components/ui';
import { copy } from '../lib/format';
import { useAuth } from '../main';
import { t } from '../i18n';

const logsCommand = 'kubectl -n kubepilot-system logs deploy/kubepilot | grep "Setup token"';

/** First start: create the administrator with the one-time token the panel printed in its logs. */
export default function Setup() {
  const { setMe } = useAuth();
  const { toast } = useUI();
  const nav = useNavigate();
  // the token may come in the #fragment of the link printed in the logs; it leaves the address bar at once
  const [token, setToken] = useState(() => window.location.hash.replace(/^#/, '').trim());
  const [needed, setNeeded] = useState<boolean | null>(null);
  const [username, setUsername] = useState('admin');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  useEffect(() => {
    // a link pasted in a tab already on /setup only changes the #fragment: take the token from there too
    const take = () => {
      const h = window.location.hash.replace(/^#/, '').trim();
      if (h) setToken(h);
      if (window.location.hash) history.replaceState(null, '', window.location.pathname);
    };
    take();
    window.addEventListener('hashchange', take);
    get<{ setup: boolean }>('/auth/options').then((o) => setNeeded(o.setup)).catch(() => setNeeded(false));
    return () => window.removeEventListener('hashchange', take);
  }, []);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (password !== confirm) { setError(t('account.mismatch')); return; }
    setLoading(true);
    setError('');
    try {
      await post('/setup', { token, username, email, password });
      setMe(await post<Me>('/auth/login', { login: username, password }));
      history.replaceState(null, '', '/');
    } catch (err: any) { setError(err.message); } finally { setLoading(false); }
  };

  if (needed === null) return <AuthShell title={t('setup.title')}><Loading /></AuthShell>;
  if (!needed) {
    return (
      <AuthShell title={t('setup.title')}>
        <div className="card space-y-4 p-6 text-sm">
          <p className="text-ink-200">{t('setup.already')}</p>
          <Button variant="primary" className="!h-9 w-full" onClick={() => nav('/')}>{t('login.submit')}</Button>
        </div>
      </AuthShell>
    );
  }
  return (
    <AuthShell title={t('setup.title')} subtitle={t('setup.subtitle')}>
      <form onSubmit={submit} className="card space-y-4 p-6">
        {error && <ErrorBox error={error} />}
        <Field label={t('setup.token')} hint={
          <span className="block space-y-1.5">
            <span className="block">{t('setup.tokenHint')}</span>
            <button type="button" onClick={async () => { await copy(logsCommand); toast(t('setup.copied')); }}
              className="flex w-full items-start gap-2 rounded-md border border-ink-700 bg-ink-950 px-2.5 py-1.5 text-left font-mono text-[11px] leading-relaxed text-ink-300 hover:border-ink-500">
              <Copy className="mt-0.5 h-3 w-3 shrink-0 text-ink-400" /><span className="min-w-0 break-all">{logsCommand}</span>
            </button>
          </span>
        }>
          <input id="setup-token" className="input font-mono" autoComplete="off" spellCheck={false} value={token} onChange={(e) => setToken(e.target.value.trim())} autoFocus={!token} required />
        </Field>
        <Field label={t('account.username')}>
          <input id="setup-username" className="input" autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} autoFocus={!!token} required />
        </Field>
        <Field label={t('account.email')}>
          <input id="setup-email" className="input" type="email" autoComplete="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
        </Field>
        <Field label={t('login.password')} hint={t('account.passwordHint')}>
          <input id="setup-password" className="input" type="password" autoComplete="new-password" minLength={8} value={password} onChange={(e) => setPassword(e.target.value)} required />
        </Field>
        <Field label={t('account.confirmPassword')}>
          <input id="setup-confirm" className="input" type="password" autoComplete="new-password" minLength={8} value={confirm} onChange={(e) => setConfirm(e.target.value)} required />
        </Field>
        <Button type="submit" variant="primary" loading={loading} icon={<Rocket />} className="!h-9 w-full">{t('setup.submit')}</Button>
      </form>
    </AuthShell>
  );
}
