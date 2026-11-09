import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, existsSync, readdirSync, mkdtempSync, rmSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
const html = path => readFileSync(`dist/${path}`, 'utf8');
test('emptyCatalogAndRoutes', () => {
  assert.deepEqual(JSON.parse(readFileSync('src/data/products.json')), []);
  for (const lang of ['zh', 'en']) {
    for (const page of ['', 'tools/', 'about/', 'privacy/', 'feedback/']) assert.ok(existsSync(`dist/${lang}/${page}index.html`));
    assert.ok(html(`${lang}/tools/index.html`).includes(lang === 'zh' ? '暂无已上线工具' : 'No tools are available yet.'));
  }
  for (const path of ['admin', 'api', 'fr', 'zh/unknown', 'zh/tools/nonexistent']) assert.ok(!existsSync(`dist/${path}`));
  assert.deepEqual(readdirSync('dist/zh/tools'), ['index.html']);
});
test('emptyPagesAreNotIndexed', () => {
  for (const path of ['404.html', 'zh/tools/index.html', 'en/tools/index.html']) assert.match(html(path), /name="robots" content="noindex/);
  const sitemap = html('sitemap.xml');
  assert.doesNotMatch(sitemap, /\/(tools|admin|api|404)(\/|\.)/);
  for (const lang of ['zh', 'en']) for (const page of ['', 'about/', 'privacy/', 'feedback/']) {
    const content = html(`${lang}/${page}index.html`);
    assert.equal((content.match(/rel="canonical"/g) || []).length, 1);
    assert.ok(content.includes(`href="http://localhost:4321/${lang}/${page}"`));
    for (const alternate of ['zh', 'en', 'x-default']) assert.ok(content.includes(`hreflang="${alternate}"`));
    assert.doesNotMatch(content, /mailto:|href="[^"]*\/admin/);
  }
});
test('privateConfigDoesNotLeak', () => {
  function scan(path) { for (const entry of readdirSync(path, { withFileTypes: true })) {
    const file = `${path}/${entry.name}`;
    if (entry.isDirectory()) scan(file);
    else assert.doesNotMatch(readFileSync(file, 'utf8'), /creative-test-sentinel-only|creative-openai-sentinel-only|DEEPSEEK_API_KEY|OPENAI_API_KEY/);
  } }
  scan('dist');
});

// Node 24 directly loads this type-only TypeScript module.
const { readPublicConfig } = await import('../src/config/public.ts');
test('publicConfigurationRejectsUnsafeDestinations', () => {
 assert.deepEqual(readPublicConfig({}), {siteURL:'http://localhost:4321',adminURL:null,feedbackEmail:null});
 assert.equal(readPublicConfig({SITE_URL:'https://creative.example', ADMIN_URL:'https://admin.example/admin/login',FEEDBACK_EMAIL:'feedback@example.com'}).adminURL, 'https://admin.example/admin/login');
 for (const SITE_URL of ['http://example.com','https://user:private@example.com','https://example.com/path','javascript:alert(1)']) assert.throws(()=>readPublicConfig({SITE_URL}), {message:'SITE_URL 配置无效'});
 assert.throws(()=>readPublicConfig({ADMIN_URL:'https://user:private@example.com'}), {message:'ADMIN_URL 配置无效'});
 assert.throws(()=>readPublicConfig({FEEDBACK_EMAIL:'bad\r\n@example.com'}), {message:'FEEDBACK_EMAIL 配置无效'});
});

test('configuredEntrypointsPreserveEmailAddress', () => {
 const directory = mkdtempSync(join(tmpdir(), 'creative-public-'));
 const address = 'help#desk?reply@example.com';
 try {
   execFileSync('npm', ['run', 'build', '--', '--outDir', directory], {
     timeout: 570000, stdio: 'pipe',
     env: { ...process.env, ASTRO_TELEMETRY_DISABLED: '1', SITE_URL: 'https://creative.example', ADMIN_URL: 'https://admin.example/admin/login', FEEDBACK_EMAIL: address }
   });
   const feedback = readFileSync(join(directory, 'zh/feedback/index.html'), 'utf8');
   assert.ok(feedback.includes('href="https://admin.example/admin/login"'));
   const link = feedback.match(/href="(mailto:[^"]+)"/)[1];
   const parsed = new URL(link);
   assert.equal(decodeURIComponent(parsed.pathname), address);
   assert.equal(parsed.hash, ''); assert.equal(parsed.search, '');
   assert.ok(feedback.includes('href="https://creative.example/zh/feedback/"'));
 } finally { rmSync(directory, { recursive: true, force: true }); }
});
