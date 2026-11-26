/* FritzFlow UI – no dependencies. */
(() => {
'use strict';

const $ = (s) => document.querySelector(s);
const el = (tag, cls, html) => { const e = document.createElement(tag); if (cls) e.className = cls; if (html != null) e.innerHTML = html; return e; };
const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const clamp = (v, a, b) => Math.max(a, Math.min(b, v));
const store = {
  get(k, d) { try { const v = localStorage.getItem(k); return v == null ? d : v; } catch { return d; } },
  set(k, v) { try { localStorage.setItem(k, v); } catch { /* ignore */ } },
};

/* ---------- formatting ---------- */
const nf0 = new Intl.NumberFormat('de-DE', { maximumFractionDigits: 0 });
const nf1 = new Intl.NumberFormat('de-DE', { minimumFractionDigits: 1, maximumFractionDigits: 1 });
function rateParts(Bps) {
  let v = Math.max(0, Bps) * 8; const u = ['bit/s', 'kbit/s', 'Mbit/s', 'Gbit/s']; let i = 0;
  while (v >= 1000 && i < u.length - 1) { v /= 1000; i++; }
  return [v < 10 && i > 0 ? nf1.format(v) : nf0.format(v), u[i]];
}
const fmtRate = (Bps) => rateParts(Bps).join(' ');
function fmtBytes(B) {
  let v = Math.max(0, B); const u = ['B', 'KB', 'MB', 'GB', 'TB']; let i = 0;
  while (v >= 1000 && i < u.length - 1) { v /= 1000; i++; }
  return (v < 10 && i > 0 ? nf1.format(v) : nf0.format(v)) + ' ' + u[i];
}
function fmtDur(s) {
  s = Math.max(0, s | 0);
  if (s < 60) return s + ' s';
  if (s < 3600) return Math.floor(s / 60) + ' min';
  return Math.floor(s / 3600) + ' h ' + Math.floor((s % 3600) / 60) + ' min';
}
const hostPort = (ip, port) => (ip.includes(':') ? '[' + ip + ']' : ip) + (port ? ':' + port : '');

/* ---------- icons ---------- */
const ICONS = {
  phone: 'M8 2h8a2 2 0 0 1 2 2v16a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2zM11 18h2',
  tablet: 'M6 2h12a2 2 0 0 1 2 2v16a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2zM11 18h2',
  laptop: 'M5 5h14a1 1 0 0 1 1 1v9H4V6a1 1 0 0 1 1-1zM2 18h20M4 15l-2 3',
  desktop: 'M3 4h18a1 1 0 0 1 1 1v11a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1zM8 21h8M12 17v4',
  tv: 'M3 6h18a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1zM8 21h8M9 2l3 3 3-3',
  console: 'M6 8h12a4 4 0 0 1 4 4v1a4 4 0 0 1-7 2.6L14 14h-4l-1 1.6A4 4 0 0 1 2 13v-1a4 4 0 0 1 4-4zM7 10.5v3M5.5 12h3M16 11h.01M18 13h.01',
  speaker: 'M7 2h10a2 2 0 0 1 2 2v16a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2zM12 11a3 3 0 1 0 0 6a3 3 0 1 0 0-6zM12 6h.01',
  printer: 'M6 9V3h12v6M6 18H4a2 2 0 0 1-2-2v-5a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2v5a2 2 0 0 1-2 2h-2M6 14h12v7H6z',
  server: 'M4 3h16a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1zM4 13h16a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1v-6a1 1 0 0 1 1-1zM7 7h.01M7 17h.01',
  camera: 'M23 7l-7 5 7 5V7zM3 5h11a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2z',
  iot: 'M9 2v6M15 2v6M6 8h12v4a6 6 0 0 1-12 0V8zM12 18v4',
  router: 'M3 14h18a1 1 0 0 1 1 1v4a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1v-4a1 1 0 0 1 1-1zM6 17h.01M10 17h.01M8 10a6 6 0 0 1 8 0M5 7a10 10 0 0 1 14 0',
  generic: 'M12 2a10 10 0 1 0 0 20a10 10 0 1 0 0-20zM2 12h20M12 2a15 15 0 0 1 0 20M12 2a15 15 0 0 0 0 20',
};
const ICON_RULES = [
  [/iphone|android|galaxy-?s|pixel|phone|handy|smartphone|xiaomi|redmi|oneplus|huawei|motorola|nokia/, 'phone'],
  [/ipad|tablet|\btab\b|kindle|galaxy-?tab|fire-?hd/, 'tablet'],
  [/macbook|laptop|notebook|thinkpad|elitebook|surface|chromebook|zenbook|yoga/, 'laptop'],
  [/\btv\b|-tv|tv-|fire-?tv|firetv|chromecast|apple-?tv|roku|shield|bravia|webos|television|fernseher|beamer/, 'tv'],
  [/playstation|\bps[345]\b|ps5|ps4|xbox|nintendo|switch-?oled|steam-?deck/, 'console'],
  [/echo|alexa|sonos|homepod|speaker|google-?home|nest-?(mini|audio|hub)|lautsprecher/, 'speaker'],
  [/drucker|printer|laserjet|officejet|deskjet|epson|brother|canon|^hp[-_]/, 'printer'],
  [/\bnas\b|synology|qnap|diskstation|server|raspberry|raspi|\bpi\b|unraid|proxmox|truenas|homeassistant/, 'server'],
  [/cam|kamera|\bring\b|arlo|eufy|blink/, 'camera'],
  [/fritz|repeater|router|access-?point|mesh/, 'router'],
  [/\bpc\b|-pc|pc-|desktop|imac|mac-?mini|mac-?studio|workstation|win-|desktop-/, 'desktop'],
  [/shelly|tasmota|esp|hue|tado|plug|sensor|homematic|smart|wled|tuya|sonoff|thermostat|bulb|lamp/, 'iot'],
];
function iconFor(name) {
  const n = String(name || '').toLowerCase();
  for (const [re, ic] of ICON_RULES) if (re.test(n)) return ic;
  return 'generic';
}
const svgIcon = (k) => `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="${ICONS[k] || ICONS.generic}"/></svg>`;
const PATHS = {};
const path2d = (k) => PATHS[k] || (PATHS[k] = new Path2D(ICONS[k] || ICONS.generic));

/* ---------- destinations ---------- */
const BRANDS = [
  [/googlevideo|youtube|ytimg|ggpht|youtu\.be/, 'YouTube', '#ff3d3d'],
  [/nflx|netflix/, 'Netflix', '#e50914'],
  [/spotify|scdn\.co/, 'Spotify', '#1ed760'],
  [/whatsapp/, 'WhatsApp', '#25d366'],
  [/facebook|fbcdn|instagram|cdninstagram|fbsbx|\bmeta\b|messenger/, 'Meta', '#1877f2'],
  [/tiktok|bytedance|ibyteimg|tiktokcdn|byteoversea/, 'TikTok', '#ff0050'],
  [/disney|bamgrid|dssott|dssedge/, 'Disney+', '#2f6bff'],
  [/twitch|ttvnw|jtvnw/, 'Twitch', '#9146ff'],
  [/steam|valve/, 'Steam', '#66c0f4'],
  [/playstation|sonyentertainment|\bpsn\b/, 'PlayStation', '#2e6cf6'],
  [/nintendo/, 'Nintendo', '#e60012'],
  [/xboxlive|xbox/, 'Xbox', '#107c10'],
  [/discord/, 'Discord', '#5865f2'],
  [/zoom\.us|zoom\.com/, 'Zoom', '#2d8cff'],
  [/telegram/, 'Telegram', '#2aabee'],
  [/signal\.org/, 'Signal', '#3a76f0'],
  [/github|githubusercontent/, 'GitHub', '#8b949e'],
  [/anthropic|claude\.ai/, 'Anthropic', '#d97757'],
  [/openai|chatgpt/, 'OpenAI', '#10a37f'],
  [/icloud|apple|aaplimg|mzstatic|cdn-apple|itunes/, 'Apple', '#a3aab8'],
  [/microsoft|windows|msft|live\.com|office|outlook|azure|bing|skype|sharepoint|msedge|onedrive|teams/, 'Microsoft', '#00a4ef'],
  [/amazon|aws|a2z|cloudfront|amazonvideo|primevideo|media-amazon/, 'Amazon', '#ff9900'],
  [/google|gstatic|gvt[12]|1e100|googleapis|doubleclick|googleusercontent|android/, 'Google', '#4285f4'],
  [/akamai/, 'Akamai', '#0099cc'],
  [/cloudflare/, 'Cloudflare', '#f38020'],
  [/fastly/, 'Fastly', '#ff282d'],
  [/dropbox/, 'Dropbox', '#0061ff'],
  [/shelly/, 'Shelly', '#4aa3df'],
];
const SLD2 = new Set(['co.uk', 'org.uk', 'gov.uk', 'ac.uk', 'com.au', 'net.au', 'co.jp', 'com.br', 'co.nz', 'com.tr', 'co.at', 'or.at', 'gv.at', 'com.cn', 'co.in', 'co.za']);
function hashColor(s) {
  let h = 0; for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) | 0;
  return `hsl(${Math.abs(h) % 360} 70% 60%)`;
}
const destCache = new Map();
function destInfo(c) {
  if (c.kind === 'router') { const n = boxLabel(); return { key: 'router', label: n, color: '#e2001a' }; }
  if (c.kind === 'lan') { const n = c.host || c.rip; return { key: 'lan:' + n, label: n, color: '#64748b' }; }
  const host = c.host || '';
  const ck = host || c.rip;
  let d = destCache.get(ck);
  if (d) return d;
  if (host) {
    for (const [re, label, color] of BRANDS) if (re.test(host)) { d = { key: label, label, color }; break; }
    if (!d) {
      const parts = host.split('.');
      let dom = parts.slice(-2).join('.');
      if (parts.length > 2 && SLD2.has(dom)) dom = parts.slice(-3).join('.');
      d = { key: dom, label: dom, color: hashColor(dom) };
    }
  } else {
    d = { key: c.rip, label: c.rip, color: '#64748b' };
  }
  if (destCache.size > 5000) destCache.clear();
  destCache.set(ck, d);
  return d;
}
const SVC_COLORS = { HTTPS: '#3b82f6', QUIC: '#22d3ee', HTTP: '#f59e0b', DNS: '#a78bfa', NTP: '#94a3b8', UDP: '#34d399', TCP: '#64748b', 'Apple Push': '#cbd5e1', SSH: '#f472b6', STUN: '#fb7185', ICMP: '#facc15' };
const svcColor = (s) => SVC_COLORS[s] || hashColor(s);

/* ---------- box identity ---------- */
const boxLabel = () => S.snap?.status?.boxName || 'FRITZ!Box';
const boxModel = () => [S.snap?.status?.boxModel, S.snap?.status?.boxOS ? 'FRITZ!OS ' + S.snap.status.boxOS : ''].filter(Boolean).join(' · ');

/* ---------- theme ---------- */
let css = {};
function readTheme() {
  const cs = getComputedStyle(document.documentElement);
  for (const k of ['down', 'down2', 'up', 'up2', 'text', 'text2', 'muted', 'grid', 'border', 'border-strong', 'card-solid', 'pill', 'ok', 'bg'])
    css[k] = cs.getPropertyValue('--' + k).trim();
}
function applyTheme(t) {
  document.documentElement.dataset.theme = t;
  readTheme();
  drawAllStatic();
}
$('#themeBtn').addEventListener('click', () => {
  const t = document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark';
  store.set('ff-theme', t); applyTheme(t);
});
document.documentElement.dataset.theme = store.get('ff-theme', 'dark'); // dark by default
readTheme();

/* ---------- state ---------- */
const S = {
  snap: null,
  devs: new Map(),
  hist: [], // {t, rx, tx}
  filter: {
    q: '',
    dev: store.get('ff-dev', '') || '',
    kind: store.get('ff-kind', store.get('ff-wan', '1') === '0' ? 'all' : 'wan'), // wan | int | all
    net: store.get('ff-net', ''), // '' = all networks, else e.g. 'LAN', 'WLAN 5 GHz', 'Gast'
  },
  sort: { k: 'rate', asc: false },
  setupShown: false,
  // per-view ordering: 'live' = by current activity, 'static' = calm/fixed order
  order: { flow: store.get('ff-sort-flow', 'static'), dest: store.get('ff-sort-dest', 'live'), dev: store.get('ff-sort-dev', 'live') },
  cfg: null,
};

/* ---------- tiles: hide / show ---------- */
const hiddenTiles = new Set((store.get('ff-hidden', '') || '').split(',').filter(Boolean));
const tileShown = (id) => !hiddenTiles.has(id);
function applyTiles() {
  document.querySelectorAll('[data-tile]').forEach((t) => t.classList.toggle('tile-hidden', hiddenTiles.has(t.dataset.tile)));
  // containers: collapse empty columns, let the remaining tile use the space
  const side = document.querySelector('.side');
  const sideVisible = [...side.querySelectorAll('[data-tile]')].some((t) => tileShown(t.dataset.tile));
  side.classList.toggle('tile-hidden', !sideVisible);
  for (const sec of document.querySelectorAll('.grid-top, .grid-main, .grid-2')) {
    const kids = [...sec.children].filter((k) => !k.classList.contains('tile-hidden'));
    sec.classList.toggle('single', kids.length < 2);
    sec.classList.toggle('tile-hidden', kids.length === 0);
  }
  const kpis = document.querySelector('.kpis');
  kpis.classList.toggle('tile-hidden', ![...kpis.children].some((k) => !k.classList.contains('tile-hidden')));
  store.set('ff-hidden', [...hiddenTiles].join(','));
  $('#viewList').innerHTML = [...document.querySelectorAll('[data-tile]')].map((t) =>
    `<label><input type="checkbox" data-t="${t.dataset.tile}" ${tileShown(t.dataset.tile) ? 'checked' : ''}> ${esc(t.dataset.title)}</label>`).join('');
  $('#viewBtn').classList.toggle('has-hidden', hiddenTiles.size > 0);
  window.dispatchEvent(new Event('resize')); // canvases re-measure
}
function setTile(id, show) {
  if (show) hiddenTiles.delete(id); else hiddenTiles.add(id);
  applyTiles();
}
document.addEventListener('click', (e) => {
  const x = e.target.closest('.tile-x');
  if (x) { setTile(x.closest('[data-tile]').dataset.tile, false); return; }
  const pop = $('#viewPop');
  if (e.target.closest('#viewBtn')) { pop.hidden = !pop.hidden; return; }
  if (!pop.hidden && !e.target.closest('#viewPop')) pop.hidden = true;
});
$('#viewList').addEventListener('change', (e) => { const c = e.target.closest('input[data-t]'); if (c) setTile(c.dataset.t, c.checked); });
$('#viewAll').addEventListener('click', () => { hiddenTiles.clear(); applyTiles(); });
document.addEventListener('keydown', (e) => { if (e.key === 'Escape') $('#viewPop').hidden = true; });
applyTiles();

/* ---------- per-view ordering ---------- */
function syncSegs() {
  document.querySelectorAll('.mini-seg').forEach((g) => g.querySelectorAll('button').forEach((b) =>
    b.classList.toggle('on', b.dataset.v === S.order[g.dataset.view])));
}
document.querySelectorAll('.mini-seg').forEach((g) => g.addEventListener('click', (e) => {
  const b = e.target.closest('button'); if (!b) return;
  S.order[g.dataset.view] = b.dataset.v; store.set('ff-sort-' + g.dataset.view, b.dataset.v);
  F.sortedL = F.sortedR = 0; syncSegs(); render();
}));
syncSegs();

/* ---------- canvas helper ---------- */
function fitCanvas(cv) {
  const dpr = Math.min(window.devicePixelRatio || 1, 2);
  const r = cv.getBoundingClientRect();
  const w = Math.max(1, Math.round(r.width)), h = Math.max(1, Math.round(r.height));
  if (cv.width !== Math.round(w * dpr) || cv.height !== Math.round(h * dpr)) {
    cv.width = Math.round(w * dpr); cv.height = Math.round(h * dpr);
  }
  const ctx = cv.getContext('2d');
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  return { ctx, w, h };
}
function roundRect(ctx, x, y, w, h, r) {
  ctx.beginPath();
  ctx.moveTo(x + r, y); ctx.arcTo(x + w, y, x + w, y + h, r); ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r); ctx.arcTo(x, y, x + w, y, r); ctx.closePath();
}
function withAlpha(color, a) {
  if (color.startsWith('#') && color.length === 7) {
    const n = parseInt(color.slice(1), 16);
    return `rgba(${n >> 16},${(n >> 8) & 255},${n & 255},${a})`;
  }
  if (color.startsWith('hsl(')) return color.replace(')', ` / ${a})`);
  return color;
}

