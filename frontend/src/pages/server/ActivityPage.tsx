import { Activity as ActIcon } from 'lucide-react';
import { Activity, get } from '../../api';
import { Card, Empty, ErrorBox, Loading, useLoad } from '../../components/ui';
import { ago, dateTime } from '../../lib/format';
import { useServer } from './ServerLayout';

const labels: Record<string, string> = {
  'server.create': 'Serveur créé', 'server.power.start': 'Démarrage', 'server.power.stop': 'Arrêt', 'server.power.restart': 'Redémarrage', 'server.power.kill': 'Arrêt forcé',
  'server.console.command': 'Commande console', 'server.file.write': 'Fichier modifié', 'server.file.mkdir': 'Dossier créé', 'server.file.rename': 'Renommé',
  'server.file.delete': 'Supprimé', 'server.file.upload': 'Fichiers envoyés', 'server.file.download': 'Téléchargement', 'server.file.compress': 'Archive créée',
  'server.file.decompress': 'Archive extraite', 'server.backup.create': 'Sauvegarde créée', 'server.backup.restore': 'Sauvegarde restaurée', 'server.backup.delete': 'Sauvegarde supprimée',
  'server.backup.download': 'Sauvegarde téléchargée', 'server.backup.lock': 'Verrou de sauvegarde', 'server.schedule.save': 'Tâche enregistrée', 'server.schedule.delete': 'Tâche supprimée',
  'server.schedule.run': 'Tâche lancée', 'server.schedule.execute': 'Tâche exécutée', 'server.startup.update': 'Variables modifiées', 'server.allocation.create': 'Port ajouté',
  'server.allocation.delete': 'Port retiré', 'server.allocation.primary': 'Port principal changé', 'server.subuser.create': 'Accès donné', 'server.subuser.update': 'Accès modifié',
  'server.subuser.delete': 'Accès retiré', 'server.settings.rename': 'Renommé', 'server.reinstall': 'Réinstallation', 'admin.server.update': 'Modifié par un admin',
  'admin.server.delete': 'Serveur supprimé', 'admin.user.create': 'Utilisateur créé', 'admin.user.update': 'Utilisateur modifié', 'admin.user.delete': 'Utilisateur supprimé',
  'admin.template.save': 'Template enregistré', 'admin.template.delete': 'Template supprimé', 'admin.settings.update': 'Réglages modifiés',
  'auth.login': 'Connexion', 'auth.fail': 'Échec de connexion', 'account.password': 'Mot de passe changé',
};
export const actLabel = (a: string) => labels[a] || a;

export function ActivityList({ items, showServer }: { items: Activity[]; showServer?: boolean }) {
  if (!items.length) return <Empty icon={<ActIcon className="h-6 w-6" />} title="Aucune activité" />;
  return (
    <ul className="divide-y divide-ink-800">
      {items.map((a) => (
        <li key={a.id} className="flex items-start gap-3 px-5 py-3 text-sm">
          <div className="mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-ink-800 text-[10px] font-semibold text-ink-300">{a.username.slice(0, 2).toUpperCase()}</div>
          <div className="min-w-0 flex-1">
            <p><span className="font-medium text-ink-100">{a.username}</span> <span className="text-ink-300">· {actLabel(a.action)}</span>{showServer && a.serverId && <span className="ml-1 font-mono text-xs text-ink-500">#{a.serverId}</span>}</p>
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
    <Card padded={false} title="Journal d'activité">
      {error ? <div className="p-4"><ErrorBox error={error} /></div> : loading || !data ? <Loading /> : <ActivityList items={data} />}
    </Card>
  );
}
