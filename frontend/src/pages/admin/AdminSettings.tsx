import { Save } from 'lucide-react';
import { useEffect, useState } from 'react';
import { get, put, Settings } from '../../api';
import { Button, Card, ErrorBox, Field, Loading, useLoad, useUI } from '../../components/ui';
import { useAuth } from '../../main';

export default function AdminSettings() {
  const { toast } = useUI();
  const { refresh } = useAuth();
  const { data, error, loading } = useLoad(() => get<Settings>('/admin/settings'));
  const [s, setS] = useState<Settings | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (data) setS(data); }, [data]);

  if (error) return <ErrorBox error={error} />;
  if (loading || !s) return <Loading />;

  const save = async () => {
    setBusy(true);
    try { await put('/admin/settings', s); toast('Réglages enregistrés'); refresh(); } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(false); }
  };
  return (
    <Card title="Réglages du panel" actions={<Button size="sm" variant="primary" icon={<Save className="h-3.5 w-3.5" />} loading={busy} onClick={save}>Enregistrer</Button>}>
      <div className="grid max-w-3xl gap-5 sm:grid-cols-2">
        <Field label="Nom du panel"><input className="input" value={s.panelName} onChange={(e) => setS({ ...s, panelName: e.target.value })} /></Field>
        <Field label="Fuseau horaire" hint="Utilisé pour les tâches planifiées et les conteneurs (TZ)."><input className="input" value={s.timezone} onChange={(e) => setS({ ...s, timezone: e.target.value })} /></Field>
        <Field label="Adresse publique des serveurs" hint="Domaine ou IP affiché aux joueurs (ex : play.mondomaine.com). Vide = IP du nœud." className="sm:col-span-2">
          <input className="input" value={s.publicHost} onChange={(e) => setS({ ...s, publicHost: e.target.value })} placeholder="play.exemple.com" />
        </Field>
        <Field label="Premier port" hint="Plage NodePort Kubernetes : 30000-32767"><input type="number" className="input" value={s.portMin} onChange={(e) => setS({ ...s, portMin: +e.target.value })} /></Field>
        <Field label="Dernier port"><input type="number" className="input" value={s.portMax} onChange={(e) => setS({ ...s, portMax: +e.target.value })} /></Field>
      </div>
    </Card>
  );
}
