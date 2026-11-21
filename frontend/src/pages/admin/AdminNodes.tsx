import { Check, Copy, Cpu, HardDrive, MemoryStick, Plus, Save, Server as ServerIcon, TriangleAlert } from 'lucide-react';
import { useEffect, useState } from 'react';
import { get, NodesInfo, NodeView, put } from '../../api';
import { Badge, Bar, Button, Card, ErrorBox, Field, Loading, Modal, Toggle, useLoad, useUI } from '../../components/ui';
import { bytes, cpuLabel, mb } from '../../lib/format';
import { t, tn, tx } from '../../i18n';

const installer = 'https://raw.githubusercontent.com/NycolazSec/KubePilot-Panel/main/install.sh';

/** Admin → Nodes: the machines of the cluster, what runs on each, and how to add one. */
export default function AdminNodes() {
  const { data, error, loading, reload } = useLoad(() => get<NodesInfo>('/admin/nodes'));
  const [adding, setAdding] = useState(false);
  useEffect(() => {
    const id = setInterval(() => reload(true), 10000);
    return () => clearInterval(id);
  }, [reload]);
  if (error) return <ErrorBox error={error} />;
  if (loading || !data) return <Loading />;
  const several = data.nodes.length > 1;
  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="max-w-3xl text-[13px] text-ink-400">{several ? t('nodes.intro') : t('nodes.introSingle')}</p>
        <Button variant="primary" size="sm" icon={<Plus className="h-3.5 w-3.5" />} onClick={() => setAdding(true)}>{t('nodes.add')}</Button>
      </div>
      {!data.agents && several && (
        <div className="flex items-start gap-2.5 rounded-md border border-amber-500/30 bg-amber-500/10 px-3.5 py-2.5 text-[13px] text-amber-200">
          <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0" />{t('nodes.agentsOff')}
        </div>
      )}
      <div className="grid gap-4 xl:grid-cols-2">
        {data.nodes.map((n) => <NodeCard key={n.name} node={n} several={several} onSaved={() => reload(true)} />)}
      </div>
      <AddNodeModal open={adding} onClose={() => setAdding(false)} />
    </div>
  );
}

function NodeCard({ node: n, several, onSaved }: { node: NodeView; several: boolean; onSaved: () => void }) {
  const { toast } = useUI();
  const [host, setHost] = useState(n.publicHost);
  const [accept, setAccept] = useState(n.acceptNew);
  const [busy, setBusy] = useState(false);
  const dirty = host !== n.publicHost || accept !== n.acceptNew;
  const save = async () => {
    setBusy(true);
    try { await put(`/admin/nodes/${encodeURIComponent(n.name)}`, { publicHost: host, acceptNew: accept }); toast(t('nodes.saved')); onSaved(); }
    catch (e: any) { toast(e.message, 'err'); } finally { setBusy(false); }
  };
  const memMB = n.memCap / 1048576;
  return (
    <Card title={
      <span className="flex flex-wrap items-center gap-2">
        <ServerIcon className="h-4 w-4 text-ink-400" /><span className="font-mono">{n.name}</span>
        {n.panel && <Badge color="violet">{t('nodes.main')}</Badge>}
        {!n.ready ? <Badge color="red">{t('overview.notReady')}</Badge> : n.cordoned ? <Badge color="yellow">{t('nodes.cordoned')}</Badge> : <Badge color="green">{t('overview.ready')}</Badge>}
        {several && !n.panel && (n.agent ? <Badge color="blue">{t('nodes.agentOk')}</Badge> : <Badge color="red">{t('nodes.agentMissing')}</Badge>)}
        {several && !n.acceptNew && <Badge>{t('nodes.closed')}</Badge>}
      </span>
    }>
      <div className="space-y-4">
        <div className="grid gap-x-6 gap-y-1 text-xs text-ink-400 sm:grid-cols-2">
          <div>{t('nodes.players')} <span className="font-mono text-ink-200">{n.address || '—'}</span></div>
          <div>IP <span className="font-mono text-ink-300">{[n.externalIP, n.internalIP].filter(Boolean).join(' · ') || '—'}</span></div>
          <div className="truncate">{n.os} · {n.arch}</div>
          <div className="font-mono">{n.version}{n.controlPlane ? ` · ${t('nodes.controlPlane')}` : ''}</div>
        </div>
        {n.ready && (
          <div className="grid gap-4 sm:grid-cols-2">
            <div>
              <div className="flex items-center gap-1.5 text-2xs font-medium uppercase tracking-wider text-ink-400"><Cpu className="h-3.5 w-3.5" />CPU</div>
              <div className="mt-1 text-xs tabular-nums">{cpuLabel(Math.round(n.cpu))} / {cpuLabel(n.cpuCap)}</div>
              <Bar className="mt-1.5" value={n.cpu} max={n.cpuCap} />
            </div>
            <div>
              <div className="flex items-center gap-1.5 text-2xs font-medium uppercase tracking-wider text-ink-400"><MemoryStick className="h-3.5 w-3.5" />{t('common.memory')}</div>
              <div className="mt-1 text-xs tabular-nums">{bytes(n.mem)} / {bytes(n.memCap, 0)}</div>
              <Bar className="mt-1.5" value={n.mem} max={n.memCap} />
            </div>
          </div>
        )}
        <div className="flex flex-wrap items-center gap-x-5 gap-y-1 rounded-md bg-ink-800/60 px-3 py-2 text-xs text-ink-300">
          <span className="flex items-center gap-1.5"><ServerIcon className="h-3.5 w-3.5 text-ink-400" />{tn('nodes.servers', n.servers)}</span>
          <span className="flex items-center gap-1.5"><MemoryStick className="h-3.5 w-3.5 text-ink-400" />{t('nodes.reserved', { used: mb(n.memoryAllocated), total: mb(Math.round(memMB)) })}</span>
          <span className="flex items-center gap-1.5"><HardDrive className="h-3.5 w-3.5 text-ink-400" />{mb(n.diskAllocated)}</span>
        </div>
        {several && n.problem && (
          <p className="flex items-start gap-2 text-xs text-amber-300"><TriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" />{n.problem}</p>
        )}
        <div className="border-t border-ink-700 pt-4">
          <Field label={t('nodes.publicHost')} hint={n.panel ? t('nodes.publicHostMainHint') : t('nodes.publicHostHint')}>
            <div className="flex gap-2">
              <input className="input min-w-0 flex-1 font-mono text-xs" value={host} placeholder={n.panel ? t('nodes.publicHostMainPlaceholder') : (n.externalIP || n.internalIP)} onChange={(e) => setHost(e.target.value)} />
              <Button variant={dirty ? 'primary' : 'secondary'} icon={<Save className="h-3.5 w-3.5" />} loading={busy} disabled={!dirty} onClick={save}>{t('common.save')}</Button>
            </div>
          </Field>
        </div>
        {several && <Toggle checked={accept} onChange={setAccept} label={t('nodes.acceptNew')} />}
      </div>
    </Card>
  );
}

