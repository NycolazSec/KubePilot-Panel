import { Outlet, useLocation } from 'react-router-dom';
import { PageHeader } from '../../components/ui';

const sections: Record<string, [string, string]> = {
  '': ['Vue d\'ensemble', 'État du cluster Kubernetes et des ressources allouées.'],
  cluster: ['Cluster', 'Toutes les applications et tous les pods de Kubernetes : état, logs en direct et actions.'],
  servers: ['Serveurs', 'Création, ressources et cycle de vie de tous les serveurs.'],
  users: ['Utilisateurs', 'Comptes et rôles du panel.'],
  templates: ['Templates', 'Modèles de serveurs : images, démarrage, variables et ports.'],
  settings: ['Réglages', 'Configuration générale du panel.'],
  activity: ['Journal', 'Historique des actions sur tout le panel.'],
};

export default function AdminLayout() {
  const key = useLocation().pathname.replace(/^\/admin\/?/, '').split('/')[0];
  const [title, subtitle] = sections[key] || sections[''];
  return (
    <>
      <p className="mb-1 text-2xs font-medium uppercase tracking-wider text-ink-500">Administration</p>
      <PageHeader title={title} subtitle={subtitle} />
      <Outlet />
    </>
  );
}
