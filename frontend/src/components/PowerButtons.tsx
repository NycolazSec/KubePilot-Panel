import { Play, RotateCw, Skull, Square } from 'lucide-react';
import { useState } from 'react';
import { post, Status } from '../api';
import { Button, useUI } from './ui';

export default function PowerButtons({ id, status, size = 'md', onDone }: { id: string; status: Status; size?: 'sm' | 'md'; onDone?: () => void }) {
  const { toast, confirm } = useUI();
  const [busy, setBusy] = useState('');
  const act = async (action: string) => {
    if (action === 'kill' && !(await confirm({ title: 'Forcer l\'arrêt ?', body: 'Le processus sera tué immédiatement, sans sauvegarde. Les données non enregistrées seront perdues.', confirm: 'Tuer le processus', danger: true }))) return;
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
  const off = status === 'offline' || status === 'crashed';
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Button size={size} variant="primary" icon={<Play />} disabled={up || status === 'installing' || status === 'stopping'} loading={busy === 'start'} onClick={() => act('start')}>Démarrer</Button>
      <Button size={size} icon={<RotateCw />} disabled={off || status === 'installing'} loading={busy === 'restart'} onClick={() => act('restart')}>Redémarrer</Button>
      {status === 'stopping' ? (
        <Button size={size} variant="danger" icon={<Skull />} loading={busy === 'kill'} onClick={() => act('kill')}>Forcer l'arrêt</Button>
      ) : (
        <Button size={size} variant="danger" icon={<Square />} disabled={off || status === 'installing'} loading={busy === 'stop'} onClick={() => act('stop')}>Arrêter</Button>
      )}
    </div>
  );
}