/* ---------- KPI number tweening ---------- */
const tweens = [];
function tweenNum(elm, target, fmt) {
  let t = tweens.find((x) => x.el === elm);
  if (!t) { t = { el: elm, cur: target, target, fmt }; tweens.push(t); elm.textContent = fmt(target); }
  t.target = target; t.fmt = fmt;
}
function stepTweens(dt) {
  for (const t of tweens) {
    if (Math.abs(t.cur - t.target) < 1e-6) continue;
    t.cur += (t.target - t.cur) * Math.min(1, dt * 7);
    if (Math.abs(t.cur - t.target) < Math.abs(t.target) * 0.002 + 0.01) t.cur = t.target;
    t.el.textContent = t.fmt(t.cur);
  }
}

/* ---------- sparklines & history ---------- */
// real internet throughput (online monitor) when available, else capture
const hasWan = () => S.hist.length > 0 && S.hist[S.hist.length - 1].wrx != null;
const val = (p, k) => (hasWan() && p['w' + k] != null ? p['w' + k] : p[k]);
function drawSpark(cv, key, color) {
  const { ctx, w, h } = fitCanvas(cv);
  ctx.clearRect(0, 0, w, h);
  const pts = S.hist.slice(-60);
  if (pts.length < 2) return;
  const max = Math.max(1, ...pts.map((p) => val(p, key)));
  const step = w / 59;
  const x0 = w - (pts.length - 1) * step;
  ctx.beginPath();
  pts.forEach((p, i) => { const x = x0 + i * step, y = h - 2 - (val(p, key) / max) * (h - 6); i ? ctx.lineTo(x, y) : ctx.moveTo(x, y); });
  ctx.strokeStyle = color; ctx.lineWidth = 2; ctx.lineJoin = 'round'; ctx.stroke();
  ctx.lineTo(w, h); ctx.lineTo(x0, h); ctx.closePath();
  const g = ctx.createLinearGradient(0, 0, 0, h);
  g.addColorStop(0, withAlpha(color, 0.35)); g.addColorStop(1, withAlpha(color, 0));
  ctx.fillStyle = g; ctx.fill();
}

let histHover = -1;
function drawHistory() {
  const cv = $('#history');
  const { ctx, w, h } = fitCanvas(cv);
  ctx.clearRect(0, 0, w, h);
  const N = 300, padL = 78, padR = 8, padT = 10, padB = 20;
  const cw = w - padL - padR, ch = h - padT - padB;
  const pts = S.hist.slice(-N);
  const maxRx = Math.max(1000, ...pts.map((p) => Math.max(val(p, 'rx'), p.rx)));
  const maxTx = Math.max(1000, ...pts.map((p) => Math.max(val(p, 'tx'), p.tx)));
  const topH = ch * 0.66, botH = ch - topH;
  const axisY = padT + topH;
  ctx.font = '11px system-ui, sans-serif'; ctx.textBaseline = 'middle'; ctx.textAlign = 'right';
  ctx.strokeStyle = css.grid; ctx.lineWidth = 1; ctx.fillStyle = css.muted;
  for (const f of [1, 0.5]) {
    const y = axisY - topH * f + 0.5;
    ctx.beginPath(); ctx.moveTo(padL, y); ctx.lineTo(w - padR, y); ctx.stroke();
    ctx.fillStyle = css.down; ctx.fillText('↓ ' + fmtRate(maxRx * f), padL - 8, y);
  }
  const yb = axisY + botH + 0.5;
  ctx.beginPath(); ctx.moveTo(padL, yb); ctx.lineTo(w - padR, yb); ctx.stroke();
  ctx.fillStyle = css.up; ctx.fillText('↑ ' + fmtRate(maxTx), padL - 8, yb - 6);
  ctx.fillStyle = css.muted; ctx.fillText('0', padL - 8, axisY);
  ctx.strokeStyle = css['border-strong'];
  ctx.beginPath(); ctx.moveTo(padL, axisY + 0.5); ctx.lineTo(w - padR, axisY + 0.5); ctx.stroke();
  ctx.textAlign = 'center'; ctx.textBaseline = 'alphabetic';
  for (let m = 1; m <= 4; m++) {
    const x = padL + cw - (m * 60 / (N - 1)) * cw;
    ctx.fillText('−' + m + ' min', x, h - 4);
  }
  if (pts.length < 2) return;
  const step = cw / (N - 1);
  const x0 = padL + cw - (pts.length - 1) * step;
  const area = (key, max, sign, c1, c2) => {
    ctx.beginPath();
    pts.forEach((p, i) => {
      const x = x0 + i * step;
      const y = axisY - sign * (val(p, key) / max) * (sign > 0 ? topH - 4 : botH - 4);
      i ? ctx.lineTo(x, y) : ctx.moveTo(x, y);
    });
    const line = new Path2D(); // stroke separately
    ctx.save();
    ctx.strokeStyle = c1; ctx.lineWidth = 1.8; ctx.lineJoin = 'round'; ctx.stroke();
    ctx.restore();
    ctx.lineTo(x0 + (pts.length - 1) * step, axisY); ctx.lineTo(x0, axisY); ctx.closePath();
    const g = ctx.createLinearGradient(0, sign > 0 ? padT : axisY + botH, 0, axisY);
    g.addColorStop(0, withAlpha(c2, 0.45)); g.addColorStop(1, withAlpha(c2, 0.02));
    ctx.fillStyle = g; ctx.fill();
    void line;
  };
  area('rx', maxRx, 1, css.down2, css.down);
  area('tx', maxTx, -1, css.up2, css.up);
  if (hasWan()) { // dashed: what the packet capture sees
    ctx.save(); ctx.setLineDash([4, 3]); ctx.lineWidth = 1.2;
    for (const [key, max, sign, col] of [['rx', maxRx, 1, css.text2], ['tx', maxTx, -1, css.text2]]) {
      ctx.beginPath();
      pts.forEach((p, i) => { const x = x0 + i * step, y = axisY - sign * (p[key] / max) * (sign > 0 ? topH - 4 : botH - 4); i ? ctx.lineTo(x, y) : ctx.moveTo(x, y); });
      ctx.strokeStyle = withAlpha(col.startsWith('#') ? col : '#a3afc2', 0.7); ctx.stroke();
    }
    ctx.restore();
  }
  $('#histCapLg').hidden = $('#histCapTxt').hidden = !hasWan();
  $('#histLegend').title = hasWan()
    ? 'Flächen: echter Durchsatz am Internet-Anschluss (FRITZ!Box-Online-Monitor). Gestrichelt: was der Paketmitschnitt davon sieht. Oben Download, unten Upload, die Mittellinie ist 0.'
    : 'Oben Download, unten Upload (gespiegelt), die Mittellinie ist 0.';
  if (histHover >= 0 && histHover < pts.length) {
    const p = pts[histHover], x = x0 + histHover * step;
    ctx.strokeStyle = css.text2; ctx.setLineDash([3, 3]);
    ctx.beginPath(); ctx.moveTo(x + 0.5, padT); ctx.lineTo(x + 0.5, axisY + botH); ctx.stroke(); ctx.setLineDash([]);
    const tip = $('#histTip');
    tip.hidden = false;
    tip.innerHTML = `<b>${new Date(p.t).toLocaleTimeString('de-DE')}</b><div class="tr"><span>Download</span><span class="d">${fmtRate(val(p, 'rx'))}</span></div><div class="tr"><span>Upload</span><span class="u">${fmtRate(val(p, 'tx'))}</span></div>` +
      (p.wrx != null ? `<div class="tr"><span>im Mitschnitt</span><span>↓ ${fmtRate(p.rx)} · ↑ ${fmtRate(p.tx)}</span></div>` : '');
    const tw = tip.offsetWidth;
    tip.style.left = clamp(x + 12, 0, w - tw) + 'px'; tip.style.top = '8px';
  }
}
$('#history').addEventListener('mousemove', (ev) => {
  const r = ev.currentTarget.getBoundingClientRect();
  const N = 300, padL = 78, padR = 8, cw = r.width - padL - padR;
  const pts = Math.min(S.hist.length, N);
  const step = cw / (N - 1), x0 = padL + cw - (pts - 1) * step;
  const i = Math.round((ev.clientX - r.left - x0) / step);
  histHover = i >= 0 && i < pts ? i : -1;
  if (histHover < 0) $('#histTip').hidden = true;
  drawHistory();
});
$('#history').addEventListener('mouseleave', () => { histHover = -1; $('#histTip').hidden = true; drawHistory(); });

