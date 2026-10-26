const relativeFormatter = new Intl.RelativeTimeFormat('en', { numeric: 'auto' });
const absoluteFormatter = new Intl.DateTimeFormat('en', {
  dateStyle: 'medium',
  timeStyle: 'short',
});

const units: Array<[Intl.RelativeTimeFormatUnit, number]> = [
  ['year', 365 * 24 * 60 * 60],
  ['month', 30 * 24 * 60 * 60],
  ['week', 7 * 24 * 60 * 60],
  ['day', 24 * 60 * 60],
  ['hour', 60 * 60],
  ['minute', 60],
];

/** Formats an ISO timestamp as "3 hours ago". */
export function relativeTime(value?: string | null): string {
  if (!value) return 'never';
  const date = new Date(value);
  if (Number.isNaN(date.getTime()) || date.getFullYear() < 1970) return 'never';

  const seconds = (date.getTime() - Date.now()) / 1000;
  const absolute = Math.abs(seconds);
  if (absolute < 45) return 'just now';

  for (const [unit, size] of units) {
    if (absolute >= size) {
      return relativeFormatter.format(Math.round(seconds / size), unit);
    }
  }
  return 'just now';
}

/** Formats an ISO timestamp for a tooltip or detail row. */
export function absoluteTime(value?: string | null): string {
  if (!value) return 'Never';
  const date = new Date(value);
  if (Number.isNaN(date.getTime()) || date.getFullYear() < 1970) return 'Never';
  return absoluteFormatter.format(date);
}

export function shortSha(sha?: string): string {
  if (!sha) return '';
  return sha.slice(0, 7);
}

const numberFormatter = new Intl.NumberFormat('en', {
  notation: 'compact',
  maximumFractionDigits: 1,
});
const plainNumberFormatter = new Intl.NumberFormat('en');

export function compactNumber(value: number | undefined | null): string {
  if (value === undefined || value === null) return '0';
  return numberFormatter.format(value);
}

export function plainNumber(value: number | undefined | null): string {
  if (value === undefined || value === null) return '0';
  return plainNumberFormatter.format(value);
}

export function plural(count: number, singular: string, pluralForm?: string): string {
  return count === 1 ? singular : (pluralForm ?? `${singular}s`);
}

export function repositoryPath(owner: string, name: string): string {
  return `/repos/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`;
}

const languageColors: Record<string, string> = {
  Go: '#00add8',
  TypeScript: '#3178c6',
  JavaScript: '#f1e05a',
  Python: '#3572a5',
  Rust: '#dea584',
  Java: '#b07219',
  'C': '#555555',
  'C++': '#f34b7d',
  'C#': '#178600',
  Ruby: '#701516',
  PHP: '#4f5d95',
  Swift: '#f05138',
  Kotlin: '#a97bff',
  Dart: '#00b4ab',
  Shell: '#89e051',
  HTML: '#e34c26',
  CSS: '#563d7c',
  Elixir: '#6e4a7e',
  Haskell: '#5e5086',
  Lua: '#000080',
  Scala: '#c22d40',
  Zig: '#ec915c',
  Nix: '#7e7eff',
  Vue: '#41b883',
  Svelte: '#ff3e00',
  Dockerfile: '#384d54',
};

export function languageColor(language?: string): string {
  if (!language) return '#8fa1b3';
  return languageColors[language] ?? '#8fa1b3';
}

export function prStateLabel(state: string): string {
  switch (state) {
    case 'open':
      return 'Open';
    case 'merged':
      return 'Merged';
    case 'closed':
      return 'Closed';
    default:
      return state;
  }
}
