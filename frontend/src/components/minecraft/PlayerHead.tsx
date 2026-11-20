import clsx from 'clsx';

/** A player's face from their skin (served by the panel, never by a third party). */
export default function PlayerHead({ uuid, name, size = 32, className }: { uuid?: string; name: string; size?: number; className?: string }) {
  const id = uuid?.replace(/-/g, '').toLowerCase();
  if (!id || !/^[0-9a-f]{32}$/.test(id)) {
    return (
      <div style={{ width: size, height: size }} title={name}
        className={clsx('flex shrink-0 items-center justify-center rounded-[4px] bg-ink-700 font-semibold uppercase text-ink-200', size < 28 ? 'text-[9px]' : 'text-xs', className)}>
        {name.replace(/^[.*]/, '').slice(0, 2)}
      </div>
    );
  }
  return (
    <img src={`/api/minecraft/avatar/${id}`} width={size} height={size} alt="" title={name} loading="lazy" draggable={false}
      className={clsx('shrink-0 rounded-[4px] bg-ink-800', className)} style={{ imageRendering: 'pixelated' }} />
  );
}
