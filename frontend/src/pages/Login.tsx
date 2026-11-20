import { ArrowLeft, ShieldCheck } from 'lucide-react';
import { useState } from 'react';
import { Me, post } from '../api';
import LanguageSelect from '../components/LanguageSelect';
import { Button, ErrorBox, Field } from '../components/ui';
import { useAuth } from '../main';
import { t } from '../i18n';

type LoginResponse = Me | { twoFactor: true; ticket: string };

export default function Login() {
  const { setMe } = useAuth();
  const [login, setLogin] = useState('');
  const [password, setPassword] = useState('');
  const [ticket, setTicket] = useState(''); // set once the password is accepted and a second factor is needed
  const [code, setCode] = useState('');
  const [recovery, setRecovery] = useState(false);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  const run = async (fn: () => Promise<void>) => {
    setLoading(true);
    setError('');
    try { await fn(); } catch (err: any) { setError(err.message); } finally { setLoading(false); }
  };

  const submitPassword = (e: React.FormEvent) => {
    e.preventDefault();
    run(async () => {
      const r = await post<LoginResponse>('/auth/login', { login, password });
      if ('twoFactor' in r) { setTicket(r.ticket); setCode(''); setRecovery(false); } else setMe(r);
    });
  };

  const submitCode = (e: React.FormEvent) => {
    e.preventDefault();
    run(async () => {
      try {
        setMe(await post<Me>('/auth/login/2fa', { ticket, code }));
      } catch (err: any) {
        if (err.status === 410) { setTicket(''); setPassword(''); } // ticket expired: back to the password
        throw err;
      }
    });
  };

  return (
    <div className="flex min-h-full flex-col items-center justify-center bg-ink-950 px-4 py-12">
      <div className="w-full max-w-[360px]">
        <div className="mb-8 flex flex-col items-center text-center">
          <img src="/kubepilot.svg" alt="" width={48} height={48} />
          <h1 className="mt-4 text-xl font-semibold tracking-tight">{ticket ? t('login.2faTitle') : t('login.title')}</h1>
          <p className="mt-1 text-[13px] text-ink-400">{ticket ? (recovery ? t('login.recoveryHint') : t('login.2faHint')) : t('login.subtitle')}</p>
        </div>
        {!ticket ? (
          <form onSubmit={submitPassword} className="card space-y-4 p-6">
            {error && <ErrorBox error={error} />}
            <Field label={t('login.login')}>
              <input className="input" autoComplete="username" value={login} onChange={(e) => setLogin(e.target.value)} autoFocus required />
            </Field>
            <Field label={t('login.password')}>
              <input className="input" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
            </Field>
            <Button type="submit" variant="primary" loading={loading} className="!h-9 w-full">{t('login.submit')}</Button>
          </form>
        ) : (
          <form onSubmit={submitCode} className="card space-y-4 p-6">
            {error && <ErrorBox error={error} />}
            <Field label={recovery ? t('login.recoveryCode') : t('login.code')}>
              {recovery ? (
                <input key="recovery" className="input font-mono" autoComplete="off" placeholder="xxxxx-xxxxx" value={code} onChange={(e) => setCode(e.target.value)} autoFocus required />
              ) : (
                <input key="totp" className="input text-center font-mono text-lg tracking-[0.4em]" inputMode="numeric" autoComplete="one-time-code" pattern="[0-9 ]{6,7}" maxLength={7}
                  placeholder="000000" value={code} onChange={(e) => setCode(e.target.value.replace(/[^0-9 ]/g, ''))} autoFocus required />
              )}
            </Field>
            <Button type="submit" variant="primary" loading={loading} icon={<ShieldCheck />} className="!h-9 w-full">{t('login.verify')}</Button>
            <div className="flex items-center justify-between text-xs">
              <button type="button" className="inline-flex items-center gap-1 text-ink-400 hover:text-ink-200" onClick={() => { setTicket(''); setPassword(''); setError(''); }}>
                <ArrowLeft className="h-3.5 w-3.5" /> {t('login.back')}
              </button>
              <button type="button" className="text-brand-300 hover:underline" onClick={() => { setRecovery(!recovery); setCode(''); setError(''); }}>
                {recovery ? t('login.useApp') : t('login.useRecovery')}
              </button>
            </div>
          </form>
        )}
        <p className="mt-6 text-center text-2xs text-ink-500">{t('login.footer')}</p>
        <div className="mt-4 flex justify-center"><LanguageSelect compact /></div>
      </div>
    </div>
  );
}
