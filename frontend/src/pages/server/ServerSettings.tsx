import { RefreshCw, Save } from 'lucide-react';
import { useState } from 'react';
import { post, put } from '../../api';
import { Button, Card, Field, useUI } from '../../components/ui';
import { cpuLabel, dateTime, mb } from '../../lib/format';
import { useServer } from './ServerLayout';

export default function ServerSettings() {
  const { server, reload, state } = useServer();
  const { toast, confirm } = useUI();
  const [name, setName] = useState(server.name);
  const [desc, setDesc] = useState(server.description);
  const [busy, setBusy] = useState('');

  const save = async () => {
    setBusy('save');
    try { await put(`/servers/${server.id}/settings`, { name, description: desc }); toast('Enregistré'); reload(); } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(''); }
  };
  const reinstall = async () => {
    if (!(await confirm({ title: 'Réinstaller le serveur ?', body: 'Le script d\'installation du template sera relancé. Tes fichiers ne sont pas supprimés, mais certains peuvent être écrasés. Fais une sauvegarde avant.', confirm: 'Réinstaller', danger: true }))) return;
    setBusy('reinstall');
    try { await post(`/servers/${server.id}/reinstall`); toast('Réinstallation lancée : suis-la dans la console'); } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(''); }
  };

  return (
    <div className="grid gap-5 lg:grid-cols-2">
      <Card title="Général" actions={<Button size="sm" variant="primary" icon={<Save className="h-3.5 w-3.5" />} loading={busy === 'save'} onClick={save}>Enregistrer</Button>}>
        <div className="space-y-4">
          <Field label="Nom du serveur"><input className="input" value={name} onChange={(e) => setName(e.target.value)} maxLength={60} /></Field>
          <Field label="Description"><textarea className="input min-h-[90px]" value={desc} onChange={(e) => setDesc(e.target.value)} maxLength={300} /></Field>
        </div>
      </Card>
      <Card title="Informations">
        <dl className="space-y-2.5 text-sm">
          {[
            ['Identifiant', <code className="font-mono">{server.id}</code>],
            ['Template', server.template],
            ['Mémoire', mb(server.memory)],
            ['Processeur', cpuLabel(server.cpu)],
            ['Disque', mb(server.disk)],
            ['Sauvegardes max', server.backupLimit],
            ['Ports supplémentaires max', server.allocationLimit],
            ['Créé le', dateTime(server.createdAt)],
            ['Ressource Kubernetes', <code className="font-mono text-xs">kubepilot-servers/srv-{server.id}</code>],
          ].map(([k, v], i) => <div key={i} className="flex justify-between gap-4"><dt className="text-ink-400">{k}</dt><dd className="text-right text-ink-100">{v}</dd></div>)}
        </dl>
      </Card>
      <Card title="Zone sensible" className="lg:col-span-2">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div>
            <p className="font-medium">Réinstaller</p>
            <p className="text-sm text-ink-400">Relance le script d'installation du template. Le serveur doit être arrêté.</p>
          </div>
          <Button variant="danger" icon={<RefreshCw className="h-4 w-4" />} loading={busy === 'reinstall'} disabled={state.status !== 'offline' && state.status !== 'crashed'} onClick={reinstall}>Réinstaller</Button>
        </div>
      </Card>
    </div>
  );
}
