import { Moon, RefreshCw, Save } from 'lucide-react';
import { useState } from 'react';
import { post, put } from '../../api';
import { Button, Card, Field, Toggle, useUI } from '../../components/ui';
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
            [t('settings.maxDatabases'), server.databaseLimit],
            [t('settings.maxPorts'), server.allocationLimit],
            [t('settings.createdAt'), dateTime(server.createdAt)],
            [t('settings.k8sResource'), <code className="font-mono text-xs">kubepilot-servers/srv-{server.id}</code>],
          ].map(([k, v], i) => <div key={i} className="flex justify-between gap-4"><dt className="text-ink-400">{k}</dt><dd className="text-right text-ink-100">{v}</dd></div>)}
        </dl>
      </Card>
      {(server.autoSleepAvailable || server.autoSleep > 0) && <AutoSleepCard />}
      <Card title={t('settings.danger')} className="lg:col-span-2">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div>
            <p className="font-medium">{t('settings.reinstall')}</p>
            <p className="text-sm text-ink-400">{t('settings.reinstallHint')}</p>
          </div>
          <Button variant="danger" icon={<RefreshCw className="h-4 w-4" />} loading={busy === 'reinstall'} disabled={state.status !== 'offline' && state.status !== 'crashed' && state.status !== 'sleeping'} onClick={reinstall}>{t('settings.reinstall')}</Button>
        </div>
      </Card>
    </div>
  );
}

const sleepDelays = [5, 10, 15, 30, 60, 120];

// Auto-sleep: the server stops when nobody plays and mc-router starts it again when a player joins its address.
function AutoSleepCard() {
  const { server, reload, state } = useServer();
  const { toast } = useUI();
  const [busy, setBusy] = useState(false);
  const routed = server.hostname && !server.internalOnly ? `${server.hostname}.${server.routerDomain}` : '';
  const delays = sleepDelays.includes(server.autoSleep) || !server.autoSleep ? sleepDelays : [...sleepDelays, server.autoSleep].sort((a, b) => a - b);
  const save = async (minutes: number) => {
    setBusy(true);
    try {
      await put(`/servers/${server.id}/auto-sleep`, { minutes });
      toast(minutes ? t('sleep.saved') : t('sleep.off'));
      reload();
    } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(false); }
  };
  const on = server.autoSleep > 0;
  return (
    <Card title={t('sleep.title')} description={t('sleep.description')} className="lg:col-span-2"
      actions={<Toggle checked={on} label={t('sleep.enable')} onChange={(v) => !busy && (v ? routed && save(server.autoSleep || 10) : save(0))} />}>
      {!routed ? <p className="text-sm text-amber-300">{t('sleep.needsAddress')}</p> : (
        <div className="space-y-3 text-sm">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-ink-400">{t('sleep.delay')}</span>
            <select className="input !w-auto !py-1.5 text-sm" value={server.autoSleep || 10} disabled={!on || busy} onChange={(e) => save(Number(e.target.value))}>
              {delays.map((m) => <option key={m} value={m}>{t('sleep.minutes', { n: m })}</option>)}
            </select>
          </div>
          {state.status === 'sleeping' && <p className="inline-flex items-center gap-2 text-brand-300"><Moon className="h-4 w-4" />{t('sleep.asleepNow')}</p>}
          <p className="text-xs text-ink-400">{t('sleep.wakeAddress', { address: routed })}</p>
        </div>
      )}
    </Card>
  );
}