/* ---------- donut ---------- */
function drawDonut(conns) {
  const cv = $('#donut');
  const { ctx, w, h } = fitCanvas(cv);
  ctx.clearRect(0, 0, w, h);
  const agg = new Map();
  let total = 0;
  const useRate = conns.some((c) => c.rxRate + c.txRate > 0);
  for (const c of conns) {
    const v = useRate ? c.rxRate + c.txRate : c.rx + c.tx;
    if (v <= 0) continue;
    agg.set(c.svc, (agg.get(c.svc) || 0) + v); total += v;
  }
  let items = [...agg.entries()].sort((a, b) => b[1] - a[1]);
  if (items.length > 6) {
    const rest = items.slice(5).reduce((s, x) => s + x[1], 0);
    items = items.slice(0, 5).concat([['Andere', rest]]);
  }
  const cx = w / 2, cy = h / 2, R = Math.min(w, h) / 2 - 4, r = R * 0.68;
  ctx.lineWidth = R - r;
  if (!total) {
    ctx.strokeStyle = css.grid; ctx.beginPath(); ctx.arc(cx, cy, (R + r) / 2, 0, Math.PI * 2); ctx.stroke();
  }
  let a = -Math.PI / 2;
  for (const [name, v] of items) {
    const da = (v / total) * Math.PI * 2;
    ctx.strokeStyle = name === 'Andere' ? css.muted : svcColor(name);
    ctx.beginPath(); ctx.arc(cx, cy, (R + r) / 2, a + 0.02, a + Math.max(0.03, da - 0.02)); ctx.stroke();
    a += da;
  }
  ctx.fillStyle = css.text; ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
  if (items.length) {
    ctx.font = '700 20px system-ui, sans-serif';
    ctx.fillText(Math.round((items[0][1] / total) * 100) + '%', cx, cy - 6);
    ctx.font = '11px system-ui, sans-serif'; ctx.fillStyle = css.muted;
    ctx.fillText(items[0][0], cx, cy + 12);
  } else {
    ctx.font = '12px system-ui, sans-serif'; ctx.fillStyle = css.muted; ctx.fillText('–', cx, cy);
  }
  $('#svcLegend').innerHTML = items.map(([n, v]) =>
    `<li><i style="background:${n === 'Andere' ? css.muted : svcColor(n)}"></i><span>${esc(n)}</span><span>${Math.round((v / total) * 100)}%</span></li>`).join('')
    || '<li class="muted">Noch keine Daten</li>';
}

