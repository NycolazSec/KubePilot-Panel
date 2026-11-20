import { Copy, Download, KeyRound, ShieldCheck, ShieldOff } from 'lucide-react';
import { useState } from 'react';
import { get, post } from '../api';
import { t, tn } from '../i18n';
import { copy } from '../lib/format';
import { useAuth } from '../main';
import { Badge, Button, Card, ErrorBox, Field, Loading, Modal, useLoad, useUI } from './ui';

type Status = { enabled: boolean; recoveryCodes: number };
type Setup = { secret: string; uri: string; qr: string };
// enable: password → scan + code → recovery codes. disable / recovery: password + code (→ recovery codes)
type Flow =
  | { kind: 'enable'; step: 'password' | 'scan' | 'codes'; setup?: Setup; codes?: string[] }
  | { kind: 'disable' }
  | { kind: 'recovery'; codes?: string[] };

function RecoveryCodes({ codes }: { codes: string[] }) {
  const { toast } = useUI();
  const text = codes.join('\n');
  const download = () => {
    const a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([`KubePilot — ${t('twofa.recoveryTitle')}\n\n${text}\n`], { type: 'text/plain' }));
    a.download = 'kubepilot-recovery-codes.txt';
    a.click();
    URL.revokeObjectURL(a.href);
  };
  return (
    <div className="space-y-3">
      <p className="text-[13px] text-ink-300">{t('twofa.recoveryExplain')}</p>
      <div className="grid grid-cols-2 gap-x-6 gap-y-1.5 rounded-md border border-ink-700 bg-ink-950 p-4 font-mono text-sm text-ink-100">
        {codes.map((c) => <span key={c}>{c}</span>)}
      </div>
      <div className="flex gap-2">
        <Button size="sm" icon={<Copy />} onClick={async () => { await copy(text); toast(t('twofa.copied')); }}>{t('common.copy')}</Button>
        <Button size="sm" icon={<Download />} onClick={download}>{t('common.download')}</Button>
      </div>
    </div>
  );
}

