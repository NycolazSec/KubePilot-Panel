import { KeyRound } from 'lucide-react';
import { useState } from 'react';
import { put } from '../api';
import { Button, Card, Field, PageHeader, useUI } from '../components/ui';
import { dateTime } from '../lib/format';
import { useAuth } from '../main';
import LanguageSelect from '../components/LanguageSelect';
import { t } from '../i18n';

export default function Account() {
  const { me } = useAuth();
  const { toast } = useUI();
  const [cur, setCur] = useState('');
  const [pw, setPw] = useState('');
  const [pw2, setPw2] = useState('');
  const [busy, setBusy] = useState(false);

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    if (pw !== pw2) return toast(t('account.mismatch'), 'err');
    setBusy(true);
    try {
      await put('/account/password', { current: cur, new: pw });
      toast(t('account.passwordChanged'));
      setCur(''); setPw(''); setPw2('');
    } catch (err: any) {
      toast(err.message, 'err');
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <PageHeader title={t('nav.account')} subtitle={t('account.subtitle')} />
      <div className="grid gap-6 lg:grid-cols-2">
        <Card title={t('account.profile')}>
          <dl className="space-y-3 text-sm">
            <div className="flex justify-between"><dt className="text-ink-400">{t('account.username')}</dt><dd>{me?.user.username}</dd></div>
            <div className="flex justify-between"><dt className="text-ink-400">{t('account.email')}</dt><dd>{me?.user.email}</dd></div>
            <div className="flex justify-between"><dt className="text-ink-400">{t('account.role')}</dt><dd>{me?.user.isAdmin ? t('role.admin') : t('role.user')}</dd></div>
            <div className="flex justify-between"><dt className="text-ink-400">{t('account.memberSince')}</dt><dd>{dateTime(me?.user.createdAt || 0)}</dd></div>
          </dl>
        </Card>
        <Card title={t('account.changePassword')}>
          <form onSubmit={save} className="space-y-4">
            <Field label={t('account.currentPassword')}><input type="password" className="input" value={cur} onChange={(e) => setCur(e.target.value)} autoComplete="current-password" required /></Field>
            <Field label={t('account.newPassword')} hint={t('account.passwordHint')}><input type="password" className="input" value={pw} onChange={(e) => setPw(e.target.value)} autoComplete="new-password" minLength={8} required /></Field>
            <Field label={t('account.confirmPassword')}><input type="password" className="input" value={pw2} onChange={(e) => setPw2(e.target.value)} autoComplete="new-password" required /></Field>
            <Button type="submit" variant="primary" loading={busy} icon={<KeyRound className="h-4 w-4" />}>{t('account.update')}</Button>
          </form>
        </Card>
        <Card title={t('account.language')} description={t('account.languageHint')}>
          <LanguageSelect />
        </Card>
      </div>
    </>
  );
}