/* ---------- flow map ---------- */
const F = {
  nodes: new Map(), // id -> node
  links: new Map(), // id -> link
  parts: [],
  pairs: new Map(), // devId -> Set(destKey)
  hover: null,
  box: { rate: 0, pulse: 0 },
  last: performance.now(),
  w: 0, h: 0,
};
function flowModel(snap) {
  const conns = visibleConns(snap.conns, true);
  const devRate = new Map(), devRx = new Map(), devTx = new Map();
  const dst = new Map();
  F.pairs = new Map();
  for (const c of conns) {
    const r = c.rxRate + c.txRate;
    devRate.set(c.dev, (devRate.get(c.dev) || 0) + r);
    devRx.set(c.dev, (devRx.get(c.dev) || 0) + c.rxRate);
    devTx.set(c.dev, (devTx.get(c.dev) || 0) + c.txRate);
    const di = destInfo(c);
    let d = dst.get(di.key);
    if (!d) { d = { ...di, rx: 0, tx: 0, n: 0, bytes: 0 }; dst.set(di.key, d); }
    d.rx += c.rxRate; d.tx += c.txRate; d.n++; d.bytes += c.rx + c.tx;
    if (!F.pairs.has(c.dev)) F.pairs.set(c.dev, new Set());
    F.pairs.get(c.dev).add(di.key);
  }
  const small = F.w < 640;
  const want = new Set();
  // Stable layout: nodes already on screen keep their slot while they stay
  // relevant; new ones are appended. Avoids nodes jumping every second.
  const choose = (cands, side, maxN, mkMore) => {
    const rank = new Map(cands.map((c, i) => [c.nid, i]));
    const cap = cands.length > maxN ? maxN - 1 : maxN;
    const prev = [...F.nodes.values()].filter((n) => n.side === side && !n.gone && !n.more)
      .sort((a, b) => a.slot - b.slot).map((n) => n.id);
    const resort = S.order.flow === 'live' && Date.now() - (F['sorted' + side] || 0) > 3000;
    if (resort) F['sorted' + side] = Date.now();
    const shown = resort ? [] : prev.filter((id) => rank.has(id) && rank.get(id) < cap + 3).slice(0, cap);
    const set = new Set(shown);
    for (const c of cands) { if (shown.length >= cap) break; if (!set.has(c.nid)) { shown.push(c.nid); set.add(c.nid); } }
    const byId = new Map(cands.map((c) => [c.nid, c]));
    const list = shown.map((id) => byId.get(id));
    const rest = cands.filter((c) => !set.has(c.nid));
    if (rest.length) list.push(mkMore(rest));
    const top = 34, bottom = F.h - 26, n = list.length;
    const gap = n > 1 ? Math.min(54, (bottom - top) / (n - 1)) : 0;
    const start = (top + bottom) / 2 - (gap * (n - 1)) / 2;
    list.forEach((it, i) => {
      want.add(it.nid);
      let nd = F.nodes.get(it.nid);
      if (!nd || nd.gone) {
        nd = { id: it.nid, side, y: start + i * gap, alpha: 0, x: 0, w: 0 };
        F.nodes.set(it.nid, nd);
      }
      Object.assign(nd, it, { ty: start + i * gap, slot: i, gone: false });
    });
  };
  const devCands = [...devRate.keys()].map((id) => ({ id, rate: devRate.get(id), rx: devRx.get(id), tx: devTx.get(id) }))
    .sort((a, b) => b.rate - a.rate || (S.devs.get(b.id)?.rx || 0) - (S.devs.get(a.id)?.rx || 0))
    .map((d) => {
      const dv = S.devs.get(d.id) || {};
      const name = dv.name || dv.ip || d.id;
      return { nid: 'd:' + d.id, devId: d.id, label: name, icon: iconFor(name), rx: d.rx, tx: d.tx, sub: fmtRate(d.rx + d.tx), more: false };
    });
  choose(devCands, 'L', small ? 6 : 9, (rest) => {
    const rx = rest.reduce((s, d) => s + d.rx, 0), tx = rest.reduce((s, d) => s + d.tx, 0);
    return { nid: 'd:+', devId: null, label: '+' + rest.length + ' weitere', icon: 'generic', rx, tx, sub: fmtRate(rx + tx), more: true };
  });
  const dstCands = [...dst.values()].sort((a, b) => (b.rx + b.tx) - (a.rx + a.tx) || b.bytes - a.bytes)
    .map((d) => ({ nid: 't:' + d.key, destKey: d.key, label: d.label, color: d.color, rx: d.rx, tx: d.tx,
      sub: fmtRate(d.rx + d.tx) + (d.n > 1 ? ' · ' + d.n + ' Verb.' : ''), more: false }));
  choose(dstCands, 'R', small ? 6 : 10, (rest) => {
    const rx = rest.reduce((s, d) => s + d.rx, 0), tx = rest.reduce((s, d) => s + d.tx, 0);
    return { nid: 't:+', destKey: null, label: '+' + rest.length + ' weitere', color: '#64748b', rx, tx, sub: fmtRate(rx + tx), more: true };
  });
  for (const [id, nd] of F.nodes) if (!want.has(id)) nd.gone = true;
  F.box.rate = [...devRate.values()].reduce((s, v) => s + v, 0);
  $('#flowEmpty').classList.toggle('gone', F.nodes.size > 0 && want.size > 0);
  if (!want.size) $('#flowEmptyText').textContent = emptyText();
}
function emptyText() {
  const st = S.snap?.status?.state;
  if (st === 'live') return S.filter.dev || S.filter.q || S.filter.net || S.filter.kind !== 'all' ? 'Keine Verbindungen für diesen Filter' : 'Gerade ist es ruhig im Netz …';
  if (st === 'error') return 'Keine Verbindung zur FRITZ!Box';
  if (st === 'setup') return 'Bitte zuerst die FRITZ!Box einrichten';
  return 'Warte auf Daten von der FRITZ!Box …';
}
function nodeAnchor(nd) {
  return nd.side === 'L' ? { x: nd.x + nd.w, y: nd.y } : { x: nd.x, y: nd.y };
}
function bez(p0, p3, t) {
  const mx = (p0.x + p3.x) / 2;
  const p1 = { x: mx, y: p0.y }, p2 = { x: mx, y: p3.y };
  const u = 1 - t;
  return {
    x: u * u * u * p0.x + 3 * u * u * t * p1.x + 3 * u * t * t * p2.x + t * t * t * p3.x,
    y: u * u * u * p0.y + 3 * u * u * t * p1.y + 3 * u * t * t * p2.y + t * t * t * p3.y,
  };
}
function isHi(nd) {
  const h = F.hover;
  if (!h) return true;
  if (h.id === nd.id || h.id === 'box') return true;
  if (h.side === 'L' && nd.side === 'R') return !!(h.devId && F.pairs.get(h.devId)?.has(nd.destKey));
  if (h.side === 'R' && nd.side === 'L') return !!(nd.devId && F.pairs.get(nd.devId)?.has(h.destKey));
  return false;
}
function drawFlow(now) {
  const cv = $('#flow');
  const { ctx, w, h } = fitCanvas(cv);
  const dt = Math.min(0.1, (now - F.last) / 1000); F.last = now;
  F.w = w; F.h = h;
  ctx.clearRect(0, 0, w, h);
  const box = { x: w / 2, y: h / 2, s: w < 640 ? 64 : 84 };
  const small = w < 640;
  const fontMain = small ? '600 12px system-ui, sans-serif' : '600 13px system-ui, sans-serif';
  const fontSub = '11px system-ui, sans-serif';

  // layout + animate nodes
  const maxRate = Math.max(1, ...[...F.nodes.values()].map((n) => n.rx + n.tx));
  for (const [id, nd] of F.nodes) {
    nd.y += (nd.ty - nd.y) * Math.min(1, dt * 6);
    nd.alpha += ((nd.gone ? 0 : 1) - nd.alpha) * Math.min(1, dt * (nd.gone ? 12 : 5));
    if (nd.gone && nd.alpha < 0.02) { F.nodes.delete(id); continue; }
    ctx.font = fontMain;
    const lw = Math.min(small ? 96 : 170, ctx.measureText(nd.label).width);
    ctx.font = fontSub;
    const sw = ctx.measureText(nd.sub || '').width;
    nd.w = 44 + Math.max(lw, sw) + 14;
    nd.labelW = small ? 96 : 170;
    nd.x = nd.side === 'L' ? 6 : w - 6 - nd.w;
  }

  const hiBox = !F.hover || F.hover.id === 'box';
  // links
  ctx.lineCap = 'round';
  for (const nd of F.nodes.values()) {
    const a = nodeAnchor(nd);
    const b = nd.side === 'L' ? { x: box.x - box.s / 2, y: box.y } : { x: box.x + box.s / 2, y: box.y };
    const rate = nd.rx + nd.tx;
    const width = 1.2 + 7 * Math.sqrt(rate / maxRate);
    const hi = isHi(nd);
    const al = nd.alpha * (hi ? 1 : 0.18);
    const g = ctx.createLinearGradient(a.x, 0, b.x, 0);
    const c1 = nd.side === 'L' ? css.down : (nd.color || css.down);
    g.addColorStop(0, withAlpha(c1.startsWith('#') || c1.startsWith('hsl') ? c1 : '#2fb9ff', 0.55 * al));
    g.addColorStop(1, withAlpha('#8b5cf6', 0.35 * al));
    ctx.strokeStyle = g; ctx.lineWidth = width;
    ctx.beginPath(); ctx.moveTo(a.x, a.y);
    const mx = (a.x + b.x) / 2;
    ctx.bezierCurveTo(mx, a.y, mx, b.y, b.x, b.y); ctx.stroke();
    // spawn particles
    for (const [dir, r] of [['d', nd.rx], ['u', nd.tx]]) {
      if (r <= 0 || nd.gone) continue;
      const intensity = clamp(Math.log10(1 + r / 500) / 4.5, 0, 1); // ~0..1 for 0..15 MB/s
      const key = 'acc' + dir;
      nd[key] = (nd[key] || 0) + dt * (0.6 + 26 * intensity * intensity);
      while (nd[key] >= 1 && F.parts.length < 700) {
        nd[key] -= 1;
        // download: box -> device (L) / dest -> box (R); upload the opposite
        const fromBox = (nd.side === 'L') === (dir === 'd');
        F.parts.push({ nd, fromBox, dir, t: 0, speed: 0.45 + 0.6 * intensity + Math.random() * 0.15, size: 1.4 + 2.2 * intensity * Math.random() + 0.6, off: (Math.random() - 0.5) * width * 0.6 });
      }
      if (nd[key] > 3) nd[key] = 3;
    }
  }

  // particles
  ctx.save();
  ctx.globalCompositeOperation = document.documentElement.dataset.theme === 'dark' ? 'lighter' : 'source-over';
  const keep = [];
  for (const p of F.parts) {
    p.t += p.speed * dt;
    if (p.t >= 1 || !F.nodes.has(p.nd.id)) continue;
    keep.push(p);
    const a = nodeAnchor(p.nd);
    const b = p.nd.side === 'L' ? { x: box.x - box.s / 2, y: box.y } : { x: box.x + box.s / 2, y: box.y };
    const pt = p.fromBox ? bez(b, a, p.t) : bez(a, b, p.t);
    const col = p.dir === 'd' ? css.down2 : css.up2;
    const hi = isHi(p.nd);
    const al = p.nd.alpha * (hi ? 1 : 0.12) * Math.sin(Math.PI * p.t) ** 0.5;
    ctx.fillStyle = withAlpha(col, 0.18 * al);
    ctx.beginPath(); ctx.arc(pt.x, pt.y + p.off, p.size * 3, 0, Math.PI * 2); ctx.fill();
    ctx.fillStyle = withAlpha(col, 0.95 * al);
    ctx.beginPath(); ctx.arc(pt.x, pt.y + p.off, p.size, 0, Math.PI * 2); ctx.fill();
  }
  F.parts = keep;
  ctx.restore();

  // nodes
  for (const nd of F.nodes.values()) {
    const hi = isHi(nd);
    ctx.globalAlpha = nd.alpha * (hi ? 1 : 0.35);
    const y = nd.y - 20;
    roundRect(ctx, nd.x, y, nd.w, 40, 12);
    ctx.fillStyle = css['card-solid']; ctx.fill();
    ctx.strokeStyle = F.hover && F.hover.id === nd.id ? css.down : css['border-strong']; ctx.lineWidth = 1; ctx.stroke();
    const cx = nd.side === 'L' ? nd.x + 22 : nd.x + nd.w - 22;
    ctx.beginPath(); ctx.arc(cx, nd.y, 15, 0, Math.PI * 2);
    if (nd.side === 'L') {
      ctx.fillStyle = css.pill; ctx.fill();
      ctx.save(); ctx.translate(cx - 9, nd.y - 9); ctx.scale(0.75, 0.75);
      ctx.strokeStyle = css.text2; ctx.lineWidth = 2; ctx.lineCap = 'round'; ctx.lineJoin = 'round';
      ctx.stroke(path2d(nd.icon)); ctx.restore();
    } else {
      ctx.fillStyle = nd.color || '#64748b'; ctx.fill();
      ctx.fillStyle = '#fff'; ctx.font = '800 12px system-ui, sans-serif'; ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
      ctx.fillText(nd.label.startsWith('+') ? '+' : nd.label.replace(/^www\./, '').charAt(0).toUpperCase(), cx, nd.y + 0.5);
    }
    ctx.textBaseline = 'alphabetic';
    ctx.textAlign = nd.side === 'L' ? 'left' : 'right';
    const tx = nd.side === 'L' ? nd.x + 44 : nd.x + nd.w - 44;
    ctx.font = fontMain; ctx.fillStyle = css.text;
    ctx.fillText(ellipsize(ctx, nd.label, nd.labelW), tx, nd.y - 2);
    ctx.font = fontSub; ctx.fillStyle = css.muted;
    ctx.fillText(nd.sub || '', tx, nd.y + 12);
  }
  ctx.globalAlpha = 1;

  // FRITZ!Box node
  F.box.pulse += dt * (1 + clamp(Math.log10(1 + F.box.rate / 1000) / 2, 0, 3));
  const glow = 0.5 + 0.5 * Math.sin(F.box.pulse * 2);
  ctx.save();
  ctx.globalAlpha = hiBox ? 1 : 0.6;
  const s = box.s;
  const gr = ctx.createRadialGradient(box.x, box.y, s * 0.3, box.x, box.y, s * (1.1 + 0.15 * glow));
  gr.addColorStop(0, withAlpha('#2fb9ff', 0.28)); gr.addColorStop(1, withAlpha('#8b5cf6', 0));
  ctx.fillStyle = gr; ctx.beginPath(); ctx.arc(box.x, box.y, s * 1.3, 0, Math.PI * 2); ctx.fill();
  roundRect(ctx, box.x - s / 2, box.y - s / 2, s, s, 18);
  const bg = ctx.createLinearGradient(box.x - s / 2, box.y - s / 2, box.x + s / 2, box.y + s / 2);
  bg.addColorStop(0, '#1d2a44'); bg.addColorStop(1, '#2a1d4a');
  ctx.fillStyle = document.documentElement.dataset.theme === 'dark' ? bg : '#ffffff'; ctx.fill();
  ctx.lineWidth = 1.5; ctx.strokeStyle = withAlpha('#4cc3ff', 0.5 + 0.4 * glow); ctx.stroke();
  ctx.save(); ctx.translate(box.x - s * 0.22, box.y - s * 0.3); ctx.scale(s / 54, s / 54);
  ctx.strokeStyle = document.documentElement.dataset.theme === 'dark' ? '#cfe6ff' : '#3b4a66'; ctx.lineWidth = 1.8; ctx.lineCap = 'round'; ctx.lineJoin = 'round';
  ctx.stroke(path2d('router')); ctx.restore();
  ctx.textAlign = 'center'; ctx.fillStyle = css.text; ctx.font = '700 12px system-ui, sans-serif';
  ctx.fillText(ellipsize(ctx, boxLabel(), s - 8), box.x, box.y + s * 0.28);
  ctx.font = '600 11px system-ui, sans-serif'; ctx.fillStyle = css.down;
  ctx.fillText(fmtRate(F.box.rate), box.x, box.y + s / 2 + 18);
  ctx.restore();
  F.boxRect = { x: box.x - s / 2, y: box.y - s / 2, w: s, h: s };
}
function ellipsize(ctx, s, max) {
  if (ctx.measureText(s).width <= max) return s;
  let lo = 0, hi = s.length;
  while (lo < hi) { const m = (lo + hi + 1) >> 1; if (ctx.measureText(s.slice(0, m) + '…').width <= max) lo = m; else hi = m - 1; }
  return s.slice(0, lo) + '…';
}
function flowHit(x, y) {
  for (const nd of F.nodes.values()) {
    if (nd.gone) continue;
    if (x >= nd.x && x <= nd.x + nd.w && y >= nd.y - 20 && y <= nd.y + 20) return nd;
  }
  const b = F.boxRect;
  if (b && x >= b.x && x <= b.x + b.w && y >= b.y && y <= b.y + b.h) return { id: 'box' };
  return null;
}
$('#flow').addEventListener('mousemove', (ev) => {
  const r = ev.currentTarget.getBoundingClientRect();
  const x = ev.clientX - r.left, y = ev.clientY - r.top;
  const nd = flowHit(x, y);
  F.hover = nd;
  ev.currentTarget.style.cursor = nd && nd.id !== 'box' && !nd.more ? 'pointer' : 'default';
  const tip = $('#flowTip');
  if (!nd) { tip.hidden = true; return; }
  let title, rx, tx, extra = '';
  if (nd.id === 'box') {
    title = esc(boxLabel()); rx = S.snap?.totals.rxRate || 0; tx = S.snap?.totals.txRate || 0;
    extra = (boxModel() ? `<div class="tr"><span>${esc(boxModel())}</span></div>` : '') +
      `<div class="tr"><span>Verbindungen</span><span>${S.snap?.totals.conns || 0}</span></div>`;
  } else {
    title = esc(nd.label); rx = nd.rx; tx = nd.tx;
    if (nd.devId) {
      const d = S.devs.get(nd.devId);
      if (d) extra = `<div class="tr"><span>IP</span><span>${esc(d.ip)}</span></div><div class="tr"><span>Verbindungen</span><span>${d.conns}</span></div>`;
    }
  }
  tip.innerHTML = `<b>${title}</b><div class="tr"><span>Download</span><span class="d">${fmtRate(rx)}</span></div><div class="tr"><span>Upload</span><span class="u">${fmtRate(tx)}</span></div>${extra}`;
  tip.hidden = false;
  const tw = tip.offsetWidth, th = tip.offsetHeight;
  tip.style.left = clamp(x + 14, 4, r.width - tw - 4) + 'px';
  tip.style.top = clamp(y + 14, 4, r.height - th - 4) + 'px';
});
$('#flow').addEventListener('mouseleave', () => { F.hover = null; $('#flowTip').hidden = true; });
$('#flow').addEventListener('click', (ev) => {
  const r = ev.currentTarget.getBoundingClientRect();
  const nd = flowHit(ev.clientX - r.left, ev.clientY - r.top);
  if (!nd || nd.id === 'box' || nd.more) return;
  if (nd.devId) setDevFilter(S.filter.dev === nd.devId ? '' : nd.devId);
  else if (nd.destKey) toggleQuery(nd.label);
});

