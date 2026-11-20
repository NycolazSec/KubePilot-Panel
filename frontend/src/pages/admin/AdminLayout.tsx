import { Outlet, useLocation } from 'react-router-dom';
import { PageHeader } from '../../components/ui';
import { t } from '../../i18n';

const sections: Record<string, [string, string]> = {
  '': [t('nav.overview'), t('admin.overviewHint')],
  servers: [t('nav.servers'), t('admin.serversHint')],
  users: [t('nav.users'), t('admin.usersHint')],
  templates: [t('nav.templates'), t('admin.templatesHint')],
  settings: [t('nav.settings'), t('admin.settingsHint')],
  activity: [t('nav.activity'), t('admin.activityHint')],
};

export default function AdminLayout() {
  const key = useLocation().pathname.replace(/^\/admin\/?/, '').split('/')[0];
  const [title, subtitle] = sections[key] || sections[''];
  return (
    <>
      <p className="mb-1 text-2xs font-medium uppercase tracking-wider text-ink-500">{t('nav.admin')}</p>
      <PageHeader title={title} subtitle={subtitle} />
      <Outlet />
    </>
  );
}
