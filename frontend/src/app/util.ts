export function detectRootDomain(hostname: string): string {
  const m = hostname.match(/(^|\.)berjis\.(test|tech|com)$/i);
  if (m) return `berjis.${m[2].toLowerCase()}`;
  // fallback to dev
  return 'berjis.test';
}

export function urlFor(sub: 'api'|'schools-api'|'schools', proto = window.location.protocol): string {
  const root = detectRootDomain(window.location.hostname);
  const host = sub === 'schools' ? `schools.${root}` : `${sub}.${root}`;
  return `${proto}//${host}`;
}

export function loginUrl(returnTo?: string): string {
  const root = detectRootDomain(window.location.hostname);
  const base = `${window.location.protocol}//${root}`;
  const ret = encodeURIComponent(returnTo ?? window.location.href);
  return `${base}/auth/login?returnUrl=${ret}`;
}