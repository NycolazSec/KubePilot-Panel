// Renders Minecraft formatted text: legacy "§" codes and JSON chat components (MOTD, server messages).
// The text comes from the game server: it is only ever rendered as React text, and colors are whitelisted.

const LEGACY: Record<string, string> = {
  '0': '#000000', '1': '#0000AA', '2': '#00AA00', '3': '#00AAAA', '4': '#AA0000', '5': '#AA00AA', '6': '#FFAA00', '7': '#AAAAAA',
  '8': '#555555', '9': '#5555FF', a: '#55FF55', b: '#55FFFF', c: '#FF5555', d: '#FF55FF', e: '#FFFF55', f: '#FFFFFF',
};
const NAMED: Record<string, string> = {
  black: '0', dark_blue: '1', dark_green: '2', dark_aqua: '3', dark_red: '4', dark_purple: '5', gold: '6', gray: '7',
  dark_gray: '8', blue: '9', green: 'a', aqua: 'b', red: 'c', light_purple: 'd', yellow: 'e', white: 'f',
};

type Style = { color?: string; bold?: boolean; italic?: boolean; underlined?: boolean; strikethrough?: boolean };
type Seg = Style & { text: string };

function legacy(s: string, base: Style, out: Seg[]) {
  let style: Style = { ...base };
  let buf = '';
  const flush = () => { if (buf) out.push({ ...style, text: buf }); buf = ''; };
  for (let i = 0; i < s.length; i++) {
    if (s[i] === '§' && i + 1 < s.length) {
      const c = s[++i].toLowerCase();
      flush();
      if (LEGACY[c]) style = { color: LEGACY[c] };
      else if (c === 'l') style = { ...style, bold: true };
      else if (c === 'o') style = { ...style, italic: true };
      else if (c === 'n') style = { ...style, underlined: true };
      else if (c === 'm') style = { ...style, strikethrough: true };
      else if (c === 'r') style = { ...base };
      continue; // §k (obfuscated) and unknown codes are dropped
    }
    buf += s[i];
  }
  flush();
}

function color(c: unknown): string | undefined {
  if (typeof c !== 'string') return undefined;
  if (NAMED[c]) return LEGACY[NAMED[c]];
  return /^#[0-9a-fA-F]{6}$/.test(c) ? c : undefined;
}

function component(c: unknown, parent: Style, out: Seg[], depth = 0) {
  if (depth > 8 || out.length > 200) return;
  if (typeof c === 'string') return legacy(c, parent, out);
  if (Array.isArray(c)) return c.forEach((x) => component(x, parent, out, depth + 1));
  if (!c || typeof c !== 'object') return;
  const o = c as Record<string, unknown>;
  const style: Style = {
    color: color(o.color) ?? parent.color,
    bold: typeof o.bold === 'boolean' ? o.bold : parent.bold,
    italic: typeof o.italic === 'boolean' ? o.italic : parent.italic,
    underlined: typeof o.underlined === 'boolean' ? o.underlined : parent.underlined,
    strikethrough: typeof o.strikethrough === 'boolean' ? o.strikethrough : parent.strikethrough,
  };
  if (typeof o.text === 'string') legacy(o.text, style, out);
  if (Array.isArray(o.extra)) o.extra.forEach((x) => component(x, style, out, depth + 1));
}

export default function MinecraftText({ value, className }: { value: unknown; className?: string }) {
  const segs: Seg[] = [];
  component(value, {}, segs);
  return (
    <span className={className} style={{ whiteSpace: 'pre-wrap' }}>
      {segs.map((s, i) => (
        <span key={i} style={{
          color: s.color, fontWeight: s.bold ? 700 : undefined, fontStyle: s.italic ? 'italic' : undefined,
          textDecoration: [s.underlined && 'underline', s.strikethrough && 'line-through'].filter(Boolean).join(' ') || undefined,
        }}>{s.text}</span>
      ))}
    </span>
  );
}
