export type PublicConfig = { siteURL: string; adminURL: string | null; feedbackEmail: string | null };
export function readPublicConfig(env: { SITE_URL?: string; ADMIN_URL?: string; FEEDBACK_EMAIL?: string }): PublicConfig {
  function url(value: string, field: string, rootOnly: boolean): string {
    try {
      const u = new URL(value);
      const local = ['localhost', '127.0.0.1', '[::1]'].includes(u.hostname);
      if (u.username || u.password || u.search || u.hash || (rootOnly && u.pathname !== '/') || !(u.protocol === 'https:' || (local && u.protocol === 'http:'))) throw new Error();
      return rootOnly ? u.origin : u.href;
    } catch { throw new Error(`${field} 配置无效`); }
  }
  const email = env.FEEDBACK_EMAIL?.trim() || null;
  if (email && !/^[A-Za-z0-9.!#$%&'*+/=?^_`{|}~-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+$/.test(email)) throw new Error('FEEDBACK_EMAIL 配置无效');
  return { siteURL: url(env.SITE_URL || 'http://localhost:4321', 'SITE_URL', true), adminURL: env.ADMIN_URL ? url(env.ADMIN_URL, 'ADMIN_URL', false) : null, feedbackEmail: email };
}
