import { Copy, ExternalLink, KeyRound, ShieldCheck } from 'lucide-react';
import { Link } from 'react-router-dom';
import { get } from '../api';
import { t } from '../i18n';
import { copy } from '../lib/format';
import { ErrorBox, Loading, Modal, useLoad, useUI } from './ui';

type Info = { enabled: boolean; host: string; port: number; username: string; fingerprint: string; passwordAllowed: boolean };

/** How to connect to a server over SFTP (FileZilla, WinSCP, sftp). */
export default function SFTPDialog({ serverId, open, onClose }: { serverId: string; open: boolean; onClose: () => void }) {
  const { toast } = useUI();
  const { data, error } = useLoad(() => (open ? get<Info>(`/servers/${serverId}/sftp`) : Promise.resolve(null)), [serverId, open]);
  const Row = ({ label, value, mono = true }: { label: string; value: string; mono?: boolean }) => (
    <div className="flex items-center justify-between gap-3 border-b border-ink-800 py-2.5 last:border-0">
      <span className="text-sm text-ink-400">{label}</span>
      <button type="button" onClick={async () => { await copy(value); toast(t('twofa.copied')); }}
        className={`inline-flex min-w-0 items-center gap-2 rounded px-1.5 py-0.5 text-right text-sm text-ink-100 hover:bg-ink-800 ${mono ? 'font-mono' : ''}`}>
        <span className="truncate">{value}</span><Copy className="h-3.5 w-3.5 shrink-0 text-ink-500" />
      </button>
    </div>
  );
  return (
    <Modal open={open} onClose={onClose} title={t('sftp.title')}>
      {error ? <ErrorBox error={error} /> : !data ? <Loading /> : !data.enabled ? <p className="text-sm text-ink-300">{t('sftp.disabled')}</p> : (
        <div className="space-y-4">
          <p className="text-sm text-ink-300">{t('sftp.intro')}</p>
          <div className="rounded-md border border-ink-700 px-3">
            <Row label={t('sftp.host')} value={data.host} />
            <Row label={t('sftp.port')} value={String(data.port)} />
            <Row label={t('sftp.username')} value={data.username} />
          </div>
          {data.passwordAllowed ? (
            <p className="flex items-start gap-2 text-sm text-ink-300"><KeyRound className="mt-0.5 h-4 w-4 shrink-0 text-ink-400" />{t('sftp.password')}</p>
          ) : (
            <p className="flex items-start gap-2 rounded-md border border-amber-500/30 bg-amber-500/[.07] px-3 py-2.5 text-[13px] text-amber-200">
              <ShieldCheck className="mt-0.5 h-4 w-4 shrink-0 text-amber-400" />
              <span>{t('sftp.keyRequired')} <Link to="/account" onClick={onClose} className="underline">{t('sftp.addKey')}</Link></span>
            </p>
          )}
          <a href={`sftp://${encodeURIComponent(data.username)}@${data.host}:${data.port}/`} className="inline-flex h-8 w-full items-center justify-center gap-1.5 rounded-md border border-brand-500/60 bg-brand-600 px-3 text-[13px] font-medium text-white hover:bg-brand-500">
            <ExternalLink className="h-4 w-4" />{t('sftp.open')}
          </a>
          <div>
            <span className="label">{t('sftp.fingerprint')}</span>
            <p className="break-all font-mono text-xs text-ink-400">{data.fingerprint}</p>
            <p className="mt-1 text-xs text-ink-500">{t('sftp.fingerprintHint')}</p>
          </div>
        </div>
      )}
    </Modal>
  );
}
