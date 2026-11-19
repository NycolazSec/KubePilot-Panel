import { Lock, Save } from 'lucide-react';
import { useEffect, useState } from 'react';
import { get, ImageOption, put, TemplateVar } from '../../api';
import { Button, Card, ErrorBox, Field, Loading, useLoad, useUI } from '../../components/ui';
import { useServer } from './ServerLayout';

type Var = TemplateVar & { value: string };
type Data = { startup: string; image: string; images: ImageOption[]; vars: Var[]; dataPath: string; canEditAll: boolean };

export default function Startup() {
  const { server } = useServer();
  const { toast } = useUI();
  const { data, error, loading, reload } = useLoad(() => get<Data>(`/servers/${server.id}/startup`), [server.id]);
  const [env, setEnv] = useState<Record<string, string>>({});
  const [image, setImage] = useState('');
  const [startup, setStartup] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!data) return;
    setEnv(Object.fromEntries(data.vars.map((v) => [v.key, v.value])));
    setImage(data.image);
    setStartup(data.startup);
  }, [data]);

  if (error) return <ErrorBox error={error} />;
  if (loading || !data) return <Loading />;

  const dirty = data.vars.some((v) => env[v.key] !== v.value) || image !== data.image || startup !== data.startup;
  const save = async () => {
    setBusy(true);
    try {
      const changed = Object.fromEntries(data.vars.filter((v) => env[v.key] !== v.value).map((v) => [v.key, env[v.key]]));
      const r = await put<{ restartRequired: boolean }>(`/servers/${server.id}/startup`, { env: changed, image, ...(data.canEditAll ? { startup } : {}) });
      toast(r.restartRequired ? 'Enregistré : redémarre le serveur pour appliquer' : 'Enregistré', r.restartRequired ? 'info' : 'ok');
      reload(true);
    } catch (e: any) { toast(e.message, 'err'); } finally { setBusy(false); }
  };

  const editable = (v: Var) => data.canEditAll || v.userEditable;

  return (
    <div className="space-y-5">
      <Card title="Configuration de démarrage" actions={<Button size="sm" variant="primary" icon={<Save className="h-3.5 w-3.5" />} loading={busy} disabled={!dirty} onClick={save}>Enregistrer</Button>}>
        <div className="grid gap-4 md:grid-cols-2">
          <Field label="Image Docker" hint={`Dossier des données dans le conteneur : ${data.dataPath}`}>
            {data.canEditAll ? (
              <input className="input font-mono" list="images" value={image} onChange={(e) => setImage(e.target.value)} />
            ) : (
              <select className="input" value={image} onChange={(e) => setImage(e.target.value)}>
                {data.images.map((i) => <option key={i.image} value={i.image}>{i.name} — {i.image}</option>)}
                {!data.images.some((i) => i.image === image) && <option value={image}>{image}</option>}
              </select>
            )}
            <datalist id="images">{data.images.map((i) => <option key={i.image} value={i.image}>{i.name}</option>)}</datalist>
          </Field>
        </div>
        {(data.startup || data.canEditAll) && (
          <Field label="Commande de démarrage" className="mt-4" hint={data.canEditAll ? 'Script shell (sh -c). Vide = commande par défaut de l\'image. Les variables sont accessibles avec $NOM.' : undefined}>
            <textarea className="input min-h-[110px] font-mono text-xs" value={startup} readOnly={!data.canEditAll} onChange={(e) => setStartup(e.target.value)} spellCheck={false} />
          </Field>
        )}
      </Card>

      {data.vars.length > 0 && (
        <div className="grid gap-4 md:grid-cols-2">
          {data.vars.map((v) => (
            <div key={v.key} className="card p-4">
              <div className="mb-2 flex items-center justify-between gap-2">
                <span className="text-sm font-medium">{v.name}</span>
                <code className="flex items-center gap-1 rounded bg-ink-800 px-1.5 py-0.5 font-mono text-[10px] text-ink-400">{!editable(v) && <Lock className="h-3 w-3" />}{v.key}</code>
              </div>
              {v.options?.length ? (
                <select className="input" value={env[v.key] ?? ''} disabled={!editable(v)} onChange={(e) => setEnv({ ...env, [v.key]: e.target.value })}>
                  {v.options.map((o) => <option key={o} value={o}>{o}</option>)}
                </select>
              ) : (
                <input className="input font-mono text-xs" value={env[v.key] ?? ''} disabled={!editable(v)} onChange={(e) => setEnv({ ...env, [v.key]: e.target.value })} />
              )}
              {v.description && <p className="mt-2 text-xs text-ink-400">{v.description}</p>}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