function Command({ cmd }: { cmd: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try { await navigator.clipboard.writeText(cmd); setCopied(true); setTimeout(() => setCopied(false), 1500); } catch { /* the text stays selectable */ }
  };
  return (
    <div className="flex items-start gap-2 rounded-md border border-ink-700 bg-ink-950 p-2.5">
      <code className="min-w-0 flex-1 select-all break-all font-mono text-xs text-ink-100">{cmd}</code>
      <button type="button" onClick={copy} title={t('nodes.copy')} className="shrink-0 rounded p-1 text-ink-400 hover:bg-ink-800 hover:text-ink-100">
        {copied ? <Check className="h-3.5 w-3.5 text-green-400" /> : <Copy className="h-3.5 w-3.5" />}
      </button>
    </div>
  );
}

function AddNodeModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  return (
    <Modal open={open} onClose={onClose} wide title={t('nodes.addTitle')} footer={<Button onClick={onClose}>{t('common.close')}</Button>}>
      <ol className="space-y-5 text-sm">
        <li>
          <p className="mb-2 font-medium text-ink-100">1. {t('nodes.step1')}</p>
          <Command cmd={`curl -sfL ${installer} | sudo sh -s -- add-node`} />
          <p className="mt-1.5 text-xs text-ink-400">{t('nodes.step1Hint')}</p>
        </li>
        <li>
          <p className="mb-2 font-medium text-ink-100">2. {t('nodes.step2')}</p>
          <Command cmd={`curl -sfL ${installer} | sudo sh -s -- join --server https://… --token …`} />
          <p className="mt-1.5 text-xs text-ink-400">{t('nodes.step2Hint')}</p>
        </li>
        <li>
          <p className="font-medium text-ink-100">3. {t('nodes.step3')}</p>
          <p className="mt-1 text-xs text-ink-400">{t('nodes.step3Hint')}</p>
        </li>
      </ol>
      <div className="mt-5 rounded-md border border-ink-700 bg-ink-800/50 p-3 text-xs text-ink-300">
        <p className="mb-1 font-medium text-ink-200">{t('nodes.network')}</p>
        <p>{tx('nodes.networkHint', { ports: <code className="font-mono text-ink-100">6443/tcp · 10250/tcp · 51820/udp</code> })}</p>
      </div>
    </Modal>
  );
}