/* ---------- filtering ---------- */
function devName(id) { const d = S.devs.get(id); return d ? (d.name || d.ip || id) : id; }
function matchesQuery(c) {
  const q = S.filter.q;
  if (!q) return true;
  const di = destInfo(c);
  return (devName(c.dev) + ' ' + c.host + ' ' + c.rip + ' ' + c.lip + ' ' + c.svc + ' ' + c.proto + ' ' + di.label + ' ' + c.rport).toLowerCase().includes(q);
}
const FAIL_TEXT = { timeout: 'Keine Antwort', refused: 'Abgelehnt', unreachable: 'Nicht erreichbar' };
function kindOK(c) {
  const k = S.filter.kind;
  if (k === 'fail') return !!c.fail;
  return k === 'all' || (k === 'wan' ? c.kind === 'wan' : c.kind !== 'wan');
}
function netOK(devId) {
  return !S.filter.net || (S.devs.get(devId)?.net || '') === S.filter.net;
}
function visibleConns(conns, forFlow) {
  return conns.filter((c) => kindOK(c) && netOK(c.dev) && (!S.filter.dev || c.dev === S.filter.dev) && (!forFlow || !c.closed || c.fail) && matchesQuery(c));
}
const NET_ORDER = ['LAN', 'WLAN 2,4 GHz', 'WLAN 5 GHz', 'WLAN 6 GHz', 'WLAN', 'Gast'];
let netKey = '';
function renderFilterBar(s) {
  const counts = new Map();
  for (const d of s.devices) if (d.net && d.online) counts.set(d.net, (counts.get(d.net) || 0) + 1);
  if (S.filter.net && !counts.has(S.filter.net)) counts.set(S.filter.net, 0);
  const nets = [...counts.keys()].sort((a, b) => (NET_ORDER.indexOf(a) + 99) % 99 - (NET_ORDER.indexOf(b) + 99) % 99 || a.localeCompare(b));
  const key = nets.map((n) => n + counts.get(n)).join('|') + '#' + S.filter.net;
  if (key !== netKey) {
    netKey = key;
    $('#netPills').innerHTML = `<button type="button" data-v="">Alle</button>` +
      nets.map((n) => `<button type="button" data-v="${esc(n)}">${esc(n)}<small>${counts.get(n)}</small></button>`).join('');
  }
  $('#netPills').querySelectorAll('button').forEach((b) => b.classList.toggle('on', b.dataset.v === S.filter.net));
  $('#kindPills').querySelectorAll('button').forEach((b) => b.classList.toggle('on', b.dataset.v === S.filter.kind));
  const parts = [];
  if (S.filter.kind !== 'all') parts.push({ wan: 'nur Internet', int: 'nur intern', fail: 'nur Fehler' }[S.filter.kind]);
  const nf = s.totals.failed || 0;
  $('#failCount').textContent = nf ? ' ' + nf : '';
  if (S.filter.net) parts.push('nur ' + S.filter.net);
  if (S.filter.dev) parts.push('nur ' + devName(S.filter.dev));
  if (S.filter.q) parts.push(`Suche „${$('#search').value.trim() || S.filter.q}“`);
  const resettable = S.filter.q || S.filter.dev || S.filter.net;
  const html = (parts.length ? 'Gefiltert: ' + esc(parts.join(', ')) : '') +
    (resettable ? ' <button type="button" class="btn link" id="filterReset" title="Suche, Geräte- und Netzfilter aufheben">Filter aufheben</button>' : '');
  if ($('#filterInfo').dataset.html !== html) { $('#filterInfo').dataset.html = html; $('#filterInfo').innerHTML = html; }
}
// Sets the search box (e.g. from a click on a destination or problem); the
// same value again clears it.
function toggleQuery(q) {
  const same = S.filter.q === q.toLowerCase();
  $('#search').value = same ? '' : q; S.filter.q = same ? '' : q.toLowerCase(); render();
}
$('#filterInfo').addEventListener('click', (e) => {
  if (!e.target.closest('#filterReset')) return;
  $('#search').value = ''; S.filter.q = '';
  S.filter.net = ''; store.set('ff-net', '');
  setDevFilter('');
});
$('#kindPills').addEventListener('click', (e) => {
  const b = e.target.closest('button'); if (!b) return;
  S.filter.kind = b.dataset.v; store.set('ff-kind', b.dataset.v); render();
});
$('#netPills').addEventListener('click', (e) => {
  const b = e.target.closest('button'); if (!b) return;
  S.filter.net = b.dataset.v; store.set('ff-net', b.dataset.v); render();
});
function setDevFilter(id) {
  S.filter.dev = id; store.set('ff-dev', id);
  render();
}
$('#search').addEventListener('input', (e) => { S.filter.q = e.target.value.trim().toLowerCase(); render(); });
$('#devChip').addEventListener('click', () => setDevFilter(''));

/* ---------- rendering ---------- */
function renderKpis(s) {
  const t = s.totals;
  const w = s.status.wan;
  const down = w ? w.down : t.rxRate, up = w ? w.up : t.txRate;
  tweenNum($('#downVal'), down, (v) => rateParts(v)[0]); $('#downUnit').textContent = rateParts(down)[1];
  tweenNum($('#upVal'), up, (v) => rateParts(v)[0]); $('#upUnit').textContent = rateParts(up)[1];
  const last60 = S.hist.slice(-60);
  const coverage = (k) => {
    const wSum = last60.reduce((a, p) => a + (p['w' + k] || 0), 0), cSum = last60.reduce((a, p) => a + p[k], 0);
    return wSum > 50_000 ? Math.min(100, Math.round((cSum / wSum) * 100)) : null;
  };
  const foot = (k, lineKbit, cur, el, bar) => {
    const parts = [];
    if (w && lineKbit) {
      const pct = Math.min(100, (cur / (lineKbit * 125)) * 100);
      parts.push(`${Math.round(pct)} % von ${fmtRate(lineKbit * 125)}`);
      $(bar).hidden = false; $(bar).firstElementChild.style.width = pct + '%';
    } else $(bar).hidden = true;
    parts.push('Spitze 60 s: ' + fmtRate(Math.max(0, ...last60.map((p) => val(p, k)))));
    const cov = w ? coverage(k) : null;
    if (cov != null) parts.push(`Mitschnitt sieht ${cov} %`);
    else if (!w) parts.push('gesamt ' + fmtBytes(k === 'rx' ? t.rx : t.tx));
    $(el).textContent = parts.join(' · ');
    $(el).title = w ? 'Wert vom FRITZ!Box-Online-Monitor (echter Durchsatz am Anschluss). „Mitschnitt sieht“: Anteil, den der Paketmitschnitt erfasst – Verkehr, den die Box per Hardware beschleunigt, fehlt dort.' : 'Wert aus dem Paketmitschnitt';
  };
  foot('rx', w && w.lineDown, down, '#downFoot', '#lineDown');
  foot('tx', w && w.lineUp, up, '#upFoot', '#lineUp');
  tweenNum($('#connVal'), t.connsWan, (v) => nf0.format(v));
  const tot = Math.max(1, t.tcp + t.udp);
  const segs = $('#connSplit').children;
  segs[0].style.width = (t.tcp / tot) * 100 + '%'; segs[1].style.width = (t.udp / tot) * 100 + '%';
  $('#connFoot').innerHTML = `<span style="color:#10b981">●</span> TCP ${t.tcp} &nbsp; <span style="color:#f59e0b">●</span> UDP ${t.udp} &nbsp;·&nbsp; ${t.conns} gesamt` +
    (t.failed || t.dnsFail ? ` &nbsp;·&nbsp; <span style="color:var(--err);font-weight:600">⚠ ${t.failed} fehlgeschl., ${t.dnsFail} DNS</span>` : '');
  tweenNum($('#devVal'), t.active, (v) => nf0.format(v));
  $('#devFoot').textContent = t.online + ' online im Heimnetz';
  const top = s.devices.filter((d) => d.rxRate + d.txRate > 0).slice(0, 7);
  const av = $('#devAvatars');
  av.innerHTML = top.map((d) => `<span class="av" title="${esc(d.name || d.ip)}">${svgIcon(iconFor(d.name))}</span>`).join('') +
    (t.active > top.length ? `<span class="av more">+${t.active - top.length}</span>` : '');
}

