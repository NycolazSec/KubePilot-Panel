import { useState } from 'react';
import { Me, post } from '../api';
import LanguageSelect from '../components/LanguageSelect';
import { Button, ErrorBox, Field } from '../components/ui';
import { useAuth } from '../main';
import { t } from '../i18n';

export default function Login() {
  const { setMe } = useAuth();
  const [login, setLogin] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setError('');
    try {
      setMe(await post<Me>('/auth/login', { login, password }));
    } catch (err: any) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="flex min-h-full flex-col items-center justify-center bg-ink-950 px-4 py-12">
      <div className="w-full max-w-[360px]">
        <div className="mb-8 flex flex-col items-center text-center">
          <img src="/kubepilot.svg" alt="" width={48} height={48} />
          <h1 className="mt-4 text-xl font-semibold tracking-tight">{t('login.title')}</h1>
          <p className="mt-1 text-[13px] text-ink-400">{t('login.subtitle')}</p>
        </div>
        <form onSubmit={submit} className="card space-y-4 p-6">
          {error && <ErrorBox error={error} />}
          <Field label={t('login.login')}>
            <input className="input" autoComplete="username" value={login} onChange={(e) => setLogin(e.target.value)} autoFocus required />
          </Field>
          <Field label={t('login.password')}>
            <input className="input" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
          </Field>
          <Button type="submit" variant="primary" loading={loading} className="!h-9 w-full">{t('login.submit')}</Button>
        </form>
        <p className="mt-6 text-center text-2xs text-ink-500">{t('login.footer')}</p>
        <div className="mt-4 flex justify-center"><LanguageSelect compact /></div>
      </div>
    </div>
  );
}
