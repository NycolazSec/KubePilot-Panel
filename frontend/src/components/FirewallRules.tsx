import { Plus, ShieldCheck, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { FirewallRule } from '../api';
import { t } from '../i18n';
import { dateTime } from '../lib/format';
import { Badge, Button, Empty, IconButton, useUI } from './ui';

/** A list of firewall rules (IPv4 address or range, allow or deny) with the form to add one. */
export default function FirewallRules({ rules, onAdd, onDelete, disabled, emptyHint }: {
  rules: FirewallRule[];
  onAdd: (r: { cidr: string; action: 'allow' | 'deny'; note: string }) => Promise<void>;
  onDelete: (r: FirewallRule) => Promise<void>;
  disabled?: boolean;
  emptyHint: string;
}) {
  const { confirm } = useUI();
  const [cidr, setCidr] = useState('');
  const [action, setAction] = useState<'allow' | 'deny'>('deny');
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);

  const add = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!cidr.trim()) return;
    setBusy(true);
    try {
      await onAdd({ cidr: cidr.trim(), action, note: note.trim() });
      setCidr('');
      setNote('');
    } finally { setBusy(false); }
  };
  const remove = async (r: FirewallRule) => {
    if (!(await confirm({ title: t('fw.removeTitle', { cidr: r.cidr }), confirm: t('fw.remove'), danger: true }))) return;
    await onDelete(r);
  };

  return (
    <div>
      {!disabled && (
        <form onSubmit={add} className="flex flex-wrap items-center gap-2 border-b border-ink-700 px-5 py-4">
          <input id="fw-cidr" className="input min-w-0 flex-[2_1_180px] font-mono" value={cidr} onChange={(e) => setCidr(e.target.value)}
            placeholder="203.0.113.7 / 198.51.100.0/24" aria-label={t('fw.cidr')} />
          <select id="fw-action" className="input !w-auto" value={action} onChange={(e) => setAction(e.target.value as 'allow' | 'deny')} aria-label={t('fw.action')}>
            <option value="deny">{t('fw.deny')}</option>
            <option value="allow">{t('fw.allow')}</option>
          </select>
          <input id="fw-note" className="input min-w-0 flex-[1_1_140px]" value={note} maxLength={100} onChange={(e) => setNote(e.target.value)} placeholder={t('fw.notePlaceholder')} aria-label={t('fw.note')} />
          <Button type="submit" variant="primary" icon={<Plus className="h-3.5 w-3.5" />} loading={busy} disabled={!cidr.trim()}>{t('fw.add')}</Button>
        </form>
      )}
      {!rules.length ? (
        <Empty icon={<ShieldCheck className="h-6 w-6" />} title={t('fw.noRules')}>{emptyHint}</Empty>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead><tr className="border-b border-ink-700">
              <th className="th">{t('fw.cidr')}</th><th className="th">{t('fw.action')}</th><th className="th">{t('fw.note')}</th><th className="th">{t('fw.added')}</th><th className="w-12" />
            </tr></thead>
            <tbody>
              {rules.map((r) => (
                <tr key={r.id} className="border-b border-ink-800 last:border-0">
                  <td className="px-4 py-2.5 font-mono text-ink-100">{r.cidr.endsWith('/32') ? r.cidr.slice(0, -3) : r.cidr}</td>
                  <td className="px-4 py-2.5">{r.action === 'allow' ? <Badge color="green">{t('fw.allow')}</Badge> : <Badge color="red">{t('fw.deny')}</Badge>}</td>
                  <td className="px-4 py-2.5 text-ink-300">{r.note || <span className="text-ink-500">—</span>}</td>
                  <td className="whitespace-nowrap px-4 py-2.5 text-xs text-ink-400">{dateTime(r.createdAt)}</td>
                  <td className="px-4 py-2 text-right">
                    {!disabled && <IconButton title={t('fw.remove')} className="hover:!text-red-300" onClick={() => remove(r)}><Trash2 className="h-4 w-4" /></IconButton>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
