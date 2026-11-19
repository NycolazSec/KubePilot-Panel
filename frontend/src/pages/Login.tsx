import { KeyRound, User } from 'lucide-react';
import { useState } from 'react';
import { Me, post } from '../api';
import { Logo } from '../components/Layout';
import { Button, ErrorBox } from '../components/ui';
import { useAuth } from '../main';

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
    <div className="app-bg relative flex min-h-full items-center justify-center overflow-hidden px-4 py-12">
      <div className="pointer-events-none absolute -left-32 top-1/4 h-96 w-96 rounded-full bg-brand-600/20 blur-3xl" />
      <div className="pointer-events-none absolute -right-32 bottom-0 h-96 w-96 rounded-full bg-sky2-500/10 blur-3xl" />
      <div className="relative w-full max-w-sm animate-fade-in">
        <div className="mb-8 flex justify-center"><Logo name="KubePilot" /></div>
        <form onSubmit={submit} className="card space-y-4 p-6 shadow-2xl">
          <div className="mb-2">
            <h1 className="text-xl font-semibold">Connexion</h1>
            <p className="mt-1 text-sm text-ink-400">Gère tes serveurs de jeu, propulsés par Kubernetes.</p>
          </div>
          {error && <ErrorBox error={error} />}
          <div className="relative">
            <User className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-ink-400" />
            <input className="input pl-9" placeholder="Nom d'utilisateur ou email" autoComplete="username" value={login} onChange={(e) => setLogin(e.target.value)} autoFocus required />
          </div>
          <div className="relative">
            <KeyRound className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-ink-400" />
            <input className="input pl-9" type="password" placeholder="Mot de passe" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
          </div>
          <Button type="submit" variant="primary" loading={loading} className="w-full">Se connecter</Button>
        </form>
      </div>
    </div>
  );
}
