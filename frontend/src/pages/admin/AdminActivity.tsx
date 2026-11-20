import { Activity, get } from '../../api';
import { Card, ErrorBox, Loading, useLoad } from '../../components/ui';
import { ActivityList } from '../server/ActivityPage';
import { t } from '../../i18n';

export default function AdminActivity() {
  const { data, error, loading } = useLoad(() => get<Activity[]>('/admin/activity'));
  return (
    <Card padded={false} title={t('admin.activityTitle')}>
      {error ? <div className="p-4"><ErrorBox error={error} /></div> : loading || !data ? <Loading /> : <ActivityList items={data} showServer />}
    </Card>
  );
}