function renderTopDest(conns) {
  const agg = new Map();
  for (const c of conns) {
    const di = destInfo(c);
    let d = agg.get(di.key);
    if (!d) { d = { ...di, rate: 0, bytes: 0, n: 0 }; agg.set(di.key, d); }
    d.rate += c.rxRate + c.txRate; d.bytes += c.rx + c.tx; d.n++;
  }
  const byVolume = S.order.dest === 'static';
  // as many entries as fit into the tile (it is as tall as the Live-Verkehr map)
  const room = $('#topDest').clientHeight;
  const fit = room > 0 ? clamp(Math.floor((room + 4) / 48), 5, 25) : 7;
  const list = [...agg.values()].sort((a, b) => byVolume ? b.bytes - a.bytes || a.label.localeCompare(b.label) : b.rate - a.rate || b.bytes - a.bytes).slice(0, fit);
  const max = Math.max(1, ...list.map((d) => d.rate));
  const ol = $('#topDest');
  ol.innerHTML = list.length ? list.map((d) => `
    <li data-q="${esc(d.label)}"><span class="badge" style="background:${d.color}">${esc(d.label.replace(/^www\./, '').charAt(0).toUpperCase())}</span>
      <div class="nm"><b>${esc(d.label)}</b><div class="bar"><i style="width:${(d.rate / max) * 100}%;background:linear-gradient(90deg, ${d.color}, ${withAlpha(d.color, 0.5)})"></i></div></div>
      <div class="val">${fmtRate(d.rate)}<small>${d.n} Verb. · ${fmtBytes(d.bytes)}</small></div></li>`).join('')
    : '<li class="rank-empty">Noch keine Ziele</li>';
}
$('#topDest').addEventListener('click', (e) => {
  const li = e.target.closest('li[data-q]');
  if (!li) return;
  toggleQuery(li.dataset.q);
});

const devEls = new Map();
function renderDevices(s) {
  const ul = $('#devices');
  const list = s.devices.filter((d) => netOK(d.id));
  if (S.order.dev === 'static') {
    const nm = (d) => (d.name || d.ip || d.id).toLowerCase();
    list.sort((a, b) => nm(a).localeCompare(nm(b), 'de'));
  }
  const max = Math.max(1, ...list.map((d) => d.rxRate + d.txRate));
  const seen = new Set();
  list.forEach((d, i) => {
    seen.add(d.id);
    let li = devEls.get(d.id);
    if (!li) {
      li = el('li');
      li.innerHTML = `<span class="dicon"></span><div class="nm"><b></b><small></small><div class="bar"><i></i></div></div><div class="rates"><div class="d"></div><div class="u"></div></div>`;
      li.addEventListener('click', () => setDevFilter(S.filter.dev === d.id ? '' : d.id));
      devEls.set(d.id, li);
    }
    const name = d.name || d.ip || d.id;
    const ic = iconFor(name);
    const icon = li.querySelector('.dicon');
    if (icon.dataset.ic !== ic) { icon.innerHTML = svgIcon(ic); icon.dataset.ic = ic; }
    icon.classList.toggle('on', !!d.online);
    li.querySelector('b').textContent = name;
    const iface = d.net || (/802\.11|wlan/i.test(d.iface) ? 'WLAN' : d.iface ? (/ether|lan/i.test(d.iface) ? 'LAN' : d.iface) : '');
    li.querySelector('small').innerHTML = (iface ? `<span class="tag">${esc(iface)}</span>` : '') + esc(d.ip || d.mac || '') + (d.conns ? ` · ${d.conns} Verb.` : '');
    li.querySelector('.bar i').style.width = ((d.rxRate + d.txRate) / max) * 100 + '%';
    li.querySelector('.rates .d').textContent = d.rxRate > 0 ? '↓ ' + fmtRate(d.rxRate) : '';
    li.querySelector('.rates .u').textContent = d.txRate > 0 ? '↑ ' + fmtRate(d.txRate) : d.rxRate > 0 ? '' : fmtBytes(d.rx + d.tx);
    li.classList.toggle('idle', d.rxRate + d.txRate === 0 && !(d.idle >= 0 && d.idle < 60));
    li.classList.toggle('sel', S.filter.dev === d.id);
    if (ul.children[i] !== li) ul.insertBefore(li, ul.children[i] || null);
  });
  for (const [id, li] of devEls) if (!seen.has(id)) { li.remove(); devEls.delete(id); }
  $('#devCount').textContent = S.filter.net ? `${list.length} in ${S.filter.net}` : `${s.totals.active} aktiv · ${list.length} bekannt`;
}

function ago(sec) {
  sec = Math.max(0, sec | 0);
  if (sec < 60) return 'vor ' + sec + ' s';
  if (sec < 3600) return 'vor ' + Math.floor(sec / 60) + ' min';
  return 'vor ' + Math.floor(sec / 3600) + ' h';
}
function renderProblems(s) {
  const devOK = (id) => netOK(id) && (!S.filter.dev || id === S.filter.dev);
  const q = S.filter.q;
  const dns = (s.dnsFails || []).filter((d) => devOK(d.dev) && (!q || (d.name + ' ' + devName(d.dev)).toLowerCase().includes(q)));
  $('#dnsFails').innerHTML = dns.length ? dns.slice(0, 60).map((d) => `
    <li data-q="${esc(d.name)}" title="${esc(d.reason)}"><span class="pi ${/Keine Antwort/.test(d.reason) ? 'w' : ''}">?</span>
      <div><b>${esc(d.name)}</b><small>${esc(devName(d.dev))} · ${esc(d.reason)}</small></div>
      <div class="cnt">${d.count}×<small>${ago(d.ago)}</small></div></li>`).join('')
    : '<li class="ok">✓ Alle DNS-Anfragen wurden beantwortet</li>';
  const groups = new Map();
  for (const c of s.conns) {
    if (!c.fail || !devOK(c.dev)) continue;
    const k = c.dev + '|' + (c.host || c.rip) + '|' + c.rport + '|' + c.fail;
    let g = groups.get(k);
    if (!g) { g = { c, n: 0, idle: c.idle }; groups.set(k, g); }
    g.n++; g.idle = Math.min(g.idle, c.idle);
  }
  const list = [...groups.values()].filter((g) => !q || (devName(g.c.dev) + ' ' + g.c.host + ' ' + g.c.rip).toLowerCase().includes(q))
    .sort((a, b) => a.idle - b.idle);
  $('#connFails').innerHTML = list.length ? list.slice(0, 60).map((g) => `
    <li data-q="${esc(g.c.host || g.c.rip)}"><span class="pi ${g.c.fail === 'timeout' ? 'w' : ''}">!</span>
      <div><b>${esc(g.c.host || g.c.rip)}<span class="muted"> :${g.c.rport}</span></b><small>${esc(devName(g.c.dev))} · ${FAIL_TEXT[g.c.fail]} · ${g.c.proto}</small></div>
      <div class="cnt">${g.n}×<small>${ago(g.idle)}</small></div></li>`).join('')
    : '<li class="ok">✓ Keine fehlgeschlagenen Verbindungen</li>';
  $('#probInfo').textContent = dns.length || list.length ? `${dns.length} DNS · ${list.length} Verbindungen` : 'alles in Ordnung';
}
for (const id of ['#dnsFails', '#connFails']) {
  $(id).addEventListener('click', (e) => {
    const li = e.target.closest('li[data-q]'); if (!li) return;
    toggleQuery(li.dataset.q);
  });
}

/* ---------- FRITZ!Box utilisation ---------- */
function drawGauge(el, frac, color, text, title) {
  const cv = el.querySelector('canvas');
  const { ctx, w, h } = fitCanvas(cv);
  ctx.clearRect(0, 0, w, h);
  const cx = w / 2, cy = h / 2, r = Math.min(w, h) / 2 - 5;
  const a0 = Math.PI * 0.75, a1 = Math.PI * 2.25;
  ctx.lineCap = 'round'; ctx.lineWidth = 7;
  ctx.strokeStyle = css.grid; ctx.beginPath(); ctx.arc(cx, cy, r, a0, a1); ctx.stroke();
  if (frac > 0) {
    const g = ctx.createLinearGradient(0, h, w, 0);
    g.addColorStop(0, withAlpha(color[0], 1)); g.addColorStop(1, withAlpha(color[1], 1));
    ctx.strokeStyle = g; ctx.beginPath(); ctx.arc(cx, cy, r, a0, a0 + (a1 - a0) * clamp(frac, 0, 1)); ctx.stroke();
  }
  el.querySelector('b').textContent = text;
  el.title = title || '';
}
function drawBoxHist(l) {
  const { ctx, w, h } = fitCanvas($('#boxHist'));
  ctx.clearRect(0, 0, w, h);
  ctx.strokeStyle = css.grid; ctx.lineWidth = 1;
  for (const f of [0, 0.5, 1]) { const y = 4 + (h - 16) * f + 0.5; ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(w, y); ctx.stroke(); }
  ctx.fillStyle = css.muted; ctx.font = '10px system-ui, sans-serif'; ctx.textAlign = 'left';
  ctx.fillText('−24 h', 0, h - 2); ctx.textAlign = 'right'; ctx.fillText('jetzt', w, h - 2);
  const line = (vals, max, c1, c2, fill) => {
    if (!vals || vals.length < 2) return;
    const step = w / 239, x0 = w - (vals.length - 1) * step;
    ctx.beginPath();
    vals.forEach((v, i) => { const x = x0 + i * step, y = 4 + (h - 16) * (1 - clamp(v / max, 0, 1)); i ? ctx.lineTo(x, y) : ctx.moveTo(x, y); });
    ctx.strokeStyle = c1; ctx.lineWidth = 1.6; ctx.stroke();
    if (fill) {
      ctx.lineTo(w, h - 12); ctx.lineTo(x0, h - 12); ctx.closePath();
      const g = ctx.createLinearGradient(0, 0, 0, h); g.addColorStop(0, withAlpha(c2, 0.35)); g.addColorStop(1, withAlpha(c2, 0));
      ctx.fillStyle = g; ctx.fill();
    }
  };
  line(l.cpuHist, 100, css.down2, css.down, true);
  line(l.tempHist, 100, '#ff8a3d', '#ff8a3d', false);
}
function renderBox(st) {
  $('#boxTitle').textContent = st.boxName || 'FRITZ!Box';
  $('#boxSub').textContent = [st.boxModel, st.boxOS && 'FRITZ!OS ' + st.boxOS].filter(Boolean).join(' · ');
  const off = !st.boxStats || !st.load;
  $('#boxOn').hidden = off;
  $('#boxOff').hidden = !off;
  if (off) {
    $('#boxOff').innerHTML = !st.boxStats
      ? 'Auslastung ist abgeschaltet – <a href="#" data-open-setup>in den Einstellungen aktivieren</a>.'
      : esc(st.loadInfo || 'Auslastung wird geladen …');
    return;
  }
  const l = st.load;
  const tempCol = l.temp >= 85 ? ['#ff5b6e', '#ff2d55'] : l.temp >= 70 ? ['#ffb547', '#ff8a3d'] : ['#2ee59d', '#10b981'];
  drawGauge($('#gCpu'), l.cpu / 100, [css.down, css.down2], Math.round(l.cpu) + ' %', 'CPU-Last');
  drawGauge($('#gTemp'), l.temp / 100, tempCol, Math.round(l.temp) + ' °C', 'Prozessortemperatur');
  drawGauge($('#gRam'), (l.ramUsed + l.ramCache) / 100, [css.up, css.up2], Math.round(l.ramUsed + l.ramCache) + ' %',
    `RAM: ${Math.round(l.ramUsed)} % belegt, ${Math.round(l.ramCache)} % Cache, ${Math.round(l.ramFree)} % frei`);
  drawGauge($('#gPow'), l.power / 100, ['#2ee59d', '#ffc061'], Math.round(l.power) + ' %',
    `Energieverbrauch: ${Math.round(l.power)} % des Maximalverbrauchs\nProzessor ${Math.round(l.powerSys)} % · WLAN ${Math.round(l.powerWlan)} % · DSL ${Math.round(l.powerDsl)} % · USB ${Math.round(l.powerUsb)} %`);
  drawBoxHist(l);
  const w = st.wan;
  const dur = (sec) => { sec = sec | 0; const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60); return (d ? d + ' T ' : '') + (d || h ? h + ' h ' : '') + m + ' min'; };
  $('#wanLine').innerHTML = w ? `<span class="dot ${w.connected ? '' : 'off'}"></span><b>Internet ${w.connected ? 'verbunden' : 'getrennt'}</b>` +
    (w.connected && w.uptime ? `<span class="muted">seit ${dur(w.uptime)}</span>` : '') +
    (w.lineDown ? `<span class="muted">· ${esc(w.media || '')} ${fmtRate(w.lineDown * 125)} ↓ / ${fmtRate(w.lineUp * 125)} ↑</span>` : '') : '';
}
document.addEventListener('click', (e) => { if (e.target.closest('#boxOff [data-open-setup]')) { e.preventDefault(); openSetup(); } });

