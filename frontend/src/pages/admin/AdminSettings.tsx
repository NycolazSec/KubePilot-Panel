import { Save } from 'lucide-react';
import { useEffect, useState } from 'react';
import { get, put, Settings } from '../../api';
import { Button, Card, ErrorBox, Field, Loading, useLoad, useUI } from '../../components/ui';
import { useAuth } from '../../main';
import { t } from '../../i18n';

export default function AdminSettings() {
  const { toast } = useUI();
  const { refresh } = useAuth();
  const { data, error, loading } = useLoad(() => get<Settings>('/admin/settings'));
  const [s, setS] = useState<Settings | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (data) setS(data); }, [data]);

  if (error) return <ErrorBox error={error} />;
  if (loading || !s) return <Loading />;

  const save = async () => {
    setBusy(true);
    try { await put('/admin/settings', s); toast(t('adminSettings.saved')); refresh(); } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(false); }
  };
  return (
    <Card title={t('adminSettings.title')} actions={<Button size="sm" variant="primary" icon={<Save className="h-3.5 w-3.5" />} loading={busy} onClick={save}>{t('common.save')}</Button>}>
      <div className="grid max-w-3xl gap-5 sm:grid-cols-2">
        <Field label={t('adminSettings.panelName')}><input className="input" value={s.panelName} onChange={(e) => setS({ ...s, panelName: e.target.value })} /></Field>
        <Field label={t('adminSettings.timezone')} hint={t('adminSettings.timezoneHint')}><input className="input" value={s.timezone} onChange={(e) => setS({ ...s, timezone: e.target.value })} /></Field>
        <Field label={t('adminSettings.publicHost')} hint={t('adminSettings.publicHostHint')} className="sm:col-span-2">
          <input className="input" value={s.publicHost} onChange={(e) => setS({ ...s, publicHost: e.target.value })} placeholder={t('adminSettings.publicHostPlaceholder')} />
        </Field>
        <Field label={t('adminSettings.portMin')} hint={t('adminSettings.portRangeHint')}><input type="number" className="input" value={s.portMin} onChange={(e) => setS({ ...s, portMin: +e.target.value })} /></Field>
        <Field label={t('adminSettings.portMax')}><input type="number" className="input" value={s.portMax} onChange={(e) => setS({ ...s, portMax: +e.target.value })} /></Field>
      </div>
    </Card>
  );
}
