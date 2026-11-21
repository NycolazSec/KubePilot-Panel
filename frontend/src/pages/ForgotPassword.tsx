import { ArrowLeft, MailCheck } from 'lucide-react';
import { useState } from 'react';
import { Link } from 'react-router-dom';
import { post } from '../api';
import AuthShell from '../components/AuthShell';
import { Button, ErrorBox, Field } from '../components/ui';
import { t } from '../i18n';

export default function ForgotPassword() {
  const [login, setLogin] = useState('');
  const [sent, setSent] = useState(false);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setError('');
    try { await post('/auth/forgot', { login }); setSent(true); } catch (err: any) { setError(err.message); } finally { setLoading(false); }
  };
  return (
    <AuthShell title={t('forgot.title')} subtitle={sent ? undefined : t('forgot.subtitle')}>
      {sent ? (
        <div className="card space-y-4 p-6 text-sm">
          <div className="flex items-start gap-3">
            <MailCheck className="mt-0.5 h-5 w-5 shrink-0 text-emerald-300" />
            <p className="text-ink-200">{t('forgot.sent')}</p>
          </div>
          <Link to="/" className="inline-flex items-center gap-1 text-xs text-ink-400 hover:text-ink-200"><ArrowLeft className="h-3.5 w-3.5" /> {t('forgot.backToLogin')}</Link>
        </div>
      ) : (
        <form onSubmit={submit} className="card space-y-4 p-6">
          {error && <ErrorBox error={error} />}
          <Field label={t('login.login')}>
            <input className="input" autoComplete="username" value={login} onChange={(e) => setLogin(e.target.value)} autoFocus required />
          </Field>
          <Button type="submit" variant="primary" loading={loading} className="!h-9 w-full">{t('forgot.submit')}</Button>
          <Link to="/" className="inline-flex items-center gap-1 text-xs text-ink-400 hover:text-ink-200"><ArrowLeft className="h-3.5 w-3.5" /> {t('forgot.backToLogin')}</Link>
        </form>
      )}
    </AuthShell>
  );
}
