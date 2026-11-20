import { RefreshCw, Save } from 'lucide-react';
import { useState } from 'react';
import { post, put } from '../../api';
import { Button, Card, Field, useUI } from '../../components/ui';
import { cpuLabel, dateTime, mb } from '../../lib/format';
import { useServer } from './ServerLayout';
import { t } from '../../i18n';

export default function ServerSettings() {
  const { server, reload, state } = useServer();
  const { toast, confirm } = useUI();
  const [name, setName] = useState(server.name);
  const [desc, setDesc] = useState(server.description);
  const [busy, setBusy] = useState('');

  const save = async () => {
    setBusy('save');
    try { await put(`/servers/${server.id}/settings`, { name, description: desc }); toast(t('common.saved')); reload(); } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(''); }
  };
  const reinstall = async () => {
    if (!(await confirm({ title: t('settings.reinstallTitle'), body: t('settings.reinstallBody'), confirm: t('settings.reinstall'), danger: true }))) return;
    setBusy('reinstall');
    try { await post(`/servers/${server.id}/reinstall`); toast(t('settings.reinstallStarted')); } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(''); }
  };

  return (
    <div className="grid gap-5 lg:grid-cols-2">
      <Card title={t('settings.general')} actions={<Button size="sm" variant="primary" icon={<Save className="h-3.5 w-3.5" />} loading={busy === 'save'} onClick={save}>{t('common.save')}</Button>}>
        <div className="space-y-4">
          <Field label={t('settings.serverName')}><input className="input" value={name} onChange={(e) => setName(e.target.value)} maxLength={60} /></Field>
          <Field label={t('common.description')}><textarea className="input min-h-[90px]" value={desc} onChange={(e) => setDesc(e.target.value)} maxLength={300} /></Field>
        </div>
      </Card>
      <Card title={t('settings.info')}>
        <dl className="space-y-2.5 text-sm">
          {[
            [t('settings.id'), <code className="font-mono">{server.id}</code>],
            [t('settings.template'), server.template],
            [t('common.memory'), mb(server.memory)],
            [t('console.cpu'), cpuLabel(server.cpu)],
            [t('common.disk'), mb(server.disk)],
            [t('settings.maxBackups'), server.backupLimit],
            [t('settings.maxPorts'), server.allocationLimit],
            [t('settings.createdAt'), dateTime(server.createdAt)],
            [t('settings.k8sResource'), <code className="font-mono text-xs">kubepilot-servers/srv-{server.id}</code>],
          ].map(([k, v], i) => <div key={i} className="flex justify-between gap-4"><dt className="text-ink-400">{k}</dt><dd className="text-right text-ink-100">{v}</dd></div>)}
        </dl>
      </Card>
      <Card title={t('settings.danger')} className="lg:col-span-2">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div>
            <p className="font-medium">{t('settings.reinstall')}</p>
            <p className="text-sm text-ink-400">{t('settings.reinstallHint')}</p>
          </div>
          <Button variant="danger" icon={<RefreshCw className="h-4 w-4" />} loading={busy === 'reinstall'} disabled={state.status !== 'offline' && state.status !== 'crashed'} onClick={reinstall}>{t('settings.reinstall')}</Button>
        </div>
      </Card>
    </div>
  );
}