const rowEls = new Map();
let firstTable = true;
function sortVal(c, k) {
  switch (k) {
    case 'dev': return devName(c.dev).toLowerCase();
    case 'host': return (c.host || c.rip).toLowerCase();
    case 'svc': return c.svc;
    case 'total': return c.rx + c.tx;
    case 'rate': return c.rxRate + c.txRate;
    default: return c[k];
  }
}
function renderTable(conns) {
  const k = S.sort.k, dir = S.sort.asc ? 1 : -1;
  const list = conns.slice().sort((a, b) => {
    const va = sortVal(a, k), vb = sortVal(b, k);
    if (va < vb) return -dir; if (va > vb) return dir;
    return b.age - a.age;
  }).slice(0, 250);
  const tb = $('#conns tbody');
  const seen = new Set();
  list.forEach((c, i) => {
    seen.add(c.id);
    let tr = rowEls.get(c.id);
    if (!tr) {
      tr = el('tr', firstTable ? '' : 'new');
      // flash once: moving the row (re-sorting) would otherwise restart the animation forever
      if (!firstTable) setTimeout(() => tr.classList.remove('new'), 1300);
      tr.innerHTML = '<td><div class="cdev"></div></td><td><div class="cdst"><span class="bdot"></span><div><b></b><small></small></div></div></td><td></td><td class="r"></td><td class="r"></td><td class="r"></td><td class="r"></td>';
      rowEls.set(c.id, tr);
    }
    const td = tr.children;
    const dn = devName(c.dev);
    const dk = dn + '|' + c.lip;
    if (tr.dataset.dk !== dk) { td[0].firstChild.innerHTML = svgIcon(iconFor(dn)) + `<span title="${esc(c.lip)}">${esc(dn)}</span>`; tr.dataset.dk = dk; }
    const di = destInfo(c);
    const hk = c.host + '|' + c.rip;
    if (tr.dataset.hk !== hk) {
      td[1].querySelector('.bdot').style.background = di.color;
      td[1].querySelector('b').textContent = c.host || c.rip;
      td[1].querySelector('b').title = c.host || c.rip;
      td[1].querySelector('small').textContent = (di.label !== (c.host || c.rip) ? di.label + ' · ' : '') + hostPort(c.rip, c.rport);
      td[2].innerHTML = `<span class="svc" style="border-color:${withAlpha(svcColor(c.svc), 0.45)}">${esc(c.svc)}<small>${c.proto}</small></span>`;
      tr.dataset.hk = hk;
      tr.dataset.fk = '';
    }
    td[3].textContent = c.rxRate >= 1 ? fmtRate(c.rxRate) : '–'; td[3].className = 'r ' + (c.rxRate >= 1 ? 'rd' : 'z');
    td[4].textContent = c.txRate >= 1 ? fmtRate(c.txRate) : '–'; td[4].className = 'r ' + (c.txRate >= 1 ? 'ru' : 'z');
    td[5].textContent = fmtBytes(c.rx + c.tx);
    td[6].textContent = fmtDur(c.age);
    tr.classList.toggle('closed', c.closed && !c.fail);
    tr.classList.toggle('failed', !!c.fail);
    const fk = c.fail || '';
    if (tr.dataset.fk !== fk) {
      td[2].querySelector('.failtag')?.remove();
      if (fk) td[2].insertAdjacentHTML('beforeend', `<span class="failtag">⚠ ${FAIL_TEXT[fk] || fk}</span>`);
      tr.dataset.fk = fk;
    }
    if (tb.children[i] !== tr) tb.insertBefore(tr, tb.children[i] || null);
  });
  for (const [id, tr] of rowEls) if (!seen.has(id)) { tr.remove(); rowEls.delete(id); }
  firstTable = false;
  $('#tableEmpty').hidden = list.length > 0;
  $('#connCount').textContent = conns.length > list.length ? `${list.length} von ${conns.length}` : `${conns.length} Verbindungen`;
  document.querySelectorAll('#conns th').forEach((th) => {
    th.classList.toggle('sorted', th.dataset.k === S.sort.k);
    th.classList.toggle('asc', th.dataset.k === S.sort.k && S.sort.asc);
  });
}
document.querySelectorAll('#conns th').forEach((th) => th.addEventListener('click', () => {
  const k = th.dataset.k;
  if (S.sort.k === k) S.sort.asc = !S.sort.asc;
  else { S.sort.k = k; S.sort.asc = k === 'dev' || k === 'host' || k === 'svc'; }
  render();
}));

function renderStatus(st) {
  const pill = $('#status');
  pill.dataset.state = st.state;
  const txt = {
    live: 'Live · ' + st.iface,
    connecting: st.message || 'Verbinde …',
    idle: 'Pausiert',
    error: 'Fehler',
    setup: 'Einrichtung nötig',
  }[st.state] || st.state;
  $('#statusText').textContent = txt;
  pill.title = st.message || '';
  const banners = [];
  if (st.demo) banners.push(['info', 'Demo-Modus: Es werden simulierte Daten angezeigt, keine echte FRITZ!Box.']);
  if (st.state === 'error') banners.push(['err', st.message]);
  if (st.fallback) banners.push(['warn', st.fallback]);
  if (st.guestInfo) banners.push(['warn', st.guestInfo]);
  if (st.hostsInfo) banners.push(['warn', st.hostsInfo + ' – Geräte werden mit IP-Adresse angezeigt.']);
  const key = JSON.stringify(banners);
  const box = $('#banners');
  if (box.dataset.key !== key) {
    box.dataset.key = key;
    box.innerHTML = banners.map(([c, m]) => `<div class="banner ${c}">${esc(m)}${c === 'err' ? '<button class="btn" data-open-setup>Einstellungen</button>' : ''}</div>`).join('');
  }
  if (st.state === 'setup' && !S.setupShown) { S.setupShown = true; openSetup(); }
  const title = st.boxName ? 'FritzFlow · ' + st.boxName : 'FritzFlow';
  if (document.title !== title) document.title = title;
  $('#footer').innerHTML = [
    `<span>${esc(st.boxName || 'FRITZ!Box')} <b>${esc([st.boxModel, st.boxOS && 'FRITZ!OS ' + st.boxOS].filter(Boolean).join(' · ') || st.box)}</b></span>`,
    `<span>Schnittstelle <b>${esc(st.iface)}</b></span>`,
    `<span>Detailgrad <b>${st.snaplen} B</b></span>`,
    `<span>Mitschnitt-Last <b>${fmtRate(st.streamRate)}</b></span>`,
    `<span><b>${nf0.format(S.snap?.totals.pps || 0)}</b> Pakete/s</span>`,
    `<span>${st.mode === 'always' ? 'Mitschnitt dauerhaft' : 'Mitschnitt nur bei geöffneter Ansicht'}</span>`,
    `<span>FritzFlow ${esc(st.version)}</span>`,
  ].join('');
}
$('#banners').addEventListener('click', (e) => { if (e.target.closest('[data-open-setup]')) openSetup(); });

// one broken tile must never take the others down
function safe(name, fn) { try { fn(); } catch (e) { console.error('FritzFlow: Fehler in ' + name, e); } }

function render() {
  const s = S.snap;
  if (!s) return;
  const conns = visibleConns(s.conns, false);
  const chip = $('#devChip');
  chip.hidden = !S.filter.dev;
  if (S.filter.dev) chip.textContent = devName(S.filter.dev);
  safe('Filter', () => renderFilterBar(s));
  safe('Live-Verkehr', () => flowModel(s));
  if (tileShown('dest')) safe('Top-Ziele', () => renderTopDest(conns.filter((c) => !c.closed)));
  if (tileShown('svc')) safe('Dienste', () => drawDonut(conns));
  if (tileShown('devices')) safe('Geräte', () => renderDevices(s));
  if (tileShown('conns')) safe('Verbindungen', () => renderTable(conns));
  if (tileShown('problems')) safe('Probleme', () => renderProblems(s));
}

function drawAllStatic() {
  if (tileShown('kpi-down')) safe('Download', () => drawSpark($('#sparkDown'), 'rx', css.down2));
  if (tileShown('kpi-up')) safe('Upload', () => drawSpark($('#sparkUp'), 'tx', css.up2));
  if (tileShown('history')) safe('Durchsatz', drawHistory);
  if (S.snap && tileShown('svc')) safe('Dienste', () => drawDonut(visibleConns(S.snap.conns, false)));
}

function onSnapshot(s) {
  S.snap = s;
  S.devs = new Map(s.devices.map((d) => [d.id, d]));
  if (s.totals) {
    const w = s.status.wan;
    S.hist.push({ t: s.t, rx: s.totals.rxRate, tx: s.totals.txRate, wrx: w ? w.down : null, wtx: w ? w.up : null });
    if (S.hist.length > 300) S.hist.shift();
  }
  safe('Status', () => renderStatus(s.status));
  safe('Kennzahlen', () => renderKpis(s));
  if (tileShown('box')) safe('FRITZ!Box', () => renderBox(s.status));
  render();
  drawAllStatic();
}

