import { ArrowLeft, CheckCircle2 } from 'lucide-react';
import { useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { post } from '../api';
import AuthShell from '../components/AuthShell';
import { Button, ErrorBox, Field } from '../components/ui';
import { t } from '../i18n';

// The token comes in the URL's #fragment (never sent to a server); it is removed from the address bar at once.
const readToken = () => window.location.hash.replace(/^#/, '').trim();

export default function ResetPassword() {
  const nav = useNavigate();
  const [token] = useState(readToken);
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [done, setDone] = useState(false);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  useEffect(() => { if (window.location.hash) history.replaceState(null, '', window.location.pathname); }, []);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (password !== confirm) { setError(t('account.mismatch')); return; }
    setLoading(true);
    setError('');
    try { await post('/auth/reset', { token, password }); setDone(true); } catch (err: any) { setError(err.message); } finally { setLoading(false); }
  };
  const back = <Link to="/" className="inline-flex items-center gap-1 text-xs text-ink-400 hover:text-ink-200"><ArrowLeft className="h-3.5 w-3.5" /> {t('forgot.backToLogin')}</Link>;
  return (
    <AuthShell title={t('reset.title')} subtitle={done || !token ? undefined : t('reset.subtitle')}>
      {!token ? (
        <div className="card space-y-4 p-6 text-sm"><ErrorBox error={t('reset.noToken')} />{back}</div>
      ) : done ? (
        <div className="card space-y-4 p-6 text-sm">
          <div className="flex items-start gap-3">
            <CheckCircle2 className="mt-0.5 h-5 w-5 shrink-0 text-emerald-300" />
            <p className="text-ink-200">{t('reset.done')}</p>
          </div>
          <Button variant="primary" className="!h-9 w-full" onClick={() => nav('/')}>{t('login.submit')}</Button>
        </div>
      ) : (
        <form onSubmit={submit} className="card space-y-4 p-6">
          {error && <ErrorBox error={error} />}
          <Field label={t('account.newPassword')} hint={t('account.passwordHint')}>
            <input className="input" type="password" autoComplete="new-password" minLength={8} value={password} onChange={(e) => setPassword(e.target.value)} autoFocus required />
          </Field>
          <Field label={t('account.confirmPassword')}>
            <input className="input" type="password" autoComplete="new-password" minLength={8} value={confirm} onChange={(e) => setConfirm(e.target.value)} required />
          </Field>
          <Button type="submit" variant="primary" loading={loading} className="!h-9 w-full">{t('reset.submit')}</Button>
          {back}
        </form>
      )}
    </AuthShell>
  );
}