export default function TwoFactorCard() {
  const { refresh } = useAuth();
  const { toast } = useUI();
  const status = useLoad(() => get<Status>('/account/2fa'));
  const [flow, setFlow] = useState<Flow | null>(null);
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const open = (f: Flow) => { setFlow(f); setPassword(''); setCode(''); setError(''); };
  const close = () => {
    const changed = flow && ((flow.kind === 'enable' && flow.step === 'codes') || flow.kind === 'recovery');
    setFlow(null);
    if (changed) { status.reload(true); refresh(); }
  };
  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    setError('');
    try { await fn(); } catch (e: any) { setError(e.message); } finally { setBusy(false); }
  };

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!flow) return;
    run(async () => {
      if (flow.kind === 'enable' && flow.step === 'password') {
        const setup = await post<Setup>('/account/2fa/setup', { password });
        setFlow({ ...flow, step: 'scan', setup });
      } else if (flow.kind === 'enable' && flow.step === 'scan') {
        const r = await post<{ recoveryCodes: string[] }>('/account/2fa/enable', { code });
        setFlow({ ...flow, step: 'codes', codes: r.recoveryCodes });
        toast(t('twofa.enabledToast'));
      } else if (flow.kind === 'disable') {
        await post('/account/2fa/disable', { password, code });
        toast(t('twofa.disabledToast'));
        setFlow(null);
        status.reload(true);
        refresh();
      } else if (flow.kind === 'recovery') {
        const r = await post<{ recoveryCodes: string[] }>('/account/2fa/recovery', { password, code });
        setFlow({ ...flow, codes: r.recoveryCodes });
      }
    });
  };

  const showCodes = (flow?.kind === 'enable' && flow.step === 'codes') || (flow?.kind === 'recovery' && flow.codes);
  const codes = flow && 'codes' in flow ? flow.codes : undefined;
  const s = status.data;

  const passwordField = (
    <Field label={t('account.currentPassword')}>
      <input type="password" className="input" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} autoFocus required />
    </Field>
  );
  const codeField = (hint?: string) => (
    <Field label={flow?.kind === 'enable' ? t('login.code') : t('twofa.codeLabel')} hint={hint}>
      <input className="input font-mono" autoComplete="one-time-code" value={code} onChange={(e) => setCode(e.target.value)} placeholder="000000" required
        autoFocus={flow?.kind === 'enable'} />
    </Field>
  );

  const title = flow?.kind === 'disable' ? t('twofa.disableTitle') : flow?.kind === 'recovery' ? t('twofa.recoveryTitle') : t('twofa.enableTitle');

  return (
    <Card title={t('twofa.title')} description={t('twofa.description')}
      actions={s && (s.enabled ? <Badge color="green"><ShieldCheck /> {t('twofa.on')}</Badge> : <Badge>{t('twofa.off')}</Badge>)}>
      {status.error ? <ErrorBox error={status.error} /> : !s ? <Loading /> : s.enabled ? (
        <div className="space-y-4">
          <p className={s.recoveryCodes <= 3 ? 'text-[13px] text-amber-300' : 'text-[13px] text-ink-400'}>
            {tn('twofa.codesLeft', s.recoveryCodes)}{s.recoveryCodes <= 3 && ` — ${t('twofa.codesLow')}`}
          </p>
          <div className="flex flex-wrap gap-2">
            <Button icon={<KeyRound />} onClick={() => open({ kind: 'recovery' })}>{t('twofa.newCodes')}</Button>
            <Button variant="danger" icon={<ShieldOff />} onClick={() => open({ kind: 'disable' })}>{t('twofa.disable')}</Button>
          </div>
        </div>
      ) : (
        <div className="space-y-4">
          <p className="text-[13px] text-ink-400">{t('twofa.offHint')}</p>
          <Button variant="primary" icon={<ShieldCheck />} onClick={() => open({ kind: 'enable', step: 'password' })}>{t('twofa.enable')}</Button>
        </div>
      )}

      <Modal open={!!flow} onClose={close} title={title}
        footer={showCodes ? <Button variant="primary" onClick={close}>{t('twofa.saved')}</Button> : <>
          <Button variant="ghost" onClick={close}>{t('common.cancel')}</Button>
          <Button type="submit" form="twofa-form" variant={flow?.kind === 'disable' ? 'danger' : 'primary'} loading={busy}>
            {flow?.kind === 'disable' ? t('twofa.disable') : flow?.kind === 'enable' && flow.step === 'scan' ? t('twofa.activate') : t('common.confirm')}
          </Button>
        </>}>
        {showCodes && codes ? <RecoveryCodes codes={codes} /> : (
          <form id="twofa-form" onSubmit={submit} className="space-y-4">
            {error && <ErrorBox error={error} />}
            {flow?.kind === 'enable' && flow.step === 'password' && <>
              <p className="text-[13px] text-ink-300">{t('twofa.enableIntro')}</p>
              {passwordField}
            </>}
            {flow?.kind === 'enable' && flow.step === 'scan' && flow.setup && <>
              <p className="text-[13px] text-ink-300">{t('twofa.scan')}</p>
              <div className="flex flex-col items-center gap-3 sm:flex-row sm:items-start">
                <img src={flow.setup.qr} alt={t('twofa.qrAlt')} width={180} height={180} className="rounded-md bg-white p-2" />
                <div className="min-w-0 flex-1 space-y-2 text-xs text-ink-400">
                  <p>{t('twofa.manual')}</p>
                  <button type="button" onClick={async () => { await copy(flow.setup!.secret); toast(t('twofa.copied')); }}
                    className="flex w-full items-center gap-2 break-all rounded-md border border-ink-700 bg-ink-950 px-3 py-2 text-left font-mono text-ink-100 hover:border-ink-500">
                    <Copy className="h-3.5 w-3.5 shrink-0 text-ink-400" />{flow.setup.secret.replace(/(.{4})/g, '$1 ').trim()}
                  </button>
                </div>
              </div>
              {codeField()}
            </>}
            {(flow?.kind === 'disable' || flow?.kind === 'recovery') && <>
              <p className="text-[13px] text-ink-300">{flow.kind === 'disable' ? t('twofa.disableIntro') : t('twofa.recoveryIntro')}</p>
              {passwordField}
              {codeField(t('twofa.codeHint'))}
            </>}
          </form>
        )}
      </Modal>
    </Card>
  );
}