/* ---------- data connection ---------- */
// Primary: Server-Sent Events. Fallback: polling (some ad/tracker blockers
// or proxies break event streams). If nothing arrives at all, show a hint.
// reachable: did the last poll get any HTTP answer? (false = FritzFlow is not running)
const conn = { es: null, last: 0, mode: 'sse', pollTimer: null, started: Date.now(), hint: '', reachable: null };
const HINTS = {
  down: 'FritzFlow läuft nicht mehr – z. B. weil es sich nach dem Schließen aller Tabs beendet hat ' +
    '(Einstellung „FritzFlow beenden nach … Minuten“) oder das Fenster geschlossen wurde. Bitte FritzFlow neu starten; ' +
    'diese Seite verbindet sich dann von selbst.',
  blocked: 'FritzFlow läuft, aber hier kommen keine Daten an. Läuft ein Werbe- oder Tracker-Blocker ' +
    '(z. B. in Vivaldi, Brave, uBlock Origin)? Bitte für localhost deaktivieren und die Seite neu laden.',
};
function handleData(text) {
  conn.last = Date.now();
  conn.reachable = true;
  if (conn.hint) { conn.hint = ''; $('#blockHint').hidden = true; }
  try { onSnapshot(JSON.parse(text)); } catch (e) { console.error(e); }
}
function startSSE() {
  try { conn.es?.close(); } catch { /* ignore */ }
  try {
    conn.es = new EventSource('live');
    conn.es.onmessage = (ev) => { if (conn.mode === 'sse') handleData(ev.data); };
    conn.es.onerror = () => { /* EventSource reconnects by itself; watchdog decides */ };
  } catch { startPolling(); }
}
function startPolling() {
  if (conn.mode === 'poll') return;
  conn.mode = 'poll';
  try { conn.es?.close(); } catch { /* ignore */ }
  console.info('FritzFlow: Live-Stream blockiert oder gestört – wechsle auf Abfrage.');
  const tick = async () => {
    try {
      const r = await fetch('state', { cache: 'no-store' });
      conn.reachable = true;
      if (r.status === 200) handleData(await r.text());
    } catch { conn.reachable = false; /* app not reachable */ }
    conn.pollTimer = setTimeout(tick, 1000);
  };
  tick();
}
startSSE();
setInterval(() => {
  const silent = Date.now() - (conn.last || conn.started);
  if (conn.mode === 'sse' && silent > 5000) startPolling();
  const kind = conn.reachable === false ? 'down' : 'blocked';
  if (silent > 9000 && conn.hint !== kind) {
    conn.hint = kind;
    $('#blockHintText').textContent = HINTS[kind];
    $('#blockHint').hidden = false;
    $('#status').dataset.state = 'error';
    $('#statusText').textContent = kind === 'down' ? 'Beendet' : 'Keine Daten';
  }
}, 1000);

/* ---------- animation loop ---------- */
let prevT = performance.now();
function frame(now) {
  const dt = Math.min(0.1, (now - prevT) / 1000); prevT = now;
  if (!document.hidden) {
    stepTweens(dt);
    if (tileShown('flow')) { try { drawFlow(now); } catch (e) { console.error(e); } }
  }
  requestAnimationFrame(frame);
}
requestAnimationFrame(frame);
/* ---------- adjustable heights (drag grip, double-click resets) ---------- */
// cssVar holds the height; it is remembered per viewer under key.
// measure returns the current height (default: the variable's value).
function makeResizable({ grip, cssVar, key, DEF, MIN, MAX, onSet, measure }) {
  const root = document.documentElement;
  const cur = measure || (() => parseFloat(getComputedStyle(root).getPropertyValue(cssVar)) || DEF);
  const set = (h, save) => {
    h = Math.round(clamp(h, MIN, MAX));
    root.style.setProperty(cssVar, h + 'px');
    if (save) store.set(key, String(h));
    onSet?.();
  };
  const saved = parseInt(store.get(key, ''), 10);
  if (saved) set(saved, false);
  let startY = 0, startH = 0, moving = false;
  grip.addEventListener('pointerdown', (e) => {
    moving = true; startY = e.clientY; startH = cur();
    grip.setPointerCapture(e.pointerId); grip.classList.add('drag'); document.body.classList.add('resizing');
  });
  grip.addEventListener('pointermove', (e) => {
    if (moving) root.style.setProperty(cssVar, Math.round(clamp(startH + e.clientY - startY, MIN, MAX)) + 'px');
  });
  const end = () => {
    if (!moving) return;
    moving = false; grip.classList.remove('drag'); document.body.classList.remove('resizing');
    set(cur(), true);
  };
  grip.addEventListener('pointerup', end);
  grip.addEventListener('pointercancel', end);
  grip.addEventListener('dblclick', () => { store.set(key, ''); set(DEF, false); });
}
makeResizable({
  grip: $('#flowGrip'), cssVar: '--flow-h', key: 'ff-flow-h', DEF: 560, MIN: 320, MAX: 1400,
  measure: () => $('#flowWrap').getBoundingClientRect().height,
  onSet: () => { if (S.snap) { F.sortedL = F.sortedR = 0; render(); } },
});
makeResizable({
  grip: $('#connsGrip'), cssVar: '--conns-h', key: 'ff-conns-h', DEF: 560, MIN: 200, MAX: 3000,
  // start from the visible height: with few rows the table is shorter than its limit
  measure: () => $('#conns').closest('.table-wrap').getBoundingClientRect().height,
});

let rz;
window.addEventListener('resize', () => { clearTimeout(rz); rz = setTimeout(() => { drawAllStatic(); if (S.snap) flowModel(S.snap); }, 120); });

/* ---------- setup dialog ---------- */
const dlg = $('#setup'), form = $('#setupForm');
function setMsg(kind, text) {
  const m = $('#setupMsg');
  m.hidden = !text; m.className = 'msg ' + kind; m.textContent = text || '';
}
async function openSetup() {
  setMsg('', '');
  try {
    const r = await fetch('api/config', { cache: 'no-store' });
    S.cfg = await r.json();
  } catch { S.cfg = S.cfg || {}; }
  const c = S.cfg;
  form.fritzbox.value = c.fritzbox || 'http://fritz.box';
  form.username.value = c.username || '';
  form.password.value = '';
  form.password.placeholder = c.passwordSet ? '•••••••• (unverändert)' : 'Passwort';
  const known = ['1-lan', '2-1'].includes(c.interface || '1-lan');
  form.ifaceSel.value = known ? (c.interface || '1-lan') : 'custom';
  form.ifaceCustom.value = known ? '' : c.interface;
  $('#ifaceCustomWrap').hidden = known;
  const snap = String(c.snaplen || 200);
  const radios = [...form.querySelectorAll('input[name=snaplen]')];
  (radios.find((x) => x.value === snap) || radios[1]).checked = true;
  form.querySelectorAll('input[name=captureMode]').forEach((x) => { x.checked = x.value === (c.captureMode || 'on-demand'); });
  form.reverseDns.checked = c.reverseDns !== false;
  $('#roNote').hidden = !c.readOnly;
  // first run: show how to create a dedicated FRITZ!Box user
  $('#userHowto').open = !c.passwordSet && !c.readOnly;
  $('#userHowto').hidden = !!c.readOnly;
  $('#boxLink').href = /^https?:\/\//.test(form.fritzbox.value) ? form.fritzbox.value : 'http://fritz.box';
  form.querySelectorAll('input,select').forEach((x) => { x.disabled = !!c.readOnly; });
  $('#testBtn').disabled = $('#saveBtn').disabled = !!c.readOnly;
  $('#cancelBtn').hidden = !c.passwordSet && !c.readOnly;
  form.openBrowser.checked = c.openBrowser !== false;
  form.boxStats.checked = c.boxStats !== false;
  form.exitAfterMinutes.value = c.exitAfterMinutes ?? 2;
  form.captureGuest.checked = !!c.captureGuest;
  await fillBrowsers(c.browser || '');
  $('#cfgPath').textContent = c.path ? 'Gespeichert in: ' + c.path + (c.passwordEnv ? ' (Passwort aus FRITZFLOW_PASSWORD)' : '') : '';
  if (!dlg.open) dlg.showModal();
  (c.passwordSet ? form.fritzbox : form.password).focus();
}
let browserList = null;
async function fillBrowsers(current) {
  if (!browserList) {
    try { browserList = await (await fetch('api/browsers', { cache: 'no-store' })).json(); }
    catch { browserList = { os: '', browsers: [] }; }
  }
  const sel = form.browserSel;
  sel.innerHTML = '<option value="">Systemstandard</option>' +
    browserList.browsers.map((b) => `<option value="${esc(b.cmd)}">${esc(b.name)}</option>`).join('') +
    '<option value="custom">Anderer …</option>';
  const known = current === '' || browserList.browsers.some((b) => b.cmd === current);
  sel.value = known ? current : 'custom';
  form.browserCustom.value = known ? '' : current;
  form.browserCustom.placeholder = { windows: 'C:\\Programme\\Browser\\browser.exe', darwin: '/Applications/Arc.app' }[browserList.os] || '/usr/bin/vivaldi';
  $('#browserCustomWrap').hidden = sel.value !== 'custom';
}
form.browserSel.addEventListener('change', () => { $('#browserCustomWrap').hidden = form.browserSel.value !== 'custom'; });
$('#browserTestBtn').addEventListener('click', async () => {
  const b = formData().browser;
  setMsg('busy', 'Starte Browser …');
  try {
    const j = await post('api/browser-test', { browser: b });
    setMsg(j.ok ? 'ok' : 'err', j.ok ? 'Browser gestartet – dort sollte FritzFlow jetzt aufgehen.' : j.message);
  } catch (e) { setMsg('err', e.message); }
});
form.fritzbox.addEventListener('input', () => {
  const v = form.fritzbox.value.trim();
  $('#boxLink').href = /^https?:\/\/[^\s]+$/.test(v) ? v : 'http://' + (v || 'fritz.box');
});
form.ifaceSel.addEventListener('change', () => { $('#ifaceCustomWrap').hidden = form.ifaceSel.value !== 'custom'; });
function formData() {
  const iface = form.ifaceSel.value === 'custom' ? form.ifaceCustom.value.trim() : form.ifaceSel.value;
  return {
    fritzbox: form.fritzbox.value.trim(),
    username: form.username.value.trim(),
    password: form.password.value,
    interface: iface,
    snaplen: +(form.querySelector('input[name=snaplen]:checked')?.value || 200),
    captureMode: form.querySelector('input[name=captureMode]:checked')?.value || 'on-demand',
    reverseDns: form.reverseDns.checked,
    openBrowser: form.openBrowser.checked,
    boxStats: form.boxStats.checked,
    exitAfterMinutes: Math.max(0, Math.round(+form.exitAfterMinutes.value || 0)),
    captureGuest: form.captureGuest.checked,
    browser: form.browserSel.value === 'custom' ? form.browserCustom.value.trim() : form.browserSel.value,
  };
}
async function post(url, body) {
  const r = await fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
  let j = {};
  try { j = await r.json(); } catch { j = { ok: false, message: 'HTTP ' + r.status }; }
  return j;
}
$('#testBtn').addEventListener('click', async () => {
  setMsg('busy', 'Teste Verbindung zur FRITZ!Box …');
  $('#testBtn').disabled = true;
  try {
    const j = await post('api/test', formData());
    setMsg(j.ok ? 'ok' : 'err', j.message);
  } catch (e) { setMsg('err', 'FritzFlow nicht erreichbar: ' + e.message); }
  $('#testBtn').disabled = false;
});
form.addEventListener('submit', async (ev) => {
  ev.preventDefault();
  setMsg('busy', 'Speichere …');
  $('#saveBtn').disabled = true;
  try {
    const j = await post('api/config', formData());
    if (j.ok) { setMsg('ok', 'Gespeichert – Mitschnitt startet …'); setTimeout(() => dlg.close(), 700); }
    else setMsg('err', j.message);
  } catch (e) { setMsg('err', 'FritzFlow nicht erreichbar: ' + e.message); }
  $('#saveBtn').disabled = false;
});
$('#cancelBtn').addEventListener('click', () => dlg.close());
dlg.addEventListener('cancel', (e) => { if (!S.cfg?.passwordSet && !S.cfg?.readOnly) e.preventDefault(); });
$('#settingsBtn').addEventListener('click', openSetup);
})();
