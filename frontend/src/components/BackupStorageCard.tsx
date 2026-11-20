import { Cloud, HardDrive, Lock } from 'lucide-react';
import { get } from '../api';
import { t } from '../i18n';
import { Badge, Card, ErrorBox, Loading, useLoad } from './ui';

type Storage = { storage: 'local' | 's3'; endpoint?: string; bucket?: string; prefix?: string; encrypted?: boolean; status?: 'ok' | 'unreachable' };

/** Where new backups go (configured with the Helm chart: credentials never reach the browser). */
export default function BackupStorageCard() {
  const { data, error } = useLoad(() => get<Storage>('/admin/backup-storage'));
  return (
    <Card title={t('bstore.title')} description={t('bstore.description')}>
      {error ? <ErrorBox error={error} /> : !data ? <Loading /> : data.storage === 'local' ? (
        <div className="flex items-start gap-3 text-sm">
          <HardDrive className="mt-0.5 h-5 w-5 shrink-0 text-ink-400" />
          <div><p className="font-medium text-ink-100">{t('bstore.local')}</p><p className="mt-1 text-ink-400">{t('bstore.localHint')}</p></div>
        </div>
      ) : (
        <div className="flex items-start gap-3 text-sm">
          <Cloud className="mt-0.5 h-5 w-5 shrink-0 text-brand-300" />
          <div className="min-w-0 space-y-1.5">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium text-ink-100">S3</span>
              {data.status === 'ok' ? <Badge color="green">{t('bstore.ok')}</Badge> : <Badge color="red">{t('bstore.unreachable')}</Badge>}
              {data.encrypted ? <Badge color="violet"><Lock className="h-3 w-3" /> {t('bstore.encrypted')}</Badge> : <Badge>{t('bstore.notEncrypted')}</Badge>}
            </div>
            <p className="truncate font-mono text-xs text-ink-300">{data.endpoint}/{data.bucket}/{data.prefix}</p>
            <p className="text-xs text-ink-500">{data.encrypted ? t('bstore.encryptedHint') : t('bstore.notEncryptedHint')}</p>
          </div>
        </div>
      )}
    </Card>
  );
}
