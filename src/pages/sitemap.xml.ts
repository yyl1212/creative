import { readPublicConfig } from '../config/public';
export function GET() {
 const { siteURL } = readPublicConfig({ SITE_URL: import.meta.env.SITE_URL, ADMIN_URL: import.meta.env.ADMIN_URL, FEEDBACK_EMAIL: import.meta.env.FEEDBACK_EMAIL });
 const paths = ['/', ...['zh', 'en'].flatMap(lang => ['', 'about/', 'privacy/', 'feedback/'].map(page => `/${lang}/${page}`))];
 const body = `<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">${paths.map(path => `<url><loc>${siteURL}${path}</loc></url>`).join('')}</urlset>`;
 return new Response(body, { headers: { 'Content-Type': 'application/xml; charset=utf-8' } });
}
