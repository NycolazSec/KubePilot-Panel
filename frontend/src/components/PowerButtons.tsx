import { Play, RotateCw, Skull, Square } from 'lucide-react';
import { useState } from 'react';
import { post, Status } from '../api';
import { Button, useUI } from './ui';
import { t } from '../i18n';

export default function PowerButtons({ id, status, size = 'md', onDone }: { id: string; status: Status; size?: 'sm' | 'md'; onDone?: () => void }) {
  const { toast, confirm } = useUI();
  const [busy, setBusy] = useState('');
  const act = async (action: string) => {
    if (action === 'kill' && !(await confirm({ title: t('power.killConfirmTitle'), body: t('power.killConfirmBody'), confirm: t('power.killConfirm'), danger: true }))) return;
    setBusy(action);
    try {
      await post(`/servers/${id}/power`, { action });
      onDone?.();
    } catch (e: any) {
      toast(e.message, 'err');
    } finally {
      setBusy('');
    }
  };
  const up = status === 'running' || status === 'starting';
  const off = status === 'offline' || status === 'crashed' || status === 'sleeping';
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Button size={size} variant="primary" icon={<Play />} disabled={up || status === 'installing' || status === 'stopping'} loading={busy === 'start'} onClick={() => act('start')}>{t('power.start')}</Button>
      <Button size={size} icon={<RotateCw />} disabled={off || status === 'installing'} loading={busy === 'restart'} onClick={() => act('restart')}>{t('power.restart')}</Button>
      {status === 'stopping' ? (
        <Button size={size} variant="danger" icon={<Skull />} loading={busy === 'kill'} onClick={() => act('kill')}>{t('power.kill')}</Button>
      ) : (
        <Button size={size} variant="danger" icon={<Square />} disabled={(off && status !== 'sleeping') || status === 'installing'} loading={busy === 'stop'} onClick={() => act('stop')}>{t('power.stop')}</Button>
      )}
    </div>
  );
}
