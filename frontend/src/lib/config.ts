/**
 * Runtime branding and palette configuration. Everything is optional: empty or
 * missing environment variables fall back to the built-in gitsion identity.
 *
 * The palette is split into two sets of CSS variables so the theme toggle keeps
 * working with custom colors. `THEME_<TOKEN>` overrides both themes, while
 * `THEME_DARK_<TOKEN>` and `THEME_LIGHT_<TOKEN>` override one theme.
 */

export interface SiteConfig {
  name: string;
  title: string;
  description: string;
  tagline: string;
}

export interface HeroCopy {
  /** First line of the visible dashboard headline. */
  lead: string;
  /** Second line, highlighted in the accent color. */
  accent: string;
  /** Visible dashboard intro paragraph. */
  description: string;
}

export type ThemeMode = 'dark' | 'light';

/** [CSS custom-property suffix, environment-variable suffix] pairs. */
const PALETTE_TOKENS = [
  ['bg', 'BG'],
  ['panel', 'PANEL'],
  ['panel-2', 'PANEL_2'],
  ['panel-hover', 'PANEL_HOVER'],
  ['border', 'BORDER'],
  ['rule-soft', 'RULE_SOFT'],
  ['text', 'TEXT'],
  ['muted', 'MUTED'],
  ['faint', 'FAINT'],
  ['accent', 'ACCENT'],
  ['accent-soft', 'ACCENT_SOFT'],
  ['on-strong', 'ON_STRONG'],
  ['on-accent', 'ON_ACCENT'],
  ['placeholder', 'PLACEHOLDER'],
] as const;

const DEFAULT_BACKGROUND: Record<ThemeMode, string> = {
  dark: '#0b0b0b',
  light: '#f2f0ea',
};

const DEFAULT_DESCRIPTION =
  'A newest-first feed of pushes, pull requests and issues across the GitHub repositories you track.';

const DEFAULT_HERO_TITLE = 'Everything that moved,|newest first.';
const DEFAULT_HERO_DESCRIPTION =
  'Pushes, pull requests and issues from the repositories you track, gathered into one calm, readable feed.';

function env(key: string): string | undefined {
  const value = process.env[key];
  if (typeof value !== 'string') return undefined;
  const trimmed = value.trim();
  return trimmed === '' ? undefined : trimmed;
}

function paletteValue(mode: ThemeMode, token: string): string | undefined {
  return (
    env(`THEME_${mode.toUpperCase()}_${token}`) ??
    env(`THEME_${token}`)
  );
}

export function getSiteConfig(): SiteConfig {
  const name = env('SITE_NAME') ?? 'gitsion';
  const tagline = env('SITE_TAGLINE') ?? 'GitHub activity feed';

  return {
    name,
    tagline,
    title: env('SITE_TITLE') ?? `${name} · ${tagline}`,
    description: env('SITE_DESCRIPTION') ?? DEFAULT_DESCRIPTION,
  };
}

/**
 * Visible dashboard headline and intro. `HERO_TITLE` uses `|` to separate the
 * plain first line from the accent-colored second line; without a separator the
 * whole headline is plain.
 */
export function getHeroCopy(): HeroCopy {
  const [lead, ...rest] = (env('HERO_TITLE') ?? DEFAULT_HERO_TITLE).split('|');

  return {
    lead: lead.trim(),
    accent: rest.join('|').trim(),
    description: env('HERO_DESCRIPTION') ?? DEFAULT_HERO_DESCRIPTION,
  };
}

/** Inline style that overrides the built-in palette on the <html> element. */
export function getThemeStyle(): string {
  const declarations: string[] = [];

  for (const [cssToken, envToken] of PALETTE_TOKENS) {
    for (const mode of ['dark', 'light'] as const) {
      const value = paletteValue(mode, envToken);
      if (value) declarations.push(`--theme-${mode}-${cssToken}:${value}`);
    }
  }

  return declarations.join(';');
}

/** Effective background color, used for the browser's theme-color meta tag. */
export function getThemeColor(mode: ThemeMode): string {
  return paletteValue(mode, 'BG') ?? DEFAULT_BACKGROUND[mode];
}
