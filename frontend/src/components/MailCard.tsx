import { Mail, MailX, Send } from 'lucide-react';
import { useState } from 'react';
import { get, post } from '../api';
import { t } from '../i18n';
import { Badge, Button, Card, ErrorBox, Loading, useLoad, useUI } from './ui';

type MailStatus = { enabled: boolean; host?: string; port?: number; tls?: string; from?: string; auth?: boolean; publicURL: string; passwordReset?: boolean };

/** How the panel sends emails (configured with the Helm chart: the SMTP password never reaches the browser). */
export default function MailCard() {
  const { toast } = useUI();
  const { data, error } = useLoad(() => get<MailStatus>('/admin/mail'));
  const [busy, setBusy] = useState(false);
  const test = async () => {
    setBusy(true);
    try { const r = await post<{ sentTo: string }>('/admin/mail/test'); toast(t('mail.testSent', { email: r.sentTo })); } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(false); }
  };
  return (
    <Card title={t('mail.title')} description={t('mail.description')}
      actions={data?.enabled && <Button size="sm" icon={<Send className="h-3.5 w-3.5" />} loading={busy} onClick={test}>{t('mail.test')}</Button>}>
      {error ? <ErrorBox error={error} /> : !data ? <Loading /> : !data.enabled ? (
        <div className="flex items-start gap-3 text-sm">
          <MailX className="mt-0.5 h-5 w-5 shrink-0 text-ink-400" />
          <div><p className="font-medium text-ink-100">{t('mail.off')}</p><p className="mt-1 text-ink-400">{t('mail.offHint')}</p></div>
        </div>
      ) : (
        <div className="flex items-start gap-3 text-sm">
          <Mail className="mt-0.5 h-5 w-5 shrink-0 text-brand-300" />
          <div className="min-w-0 space-y-1.5">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium text-ink-100">{data.from}</span>
              <Badge color={data.tls === 'none' ? 'yellow' : 'green'}>{data.tls === 'none' ? t('mail.noTLS') : data.tls === 'tls' ? 'TLS' : 'STARTTLS'}</Badge>
              {data.passwordReset ? <Badge color="violet">{t('mail.resetOn')}</Badge> : <Badge color="yellow">{t('mail.resetOff')}</Badge>}
            </div>
            <p className="truncate font-mono text-xs text-ink-300">{data.host}:{data.port}</p>
            <p className="text-xs text-ink-500">{data.passwordReset ? t('mail.linksTo', { url: data.publicURL }) : t('mail.noPublicURL')}</p>
          </div>
        </div>
      )}
    </Card>
  );
}
