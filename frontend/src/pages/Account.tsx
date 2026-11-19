import { KeyRound } from 'lucide-react';
import { useState } from 'react';
import { put } from '../api';
import { Button, Card, Field, PageHeader, useUI } from '../components/ui';
import { dateTime } from '../lib/format';
import { useAuth } from '../main';

export default function Account() {
  const { me } = useAuth();
  const { toast } = useUI();
  const [cur, setCur] = useState('');
  const [pw, setPw] = useState('');
  const [pw2, setPw2] = useState('');
  const [busy, setBusy] = useState(false);

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    if (pw !== pw2) return toast('Les deux mots de passe ne correspondent pas', 'err');
    setBusy(true);
    try {
      await put('/account/password', { current: cur, new: pw });
      toast('Mot de passe modifié. Les autres sessions ont été déconnectées.');
      setCur(''); setPw(''); setPw2('');
    } catch (err: any) {
      toast(err.message, 'err');
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <PageHeader title="Mon compte" subtitle="Tes informations et ta sécurité." />
      <div className="grid gap-6 lg:grid-cols-2">
        <Card title="Profil">
          <dl className="space-y-3 text-sm">
            <div className="flex justify-between"><dt className="text-ink-400">Nom d'utilisateur</dt><dd>{me?.user.username}</dd></div>
            <div className="flex justify-between"><dt className="text-ink-400">Email</dt><dd>{me?.user.email}</dd></div>
            <div className="flex justify-between"><dt className="text-ink-400">Rôle</dt><dd>{me?.user.isAdmin ? 'Administrateur' : 'Utilisateur'}</dd></div>
            <div className="flex justify-between"><dt className="text-ink-400">Membre depuis</dt><dd>{dateTime(me?.user.createdAt || 0)}</dd></div>
          </dl>
        </Card>
        <Card title="Changer de mot de passe">
          <form onSubmit={save} className="space-y-4">
            <Field label="Mot de passe actuel"><input type="password" className="input" value={cur} onChange={(e) => setCur(e.target.value)} autoComplete="current-password" required /></Field>
            <Field label="Nouveau mot de passe" hint="8 caractères minimum."><input type="password" className="input" value={pw} onChange={(e) => setPw(e.target.value)} autoComplete="new-password" minLength={8} required /></Field>
            <Field label="Confirmer"><input type="password" className="input" value={pw2} onChange={(e) => setPw2(e.target.value)} autoComplete="new-password" required /></Field>
            <Button type="submit" variant="primary" loading={busy} icon={<KeyRound className="h-4 w-4" />}>Mettre à jour</Button>
          </form>
        </Card>
      </div>
    </>
  );
}
