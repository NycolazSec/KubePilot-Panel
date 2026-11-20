import { Activity as ActIcon } from 'lucide-react';
import { Activity, get } from '../../api';
import { Card, Empty, ErrorBox, Loading, useLoad } from '../../components/ui';
import { ago, dateTime } from '../../lib/format';
import { useServer } from './ServerLayout';
import { t } from '../../i18n';

const labels: Record<string, string> = {
  'server.create': t('act.server.create'),
  'server.power.start': t('act.server.power.start'),
  'server.power.stop': t('act.server.power.stop'),
  'server.power.restart': t('act.server.power.restart'),
  'server.power.kill': t('act.server.power.kill'),
  'server.console.command': t('act.server.console.command'),
  'server.file.write': t('act.server.file.write'),
  'server.file.mkdir': t('act.server.file.mkdir'),
  'server.file.rename': t('act.server.file.rename'),
  'server.file.delete': t('act.server.file.delete'),
  'server.file.upload': t('act.server.file.upload'),
  'server.file.download': t('act.server.file.download'),
  'server.file.compress': t('act.server.file.compress'),
  'server.file.decompress': t('act.server.file.decompress'),
  'server.backup.create': t('act.server.backup.create'),
  'server.backup.restore': t('act.server.backup.restore'),
  'server.backup.delete': t('act.server.backup.delete'),
  'server.backup.download': t('act.server.backup.download'),
  'server.backup.lock': t('act.server.backup.lock'),
  'server.schedule.save': t('act.server.schedule.save'),
  'server.schedule.delete': t('act.server.schedule.delete'),
  'server.schedule.run': t('act.server.schedule.run'),
  'server.schedule.execute': t('act.server.schedule.execute'),
  'server.startup.update': t('act.server.startup.update'),
  'server.allocation.create': t('act.server.allocation.create'),
  'server.allocation.delete': t('act.server.allocation.delete'),
  'server.allocation.primary': t('act.server.allocation.primary'),
  'server.subuser.create': t('act.server.subuser.create'),
  'server.subuser.update': t('act.server.subuser.update'),
  'server.subuser.delete': t('act.server.subuser.delete'),
  'server.settings.rename': t('act.server.settings.rename'),
  'server.reinstall': t('act.server.reinstall'),
  'admin.server.update': t('act.admin.server.update'),
  'admin.server.delete': t('act.admin.server.delete'),
  'admin.user.create': t('act.admin.user.create'),
  'admin.user.update': t('act.admin.user.update'),
  'admin.user.delete': t('act.admin.user.delete'),
  'admin.template.save': t('act.admin.template.save'),
  'admin.template.delete': t('act.admin.template.delete'),
  'admin.settings.update': t('act.admin.settings.update'),
  'auth.login': t('act.auth.login'),
  'auth.fail': t('act.auth.fail'),
  'account.password': t('act.account.password'),
  'account.apikey.create': t('act.account.apikey.create'),
  'account.apikey.delete': t('act.account.apikey.delete'),
  'server.disk.stop': t('act.server.disk.stop'),
  'auth.2fa.fail': t('act.auth.2fa.fail'),
  'auth.2fa.recovery': t('act.auth.2fa.recovery'),
  'account.2fa.enable': t('act.account.2fa.enable'),
  'account.2fa.disable': t('act.account.2fa.disable'),
  'account.2fa.recovery': t('act.account.2fa.recovery'),
  'admin.user.2fa.reset': t('act.admin.user.2fa.reset'),
};
export const actLabel = (a: string) => labels[a] || a;
// actions done by the panel itself (schedules…) have no user
const who = (a: Activity) => (a.userId ? a.username : t('activity.system'));

export function ActivityList({ items, showServer }: { items: Activity[]; showServer?: boolean }) {
  if (!items.length) return <Empty icon={<ActIcon className="h-6 w-6" />} title={t('activity.empty')} />;
  return (
    <ul className="divide-y divide-ink-800">
      {items.map((a) => (
        <li key={a.id} className="flex items-start gap-3 px-5 py-3 text-sm">
          <div className="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-ink-800 text-[10px] font-semibold text-ink-300">{who(a).slice(0, 2).toUpperCase()}</div>
          <div className="min-w-0 flex-1">
            <p><span className="font-medium text-ink-100">{who(a)}</span>{a.apiKey && <span className="ml-1.5 rounded bg-brand-500/10 px-1.5 py-0.5 text-2xs font-medium text-brand-300 ring-1 ring-inset ring-brand-500/25">{t('activity.viaKey', { name: a.apiKey })}</span>} <span className="text-ink-300">· {actLabel(a.action)}</span>{showServer && a.serverId && <span className="ml-1 font-mono text-xs text-ink-500">#{a.serverId}</span>}</p>
            {a.meta && <p className="mt-0.5 truncate font-mono text-xs text-ink-400">{a.meta}</p>}
          </div>
          <div className="shrink-0 text-right text-xs text-ink-500" title={dateTime(a.createdAt)}>{ago(a.createdAt)}{a.ip && <div className="font-mono">{a.ip}</div>}</div>
        </li>
      ))}
    </ul>
  );
}

export default function ActivityPage() {
  const { server } = useServer();
  const { data, error, loading } = useLoad(() => get<Activity[]>(`/servers/${server.id}/activity`), [server.id]);
  return (
    <Card padded={false} title={t('activity.title')}>
      {error ? <div className="p-4"><ErrorBox error={error} /></div> : loading || !data ? <Loading /> : <ActivityList items={data} />}
    </Card>
  );
}
