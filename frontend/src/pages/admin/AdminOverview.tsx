import { Boxes, Cpu, HardDrive, History, MemoryStick, Network, Server, Users } from 'lucide-react';
import { useEffect } from 'react';
import { get, NodeInfo } from '../../api';
import { Badge, Bar, Card, ErrorBox, Loading, useLoad } from '../../components/ui';
import { bytes, cpuLabel, mb, statusLabel } from '../../lib/format';
import { t } from '../../i18n';

type Overview = {
  nodes: NodeInfo[]; servers: number; users: number; statuses: Record<string, number>;
  memoryAllocated: number; cpuAllocated: number; diskAllocated: number; diskUsed: number; backupBytes: number;
  portsUsed: number; portsTotal: number; namespace: string; version: string;
};

function Tile({ icon, label, value, sub }: { icon: React.ReactNode; label: string; value: React.ReactNode; sub?: React.ReactNode }) {
  return (
    <div className="card px-4 py-3">
      <div className="flex items-center gap-1.5 text-2xs font-medium uppercase tracking-wider text-ink-400 [&_svg]:h-3.5 [&_svg]:w-3.5">{icon}{label}</div>
      <div className="mt-1 text-lg font-semibold tabular-nums tracking-tight">{value}</div>
      {sub && <div className="mt-0.5 text-xs text-ink-400">{sub}</div>}
    </div>
  );
}

export default function AdminOverview() {
  const { data, error, loading, reload } = useLoad(() => get<Overview>('/admin/overview'));
  useEffect(() => {
    const t = setInterval(() => reload(true), 10000);
    return () => clearInterval(t);
  }, [reload]);
  if (error) return <ErrorBox error={error} />;
  if (loading || !data) return <Loading />;
  const ready = data.nodes.filter((n) => n.ready);
  const memCap = ready.reduce((s, n) => s + n.memCap, 0);
  const cpuCap = ready.reduce((s, n) => s + n.cpuCap, 0);

  return (
    <div className="space-y-6">
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <Tile icon={<Server className="h-3.5 w-3.5" />} label={t('nav.servers')} value={data.servers}
          sub={<span className="flex flex-wrap gap-1.5">{Object.entries(data.statuses).map(([k, v]) => <span key={k}>{v} {statusLabel[k]?.toLowerCase()}</span>)}</span>} />
        <Tile icon={<Users className="h-3.5 w-3.5" />} label={t('nav.users')} value={data.users} />
        <Tile icon={<MemoryStick className="h-3.5 w-3.5" />} label={t('overview.ramAllocated')} value={mb(data.memoryAllocated)} sub={t('overview.ofCluster', { total: bytes(memCap, 0) })} />
        <Tile icon={<Network className="h-3.5 w-3.5" />} label={t('network.ports')} value={`${data.portsUsed} / ${data.portsTotal}`} sub={t('overview.portsUsed')} />
        <Tile icon={<Cpu className="h-3.5 w-3.5" />} label={t('overview.cpuAllocated')} value={cpuLabel(data.cpuAllocated)} sub={t('overview.ofVcpu', { count: (cpuCap / 1000).toFixed(0) })} />
        <Tile icon={<HardDrive className="h-3.5 w-3.5" />} label={t('overview.diskUsed')} value={bytes(data.diskUsed)} sub={t('overview.allocated', { size: mb(data.diskAllocated) })} />
        <Tile icon={<History className="h-3.5 w-3.5" />} label={t('tab.backups')} value={bytes(data.backupBytes)} />
        <Tile icon={<Boxes className="h-3.5 w-3.5" />} label={t('overview.namespace')} value={<code className="font-mono text-base">{data.namespace}</code>} sub={`KubePilot v${data.version}`} />
      </div>

      <Card padded={false} title={t('overview.nodes')}>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead><tr className="border-b border-ink-700">
              <th className="th">{t('overview.node')}</th><th className="th">{t('overview.state')}</th><th className="th">CPU</th><th className="th">{t('common.memory')}</th><th className="th">{t('nav.servers')}</th><th className="th">{t('overview.version')}</th>
            </tr></thead>
            <tbody>
              {data.nodes.map((n) => (
                <tr key={n.name} className="border-b border-ink-800">
                  <td className="px-4 py-3"><div className="font-mono">{n.name}</div><div className="text-xs text-ink-500">{n.address} · {n.os}</div></td>
                  <td className="px-4 py-3">{n.ready ? <Badge color="green">{t('overview.ready')}</Badge> : <Badge color="red">{t('overview.notReady')}</Badge>}</td>
                  <td className="w-44 px-4 py-3">{n.ready ? <><div className="text-xs">{cpuLabel(Math.round(n.cpu))} / {cpuLabel(n.cpuCap)}</div><Bar className="mt-1.5" value={n.cpu} max={n.cpuCap} /></> : '—'}</td>
                  <td className="w-44 px-4 py-3">{n.ready ? <><div className="text-xs">{bytes(n.mem)} / {bytes(n.memCap, 0)}</div><Bar className="mt-1.5" value={n.mem} max={n.memCap} /></> : '—'}</td>
                  <td className="px-4 py-3 text-ink-300">{n.pods}</td>
                  <td className="px-4 py-3 font-mono text-xs text-ink-400">{n.version}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>
    </div>
  );
}
