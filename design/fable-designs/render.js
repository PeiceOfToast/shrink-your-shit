// Renders every screen and variant in manifest.json to png/ with headless Chromium through Playwright.
// Usage: node render.js [screen-filter]   (needs `playwright` resolvable, e.g. NODE_PATH=<a node_modules with playwright>)
// If the browser's shared libraries are not installed system-wide, put them on LD_LIBRARY_PATH before running.
const { chromium } = require('playwright');
const path = require('path'), fs = require('fs');
const here = __dirname, manifest = JSON.parse(fs.readFileSync(path.join(here, 'manifest.json'), 'utf8'));
const only = process.argv[2];
const VIEWPORTS = { 1440: { width: 1440, height: 900 }, 390: { width: 390, height: 844 } };
(async () => {
  const browser = await chromium.launch();
  let n = 0;
  for (const s of manifest.screens) {
    if (only && !s.id.includes(only)) continue;
    for (const st of s.states) for (const w of Object.keys(VIEWPORTS)) for (const theme of ['dark', 'light']) {
      const ctx = await browser.newContext({ viewport: VIEWPORTS[w], colorScheme: theme, deviceScaleFactor: 1, reducedMotion: 'reduce' });
      const page = await ctx.newPage();
      const url = 'file://' + path.join(here, 'screens', s.file) + '?theme=' + theme + (st ? '&state=' + st : '');
      await page.goto(url, { waitUntil: 'load' });
      await page.waitForTimeout(60);
      const out = path.join(here, 'png', `${s.id}${st ? '-' + st : ''}-${w}-${theme}.png`);
      await page.screenshot({ path: out, fullPage: true });
      await ctx.close(); n++;
    }
  }
  await browser.close();
  console.log('rendered', n, 'png files into', path.join(here, 'png'));
})().catch((e) => { console.error(e); process.exit(1); });
