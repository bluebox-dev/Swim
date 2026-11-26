/* SWIM Console — vanilla JS single-page app (no build step). */
(() => {
'use strict';

// ───────── utilities ─────────
const $ = (s, el = document) => el.querySelector(s);
// AGPL-3.0 section 13: everyone who uses this console over the network must be able to get
// the source of the version that is running. If you run a modified copy, point this at YOUR source.
const SOURCE_URL = 'https://github.com/bluebox-dev/Swim';
const sourceLink = () => `<a href="${SOURCE_URL}" target="_blank" rel="noopener noreferrer">ซอร์สโค้ด (AGPL v3)</a>`;
const esc = v => String(v ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const store = {
  get(k) { try { return sessionStorage.getItem(k) || ''; } catch (e) { return ''; } },
  set(k, v) { try { v ? sessionStorage.setItem(k, v) : sessionStorage.removeItem(k); } catch (e) {} },
};
const fmtBytes = b => {
  if (!b) return '0 B';
  const u = ['B', 'KB', 'MB', 'GB', 'TB']; let i = 0;
  while (b >= 1024 && i < u.length - 1) { b /= 1024; i++; }
  return (b >= 100 || i === 0 ? b.toFixed(0) : b.toFixed(1)) + ' ' + u[i];
};
// ───────── language: 'th' = Thai with English technical terms, 'en' = English only ─────────
const LANG = (() => { try { return localStorage.getItem('swim-lang') === 'en' ? 'en' : 'th'; } catch (e) { return 'th'; } })();
const LOC = () => LANG === 'en' ? 'en-GB' : 'th-TH';
const fmtDate = t => t ? new Date(t).toLocaleString(LOC(), { dateStyle: 'medium', timeStyle: 'short' }) : '—';
const ago = t => {
  if (!t) return '—';
  const s = (Date.now() - new Date(t).getTime()) / 1000;
  const en = LANG === 'en', n = x => Math.floor(x);
  if (s < 60) return en ? 'just now' : 'เมื่อสักครู่';
  if (s < 3600) return en ? `${n(s / 60)} min ago` : n(s / 60) + ' นาทีที่แล้ว';
  if (s < 86400) return en ? `${n(s / 3600)} h ago` : n(s / 3600) + ' ชั่วโมงที่แล้ว';
  if (s < 86400 * 30) return en ? `${n(s / 86400)} days ago` : n(s / 86400) + ' วันที่แล้ว';
  return fmtDate(t);
};
const uptime = s => LANG === 'en' ? (s < 3600 ? Math.max(1, Math.floor(s / 60)) + ' min' : s < 86400 ? Math.floor(s / 3600) + ' h' : Math.floor(s / 86400) + ' days')
  : s < 3600 ? Math.max(1, Math.floor(s / 60)) + ' นาที' : s < 86400 ? Math.floor(s / 3600) + ' ชั่วโมง' : Math.floor(s / 86400) + ' วัน';
const when = t => `<span title="${esc(fmtDate(t))}">${esc(ago(t))}</span>`;

// ───────── icons (stroke, 24px grid) ─────────
const P = {
  chevron: '<path d="m6 9 6 6 6-6"/>',
  win: '<path d="M3 5.5 10.5 4.4v7.1H3zM12 4.2 21 3v8.5h-9zM3 13h7.5v7.1L3 19zM12 13h9v8l-9-1.2z" fill="currentColor" stroke="none"/>',
  map: '<rect x="9" y="2.5" width="6" height="5" rx="1.2"/><rect x="2.5" y="16.5" width="6" height="5" rx="1.2"/><rect x="15.5" y="16.5" width="6" height="5" rx="1.2"/><path d="M12 7.5v4.5M5.5 16.5V12h13v4.5"/>',
  list: '<path d="M8 6h13M8 12h13M8 18h13M3.5 6h.01M3.5 12h.01M3.5 18h.01"/>',
  overview: '<rect x="3" y="3" width="7" height="9" rx="1.5"/><rect x="14" y="3" width="7" height="5" rx="1.5"/><rect x="14" y="12" width="7" height="9" rx="1.5"/><rect x="3" y="16" width="7" height="5" rx="1.5"/>',
  builder: '<rect x="2.5" y="4" width="19" height="13" rx="2"/><path d="M8 21h8M12 17v4"/>',
  images: '<path d="M12 3 2.5 8 12 13l9.5-5L12 3Z"/><path d="m2.5 13 9.5 5 9.5-5"/><path d="m2.5 17.5 9.5 5 9.5-5" opacity=".5"/>',
  hosts: '<rect x="4" y="5" width="16" height="11" rx="1.5"/><path d="M2 19h20"/>',
  copy: '<rect x="8.5" y="8.5" width="12" height="12" rx="2"/><path d="M15.5 8.5V5.5a2 2 0 0 0-2-2h-8a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h3"/>',
  phone: '<rect x="6.5" y="2.5" width="11" height="19" rx="2.5"/><path d="M11 18.5h2"/>',
  user: '<circle cx="12" cy="8" r="4"/><path d="M4.5 20.5c1.2-3.6 4-5.5 7.5-5.5s6.3 1.9 7.5 5.5"/>',
  shield: '<path d="M12 3 4.5 6v5.5c0 4.6 3.1 8.3 7.5 9.5 4.4-1.2 7.5-4.9 7.5-9.5V6L12 3Z"/>',
  audit: '<path d="M8 6h12M8 12h12M8 18h12"/><circle cx="4" cy="6" r="1"/><circle cx="4" cy="12" r="1"/><circle cx="4" cy="18" r="1"/>',
  play: '<path d="M7 4.5v15l12-7.5-12-7.5Z"/>',
  power: '<path d="M12 3v8"/><path d="M6.3 7.2a8 8 0 1 0 11.4 0"/>',
  stop: '<rect x="6" y="6" width="12" height="12" rx="1.5"/>',
  reset: '<path d="M3 12a9 9 0 1 0 3-6.7L3 8"/><path d="M3 3v5h5"/>',
  console: '<rect x="2.5" y="4" width="19" height="16" rx="2"/><path d="m7 10 3 2.5L7 15M12.5 15H17"/>',
  camera: '<path d="M4 8h3l2-3h6l2 3h3a1 1 0 0 1 1 1v9a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V9a1 1 0 0 1 1-1Z"/><circle cx="12" cy="13" r="3.5"/>',
  capture: '<path d="M12 3v12M7 10l5 5 5-5"/><path d="M4 17v2.5A1.5 1.5 0 0 0 5.5 21h13a1.5 1.5 0 0 0 1.5-1.5V17"/>',
  disc: '<circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="2.5"/>',
  undo: '<path d="M9 14 4 9l5-5"/><path d="M4 9h10.5a5.5 5.5 0 0 1 0 11H11"/>',
  trash: '<path d="M4 7h16M10 11v6M14 11v6M6 7l1 13h10l1-13M9 7V4h6v3"/>',
  log: '<path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8l-5-5Z"/><path d="M14 3v5h5M9 13h6M9 17h4"/>',
  logout: '<path d="M15 4h3a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-3M10 17l-5-5 5-5M5 12h11"/>',
  check: '<path d="m5 12.5 4.5 4.5L19 7.5"/>',
  x: '<path d="M6 6l12 12M18 6 6 18"/>',
  alert: '<path d="M12 3 2 20h20L12 3Z"/><path d="M12 10v4.5M12 17.5v.01"/>',
  info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v6M12 7.5v.01"/>',
  refresh: '<path d="M20 11a8 8 0 0 0-14.6-4.5L3 9"/><path d="M3 4v5h5"/><path d="M4 13a8 8 0 0 0 14.6 4.5L21 15"/><path d="M21 20v-5h-5"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  calendar: '<rect x="3.5" y="5" width="17" height="15.5" rx="2"/><path d="M3.5 10h17M8 3v4M16 3v4"/>',
  left: '<path d="m15 6-6 6 6 6"/>', right: '<path d="m9 6 6 6-6 6"/>',
  kebab: '<circle cx="12" cy="5.5" r="1.3"/><circle cx="12" cy="12" r="1.3"/><circle cx="12" cy="18.5" r="1.3"/>',
  filter: '<path d="M3.5 5.5h17M7 12h10M10.5 18.5h3"/>',
  db: '<ellipse cx="12" cy="5.5" rx="7.5" ry="2.5"/><path d="M4.5 5.5v13c0 1.4 3.4 2.5 7.5 2.5s7.5-1.1 7.5-2.5v-13"/><path d="M4.5 12c0 1.4 3.4 2.5 7.5 2.5s7.5-1.1 7.5-2.5"/>',
  cpu: '<rect x="6" y="6" width="12" height="12" rx="1.5"/><path d="M9 2v4M15 2v4M9 18v4M15 18v4M2 9h4M2 15h4M18 9h4M18 15h4"/>',
  hdd: '<rect x="3" y="12" width="18" height="8" rx="1.5"/><path d="M5.5 12 8 4h8l2.5 8M7 16h.01"/>',
  link: '<path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7L11.5 6.8"/><path d="M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1.5-1.5"/>',
  menu: '<path d="M4 7h16M4 12h16M4 17h16"/>',
  search: '<circle cx="11" cy="11" r="6.5"/><path d="m20 20-4-4"/>',
  key: '<circle cx="8" cy="15" r="4"/><path d="m11 12 9-9M17 6l3 3M15 8l2 2"/>',
  layers: '<path d="M12 3 2.5 8 12 13l9.5-5L12 3Z"/><path d="m2.5 13 9.5 5 9.5-5"/>',
  sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>',
  moon: '<path d="M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5Z"/>',
  clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
};
const ic = (n, extra = '') => `<svg class="i" viewBox="0 0 24 24" aria-hidden="true" ${extra}>${P[n] || ''}</svg>`;

// ───────── status vocabulary ─────────
const STATUS = {
  // VM
  'running': ['ok', 'กำลังทำงาน'], 'shut off': ['', 'ปิดอยู่'], 'paused': ['warn', 'หยุดชั่วคราว'], 'in shutdown': ['warn', 'กำลังปิด'],
  // images
  building: ['info', 'กำลังสร้าง'], testing: ['info', 'กำลังทดสอบ'], pending: ['warn', 'รออนุมัติ'], published: ['ok', 'ใช้งานอยู่'],
  superseded: ['', 'ถูกแทนที่'], withdrawn: ['bad', 'ถอนออก'], archived: ['', 'เก็บถาวร'], failed: ['bad', 'ล้มเหลว'],
  test_failed: ['bad', 'ทดสอบไม่ผ่าน'], rejected: ['bad', 'ปฏิเสธ'], deleted: ['', 'ลบแล้ว'],
  // tasks
  queued: ['', 'รอคิว'], done: ['ok', 'สำเร็จ'],
  // health
  ok: ['ok', 'ปกติ'], degraded: ['warn', 'มีปัญหาบางส่วน'], down: ['bad', 'ใช้งานไม่ได้'], warn: ['warn', 'ใกล้เต็ม'], block: ['bad', 'เต็ม'],
};
const badge = (s, label) => {
  const [cls, th] = STATUS[s] || ['', s];
  const pulse = s === 'running' || s === 'building' ? ' pulse' : '';
  return `<span class="badge ${cls}${pulse}">${esc(label || th)}</span>`;
};
const TASK_KIND = { iso_inspect: 'อ่านรุ่น Windows ในแผ่น ISO', iso_import: 'นำเข้าแผ่น ISO เป็น image', capture_image: 'Capture เป็น WIM', vm_snapshot: 'สร้าง Snapshot', vm_revert: 'ย้อนกลับ Snapshot', vm_snapshot_delete: 'ลบ Snapshot' };
const taskBadge = s => s === 'running' ? '<span class="badge info pulse">กำลังทำ</span>' : badge(s);

// ───────── API ─────────
let token = store.get('swim-token');
let me = null;
const authHeaders = () => token ? { Authorization: 'Bearer ' + token, 'X-SWIM': '1' } : { 'X-SWIM': '1' };
class ApiError extends Error { constructor(msg, status) { super(msg); this.status = status; } }
async function api(path, method = 'GET', body, extraHeaders = {}) {
  const headers = { ...authHeaders(), ...extraHeaders };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  const r = await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  if (r.status === 401 && path !== '/v1/whoami') { logout(true); throw new ApiError('เซสชันหมดอายุ กรุณาเข้าสู่ระบบใหม่', 401); }
  const ct = r.headers.get('content-type') || '';
  const data = ct.includes('json') ? await r.json().catch(() => null) : await r.text();
  if (!r.ok) throw new ApiError((data && data.error && data.error.message) || ('HTTP ' + r.status), r.status);
  return data;
}

// ───────── toast / modal / drawer ─────────
function toast(msg, kind = 'ok') {
  const el = document.createElement('div');
  el.className = 'toast' + (kind === 'bad' ? ' bad' : '');
  el.innerHTML = ic(kind === 'bad' ? 'alert' : 'check') + `<div>${esc(msg)}</div>`;
  $('#toasts').appendChild(el);
  setTimeout(() => el.remove(), kind === 'bad' ? 6000 : 3500);
}

/** modal({title, desc, body, confirm, danger, onConfirm(formData) → Promise}) */
function modal({ title, desc = '', body = '', confirm = 'ยืนยัน', danger = false, wide = false, onConfirm }) {
  const scrim = document.createElement('div');
  scrim.className = 'scrim';
  scrim.innerHTML = `<form class="modal${wide ? ' wide' : ''}" role="dialog" aria-modal="true" aria-label="${esc(title)}">
    <div class="modal-head"><h3>${esc(title)}</h3>${desc ? `<p>${desc}</p>` : ''}</div>
    ${body ? `<div class="modal-body">${body}</div>` : '<div style="height:8px"></div>'}
    <div class="modal-foot"><button type="button" class="btn" data-close>ยกเลิก</button>
      <button class="btn ${danger ? 'danger solid' : 'primary'}" type="submit">${esc(confirm)}</button></div></form>`;
  const close = () => { scrim.remove(); document.removeEventListener('keydown', onKey); };
  const onKey = e => { if (e.key === 'Escape') close(); };
  document.addEventListener('keydown', onKey);
  scrim.addEventListener('mousedown', e => { if (e.target === scrim) close(); });
  scrim.querySelector('[data-close]').onclick = close;
  scrim.querySelector('form').onsubmit = async e => {
    e.preventDefault();
    const btn = e.submitter || scrim.querySelector('[type=submit]');
    btn.disabled = true;
    try { await onConfirm(Object.fromEntries(new FormData(e.target))); close(); }
    catch (err) { toast(err.message, 'bad'); btn.disabled = false; }
  };
  document.body.appendChild(scrim);
  (scrim.querySelector('input,select,textarea') || scrim.querySelector('[type=submit]')).focus();
}

let drawerTimer = null;
function openLog(task) {
  closeDrawer();
  const scrim = document.createElement('div'); scrim.className = 'scrim'; scrim.style.background = 'rgba(9,14,22,.3)';
  const d = document.createElement('aside'); d.className = 'drawer';
  d.innerHTML = `<div class="drawer-head"><div class="dot-icon accent">${ic('log')}</div><div style="flex:1;min-width:0">
      <h3>${esc(TASK_KIND[task.kind] || task.kind)} · <span class="mono">${esc(task.payload.vm || '')}</span></h3>
      <div class="mute mono" style="font-size:11.5px">${esc(task.id)}</div></div><span id="drawer-status">${taskBadge(task.status)}</span>
      <button class="btn icon ghost" data-close aria-label="ปิด">${ic('x')}</button></div><pre id="drawer-log">กำลังโหลด…</pre>`;
  scrim.onclick = closeDrawer; d.querySelector('[data-close]').onclick = closeDrawer;
  document.body.append(scrim, d);
  const load = async () => {
    try {
      const txt = await api('/v1/tasks/' + encodeURIComponent(task.id) + '/log');
      const pre = $('#drawer-log'); if (!pre) return;
      const atBottom = pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 20;
      pre.textContent = txt || '(ยังไม่มี log)';
      if (atBottom) pre.scrollTop = pre.scrollHeight;
      const t = (await api('/v1/tasks')).find(x => x.id === task.id);
      if (t && $('#drawer-status')) $('#drawer-status').innerHTML = taskBadge(t.status);
      if (t && (t.status === 'done' || t.status === 'failed')) clearInterval(drawerTimer);
    } catch (e) { const pre = $('#drawer-log'); if (pre) pre.textContent = e.status === 404 ? '(ยังไม่มี log)' : e.message; }
  };
  load(); drawerTimer = setInterval(load, 3000);
  document.addEventListener('keydown', escDrawer);
}
function escDrawer(e) { if (e.key === 'Escape') closeDrawer(); }
function closeDrawer() {
  clearInterval(drawerTimer); if (typeof batchTimer !== 'undefined') clearInterval(batchTimer);
  document.querySelectorAll('.drawer').forEach(d => { d.previousElementSibling?.remove(); d.remove(); });
  document.removeEventListener('keydown', escDrawer);
}

// ───────── theme ─────────
const theme = {
  get() { return store.get('swim-theme') || ''; },
  apply() { const t = this.get(); t ? document.documentElement.setAttribute('data-theme', t) : document.documentElement.removeAttribute('data-theme'); },
  toggle() {
    const dark = this.get() ? this.get() === 'dark' : matchMedia('(prefers-color-scheme: dark)').matches;
    store.set('swim-theme', dark ? 'light' : 'dark'); this.apply(); renderShell();
  },
  isDark() { const t = this.get(); return t ? t === 'dark' : matchMedia('(prefers-color-scheme: dark)').matches; },
};
theme.apply();

// ───────── login ─────────
// SWIM mark: three staged image layers, the top one carrying a wave (swim). Original artwork.
const SWIM_MARK = `<path class="m-l3" d="M7 31.5 24 40l17-8.5"/><path class="m-l2" d="M7 24.5 24 33l17-8.5"/>
  <path class="m-top" d="M24 8.5 41 17 24 25.5 7 17Z"/><path class="m-wave" d="M15.5 17.6c2.8-2.2 5.7-2.2 8.5 0s5.7 2.2 8.5 0"/>`;
const swimLogo = (cls = '') => `<svg class="swim-logo ${cls}" viewBox="0 0 48 48" aria-hidden="true">${SWIM_MARK}</svg>`;
// ───────── sign-in: username + password, then a 6-digit code from Google Authenticator ─────────
// /v1/auth/config says what the server supports; an older server (404) keeps the admin-token sign-in.
let authCfg = null;
async function loadAuthCfg() { try { const r = await fetch('/v1/auth/config', { cache: 'no-store' }); authCfg = r.ok ? await r.json() : null; } catch (e) { authCfg = null; } }
const accountsOn = () => !!authCfg?.users_exist;
async function authPost(path, body) {
  const r = await fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-SWIM': '1' }, body: JSON.stringify(body) });
  const d = await r.json().catch(() => null);
  if (!r.ok) { const e = new ApiError(d?.error?.message || ('HTTP ' + r.status), r.status); e.retry = d?.error?.retry_after || d?.retry_after; throw e; }
  return d;
}
function loginShell(card, opts = {}) {
  $('#app').innerHTML = `<div class="login2">
    <section class="l2-art" aria-hidden="true">
      <div class="l2-center">${swimLogo('xl')}<h2>SWIM</h2><p>Staged Windows Image Manager</p>
        <p class="l2-lead">สร้าง image ล้างเครื่อง และดูแลเครื่อง Windows ทั้งห้องแล็บจากที่เดียว</p>
        <p class="l2-host">${esc(location.host)}</p></div>
    </section>
    <section class="l2-form">
      <div class="l2-tools"><button type="button" class="l2-help l2-lang" id="l2-lang" translate="no" aria-label="${LANG === 'en' ? 'เปลี่ยนเป็นภาษาไทย' : 'Switch to English'}">${LANG === 'en' ? 'ไทย' : 'EN'}</button>
        <button type="button" class="l2-help" id="l2-help" aria-label="วิธีเข้าสู่ระบบ" title="วิธีเข้าสู่ระบบ">?</button></div>
      <div class="l2-card${opts.wide ? ' wide' : ''}">
        <div class="l2-brand">${swimLogo('lg')}<b>SWIM</b><small>${esc(opts.sub || 'Console')}</small></div>
        ${card}
        <p class="l2-hint" id="l2-hint" hidden>${accountsOn() ? 'ลืมรหัสผ่านหรือทำโทรศัพท์หาย: ให้ผู้ดูแลอีกคนกด "รีเซ็ตการยืนยันตัวตน" ในหน้า ตั้งค่า → ผู้ดูแลระบบ แล้วส่งลิงก์ใหม่ให้'
          : 'token อยู่ในไฟล์ <code class="k">builder-infra/.env</code> บน server ขอจากผู้ดูแลระบบ อย่าส่งต่อทางแชต'}</p>
      </div>
      <footer class="l2-foot">SWIM, ห้องปฏิบัติการคอมพิวเตอร์<br>${sourceLink()}</footer>
    </section></div>`;
  $('#l2-lang').onclick = () => setLang(LANG === 'en' ? 'th' : 'en');
  $('#l2-help').onclick = () => { const hnt = $('#l2-hint'); hnt.hidden = !hnt.hidden; };
}
const loginErr = msg => msg ? `<div class="callout bad" role="alert">${ic('alert')}<div>${esc(msg)}</div></div>` : '';
function renderLogin(msg = '', mode = '') {
  const useToken = mode === 'token' || !accountsOn();
  if (useToken) return renderTokenLogin(msg);
  loginShell(`<form id="login" class="l2-stack">${loginErr(msg)}
      <label class="l2-field"><span>ชื่อผู้ใช้</span><input id="lu" autocomplete="username" required spellcheck="false" autocapitalize="off"></label>
      <label class="l2-field"><span>รหัสผ่าน</span><input id="lp" type="password" autocomplete="current-password" required></label>
      <button class="btn primary l2-go" type="submit">ถัดไป</button>
      ${authCfg?.token_login ? '<button type="button" class="l2-link" id="l2-tok">ใช้ Admin token แทน</button>' : ''}</form>`);
  $('#lu').focus();
  $('#l2-tok')?.addEventListener('click', () => renderLogin('', 'token'));
  $('#login').onsubmit = async e => {
    e.preventDefault(); const btn = e.submitter || e.target.querySelector('[type=submit]'); btn.disabled = true;
    const username = $('#lu').value.trim();
    try {
      const r = await authPost('/v1/auth/login', { username, password: $('#lp').value });
      if (r.next === 'setup') return renderSetup({ challenge: r.challenge, username });
      renderOtp(r.challenge, username);
    } catch (err) { renderLogin(err.status === 429 ? `ใส่ผิดหลายครั้ง ลองใหม่ได้ในอีก ${Math.ceil((err.retry || 900) / 60)} นาที` : err.status === 401 ? 'ชื่อผู้ใช้หรือรหัสผ่านไม่ถูกต้อง' : err.message); }
  };
}
function renderTokenLogin(msg = '') {
  loginShell(`<form id="login" class="l2-stack">${loginErr(msg)}
      ${authCfg && !authCfg.users_exist ? `<div class="callout">${ic('info')}<div>ยังไม่มีบัญชีผู้ดูแล เข้าด้วย Admin token ครั้งนี้ แล้วสร้างบัญชีของคุณที่ ตั้งค่า → ผู้ดูแลระบบ</div></div>` : ''}
      <label class="l2-field"><span>Admin token</span><input id="tok" type="password" autocomplete="current-password" required spellcheck="false"></label>
      <button class="btn primary l2-go" type="submit">เข้าสู่ระบบ</button>
      ${accountsOn() ? '<button type="button" class="l2-link" id="l2-acc">กลับไปใช้ชื่อผู้ใช้และรหัสผ่าน</button>' : ''}</form>`);
  $('#tok').focus();
  $('#l2-acc')?.addEventListener('click', () => renderLogin());
  $('#login').onsubmit = async e => {
    e.preventDefault();
    token = $('#tok').value.trim();
    try { me = await api('/v1/whoami'); store.set('swim-token', token); boot(); }
    catch (err) { token = ''; renderTokenLogin(err.status === 401 ? 'Token ไม่ถูกต้อง' : err.message); }
  };
}
// one field with autocomplete="one-time-code": phones and password managers fill it, and it submits itself at 6 digits
function renderOtp(challenge, username, msg = '', recovery = false) {
  loginShell(`<form id="otp" class="l2-stack">${loginErr(msg)}
      <div class="otp-head">${ic(recovery ? 'shield' : 'phone')}<div><b>${recovery ? 'ใช้รหัสสำรอง' : 'ใส่รหัสจาก Google Authenticator'}</b>
        <small>${recovery ? 'รหัสสำรองแต่ละตัวใช้ได้ครั้งเดียว' : `เปิดแอปแล้วดูรหัส 6 หลักของ SWIM (${esc(username)})`}</small></div></div>
      ${recovery ? `<label class="l2-field"><span>รหัสสำรอง</span><input id="oc" autocomplete="off" required spellcheck="false" placeholder="xxxx-xxxx"></label>`
        : `<input id="oc" class="otp-in" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9]{6}" maxlength="6" required aria-label="รหัส 6 หลัก" placeholder="000000">`}
      <button class="btn primary l2-go" type="submit">เข้าสู่ระบบ</button>
      <div class="l2-links"><button type="button" class="l2-link" id="o-back">${ic('left')}กลับ</button>
        <button type="button" class="l2-link" id="o-alt">${recovery ? 'ใช้รหัสจากแอปแทน' : 'ใช้รหัสสำรอง'}</button></div></form>`, { sub: 'ยืนยันตัวตน' });
  const oc = $('#oc'); oc.focus();
  $('#o-back').onclick = () => renderLogin();
  $('#o-alt').onclick = () => renderOtp(challenge, username, '', !recovery);
  if (!recovery) oc.oninput = () => { oc.value = oc.value.replace(/\D/g, '').slice(0, 6); if (oc.value.length === 6) $('#otp').requestSubmit(); };
  $('#otp').onsubmit = async e => {
    e.preventDefault(); oc.disabled = true;
    try {
      const r = await authPost('/v1/auth/totp', recovery ? { challenge, recovery_code: oc.value.trim() } : { challenge, code: oc.value });
      token = ''; store.set('swim-token', ''); me = r.user || null;
      if (recovery) toast(`เหลือรหัสสำรอง ${r.recovery_left ?? '?'} ตัว สร้างชุดใหม่ได้ที่เมนูบัญชี`, r.recovery_left <= 2 ? 'bad' : 'ok');
      boot();
    } catch (err) {
      if (err.status === 410 || /challenge|expired|หมดอายุ/i.test(err.message)) return renderLogin('หมดเวลา กรุณาเข้าสู่ระบบอีกครั้ง');
      renderOtp(challenge, username, err.status === 429 ? `ใส่ผิดหลายครั้ง ลองใหม่ได้ในอีก ${Math.ceil((err.retry || 900) / 60)} นาที` : err.status === 401 ? 'รหัสไม่ถูกต้อง ตรวจว่าเวลาบนโทรศัพท์ตรง แล้วลองรหัสใหม่' : err.message, recovery);
    }
  };
}
// first-time setup (after the first sign-in, or from a one-time setup link): password → scan QR → confirm a code → save recovery codes
async function renderSetup({ challenge, setupToken, username }, msg = '') {
  const id = setupToken ? { setup_token: setupToken } : { challenge };
  let st;
  try { st = await authPost('/v1/auth/setup/start', id); }
  catch (err) { return loginShell(`<div class="l2-stack">${loginErr(err.status === 404 || err.status === 410 ? 'ลิงก์ตั้งค่านี้หมดอายุหรือถูกใช้ไปแล้ว ขอลิงก์ใหม่จากผู้ดูแลระบบ' : err.message)}
      <button class="btn primary l2-go" id="s-home">ไปหน้าเข้าสู่ระบบ</button></div>`, { sub: 'ตั้งค่าบัญชี' }), $('#s-home').onclick = () => { location.hash = ''; renderLogin(); }; }
  const secret = String(st.secret || '').replace(/(.{4})/g, '$1 ').trim();
  loginShell(`<form id="setup" class="l2-stack">${loginErr(msg)}
      <ol class="setup-steps">
        ${setupToken || st.password_required ? `<li><b>ตั้งรหัสผ่าน</b><small>อย่างน้อย 10 ตัวอักษร ใช้วลียาว ๆ จำง่ายดีกว่าคำสั้นที่ซับซ้อน</small>
          <label class="l2-field"><span>ชื่อผู้ใช้</span><input value="${esc(st.username || username || '')}" autocomplete="username" readonly></label>
          <label class="l2-field"><span>รหัสผ่านใหม่</span><input id="sp1" type="password" autocomplete="new-password" minlength="10" required></label>
          <label class="l2-field"><span>ยืนยันรหัสผ่าน</span><input id="sp2" type="password" autocomplete="new-password" minlength="10" required></label></li>` : ''}
        <li><b>สแกน QR ด้วย Google Authenticator</b><small>เปิดแอป กด + แล้วเลือก "Scan a QR code" (ใช้ Microsoft Authenticator หรือแอป TOTP อื่นก็ได้)</small>
          <div class="qr-box"><div class="qr" translate="no">${st.qr_svg || ''}</div>
            <div class="qr-alt"><small>สแกนไม่ได้: ใส่รหัสนี้ในแอปเอง</small><code translate="no">${esc(secret)}</code>
              <button type="button" class="btn sm" id="s-copy">${ic('copy')}คัดลอก</button></div></div></li>
        <li><b>ใส่รหัส 6 หลักที่แอปแสดง</b><small>เพื่อยืนยันว่าตั้งในแอปถูกต้อง</small>
          <input id="sc" class="otp-in" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9]{6}" maxlength="6" required aria-label="รหัส 6 หลัก" placeholder="000000"></li>
      </ol>
      <button class="btn primary l2-go" type="submit">ยืนยันและตั้งค่าเสร็จ</button></form>`, { sub: 'ตั้งค่าบัญชี', wide: true });
  ($('#sp1') || $('#sc')).focus();
  $('#s-copy').onclick = async () => { try { await navigator.clipboard.writeText(st.secret); toast('คัดลอกรหัสแล้ว'); } catch (e) { toast('คัดลอกไม่ได้ เลือกข้อความแล้วกด Ctrl+C', 'bad'); } };
  const sc = $('#sc'); sc.oninput = () => { sc.value = sc.value.replace(/\D/g, '').slice(0, 6); };
  $('#setup').onsubmit = async e => {
    e.preventDefault();
    const body = { ...id, code: sc.value };
    if ($('#sp1')) { if ($('#sp1').value !== $('#sp2').value) return toast('รหัสผ่านสองช่องไม่ตรงกัน', 'bad'); body.password = $('#sp1').value; }
    const sb = e.submitter || e.target.querySelector('[type=submit]'); sb.disabled = true;
    try { const r = await authPost('/v1/auth/setup/finish', body); token = ''; store.set('swim-token', ''); history.replaceState(null, '', location.pathname + '#/home'); renderRecovery(r.recovery_codes || []); }
    catch (err) { sb.disabled = false; toast(err.status === 401 ? 'รหัสไม่ถูกต้อง ตรวจว่าเวลาบนโทรศัพท์ตรง แล้วลองรหัสใหม่' : err.message, 'bad'); sc.value = ''; sc.focus(); }
  };
}
function recoveryBlock(codes) {
  return `<div class="rc-box"><ol class="rc-list" translate="no">${codes.map(c => `<li><code>${esc(c)}</code></li>`).join('')}</ol>
    <div class="rc-actions"><button type="button" class="btn sm" id="rc-copy">${ic('copy')}คัดลอก</button><button type="button" class="btn sm" id="rc-dl">${ic('capture')}ดาวน์โหลด .txt</button></div></div>`;
}
function wireRecovery(codes) {
  const text = `SWIM recovery codes (${location.host}, ${me?.username || ''})\n${new Date().toLocaleString()}\n\n${codes.join('\n')}\n\nEach code works once.`;
  $('#rc-copy').onclick = async () => { try { await navigator.clipboard.writeText(text); toast('คัดลอกรหัสสำรองแล้ว'); } catch (e) { toast('คัดลอกไม่ได้', 'bad'); } };
  $('#rc-dl').onclick = () => { const a = document.createElement('a'); a.href = URL.createObjectURL(new Blob([text], { type: 'text/plain' })); a.download = 'swim-recovery-codes.txt'; document.body.append(a); a.click(); a.remove(); };
}
function renderRecovery(codes) {
  loginShell(`<div class="l2-stack"><div class="otp-head">${ic('shield')}<div><b>เก็บรหัสสำรอง 10 ตัวนี้ไว้</b>
      <small>ใช้เข้าระบบแทนรหัสจากแอปเมื่อโทรศัพท์หาย แต่ละตัวใช้ได้ครั้งเดียว หน้านี้แสดงครั้งเดียว</small></div></div>
    ${recoveryBlock(codes)}
    <label class="check"><input type="checkbox" id="rc-ok"> ฉันเก็บรหัสสำรองไว้ในที่ปลอดภัยแล้ว</label>
    <button class="btn primary l2-go" id="rc-go" disabled>เข้าสู่ SWIM</button></div>`, { sub: 'ตั้งค่าบัญชี', wide: true });
  wireRecovery(codes);
  $('#rc-ok').onchange = () => { $('#rc-go').disabled = !$('#rc-ok').checked; };
  $('#rc-go').onclick = () => boot();
}
function logout(expired) {
  if (me?.auth === 'session' && !expired) fetch('/v1/auth/logout', { method: 'POST', headers: { 'X-SWIM': '1' } }).catch(() => {});
  token = ''; me = null; store.set('swim-token', ''); stopPolling(); stopLive(); closeDrawer();
  renderLogin(expired ? 'เซสชันหมดอายุ กรุณาเข้าสู่ระบบใหม่' : '');
  loadAuthCfg().then(() => { if (!me && !$('#otp, #setup')) renderLogin(expired ? 'เซสชันหมดอายุ กรุณาเข้าสู่ระบบใหม่' : ''); });
}

// ───────── shell + routing ─────────
const ROUTES = {
  home: { title: 'หน้าแรก', sub: 'สิ่งที่ต้องทำตอนนี้ และภาพรวมของระบบ', icon: 'overview', render: pageHome },
  images: { title: 'Images', sub: 'สร้าง Windows image เวอร์ชันใหม่จากเครื่องต้นแบบ และดูทุกเวอร์ชัน', icon: 'images', render: pageImages },
  hosts: { title: 'เครื่อง', sub: 'เครื่อง Windows ทั้งหมด — คลิกเครื่องเพื่อดูรายละเอียดหรือติดตั้ง Windows ใหม่', icon: 'hosts', render: () => routeArg() ? pageHost(routeArg()) : viewPref.get() === 'map' ? pageMap() : pageHosts() },
  enroll: { title: 'เพิ่มเครื่อง', sub: 'ติดตั้ง SWIM Agent บนเครื่อง Windows', icon: 'plus', nav: 'hosts', render: pageEnroll },
  schedule: { title: 'ตารางงาน', sub: 'งานที่ตั้งเวลาไว้ล่วงหน้า เช่น ล้างเครื่องทั้งห้องตอนกลางคืน', icon: 'calendar', render: pageSchedule },
  logs: { title: 'ประวัติการใช้งาน', sub: 'ใครทำอะไร กับเครื่องไหน เมื่อไหร่ — ทุกคำสั่งเก็บสคริปต์และผลลัพธ์ไว้ครบ', icon: 'audit', render: pageLogs },
  storage: { title: 'พื้นที่ server', sub: 'อะไรใช้ดิสก์ของ server เท่าไร และล้างไฟล์ที่ไม่ใช้แล้ว', icon: 'hdd', render: () => pageStorage() },
  settings: { title: 'ตั้งค่า', sub: 'ห้องและกลุ่มของเครื่อง', icon: 'layers', render: pageSettings },
};
// old links keep working
const ROUTE_ALIAS = { overview: 'home', builder: 'images', scripts: 'settings', safety: 'settings', audit: 'logs' };
let health = null, pendingImages = 0;
const routePath = () => (location.hash.replace(/^#\/?/, '') || 'home').split('?')[0];
const route = () => routePath().split('/')[0];
let siteCache = [];
const routeArg = () => decodeURIComponent(routePath().split('/').slice(1).join('/'));

function renderShell() {
  const r = route();
  const nav = (key, extra = '') => `<a href="#/${key}" class="${r === key || ROUTES[r]?.nav === key ? 'active' : ''}">${ic(ROUTES[key].icon)}<span>${ROUTES[key].title}</span>${extra}</a>`;
  const hs = health ? health.status : '';
  $('#app').innerHTML = `<div class="shell">
    <aside class="side" id="side">
      <a class="brand" href="#/home" aria-label="SWIM หน้าแรก">${swimLogo('side')}<div><b>SWIM</b><small>Image Manager</small></div></a>
      <nav class="nav">
        <div class="nav-label">ภาพรวม</div>${nav('home')}${nav('hosts')}${nav('schedule')}
        <div class="nav-label">Windows</div>${nav('images', pendingImages ? `<span class="count warn" title="รออนุมัติ">${pendingImages}</span>` : '')}
        <div class="nav-label">ระบบ</div>${nav('logs')}${nav('storage')}${nav('settings')}
      </nav>
      <div class="side-foot">
        <div class="row">${health ? badge(hs, hs === 'ok' ? 'ระบบปกติ' : STATUS[hs]?.[1]) : '<span class="badge">กำลังตรวจ…</span>'}</div>
        <dl class="versions"><div><dt>Server</dt><dd>${esc(health?.version || '—')}</dd></div><div><dt>Agent ล่าสุด</dt><dd id="ver-agent">${esc(latestAgent || '—')}</dd></div>
          <div><dt>ที่อยู่</dt><dd>${esc(location.hostname)}</dd></div></dl>
      </div>
    </aside>
    <div class="main r-${esc(r)}">
      <header class="top">
        <button class="btn icon ghost menu-btn" id="menu" aria-label="เมนู">${ic('menu')}</button>
        <div style="min-width:0"><h1>${esc(ROUTES[r]?.title || '')}</h1><p class="hide-sm">${esc(ROUTES[r]?.sub || '')}</p></div>
        <div class="spacer"></div>
        <form class="gsearch" id="gsearch" role="search">${ic('search')}<input class="input" id="gq" placeholder="ค้นหาเครื่อง ชื่อ / IP / ผู้ใช้" autocomplete="off"><kbd>/</kbd></form>
        ${liveBadge()}
        <button class="btn icon ghost" id="refresh" title="รีเฟรช" aria-label="รีเฟรช">${ic('refresh')}</button>
        <button class="btn icon ghost" id="help" title="วิธีใช้" aria-label="วิธีใช้"><span class="help-q">?</span></button>
        <button class="btn ghost lang-btn" id="lang" title="${LANG === 'en' ? 'ภาษาไทย' : 'English'}" aria-label="${LANG === 'en' ? 'เปลี่ยนเป็นภาษาไทย' : 'Switch to English'}" translate="no">${LANG === 'en' ? 'ไทย' : 'EN'}</button>
        <button class="btn icon ghost" id="theme" title="สลับธีม" aria-label="สลับธีม">${ic(theme.isDark() ? 'sun' : 'moon')}</button>
        <button class="avatar" id="logout" title="${esc(me?.name || '')} — ออกจากระบบ" aria-label="บัญชี ${esc(me?.name || '')}: ออกจากระบบ">${esc((me?.name || '?').slice(0, 1).toUpperCase())}</button>
      </header>
      <main class="content" id="view"></main>
    </div></div>`;
  $('#menu').onclick = () => $('#side').classList.toggle('open');
  $('#gsearch').onsubmit = e => { e.preventDefault(); hostFilter = $('#gq').value.trim(); viewPref.set('table'); location.hash = '#/hosts'; if (route() === 'hosts') renderPage(); };
  $('#logout').onclick = () => accountMenu($('#logout'));
  $('#help').onclick = () => modal({ title: 'วิธีใช้ SWIM', confirm: 'เข้าใจแล้ว', desc: 'ทางลัดและขั้นตอนหลัก',
    body: `<ul class="help-list"><li><kbd>/</kbd> ค้นหาเครื่องจากทุกหน้า</li><li><b>เพิ่มเครื่อง</b> ดาวน์โหลดตัวติดตั้ง แล้วดับเบิลคลิกบนเครื่อง Windows</li>
      <li><b>สร้าง image</b> เตรียมเครื่องต้นแบบ ปิดเครื่อง แล้วกด Capture จากนั้นอนุมัติ</li><li><b>ล้างเครื่อง</b> เปิดหน้าเครื่อง แล้วกด "ล้างเครื่องและติดตั้ง"</li></ul>
      <p class="help-src">SWIM เป็นซอฟต์แวร์เสรี เผยแพร่ภายใต้ GNU AGPL v3 ${sourceLink()}</p>`, onConfirm: async () => {} });
  $('#theme').onclick = () => theme.toggle();
  $('#lang').onclick = () => setLang(LANG === 'en' ? 'th' : 'en');
  $('#refresh').onclick = () => { refreshMeta(); renderPage(); };
  $('#side').addEventListener('click', e => { if (e.target.closest('a')) $('#side').classList.remove('open'); });
  renderPage(true);
}

let pageSeq = 0;
async function renderPage(initial = false) {
  const r = route();
  const def = ROUTES[r];
  if (!def) { location.hash = '#/' + (ROUTE_ALIAS[r] || 'home'); return; }
  const seq = ++pageSeq;
  const view = $('#view');
  if (initial) view.innerHTML = skeleton();
  try {
    const html = await def.render();
    if (seq !== pageSeq || route() !== r) return; // navigated away
    const scroll = window.scrollY;
    view.innerHTML = html;
    if (!initial) window.scrollTo(0, scroll);
    def.after && def.after();
  } catch (e) {
    if (e.status === 401) return;
    view.innerHTML = `<div class="card"><div class="empty">${ic('alert')}<b>โหลดข้อมูลไม่สำเร็จ</b>${esc(e.message)}</div></div>`;
  }
}
const skeleton = () => `<div class="grid kpis">${'<div class="card kpi"><div class="skel" style="width:40%"></div><div class="skel" style="height:26px;width:60%;margin-top:8px"></div></div>'.repeat(4)}</div>
  <div class="card"><div class="card-body">${'<div class="skel" style="margin:10px 0"></div>'.repeat(5)}</div></div>`;

// vendor + model without BIOS placeholders ("None", "System Product Name", "To be filled by O.E.M." …)
const HW_JUNK = /^(none|null|unknown|n\/?a|not applicable|default string|system (product name|manufacturer|version)|to be filled by o\.?e\.?m\.?|o\.?e\.?m\.?|-+)$/i;
const hwModel = h => [h.vendor, h.model].map(x => String(x || '').trim()).filter(x => x && !HW_JUNK.test(x)).join(' ');
async function refreshMeta() {
  try { health = await api('/v1/health'); } catch (e) { health = { status: 'down', version: '?', checks: {} }; }
  try { pendingImages = (await api('/v1/images')).filter(i => i.status === 'pending').length; } catch (e) {}
  try { latestAgent = (await api('/v1/enroll/info')).agent?.version || latestAgent; } catch (e) {}
  const va = $('#ver-agent'); if (va) va.textContent = latestAgent || '—';
  // update shell bits without re-rendering the page
  const foot = $('.side-foot .row');
  if (foot) foot.innerHTML = badge(health.status, health.status === 'ok' ? 'ระบบปกติ' : STATUS[health.status]?.[1]);
}

// polling: overview every 15 s, builder every 5 s while a task runs
let pollTimer = null, lastVmSig = '';
function schedulePoll() {
  stopPolling();
  const r = route();
  const every = r === 'images' ? 4000 : r === 'hosts' ? (live.connected ? 60000 : 10000) : r === 'home' ? 15000 : 30000;
  pollTimer = setTimeout(async () => {
    if (r === 'images' && !document.hidden && !document.querySelector('.scrim')) {
      try {
        const vms = await api('/v1/builder/vms'), sig = JSON.stringify(vms.map(x => [x.name, x.state, x.snapshots.length, vmStateLabel(x)[0], (vmStateLabel(x)[2] || 0) > 90000]));
        if (sig !== lastVmSig || builderBusy) { lastVmSig = sig; imagesQuick = false; await renderPage(); }
      } catch (e) {}
      return schedulePoll();
    }
    if (!document.hidden && !document.querySelector('.scrim') && !document.querySelector('.map.dragging')) {
      await refreshMeta();
      if (r === 'home' || r === 'images' || r === 'hosts') await renderPage();
    }
    schedulePoll();
  }, every);
}
function stopPolling() { clearTimeout(pollTimer); }

// ───────── common pieces ─────────
const card = (title, sub, body, actions = '', flush = false) => `<section class="card">
  <div class="card-head"><div><h2>${title}</h2>${sub ? `<p>${sub}</p>` : ''}</div><div class="spacer"></div>${actions}</div>
  <div class="card-body ${flush ? 'flush' : ''}">${body}</div></section>`;
const empty = (icon, title, text = '') => `<div class="empty">${ic(icon)}<b>${esc(title)}</b>${text}</div>`;
function table(rows, cols, emptyHtml) {
  if (!rows.length) return emptyHtml || empty('info', 'ยังไม่มีข้อมูล');
  return `<div class="table-wrap"><table class="t"><thead><tr>${cols.map(c => `<th${c.cls ? ` class="${c.cls}"` : ''}>${esc(c.h)}</th>`).join('')}</tr></thead>
    <tbody>${rows.map(r => `<tr>${cols.map(c => `<td${c.cls ? ` class="${c.cls}"` : ''}>${c.v(r)}</td>`).join('')}</tr>`).join('')}</tbody></table></div>`;
}
const kpi = (icon, label, value, sub = '', extra = '') => `<div class="card kpi"><div class="label">${ic(icon)}${esc(label)}</div>
  <div class="value">${value}</div>${sub ? `<div class="sub">${sub}</div>` : ''}${extra}</div>`;
const meter = (pct, level) => `<div class="meter ${level === 'warn' ? 'warn' : level === 'block' ? 'bad' : ''}"><i style="width:${Math.min(100, pct).toFixed(1)}%"></i></div>`;

// ───────── pages ─────────
let lastTasks = [];

// ───────── generic side panel (details behind a Home number, etc.) ─────────
function openPanel({ icon = 'info', title, sub = '', body }) {
  closeDrawer();
  const scrim = document.createElement('div'); scrim.className = 'scrim'; scrim.style.background = 'rgba(9,14,22,.3)';
  const d = document.createElement('aside'); d.className = 'drawer';
  d.innerHTML = `<div class="drawer-head"><div class="dot-icon">${ic(icon)}</div><div style="flex:1;min-width:0"><h3>${esc(title)}</h3>
    ${sub ? `<div class="mute" style="font-size:12.5px">${sub}</div>` : ''}</div><button class="btn icon ghost" data-close aria-label="ปิด">${ic('x')}</button></div>
    <div class="panel-body">${body}</div>`;
  scrim.onclick = closeDrawer; d.querySelector('[data-close]').onclick = closeDrawer;
  d.addEventListener('click', e => { if (e.target.closest('a[href^="#/"]')) closeDrawer(); });
  document.body.append(scrim, d); document.addEventListener('keydown', escDrawer);
}
const hostLink = h => `<a href="#/hosts/${encodeURIComponent(h.id)}"><b>${esc(h.hostname || h.id)}</b></a>`;
const siteName = id => siteCache.find(s => s.id === id)?.name || 'ไม่ระบุ site';
function openStat(key) {
  const live = hostList.filter(h => !h.retired_at);
  if (key === 'online') {
    const rows = [...live].sort((a, b) => (b.online - a.online) || (a.hostname || '').localeCompare(b.hostname || ''));
    return openPanel({ icon: 'hosts', title: 'เครื่องทั้งหมด', sub: `ออนไลน์ ${live.filter(h => h.online).length} จาก ${live.length} เครื่อง`,
      body: table(rows, [
        { h: 'เครื่อง', v: h => hostLink(h) + `<div class="mute" style="font-size:12px">${esc(siteName(h.site_id))}</div>` },
        { h: 'สถานะ', v: h => `<span class="badge ${({ on: 'ok', off: '', busy: 'accent', warn: 'warn', bad: 'bad', stuck: 'bad' })[tileState(h)]}">${esc(TILE_LABEL[tileState(h)])}</span>` },
        { h: 'IP', v: h => `<span class="mono">${esc(hostIPs(h)[0] || '—')}</span>` },
        { h: 'ติดต่อล่าสุด', v: h => h.online ? 'ตอนนี้' : h.last_seen ? when(h.last_seen) : '—', cls: 'nowrap' },
      ], empty('hosts', 'ยังไม่มีเครื่อง', '<div><a href="#/enroll">เพิ่มเครื่อง</a></div>')) + `<div class="panel-foot"><a class="btn" href="#/hosts">เปิดหน้าเครื่องทั้งหมด</a></div>` });
  }
  if (key === 'busy') {
    const busy = live.filter(h => tileState(h) === 'busy');
    return openPanel({ icon: 'capture', title: 'กำลังติดตั้ง Windows', sub: busy.length ? `${busy.length} เครื่อง · อัปเดตทุก 15 วินาทีในหน้าเครื่อง` : 'ไม่มีงานติดตั้งค้างอยู่',
      body: busy.length ? busy.map(h => { const j = jobLive[h.id] || h.job || {};
        return `<div class="panel-item"><div class="pi-head">${hostLink(h)}<span class="mono mute">${esc(j.image_version || '')}</span>${jobBadge(j.status)}</div>${jobProgress(j)}</div>`; }).join('')
        : `<div class="empty">${ic('check')}<b>ไม่มีเครื่องกำลังติดตั้ง</b><div>เริ่มติดตั้งได้จากหน้าเครื่อง → "ล้างเครื่องและติดตั้ง"</div></div>` });
  }
  if (key === 'care') {
    const care = live.filter(h => ['warn', 'bad', 'stuck'].includes(tileState(h)));
    return openPanel({ icon: 'alert', title: 'เครื่องที่ต้องดูแล', sub: care.length ? `${care.length} เครื่อง — กดชื่อเครื่องเพื่อแก้ไข` : 'ไม่มี',
      body: care.length ? care.map(h => `<div class="panel-item"><div class="pi-head">${hostLink(h)}<span class="mute">${esc(siteName(h.site_id))}</span></div>
          <ul class="reasons">${(tileState(h) === 'stuck' ? [['bad', 'ติดตั้ง Windows ไม่สำเร็จ — เครื่องรออยู่ใน WinPE กด "ลองใหม่" ในหน้าเครื่อง']] : [])
            .concat(h.review_at ? [['warn', 'รอตรวจสอบก่อนรับกลับเข้าระบบ']] : [], hostAlerts(h).filter(a => a[0] === 'bad' || a[0] === 'warn'))
            .map(([l, t]) => `<li class="${l}">${esc(t)}</li>`).join('')}</ul></div>`).join('')
        : `<div class="empty">${ic('check')}<b>ทุกเครื่องปกติ</b></div>` });
  }
}

// ───────── server storage: what uses the disk, and one button to clear what is no longer needed ─────────
const STORE_CAT = {
  vms: ['เครื่องต้นแบบ (VM)', 'ดิสก์ของเครื่องต้นแบบ, nvram และ snapshot — จัดการที่หน้า Images'],
  images: ['Image ที่สร้างแล้ว', 'ไฟล์ .wim ของแต่ละเวอร์ชัน — ลบเวอร์ชันเก่าได้ที่หน้า Images'],
  winpe: ['WinPE', 'ระบบที่ใช้บูตตอนติดตั้ง Windows และสำเนาต่องานติดตั้ง'],
  rescue: ['แผ่นกู้เครื่อง', 'สื่อบูตสำหรับเครื่องที่ติดตั้งไม่สำเร็จ'],
  agent: ['ตัวติดตั้ง Agent', 'ไฟล์ Agent ทุกเวอร์ชันที่เคย build'],
  iso: ['ไฟล์ ISO', 'จัดการผ่าน ssh เท่านั้น'],
  drivers: ['ไดรเวอร์', 'ไดรเวอร์ที่ใส่ตอนติดตั้ง'],
  logs: ['Log งาน', 'Log ของงานสร้าง image (เก่ากว่า 30 วันลบได้)'],
};
const STORE_COL = ['var(--ink)', 'var(--s-blue)', '#7d8ca3', '#b7a07a', '#8fa894', '#a990b4', '#c48a7c', '#9db3c9'];
let storeCache = null;
async function pageStorage() {
  const d = storeCache = await api('/v1/capacity/server/detail'), st = d.storage, used = st.total_bytes - st.free_bytes;
  const cats = d.categories.filter(c => c.bytes > 0).sort((a, b) => b.bytes - a.bytes);
  const seg = [...cats.map((c, i) => ({ k: c.key, label: STORE_CAT[c.key]?.[0] || c.key, bytes: c.bytes, col: STORE_COL[i % STORE_COL.length] })),
    { k: 'other', label: 'ระบบอื่น (OS, Docker, ฐานข้อมูล)', bytes: d.other_bytes, col: 'var(--faint)' }];
  const pct = b => (b / st.total_bytes * 100).toFixed(2);
  const removable = cats.flatMap(c => c.items.filter(i => i.removable).map(i => ({ ...i, cat: c.key })));
  const fmtD = t => t ? esc(new Date(t).toLocaleDateString(LOC(), { day: 'numeric', month: 'short', year: '2-digit' })) : '—';
  return `
  <section class="card store-head">
    <div class="store-top">
      <div><span class="stat-l">ใช้ไป</span><div class="store-big">${fmtBytes(used)}<small> / ${fmtBytes(st.total_bytes)}</small></div>
        <span class="stat-s">${st.used_pct.toFixed(0)}% · ว่าง ${fmtBytes(st.free_bytes)}${st.level !== 'ok' ? ` · <b style="color:var(--${st.level === 'block' ? 'bad' : 'warn'})">ใกล้เต็ม</b>` : ''}</span></div>
      <div class="spacer"></div>
      <div class="store-clean"><span class="stat-l">ลบได้อย่างปลอดภัย</span><div class="store-big">${fmtBytes(d.removable_bytes)}</div>
        <button class="btn ${removable.length ? 'primary' : ''}" data-store-clean ${removable.length ? '' : 'disabled'}>${ic('trash')}ล้างไฟล์ที่ไม่ใช้แล้ว</button></div>
    </div>
    <div class="store-bar" role="img" aria-label="สัดส่วนการใช้พื้นที่">${seg.map(s => `<i style="width:${pct(s.bytes)}%;background:${s.col}" title="${esc(s.label)} · ${fmtBytes(s.bytes)}"></i>`).join('')}</div>
    <div class="store-legend">${seg.map(s => `<span><i style="background:${s.col}"></i>${esc(s.label)} <b>${fmtBytes(s.bytes)}</b></span>`).join('')}
      <span><i style="background:var(--line-2);outline:1px solid var(--line)"></i>ว่าง <b>${fmtBytes(st.free_bytes)}</b></span></div>
  </section>
  <div class="store-cats">${cats.map(c => `<details class="card store-cat" ${c.removable_bytes ? 'open' : ''}>
    <summary><div><b>${esc(STORE_CAT[c.key]?.[0] || c.key)}</b><p>${esc(STORE_CAT[c.key]?.[1] || '')}</p></div><div class="spacer"></div>
      ${c.removable_bytes ? `<span class="badge warn plain">ลบได้ ${fmtBytes(c.removable_bytes)}</span>` : ''}<span class="store-size">${fmtBytes(c.bytes)}</span>${ic('chevron')}</summary>
    ${table(c.items.slice().sort((a, b) => b.bytes - a.bytes), [
      { h: 'ไฟล์', v: i => `<span class="mono" style="font-size:12.5px">${esc(i.name)}</span>${i.note ? `<div class="mute" style="font-size:12px">${esc(i.note)}</div>` : ''}` },
      { h: 'ขนาด', v: i => fmtBytes(i.bytes), cls: 'nowrap' },
      { h: 'แก้ไขล่าสุด', v: i => fmtD(i.modified), cls: 'nowrap' },
      { h: '', v: i => i.removable ? '<span class="badge warn plain">ไม่ใช้แล้ว</span>' : '<span class="mute" style="font-size:12px">ใช้อยู่</span>', cls: 'nowrap' },
    ])}</details>`).join('')}</div>
  <p class="mute" style="font-size:12.5px;margin:4px 2px">ไฟล์ที่ "ใช้อยู่" ระบบไม่ลบให้: image ลบได้ที่หน้า Images · เครื่องต้นแบบและ ISO จัดการผ่าน ssh · "ระบบอื่น" คือ Ubuntu, Docker และฐานข้อมูลบน server</p>`;
}
function askStorageCleanup() {
  const d = storeCache; if (!d) return;
  const items = d.categories.flatMap(c => c.items.filter(i => i.removable).map(i => ({ ...i, cat: STORE_CAT[c.key]?.[0] || c.key })));
  modal({ title: `ล้างไฟล์ที่ไม่ใช้แล้ว ${fmtBytes(d.removable_bytes)}?`, danger: true, confirm: 'ลบไฟล์เหล่านี้', wide: true,
    desc: `ลบ ${items.length} รายการด้านล่างออกจาก server — ทั้งหมดเป็นไฟล์ที่ระบบไม่ได้ใช้แล้ว (Agent เวอร์ชันเก่า, WinPE ของงานติดตั้งที่จบแล้ว, log เก่า) ไม่กระทบ image, เครื่องต้นแบบ หรือเครื่องในระบบ`,
    body: `<div class="clean-list">${table(items, [
      { h: 'ประเภท', v: i => esc(i.cat), cls: 'nowrap' }, { h: 'ไฟล์', v: i => `<span class="mono" style="font-size:12px">${esc(i.name)}</span>` },
      { h: 'ขนาด', v: i => fmtBytes(i.bytes), cls: 'nowrap' }])}</div>`,
    onConfirm: async () => {
      const r = await api('/v1/capacity/server/cleanup', 'POST', {});
      toast(`คืนพื้นที่แล้ว ${fmtBytes(r.freed_bytes)}${r.failed?.length ? ` · ลบไม่ได้ ${r.failed.length} รายการ` : ''}`, r.failed?.length ? 'bad' : '');
      renderPage();
    } });
}

// ───────── remote desktop in the browser (Guacamole), through the server only ─────────
// agents from 0.5.15 open the session with a one-time account, no password prompt
// Guacamole keeps its sign-in in this browser. A link from an earlier remote session leaves a
// token behind whose one-time data has expired; the next link then opened on that stale
// sign-in and Guacamole answered "tunnel destination does not exist" (a blank page with
// nothing to press). Sign that old session out first, so every link signs in afresh.
async function guacSignOut() {
  try {
    for (const k of ['GUAC_AUTH_TOKEN', 'GUAC_AUTH']) {
      const v = localStorage.getItem(k); if (!v) continue;
      let t = v; try { const j = JSON.parse(v); t = typeof j === 'string' ? j : j?.authToken || ''; } catch (e) {}
      if (t && /^[A-Za-z0-9]+$/.test(t)) await fetch('/guacamole/api/tokens/' + t, { method: 'DELETE' }).catch(() => {});
      localStorage.removeItem(k);
    }
  } catch (e) {}
}
const MIN_PWLESS = '0.5.15';
const remotePwless = h => !!h.agent_version && verCmp(h.agent_version, MIN_PWLESS) >= 0;
function askRemote() {
  const h = hostCache, name = esc(h.hostname || h.id), pw = remotePwless(h);
  modal({ title: 'รีโมตเข้าเครื่อง', confirm: 'เชื่อมต่อ',
    desc: pw ? `เปิดหน้าจอของ <b>${name}</b> ในแท็บใหม่ได้ทันที ไม่ต้องใส่รหัส — เข้าด้วยบัญชีผู้ดูแลชั่วคราว <b>swim-remote</b> ที่ SWIM สร้างและปิดเองภายใน 10 นาที`
      : `เปิดหน้าจอของ <b>${name}</b> ในแท็บใหม่ผ่าน Remote Desktop — Windows จะถามชื่อผู้ใช้และรหัสผ่านของเครื่องตอนเชื่อมต่อ (SWIM ไม่เห็นและไม่เก็บรหัสผ่าน)`,
    body: pw ? `<div class="callout" style="margin:0 0 10px">${ic('shield')}<div>เปิด Remote Desktop ให้เฉพาะ SWIM server · รหัสผ่านสร้างใหม่ทุกครั้ง ไม่แสดงและไม่ถูกบันทึกที่ใด · ทุกครั้งที่เชื่อมต่อถูกบันทึกในประวัติการใช้งาน</div></div>
      <div class="callout warn" style="margin:0">${ic('info')}<div>${inv(h).user ? `ตอนนี้ <b>${esc(inv(h).user)}</b> ใช้เครื่องอยู่ ` : 'ถ้ามีคนใช้เครื่องอยู่ '}เขาจะเห็นข้อความว่า IT กำลังดูแลเครื่อง แล้วหน้าจอจะถูกล็อกทันที
        งานและโปรแกรมที่เปิดไว้ยังอยู่ครบ ระหว่างนี้มีแค่คุณที่ควบคุมเครื่องได้ เมื่อคุณออก เขาล็อกอินกลับมาทำงานต่อได้
        <br><span class="mute">ถ้ามีคนพยายามล็อกอินที่หน้าเครื่องระหว่างนี้ ระบบจะล็อกจอเขาอีกครั้ง แต่หน้ารีโมตของคุณอาจหลุดชั่วครู่ ให้กดเชื่อมต่อใหม่</span></div></div>`
    : `<label class="check"><input type="checkbox" name="enable" checked> เปิด Remote Desktop บนเครื่องนี้ก่อน <span class="mute">— อนุญาตเฉพาะการเชื่อมต่อจาก SWIM server เท่านั้น ไม่เปิดให้เครื่องอื่นในเครือข่าย</span></label>
      <div class="field" style="margin-top:12px"><label>ชื่อผู้ใช้ Windows <small class="mute">(ไม่บังคับ — กรอกไว้ล่วงหน้า)</small></label><input class="input" name="username" autocomplete="off" placeholder="เช่น .\\admin หรือ DOMAIN\\user"></div>
      <div class="callout" style="margin:0">${ic('info')}<div>ลิงก์ใช้ได้ 5 นาที · ทุกครั้งที่เชื่อมต่อถูกบันทึกในประวัติการใช้งาน</div></div>`,
    onConfirm: async f => {
      const w = window.open('', '_blank'); // open now (user click) so the browser does not block it
      if (w) w.document.write('<p style="font:16px system-ui;padding:24px">กำลังเตรียม Remote Desktop…</p>');
      try {
        const r = await api('/v1/hosts/' + encodeURIComponent(h.id) + '/remote', 'POST', pw ? {} : { enable: !!f.enable, username: (f.username || '').trim() });
        if (r.command_id) {
          toast('กำลังเปิด Remote Desktop บนเครื่อง…');
          const t0 = Date.now();
          for (;;) {
            const c = await api('/v1/commands/' + encodeURIComponent(r.command_id));
            if (c.status === 'done') { const d = c.result?.data || {}; if (d.console_user && d.console_locked) toast(`แจ้งและล็อกจอของ ${d.console_user} แล้ว งานที่เปิดไว้ยังอยู่`); break; }
            if (['failed', 'expired', 'cancelled'].includes(c.status)) throw new Error('เปิด Remote Desktop บนเครื่องไม่สำเร็จ: ' + (c.result?.error || c.status));
            if (Date.now() - t0 > 90000) throw new Error('เครื่องยังไม่ตอบ — ลองใหม่อีกครั้ง');
            await new Promise(res => setTimeout(res, 1500));
          }
        }
        await guacSignOut();
        if (w) w.location = r.url; else window.open(r.url, '_blank');
        toast(`เปิดหน้าจอ ${h.hostname} ในแท็บใหม่แล้ว`); renderPage();
      } catch (e) { if (w) w.close(); throw e; }
    } });
}

// ───────── ready-made actions ─────────
// Only what the page needs to draw each tool lives here. The PowerShell of every tool is
// written and checked on the server (backend/internal/api/tools.go): the page sends the
// tool's id and the form values, never script text.
const PRESETS = [
  {"id":"shutdown","g":"พลังงาน","icon":"power","label":"ปิดเครื่อง","danger":true,"desc":"ผู้ใช้จะเห็นข้อความและนับถอยหลังก่อนปิด","fields":"<div class=\"row-fields\"><div class=\"field\"><label>ปิดในอีก</label><select class=\"select\" name=\"t\"><option value=\"60\">1 นาที</option><option value=\"300\" selected>5 นาที</option><option value=\"900\">15 นาที</option><option value=\"0\">ทันที</option></select></div>\n      <div class=\"field\"><label>ข้อความถึงผู้ใช้</label><input class=\"input\" name=\"msg\" maxlength=\"200\" value=\"IT จะปิดเครื่องนี้ กรุณาบันทึกงาน\"></div></div>"},
  {"id":"abort","g":"พลังงาน","icon":"x","label":"ยกเลิกการปิด/รีบูต","desc":"ยกเลิกการปิดหรือรีบูตที่ตั้งเวลาไว้"},
  {"id":"lock","g":"พลังงาน","icon":"shield","label":"ล็อกหน้าจอ","desc":"ล็อกหน้าจอของผู้ใช้ที่ล็อกอินอยู่ (งานที่เปิดไว้ยังอยู่)"},
  {"id":"logoff","g":"พลังงาน","icon":"logout","label":"ออกจากระบบผู้ใช้","danger":true,"desc":"ผู้ใช้ที่ล็อกอินอยู่จะถูกออกจากระบบทันที งานที่ยังไม่บันทึกจะหาย"},
  {"id":"msg","g":"ผู้ใช้","icon":"info","label":"ส่งข้อความถึงผู้ใช้","desc":"ขึ้นกล่องข้อความบนหน้าจอของทุกคนที่ล็อกอินอยู่","fields":"<div class=\"field\"><label>ข้อความ</label><textarea class=\"input\" name=\"text\" rows=\"3\" maxlength=\"400\" required></textarea></div>\n      <div class=\"field\"><label>แสดงนาน</label><select class=\"select\" name=\"sec\"><option value=\"60\">1 นาที</option><option value=\"300\" selected>5 นาที</option><option value=\"3600\">1 ชั่วโมง</option></select></div>"},
  {"id":"users","g":"ผู้ใช้","icon":"hosts","label":"ดูบัญชีผู้ใช้","view":true},
  {"id":"useradd","g":"ผู้ใช้","icon":"plus","label":"สร้างบัญชีผู้ใช้","pw":true,"desc":"สร้างบัญชีในเครื่อง (local account) — ผู้ใช้ต้องเปลี่ยนรหัสผ่านตอนล็อกอินครั้งแรก","fields":"<div class=\"row-fields\"><div class=\"field\"><label>ชื่อผู้ใช้</label><input class=\"input\" name=\"user\" required pattern=\"[A-Za-z0-9._-]{1,20}\" autocomplete=\"off\"></div>\n      <div class=\"field\"><label>ชื่อเต็ม</label><input class=\"input\" name=\"full\" maxlength=\"60\"></div></div>\n      <div class=\"field\"><label>รหัสผ่านชั่วคราว</label><input class=\"input\" name=\"pass\" type=\"password\" required minlength=\"8\" autocomplete=\"new-password\"></div>\n      <label class=\"check\"><input type=\"checkbox\" name=\"admin\"> เป็นผู้ดูแลเครื่อง (Administrators)</label>"},
  {"id":"userpw","g":"ผู้ใช้","icon":"shield","label":"ตั้งรหัสผ่านใหม่","pw":true,"desc":"ตั้งรหัสผ่านชั่วคราวให้บัญชีในเครื่อง ผู้ใช้ต้องเปลี่ยนตอนล็อกอินครั้งถัดไป","fields":"<div class=\"field\"><label>ชื่อผู้ใช้</label><input class=\"input\" name=\"user\" required autocomplete=\"off\"></div>\n      <div class=\"field\"><label>รหัสผ่านชั่วคราว</label><input class=\"input\" name=\"pass\" type=\"password\" required minlength=\"8\" autocomplete=\"new-password\"></div>"},
  {"id":"userstate","g":"ผู้ใช้","icon":"reset","label":"เปิด/ปิดบัญชี","fields":"<div class=\"row-fields\"><div class=\"field\"><label>ชื่อผู้ใช้</label><input class=\"input\" name=\"user\" required autocomplete=\"off\"></div>\n      <div class=\"field\"><label>ทำอะไร</label><select class=\"select\" name=\"on\"><option value=\"0\">ปิดบัญชี (ล็อกอินไม่ได้)</option><option value=\"1\">เปิดบัญชี</option></select></div></div>"},
  {"id":"apps","g":"โปรแกรม","icon":"layers","label":"โปรแกรมที่ติดตั้ง","view":true},
  {"id":"uninstall","g":"โปรแกรม","icon":"trash","label":"ถอนโปรแกรม","danger":true,"desc":"ถอนแบบเงียบ (ไม่ถามผู้ใช้) — ใช้ชื่อตามรายการ \"โปรแกรมที่ติดตั้ง\"","fields":"<div class=\"field\"><label>ชื่อโปรแกรม <small class=\"mute\">(ตรงกับชื่อในรายการ หรือขึ้นต้นด้วยคำนี้)</small></label><input class=\"input\" name=\"app\" required autocomplete=\"off\"></div>"},
  {"id":"procs","g":"โปรแกรม","icon":"console","label":"โปรเซสที่ทำงาน","view":true},
  {"id":"kill","g":"โปรแกรม","icon":"x","label":"ปิดโปรเซส","danger":true,"desc":"บังคับปิดโปรแกรม งานที่ยังไม่บันทึกในโปรแกรมนั้นจะหาย","fields":"<div class=\"field\"><label>ชื่อโปรเซสหรือ PID</label><input class=\"input\" name=\"p\" required autocomplete=\"off\" placeholder=\"เช่น chrome หรือ 4120\"></div>"},
  {"id":"services","g":"ระบบ","icon":"list","label":"ดู Service","view":true},
  {"id":"service","g":"ระบบ","icon":"refresh","label":"จัดการ Service","fields":"<div class=\"row-fields\"><div class=\"field\"><label>ชื่อ Service</label><input class=\"input\" name=\"svc\" required autocomplete=\"off\" placeholder=\"เช่น Spooler\"></div>\n      <div class=\"field\"><label>ทำอะไร</label><select class=\"select\" name=\"act\"><option value=\"Restart\">รีสตาร์ต</option><option value=\"Start\">เริ่ม</option><option value=\"Stop\">หยุด</option></select></div>\n      <div class=\"field\"><label>เริ่มอัตโนมัติ</label><select class=\"select\" name=\"st\"><option value=\"\">ไม่เปลี่ยน</option><option value=\"Automatic\">อัตโนมัติ</option><option value=\"Manual\">ด้วยตนเอง</option><option value=\"Disabled\">ปิดใช้งาน</option></select></div></div>"},
  {"id":"wu-check","g":"ระบบ","icon":"search","label":"ตรวจ Windows Update","view":true},
  {"id":"wu-install","g":"ระบบ","icon":"capture","label":"ติดตั้ง Windows Update","desc":"ดาวน์โหลดและติดตั้งอัปเดตทั้งหมด (อาจใช้เวลานาน) — ไม่รีบูตเอง ระบบจะบอกถ้าต้องรีบูต"},
  {"id":"uefi-check","g":"ระบบ","icon":"search","label":"ตรวจความพร้อม UEFI / Windows 11","view":true},
  {"id":"rename","g":"ระบบ","icon":"hosts","label":"เปลี่ยนชื่อเครื่อง","desc":"ชื่อใหม่มีผลหลังรีบูต","fields":"<div class=\"field\"><label>ชื่อใหม่ <small class=\"mute\">(A–Z, 0–9, ขีด; ไม่เกิน 15 ตัว)</small></label><input class=\"input mono\" name=\"nn\" required pattern=\"[A-Za-z0-9-]{1,15}\" autocomplete=\"off\"></div>\n      <label class=\"check\"><input type=\"checkbox\" name=\"rb\"> รีบูตเครื่องหลังเปลี่ยนชื่อ (ใน 1 นาที)</label>"},
  {"id":"time","g":"ระบบ","icon":"clock","label":"ตั้งเวลา / โซนเวลา","fields":"<div class=\"field\"><label>โซนเวลา</label><select class=\"select\" name=\"tz\"><option value=\"SE Asia Standard Time\">กรุงเทพ (UTC+7)</option><option value=\"\">ไม่เปลี่ยน</option>\n      <option value=\"Singapore Standard Time\">สิงคโปร์ (UTC+8)</option><option value=\"Tokyo Standard Time\">โตเกียว (UTC+9)</option><option value=\"UTC\">UTC</option></select></div>\n      <label class=\"check\"><input type=\"checkbox\" name=\"sync\" checked> ซิงก์นาฬิกากับ time server ตอนนี้</label>"},
  {"id":"rdp-off","g":"ระบบ","icon":"shield","label":"ปิด Remote Desktop","desc":"ปิด Remote Desktop และกฎ firewall ที่เปิดไว้ให้ SWIM server"},
  {"id":"ipconfig","g":"เครือข่าย","icon":"link","label":"ดูการตั้งค่าเครือข่าย","view":true},
  {"id":"ping","g":"เครือข่าย","icon":"link","label":"ทดสอบการเชื่อมต่อ","view":true,"fields":"<div class=\"row-fields\"><div class=\"field\"><label>ปลายทาง</label><input class=\"input mono\" name=\"to\" required value=\"8.8.8.8\" autocomplete=\"off\"></div>\n      <div class=\"field\"><label>Port <small class=\"mute\">(ไม่บังคับ)</small></label><input class=\"input mono\" name=\"port\" inputmode=\"numeric\" pattern=\"\\d{0,5}\"></div></div>"},
  {"id":"dns","g":"เครือข่าย","icon":"refresh","label":"ล้าง DNS cache"},
  {"id":"disk","g":"ดิสก์","icon":"hdd","label":"ดูพื้นที่ดิสก์","view":true},
  {"id":"temp","g":"ดิสก์","icon":"trash","label":"ล้างไฟล์ชั่วคราว","desc":"ลบไฟล์ใน Windows\\Temp และ Temp ของทุกผู้ใช้ (ไฟล์ที่เปิดอยู่จะถูกข้าม)"},
  {"id":"hiber","g":"ดิสก์","icon":"power","label":"ปิด/เปิด Hibernate","desc":"ปิด Hibernate จะลบไฟล์ hiberfil.sys และคืนพื้นที่ประมาณขนาด RAM","fields":"<div class=\"field\"><label>ทำอะไร</label><select class=\"select\" name=\"on\"><option value=\"off\">ปิด Hibernate (คืนพื้นที่)</option><option value=\"on\">เปิด Hibernate</option></select></div>"},
];
const PRESET_GROUPS = ['พลังงาน', 'ผู้ใช้', 'โปรแกรม', 'ระบบ', 'เครือข่าย', 'ดิสก์'];
const TOOL_HINT = {
  shutdown: 'ปิดเครื่องพร้อมนับถอยหลังและข้อความ', abort: 'ยกเลิกการปิด/รีบูตที่ตั้งเวลาไว้', lock: 'ล็อกหน้าจอผู้ใช้ งานยังอยู่', logoff: 'ออกจากระบบผู้ใช้ทันที',
  msg: 'ขึ้นข้อความบนหน้าจอผู้ใช้', users: 'บัญชีในเครื่องและผู้ดูแล', useradd: 'บัญชีใหม่ บังคับเปลี่ยนรหัสครั้งแรก', userpw: 'ตั้งรหัสชั่วคราวให้บัญชี', userstate: 'อนุญาตหรือห้ามล็อกอิน',
  apps: 'รายชื่อ เวอร์ชัน และผู้ผลิต', uninstall: 'ถอนแบบเงียบ ไม่ถามผู้ใช้', procs: '80 โปรเซสที่ใช้หน่วยความจำมากสุด', kill: 'บังคับปิดตามชื่อหรือ PID',
  services: 'ทุก Service และสถานะ', service: 'เริ่ม หยุด รีสตาร์ต ตั้งค่าเริ่มอัตโนมัติ', 'wu-check': 'อัปเดตที่ยังไม่ได้ติดตั้ง', 'wu-install': 'ดาวน์โหลดและติดตั้งทั้งหมด',
  rename: 'มีผลหลังรีบูต', 'uefi-check': 'โหมดบูต ดิสก์ MBR/GPT TPM และ CPU', time: 'โซนเวลาและซิงก์นาฬิกา', 'rdp-off': 'ปิดและปิดกฎ firewall',
  ipconfig: 'ipconfig /all', ping: 'ping / ทดสอบ port', dns: 'ipconfig /flushdns',
  disk: 'ขนาดและพื้นที่ว่างทุกไดรฟ์', temp: 'Windows\\Temp และ Temp ผู้ใช้', hiber: 'ปิดเพื่อคืนพื้นที่ขนาดเท่า RAM',
};
const TOOL_TONE = { 'พลังงาน': 'yellow', 'ผู้ใช้': 'lilac', 'โปรแกรม': 'blue', 'ระบบ': 'green', 'เครือข่าย': 'peach', 'ดิสก์': 'grey', 'อื่น ๆ': 'grey' };
const toolState = { cat: store.get('swim-tool-cat') || '', q: '' };
const toolRecent = { get() { try { return JSON.parse(localStorage.getItem('swim-tools-recent') || '[]'); } catch (e) { return []; } },
  add(id) { try { localStorage.setItem('swim-tools-recent', JSON.stringify([id, ...this.get().filter(x => x !== id)].slice(0, 6))); } catch (e) {} } };
function toolTile(p, dis) {
  return `<button class="tl-tile tone-${TOOL_TONE[p.g]}" data-preset="${p.id}" ${dis}>
    <span class="tl-ico">${ic(p.icon)}</span><span class="tl-txt"><b>${esc(p.label)}</b><small>${esc(TOOL_HINT[p.id] || '')}</small></span>
    ${p.view ? '<span class="tl-tag view">ดูอย่างเดียว</span>' : p.danger ? '<span class="tl-tag danger" title="กระทบผู้ใช้ที่ใช้งานอยู่">กระทบผู้ใช้</span>' : ''}</button>`;
}
function toolsGridHTML(dis) {
  const q = toolState.q.trim().toLowerCase();
  let list, title;
  if (q) { list = PRESETS.filter(p => (p.label + ' ' + (TOOL_HINT[p.id] || '') + ' ' + p.g).toLowerCase().includes(q)); title = `ผลการค้นหา "${esc(toolState.q)}"`; }
  else if (toolState.cat === 'ล่าสุด') { list = toolRecent.get().map(id => PRESETS.find(p => p.id === id)).filter(Boolean); title = 'ใช้ล่าสุด'; }
  else { const g = PRESET_GROUPS.includes(toolState.cat) || toolState.cat === 'อื่น ๆ' ? toolState.cat : PRESET_GROUPS[0]; list = PRESETS.filter(p => p.g === g); title = g; }
  const script = `<button class="tl-tile tone-grey" data-cmd="run_script" ${dis}><span class="tl-ico">${ic('console')}</span>
    <span class="tl-txt"><b>รันสคริปต์ PowerShell</b><small>เลือกจากสคริปต์ที่บันทึกไว้บน server</small></span></button>`;
  const showScript = (!q && toolState.cat === 'อื่น ๆ') || (q && 'รันสคริปต์ powershell script'.includes(q));
  return `<div class="tl-title">${title}<span>${list.length + (showScript ? 1 : 0)} รายการ</span></div>
    <div class="tl-grid">${list.map(p => toolTile(p, dis)).join('')}${showScript ? script : ''}${!list.length && !showScript ? '<p class="mute">ไม่พบเครื่องมือ</p>' : ''}</div>`;
}
function toolsCard(h, dis) {
  const recent = toolRecent.get();
  const cats = [...(recent.length ? ['ล่าสุด'] : []), ...PRESET_GROUPS, 'อื่น ๆ'];
  if (!cats.includes(toolState.cat)) toolState.cat = recent.length ? 'ล่าสุด' : PRESET_GROUPS[0];
  const count = c => c === 'ล่าสุด' ? recent.length : c === 'อื่น ๆ' ? 1 : PRESETS.filter(p => p.g === c).length;
  return card('เครื่องมือ', 'สคริปต์ของทุกเครื่องมือเขียนและตรวจบน server แล้วเซ็นก่อนส่ง รันเป็น SYSTEM ถามยืนยันก่อนเสมอ และเก็บผลลัพธ์ไว้ในประวัติ', `<div class="tools2" data-dis="${esc(dis)}">
    <aside class="tl-rail"><div class="search tl-search">${ic('search')}<input class="input" id="tl-q" placeholder="ค้นหาเครื่องมือ" value="${esc(toolState.q)}" autocomplete="off"></div>
      <nav>${cats.map(c => `<button class="tl-cat${!toolState.q && toolState.cat === c ? ' on' : ''}" data-tl-cat="${esc(c)}"><i class="tone-${TOOL_TONE[c] || 'yellow'}"></i>${esc(c)}<span>${count(c)}</span></button>`).join('')}</nav>
      <p class="tl-key"><span class="tl-tag view">ดูอย่างเดียว</span> ไม่เปลี่ยนอะไรในเครื่อง<br><span class="tl-tag danger">กระทบผู้ใช้</span> ผู้ใช้จะเห็นผลทันที</p></aside>
    <div class="tl-main" id="tl-main">${toolsGridHTML(dis)}</div></div>`, '', true);
}
function redrawTools() {
  const box = $('.tools2'); if (!box) return;
  const dis = box.dataset.dis;
  $('#tl-main').innerHTML = toolsGridHTML(dis);
  box.querySelectorAll('.tl-cat').forEach(b => b.classList.toggle('on', !toolState.q && b.dataset.tlCat === toolState.cat));
}
document.addEventListener('keydown', e => { if (e.key === '/' && !/INPUT|TEXTAREA|SELECT/.test(document.activeElement?.tagName || '')) { const q = $('#host-q') || $('#gq'); if (q) { e.preventDefault(); q.focus(); } } });
document.addEventListener('input', e => { if (e.target.id === 'tl-q') { toolState.q = e.target.value; redrawTools(); } });
function askPreset(id) {
  const p = PRESETS.find(x => x.id === id), h = hostCache, name = esc(h.hostname || h.id);
  modal({ title: p.label, wide: !!p.fields, danger: !!p.danger, confirm: p.view ? 'ดูผล' : p.danger ? p.label : 'ส่งคำสั่ง',
    desc: `${p.desc ? esc(p.desc) + ' — ' : ''}บน <b>${name}</b>${h.online ? '' : ' (ออฟไลน์อยู่ — คำสั่งจะรอในคิว 1 ชั่วโมง)'}`,
    body: `${p.fields || ''}${p.pw ? `<div class="callout warn" style="margin:12px 0 0">${ic('alert')}<div>รหัสผ่านถูกส่งถึงเครื่องในคำสั่งที่เซ็นแล้ว และแสดงเป็น ******** ในประวัติการใช้งาน ใช้รหัสชั่วคราวเท่านั้น ผู้ใช้จะต้องเปลี่ยนตอนล็อกอินครั้งแรก</div></div>` : ''}
      <details class="preset-script"><summary>ดูสคริปต์ที่จะรัน</summary><pre class="code" id="preset-code"></pre></details>`,
    onConfirm: async f => {
      toolRecent.add(p.id);
      const r = await sendCommand('run_script', { tool: p.id, params: f });
      if (p.view && r?.id) watchCommand(r.id);
    } });
  const form = document.querySelector('.scrim form'), code = $('#preset-code');
  // the server builds the script; show what it would run (passwords come back as ********)
  let seq = 0, timer = 0;
  const draw = async () => {
    const n = ++seq;
    try { const r = await api('/v1/tools/preview', 'POST', { tool: p.id, params: Object.fromEntries(new FormData(form)) }); if (n === seq) code.textContent = r.body; }
    catch (e) { if (n === seq) code.textContent = e.status === 400 ? 'กรอกข้อมูลให้ครบก่อน: ' + e.message : e.message; }
  };
  const later = () => { clearTimeout(timer); timer = setTimeout(draw, 350); };
  form.addEventListener('input', later); form.addEventListener('change', later);
  form.querySelector('.preset-script').addEventListener('toggle', e => { if (e.target.open) draw(); });
}
// wait for a "view" command to finish, then open its output
async function watchCommand(id) {
  const t0 = Date.now();
  while (Date.now() - t0 < 180000) {
    await new Promise(res => setTimeout(res, 2000));
    try { const c = await api('/v1/commands/' + encodeURIComponent(id));
      if (['done', 'failed', 'expired', 'cancelled'].includes(c.status)) { if (route() === 'hosts') await renderPage(); return openCommandOutput(id); } } catch (e) { return; }
  }
}

// ───────── Home: the whole system at a glance — product header, key numbers, a live flow diagram, what is happening now ─────────
const reduceMotion = () => matchMedia('(prefers-reduced-motion: reduce)').matches;

// ───────── Home: an executive dashboard (KPI cards, charts, sites, what needs doing, health) ─────────
let liveEnter = true;
const pct0 = (a, b) => b ? Math.round(a / b * 100) : 0;
const upFor = s => s < 3600 ? `${Math.max(1, Math.round(s / 60))} นาที` : s < 172800 ? `${Math.round(s / 3600)} ชั่วโมง` : `${Math.round(s / 86400)} วัน`;
// donut: segments [{label, value, tone}]
function donut(segs, center, sub) {
  const total = segs.reduce((a, s) => a + s.value, 0), R = 54, C = 2 * Math.PI * R;
  let off = 0;
  const arcs = total ? segs.filter(s => s.value).map(s => { const len = s.value / total * C;
    const el = `<circle class="dn-seg ${s.tone}" r="${R}" cx="70" cy="70" stroke-dasharray="${Math.max(0, len - 2).toFixed(2)} ${(C - len + 2).toFixed(2)}" stroke-dashoffset="${(-off).toFixed(2)}" data-tip="${esc(s.label)}" data-tip-sub="${s.value} เครื่อง · ${pct0(s.value, total)}%"/>`;
    off += len; return el; }).join('') : `<circle class="dn-empty" r="${R}" cx="70" cy="70"/>`;
  return `<div class="donut"><svg viewBox="0 0 140 140" aria-hidden="true"><g transform="rotate(-90 70 70)">${arcs}</g>
      <text x="70" y="68" class="dn-v">${center}</text><text x="70" y="88" class="dn-s">${esc(sub)}</text></svg>
    <ul class="dn-legend">${segs.map(s => `<li><i class="${s.tone}"></i>${esc(s.label)}<b>${s.value}</b><span>${pct0(s.value, total)}%</span></li>`).join('')}</ul></div>`;
}
// grouped bars per day: days [{label, values:{k:n}}], series [{k, label, tone}]
function barChart(days, series) {
  const W = 520, H = 160, L = 30, B = 24, T = 10, max = Math.max(1, ...days.flatMap(d => series.map(s => d.values[s.k] || 0)));
  const nice = max <= 4 ? 4 : Math.ceil(max / 4) * 4, gw = (W - L) / days.length, bw = Math.min(14, (gw - 14) / series.length);
  const y = v => T + (H - T - B) * (1 - v / nice);
  let g = '';
  for (let i = 0; i <= 4; i++) { const v = nice * i / 4; g += `<line class="ch-grid" x1="${L}" x2="${W}" y1="${y(v)}" y2="${y(v)}"/><text class="ch-ax" x="${L - 6}" y="${y(v) + 4}" text-anchor="end">${Math.round(v)}</text>`; }
  days.forEach((d, i) => {
    const x0 = L + i * gw + (gw - bw * series.length - 3 * (series.length - 1)) / 2;
    series.forEach((s, k) => { const v = d.values[s.k] || 0, x = x0 + k * (bw + 3);
      g += `<rect class="ch-bar ${s.tone}" x="${x.toFixed(1)}" y="${y(v).toFixed(1)}" width="${bw.toFixed(1)}" height="${Math.max(v ? 2 : 0, H - B - y(v)).toFixed(1)}" rx="3" data-tip="${esc(d.full)} · ${esc(s.label)}" data-tip-sub="${v} ครั้ง"/>`; });
    g += `<text class="ch-ax" x="${(L + i * gw + gw / 2).toFixed(1)}" y="${H - 6}" text-anchor="middle">${esc(d.label)}</text>`;
  });
  return `<svg class="chart" viewBox="0 0 ${W} ${H}">${g}</svg>
    <div class="ch-legend">${series.map(s => `<span><i class="${s.tone}"></i>${esc(s.label)} <b>${days.reduce((a, d) => a + (d.values[s.k] || 0), 0)}</b></span>`).join('')}</div>`;
}
function sparkline(vals) {
  if (!vals || vals.length < 2) return '';
  const W = 120, H = 32, max = Math.max(1, ...vals), pts = vals.map((v, i) => [i / (vals.length - 1) * W, H - 3 - (H - 6) * v / max]);
  const d = pts.map((p, i) => (i ? 'L' : 'M') + p[0].toFixed(1) + ',' + p[1].toFixed(1)).join(' ');
  return `<svg class="spark" viewBox="0 0 ${W} ${H}" preserveAspectRatio="none"><path class="sp-area" d="${d} L${W},${H} L0,${H} Z"/><path class="sp-line" d="${d}"/></svg>`;
}
function wireTips(root) {
  if (!root) return;
  let t = document.querySelector('.fl-tip.global');
  if (!t) { t = document.createElement('div'); t.className = 'fl-tip global'; document.body.append(t); }
  root.addEventListener('mousemove', e => {
    const n = e.target.closest('[data-tip]'); if (!n) { t.classList.remove('on'); return; }
    t.innerHTML = `<b>${esc(n.dataset.tip)}</b>${n.dataset.tipSub ? `<span>${esc(n.dataset.tipSub)}</span>` : ''}`;
    t.classList.add('on');
    t.style.transform = `translate(${Math.min(e.clientX + 14, innerWidth - t.offsetWidth - 8)}px, ${Math.max(6, e.clientY - t.offsetHeight - 12)}px)`;
  });
  root.addEventListener('mouseleave', () => t.classList.remove('on'));
}

// ───────── "ผังระบบ": an illustration panel with a 3D (isometric) and a 2D (flat schematic) view ─────────
let topoView = ['grid'].includes(store.get('swim-topo-view')) ? 'grid' : 'hub', topoPaused = false, topoEnter = true, topoCtx = null;
const TK = 30; // px per world unit
const tP = (x, y, z = 0) => [(x - y) * TK, (x + y) * TK / 2 - z * TK * .82];
const f1 = n => n.toFixed(1);
// a polygon with rounded corners (quadratic curves at each vertex)
function roundPath(pts, r) {
  const n = pts.length; let d = '';
  for (let i = 0; i < n; i++) {
    const p = pts[i], a = pts[(i - 1 + n) % n], b = pts[(i + 1) % n];
    const la = Math.hypot(p[0] - a[0], p[1] - a[1]), lb = Math.hypot(b[0] - p[0], b[1] - p[1]);
    const ra = Math.min(r, la / 2), rb = Math.min(r, lb / 2);
    const s = [p[0] + (a[0] - p[0]) * ra / la, p[1] + (a[1] - p[1]) * ra / la], e = [p[0] + (b[0] - p[0]) * rb / lb, p[1] + (b[1] - p[1]) * rb / lb];
    d += `${i ? 'L' : 'M'}${f1(s[0])},${f1(s[1])} Q${f1(p[0])},${f1(p[1])} ${f1(e[0])},${f1(e[1])} `;
  }
  return d + 'Z';
}
const polyD = pts => 'M' + pts.map(p => f1(p[0]) + ',' + f1(p[1])).join(' L') + ' Z';
let tpId = 0;
// soft hexagonal "pillow" block centred at (xc, yc) with side a and height h
function pillow(xc, yc, a, h, tone, label, glyph = 'rack', extra = {}) {
  const x0 = xc - a / 2, x1 = xc + a / 2, y0 = yc - a / 2, y1 = yc + a / 2, id = 'tp' + (++tpId);
  const T0 = tP(x0, y0, h), T1 = tP(x1, y0, h), T2 = tP(x1, y1, h), T3 = tP(x0, y1, h), B1 = tP(x1, y0, 0), B2 = tP(x1, y1, 0), B3 = tP(x0, y1, 0);
  const sil = [T0, T1, B1, B2, B3, T3], r = TK * .55, silD = roundPath(sil, r);
  const [cx, cy] = tP(xc, yc, h);
  const maxW = a * TK * 1.6, fs = 11.5, maxC = Math.max(4, Math.floor(maxW / (fs * .6)));
  const lab = label.length > maxC ? label.slice(0, maxC - 1) + '…' : label;
  const g = glyph === 'rack' ? `<g class="tp-gl" transform="translate(${f1(cx)},${f1(cy - 15)})"><rect x="-12" y="-9" width="24" height="8" rx="2.5"/><rect x="-12" y="2" width="24" height="8" rx="2.5"/><circle cx="-6" cy="-5" r="1.4"/><circle cx="-6" cy="6" r="1.4"/></g>`
    : glyph === 'disc' ? `<g class="tp-gl" transform="translate(${f1(cx)},${f1(cy - 15)})"><ellipse cx="0" cy="-5" rx="11" ry="4"/><path d="M-11 -5v9c0 2.2 4.9 4 11 4s11-1.8 11-4v-9"/></g>`
    : glyph === 'pc' ? `<g class="tp-gl" transform="translate(${f1(cx)},${f1(cy - 15)})"><rect x="-12" y="-9" width="24" height="15" rx="2.5"/><path d="M-5 10h10M0 6v4"/></g>`
    : `<g class="tp-gl" transform="translate(${f1(cx)},${f1(cy - 15)})"><rect x="-11" y="-9" width="22" height="18" rx="3"/><path d="M-5 -2h10M-5 3h6"/></g>`;
  return { depth: xc + yc, cx, cy, top: Math.min(T0[1], T1[1], T3[1]), right: T1[0], svg: `<g class="tp-blk ${tone}${extra.cls ? ' ' + extra.cls : ''}" ${extra.attrs || ''}>
    <path class="tp-sh" d="${silD}" transform="translate(9,12)"/>
    <clipPath id="${id}"><path d="${silD}"/></clipPath>
    <g clip-path="url(#${id})"><path class="f-r" d="${polyD([T2, T1, B1, B2])}"/><path class="f-l" d="${polyD([T3, T2, B2, B3])}"/><path class="f-t" d="${polyD([T0, T1, T2, T3])}"/>
      <path class="f-sheen" d="${polyD([T0, T1, T2, T3])}"/><path class="f-edge" d="M${f1(T3[0])},${f1(T3[1])} L${f1(T2[0])},${f1(T2[1])} L${f1(T1[0])},${f1(T1[1])} M${f1(T2[0])},${f1(T2[1])} L${f1(B2[0])},${f1(B2[1])}"/></g>
    <path class="tp-ol" d="${silD}"/>${g}<text class="tp-lab" x="${f1(cx)}" y="${f1(cy + 9)}">${esc(lab)}</text></g>` };
}
const tpPill = (x, y, text, pct = null, tone = '', icon = 'dot') => {
  const w = Math.max(58, text.length * 7.3 + (pct != null ? 66 : 30));
  const ico = icon === 'time' ? `<path class="pi-ico" d="M10 7h8M10 19h8M11 7c0 4 6 4 6 6s-6 2-6 6"/>` : `<circle class="pi-ico" cx="14" cy="13" r="5"/>`;
  return `<g class="tp-pill ${tone}" transform="translate(${f1(x)},${f1(y - 13)})"><rect width="${f1(w)}" height="26" rx="13"/>${ico}
    ${pct != null ? `<rect x="25" y="10" width="34" height="6" rx="3" class="pi-bg"/><rect x="25" y="10" width="${f1(Math.max(3, Math.min(34, pct * .34)))}" height="6" rx="3" class="pi-bar"/>` : ''}
    <text x="${f1(w - 11)}" y="17.5" text-anchor="end">${esc(text)}</text></g>`;
};
const STATE3 = { on: 'blue', off: 'pale', busy: 'blue busy', warn: 'amber', bad: 'rose', stuck: 'rose' };
const STATE_RANK = { stuck: 5, bad: 4, busy: 3, warn: 2, on: 1, off: 0 };

function topoGroups(c) {
  return [...c.sites.map(s => ({ id: s.id, name: s.name, hosts: c.live.filter(h => h.site_id === s.id) })),
    { id: '', name: 'ไม่ระบุ site', hosts: c.live.filter(h => !h.site_id) }].filter(g => g.hosts.length).sort((a, b) => b.hosts.length - a.hosts.length);
}
// ───────── "ผังระบบ" hub view: SWIM control plane in the centre, the parts of the system around it ─────────
const HUB_ICON = {
  monitor: '<rect x="-17" y="-15" width="34" height="24" rx="3"/><path d="M-7 15h14M0 9v6"/>',
  stack: '<path d="M0 -16 L18 -7 L0 2 L-18 -7 Z"/><path d="M-18 0 L0 9 L18 0"/><path d="M-18 7 L0 16 L18 7"/>',
  db: '<ellipse cx="0" cy="-12" rx="15" ry="5"/><path d="M-15 -12 v24 c0 3 7 5 15 5 s15 -2 15 -5 v-24"/><path d="M-15 -4 c0 3 7 5 15 5 s15 -2 15 -5 M-15 4 c0 3 7 5 15 5 s15 -2 15 -5"/>',
  disk: '<rect x="-17" y="-12" width="34" height="24" rx="4"/><circle cx="0" cy="0" r="6"/><circle cx="10" cy="7" r="1.6"/>',
  pin: '<path d="M0 8 C-6 1 -7 -2 -7 -5 a7 7 0 0 1 14 0 c0 3 -1 6 -7 13 Z"/><circle cx="0" cy="-5" r="2.4"/>',
};
const hubIcon = (k, x, y, s = 1, cls = '') => `<g class="hb-ic ${cls}" transform="translate(${x},${y}) scale(${s})">${HUB_ICON[k]}</g>`;
function topoHub(c) {
  const { live, gm, pub, pubCheck, capture, st, hs } = c, motion = !reduceMotion();
  const groups = topoGroups(c);
  const W = 1200, CW = 300, CH = 176, COLX = [50, 450, 850], TOPY = 84;
  const rooms = groups.slice(0, groups.length > 6 ? 5 : 6), more = groups.length - rooms.length;
  const rowsR = Math.ceil((rooms.length + (more > 0 ? 1 : 0)) / 3) || 1;
  const PY = 330, PH = 140, PW = 380, PX = W / 2 - PW / 2;        // control plane
  const R1 = 560, RG = 74, H = R1 + rowsR * (CH + RG) + 10;
  const tip = (t, s) => `data-tip="${esc(t)}" data-tip-sub="${esc(s || '')}"`;
  let cards = '', lines = '', dots = '', labels = '';
  const card = (x, y, tone, inner, href, t, s) => `<a href="${href}" class="hb-node" ${tip(t, s)}><rect class="hb-card ${tone}" x="${x}" y="${y}" width="${CW}" height="${CH}" rx="6"/>${inner}</a>`;
  const loc = (x, y, text) => { const w = text.length * 8.4; return `<g class="hb-loc">${hubIcon('pin', x - w / 2 - 4, y - 5, .9)}<text x="${x + 10}" y="${y}" text-anchor="middle">${esc(text.toUpperCase())}</text></g>`; };
  const half = (cx, y, icon, name, role, cls = '') => `${hubIcon(icon, cx, y + 62, 1.15, cls)}<text class="hb-name" x="${cx}" y="${y + 118}">${esc(name)}</text><text class="hb-role" x="${cx}" y="${y + 138}">${esc(role)}</text>`;
  const split = (x, y) => `<path class="hb-div" d="M${x + CW / 2},${y + 26} V${y + CH - 26}"/>`;
  const line = (d, lab = [], lx = 0, ly = 0, anchor = 'middle', hot = false) => {
    lines += `<path class="hb-link${hot ? ' hot' : ''}" d="${d}" pathLength="1"/>`;
    if (lab.length) labels += lab.map((l, i) => `<text class="hb-lab" x="${lx}" y="${ly + i * 19}" text-anchor="${anchor}">${esc(l)}</text>`).join('');
    if (hot && motion) dots += `<circle r="5" class="hb-pkt"><animateMotion dur="2.2s" repeatCount="indefinite" path="${d}"/></circle>`;
  };
  const dotEnd = (x, y) => `<circle class="hb-end" cx="${x}" cy="${y}" r="3.5"/>`;
  const ctl = d => { lines += `<path class="hb-ctl" d="${d}"/>`; };
  // top-left: building images
  const gmOn = gm?.state === 'running', capPct = capture ? Math.floor(capture.result?.progress?.percent ?? 0) : null;
  const tl = [COLX[0], TOPY];
  cards += card(tl[0], tl[1], capture ? 'busy' : '', split(...tl) + half(tl[0] + CW / 4, tl[1], 'monitor', 'เครื่องต้นแบบ', gm ? `${gm.name} · ${gmOn ? 'เปิดอยู่' : 'ปิดอยู่'}` : 'ยังไม่ได้สร้าง')
    + half(tl[0] + CW * 3 / 4, tl[1], 'stack', 'Capture', capture ? `กำลังทำ ${capPct}%` : 'sysprep + ตรวจ 6 ข้อ'), '#/images', 'สร้าง image', capture ? `กำลัง Capture ${capPct}%` : 'จากเครื่องต้นแบบ');
  labels += loc(tl[0] + CW / 2, tl[1] - 22, 'สร้าง image');
  // top-right: image store
  const tr = [COLX[2], TOPY];
  cards += card(tr[0], tr[1], st.level === 'ok' ? '' : 'warn', split(...tr) + half(tr[0] + CW / 4, tr[1], 'db', 'Image', pub ? `${pub.version}${pubCheck ? ' · ' + pubCheck : ''}` : 'ยังไม่มี')
    + half(tr[0] + CW * 3 / 4, tr[1], 'disk', 'พื้นที่เก็บ', `ใช้ ${Math.round(st.used_pct)}% · ว่าง ${fmtBytes(st.free_bytes)}`), '#/storage', 'เก็บ image', `ดิสก์ ${Math.round(st.used_pct)}%`);
  labels += loc(tr[0] + CW / 2, tr[1] - 22, 'เก็บ image');
  // image path: build → store (top line), store → rooms (down the right, then along the row gap)
  const yTop = TOPY + CH / 2 + 20;
  line(`M${tl[0] + CW + 8},${yTop} H${tr[0] - 8}`, ['capture → image ใหม่', 'ตรวจอัตโนมัติก่อนอนุมัติ'], W / 2, yTop - 30, 'middle', !!capture);
  dots += dotEnd(tl[0] + CW + 8, yTop) + dotEnd(tr[0] - 8, yTop);
  // control plane
  const pTone = hs === 'ok' ? '' : 'bad';
  cards += `<a href="#/settings" class="hb-node" ${tip('SWIM Server', `${hs === 'ok' ? 'ทำงานปกติ' : 'ผิดปกติ'} · ${location.hostname}`)}><rect class="hb-plane ${pTone}" x="${PX}" y="${PY}" width="${PW}" height="${PH}" rx="14"/>
    <g transform="translate(${W / 2 - 92},${PY + 56})"><rect class="hb-mark" x="-22" y="-22" width="44" height="44" rx="10"/><path class="hb-mark-g" d="M-11 -9 h22 M-11 0 h22 M-11 9 h14"/></g>
    <text class="hb-word" x="${W / 2 - 58}" y="${PY + 70}">SWIM</text>
    <text class="hb-sub" x="${W / 2}" y="${PY + 104}">Control Plane</text>
    <text class="hb-role" x="${W / 2}" y="${PY + 126}">${esc(location.hostname)} · ${hs === 'ok' ? 'ทำงานปกติ' : 'ผิดปกติ'} · ดิสก์ ${Math.round(st.used_pct)}%</text></a>`;
  // dotted control lines from the top cards into the plane
  ctl(`M${tl[0] + CW + 8},${TOPY + CH - 30} H${PX + 60} V${PY - 6}`);
  ctl(`M${tr[0] - 8},${TOPY + CH - 30} H${PX + PW - 60} V${PY - 6}`);
  // rooms
  const gapY = R1 - 38;
  const slots = rooms.length === 1 ? [1] : rooms.length === 2 ? [0, 2] : [0, 1, 2, 0, 1, 2];
  const roomPos = rooms.map((g, i) => ({ g, x: COLX[slots[i] ?? i % 3], y: R1 + Math.floor(i / 3) * (CH + RG), col: slots[i] ?? i % 3, row: Math.floor(i / 3) }));
  if (more > 0) { const i = rooms.length; roomPos.push({ more: true, x: COLX[i % 3], y: R1 + Math.floor(i / 3) * (CH + RG), col: i % 3, row: Math.floor(i / 3) }); }
  const busyRooms = new Set(rooms.filter(g => g.hosts.some(h => tileState(h) === 'busy')).map(g => g.id));
  if (roomPos.length) {
    const firstRow = roomPos.filter(r => r.row === 0), minX = Math.min(...firstRow.map(r => r.x + CW / 2)), anyBusy = busyRooms.size > 0;
    line(`M${tr[0] + CW / 2},${TOPY + CH + 8} V${gapY} H${minX}`, ['ส่ง image ไปติดตั้ง', 'HTTPS :443'], tr[0] + CW / 2 + 16, (TOPY + CH + gapY) / 2 + 26, 'start', anyBusy);
    dots += dotEnd(tr[0] + CW / 2, TOPY + CH + 8);
  }
  roomPos.forEach(r => {
    const { x, y } = r;
    if (r.more) { cards += `<a href="#/hosts" class="hb-node"><rect class="hb-card ghost" x="${x}" y="${y}" width="${CW}" height="${CH}" rx="6"/><text class="hb-name" x="${x + CW / 2}" y="${y + CH / 2 + 6}">+${more} ห้อง</text><text class="hb-role" x="${x + CW / 2}" y="${y + CH / 2 + 28}">ดูทั้งหมดในหน้าเครื่อง</text></a>`; return; }
    const g = r.g, states = g.hosts.map(tileState), on = g.hosts.filter(h => h.online).length;
    const tone = states.some(s => s === 'bad' || s === 'stuck') ? 'bad' : states.some(s => s === 'warn') ? 'warn' : states.includes('busy') ? 'busy' : on ? '' : 'off';
    let inner = '';
    if (g.hosts.length <= 2) {
      g.hosts.forEach((h, k) => { const s = tileState(h), cx = g.hosts.length === 1 ? x + CW / 2 : x + CW / 4 + k * CW / 2, j = jobLive[h.id] || h.job || {};
        inner += half(cx, y, 'monitor', h.hostname || h.id, s === 'busy' ? `ติดตั้ง Windows ${j.progress || 0}%` : `${TILE_LABEL[s]}${hostIPs(h)[0] ? ' · ' + hostIPs(h)[0] : ''}`, 'st-' + s); });
      if (g.hosts.length === 2) inner += split(x, y);
    } else {
      inner += split(x, y) + `<text class="hb-big" x="${x + CW / 4}" y="${y + 86}">${g.hosts.length}</text><text class="hb-name" x="${x + CW / 4}" y="${y + 118}">เครื่อง Windows</text>
        <text class="hb-role" x="${x + CW / 4}" y="${y + 138}">ออนไลน์ ${on} / ${g.hosts.length}</text>`;
      const shown = g.hosts.slice(0, 24), per = 6;
      shown.forEach((h, k) => { const s = tileState(h);
        inner += `<circle class="hb-dot st-${s}" cx="${x + CW / 2 + 32 + (k % per) * 18}" cy="${y + 50 + Math.floor(k / per) * 18}" r="6"><title>${esc(h.hostname)} — ${esc(TILE_LABEL[s])}</title></circle>`; });
      if (g.hosts.length > 24) inner += `<text class="hb-role" x="${x + CW * 3 / 4}" y="${y + 138}">+${g.hosts.length - 24} เครื่อง</text>`;
    }
    cards += card(x, y, tone, inner, '#/hosts', g.name, `ออนไลน์ ${on} จาก ${g.hosts.length} เครื่อง`);
    labels += loc(x + CW / 2, y + CH + 28, 'ห้อง ' + g.name);
    // image path into the room (from the gap line for row 0, from the card above for row 1)
    const hot = busyRooms.has(g.id);
    if (r.row === 0) { line(`M${x + CW / 2},${gapY} V${y - 8}`, [], 0, 0, 'middle', hot); dots += dotEnd(x + CW / 2, y - 8); }
    else { line(`M${x + 44},${y - RG + 8} V${y - 8}`, [], 0, 0, 'middle', hot); dots += dotEnd(x + 44, y - RG + 8) + dotEnd(x + 44, y - 8); }
    // dotted control line to the plane (row 0 only: the plane sits right above it)
    if (r.row === 0) {
      const px = r.col === 0 ? PX + 70 : r.col === 2 ? PX + PW - 70 : W / 2;
      const sx = r.col === 1 ? x + CW / 2 + 60 : r.col === 0 ? x + CW - 40 : x + 40;
      ctl(r.col === 1 ? `M${sx},${y - 6} V${PY + PH + 6}` : `M${sx},${y - 6} V${gapY + 18} H${px} V${PY + PH + 6}`);
    }
  });
  if (!rooms.length) labels += `<text class="hb-name" x="${W / 2}" y="${R1 + 60}">ยังไม่มีเครื่อง — กด "เพิ่มเครื่อง" เพื่อเริ่ม</text>`;
  return `<svg class="tp-svg hb" viewBox="0 0 ${W} ${H}" role="img" aria-label="ผังระบบ SWIM">
    <defs><radialGradient id="hb-vig" cx="50%" cy="45%" r="75%"><stop offset="0" class="v0"/><stop offset="1" class="v1"/></radialGradient></defs>
    <rect class="hb-bg" width="${W}" height="${H}" fill="url(#hb-vig)"/>${lines}${cards}${dots}${labels}</svg>`;
}

// ───────── "ตาราง" view: dashed zones with a strict grid of equal tiles (one per machine) ─────────
const GRID_ICON = {
  server: '<rect x="4" y="3" width="24" height="26" rx="2"/><path d="M8 8h16M8 12h16M8 16h16M8 20h16"/><circle cx="10" cy="25" r="1"/><circle cx="16" cy="25" r="1"/><circle cx="22" cy="25" r="1"/>',
  db: '<ellipse cx="16" cy="7" rx="11" ry="4"/><path d="M5 7v18c0 2.2 4.9 4 11 4s11-1.8 11-4V7M5 13c0 2.2 4.9 4 11 4s11-1.8 11-4M5 19c0 2.2 4.9 4 11 4s11-1.8 11-4"/>',
  disk: '<ellipse cx="16" cy="11" rx="12" ry="5"/><path d="M4 11v9c0 2.8 5.4 5 12 5s12-2.2 12-5v-9"/>',
  monitor: '<rect x="3" y="5" width="26" height="17" rx="2"/><path d="M12 27h8M16 22v5"/>',
};
const gIcon = k => `<svg class="gt-ic" viewBox="0 0 32 32" aria-hidden="true">${GRID_ICON[k]}</svg>`;
function topoGrid(c) {
  const { live, gm, pub, pubCheck, capture, st, hs } = c, groups = topoGroups(c);
  const CHIP = { A: 'Agent ออนไลน์', I: 'กำลังติดตั้ง', '!': 'ต้องดูแล', O: 'ออฟไลน์', S: 'บริการบน server' };
  const chipsFor = s => s === 'busy' ? ['A', 'I'] : s === 'on' ? ['A'] : s === 'off' ? ['O'] : ['A', '!'];
  const tile = (icon, l1, l2, chips, href, cls = '', wide = false) => `<a class="gt-cell${wide ? ' wide' : ''}" href="${href}">
    <div class="gt-chips">${chips.map(k => `<span class="gt-chip${k === '!' ? ' warn' : k === 'I' ? ' busy' : k === 'O' ? ' off' : ''}" title="${esc(CHIP[k])}">${k}</span>`).join('')}</div>
    <div class="gt-tile ${cls}">${gIcon(icon)}<b>${esc(l1)}</b><span>${esc(l2)}</span></div></a>`;
  const serverZone = `<section class="gt-zone"><h4>SWIM Server <small>${esc(location.hostname)}</small></h4><div class="gt-grid">
    ${tile('server', 'Server', hs === 'ok' ? 'ทำงานปกติ' : 'ผิดปกติ', ['S'], '#/settings', hs === 'ok' ? '' : 'bad')}
    ${tile('db', 'Image store', pub ? `${pub.version}${pubCheck ? ' · ' + pubCheck : ''}` : 'ยังไม่มี', ['S'], '#/images')}
    ${tile('monitor', 'เครื่องต้นแบบ', gm ? `${gm.name} · ${capture ? 'Capture' : gm.state === 'running' ? 'เปิด' : 'ปิด'}` : 'ไม่มี', ['S'], '#/images', capture ? 'busy' : '')}
    ${tile('disk', 'Storage', `ใช้ ${Math.round(st.used_pct)}% · ว่าง ${fmtBytes(st.free_bytes)}`, ['S'], '#/storage', st.level === 'ok' ? '' : 'warn')}
  </div></section>`;
  const roomZones = groups.map(g => { const hs12 = g.hosts.slice(0, 12), extra = g.hosts.length - hs12.length, on = g.hosts.filter(h => h.online).length;
    return `<section class="gt-zone"><h4>ห้อง ${esc(g.name)} <small>ออนไลน์ ${on}/${g.hosts.length}</small></h4><div class="gt-grid">
      ${hs12.map(h => { const s = tileState(h), j = jobLive[h.id] || h.job || {};
        return tile('monitor', h.hostname || h.id, s === 'busy' ? `ติดตั้ง ${j.progress || 0}%` : hostIPs(h)[0] || TILE_LABEL[s], chipsFor(s), '#/hosts/' + encodeURIComponent(h.id), s); }).join('')}
      ${extra > 0 ? `<a class="gt-cell" href="#/hosts"><div class="gt-chips"></div><div class="gt-tile more"><b>+${extra}</b><span>เครื่อง</span></div></a>` : ''}</div></section>`; }).join('');
  return `<div class="gt"><div class="gt-legend">${Object.entries(CHIP).map(([k, v]) => `<span><i class="gt-chip${k === '!' ? ' warn' : k === 'I' ? ' busy' : k === 'O' ? ' off' : ''}">${k}</i>${esc(v)}</span>`).join('')}</div>
    <div class="gt-zones">${serverZone}${roomZones || '<section class="gt-zone"><h4>ยังไม่มีเครื่อง</h4></section>'}</div></div>`;
}

// ───────── Control Center: the home canvas (compact nodes, dashed links with a label pill) ─────────
const CC_ICON = {
  gm: '<rect x="3" y="5" width="18" height="12" rx="2"/><path d="M9 21h6M12 17v4"/>',
  image: '<ellipse cx="12" cy="6" rx="8" ry="3"/><path d="M4 6v12c0 1.7 3.6 3 8 3s8-1.3 8-3V6M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3"/>',
  server: '<rect x="4" y="3" width="16" height="7" rx="2"/><rect x="4" y="14" width="16" height="7" rx="2"/><path d="M8 6.5h.01M8 17.5h.01"/>',
  room: '<path d="M3 21V8l9-5 9 5v13"/><path d="M9 21v-6h6v6"/>',
  pc: '<rect x="3" y="4" width="18" height="12" rx="2"/><path d="M8 20h8M12 16v4"/>',
};
const ccIco = (k, x, y, tone = '') => `<rect class="cc-tile ${tone}" x="${x}" y="${y}" width="38" height="38" rx="9"/><g class="cc-ic" transform="translate(${x + 7},${y + 7})">${CC_ICON[k]}</g>`;
function ccGraph(c) {
  const { live, gm, pub, pubCheck, capture, st, hs } = c, groups = topoGroups(c);
  const NW = 236, NH = 60, COL = [40, 360, 680, 1010], W = 1290;
  const rooms = groups.slice(0, groups.length > 8 ? 7 : 8), more = groups.length - rooms.length;
  const nR = rooms.length + (more > 0 ? 1 : 0), RG = 26, H = Math.max(340, nR * (NH + RG) + 80), CY = H / 2;
  const tip = (t, s) => `data-tip="${esc(t)}" data-tip-sub="${esc(s || '')}"`;
  const trunc = (t, n) => t.length > n ? t.slice(0, n - 1) + '…' : t;
  let nodes = '', edges = '', pills = '';
  const node = (x, y, icon, name, sub, href, tone = '', t = name, ts = sub) => `<a href="${href}" class="cc-node" ${tip(t, ts)}>
    <rect class="cc-card ${tone}" x="${x}" y="${y}" width="${NW}" height="${NH}" rx="10"/>${ccIco(icon, x + 11, y + 11, tone)}
    <text class="cc-name" x="${x + 60}" y="${y + 27}">${esc(trunc(name, 22))}</text><text class="cc-sub" x="${x + 60}" y="${y + 45}">${esc(trunc(sub, 28))}</text></a>`;
  const vis = t => t.replace(/[\u0E31\u0E34-\u0E3A\u0E47-\u0E4E]/g, '').length;
  const pill = (x, y, tone, a, b) => { a = trText(a); b = trText(b); // measure the text that will be shown (English is wider)
    const la = vis(a) * 6.4, w = la + vis(b) * 6.4 + 40;
    return `<g class="cc-pill ${tone}" transform="translate(${(x - w / 2).toFixed(1)},${(y - 12).toFixed(1)})"><rect width="${w.toFixed(1)}" height="24" rx="12"/><circle cx="13" cy="12" r="3.5"/>
      <text x="22" y="16">${esc(a)}</text><path d="M${(22 + la + 6).toFixed(1)} 6 v12"/><text class="b" x="${(22 + la + 12).toFixed(1)}" y="16">${esc(b)}</text></g>`; };
  // two parallel dashed lines, like the reference
  const edge = (d, tone, fast) => { edges += `<path class="cc-edge ${tone}${fast ? ' fast' : ''}" d="${d}" transform="translate(0,-2.5)"/><path class="cc-edge ${tone}${fast ? ' fast' : ''}" d="${d}" transform="translate(0,2.5)"/>`; };
  const gmY = CY - NH - 40, imY = CY + 40;
  const gmTone = capture ? 'busy' : '';
  nodes += node(COL[0], gmY, 'gm', 'เครื่องต้นแบบ', gm ? `${gm.name}, ${capture ? `Capture ${Math.floor(capture.result?.progress?.percent ?? 0)}%` : gm.state === 'running' ? 'เปิดอยู่' : 'ปิดอยู่'}` : 'ยังไม่ได้สร้าง', '#/images', gmTone);
  nodes += node(COL[0], imY, 'image', pub ? pub.version : 'ยังไม่มี image', pub ? `พร้อมติดตั้ง${pubCheck ? ', ตรวจผ่าน ' + pubCheck : ''}` : 'Capture เพื่อสร้าง image แรก', '#/images', pub ? '' : 'warn', 'Image');
  edge(`M${COL[0] + NW / 2},${gmY + NH} V${imY}`, capture ? 'busy' : 'ok', !!capture);
  pills += pill(COL[0] + NW / 2, (gmY + NH + imY) / 2, capture ? 'busy' : 'ok', 'capture', capture ? 'กำลังทำ' : 'พร้อม');
  const sy = CY - NH / 2;
  nodes += node(COL[1] + 60, sy, 'server', 'SWIM Server', `${location.hostname}, ดิสก์ ${Math.round(st.used_pct)}%`, '#/storage', hs === 'ok' ? '' : 'bad');
  edge(`M${COL[0] + NW},${imY + NH / 2} C${COL[0] + NW + 60},${imY + NH / 2} ${COL[1] + 0},${sy + NH / 2} ${COL[1] + 60},${sy + NH / 2}`, 'ok', false);
  pills += pill((COL[0] + NW + COL[1] + 60) / 2, (imY + NH / 2 + sy + NH / 2) / 2 + 4, 'ok', 'เก็บ', 'image');
  // rooms on the right, the server fans out to each one
  const ry0 = CY - (nR * (NH + RG) - RG) / 2;
  rooms.forEach((g, i) => {
    const y = ry0 + i * (NH + RG), on = g.hosts.filter(h => h.online).length, states = g.hosts.map(tileState);
    const tone = states.some(s => s === 'bad' || s === 'stuck') ? 'bad' : states.some(s => s === 'warn') ? 'warn' : states.includes('busy') ? 'busy' : on ? 'ok' : 'off';
    nodes += node(COL[3], y, 'room', `ห้อง ${g.name}`, `${g.hosts.length} เครื่อง, ออนไลน์ ${on}`, '#/hosts', tone === 'ok' ? '' : tone, g.name, `ออนไลน์ ${on} จาก ${g.hosts.length} เครื่อง`);
    const x1 = COL[1] + 60 + NW, y1 = sy + NH / 2, x2 = COL[3], y2 = y + NH / 2, mx = (x1 + x2) / 2;
    edge(`M${x1},${y1} C${mx},${y1} ${mx},${y2} ${x2},${y2}`, tone, tone === 'busy');
    pills += pill(mx + (x2 - mx) * .45, y2 + (y1 - y2) * .12, tone, tone === 'busy' ? 'ติดตั้ง' : tone === 'warn' || tone === 'bad' ? 'ต้องดูแล' : 'Agent', `ออนไลน์ ${on}/${g.hosts.length}`);
  });
  if (more > 0) { const y = ry0 + rooms.length * (NH + RG); nodes += `<a href="#/hosts" class="cc-node"><rect class="cc-card ghost" x="${COL[3]}" y="${y}" width="${NW}" height="${NH}" rx="10"/><text class="cc-name" x="${COL[3] + NW / 2}" y="${y + 36}" text-anchor="middle">+${more} ห้อง</text></a>`; }
  return `<svg class="cc-svg" viewBox="0 0 ${W} ${H}" role="img" aria-label="ผังระบบ SWIM">${edges}${pills}${nodes}</svg>`;
}
// one square per machine, coloured by state; stays readable for 1000 machines (hover for details, click to open)
const SQ_ORDER = { stuck: 0, bad: 1, busy: 2, warn: 3, off: 4, on: 5 };
const machSq = h => { const st = tileState(h), x = installedOf(h);
  return `<a class="sq ${st}" href="#/hosts/${encodeURIComponent(h.id)}" data-tip="${esc(h.hostname || h.id)}" data-tip-sub="${esc([TILE_LABEL[st], hostIPs(h)[0], x ? (x.iso ? 'ISO ' : '') + x.version : ''].filter(Boolean).join(', '))}" aria-label="${esc(h.hostname || h.id)}: ${esc(TILE_LABEL[st])}"></a>`; };
const sortSq = hs => [...hs].sort((a, b) => (SQ_ORDER[tileState(a)] - SQ_ORDER[tileState(b)]) || String(a.hostname).localeCompare(String(b.hostname)));
const SQ_LEGEND = `<div class="sq-legend">${[['on', 'ออนไลน์'], ['busy', 'กำลังติดตั้ง'], ['warn', 'ต้องดูแล'], ['bad', 'มีปัญหา'], ['off', 'ออฟไลน์']].map(([k, l]) => `<span><i class="sq ${k}"></i>${l}</span>`).join('')}</div>`;
function ccMachines(c) {
  const groups = topoGroups(c);
  if (!groups.length) return ccEmpty();
  return `<div class="heat">${SQ_LEGEND}${groups.map(g => { const on = g.hosts.filter(h => h.online).length;
    return `<div class="heat-row"><a class="heat-name" href="#/hosts" data-site-filter-link="${esc(g.id || 'none')}"><b>${g.id ? 'ห้อง ' + esc(g.name) : esc(g.name)}</b><small>${on}/${g.hosts.length} ออนไลน์</small></a>
      <div class="sqs">${sortSq(g.hosts).map(machSq).join('')}</div></div>`; }).join('')}</div>`;
}
// rooms as cards: state bar, what is installed, and the machines as squares
function roomCards(c) {
  const groups = topoGroups(c);
  if (!groups.length) return ccEmpty();
  const SHOW = 120;
  const card = g => {
    const n = g.hosts.length, cnt = k => g.hosts.filter(h => tileState(h) === k).length;
    const parts = [['bad', cnt('bad') + cnt('stuck')], ['busy', cnt('busy')], ['warn', cnt('warn')], ['on', cnt('on')], ['off', cnt('off')]];
    const imgs = {}; for (const h of g.hosts) { const x = installedOf(h), k = x ? `${x.iso ? 'ISO ' : ''}${x.version}` : 'Windows เดิม'; imgs[k] = (imgs[k] || 0) + 1; }
    const top = Object.entries(imgs).sort((a, b) => b[1] - a[1]), issue = parts[0][1] + parts[2][1];
    return `<section class="rcard${issue ? ' has-issue' : ''}"><a class="rc-head" href="#/hosts" data-site-filter-link="${esc(g.id || 'none')}">
        <span class="rc-ic">${ic('layers')}</span><span><b>${g.id ? 'ห้อง ' + esc(g.name) : esc(g.name)}</b><small>${n} เครื่อง, ออนไลน์ ${g.hosts.filter(h => h.online).length}</small></span>
        ${issue ? `<span class="rc-flag">${ic('alert')}${issue}</span>` : ''}</a>
      <div class="rc-bar" aria-hidden="true">${parts.filter(([, v]) => v).map(([k, v]) => `<i class="${k}" style="flex:${v}"></i>`).join('')}</div>
      <div class="rc-counts">${parts.filter(([, v]) => v).map(([k, v]) => `<span><i class="sq ${k}"></i>${v} ${esc({ bad: 'มีปัญหา', busy: 'กำลังติดตั้ง', warn: 'ต้องดูแล', on: 'ปกติ', off: 'ออฟไลน์' }[k])}</span>`).join('')}</div>
      <div class="rc-imgs">${top.slice(0, 3).map(([k, v]) => `<span class="rc-img"><span class="ichip ${k.startsWith('ISO') ? 'iso' : k.startsWith('Windows') ? 'own' : 'gm'}">${k.startsWith('ISO') ? 'ISO' : k.startsWith('Windows') ? 'เดิม' : 'Image'}</span><b translate="no">${esc(k.replace(/^ISO /, ''))}</b><small>${v}</small></span>`).join('')}${top.length > 3 ? `<small class="mute">และอีก ${top.length - 3} แบบ</small>` : ''}</div>
      <div class="sqs">${sortSq(g.hosts).slice(0, SHOW).map(machSq).join('')}${n > SHOW ? `<a class="sq-more" href="#/hosts" data-site-filter-link="${esc(g.id || 'none')}">+${n - SHOW}</a>` : ''}</div></section>`;
  };
  return `<div class="rooms">${SQ_LEGEND}<div class="rcards">${groups.map(card).join('')}</div></div>`;
}
const ccEmpty = () => emptyCta('hosts', 'ยังไม่มีเครื่องในระบบ', 'ดาวน์โหลดตัวติดตั้ง SWIM Agent แล้วดับเบิลคลิกบนเครื่อง Windows<br>เครื่องจะขึ้นที่นี่ภายในไม่กี่วินาที', '#/enroll', 'เพิ่มเครื่อง', '#/enroll');
let ccView = ['rooms', 'machines'].includes(store.get('swim-cc-view')) ? store.get('swim-cc-view') : 'graph';
function controlCenter(c) {
  topoCtx = c;
  const body = ccView === 'rooms' ? roomCards(c) : ccView === 'machines' ? ccMachines(c) : c.live.length || c.gm ? ccGraph(c) : ccEmpty();
  return `<section class="cc${topoEnter ? ' enter' : ''}" id="topo">
    <div class="cc-bar"><div class="cc-seg" role="tablist">${[['graph', 'layers', 'ผังระบบ'], ['rooms', 'map', 'ห้อง'], ['machines', 'hosts', 'เครื่อง']].map(([k, i, l]) =>
      `<button role="tab" data-ccview="${k}" class="${ccView === k ? 'on' : ''}" aria-selected="${ccView === k}">${ic(i)}${l}</button>`).join('')}</div>
      ${ccLive()}</div>
    <div class="cc-body">${body}</div></section>`;
}
function emptyCta(icon, title, text, href, btn, learn) {
  return `<div class="empty2"><span class="e2-tile">${ic(icon)}</span><h3>${esc(title)}</h3><p>${text}</p>
    ${href ? `<a class="btn primary" href="${href}">${ic('plus')}${esc(btn)}</a>` : ''}${learn ? `<a class="e2-learn" href="${learn}">เรียนรู้เพิ่มเติม</a>` : ''}</div>`;
}

function topoCard(c) {
  topoCtx = c;
  const hubV = topoView !== 'grid';
  return `<section class="card topo${hubV ? ' is-hub' : ' is-grid'}${topoEnter ? ' enter' : ''}" id="topo">
    <div class="card-head"><div><h2>ผังระบบ</h2><p>SWIM server เป็นศูนย์กลาง — สร้าง image, เก็บ image และส่งไปติดตั้งในแต่ละห้อง · ชี้เพื่อดูรายละเอียด คลิกเพื่อเปิด</p></div><div class="spacer"></div>
      <div class="seg"><button data-tview="hub" class="${hubV ? 'on' : ''}" aria-pressed="${hubV}">ผังระบบ</button><button data-tview="grid" class="${hubV ? '' : 'on'}" aria-pressed="${!hubV}">ตาราง</button></div></div>
    <div class="tp-stage">${hubV ? topoHub(c) : topoGrid(c)}</div>
    ${hubV ? `<div class="tp-legend"><span><i class="ln acc"></i>เส้นทางของ image</span><span><i class="ln dot"></i>ช่องทางควบคุม (agent)</span><span class="mute">ขอบการ์ดสีส้มอ่อนหรือแดง = มีเครื่องต้องดูแล</span></div>` : ''}
  </section>`;
}
function wireTopo() {
  const t = $('#topo'); if (!t) return;
  if (topoEnter) setTimeout(() => t.classList.remove('enter'), 1400);
  topoEnter = false;
  const svg = t.querySelector('svg.tp-svg'); if (svg && topoPaused && svg.pauseAnimations) svg.pauseAnimations();
  wireTips(t);
}
function redrawTopo() { const t = $('#topo'); if (!t || !topoCtx) return; t.outerHTML = topoCard(topoCtx); wireTopo(); }

async function pageHome() {
  const since7 = new Date(Date.now() - 7 * 864e5); since7.setHours(0, 0, 0, 0);
  const [cap, images, hosts, tasks, sites, vms, chs, audit, detail, online24, enrollInfo, occ] = await Promise.all([
    api('/v1/capacity/server'), api('/v1/images'), api('/v1/hosts'), api('/v1/tasks'), api('/v1/sites'), api('/v1/builder/vms').catch(() => []), api('/v1/channels').catch(() => []),
    api('/v1/audit?limit=5000&from=' + encodeURIComponent(since7.toISOString())).catch(() => []),
    api('/v1/capacity/server/detail').catch(() => null), api('/v1/stats/online?hours=24').catch(() => null), api('/v1/enroll/info').catch(() => ({})),
    api(`/v1/schedules/occurrences?from=${encodeURIComponent(new Date(Date.now() - 864e5).toISOString())}&to=${encodeURIComponent(new Date(Date.now() + 7 * 864e5).toISOString())}`).catch(() => [])]);
  latestAgent = enrollInfo.agent?.version || latestAgent;
  lastTasks = tasks; hostList = hosts; siteCache = sites;
  for (const h of hosts) if (h.job) jobLive[h.id] = h.job;
  const live = hosts.filter(h => !h.retired_at), online = live.filter(h => h.online).length;
  const pub = images.find(i => i.status === 'published'), pending = images.filter(i => i.status === 'pending');
  let pubCheck = '';
  if (pub) try { const d = await api('/v1/images/' + encodeURIComponent(pub.id)); const c = d.checks || []; if (c.length) pubCheck = `${c.filter(x => x.passed).length}/${c.length}`; } catch (e) {}
  const capture = tasks.find(t => (t.status === 'running' || t.status === 'queued') && t.kind === 'capture_image');
  const state = h => tileState(h);
  const busy = live.filter(h => state(h) === 'busy'), stuck = live.filter(h => state(h) === 'stuck');
  const care = live.filter(h => ['warn', 'bad'].includes(state(h)) && !h.removing_at);
  const st = cap.storage, hs = health?.status || 'down', gm = vms[0];
  const day0 = Date.now() - 864e5, a24 = audit.filter(a => Date.parse(a.at) >= day0);
  const isCmd = a => a.action.startsWith('command.') && a.action !== 'command.result';
  const cmd24 = a24.filter(isCmd).length, cmdPrev = audit.filter(a => isCmd(a) && Date.parse(a.at) < day0 && Date.parse(a.at) >= day0 - 864e5).length;

  // what needs doing (one action each)
  const now = [];
  const ev = (tone, title, body, href, btn) => now.push(`<li class="todo-i ${tone}"><span class="td-dot"></span><div><b>${title}</b><p>${body}</p></div>${href ? `<a class="btn sm" href="${href}">${btn}</a>` : ''}</li>`);
  for (const h of stuck) ev('bad', `${esc(h.hostname)} ติดตั้งไม่สำเร็จ`, 'เครื่องรออยู่ใน WinPE — กด "ลองใหม่" ในหน้าเครื่อง', '#/hosts/' + encodeURIComponent(h.id), 'แก้ไข');
  for (const h of busy) { const j = jobLive[h.id] || h.job || {}; ev('accent', `${esc(h.hostname)} กำลังติดตั้ง Windows`, `${esc(PHASE_TH[j.phase] || j.phase || '')} · ${j.progress || 0}% · ไม่ต้องทำอะไร`, '#/hosts/' + encodeURIComponent(h.id), 'ดู'); }
  if (capture) ev('accent', 'กำลังสร้าง image เวอร์ชันใหม่', esc(CAP_STAGE[capture.result?.progress?.stage] || 'กำลังเริ่ม…'), '#/images', 'ดู');
  for (const i of pending) ev('warn', `image ${esc(i.version)} รออนุมัติ`, 'ดูผลตรวจอัตโนมัติแล้วกดอนุมัติ', '#/images', 'ตรวจและอนุมัติ');
  for (const h of care.slice(0, 4)) ev('warn', esc(h.hostname), esc(hostAlerts(h).filter(a => a[0] === 'bad' || a[0] === 'warn').map(a => a[1]).join(', ') || 'รอตรวจสอบ'), '#/hosts/' + encodeURIComponent(h.id), 'เปิด');
  if (st.level !== 'ok') ev(st.level === 'block' ? 'bad' : 'warn', 'พื้นที่ server ใกล้เต็ม', `ใช้ไป ${st.used_pct.toFixed(0)}%`, '#/storage', 'จัดการ');
  if (!live.length) ev('accent', 'ยังไม่มีเครื่องในระบบ', 'ดาวน์โหลดตัวติดตั้งแล้วดับเบิลคลิกบนเครื่อง Windows', '#/enroll', 'เพิ่มเครื่อง');
  const issues = stuck.length + pending.length + care.length + (st.level !== 'ok' ? 1 : 0);

  // KPI cards
  const kpi = (key, label, icon, value, sub, extra = '', tone = '') => `<a class="kpi2 ${tone}" ${key.startsWith('#') ? `href="${key}"` : `href="#/home" data-stat="${key}"`}>
    <div class="k2-top"><span class="k2-ico">${ic(icon)}</span><span class="k2-l">${label}</span></div>
    <div class="k2-v">${value}</div><div class="k2-s">${sub}</div>${extra}</a>`;
  const onPct = pct0(online, live.length);
  const spark = Array.isArray(online24) && online24.length > 1 && new Set(online24.map(p => p.online)).size > 1 ? sparkline(online24.map(p => p.online)) : '';
  const cell = (key, label, value, sub, tone = '') => `<a class="ks-cell ${tone}" ${key.startsWith('#') ? `href="${key}"` : `href="#/home" data-stat="${key}"`}>
    <span class="ks-l">${label}</span><b class="ks-v">${value}</b><span class="ks-s">${sub}</span></a>`;
  const nCare = care.length + stuck.length;
  const kpis = `<nav class="kstrip" aria-label="ตัวเลขสำคัญ">
    ${cell('online', 'เครื่องออนไลน์', `${online}<small> จาก ${live.length}</small>`, live.length ? `${onPct}% ของทั้งหมด` : 'ยังไม่มีเครื่อง', live.length && onPct < 50 ? 'bad' : '')}
    ${cell('busy', 'กำลังติดตั้ง Windows', busy.length, busy.length ? esc(busy.map(h => h.hostname).join(', ')) : 'ไม่มีงานค้าง', busy.length ? 'accent' : '')}
    ${cell('care', 'ต้องดูแล', nCare, nCare ? 'กดเพื่อดูสาเหตุ' : 'ทุกเครื่องปกติ', nCare ? 'warn' : '')}
    ${cell(pub ? 'image:' + pub.id : '#/images', 'Image ที่ใช้ติดตั้ง', pub ? esc(pub.version) : 'ยังไม่มี', pub ? (pubCheck ? `ตรวจผ่าน ${pubCheck}` : 'พร้อมติดตั้ง') : 'สร้างจากเครื่องต้นแบบ', 'text')}
    ${cell('#/storage', 'พื้นที่ server', `${Math.round(st.used_pct)}<small>%</small>`, `ว่าง ${fmtBytes(st.free_bytes)}`, st.level === 'ok' ? '' : 'warn')}
    ${cell('#/logs', 'คำสั่งใน 24 ชั่วโมง', cmd24, cmd24 === cmdPrev ? 'เท่ากับเมื่อวาน' : `${cmd24 > cmdPrev ? 'มากกว่า' : 'น้อยกว่า'}เมื่อวาน ${Math.abs(cmd24 - cmdPrev)}`)}
  </nav>`;


  imageCache = images; channelCache = chs; markDups(hosts);
  // what is running right now: installs in progress, schedule runs, commands waiting on machines
  const runs = occ.filter(o => o.status === 'running'), openCmds = live.reduce((a, h) => a + (h.open_commands || 0), 0);
  const nowRows = [...busy.slice(0, 6).map(h => { const j = jobLive[h.id] || h.job || {};
      return `<a class="now-row" href="#/hosts/${encodeURIComponent(h.id)}"><i class="sq busy"></i><span><b translate="no">${esc(h.hostname || h.id)}</b>${dupTag(h)}<small>${esc(PHASE_TH[j.phase] || 'กำลังติดตั้ง Windows')}</small></span>
        <span class="now-p"><span class="rs-bar sm"><i style="width:${Math.max(3, j.progress || 0)}%"></i></span><small>${j.progress || 0}%</small></span></a>`; }),
    ...runs.map(o => `<a class="now-row" href="#/schedule"><i class="sq busy"></i><span><b translate="no">${esc(o.title || '')}</b><small>ตารางงาน, ${esc(o.target_label || '')}</small></span>
        <span class="now-p"><span class="rs-bar sm"><i style="width:${Math.max(3, o.result?.progress || 0)}%"></i></span><small>${Math.round(o.result?.progress || 0)}%</small></span></a>`)];
  const nowCard = card('กำลังทำอยู่ตอนนี้', busy.length || runs.length ? `ติดตั้ง Windows ${busy.length} เครื่อง${runs.length ? `, งานตามตาราง ${runs.length} งาน` : ''}` : 'ไม่มีงานที่กำลังทำ',
    nowRows.length ? `<div class="now-list">${nowRows.join('')}${busy.length > 6 ? `<a class="now-more" href="#/hosts">และอีก ${busy.length - 6} เครื่อง</a>` : ''}</div>`
      : `<div class="empty">${ic('check')}<b>ไม่มีงานค้าง</b><div>${openCmds ? `มีคำสั่งรอเครื่องรับ ${openCmds} คำสั่ง` : 'ไม่มีการติดตั้งหรือคำสั่งที่รออยู่'}</div></div>`, '', true);
  // what is coming up in the next 7 days
  const next = occ.filter(o => o.status === 'scheduled' && Date.parse(o.at) > Date.now()).sort((a, b) => Date.parse(a.at) - Date.parse(b.at));
  const nextCard = card('งานถัดไป', next.length ? `${next.length} งานใน 7 วัน` : 'ไม่มีงานที่ตั้งเวลาไว้ใน 7 วัน', next.length ? `<ul class="next-list">${next.slice(0, 6).map(o => { const k = SK[o.kind] || [o.kind, 'clock', 'k-other'], at = new Date(o.at);
      return `<li><a href="#/schedule" class="${k[2]}"><span class="nx-when"><b>${esc(at.toLocaleDateString(LOC(), { weekday: 'short', day: 'numeric', month: 'short' }))}</b><small>${hhmm(at)}</small></span>
        <span class="nx-what"><b translate="no">${esc(o.title || k[0])}</b><small>${esc(k[0])}, ${esc(o.target_label || '')}</small></span></a></li>`; }).join('')}</ul>`
    : `<div class="empty">${ic('calendar')}<b>ยังไม่มีงานล่วงหน้า</b><div><a href="#/schedule">ตั้งเวลา</a> เช่น ปลุกเครื่องก่อนเรียน หรือล้างเครื่องตอนกลางคืน</div></div>`,
    '<a class="btn sm ghost" href="#/schedule">ตารางงาน</a>', true);

  // rooms: one clickable row per room with what matters (online, care items, installed Windows, next job)
  const groups = [...sites.map(s => ({ id: s.id, name: s.name, hosts: live.filter(h => h.site_id === s.id) })), { id: '', name: 'ไม่ระบุห้อง', hosts: live.filter(h => !h.site_id) }]
    .filter(g => g.hosts.length || g.id).sort((a, b) => b.hosts.length - a.hosts.length);
  const roomRow = g => {
    const n = g.hosts.length, on = g.hosts.filter(h => h.online).length;
    const careTxt = {}; for (const h of g.hosts) for (const [l, t] of hostAlerts(h)) if (l === 'warn' || l === 'bad') careTxt[t] = (careTxt[t] || 0) + 1;
    const careTop = Object.entries(careTxt).sort((a, b) => b[1] - a[1]), careN = g.hosts.filter(h => ['warn', 'bad', 'stuck'].includes(state(h))).length;
    const imgs = {}; for (const h of g.hosts) { const x = installedOf(h), k = x ? `${x.iso ? 'ISO ' : ''}${x.version}` : 'Windows เดิม'; imgs[k] = (imgs[k] || 0) + 1; }
    const imgTop = Object.entries(imgs).sort((a, b) => b[1] - a[1]);
    const nx = next.find(o => g.id && String(o.target_label || '').includes(g.name));
    return `<a class="room-row" href="#/hosts" data-site-filter-link="${esc(g.id || 'none')}">
      <span class="rr-name"><b>${g.id ? esc(g.name) : 'ไม่ระบุห้อง'}</b><small>${n} เครื่อง</small></span>
      <span class="rr-on">${n ? `<span class="k2-bar ok"><i style="width:${pct0(on, n)}%"></i></span><small>ออนไลน์ ${on}/${n}</small>` : '<small class="mute">ยังไม่มีเครื่อง</small>'}</span>
      <span class="rr-care">${careN ? `<span class="badge warn">${careN}</span><small title="${esc(careTop.map(([t, c]) => `${t} ${c}`).join(', '))}">${esc(careTop.slice(0, 2).map(([t, c]) => `${t} ${c}`).join(', '))}</small>` : n ? '<small class="ok-t">ปกติ</small>' : ''}</span>
      <span class="rr-img">${imgTop.length ? `<span class="ichip sm ${imgTop[0][0].startsWith('ISO') ? 'iso' : imgTop[0][0].startsWith('Windows') ? 'own' : 'gm'}">${imgTop[0][0].startsWith('ISO') ? 'ISO' : imgTop[0][0].startsWith('Windows') ? 'เดิม' : 'Image'}</span><small translate="no">${esc(imgTop[0][0].replace(/^ISO /, ''))}${imgTop.length > 1 ? ` +${imgTop.length - 1}` : ''}</small>` : ''}</span>
      <span class="rr-next">${nx ? `<small>${esc(new Date(nx.at).toLocaleDateString(LOC(), { weekday: 'short', day: 'numeric' }))} ${hhmm(new Date(nx.at))}</small><small class="mute" translate="no">${esc(nx.title || '')}</small>` : '<small class="mute">ไม่มีงาน</small>'}</span>
      <span class="rr-go">${ic('right')}</span></a>`;
  };
  const siteTable = card('ห้อง', `${groups.length} ห้อง กดเพื่อดูเครื่องในห้องนั้น`, groups.length ? `<div class="room-head"><span>ห้อง</span><span>ออนไลน์</span><span>ต้องดูแล</span><span>Windows ที่ใช้มากสุด</span><span>งานถัดไป</span><span></span></div>
    <div class="room-rows">${groups.map(roomRow).join('')}</div>` : empty('layers', 'ยังไม่มีห้อง'), '<a class="btn sm ghost" href="#/hosts">ดูเครื่องทั้งหมด</a>', true);
  const todoCard = card('ต้องดำเนินการ', issues ? `${now.length} รายการ` : 'ไม่มีเรื่องค้าง', now.length ? `<ul class="todo">${now.join('')}</ul>`
    : `<div class="empty">${ic('check')}<b>ทุกอย่างปกติ</b><div>ระบบจะแจ้งที่นี่เมื่อมีเรื่องที่ต้องทำ</div></div>`, '', true);

  // activity timeline + server health
  const timeline = card('กิจกรรมล่าสุด', 'ใครทำอะไร กับเครื่องไหน', a24.length || audit.length ? `<ul class="tl2">${audit.slice(0, 8).map(a => `<li class="${a.action.startsWith('job.') ? 'job' : isCmd(a) ? 'cmd' : a.result === 'failed' ? 'bad' : ''}">
      <span class="tl2-dot"></span><div><b>${esc(actorName(a.actor))}</b> ${esc(actionTh(a.action))}${a.object_type === 'host' ? ` <span class="mute">· ${esc(a.object_id.split(',').map(id => hostList.find(h => h.id === id)?.hostname || '').filter(Boolean).join(', '))}</span>` : ''}</div><time>${when(a.at)}</time></li>`).join('')}</ul>`
    : empty('audit', 'ยังไม่มีกิจกรรม'), '<a class="btn sm ghost" href="#/logs">ดูทั้งหมด</a>', true);
  const checks = Object.entries(health?.checks || {}).map(([k, v]) => { const ok = typeof v === 'string' ? /^ok|compiled|running/i.test(v) : v.level === 'ok';
    return `<li><span class="hc-dot ${ok ? 'ok' : 'bad'}"></span>${esc({ database: 'ฐานข้อมูล', libvirt: 'ระบบ VM (libvirt)', storage: 'พื้นที่เก็บข้อมูล' }[k] || k)}<span class="mute">${ok ? 'ปกติ' : 'ผิดปกติ'}</span></li>`; }).join('');
  const cats = detail ? [...detail.categories.filter(c => c.bytes).sort((a, b) => b.bytes - a.bytes), { key: 'other', bytes: detail.other_bytes }] : [];
  const CAT_COL = ['var(--accent)', '#7a8cf0', '#14b8a6', '#f59e0b', '#a78bfa', '#94a3b8', '#64748b', '#cbd5e1', '#475467'];
  const agents = Object.entries(live.reduce((m, h) => (m[h.agent_version || '—'] = (m[h.agent_version || '—'] || 0) + 1, m), {})).sort((a, b) => b[1] - a[1]);
  const healthCard = card('สุขภาพ server', `v${esc(health?.version || '')} · เปิดมา ${upFor(health?.uptime_s || 0)}`, `<div class="hc">
    <ul class="hc-list">${checks}</ul>
    ${detail ? `<div class="hc-sec"><div class="hc-h"><span>การใช้ดิสก์</span><a href="#/storage">รายละเอียด</a></div>
      <div class="stack">${cats.map((c, i) => `<i style="width:${(c.bytes / detail.storage.total_bytes * 100).toFixed(2)}%;background:${CAT_COL[i % CAT_COL.length]}" data-tip="${esc(STORE_CAT[c.key]?.[0] || 'ระบบอื่น')}" data-tip-sub="${fmtBytes(c.bytes)}"></i>`).join('')}</div>
      <div class="stack-l">${cats.slice(0, 5).map((c, i) => `<span><i style="background:${CAT_COL[i % CAT_COL.length]}"></i>${esc(STORE_CAT[c.key]?.[0] || 'ระบบอื่น')} ${fmtBytes(c.bytes)}</span>`).join('')}</div></div>` : ''}
    <div class="hc-sec"><div class="hc-h"><span>เวอร์ชัน Agent</span><span class="mute">ล่าสุด ${esc(latestAgent || '—')}</span></div>
      ${agents.map(([v, n]) => `<div class="ag-row"><span>${esc(v)}</span><div class="k2-bar ${latestAgent && v !== latestAgent ? 'warn' : 'ok'}"><i style="width:${pct0(n, live.length)}%"></i></div><b>${n}</b></div>`).join('') || '<p class="mute">ยังไม่มีเครื่อง</p>'}</div>
  </div>`, '', true);

  const sentence = hs !== 'ok' ? 'Server ทำงานผิดปกติ ตรวจสอบที่หน้าตั้งค่า'
    : issues ? `มี ${issues} เรื่องที่ต้องดู เครื่องออนไลน์ ${online} จาก ${live.length} เครื่อง`
    : busy.length ? `กำลังติดตั้ง Windows ${busy.length} เครื่อง เครื่องออนไลน์ ${online} จาก ${live.length} เครื่อง`
    : live.length ? `ระบบทำงานปกติ เครื่องออนไลน์ ${online} จาก ${live.length} เครื่อง${pub ? ` และ image ${esc(pub.version)} พร้อมติดตั้ง` : ''}`
    : 'ยังไม่มีเครื่องในระบบ เริ่มจากกด "เพิ่มเครื่อง" แล้วติดตั้ง Agent บนเครื่อง Windows';
  return `<div class="dash">
  <header class="page-h2">
    <div><h2>ภาพรวมระบบ</h2><p class="health ${hs !== 'ok' ? 'bad' : issues ? 'warn' : 'ok'}"><i></i>${sentence}</p></div>
    <div class="page-h2-a"><a class="btn" href="#/enroll">${ic('plus')}เพิ่มเครื่อง</a><a class="btn primary" href="#/images">${ic('builder')}สร้าง image</a></div>
  </header>
  ${controlCenter({ live, sites, gm, pub, pubCheck, capture, st, hs, images, chs })}
  ${kpis}
  <div class="dgrid">
    <div class="c7">${todoCard}</div><div class="c5">${nowCard}</div>
    <div class="c7">${siteTable}</div><div class="c5">${nextCard}</div>
    <div class="c7">${timeline}</div><div class="c5">${healthCard}</div>
  </div>
</div>`;
}
ROUTES.home.after = () => {
  liveEnter = false;
  document.querySelectorAll('[data-stat]').forEach(a => a.onclick = e => { e.preventDefault(); const k = a.dataset.stat; k.startsWith('image:') ? openImage(k.slice(6)) : openStat(k); });
  wireTips($('.dash'));
  wireTopo();
};

// ───────── shared progress: bar + elapsed + time left, kept live by a 1 s ticker ─────────
const fmtClock = sec => { sec = Math.max(0, Math.floor(sec)); const h = Math.floor(sec / 3600), m = Math.floor(sec % 3600 / 60), x = sec % 60;
  return (h ? h + ':' + String(m).padStart(2, '0') : m) + ':' + String(x).padStart(2, '0'); };
const fmtLeft = sec => sec == null || !isFinite(sec) ? '' : sec < 60 ? 'ไม่ถึง 1 นาที' : sec < 5400 ? `ประมาณ ${Math.round(sec / 60)} นาที` : `ประมาณ ${(sec / 3600).toFixed(1)} ชั่วโมง`;
// expected finish as a local clock time; "late" once it has passed and the work still runs
const finishText = at => {
  if (!at) return '';
  if (at <= Date.now()) return '<span class="late">ช้ากว่าที่ประเมิน — กำลังทำงาน</span>';
  const d = new Date(at), t = d.toLocaleTimeString(LOC(), { hour: '2-digit', minute: '2-digit' });
  const days = Math.round((new Date(d.toDateString()) - new Date(new Date().toDateString())) / 864e5);
  return `คาดว่าเสร็จประมาณ <b>${t} น.</b>${days === 1 ? ' (พรุ่งนี้)' : days > 1 ? ` (${d.toLocaleDateString(LOC(), { day: 'numeric', month: 'short' })})` : ''}`;
};
// percent null = still working, no measurable progress yet (moving stripe)
function progressBox({ percent = null, title = '', detail = '', since = Date.now(), finishAt = null, updatedAt = null, staleAfter = 120, logId = '', note = '' }) {
  const age = updatedAt ? (Date.now() - Date.parse(updatedAt)) / 1000 : 0, stale = updatedAt && age > staleAfter;
  return `<div class="pbox${stale ? ' stale' : ''}">
    <div class="pbox-top"><span class="spin" aria-hidden="true"></span><b>${title}</b>${percent != null ? `<span class="pct">${Math.floor(percent)}%</span>` : ''}
      <span class="spacer"></span>${logId ? `<button class="btn sm ghost" data-log="${esc(logId)}">${ic('log')}ดู Log</button>` : ''}</div>
    <div class="meter${percent == null ? ' indet' : ''}"><i style="width:${percent == null ? 35 : Math.max(2, Math.min(100, percent))}%"></i></div>
    ${detail ? `<div class="pbox-detail">${detail}</div>` : ''}
    <div class="pbox-time">${finishAt ? `<span class="pbox-eta" data-finish-at="${Math.round(finishAt)}">${finishText(finishAt)}</span> · `
      : percent != null ? '<span class="pbox-eta">กำลังประเมินเวลา…</span> · ' : ''}ผ่านไป <span data-elapsed-since="${Math.round(since)}">${fmtClock((Date.now() - since) / 1000)}</span>${note ? ' · ' + note : ''}</div>
    ${stale ? `<div class="pbox-stale">${ic('alert')}<span>ไม่มีความคืบหน้าใหม่มา ${fmtLeft(age).replace('ประมาณ ', '')} — งานอาจค้าง ลองดู Log</span></div>` : ''}
  </div>`;
}
const live1s = setInterval(() => {
  const now = Date.now();
  document.querySelectorAll('[data-elapsed-since]').forEach(el => { el.textContent = fmtClock((now - Number(el.dataset.elapsedSince)) / 1000); });
  document.querySelectorAll('[data-finish-at]').forEach(el => { const at = Number(el.dataset.finishAt); if (at <= now && !el.dataset.late) { el.dataset.late = '1'; el.innerHTML = finishText(at); } });
}, 1000);
const CAP_STAGE = { preflight: 'ตรวจพื้นที่ก่อนเริ่ม', sysprep: 'sysprep ในสำเนาชั่วคราว', decompress: 'คลายไฟล์ที่ Windows บีบอัด', test: 'ตรวจอัตโนมัติ 6 ข้อ',
  capture: 'จับภาพเป็นไฟล์ WIM', hash: 'คำนวณ SHA-256' };
// a builder task (capture / snapshot / revert) as a progress box
function taskProgress(t) {
  const pr = t.result && t.result.progress;
  const start = Date.parse(t.started_at || t.created_at);
  if (t.status === 'queued') return progressBox({ title: `${esc(TASK_KIND[t.kind] || t.kind)} — รอคิว`, detail: 'จะเริ่มเมื่องานก่อนหน้าเสร็จ', since: Date.parse(t.created_at), logId: t.id });
  if (!pr) return progressBox({ title: esc(TASK_KIND[t.kind] || t.kind), detail: t.kind === 'capture_image' || t.kind === 'iso_import' ? 'กำลังทำงาน — ยังไม่มีตัวเลขความคืบหน้าจาก server' : 'กำลังทำ — ปกติใช้เวลาไม่เกิน 1–2 นาที', since: start, logId: t.id });
  const upd = Date.parse(pr.updated_at) || Date.now();
  const stages = t.kind === 'iso_import' ? ISO_STAGE : CAP_STAGE;
  return progressBox({ percent: pr.percent, title: `${esc(TASK_KIND[t.kind] || t.kind)} · ${esc(stages[pr.stage] || pr.stage || '')}`, detail: esc(pr.message || ''),
    since: pr.started_at ? Date.parse(pr.started_at) : pr.elapsed_s != null ? upd - pr.elapsed_s * 1000 : start,
    finishAt: pr.eta_at ? Date.parse(pr.eta_at) : pr.eta_s != null ? upd + pr.eta_s * 1000 : null, updatedAt: pr.updated_at, logId: t.id });
}

// ───────── install Windows from an ISO on the server (ISOs are copied in over ssh) ─────────
const ISO_STAGE = { preflight: 'ตรวจพื้นที่และแผ่น ISO', capture: 'คัดลอก Windows รุ่นที่เลือกจากแผ่น', hash: 'คำนวณ SHA-256' };
const isIsoChannel = c => /^iso-/.test(c?.name || '');
function isoSection(isos, tasks) {
  const running = tasks.filter(t => ['iso_inspect', 'iso_import'].includes(t.kind) && ['running', 'queued'].includes(t.status));
  const lastFail = tasks.find(t => ['iso_inspect', 'iso_import'].includes(t.kind) && t.status === 'failed' && Date.now() - Date.parse(t.finished_at || t.created_at) < 3600e3);
  const busyFor = name => running.find(t => t.payload?.iso === name);
  const gb = b => b ? fmtBytes(b) : '—';
  const rows = isos.map(f => {
    const t = busyFor(f.name);
    const eds = f.editions || [];
    return `<div class="iso-row">
      <div class="iso-head">${ic('hdd')}<div class="grow"><b>${esc(f.name)}</b><span class="mute">${gb(f.size_bytes)} · วางไว้ ${when(f.modified)}${eds.length ? ` · ${eds.length} รุ่น` : ''}</span></div>
        ${t ? '' : `<button class="btn sm${eds.length ? ' ghost' : ' primary'}" data-iso-inspect="${esc(f.name)}" ${builderBusy ? 'disabled title="มีงานอื่นทำอยู่ — รอให้เสร็จก่อน"' : ''}>${ic('search')}${eds.length ? 'อ่านใหม่' : 'อ่านรุ่น Windows ในแผ่น'}</button>`}</div>
      ${t ? `<div class="iso-prog">${taskProgress(t)}</div>` : ''}
      ${eds.length ? table(eds, [
        { h: 'รุ่น Windows', v: e => `<b style="font-weight:600">${esc(e.name)}</b>${e.description && e.description !== e.name ? `<div class="mute" style="font-size:12px">${esc(e.description)}</div>` : ''}` },
        { h: 'เวอร์ชัน', v: e => esc(e.version || '—'), cls: 'nowrap' },
        { h: 'สถาปัตยกรรม', v: e => esc(e.arch || '—'), cls: 'nowrap' },
        { h: 'ภาษา', v: e => esc((e.languages || []).join(', ') || '—') },
        { h: 'ขนาดเมื่อติดตั้ง', v: e => gb(e.expanded_bytes), cls: 'nowrap' },
        { h: '', cls: 'actions', v: e => `<button class="btn sm" data-iso-import="${esc(f.name)}" data-index="${e.index}" data-edition="${esc(e.name)}" data-ver="${esc(e.version || '')}"
            ${t || builderBusy ? 'disabled title="มีงานอื่นทำอยู่ — รอให้เสร็จก่อน"' : ''}>${ic('plus')}นำเข้าเป็น image</button>` },
      ]) : ''}</div>`;
  }).join('');
  const emptyHelp = `<div class="iso-empty">${ic('info')}<div><b>ยังไม่มีไฟล์ ISO บน server</b>
    <p>คัดลอกแผ่น Windows (.iso) จากเครื่องของคุณไปที่โฟลเดอร์ <code class="k">/srv/swim/vms/iso/</code> บน server ผ่าน SSH แล้วกดรีเฟรช เช่น</p>
    <pre class="code">scp Win11_24H2_Thai_x64.iso ubuntu@${esc(location.hostname)}:/srv/swim/vms/iso/</pre></div></div>`;
  return card('ติดตั้งจากแผ่น ISO', 'ใช้ Windows ต้นฉบับจากแผ่น ISO แทนเครื่องต้นแบบ — อ่านรุ่นในแผ่น → นำเข้าเป็น image → อนุมัติ → เลือกติดตั้งในหน้าเครื่องได้เหมือน image ปกติ',
    `${lastFail ? `<div class="callout bad" style="margin:14px 18px 0">${ic('alert')}<div><b>${esc(TASK_KIND[lastFail.kind])} ไม่สำเร็จ</b> — ${esc(lastFail.error || lastFail.result?.error || 'ดู log')}
      <button class="btn sm ghost" data-log="${esc(lastFail.id)}">${ic('log')}ดู log</button></div></div>` : ''}
    ${rows || emptyHelp}`, '', true);
}
function askIsoImport(b) {
  const name = b.dataset.isoImport, edition = b.dataset.edition;
  modal({ title: 'นำเข้าเป็น image', confirm: 'เริ่มนำเข้า',
    desc: `คัดลอก <b>${esc(edition)}</b> จากแผ่น <b>${esc(name)}</b> มาเป็น image ใหม่ในหัวข้อของแผ่น ISO (ไม่แทนที่ image จากเครื่องต้นแบบ) — เสร็จแล้วจะอยู่ในสถานะ "รออนุมัติ" เหมือน image ที่ capture`,
    body: `<div class="field"><label>ชื่อเวอร์ชัน</label><input class="input" name="version" value="${esc(b.dataset.ver)}" placeholder="${esc(b.dataset.ver || 'เช่น 10.0.26100.1742')}" maxlength="64">
      <span class="hint">เว้นว่างเพื่อใช้เวอร์ชันของ Windows ในแผ่น</span></div>
      <div class="callout" style="margin:0">${ic('info')}<div>ใช้เวลาหลายนาทีตามขนาดแผ่น · ระหว่างนี้ทำงานอื่นของเครื่องต้นแบบไม่ได้ · หลังนำเข้า ระบบตรวจ image อัตโนมัติภายในประมาณ 1 นาที</div></div>`,
    onConfirm: async f => {
      await api('/v1/builder/isos/' + encodeURIComponent(name) + '/import', 'POST', { index: Number(b.dataset.index), ...(f.version.trim() ? { version: f.version.trim() } : {}) });
      toast('เริ่มนำเข้าแล้ว'); renderPage(); schedulePoll();
    } });
}

// ───────── Images: build a new version (5 stages) + every version per topic ─────────
let builderBusy = false, vmCache = [], channelCache = [], isoCache = [];
const stageView = {}, stageSeen = {};
const tsOf = d => typeof d === 'number' ? d * 1000 : /^\d+$/.test(String(d)) ? Number(d) * 1000 : Date.parse(d) || 0;
const STAGES = [
  ['เตรียมเครื่องต้นแบบ', 'ติดตั้งโปรแกรม'],
  ['ปิดเครื่อง', 'ปิด Windows ตามปกติ'],
  ['Capture', 'sysprep + จับภาพอัตโนมัติ'],
  ['อนุมัติ', 'ตรวจแล้วเริ่มใช้'],
];
// a shutdown request is remembered for 10 min so the card can say "shutting down" — and notice when Windows ignored it
const shutdownMark = {
  key: vm => 'swim-shutdown-' + vm,
  set(vm) { try { localStorage.setItem(this.key(vm), String(Date.now())); } catch (e) {} },
  clear(vm) { try { localStorage.removeItem(this.key(vm)); } catch (e) {} },
  age(vm) { try { const t = Number(localStorage.getItem(this.key(vm))); return t && Date.now() - t < 600000 ? Date.now() - t : null; } catch (e) { return null; } },
};
function vmStateLabel(v) {
  const pend = v.state === 'running' ? shutdownMark.age(v.name) : null;
  if (v.state !== 'running') shutdownMark.clear(v.name);
  if (v.state === 'running') return pend !== null ? ['warn', 'กำลังปิด…', pend] : ['ok', 'กำลังทำงาน'];
  if (v.state === 'shut off') return ['off', 'ปิดอยู่'];
  if (v.state === 'in shutdown') return ['warn', 'กำลังปิด…'];
  return ['warn', v.state];
}
const vmBadge = v => { const [k, t] = vmStateLabel(v); return `<span class="vm-state ${k}"><i></i>${esc(t)}</span>`; };

let imagesData = null, imagesQuick = false;
async function pageImages() {
  // a stage click only changes what is shown: reuse the data loaded a moment ago
  if (!imagesQuick || !imagesData) imagesData = await Promise.all([api('/v1/builder/vms'), api('/v1/tasks'), api('/v1/channels'), api('/v1/images'), api('/v1/builder/isos')]);
  imagesQuick = false;
  const [vms, tasks, chs, imgs, isos] = imagesData;
  vmCache = vms; channelCache = chs; isoCache = isos; lastTasks = tasks;
  builderBusy = tasks.some(t => t.status === 'running' || t.status === 'queued');
  const flows = vms.length ? vms.map(v => buildFlow(v, tasks, imgs, chs)).join('<div style="height:16px"></div>')
    : card('สร้าง image เวอร์ชันใหม่', '', empty('builder', 'ยังไม่มีเครื่องต้นแบบ', '<div>สร้างด้วย <code class="k">scripts/create-golden-master.sh</code> บน server</div>'));
  const topics = chs.length ? chs.map(c => channelCard(c, imgs.filter(i => i.channel_id === c.id))).join('<div style="height:12px"></div>')
    : card('', '', empty('layers', 'ยังไม่มีหัวข้อ image', '<div>สร้างหัวข้อก่อน เช่น "Windows 11 Pro สำนักงาน" แล้วค่อย Capture</div>'));
  return `${flows}
  <div style="height:16px"></div>${isoSection(isos, tasks)}
  <div class="section-title"><div><h2>Image ทั้งหมด</h2><p>แยกตามหัวข้อ · แต่ละหัวข้อมีเวอร์ชันที่ใช้งานได้ทีละ 1 เวอร์ชัน</p></div><div class="spacer"></div>
    <button class="btn sm" data-new-channel>${ic('plus')}หัวข้อใหม่</button></div>
  ${topics}`;
}

function buildFlow(v, tasks, imgs, chs) {
  const n = esc(v.name), on = v.state === 'running', off = v.state === 'shut off';
  const [stk, , pendAge] = vmStateLabel(v), stopping = stk === 'warn';
  const mine = tasks.filter(t => t.payload.vm === v.name);
  const busy = mine.find(t => t.status === 'running' || t.status === 'queued');
  const lastCap = mine.find(t => t.kind === 'capture_image');
  const capImg = lastCap && lastCap.result && imgs.find(i => i.id === lastCap.result.image_id);
  const capFailed = lastCap && lastCap.status === 'failed';
  const chName = id => chs.find(c => c.id === id)?.name || '';
  const snaps = v.snapshots.slice().sort((a, b) => tsOf(b.date) - tsOf(a.date));

  let cur; // 1..4
  if (busy && busy.kind === 'capture_image') cur = 3;
  else if (busy) cur = 1;
  else if (capImg && capImg.status === 'pending' && !on) cur = 4;
  else if (stopping) cur = 2;
  else if (on) cur = 1;
  else cur = 3;
  if (stageSeen[v.name] !== cur) { stageSeen[v.name] = cur; delete stageView[v.name]; }
  const view = stageView[v.name] || cur;

  const pill = (s, i) => {
    const k = i + 1, state = k === 3 && capFailed && cur === 3 && !busy ? 'failed' : k < cur ? 'done' : k === cur ? (busy || stopping ? 'busy' : 'current') : 'todo';
    return `<li class="${state}${k === view ? ' viewing' : ''}"><button data-stage="${n}|${k}">
      <span class="n">${state === 'done' ? ic('check') : state === 'failed' ? ic('x') : k}</span><span class="t"><b>${s[0]}</b><small>${s[1]}</small></span></button></li>`;
  };
  const cd = (v.disks || []).find(d => d.device === 'cdrom'), iso = cd && cd.source ? cd.source.split('/').pop() : '';
  const startBtn = `<button class="btn primary" data-act="power/start" ${off && !busy ? '' : 'disabled'}>${ic('play')}เปิดเครื่อง</button>`;
  const consoleBtn = `<button class="btn primary" data-act="console" ${on ? '' : 'disabled'} title="${on ? '' : 'เปิดเครื่องก่อน'}">${ic('console')}เปิดหน้าจอ (Console)</button>`;
  const shutBtn = `<button class="btn" data-act="power/shutdown" ${on ? '' : 'disabled'}>${ic('power')}ปิดเครื่อง</button>`;
  const offBtn = `<button class="btn danger" data-act="power/poweroff" ${on ? '' : 'disabled'}>${ic('stop')}ตัดไฟ</button>`;
  const ignored = pendAge !== undefined && pendAge > 90000;
  const panel = {
    1: `${busy && busy.kind !== 'capture_image' ? taskProgress(busy) + '<div style="height:12px"></div>' : ''}<p>เปิดเครื่องต้นแบบแล้วใช้หน้าจอ Console ติดตั้ง Windows Update และโปรแกรมที่ทุกเครื่องต้องมี — เสร็จแล้วไปขั้นที่ 2 ปิดเครื่อง</p>
      <div class="btn-group">${off ? startBtn : consoleBtn + shutBtn}</div>
      <details class="adv"><summary>จุดย้อนกลับ (snapshot) — ไม่บังคับ${snaps.length ? ` · มี ${snaps.length}` : ''}</summary>
        <p class="mute" style="font-size:12.5px;margin:6px 0 8px">เก็บสภาพเครื่องต้นแบบไว้ก่อนแก้ไขครั้งใหญ่ จะได้ย้อนกลับได้ถ้าติดตั้งอะไรพลาด (ทำได้เมื่อปิดเครื่อง)</p>
        <div class="btn-group"><button class="btn sm" data-act="snapshot" ${off && !busy ? '' : 'disabled'}>${ic('camera')}สร้าง Snapshot</button></div>
        ${snaps.length ? table(snaps, [
          { h: 'ชื่อ', v: s => `<span class="mono">${esc(s.name)}</span>` },
          { h: 'สร้างเมื่อ', v: s => when(s.date), cls: 'nowrap' },
          { h: '', cls: 'actions', v: s => `<button class="btn sm" data-act="revert" data-snap="${esc(s.name)}" ${off && !busy ? '' : 'disabled'}>${ic('undo')}ย้อนกลับ</button>
              <button class="btn sm icon ghost" data-act="delete-snapshot" data-snap="${esc(s.name)}" ${off && !busy ? '' : 'disabled'} title="ลบ">${ic('trash')}</button>` },
        ]) : ''}</details>
      <details class="adv"><summary>ตัวเลือกอื่น</summary><div class="btn-group" style="margin-top:8px">
        <span class="mute" style="font-size:12.5px">แผ่น ISO: <span class="mono">${iso ? esc(iso) : 'ไม่มี'}</span></span><button class="btn sm" data-act="media">เปลี่ยนแผ่น</button>
        <button class="btn sm" data-act="power/reset" ${on ? '' : 'disabled'}>${ic('reset')}Reset</button>
        <button class="btn sm danger" data-act="power/poweroff" ${on ? '' : 'disabled'}>${ic('stop')}ตัดไฟ</button></div></details>`,
    2: `<p>ปิด Windows ตามปกติ — กด <b>ปิดเครื่อง</b> ด้านล่าง หรือใน Console กด Start → Shut down <b>ไม่ต้องรัน sysprep เอง</b> ระบบทำให้ตอน Capture</p>
      ${ignored ? `<div class="callout warn">${ic('alert')}<div><b>Windows ยังไม่ปิด</b> — ส่งคำสั่งไปแล้ว ${Math.round(pendAge / 60000) || 1} นาที บางครั้ง Windows ไม่รับคำสั่งปิดจากภายนอก
          (มีหน้าต่างค้าง หรือกำลังติดตั้งอัปเดต) ให้เปิด Console แล้วสั่ง Shut down ในเครื่อง หรือกด <b>ตัดไฟ</b> ถ้าแน่ใจว่าบันทึกงานแล้ว</div></div>`
        : stopping ? `<div class="callout">${ic('clock')}<div>ส่งคำสั่งปิดแล้ว กำลังรอ Windows ปิดตัว (ปกติไม่เกิน 1 นาที)</div></div>` : ''}
      <div class="btn-group">${on ? shutBtn + consoleBtn.replace('btn primary', 'btn') + (ignored ? offBtn : '') : `<span class="mute">เครื่องปิดอยู่แล้ว — ไปขั้นที่ 3 Capture ได้เลย</span>`}</div>`,
    3: busy && busy.kind === 'capture_image'
      ? `${taskProgress(busy)}<p class="mute" style="font-size:12.5px;margin:10px 0 0">ระหว่างนี้เปิดเครื่องต้นแบบไม่ได้ — ระบบกำลังใช้ดิสก์ของมันอยู่ · ปิดหน้านี้ได้ งานทำต่อบน server</p>`
      : `${capFailed ? `<div class="callout bad">${ic('alert')}<div><b>Capture ครั้งล่าสุดไม่สำเร็จ</b> — ${esc((lastCap.result.error || '').slice(0, 240))}
            <button class="btn sm ghost" data-log="${esc(lastCap.id)}">${ic('log')}ดู Log</button></div></div>` : ''}
        <p>ระบบทำสำเนาชั่วคราวของเครื่องต้นแบบ รัน sysprep ในสำเนา คลายไฟล์ที่ Windows บีบอัด ตรวจ 6 ข้อ แล้วจับภาพเป็นเวอร์ชันใหม่ (รออนุมัติ)
          — <b>เครื่องต้นแบบไม่ถูกแก้</b> ใช้แก้ไขต่อได้ทันที</p>
        <div class="btn-group"><button class="btn primary" data-act="capture" ${off && !busy ? '' : 'disabled'}>${ic('capture')}${capFailed ? 'ลอง Capture อีกครั้ง' : 'Capture'}</button></div>
        ${on ? `<div class="callout warn" style="margin:12px 0 0">${ic('alert')}<div>เครื่องต้นแบบยังเปิดอยู่ — ปิดเครื่องก่อน (ขั้นที่ 2)</div></div>` : ''}`,
    4: capImg && capImg.status === 'pending'
      ? `<p>เวอร์ชัน <b class="mono">${esc(chName(capImg.channel_id))} ${esc(capImg.version)}</b> (${fmtBytes(capImg.size_bytes)}) พร้อมให้ตรวจ
          <button class="btn sm ghost" data-image="${esc(capImg.id)}">${ic('log')}ดูผลตรวจอัตโนมัติ</button></p>
        ${selfBlocked(capImg.built_by) ? `<div class="callout">${ic('info')}<div>คุณเป็นคนสร้างเวอร์ชันนี้ — ต้องให้ <b>admin อีกคน</b>เข้ามากดอนุมัติ</div></div>`
          : `<div class="btn-group"><button class="btn primary" data-approve="${esc(capImg.id)}" data-label="${esc(chName(capImg.channel_id) + ' ' + capImg.version)}">${ic('check')}อนุมัติและเริ่มใช้งาน</button></div>`}`
      : capImg && capImg.status === 'published' ? `<p>เวอร์ชัน <b class="mono">${esc(capImg.version)}</b> อนุมัติแล้วและใช้งานอยู่ — เครื่องในหัวข้อ <b>${esc(chName(capImg.channel_id))}</b> ติดตั้งเวอร์ชันนี้ได้แล้ว</p>`
      : `<p class="mute">ยังไม่มีเวอร์ชันใหม่ที่รออนุมัติ — หลัง Capture เวอร์ชันใหม่จะมารอที่ขั้นนี้</p>`,
  }[view];

  const latest = capImg && capImg.status === 'published' && cur === 3 && !capFailed;
  return `<section class="card build" data-vm="${n}">
    <div class="card-head"><div class="dot-icon accent">${ic('builder')}</div><div><h2>สร้าง image เวอร์ชันใหม่</h2>
      <p>เครื่องต้นแบบ <span class="mono">${n}</span> · ดิสก์ ${fmtBytes(v.disk_bytes)}</p></div>
      <div class="spacer"></div>${busy ? `<span class="badge info pulse">${esc(TASK_KIND[busy.kind] || busy.kind)}…</span>` : ''}${vmBadge(v)}</div>
    ${latest ? `<div class="build-done">${ic('check')}<div>เวอร์ชันล่าสุด <b class="mono">${esc(capImg.version)}</b> ใช้งานอยู่ — ถ้าแก้เครื่องต้นแบบแล้ว กด Capture เพื่อสร้างเวอร์ชันถัดไป</div></div>` : ''}
    <ol class="stages">${STAGES.map(pill).join('')}</ol>
    <div class="stage-panel"><h3>ขั้นที่ ${view} · ${STAGES[view - 1][0]}</h3>${panel}</div>
  </section>`;
}

// image detail: what the builder did and what it checked, before anyone approves it
const CHECK_TH = {
  'S-01': 'Windows ผ่าน sysprep แล้ว — ติดตั้งให้หลายเครื่องได้ (ชื่อเครื่องและบัญชีตั้งใหม่ได้)',
  'S-02': 'ไฟล์บูตและเครื่องมือติดตั้งครบและอ่านได้ใน WinPE',
  'S-03': 'ไม่มีตัวตน SWIM agent ของเครื่องต้นแบบติดมา',
  'S-04': 'ไม่มีรหัสผ่านแบบอ่านได้ในไฟล์ unattend',
  'S-05': 'ไม่มีอัปเดต Windows ค้างติดตั้ง',
  'S-06': 'ไม่รวมไฟล์ pagefile / hiberfil ใน image',
};
const STEP_TH = { preflight: 'ตรวจก่อนเริ่ม', sysprep: 'ตรวจว่า sysprep แล้ว', nbd: 'เปิดดิสก์เครื่องต้นแบบ', decompress: 'คลายไฟล์ที่ Windows บีบอัดไว้',
  test: 'ตรวจอัตโนมัติ', capture: 'จับภาพเป็นไฟล์ WIM', register: 'บันทึก image', hash: 'คำนวณ SHA-256', find_partition: 'หาพาร์ทิชัน Windows', prepare: 'เตรียมงาน' };
async function openImage(id) {
  closeDrawer();
  const scrim = document.createElement('div'); scrim.className = 'scrim'; scrim.style.background = 'rgba(9,14,22,.3)';
  const d = document.createElement('aside'); d.className = 'drawer';
  d.innerHTML = `<div class="drawer-head"><div class="dot-icon accent">${ic('images')}</div><div style="flex:1;min-width:0"><h3 id="img-title">Image</h3>
    <div class="mute" style="font-size:12px" id="img-sum">กำลังโหลด…</div></div><button class="btn icon ghost" data-close aria-label="ปิด">${ic('x')}</button></div>
    <div id="img-body" style="overflow:auto;flex:1"></div>`;
  scrim.onclick = closeDrawer; d.querySelector('[data-close]').onclick = closeDrawer;
  document.body.append(scrim, d); document.addEventListener('keydown', escDrawer);
  let r;
  try { r = await api('/v1/images/' + encodeURIComponent(id)); } catch (e) { $('#img-body').innerHTML = empty('alert', 'โหลดไม่สำเร็จ', esc(e.message)); return; }
  const i = r.image, checks = r.checks || [], events = (r.events || []).filter(e => !e.step.startsWith('check '));
  const ch = channelCache.find(c => c.id === i.channel_id);
  const checkMsg = cid => (r.events || []).find(e => e.step === 'check ' + cid)?.message?.replace(/\s—\s*$/, '') || '';
  $('#img-title').textContent = `${ch ? ch.name + ' ' : ''}${i.version}`;
  $('#img-sum').innerHTML = `${i.status === 'published' ? 'ใช้งานอยู่' : i.status === 'pending' ? 'รออนุมัติ' : esc(i.status)} · สร้าง ${when(i.created_at)} โดย ${esc((i.built_by || '').replace(/^u_/, ''))}`;
  const passed = checks.filter(c => c.passed).length;
  const fact = (k, v) => `<div class="fact"><span>${k}</span><b>${v}</b></div>`;
  $('#img-body').innerHTML = `
    <div style="padding:16px 18px 4px">
      ${i.installable === false ? `<div class="callout bad">${ic('alert')}<div><b>ติดตั้งผ่านระบบล้างเครื่องไม่ได้</b> — ${i.wof_files == null ? 'image นี้สร้างก่อนระบบตรวจไฟล์บีบอัด' : `ยังมีไฟล์ระบบที่ถูกบีบอัด ${i.wof_files} ไฟล์`} ให้สร้างเวอร์ชันใหม่</div></div>`
        : i.installable ? `<div class="callout ok">${ic('check')}<div><b>พร้อมติดตั้ง</b> — ผ่านการตรวจอัตโนมัติ ${passed}/${checks.length} ข้อ</div></div>` : ''}
      <div class="facts" style="padding:0">
        ${fact('ขนาดไฟล์', fmtBytes(i.size_bytes))}${fact('ติดตั้งแล้วใช้', i.expanded_bytes ? '~' + fmtBytes(i.expanded_bytes) : '—')}
        ${fact('C: ต้องมีอย่างน้อย', i.min_system_gb ? i.min_system_gb + ' GB' : '—')}${fact('SHA-256', `<span class="mono" title="${esc(i.sha256 || '')}">${esc((i.sha256 || '').slice(0, 16))}…</span>`)}</div>
    </div>
    ${checks.length ? `<div class="sub-head" style="padding:14px 18px 6px"><h3>ตรวจอัตโนมัติ</h3></div><div class="list">${checks.map(c => `<div class="list-row" style="align-items:flex-start">
        <div class="dot-icon ${c.passed ? 'ok' : 'bad'}">${ic(c.passed ? 'check' : 'x')}</div>
        <div class="grow"><div class="title" style="white-space:normal">${esc(CHECK_TH[c.id] || c.id)}</div><div class="meta" style="white-space:normal">${esc(c.id)} · ${esc(checkMsg(c.id))}</div></div></div>`).join('')}</div>` : ''}
    <div class="sub-head" style="padding:14px 18px 6px"><h3>ขั้นตอนที่ builder ทำ</h3></div>
    <ol class="timeline">${events.map(e => `<li class="${e.status === 'ok' ? 'ok' : e.status === 'failed' ? 'bad' : e.status === 'skipped' ? 'skip' : ''}">
      <b>${esc(STEP_TH[e.step] || e.step)}</b> <span class="mute" style="font-size:12px">${esc(new Date(e.at).toLocaleTimeString(LOC()))}</span>
      <div class="mute" style="font-size:12.5px">${esc(e.message || '')}</div></li>`).join('')}</ol>
    ${i.status === 'pending' ? `<div style="padding:4px 18px 18px">${selfBlocked(i.built_by) ? '<span class="mute">รอ admin อีกคนอนุมัติ</span>'
      : `<button class="btn primary" data-approve="${esc(i.id)}" data-label="${esc((ch?.name || '') + ' ' + i.version)}" ${i.installable === false ? 'disabled' : ''}>${ic('check')}อนุมัติและเริ่มใช้งาน</button>`}</div>` : ''}
    ${!['building', 'deleted'].includes(i.status) ? `<div style="padding:4px 18px 22px;border-top:1px solid var(--line-2);margin-top:8px;padding-top:14px">
      <button class="btn danger" data-image-delete="${esc(i.id)}" data-version="${esc(i.version)}" data-status="${esc(i.status)}" data-label="${esc((ch?.name || '') + ' ' + i.version)}">${ic('trash')}ลบเวอร์ชันนี้</button></div>` : ''}`;
}

function channelCard(c, list) {
  const cur = list.find(i => i.status === 'published');
  const order = { pending: 0, published: 1 };
  const gone = list.filter(i => i.status === 'deleted').length;
  list = list.filter(i => i.status !== 'deleted');
  const rows = list.slice().sort((a, b) => (order[a.status] ?? 2) - (order[b.status] ?? 2) || Date.parse(b.created_at) - Date.parse(a.created_at));
  const stat = i => i.status === 'published' ? '<span class="badge ok">ใช้งานอยู่</span>' : i.status === 'pending' ? '<span class="badge warn">รออนุมัติ</span>'
    : i.status === 'building' ? '<span class="badge info pulse">กำลังสร้าง</span>' : i.status === 'failed' ? '<span class="badge bad plain">ล้มเหลว</span>'
    : i.status === 'rejected' ? '<span class="badge plain">ไม่ใช้ (ถูกปฏิเสธ)</span>' : '<span class="badge">เวอร์ชันเก่า</span>';
  return card(`${esc(c.name)}${isIsoChannel(c) ? ' <span class="badge info plain">จากแผ่น ISO</span>' : ''}`, `Windows ${esc(c.edition)} · ${cur ? `ใช้งานอยู่: <b class="mono">${esc(cur.version)}</b>` : 'ยังไม่มีเวอร์ชันที่ใช้งาน'}${gone ? ` · ลบไปแล้ว ${gone} เวอร์ชัน` : ''}`,
    table(rows, [
      { h: 'เวอร์ชัน', v: i => `<b class="mono" style="font-weight:500" title="SHA-256 ${esc(i.sha256 || '')}">${esc(i.version)}</b>` },
      { h: 'สถานะ', v: stat },
      { h: 'ขนาดไฟล์', v: i => i.size_bytes ? fmtBytes(i.size_bytes) + (i.expanded_bytes ? `<div class="mute" style="font-size:12px">ติดตั้งแล้วใช้ ~${fmtBytes(i.expanded_bytes)}</div>` : '') : '<span class="mute">—</span>' },
      { h: 'สร้าง', v: i => `${when(i.created_at)}<div class="mute" style="font-size:12px">โดย ${esc((i.built_by || '').replace(/^u_/, ''))}${i.approved_by ? ' · อนุมัติโดย ' + esc(i.approved_by.replace(/^u_/, '')) : ''}</div>`, cls: 'nowrap' },
      { h: '', cls: 'actions', v: i => `${i.status !== 'building' ? `<button class="btn sm ghost" data-image="${esc(i.id)}">${ic('log')}ผลตรวจ</button>` : ''}${i.status !== 'pending' ? ''
          : selfBlocked(i.built_by) ? '<span class="mute" style="font-size:12px">รอ admin อีกคนอนุมัติ</span>'
          : `<button class="btn sm primary" data-approve="${esc(i.id)}" data-label="${esc(c.name + ' ' + i.version)}">${ic('check')}อนุมัติ</button>`}${i.status !== 'building'
          ? `<button class="btn sm icon ghost" data-image-delete="${esc(i.id)}" data-version="${esc(i.version)}" data-status="${esc(i.status)}" data-label="${esc(c.name + ' ' + i.version)}" title="ลบเวอร์ชันนี้" aria-label="ลบ ${esc(i.version)}">${ic('trash')}</button>` : ''}` },
    ], empty('images', 'ยังไม่มีเวอร์ชัน', '<div>Capture จากเครื่องต้นแบบด้านบน</div>')), '', true);
}

// ───────── machine helpers (shared by list + detail) ─────────
const inv = h => h.inventory || {};
const osShort = h => {
  const o = inv(h).os || {};
  const cap = (o.caption || '').replace(/^Microsoft\s+/, '');
  return cap ? cap + (o.display_version ? ' ' + o.display_version : '') : '';
};
const osBuild = h => { const o = inv(h).os || {}; return o.build ? o.build + (o.ubr ? '.' + o.ubr : '') : ''; };
const hostIPs = h => (inv(h).network || []).flatMap(n => n.ipv4 || []).filter(ip => ip && !ip.startsWith('169.254.'));
const diskC = h => (inv(h).disks || []).find(d => (d.drive || '').toUpperCase() === 'C:') || (inv(h).disks || [])[0];
const verCmp = (a, b) => { const x = String(a || '0').split('.').map(Number), y = String(b || '0').split('.').map(Number);
  for (let i = 0; i < 3; i++) { if ((x[i] || 0) !== (y[i] || 0)) return (x[i] || 0) - (y[i] || 0); } return 0; };
let latestAgent = '';
const fmtUptime = s => !s && s !== 0 ? '—' : s < 3600 ? Math.round(s / 60) + ' นาที' : s < 86400 ? Math.round(s / 3600) + ' ชม.' : Math.floor(s / 86400) + ' วัน ' + Math.round((s % 86400) / 3600) + ' ชม.';

// problems worth an admin's attention; [level, label]
function hostAlerts(h) {
  const i = inv(h), a = [];
  if (h.retired_at) return a;
  if (!h.inventory) a.push(['', 'ยังไม่มีข้อมูลละเอียด']);
  const c = diskC(h);
  if (c && c.size && (c.free < 10 * 2 ** 30 || c.free / c.size < 0.1)) a.push(['bad', 'ดิสก์ใกล้เต็ม']);
  if (i.defender && i.defender.realtime === false) a.push(['bad', 'Defender ปิด']);
  else if (i.defender && i.defender.signature_age_days > 7) a.push(['warn', 'ฐานไวรัสเก่า']);
  if (i.bitlocker && i.bitlocker.protection !== 1) a.push(['warn', 'BitLocker ปิด']);
  if (i.pending_reboot) a.push(['warn', 'รอรีสตาร์ต']);
  if (latestAgent && h.agent_version && verCmp(h.agent_version, latestAgent) < 0) a.push(['info', 'agent เก่า']);
  return a;
}
// two-person mode only: the creator may not approve their own image/script
const selfBlocked = by => !!(me && me.two_person && by === me.user_id);
const presence = h => h.retired_at ? '<span class="badge bad plain">ถอดออกแล้ว</span>'
  : h.removing_at ? '<span class="badge warn pulse">กำลังถอด Agent…</span>'
  : h.review_at ? '<span class="badge warn">รอตรวจสอบ</span>'
  : `<span class="badge ${h.online ? 'ok' : ''}" data-presence="${esc(h.id)}">${h.online ? 'ออนไลน์' : 'ออฟไลน์'}</span>`;
const hostState = presence;
const meterBar = (used, total) => { const pct = total ? used / total * 100 : 0;
  return `<div class="meter ${pct >= 90 ? 'bad' : pct >= 80 ? 'warn' : ''}" style="margin:3px 0 0;width:110px"><i style="width:${pct.toFixed(0)}%"></i></div>`; };

// ───────── machines list ─────────
let hostQuick = 'all', hostLimit = 100;
function closeHostMenu() { $('#hmenu')?.remove(); document.removeEventListener('pointerdown', hostMenuAway, true); }
function hostMenuAway(e) { if (!e.target.closest('#hmenu, [data-hmenu]')) closeHostMenu(); }
function hostMenu(btn) {
  const open = $('#hmenu'); closeHostMenu(); if (open && open.dataset.for === btn.dataset.hmenu) return;
  const h = hostList.find(x => x.id === btn.dataset.hmenu); if (!h) return;
  const it = (act, icon, label, on = true) => on ? `<button role="menuitem" data-hm="${act}" data-id="${esc(h.id)}">${ic(icon)}${label}</button>` : '';
  const m = document.createElement('div'); m.id = 'hmenu'; m.className = 'hmenu'; m.setAttribute('role', 'menu'); m.dataset.for = h.id;
  m.innerHTML = `<a role="menuitem" href="#/hosts/${encodeURIComponent(h.id)}">${ic('hosts')}ดูรายละเอียด</a>${it('remote', 'console', 'รีโมตเข้าเครื่อง', h.online && !h.retired_at)}${it('command', 'console', 'สั่งงาน', !h.retired_at)}
    ${h.retired_at ? '' : (() => { const why = h.online ? 'เปิดอยู่แล้ว' : !macOf(h) ? 'ไม่รู้ MAC' : ''; return `<button role="menuitem" data-hm="wake" data-id="${esc(h.id)}" ${why ? 'disabled' : ''}>${ic('power')}ปลุกเครื่อง${why ? `<small class="hm-why">${why}</small>` : ''}</button>`; })()}${it('site', 'layers', 'ย้ายห้อง', !h.retired_at)}`;
  document.body.append(m);
  const r = btn.getBoundingClientRect(), w = m.offsetWidth;
  m.style.top = (r.bottom + 6 + window.scrollY) + 'px'; m.style.left = Math.max(8, r.right - w + window.scrollX) + 'px';
  m.querySelector('a,button')?.focus();
  m.addEventListener('keydown', e => { if (e.key === 'Escape') { closeHostMenu(); btn.focus(); } });
  document.addEventListener('pointerdown', hostMenuAway, true);
}
let hostFilter = '', showRetired = false, siteFilter = 'all', hostSel = new Set(), hostList = [];
// online / offline as one clear green or red state; care items as a quiet amber line under it
function presenceCell(h, al = hostAlerts(h)) {
  if (h.retired_at) return '<span class="pst gone"><i></i>ถอดออกแล้ว</span>';
  const care = al.filter(a => a[0] === 'bad' || a[0] === 'warn').map(a => a[1]);
  return `<span class="pst ${h.online ? 'on' : 'off'}" data-pst="${esc(h.id)}"><i></i><b>${h.online ? 'ออนไลน์' : 'ออฟไลน์'}</b></span>
    <small class="pst-sub" data-seen="${esc(h.id)}">${h.online ? '' : `ติดต่อล่าสุด ${esc(ago(h.last_seen))}`}</small>${care.length ? `<small class="pst-care" title="${esc(care.join(', '))}">${ic('alert')}${esc(care.slice(0, 2).join(', '))}${care.length > 2 ? ` +${care.length - 2}` : ''}</small>` : ''}`;
}
// which Windows the machine runs now: the image (golden master or ISO) of its last finished SWIM install, or the Windows it came with
const instCache = {};
function installedOf(h) {
  const x = h.installed_image || instCache[h.id];
  if (!x || !x.version) return null;
  const img = imageCache.find(i => i.id === x.image_id || i.version === x.version), ch = channelCache.find(c => c.id === (x.channel_id || img?.channel_id));
  return { version: x.version, at: x.installed_at || x.at, iso: x.iso ?? isIsoChannel(ch), edition: x.edition || ch?.edition || '', channel: x.channel_name || ch?.name || '' };
}
function installedCell(h) {
  const x = installedOf(h);
  const ps = h.policy?.prestage_image_id ? prestageState(h) : null;
  const keep = ps ? `<small class="ps-line ${ps.tone}">${ic('hdd')}${esc(ps.label)}</small>` : '';
  if (x) return `<span class="inst"><span class="ichip ${x.iso ? 'iso' : 'gm'}">${x.iso ? 'ISO' : 'Image'}</span><b translate="no">${esc(x.version)}</b></span>
    <small>${esc(x.iso ? `Windows ${x.edition || ''}`.trim() : (x.channel || 'จากเครื่องต้นแบบ'))}${x.at ? `, ติดตั้งเมื่อ ${esc(fmtDate(x.at))}` : ''}</small>${keep}`;
  if (!('installed_image' in h) && h.job?.status === 'completed' && !(h.id in instCache)) return '<span class="mute">กำลังโหลด…</span>';
  const ps2 = h.policy?.prestage_image_id ? prestageState(h) : null;
  return `<span class="inst"><span class="ichip own">เดิม</span><b>${esc(osShort(h) || 'Windows')}</b></span><small>ยังไม่เคยล้างเครื่องด้วย SWIM</small>${ps2 ? `<small class="ps-line ${ps2.tone}">${ic('hdd')}${esc(ps2.label)}</small>` : ''}`;
}
async function loadInstalled() {
  const need = hostList.filter(h => !('installed_image' in h) && h.job?.status === 'completed' && !(h.id in instCache)).slice(0, 40);
  await Promise.all(need.map(async h => {
    try { const j = (await api('/v1/hosts/' + encodeURIComponent(h.id) + '/jobs')).find(x => x.status === 'completed');
      instCache[h.id] = j ? { version: j.image_version, at: j.finished_at || j.updated_at } : null; } catch (e) { instCache[h.id] = null; }
    const td = document.querySelector(`[data-inst="${h.id}"]`); if (td) td.innerHTML = installedCell(h);
  }));
}
async function pageHosts() {
  const [all, sites, info, imgs, chs] = await Promise.all([api('/v1/hosts'), api('/v1/sites'), api('/v1/enroll/info').catch(() => ({})),
    api('/v1/images').catch(() => []), api('/v1/channels').catch(() => [])]);
  hostList = all; siteCache = sites; latestAgent = info.agent?.version || latestAgent; imageCache = imgs; channelCache = chs; markDups(all);
  const live = all.filter(h => !h.retired_at), retiredCount = all.length - live.length;
  const inSite = h => siteFilter === 'all' || (siteFilter === 'review' ? !!h.review_at : siteFilter === 'none' ? !h.site_id && !h.review_at : !h.review_at && h.site_id === siteFilter);
  const reviewN = live.filter(h => h.review_at).length;
  const q = hostFilter.toLowerCase();
  const rows = (showRetired ? all : live).filter(inSite).filter(h => !q || [h.hostname, h.model, h.serial, h.vendor, inv(h).user, ...hostIPs(h), osShort(h)]
    .join(' ').toLowerCase().includes(q)).sort((a, b) => (b.online - a.online) || String(a.hostname).localeCompare(String(b.hostname)));
  for (const id of [...hostSel]) if (!rows.some(h => h.id === id && !h.retired_at)) hostSel.delete(id);
  const scope = live.filter(inSite), online = scope.filter(h => h.online).length;
  const attention = scope.filter(h => hostAlerts(h).some(a => a[0] === 'bad' || a[0] === 'warn')).length;
  const outdated = scope.filter(h => latestAgent && verCmp(h.agent_version, latestAgent) < 0).length;
  const chip = (key, label, n) => `<button class="chip ${siteFilter === key ? 'on' : ''}" data-site-filter="${esc(key)}">${esc(label)} <span>${n}</span></button>`;
  const siteName = siteFilter === 'all' ? '' : siteFilter === 'review' ? 'รอตรวจสอบ' : siteFilter === 'none' ? 'ไม่ระบุ site' : (sites.find(s => s.id === siteFilter)?.name || '');
  const allSel = rows.length && rows.filter(h => !h.retired_at).every(h => hostSel.has(h.id));

  const care = h => hostAlerts(h).some(a => a[0] === 'bad' || a[0] === 'warn');
  const quickN = { all: scope.length, online, offline: scope.length - online, care: attention };
  const shown = rows.filter(h => hostQuick === 'all' || (hostQuick === 'online' ? h.online : hostQuick === 'offline' ? !h.online : care(h)));
  // big lists render in pages of 100 so the table stays fast with 1000 machines
  const page = shown.slice(0, hostLimit), more = shown.length - page.length;
  const tableHtml = shown.length ? `<div class="table-wrap"><table class="t hosts peers"><thead><tr>
      <th style="width:34px"><input type="checkbox" class="cb" data-sel-all ${allSel ? 'checked' : ''} aria-label="เลือกทั้งหมด"></th>
      <th>ชื่อเครื่อง</th><th>ที่อยู่</th><th>ห้อง</th><th>สถานะ</th><th>Windows ที่ติดตั้งอยู่</th><th>Agent</th><th style="width:44px"><span class="sr">เมนู</span></th></tr></thead><tbody>
    ${page.map(h => {
      const al = hostAlerts(h), u = inv(h).user, ips = hostIPs(h), model = hwModel(h);
      return `<tr class="${hostSel.has(h.id) ? 'sel' : ''}${h.retired_at ? ' retired' : ''}">
        <td>${h.retired_at ? '' : `<input type="checkbox" class="cb" data-sel="${esc(h.id)}" ${hostSel.has(h.id) ? 'checked' : ''} aria-label="เลือก ${esc(h.hostname || h.id)}">`}</td>
        <td><div class="pc"><span class="dot ${h.retired_at ? 'gone' : h.online ? 'on' : ''}" data-dot="${esc(h.id)}" title="${h.online ? 'ออนไลน์' : 'ออฟไลน์'}"></span><div>
          <a href="#/hosts/${encodeURIComponent(h.id)}" class="pc-name">${esc(h.hostname || h.id)}</a>${dupTag(h)}${h.open_commands ? ` <span class="badge info plain">${h.open_commands} คำสั่ง</span>` : ''}
          <small>${u ? esc(u) : esc(model || 'ไม่มีผู้ใช้ล็อกอิน')}</small></div></div></td>
        <td>${ips.length ? `<span class="pc-l">${esc(ips[0])}</span>${ips[1] ? `<small>${esc(ips[1])}</small>` : model && u ? `<small>${esc(model)}</small>` : ''}` : '<span class="mute">ไม่ทราบ</span>'}</td>
        <td>${h.site_name ? `<span class="gchip">${ic('layers')}${esc(h.site_name)}</span>` : h.review_at ? '<span class="gchip warn">รอตรวจสอบ</span>' : '<span class="mute">ไม่ระบุ</span>'}</td>
        <td>${presenceCell(h, al)}</td>
        <td data-inst="${esc(h.id)}">${installedCell(h)}</td>
        <td><span class="pc-l">${esc(h.agent_version || 'ไม่ทราบ')}${latestAgent && h.agent_version && verCmp(h.agent_version, latestAgent) < 0 ? ' <span class="badge warn plain">มีรุ่นใหม่</span>' : ''}</span></td>
        <td class="actions"><button class="btn icon ghost sm" data-hmenu="${esc(h.id)}" aria-label="เมนูของ ${esc(h.hostname || h.id)}" aria-haspopup="menu">${ic('kebab')}</button></td></tr>`;
    }).join('')}</tbody></table></div>${more > 0 ? `<div class="tmore"><span>แสดง ${page.length} จาก ${shown.length} เครื่อง</span><button class="btn sm" data-hmore>แสดงเพิ่มอีก ${Math.min(100, more)} เครื่อง</button>${more > 100 ? '<button class="btn sm ghost" data-hall>แสดงทั้งหมด</button>' : ''}</div>` : ''}`
    : all.length ? emptyCta('search', 'ไม่พบเครื่องที่ตรงกับตัวกรอง', 'ลองเปลี่ยนคำค้นหา ห้อง หรือสถานะที่เลือกไว้', null, '', null) + `<div class="empty2-a"><button class="btn" data-hosts-reset>ล้างตัวกรอง</button></div>`
    : emptyCta('hosts', 'ยังไม่มีเครื่องในระบบ', 'ดาวน์โหลดตัวติดตั้ง SWIM Agent แล้วดับเบิลคลิกบนเครื่อง Windows<br>เครื่องจะขึ้นที่นี่ภายในไม่กี่วินาที', '#/enroll', 'เพิ่มเครื่อง', '#/enroll');

  const selBar = hostSel.size ? `<div class="selbar"><b>เลือก ${hostSel.size} เครื่อง</b>
      <button class="btn sm" data-bulk="site">${ic('layers')}ย้ายห้อง</button>
      <button class="btn sm" data-bulk="channel">${ic('images')}ตั้งหัวข้อ image</button>
      ${(() => { const sel = [...hostSel].map(id => hostList.find(h => h.id === id)).filter(Boolean), n = sel.filter(h => !h.online && macOf(h)).length;
        return `<button class="btn sm" data-bulk="wake" ${n ? '' : `disabled title="${sel.length ? 'เครื่องที่เลือกเปิดอยู่แล้ว หรือยังไม่รู้ MAC' : ''}"`}>${ic('power')}ปลุกเครื่อง${n ? ` (${n})` : ''}</button>`; })()}
      <button class="btn sm" data-bulk="lock">${ic('shield')}ล็อกการติดตั้ง</button>
      <button class="btn sm" data-bulk="unlock">ปลดล็อก</button>
      <button class="btn sm" data-bulk="clear-history">${ic('trash')}ล้างประวัติคำสั่ง</button>
      <button class="btn sm" data-bulk="prestage">${ic('hdd')}เก็บ image ล่วงหน้า</button>
      <button class="btn sm" data-bulk="deploy">${ic('capture')}ล้างเครื่องและติดตั้ง</button>
      <button class="btn sm primary" data-bulk="command">${ic('console')}สั่งงาน</button>
      <div class="spacer"></div><button class="btn sm ghost" data-bulk="clear">ล้างการเลือก</button></div>` : '';
  const qseg = [['all', 'ทั้งหมด'], ['online', 'ออนไลน์'], ['offline', 'ออฟไลน์'], ['care', 'ต้องดูแล']];
  const desc = `เครื่อง Windows ที่ติดตั้ง SWIM Agent แล้ว ออนไลน์ <span data-online-count>${online}</span> จาก ${scope.length} เครื่อง${outdated ? `, agent รุ่นเก่า ${outdated} เครื่อง` : ''}`;
  return `
  <header class="page-h2"><div><h2>${siteName ? 'เครื่องใน ' + esc(siteName) : 'เครื่อง'}<span class="n">${scope.length}</span></h2><p>${desc}</p></div></header>
  <div class="ptool">
    <div class="search psearch">${ic('search')}<input class="input" id="host-q" placeholder="ค้นหาชื่อ ผู้ใช้ IP หรือรุ่น" value="${esc(hostFilter)}" aria-label="ค้นหาเครื่อง"><kbd>/</kbd></div>
    <label class="pfilter">${ic('filter')}<select class="select" id="site-f" aria-label="กรองตามห้อง">
      <option value="all" ${siteFilter === 'all' ? 'selected' : ''}>ทุกห้อง</option>${sites.map(x => `<option value="${esc(x.id)}" ${siteFilter === x.id ? 'selected' : ''}>ห้อง ${esc(x.name)} (${x.hosts})</option>`).join('')}
      <option value="none" ${siteFilter === 'none' ? 'selected' : ''}>ไม่ระบุห้อง</option>${reviewN ? `<option value="review" ${siteFilter === 'review' ? 'selected' : ''}>รอตรวจสอบ (${reviewN})</option>` : ''}
      <option value="__manage">จัดการห้อง…</option></select></label>
    <div class="seg pseg" role="tablist" aria-label="สถานะ">${qseg.map(([k, l]) => `<button role="tab" class="${hostQuick === k ? 'on' : ''}" aria-selected="${hostQuick === k}" data-hquick="${k}">${l}<span>${quickN[k]}</span></button>`).join('')}</div>
    <div class="spacer"></div>
    ${siteFilter !== 'all' && scope.length ? `<button class="btn" data-site-command>${ic('console')}สั่งงานทั้งห้อง</button>` : ''}
    <span class="vseg">${viewSwitch('table')}</span>
    <button class="btn icon" data-hosts-refresh title="รีเฟรช" aria-label="รีเฟรชรายการ">${ic('refresh')}</button>
    <a class="btn primary" href="#/enroll">${ic('plus')}เพิ่มเครื่อง</a>
  </div>
  ${selBar}
  <div class="ptable">${tableHtml}</div>
  ${retiredCount ? `<label class="toggle pfoot"><input type="checkbox" id="show-retired" ${showRetired ? 'checked' : ''}> แสดงเครื่องที่ถอดออกแล้ว ${retiredCount} เครื่อง</label>` : ''}
`;
}
ROUTES.hosts.after = () => {
  if (routeArg()) return pageHostAfter();
  if (viewPref.get() === 'map') return mapAfter();
  loadInstalled();
  const sr = $('#show-retired'); if (sr) sr.onchange = () => { showRetired = sr.checked; renderPage(); };
  const sf = $('#site-f'); if (sf) sf.onchange = () => { if (sf.value === '__manage') { location.hash = '#/settings/sites'; return; } siteFilter = sf.value; hostSel.clear(); renderPage(); };
  const q = $('#host-q'); if (!q) return;
  q.oninput = () => { hostFilter = q.value; hostLimit = 100; clearTimeout(q._t); q._t = setTimeout(async () => { await renderPage(); const n = $('#host-q'); n.focus(); n.setSelectionRange(n.value.length, n.value.length); }, 250); };
};
document.addEventListener('change', e => {
  const t = e.target;
  if (t.dataset.sel) { t.checked ? hostSel.add(t.dataset.sel) : hostSel.delete(t.dataset.sel); renderPage(); }
  if (t.hasAttribute('data-sel-all')) {
    const ids = [...document.querySelectorAll('[data-sel]')].map(x => x.dataset.sel);
    ids.forEach(id => t.checked ? hostSel.add(id) : hostSel.delete(id)); renderPage();
  }
});

// pick one of the approved saved scripts (free-typed PowerShell was removed: it is an attack path)
const runnable = scripts => scripts.filter(x => x.status === 'approved');
const scriptPickerHTML = scripts => runnable(scripts).length ? `<div class="field"><label>สคริปต์ที่อนุมัติแล้ว</label><select class="select" name="script_id" id="pick-script" required>
    ${runnable(scripts).map(x => `<option value="${esc(x.id)}">${esc(x.name)}</option>`).join('')}</select></div>
  <pre class="code" id="pick-preview" style="max-height:260px"></pre>` : `<div class="callout">${ic('info')}<div>ไม่มีสคริปต์ที่อนุมัติแล้ว ใช้การ์ด "เครื่องมือ" ในหน้าเครื่องแทน</div></div>`;
function wireScriptPicker(scripts) {
  const sel = $('#pick-script'), pre = $('#pick-preview'); if (!sel) return;
  const show = () => { const x = scripts.find(s => s.id === sel.value); pre.textContent = x ? x.body : ''; };
  sel.onchange = show; show();
}
const scriptArgs = f => { if (!f.script_id) throw new Error('เลือกสคริปต์ก่อน'); return { script_id: f.script_id }; };
// group command modal → POST /v1/batches → live progress drawer
const BULK_TYPES = ['ping', 'collect_logs', 'update_agent', 'restart_agent', 'reboot', 'run_script', 'uninstall_agent'];
async function openBulkCommand(target, label) {
  const scripts = (await api('/v1/scripts')).filter(x => x.status !== 'retired');
  modal({ title: 'สั่งงานเป็นกลุ่ม', wide: true, confirm: 'ส่งคำสั่ง',
    desc: `ส่งไปที่ <b>${esc(label)}</b> — แต่ละเครื่องได้คำสั่งที่เซ็นเฉพาะเครื่องตัวเอง เครื่องที่ออฟไลน์จะได้รับเมื่อกลับมา (ภายใน 1 ชม.)`,
    body: `<div class="field"><label>คำสั่ง</label><select class="select" name="type" id="bulk-type">${BULK_TYPES.map(t => `<option value="${t}">${esc(CMD[t][0])}</option>`).join('')}</select></div>
      <div id="bulk-extra"></div>`,
    onConfirm: async f => {
      const args = {};
      if (f.type === 'reboot') { args.delay_s = Number(f.delay_s); args.message = f.message; }
      if (f.type === 'run_script') Object.assign(args, scriptArgs(f));
      if (f.type === 'uninstall_agent' && f.confirm !== 'ถอนการติดตั้ง') throw new Error('พิมพ์คำว่า "ถอนการติดตั้ง" เพื่อยืนยัน');
      const r = await api('/v1/batches', 'POST', { ...target, type: f.type, args });
      toast(`ส่งแล้ว ${r.queued} เครื่อง${r.skipped.length ? ` · ข้าม ${r.skipped.length}` : ''}`);
      hostSel.clear(); await renderPage(); openBatch(r.id);
    } });
  const sel = $('#bulk-type'), extra = $('#bulk-extra');
  const draw = () => {
    const t = sel.value;
    extra.innerHTML = t === 'reboot' ? `<div class="row-fields"><div class="field"><label>รีบูตในอีก</label><select class="select" name="delay_s"><option value="300">5 นาที</option><option value="900">15 นาที</option><option value="3600">1 ชั่วโมง</option></select></div>
        <div class="field"><label>ข้อความถึงผู้ใช้</label><input class="input" name="message" value="IT จะรีสตาร์ตเครื่องนี้ กรุณาบันทึกงาน"></div></div>`
      : t === 'run_script' ? scriptPickerHTML(scripts)
      : t === 'uninstall_agent' ? `<div class="callout bad">${ic('alert')}<div>ทุกเครื่องในกลุ่มจะลบ agent ออกและหลุดจาก SWIM</div></div>
        <div class="field"><label>พิมพ์ <b>ถอนการติดตั้ง</b> เพื่อยืนยัน</label><input class="input" name="confirm" required autocomplete="off"></div>`
      : t === 'update_agent' ? `<div class="callout">${ic('info')}<div>อัปเดตเป็น agent <b>${esc(latestAgent || 'ล่าสุด')}</b> — ตรวจ SHA-256 และย้อนกลับเองถ้าเวอร์ชันใหม่สตาร์ตไม่ได้ เครื่องที่เป็นเวอร์ชันล่าสุดแล้วจะถูกข้าม (agent 0.3 ใช้สคริปต์ "อัปเดต SWIM Agent" แทน)</div></div>` : '';
  };
  sel.onchange = () => { draw(); if (sel.value === 'run_script') wireScriptPicker(scripts); }; draw();
}

let batchTimer = null;
async function openBatch(id) {
  closeDrawer();
  const scrim = document.createElement('div'); scrim.className = 'scrim'; scrim.style.background = 'rgba(9,14,22,.3)';
  const d = document.createElement('aside'); d.className = 'drawer'; d.dataset.batch = id;
  d.innerHTML = `<div class="drawer-head"><div class="dot-icon accent">${ic('console')}</div><div style="flex:1;min-width:0"><h3 id="batch-title">งานกลุ่ม</h3>
    <div class="mute" style="font-size:12px" id="batch-sum"></div></div><button class="btn icon ghost" data-close aria-label="ปิด">${ic('x')}</button></div>
    <div id="batch-body" style="overflow:auto;flex:1"></div>`;
  scrim.onclick = closeDrawer; d.querySelector('[data-close]').onclick = closeDrawer;
  document.body.append(scrim, d); document.addEventListener('keydown', escDrawer);
  const load = async () => {
    if (!document.querySelector(`.drawer[data-batch="${id}"]`)) return;
    const b = await api('/v1/batches/' + encodeURIComponent(id));
    const cnt = s => b.items.filter(i => i.status === s).length;
    $('#batch-title').textContent = CMD[b.type]?.[0] || b.type;
    $('#batch-sum').textContent = `${b.items.length} เครื่อง · สำเร็จ ${cnt('done')} · ล้มเหลว ${cnt('failed')} · กำลังทำ/รอ ${b.items.length - cnt('done') - cnt('failed') - cnt('expired') - cnt('cancelled')}`;
    $('#batch-body').innerHTML = table(b.items, [
      { h: 'เครื่อง', v: i => `<span class="dot ${i.online ? 'on' : ''}"></span> <a href="#/hosts/${encodeURIComponent(i.host_id)}">${esc(i.hostname)}</a>` },
      { h: 'สถานะ', v: i => cmdBadge(i.status) },
      { h: 'ผล', v: i => i.error ? `<span style="color:var(--bad)">${esc(i.error.slice(0, 120))}</span>` : `<span class="mono" style="font-size:12px">${esc((i.output || '').split('\n').filter(Boolean).slice(-1)[0] || '')}</span>` },
      { h: '', cls: 'actions', v: i => ['done', 'failed'].includes(i.status) ? `<button class="btn sm" data-cmd-out="${esc(i.command_id)}">ดูผล</button>` : '' },
    ]) + ((b.skipped || []).length ? `<div style="padding:12px 18px"><b style="font-size:13px">ข้าม ${b.skipped.length} เครื่อง</b>${b.skipped.map(s => `<div class="mute" style="font-size:12.5px">${esc(hostList.find(h => h.id === s.host_id)?.hostname || s.host_id)}: ${esc(s.reason)}</div>`).join('')}</div>` : '');
  };
  await load(); clearInterval(batchTimer); batchTimer = setInterval(load, 4000);
}

function openSitesManager() {
  const rows = siteCache.map(s => `<div class="list-row"><div class="grow"><div class="title">${esc(s.name)}</div><div class="meta">${s.hosts} เครื่อง${s.description ? ' · ' + esc(s.description) : ''}</div></div>
    <button type="button" class="btn sm" data-site-rename="${esc(s.id)}">เปลี่ยนชื่อ</button>
    <button type="button" class="btn sm danger" data-site-delete="${esc(s.id)}" ${s.hosts ? 'disabled title="ย้ายเครื่องออกก่อน"' : ''}>ลบ</button></div>`).join('');
  modal({ title: 'จัดการ Site', wide: true, confirm: 'สร้าง site ใหม่',
    desc: 'Site คือกลุ่มของเครื่อง (สาขา อาคาร แผนก) — ใช้สั่งงานทั้งกลุ่ม และจำกัดจำนวนติดตั้งพร้อมกัน',
    body: `<div class="list" style="border:1px solid var(--line);border-radius:8px;margin-bottom:14px">${rows || '<div class="empty" style="padding:16px">ยังไม่มี site</div>'}</div>
      <div class="row-fields"><div class="field" style="flex:1 1 200px;margin:0"><label>ชื่อ site ใหม่</label><input class="input" name="name" placeholder="เช่น BKK-HQ, ห้องแล็บ 2"></div></div>`,
    onConfirm: async f => { if (!f.name.trim()) throw new Error('ใส่ชื่อ site'); await api('/v1/sites', 'POST', { name: f.name.trim() }); toast('สร้าง site แล้ว'); renderPage(); } });
}

// who did it, in plain Thai (users by name, machines, automatic system work)
const ACTOR_TH = { 'system:auto-update': 'อัปเดตอัตโนมัติ', 'system:policy': 'นโยบายอัตโนมัติ', 'system:removed': 'ระบบ (เครื่องที่ลบแล้ว)', 'system:watchdog': 'ระบบตรวจงานค้าง', 'system:retention': 'ระบบลบประวัติเก่า', token: 'ผู้ใช้ token' };
const actorName = a => !a ? '' : a.startsWith('user:') ? a.slice(5) : a.startsWith('schedule:') ? `ตารางงาน (${a.slice(9).replace(/^user:/, '')})` : ACTOR_TH[a] || (a.startsWith('host:') ? 'เครื่อง' : a.split(':')[0]);
// ───────── Logging: who did what, on which machine, when (audit log, filterable, CSV) ─────────
const ACTION_TH = {
  'command.': 'สั่งงานเครื่อง', 'command.result': 'เครื่องส่งผลคำสั่ง', 'commands.clear': 'ล้างประวัติคำสั่ง', 'batch.': 'สั่งงานเป็นกลุ่ม',
  'host.enroll': 'เพิ่มเครื่อง', 'host.reenroll': 'เครื่องกลับมาหลังติดตั้ง Windows', 'host.retire': 'ลบเครื่องออก', 'host.remove': 'ลบเครื่อง (ถอด Agent)',
  'host.delete': 'ลบเครื่องถาวร', 'host.wake': 'ปลุกเครื่อง (Wake-on-LAN)', 'host.assign_site': 'ย้าย site', 'host.assign_channel': 'ตั้งหัวข้อ image',
  'host.removed_agent_returned': 'Agent ของเครื่องที่ลบแล้วกลับมา (สั่งถอนตัว)', 'host.remote': 'เปิด Remote Desktop', 'host.policy': 'ตั้งนโยบายเครื่อง', 'host.prestage': 'ตั้ง image ที่เก็บไว้ในเครื่อง', 'auth.login': 'เข้าสู่ระบบ', 'auth.login_failed': 'เข้าสู่ระบบไม่สำเร็จ', 'auth.lockout': 'บัญชีถูกล็อกชั่วคราว (ใส่ผิดหลายครั้ง)',
  'auth.setup': 'ตั้งค่า Google Authenticator', 'auth.logout': 'ออกจากระบบ', 'auth.session_revoke': 'ยกเลิกการเข้าระบบของอุปกรณ์', 'user.create': 'เพิ่มผู้ดูแล', 'user.disable': 'ปิดใช้งานบัญชีผู้ดูแล',
  'user.enable': 'เปิดใช้งานบัญชีผู้ดูแล', 'user.reset_2fa': 'รีเซ็ตการยืนยันตัวตน', 'user.password_change': 'เปลี่ยนรหัสผ่าน', 'user.recovery_codes': 'สร้างรหัสสำรองชุดใหม่', 'job.aborted': 'ยกเลิกงานติดตั้งที่ค้าง (ระบบตรวจเอง)', 'job.rehandoff': 'บูตเข้า WinPE อีกครั้ง', 'audit.archived': 'เก็บถาวรและเริ่มประวัติการใช้งานใหม่', 'audit.archive_download': 'ดาวน์โหลดประวัติที่เก็บถาวร', 'audit.pruned': 'ลบประวัติที่เก่าเกินกำหนด (อัตโนมัติ)', 'job.delete': 'ลบประวัติการติดตั้ง 1 รายการ', 'jobs.clear': 'ล้างประวัติการติดตั้ง', 'job.': 'ล้างเครื่อง/ติดตั้ง Windows', 'image.': 'Image', 'script.': 'สคริปต์',
  'task.': 'งานเครื่องต้นแบบ', 'enrollment_token.': 'ตัวติดตั้ง', 'site.': 'Site', 'channel.': 'หัวข้อ image', 'auth.': 'เข้าสู่ระบบ',
};
const actionTh = a => ACTION_TH[a] || Object.entries(ACTION_TH).find(([k]) => k.endsWith('.') && a.startsWith(k))?.[1] || a;
const ACTION_GROUPS = [['', 'ทุกการกระทำ'], ['command.', 'สั่งงานเครื่อง'], ['host.', 'เครื่อง'], ['job.', 'ล้างเครื่อง / ติดตั้ง Windows'],
  ['auth.', 'เข้าสู่ระบบ'], ['image.', 'Image'], ['task.', 'งานเครื่องต้นแบบ'], ['script.', 'สคริปต์'], ['enrollment_token.', 'ตัวติดตั้ง'], ['commands.clear', 'ล้างประวัติคำสั่ง']];
let logFilter = { actor: '', host: '', action: '', from: '', to: '', q: '' }, logRows = [];
const hostName = id => hostList.find(h => h.id === id)?.hostname || '';
// A machine behind a router (NAT): its agent reaches the server, but the server cannot reach the machine's own address.
const natOf = h => { const ips = (inv(h).network || []).flatMap(n => n.ipv4 || []); return h.seen_ip && ips.length && !ips.includes(h.seen_ip) ? { seen: h.seen_ip, local: ips[0] } : null; };
const natWarn = h => { const n = natOf(h); if (!n) return '';
  const where = `เครื่องใช้ที่อยู่ <span class="mono" translate="no">${esc(n.local)}</span> แต่ server เห็นเป็น <span class="mono" translate="no">${esc(n.seen)}</span>`;
  return verCmp(h.agent_version || '0', '0.5.24') >= 0
    ? `<div class="callout" style="margin:0 0 12px">${ic('info')}<div><b>เครื่องนี้อยู่หลัง router (NAT)</b><br>${where} Remote Desktop จะวิ่งผ่านอุโมงค์ที่ Agent เปิดออกมาหา server เอง จึงใช้ได้โดยไม่ต้องตั้งค่า router</div></div>`
    : `<div class="callout warn" style="margin:0 0 12px">${ic('alert')}<div><b>เครื่องนี้อยู่หลัง router (NAT)</b><br>${where} Agent รุ่นนี้ยังเปิดอุโมงค์ Remote ไม่ได้ รอ Agent อัปเดตเป็น 0.5.24 ก่อน แล้ว Remote จะใช้ได้เอง</div></div>`; };
const LOGIN_STEP = { password: 'รหัสผ่านผิด', totp: 'รหัสจากแอปผิด', recovery_code: 'รหัสสำรองผิด', setup_code: 'รหัสตอนตั้งค่าแอปผิด', setup_required: 'บัญชีต้องตั้งค่าแอปยืนยันตัวตนใหม่', token: 'token ผิด' };
// short browser name from a User-Agent ("Chrome บน Windows"); the full text stays in the tooltip
const uaShort = ua => { if (!ua) return ''; const b = /Edg\//.test(ua) ? 'Edge' : /Chrome\//.test(ua) ? 'Chrome' : /Firefox\//.test(ua) ? 'Firefox' : /Safari\//.test(ua) ? 'Safari' : /curl|python|Go-http|wget/i.test(ua) ? 'โปรแกรมอัตโนมัติ' : ua.split(/[ /]/)[0];
  const o = /Windows/.test(ua) ? 'Windows' : /Android/.test(ua) ? 'Android' : /iPhone|iPad/.test(ua) ? 'iOS' : /Mac OS X/.test(ua) ? 'macOS' : /Linux/.test(ua) ? 'Linux' : ''; return o ? `${b} บน ${o}` : b; };
function logDetail(a) {
  const d = a.data || {};
  if (a.action.startsWith('auth.')) {
    const bits = [];
    if (a.action === 'auth.login_failed') {
      bits.push(`<span class="badge bad">${esc(LOGIN_STEP[d.step] || 'ไม่สำเร็จ')}</span>`);
      if (d.step !== 'token') bits.push(`ชื่อที่กรอก <b class="mono">${esc(d.username || a.actor.replace(/^user:/, ''))}</b>${d.known_user === false ? ' <span class="mute">(ไม่มีบัญชีนี้)</span>' : ''}`);
      if (d.failures_from_ip > 1) bits.push(`ผิดครั้งที่ ${esc(d.failures_from_ip)} จาก IP นี้ใน 15 นาที`);
    } else if (a.action === 'auth.lockout') bits.push(`ล็อก ${esc(d.minutes ?? 15)} นาที`);
    else if (d.method) bits.push(d.method === 'recovery_code' ? 'ใช้รหัสสำรอง' : 'รหัสจากแอป');
    if (a.ip) bits.unshift(`IP <b class="mono">${esc(a.ip)}</b>`);
    if (d.user_agent) bits.push(`<span class="mute" title="${esc(d.user_agent)}">${esc(uaShort(d.user_agent))}</span>`);
    return bits.join(' ');
  }
  if (a.action === 'audit.pruned') return `ลบ ${Number(d.rows || 0).toLocaleString(LOC())} รายการที่เก่ากว่า ${esc(d.keep_days ?? 90)} วัน${(d.archive_files || []).length ? ` และไฟล์เก็บถาวร ${d.archive_files.length} ไฟล์` : ''}`;
  if (a.action === 'command.result') return `ผล: ${esc(a.result)}${d.exit_code != null ? ` · exit ${esc(d.exit_code)}` : ''}${d.type ? ' · ' + esc(CMD[d.type]?.[0] || d.type) : ''}`;
  if (a.action.startsWith('command.')) {
    const ar = d.args || {};
    return `${esc(CMD[a.action.slice(8)]?.[0] || a.action.slice(8))}${ar.name ? ' · ' + esc(ar.name) : ''}${ar.ad_hoc ? ' <span class="badge plain">พิมพ์เอง</span>' : ''}${ar.sha256 ? ` <span class="mono mute" title="SHA-256 ${esc(ar.sha256)}">${esc(ar.sha256.slice(0, 10))}…</span>` : ''}`;
  }
  if (a.action === 'host.wake') return `ส่งแล้ว ${(d.sent || []).length} · ข้าม ${(d.skipped || []).length}${d.error ? ' · <span style="color:var(--bad)">' + esc(d.error) + '</span>' : ''}`;
  if (a.action === 'commands.clear') return `ซ่อน ${esc(d.commands_hidden ?? 0)} คำสั่ง`;
  const keys = Object.entries(d).filter(([, v]) => v !== null && typeof v !== 'object').slice(0, 3);
  return keys.map(([k, v]) => `${esc(k)}: ${esc(String(v).slice(0, 60))}`).join(' · ') || (a.reason ? esc(a.reason) : '');
}
function logObject(a) {
  if (a.object_type === 'host') {
    const ids = a.object_id.split(',');
    return ids.map(id => hostName(id) ? `<a href="#/hosts/${encodeURIComponent(id)}">${esc(hostName(id))}</a>` : `<span class="mono mute">${esc(id)}</span>`).join(', ');
  }
  return a.object_id ? `<span class="mute">${esc(a.object_type)}</span> <span class="mono">${esc(a.object_id.slice(0, 32))}</span>` : '';
}
async function pageLogs() {
  const qs = new URLSearchParams({ limit: '1000' });
  if (logFilter.actor) qs.set('actor', logFilter.actor);
  if (logFilter.host) qs.set('host', logFilter.host);
  if (logFilter.action) qs.set('action', logFilter.action);
  if (logFilter.q) qs.set('q', logFilter.q);
  if (logFilter.from) qs.set('from', new Date(logFilter.from + 'T00:00:00').toISOString());
  if (logFilter.to) qs.set('to', new Date(new Date(logFilter.to + 'T00:00:00').getTime() + 864e5).toISOString());
  const [rows, v, hosts, archives] = await Promise.all([api('/v1/audit?' + qs), api('/v1/audit/verify'), api('/v1/hosts'), api('/v1/audit/archives').catch(() => [])]);
  auditTotal = v.visible_rows ?? rows.length;
  logRows = rows; hostList = hosts;
  const actors = [...new Set(rows.map(a => a.actor.replace(/^user:/, '')).filter(a => !a.includes(':')))];
  const opt = (v, l, cur) => `<option value="${esc(v)}" ${v === cur ? 'selected' : ''}>${esc(l)}</option>`;
  return `
  <form class="card log-filter" id="log-filter">
    <div class="field"><label>ผู้ทำ</label><input class="input" name="actor" list="log-actors" value="${esc(logFilter.actor)}" placeholder="ทั้งหมด">
      <datalist id="log-actors">${actors.map(a => `<option value="${esc(a)}">`).join('')}</datalist></div>
    <div class="field"><label>เครื่อง</label><input class="input" name="host" list="log-hosts" value="${esc(logFilter.host)}" placeholder="ชื่อหรือ id">
      <datalist id="log-hosts">${hosts.filter(h => !h.retired_at).map(h => `<option value="${esc(h.id)}">${esc(h.hostname || '')}</option>`).join('')}</datalist></div>
    <div class="field"><label>การกระทำ</label><select class="select" name="action">${ACTION_GROUPS.map(([v, l]) => opt(v, l, logFilter.action)).join('')}</select></div>
    <div class="field"><label>ตั้งแต่</label><input class="input" type="date" name="from" value="${esc(logFilter.from)}"></div>
    <div class="field"><label>ถึง</label><input class="input" type="date" name="to" value="${esc(logFilter.to)}"></div>
    <div class="field"><label>ค้นหา</label><input class="input" name="q" value="${esc(logFilter.q)}" placeholder="เช่น ชื่อสคริปต์, คำสั่ง"></div>
    <div class="btn-group" style="align-self:end"><button class="btn primary" type="submit">${ic('search')}กรอง</button>
      <button class="btn ghost" type="button" data-log-reset>ล้างตัวกรอง</button></div>
  </form>
  ${card('บันทึกทั้งหมด', `${rows.length >= 1000 ? 'แสดง 1,000 รายการล่าสุด — กรองให้แคบลงเพื่อดูเก่ากว่านี้' : rows.length + ' รายการ'} · ${v.ok
      ? `${ic('check', 'style="width:13px;height:13px;vertical-align:-2px;color:var(--ok)"')} ตรวจแล้ว ไม่มีรายการถูกแก้ไข · เก็บย้อนหลัง ${esc(Math.round((v.keep_days || 90) / 30))} เดือน เก่ากว่านั้นระบบลบเอง`
      : `<span style="color:var(--bad)">บันทึกเสียหายที่รายการ #${esc(v.broken_at)}</span>`}`,
    table(rows, [
      { h: 'เวลา', v: a => `<span class="nowrap" title="${esc(fmtDate(a.at))}">${esc(new Date(a.at).toLocaleString(LOC(), { dateStyle: 'short', timeStyle: 'medium' }))}</span>` },
      { h: 'ผู้ทำ', v: a => a.actor.startsWith('user:') ? `<b style="font-weight:500">${esc(a.actor.slice(5))}</b>` : `<span class="mute">${esc(actorName(a.actor))}</span>` },
      { h: 'การกระทำ', v: a => esc(actionTh(a.action)) },
      { h: 'เครื่อง / สิ่งที่ถูกทำ', v: a => `<span translate="no">${logObject(a)}</span>` },
      { h: 'รายละเอียด', v: a => `<span>${logDetail(a)}</span>` },
      { h: '', cls: 'actions', v: a => a.data && a.data.command_id ? `<button class="btn sm ghost" data-cmd-out="${esc(a.data.command_id)}">${ic('log')}ดูผล</button>`
          : a.object_type === 'command' ? `<button class="btn sm ghost" data-cmd-out="${esc(a.object_id)}">${ic('log')}ดูผล</button>` : '' },
    ], empty('audit', 'ไม่พบบันทึกตามตัวกรอง')),
    `<button class="btn sm" data-log-csv>${ic('capture')}ส่งออก CSV</button><button class="btn sm" data-audit-archive>${ic('audit')}เก็บถาวรและเริ่มหน้าใหม่</button>`, true)}
  ${archives.length ? card('ประวัติที่เก็บถาวร', 'ไฟล์บน server ที่เก็บบันทึกเก่าไว้ ใช้ตรวจย้อนหลังได้ ระบบลบไฟล์ที่เก่าเกินกำหนดเอง', `<div class="list">${archives.map(x => `<div class="list-row"><div class="dot-icon">${ic('audit')}</div>
      <div class="grow"><div class="title">${esc(when(x.created_at))}, ${Number(x.rows).toLocaleString(LOC())} รายการ</div><div class="meta">เก็บโดย ${esc(actorName(x.actor || ''))}, ${esc(fmtBytes(x.size || 0))}${x.missing ? ', <span style="color:var(--bad)">ไม่พบไฟล์บน server</span>' : ''}</div></div>
      <button class="btn sm" data-audit-dl="${esc(x.file)}" ${x.missing ? 'disabled' : ''}>${ic('capture')}ดาวน์โหลด</button></div>`).join('')}</div>`, '', true) : ''}`;
}
let auditTotal = 0;
ROUTES.logs.after = () => {
  const f = $('#log-filter'); if (!f) return;
  f.onsubmit = e => { e.preventDefault(); logFilter = Object.fromEntries(new FormData(f)); renderPage(); };
};
function exportLogsCSV() {
  const q = v => `"${String(v ?? '').replace(/"/g, '""')}"`;
  const lines = [['time', 'actor', 'action', 'action_th', 'object_type', 'object_id', 'hostname', 'result', 'ip', 'data'].join(',')];
  for (const a of logRows) lines.push([a.at, a.actor, a.action, actionTh(a.action), a.object_type, a.object_id,
    a.object_type === 'host' ? a.object_id.split(',').map(hostName).join(' ') : '', a.result, a.ip, JSON.stringify(a.data ?? '')].map(q).join(','));
  const blob = new Blob(['﻿' + lines.join('\r\n')], { type: 'text/csv;charset=utf-8' });
  const el = document.createElement('a'); el.href = URL.createObjectURL(blob); el.download = `swim-log-${new Date().toISOString().slice(0, 10)}.csv`;
  document.body.appendChild(el); el.click(); el.remove(); setTimeout(() => URL.revokeObjectURL(el.href), 2000);
}

// ───────── settings: things you set up once or rarely need ─────────
const SETTINGS_TABS = [['sites', 'ห้อง (Site)'], ['admins', 'ผู้ดูแลระบบ']];
const settingsTab = () => SETTINGS_TABS.some(t => t[0] === routeArg()) ? routeArg() : 'sites';
async function pageSettings() {
  const tab = settingsTab();
  const tabs = `<nav class="htabs" role="tablist" aria-label="ตั้งค่า">${SETTINGS_TABS.map(([k, l]) => `<a role="tab" class="${k === tab ? 'on' : ''}" aria-selected="${k === tab}" href="#/settings/${k}">${l}</a>`).join('')}</nav>`;
  return tabs + (tab === 'admins' ? await settingsAdmins() : await settingsSites());
}
// ── admin accounts: each admin signs in with a password and Google Authenticator
async function settingsAdmins() {
  let users;
  try { users = await api('/v1/users'); }
  catch (e) { return emptyCta('user', 'ระบบบัญชีผู้ดูแลยังไม่พร้อมบน server', 'ตอนนี้ยังเข้าสู่ระบบด้วย Admin token เมื่อ server พร้อม หน้านี้จะใช้เพิ่มผู้ดูแลแต่ละคนได้', null, '', null); }
  adminCache = users;
  const row = u => `<tr><td><div class="pc"><span class="av-s">${esc((u.display_name || u.username || '?').slice(0, 1).toUpperCase())}</span><div>
        <b class="pc-name" translate="no">${esc(u.display_name || u.username)}</b><small translate="no">${esc(u.username)}${u.id === me?.user_id || u.username === me?.username ? ' (คุณ)' : ''}</small></div></div></td>
      <td>${u.disabled_at ? '<span class="pst off"><i></i><b>ปิดใช้งาน</b></span>' : u.totp_ready ? `<span class="pst on"><i></i><b>พร้อมใช้งาน</b></span>` : '<span class="pst wait"><i></i><b>รอตั้งค่า</b></span>'}
        <small>${u.totp_ready ? `Google Authenticator ตั้งแล้ว${u.recovery_left != null ? `, รหัสสำรองเหลือ ${u.recovery_left}` : ''}` : u.setup_pending ? 'ส่งลิงก์ตั้งค่าแล้ว รอเจ้าตัวตั้งค่า' : 'ยังไม่ได้สแกน QR'}</small></td>
      <td>${u.last_login_at ? esc(ago(u.last_login_at)) : '<span class="mute">ยังไม่เคย</span>'}${u.locked_until && Date.parse(u.locked_until) > Date.now() ? '<small style="color:#d99a26">ถูกล็อกชั่วคราว (ใส่ผิดหลายครั้ง)</small>' : ''}</td>
      <td class="actions">${u.id === me?.user_id || u.username === me?.username ? '' : `<button class="btn sm" data-u-reset="${esc(u.id)}">${ic('refresh')}รีเซ็ตการยืนยันตัวตน</button>
        <button class="btn sm ${u.disabled_at ? '' : 'danger'}" data-u-toggle="${esc(u.id)}">${u.disabled_at ? 'เปิดใช้งาน' : 'ปิดใช้งาน'}</button>`}</td></tr>`;
  return `<header class="page-h2" style="margin-top:6px"><div><h2>ผู้ดูแลระบบ<span class="n">${users.length}</span></h2>
      <p>ทุกคนเข้าระบบด้วยชื่อผู้ใช้ รหัสผ่าน และรหัส 6 หลักจาก Google Authenticator ที่เปลี่ยนทุก 30 วินาที</p></div>
      <div class="page-h2-a"><button class="btn primary" data-u-add>${ic('plus')}เพิ่มผู้ดูแล</button></div></header>
    <div class="ptable" style="margin-top:16px">${users.length ? `<div class="table-wrap"><table class="t peers"><thead><tr><th>ชื่อ</th><th>สถานะ</th><th>เข้าระบบล่าสุด</th><th></th></tr></thead>
      <tbody>${users.map(row).join('')}</tbody></table></div>` : emptyCta('user', 'ยังไม่มีบัญชีผู้ดูแล', 'สร้างบัญชีของคุณก่อน แล้วระบบจะเลิกใช้ Admin token สำหรับเข้าหน้าเว็บ', null, '', null)}</div>
    <p class="mute" style="font-size:12.5px;margin:12px 2px 0">ถ้าทุกคนเข้าระบบไม่ได้: ssh เข้า server แล้วใช้คำสั่ง <code class="k" translate="no">sudo docker exec swim-backend-1 swim-server user reset-2fa ชื่อผู้ใช้</code> ระบบจะพิมพ์ลิงก์ตั้งค่าใหม่ให้</p>`;
}
let adminCache = [];
function showSetupLink(title, link, who) {
  const url = link.startsWith('http') ? link : location.origin + location.pathname + link.replace(/^\//, '');
  modal({ title, confirm: 'เสร็จแล้ว', wide: true,
    desc: `ส่งลิงก์นี้ให้ <b translate="no">${esc(who)}</b> โดยตรง (ไม่โพสต์ในกลุ่ม) ลิงก์ใช้ได้ครั้งเดียวภายใน 24 ชั่วโมง เจ้าตัวจะตั้งรหัสผ่านและสแกน QR ด้วย Google Authenticator เอง`,
    body: `<div class="link-box"><code translate="no" id="setup-url">${esc(url)}</code><button type="button" class="btn sm" id="setup-copy">${ic('copy')}คัดลอกลิงก์</button></div>`,
    onConfirm: async () => {} });
  $('#setup-copy').onclick = async () => { try { await navigator.clipboard.writeText(url); toast('คัดลอกลิงก์แล้ว'); } catch (e) { toast('คัดลอกไม่ได้ เลือกข้อความแล้วกด Ctrl+C', 'bad'); } };
}
// avatar menu: my account
function accountMenu(btn) {
  const open = $('#hmenu'); closeHostMenu(); if (open && open.dataset.for === 'me') return;
  const sess = me?.auth === 'session';
  const m = document.createElement('div'); m.id = 'hmenu'; m.className = 'hmenu'; m.setAttribute('role', 'menu'); m.dataset.for = 'me';
  m.innerHTML = `<div class="hm-who"><b translate="no">${esc(me?.display_name || me?.name || me?.username || '')}</b><small>${sess ? 'เข้าด้วยรหัสผ่าน + Google Authenticator' : 'เข้าด้วย Admin token'}</small></div>
    ${sess ? `<button role="menuitem" data-me="password">${ic('shield')}เปลี่ยนรหัสผ่าน</button><button role="menuitem" data-me="recovery">${ic('copy')}สร้างรหัสสำรองชุดใหม่</button>` : ''}
    <button role="menuitem" data-me="logout">${ic('x')}ออกจากระบบ</button>`;
  document.body.append(m);
  const r = btn.getBoundingClientRect();
  m.style.top = (r.bottom + 6 + scrollY) + 'px'; m.style.left = Math.max(8, r.right - m.offsetWidth + scrollX) + 'px';
  m.querySelector('button')?.focus();
  m.addEventListener('keydown', e => { if (e.key === 'Escape') { closeHostMenu(); btn.focus(); } });
  document.addEventListener('pointerdown', hostMenuAway, true);
}
ROUTES.settings.after = () => {
  const tab = settingsTab();
  if (tab === 'sites') {
    const f = $('#site-form'); if (!f) return;
    f.onsubmit = async e => {
      e.preventDefault();
      const name = f.elements.name.value.trim(); if (!name) return;
      try { await api('/v1/sites', 'POST', { name, description: f.elements.description.value.trim() }); toast('สร้าง site แล้ว'); renderPage(); }
      catch (err) { toast(err.message, 'bad'); }
    };
  }
};

async function settingsSites() {
  const sites = await api('/v1/sites'); siteCache = sites;
  return card('Site', 'กลุ่มของเครื่อง เช่น อาคาร ห้องแล็บ หรือแผนก — ใช้กรองรายการเครื่องและสั่งงานทั้งกลุ่มพร้อมกัน', `
    ${sites.length ? `<div class="list" style="margin:-16px -18px 16px">${sites.map(s => `<div class="list-row"><div class="dot-icon">${ic('layers')}</div>
      <div class="grow"><div class="title">${esc(s.name)}</div><div class="meta">${s.hosts} เครื่อง${s.description ? ' · ' + esc(s.description) : ''}</div></div>
      <button type="button" class="btn sm ghost" data-site-filter="${esc(s.id)}">ดูเครื่อง</button>
      <button type="button" class="btn sm" data-site-rename="${esc(s.id)}">แก้ไข</button>
      <button type="button" class="btn sm danger" data-site-delete="${esc(s.id)}" ${s.hosts ? 'disabled title="ย้ายเครื่องออกก่อน"' : ''}>ลบ</button></div>`).join('')}</div>`
      : `<div class="empty" style="padding:8px 0 18px">${ic('layers')}<b>ยังไม่มี site</b></div>`}
    <form id="site-form" style="display:flex;gap:10px;align-items:flex-end;flex-wrap:wrap">
      <div class="field" style="flex:1 1 200px;margin:0"><label>ชื่อ site ใหม่</label><input class="input" name="name" placeholder="เช่น อาคาร A, ห้องแล็บ 2"></div>
      <div class="field" style="flex:1 1 200px;margin:0"><label>คำอธิบาย (ถ้ามี)</label><input class="input" name="description"></div>
      <button class="btn primary" type="submit">${ic('plus')}สร้าง site</button></form>
    <p class="mute" style="font-size:12.5px;margin:12px 0 0">ย้ายเครื่องเข้า site: หน้า <a href="#/hosts">เครื่อง</a> → ติ๊กเลือกเครื่อง → "ย้าย site"</p>`);
}

function auditTable(rows) {
  return table(rows, [
    { h: 'เวลา', v: a => when(a.at), cls: 'nowrap' },
    { h: 'ผู้ทำ', v: a => esc(a.actor.replace(/^user:/, '')) },
    { h: 'การกระทำ', v: a => `<span class="mono">${esc(a.action)}</span>` },
    { h: 'ออบเจกต์', v: a => `${esc(a.object_type)} <span class="mono mute">${esc(a.object_id)}</span>` },
    { h: 'เหตุผล', v: a => a.reason ? esc(a.reason) : '<span class="mute">—</span>' },
  ], empty('audit', 'ยังไม่มีบันทึก'));
}
async function pageAudit() {
  const [rows, v] = await Promise.all([api('/v1/audit'), api('/v1/audit/verify')]);
  return `${v.ok
    ? `<div class="callout">${ic('link')}<div><b>Hash chain ถูกต้อง</b> — ตรวจครบ ${v.rows} รายการ ไม่มีรายการใดถูกแก้ไขหรือลบ</div></div>`
    : `<div class="callout bad">${ic('alert')}<div><b>Hash chain เสียหายที่รายการ #${esc(v.broken_at)}</b> — ${esc(v.problem)} มีการแก้ไขฐานข้อมูลโดยตรง ให้ตรวจสอบทันที</div></div>`}
  ${card('บันทึกล่าสุด', '200 รายการล่าสุด · แอปแก้หรือลบไม่ได้', auditTable(rows), '', true)}`;
}


// ───────── enroll (Add machine) ─────────
let enrollResult = null; // {token, expires_at, site, max_uses} — shown once, never stored
const trustCommand = pin => `$SwimServer='${location.origin}';$SwimPin='${pin}';` +
  `[Net.ServicePointManager]::SecurityProtocol='Tls12';` +
  `[Net.ServicePointManager]::ServerCertificateValidationCallback={param($a,$c) ([BitConverter]::ToString([Security.Cryptography.SHA256]::Create().ComputeHash($c.GetRawCertData())) -replace '-','') -eq '${pin}'};` +
  `iex (New-Object Net.WebClient).DownloadString($SwimServer+'/dl/agent/Set-SwimTrust.ps1')`;
const psCommand = (token, pin) => `$SwimServer='${location.origin}';$SwimToken='${token}';$SwimPin='${pin}';` +
  `[Net.ServicePointManager]::SecurityProtocol='Tls12';` +
  `[Net.ServicePointManager]::ServerCertificateValidationCallback={param($a,$c) ([BitConverter]::ToString([Security.Cryptography.SHA256]::Create().ComputeHash($c.GetRawCertData())) -replace '-','') -eq '${pin}'};` +
  `iex (New-Object Net.WebClient).DownloadString($SwimServer+'/dl/agent/install.ps1')`;
let enrollInfo = null;

async function pageEnroll() {
  const [info, tokens, sites] = await Promise.all([api('/v1/enroll/info'), api('/v1/enrollment-tokens'), api('/v1/sites')]);
  enrollInfo = info; siteCache = sites;
  const ag = info.agent;
  const head = r => `<div class="card-head"><div class="dot-icon ok">${ic('check')}</div><div><h2>${r.mode === 'msi' ? 'ดาวน์โหลดตัวติดตั้งแล้ว' : 'พร้อมติดตั้งแล้ว'}</h2>
      <p>${esc(r.site || 'ไม่ระบุ site')} · ใช้ติดตั้งได้ 1 เครื่อง</p></div>
      <div class="spacer"></div><button class="btn sm ghost" data-enroll-close>ปิด</button></div>`;
  const result = !enrollResult ? '' : enrollResult.mode === 'msi' ? `
  <section class="card" style="margin-bottom:16px;border-color:color-mix(in srgb,var(--ok) 40%,var(--line))">${head(enrollResult)}
    <div class="card-body"><div id="enroll-live">${enrollLiveHTML()}</div>
      <div class="msi-file"><div class="dot-icon accent" style="width:44px;height:44px">${ic('capture')}</div>
        <div><b class="mono">${esc(enrollResult.file)}</b><div class="mute" style="font-size:12.5px">${fmtBytes(enrollResult.size)} · อยู่ในโฟลเดอร์ Downloads ของเบราว์เซอร์นี้</div></div></div>
      <ol class="install-steps" style="margin-top:14px">
        <li>คัดลอกไฟล์ไปที่เครื่อง Windows (USB, แชร์ไดรฟ์ หรือดาวน์โหลดจากเว็บนี้บนเครื่องนั้นเลย)</li>
        <li><b>ดับเบิลคลิก</b> ไฟล์ → กด <b>Yes</b> ที่หน้าต่าง UAC — ติดตั้งเสร็จในไม่กี่วินาที ไม่มีอะไรให้กรอก</li>
        <li>เครื่องจะขึ้นในหน้า <a href="#/hosts">เครื่องลูก</a> ภายใน 1–2 นาที (ถ้าตอนติดตั้งไม่มีเน็ต agent จะลงทะเบียนเองเมื่อเน็ตกลับมา)</li>
      </ol>
      <div class="callout warn" style="margin:0 0 12px">${ic('alert')}<div>ไฟล์นี้มี token ฝังอยู่ ใครได้ไฟล์ไปก็ลงทะเบียนเครื่องได้จนกว่าจะหมดอายุหรือครบจำนวน — ยกเลิกได้ที่ตารางด้านล่าง</div></div>
      <details class="adv" open><summary>ถ้า Windows / Edge เตือนว่าไฟล์ไม่ปลอดภัย (ยังไม่ได้เซ็นโค้ด)</summary>
        <ol class="install-steps" style="margin:6px 0 0">
          <li><b>Edge เตือนตอนดาวน์โหลด</b> ("Make sure you trust…"): กดลูกศรข้างปุ่ม <b>Delete</b> → <b>Keep</b> → <b>Show more</b> → <b>Keep anyway</b></li>
          <li><b>Chrome เตือน</b>: กด <b>Keep</b> / <b>Download suspicious file</b></li>
          <li><b>ตอนดับเบิลคลิก</b> ขึ้น "Windows protected your PC": กด <b>More info</b> → <b>Run anyway</b></li>
          <li>ไม่อยากเห็นคำเตือนเลย: ใช้ปุ่ม <b>"ใช้คำสั่ง PowerShell แทน"</b> — ไฟล์ที่ PowerShell ดาวน์โหลดไม่ผ่าน SmartScreen</li>
        </ol>
        <p class="mute" style="font-size:12.5px;margin:8px 0 0">แก้ถาวร: เซ็นไฟล์ด้วยใบรับรอง Code Signing หรือให้ IT ตั้ง policy ให้เครื่องในองค์กรเชื่อถือ server นี้</p></details>
      <details class="adv"><summary>ติดตั้งแบบเงียบ / GPO / Intune</summary>
        <pre class="code">msiexec /i ${esc(enrollResult.file)} /qn /l*v %TEMP%\swim-agent-install.log</pre>
        <p class="mute" style="font-size:12.5px;margin:8px 0 0">GPO: Computer Configuration → Software Installation → เพิ่มไฟล์ .msi นี้จาก share · Intune: Line-of-business app (.msi)</p></details>
    </div></section>` : `
  <section class="card" style="margin-bottom:16px;border-color:color-mix(in srgb,var(--ok) 40%,var(--line))">${head(enrollResult)}
    <div class="card-body"><div id="enroll-live">${enrollLiveHTML()}</div>
      <div class="callout warn">${ic('alert')}<div>คำสั่งนี้มี enrollment token ที่แสดง<b>ครั้งเดียว</b> — อย่าโพสต์ในที่สาธารณะ</div></div>
      <ol class="install-steps">
        <li>บนเครื่อง Windows คลิกขวาที่ Start → <b>Terminal (Admin)</b></li>
        <li>วางคำสั่งด้านล่างแล้วกด Enter</li>
        <li>เครื่องจะขึ้นในหน้า <a href="#/hosts">เครื่องลูก</a> ภายใน 1 นาที</li>
      </ol>
      <pre class="code" id="ps-cmd">${esc(psCommand(enrollResult.token, info.cert_sha256))}</pre>
      <div class="btn-group" style="margin-top:12px"><button class="btn primary" data-copy-cmd>${ic('check')}คัดลอกคำสั่ง</button></div>
    </div></section>`;

  const form = card('เพิ่มเครื่องใหม่', 'ดาวน์โหลดตัวติดตั้งสำหรับเครื่องหนึ่งเครื่อง', `
    <form id="enroll-form">
      <div class="field"><label for="en-site">เครื่องเหล่านี้อยู่ site ไหน</label>
        <div style="display:flex;gap:8px"><select class="select" id="en-site" name="site_id"><option value="">— ไม่ระบุ (เลือกทีหลังได้) —</option>
          ${sites.map(x => `<option value="${esc(x.id)}">${esc(x.name)}</option>`).join('')}</select>
          <button type="button" class="btn" data-new-site>${ic('plus')}Site</button></div>
        <span class="hint">Site คือกลุ่มของเครื่อง เช่น อาคาร ห้องแล็บ หรือแผนก — ย้ายภายหลังได้</span></div>
      <div class="btn-group">
        <button class="btn primary" type="submit" name="mode" value="msi" ${ag ? '' : 'disabled'}>${ic('capture')}ดาวน์โหลดตัวติดตั้ง (.msi)</button>
      </div>
      <p class="mute" style="font-size:12.5px;margin:12px 0 0">ไฟล์หนึ่งไฟล์ติดตั้งได้ 1 เครื่อง — ติดตั้งเสร็จแล้วไฟล์จะใช้ซ้ำไม่ได้
        · <button type="submit" name="mode" value="command" class="linkbtn" ${ag ? '' : 'disabled'}>ใช้คำสั่ง PowerShell แทนไฟล์ .msi</button></p>
      ${ag ? '' : `<div class="callout bad" style="margin-top:12px">${ic('alert')}<div>ยังไม่มีไฟล์ agent บน server — รัน <code class="k">scripts/build-agent.sh</code></div></div>`}
    </form>`);

  const how = card('ขั้นตอน', '', `<ol class="install-steps" style="margin:0">
      <li>กด <b>ดาวน์โหลดตัวติดตั้ง (.msi)</b></li>
      <li>นำไฟล์ไปที่เครื่อง Windows แล้ว<b>ดับเบิลคลิก</b> → กด <b>Yes</b> — เสร็จในไม่กี่วินาที</li>
      <li>เครื่องจะขึ้นในหน้า <a href="#/hosts">เครื่อง</a> ภายใน 1–2 นาที</li></ol>
    <details class="adv"><summary>ถ้า Windows เตือนว่าไฟล์ไม่ปลอดภัย</summary>
      <ol class="install-steps" style="margin:6px 0 0">
        <li><b>ตอนดาวน์โหลด</b>: กด <b>Keep</b> / <b>Keep anyway</b></li>
        <li><b>ตอนเปิดไฟล์</b> ("Windows protected your PC"): กด <b>More info</b> → <b>Run anyway</b></li></ol>
      <p class="mute" style="font-size:12.5px;margin:8px 0">ไม่อยากให้เตือนเลย: ให้ IT ตั้ง 3 ค่านี้ผ่าน GPO/Intune — ใบรับรอง
        <a href="/dl/agent/swim-server.cer" download>swim-server.cer</a> เป็น Trusted Root, <span class="mono">${esc(location.origin)}</span> เป็น Local intranet
        และใส่ <span class="mono">${esc(location.hostname)}</span> ใน SmartScreenAllowListDomains</p>
      <p class="mute" style="font-size:12.5px;margin:0 0 6px">เครื่องเดียว (นอกโดเมน): เปิด Terminal (Admin) แล้ววางคำสั่งนี้</p>
      <pre class="code" id="trust-cmd">${esc(trustCommand(info.cert_sha256))}</pre>
      <div class="btn-group" style="margin-top:8px"><button class="btn sm" data-copy-trust>${ic('check')}คัดลอกคำสั่ง</button></div></details>
    <p class="mute" style="font-size:12.5px;margin:10px 0 0">ติดตั้งหลายเครื่อง: ดาวน์โหลดตัวติดตั้งใหม่สำหรับแต่ละเครื่อง</p>`);

  const active = tokens.filter(t => t.active);
  const tokTable = table(active, [
    { h: 'Site', v: t => esc(t.site_name || 'ไม่ระบุ') },
    { h: 'สร้างโดย', v: t => esc((t.created_by || '').replace(/^u_/, '')) },
    { h: '', cls: 'actions', v: t => `<button class="btn sm danger" data-revoke="${esc(t.id)}">ยกเลิกไฟล์นี้</button>` },
  ]);
  return `<a class="back" href="#/hosts">${ic('undo')}กลับไปหน้าเครื่อง</a>${result}<div class="grid cols-2">${form}${how}</div>
    ${active.length ? `<div style="height:16px"></div>${card('ตัวติดตั้งที่ยังไม่ได้ใช้', 'ยกเลิกได้ถ้าไม่ใช้แล้วหรือไฟล์หลุดออกไป', tokTable, '', true)}` : ''}`;
}
ROUTES.enroll.after = () => {
  const f = $('#enroll-form'); if (!f) return;
  f.onsubmit = async e => {
    e.preventDefault();
    const d = Object.fromEntries(new FormData(f));
    const mode = e.submitter?.value || 'msi';
    const body = { site_id: d.site_id || null, ttl_hours: 720, max_uses: 1 }; // one installer = one machine
    const site = d.site_id ? $('#en-site').selectedOptions[0].textContent : '';
    const btns = f.querySelectorAll('[type=submit]'); btns.forEach(b => b.disabled = true);
    try {
      if (mode === 'msi') {
        const r = await fetch('/v1/enroll/msi', { method: 'POST', headers: { ...authHeaders(), 'Content-Type': 'application/json' },
          body: JSON.stringify({ ...body, server: location.origin }) });
        if (!r.ok) throw new Error((await r.json().catch(() => ({}))).error?.message || 'HTTP ' + r.status);
        const name = (r.headers.get('content-disposition') || '').match(/filename="([^"]+)"/)?.[1] || 'swim-agent.msi';
        const blob = await r.blob();
        const a = document.createElement('a'); a.href = URL.createObjectURL(blob); a.download = name;
        document.body.appendChild(a); a.click(); a.remove(); setTimeout(() => URL.revokeObjectURL(a.href), 2000);
        enrollResult = { mode: 'msi', file: name, size: blob.size, site, token_id: r.headers.get('x-swim-token-id'), started: Date.now() };
      } else {
        const r = await api('/v1/enrollment-tokens', 'POST', body);
        enrollResult = { mode: 'command', token: r.token, site, token_id: r.id, started: Date.now() };
      }
      await renderPage(); window.scrollTo({ top: 0, behavior: 'smooth' });
    } catch (err) { toast(err.message, 'bad'); btns.forEach(b => b.disabled = false); }
  };
  watchEnroll();
};
// live status of the installer just downloaded: waiting → machine connected (or refused)
let enrollWatch = null;
function enrollLiveHTML() {
  const r = enrollResult; if (!r || !r.token_id) return '';
  if (r.host) return `<div class="enroll-live ok">${ic('check')}<div><b>เครื่อง <a href="#/hosts/${encodeURIComponent(r.host.id)}">${esc(r.host.hostname || r.host.id)}</a> เชื่อมต่อแล้ว</b>
      <span>ติดตั้งสำเร็จ · ตัวติดตั้งนี้ใช้ไปแล้ว ไฟล์ใช้กับเครื่องอื่นไม่ได้อีก</span></div>
      <a class="btn sm primary" href="#/hosts/${encodeURIComponent(r.host.id)}">เปิดหน้าเครื่อง</a></div>`;
  if (r.refused) return `<div class="enroll-live bad">${ic('alert')}<div><b>เครื่อง ${esc(r.refused.hostname || '')} ติดตั้งไม่สำเร็จ</b>
      <span>${esc(r.refused.message === 'enrollment token used up' ? 'ไฟล์นี้ใช้กับเครื่องอื่นไปแล้ว — ดาวน์โหลดตัวติดตั้งใหม่สำหรับเครื่องนี้'
        : r.refused.message === 'enrollment token expired' ? 'ไฟล์นี้หมดอายุแล้ว — ดาวน์โหลดตัวติดตั้งใหม่' : r.refused.message)}</span></div></div>`;
  return `<div class="enroll-live"><span class="spin" aria-hidden="true"></span><div><b>รอติดตั้งบนเครื่อง Windows…</b>
      <span>ผ่านไป <span data-elapsed-since="${r.started}">${fmtClock((Date.now() - r.started) / 1000)}</span> · หน้านี้จะเปลี่ยนเองทันทีที่เครื่องเชื่อมต่อ</span></div></div>`;
}
function watchEnroll() {
  clearInterval(enrollWatch);
  if (!enrollResult || !enrollResult.token_id || enrollResult.host) return;
  enrollWatch = setInterval(async () => {
    if (route() !== 'enroll' || !enrollResult || enrollResult.host) { clearInterval(enrollWatch); return; }
    try {
      const st = await api('/v1/enrollment-tokens/' + encodeURIComponent(enrollResult.token_id));
      const was = !!enrollResult.host;
      enrollResult.host = st.host || null; enrollResult.refused = st.host ? null : st.refused || null;
      const box = $('#enroll-live'); if (box) box.innerHTML = enrollLiveHTML();
      if (!was && enrollResult.host) { toast(`เครื่อง ${enrollResult.host.hostname} เชื่อมต่อแล้ว`); clearInterval(enrollWatch); }
    } catch (e) {}
  }, 3000);
}

// ───────── machine detail ─────────
const CMD = {
  ping: ['ทดสอบการเชื่อมต่อ', 'link'], collect_logs: ['เก็บ Log และข้อมูลระบบ', 'log'], refresh_inventory: ['อัปเดตข้อมูลเครื่อง', 'refresh'],
  update_agent: ['อัปเดต Agent', 'capture'], reimage: ['ล้างเครื่องและติดตั้ง Windows', 'capture'], restart_agent: ['รีสตาร์ต Agent', 'reset'], reboot: ['รีบูตเครื่อง', 'power'],
  run_script: ['รันสคริปต์', 'console'], uninstall_agent: ['ถอนการติดตั้ง Agent', 'trash'], delete_staged_image: ['ลบ image ที่เก็บในเครื่อง', 'trash'], release_staging: ['คืนพื้นที่ที่ใช้ติดตั้ง', 'hdd'], remote_session: ['เปิดรีโมต (ไม่ใช้รหัส)', 'console'], rehandoff: ['บูตเข้า WinPE อีกครั้ง', 'refresh'], set_policy: ['ตั้งนโยบายเครื่อง', 'shield'], prestage_image: ['เก็บ image ไว้ในเครื่อง', 'hdd'], wake: ['ส่ง Wake-on-LAN', 'power'],
};
const CMD_STATUS = { queued: ['', 'รอเครื่องรับ'], delivered: ['info', 'ส่งถึงเครื่องแล้ว'], running: ['info', 'กำลังทำ'], done: ['ok', 'สำเร็จ'],
  failed: ['bad', 'ล้มเหลว'], expired: ['warn', 'หมดอายุ'], cancelled: ['', 'ยกเลิก'] };
// Remote Desktop state: measured by the agent when it reports inv.rdp, otherwise read from the latest open/close command
const isRdpOff = c => c.type === 'run_script' && c.args?.name === 'ปิด Remote Desktop';
function rdpState(h, cmds) {
  const r = inv(h).rdp;
  if (r && typeof r.enabled === 'boolean') {
    const seen = r.checked_at || h.inventory_at, when2 = seen ? `ตรวจจากเครื่องเมื่อ ${ago(seen)}` : 'ตรวจจากเครื่อง';
    if (!r.enabled && !r.firewall_open) return { tone: 'ok', label: 'ปิดอยู่', detail: when2, measured: true };
    const bits = [r.enabled ? 'เปิดรับการเชื่อมต่อ' : 'ปิดอยู่ แต่กฎ firewall ยังเปิด',
      r.firewall_open ? `firewall เปิดให้ ${(r.allowed_from || []).map(x => x === 'Any' ? 'ทุกเครื่อง' : x).join(', ') || 'เครื่องอื่น'}` : '',
      r.swim_account === 'enabled' ? 'บัญชี swim-remote ยังใช้งานได้' : '', r.sessions ? `มีคนเชื่อมต่ออยู่ ${r.sessions} คน` : ''].filter(Boolean);
    return { tone: 'warn', label: 'เปิดอยู่', detail: `${bits.join(', ')} (${when2})`, measured: true };
  }
  const c = cmds.find(x => isRdpOff(x) || x.type === 'remote_session');
  if (!c) return { tone: '', label: 'ยังไม่ทราบ', detail: 'agent รุ่นนี้ยังไม่รายงานสถานะ Remote Desktop และยังไม่เคยสั่งเปิดหรือปิดจาก SWIM' };
  const t = ago(c.finished_at || c.created_at), pending = ['queued', 'delivered', 'running'].includes(c.status);
  if (isRdpOff(c)) return pending ? { tone: 'info', label: 'กำลังปิด', detail: `ส่งคำสั่งปิดแล้ว ${t}, รอเครื่องตอบ` }
    : c.status === 'done' ? { tone: 'ok', label: 'สั่งปิดแล้ว', detail: `คำสั่งปิดสำเร็จ ${t} (ดูจากคำสั่งล่าสุด รอ agent รายงานสถานะจริง)` }
    : { tone: 'bad', label: 'ปิดไม่สำเร็จ', detail: `คำสั่งปิด${CMD_STATUS[c.status]?.[1] || c.status} ${t} ลองกดปิดอีกครั้ง` };
  return { tone: 'warn', label: 'เคยเปิดให้รีโมต', detail: `เปิดให้รีโมตล่าสุด ${t} (ดูจากคำสั่งล่าสุด) กดปิดเมื่อใช้เสร็จ` };
}
// pre-staging: the image a machine keeps in SWIMSTAGE so a reimage needs no big download
function prestageState(h) {
  const want = h.policy?.prestage_image_id || '', p = inv(h).prestage || {}, img = imageCache.find(x => x.id === want);
  const name = img?.version || p.version || '';
  if (!want) return p.state && p.state !== 'none' ? { tone: 'info', label: 'กำลังลบ image ที่เก็บไว้', detail: 'ตั้งเป็นไม่เก็บแล้ว เครื่องจะคืนพื้นที่ให้ C:' } : { tone: '', label: 'ไม่ได้เก็บ image ไว้', detail: 'ล้างเครื่องแต่ละครั้งจะดาวน์โหลด image จาก server' };
  const match = p.image_id === want || (img && p.version === img.version);
  if (match && p.state === 'ready') return { tone: 'ok', label: `เก็บ ${name} ไว้แล้ว`, detail: `พร้อมใช้ ล้างเครื่องได้โดยไม่ต้องดาวน์โหลด image${p.updated_at ? `, ตรวจล่าสุด ${ago(p.updated_at)}` : ''}`, name };
  if (match && p.state === 'downloading') return { tone: 'info', label: `กำลังดาวน์โหลด ${name} ${p.progress != null ? Math.round(p.progress) + '%' : ''}`.trim(), detail: 'ดาวน์โหลดเบื้องหลังและตรวจ SHA-256 ผู้ใช้ใช้เครื่องได้ตามปกติ', progress: p.progress, name };
  if (match && p.state === 'failed') return { tone: 'bad', label: `เก็บ ${name} ไม่สำเร็จ`, detail: p.error || 'ระบบจะลองใหม่เอง', name };
  return { tone: 'info', label: `รอเครื่องเริ่มเก็บ ${name}`, detail: h.online ? 'ส่งคำสั่งแล้ว รอเครื่องรับ' : 'เครื่องออฟไลน์ จะเริ่มเมื่อกลับมาออนไลน์', name };
}
const imageOptions = (sel, noneLabel) => {
  const chOf = x => channelCache.find(c => c.id === x.channel_id), pub = imageCache.filter(x => x.status === 'published');
  const gm = pub.filter(x => !isIsoChannel(chOf(x))), iso = pub.filter(x => isIsoChannel(chOf(x)));
  const o = x => `<option value="${esc(x.id)}" ${x.id === sel ? 'selected' : ''}>${esc(x.version)} (${esc(isIsoChannel(chOf(x)) ? 'Windows ' + (chOf(x)?.edition || '') : chOf(x)?.name || '')}, ${fmtBytes(x.size_bytes)})</option>`;
  return `<option value="" ${sel ? '' : 'selected'}>${esc(noneLabel)}</option>${gm.length ? `<optgroup label="จากเครื่องต้นแบบ">${gm.map(o).join('')}</optgroup>` : ''}${iso.length ? `<optgroup label="จากแผ่น ISO">${iso.map(o).join('')}</optgroup>` : ''}`;
};
const PRESTAGE_WHAT = `<p class="ps-what">เครื่องจะดาวน์โหลด image ที่เลือกมาเก็บในพาร์ทิชัน SWIMSTAGE ล่วงหน้าแบบเบื้องหลัง (C: เล็กลงเท่าขนาด image + 3 GB อย่างน้อย 20 GB)
  พอสั่งล้างเครื่อง ทั้งสั่งเองและตามตารางงาน จะใช้ไฟล์ในเครื่องเลย ไม่ต้องรอดาวน์โหลดหลาย GB จาก server</p>`;
function askPrestage(ids, label, current = '') {
  const img = id => imageCache.find(x => x.id === id);
  modal({ title: 'เก็บ image ไว้ในเครื่องล่วงหน้า', confirm: 'บันทึก', wide: true, desc: `ตั้งค่าให้ <b translate="no">${esc(label)}</b>`,
    body: `${PRESTAGE_WHAT}<div class="field"><label>Image ที่จะเก็บไว้</label><select class="select" name="prestage_image_id" id="ps-pick">${imageOptions(current, 'ไม่เก็บ (ลบที่เก็บไว้และคืนพื้นที่ให้ C:)')}</select></div>
      <div class="callout" id="ps-note">${ic('info')}<div></div></div>`,
    onConfirm: async f => {
      const id = f.prestage_image_id;
      if (ids.length === 1) { await api('/v1/hosts/' + encodeURIComponent(ids[0]) + '/policy', 'PUT', { prestage_image_id: id }); toast(id ? `ตั้งให้เก็บ ${img(id)?.version || ''} แล้ว เครื่องจะดาวน์โหลดเบื้องหลัง` : 'ตั้งเป็นไม่เก็บแล้ว'); }
      else { const r = await api('/v1/hosts/policy', 'POST', { host_ids: ids, prestage_image_id: id });
        toast(`ตั้งแล้ว ${r.updated ?? ids.length} เครื่อง${r.skipped?.length ? `, ข้าม ${r.skipped.length}: ${r.skipped.map(x => (hostList.find(h => h.id === x.host_id)?.hostname || x.host_id) + ' ' + x.reason).join('; ')}` : ''}`, r.skipped?.length ? 'bad' : 'ok'); hostSel.clear(); }
      renderPage();
    } });
  const pick = $('#ps-pick'), note = $('#ps-note div');
  const sync = () => { const x = img(pick.value); note.textContent = x ? `ใช้พื้นที่ดิสก์ในเครื่องประมาณ ${fmtBytes(x.size_bytes + 3 * 2 ** 30)} ผู้ใช้ใช้เครื่องได้ตามปกติระหว่างดาวน์โหลด` : 'เครื่องจะลบ image ที่เก็บไว้และคืนพื้นที่ทั้งหมดให้ไดรฟ์ C:'; };
  pick.onchange = sync; sync();
}
const stateBox = (what, st) => `<div class="rdp-state ${st.tone}" role="status"><span class="rdp-ic">${ic(st.tone === 'ok' ? 'shield' : st.tone === 'warn' || st.tone === 'bad' ? 'alert' : 'info')}</span>
  <div><b>${esc(what)}: ${esc(st.label)}</b><small>${esc(st.detail)}</small></div></div>`;
const rdpBadge = st => stateBox('Remote Desktop', st);
// install lock: desired on the host (h.policy), measured by the agent (inv.policy)
function lockState(h) {
  const want = !!h.policy?.install_lock, m = inv(h).policy?.install_lock || null;
  const seen = m?.checked_at ? `ตรวจจากเครื่องเมื่อ ${ago(m.checked_at)}` : '';
  const tail = x => [x, seen].filter(Boolean).join(', ');
  if (want && m?.error) return { tone: 'bad', label: 'ใช้นโยบายไม่สำเร็จ', short: 'ล้มเหลว', on: true, detail: tail(m.error) };
  if (want && m?.applied && m.desired) return { tone: 'ok', label: 'ล็อกแล้ว', short: 'ล็อกแล้ว', on: true,
    detail: tail(`ติดตั้งโปรแกรมได้เฉพาะจาก SWIM${m.admins?.length ? `, ผู้ดูแลในเครื่อง: ${m.admins.join(', ')}` : ''}`) };
  if (want) return { tone: 'info', label: 'กำลังใช้นโยบาย', short: 'กำลังล็อก', on: true,
    detail: tail(m?.problem || (h.online ? 'ส่งให้เครื่องแล้ว รอเครื่องยืนยัน' : 'เครื่องออฟไลน์อยู่ จะใช้นโยบายเมื่อกลับมาออนไลน์')) };
  if (m?.locked || (m?.desired && !m?.applied)) return { tone: 'info', label: 'กำลังปลดล็อก', short: 'กำลังปลด', detail: tail(m?.problem || (h.online ? 'ส่งให้เครื่องแล้ว รอเครื่องยืนยัน' : 'จะปลดเมื่อเครื่องกลับมาออนไลน์')) };
  return { tone: '', label: 'ยังไม่ล็อก', short: 'ยังไม่ล็อก', detail: 'ผู้ใช้ที่เป็นผู้ดูแลเครื่องยังติดตั้งโปรแกรมเองได้' };
}
// accounts that lose admin rights when the lock is applied (kept: built-in Administrator, swim-remote, the reimage admin)
function lockLosers(h) {
  const admins = inv(h).policy?.install_lock?.admins;
  if (!admins) return null;
  const keep = new Set(['administrator', 'swim-remote', ...(h.policy_keep || []).map(x => String(x).toLowerCase())]);
  return admins.filter(a => !keep.has(String(a).split('\\').pop().toLowerCase()));
}
const LOCK_WHAT = `<ul class="remote-pts"><li>${ic('check')}<span>ผู้ใช้ทุกคนเป็นบัญชีธรรมดา กดติดตั้ง .exe หรือ .msi แล้ว Windows ปฏิเสธทันที ไม่ถามรหัส</span></li>
  <li>${ic('check')}<span>ปิด Microsoft Store</span></li><li>${ic('check')}<span>SWIM ยังติดตั้งโปรแกรม อัปเดต และล้างเครื่องได้ตามปกติ</span></li>
  <li>${ic('check')}<span>ปลดล็อกได้ทุกเมื่อ ระบบคืนสิทธิ์ผู้ดูแลให้บัญชีเดิม</span></li></ul>`;
function askLock(ids, label, on) {
  const h = ids.length === 1 && hostCache?.id === ids[0] ? hostCache : null, losers = on && h ? lockLosers(h) : null;
  const who = !on ? (h && inv(h).policy?.install_lock?.removed?.length ? `<p style="margin:10px 0 0">ได้สิทธิ์ผู้ดูแลคืน: <b>${inv(h).policy.install_lock.removed.map(esc).join(', ')}</b></p>` : '')
    : losers === null ? '<p class="mute" style="margin:10px 0 0;font-size:13px">บัญชีที่ยังเป็นผู้ดูแล: Administrator ในตัว, swim-remote และบัญชีผู้ดูแลที่ตั้งตอนล้างเครื่องด้วย SWIM ครั้งล่าสุด</p>'
    : losers.length ? `<div class="callout warn" style="margin:10px 0 0">${ic('alert')}<div>บัญชีที่จะเสียสิทธิ์ผู้ดูแล: <b>${losers.map(esc).join(', ')}</b>${h.policy_keep ? '' : '<br><span class="mute">ยกเว้นบัญชีผู้ดูแลที่ตั้งตอนล้างเครื่องด้วย SWIM ครั้งล่าสุด ซึ่งยังเป็นผู้ดูแลต่อ</span>'}</div></div>`
    : '<p class="mute" style="margin:10px 0 0;font-size:13px">ไม่มีบัญชีไหนเสียสิทธิ์ผู้ดูแล</p>';
  modal({ title: on ? 'ล็อกการติดตั้งโปรแกรม?' : 'ปลดล็อกการติดตั้งโปรแกรม?', confirm: on ? 'ล็อก' : 'ปลดล็อก',
    desc: on ? `ใช้กับ <b>${esc(label)}</b> เครื่องที่ออฟไลน์จะได้รับเมื่อกลับมาออนไลน์` : `ผู้ใช้ที่เคยเป็นผู้ดูแลบน <b>${esc(label)}</b> จะได้สิทธิ์คืน และติดตั้งโปรแกรมเองได้อีก`,
    body: (on ? LOCK_WHAT + '<p class="mute" style="margin:10px 0 0;font-size:13px">โปรแกรมแบบไม่ต้องติดตั้ง (portable) ยังเปิดได้</p>' : '') + who,
    onConfirm: async () => {
      const r = ids.length === 1 ? await api('/v1/hosts/' + encodeURIComponent(ids[0]) + '/policy', 'PUT', { install_lock: on })
        : await api('/v1/hosts/policy', 'POST', { host_ids: ids, install_lock: on });
      const n = r?.updated ?? 1, sk = r?.skipped?.length || 0;
      toast(`${on ? 'ส่งนโยบายล็อก' : 'ส่งคำสั่งปลดล็อก'}แล้ว ${n} เครื่อง${sk ? `, ข้าม ${sk} เครื่อง` : ''}`); hostSel.clear(); renderPage();
    } });
}
const cmdBadge = s => `<span class="badge ${CMD_STATUS[s]?.[0] || ''}${s === 'running' || s === 'delivered' ? ' pulse' : ''}">${esc(CMD_STATUS[s]?.[1] || s)}</span>`;
let hostTab = 'overview';
let hostBusy = false, hostCache = null, scriptCache = [], cmdCache = [], imageCache = [], jobCache = [];

// images kept on the machine's SWIMSTAGE partition (from the agent's inventory)
const MIN_STAGE_DELETE = '0.5.9', MIN_RELEASE = '0.5.13';
function stagedSection(h, activeJob, retired) {
  const st = inv(h).staging;
  if (!st) return h.inventory && !retired ? `<div style="height:16px"></div>${card('พื้นที่ติดตั้ง Windows', '',
    `<div class="staging-clean">${ic('check')}<div><b>ไม่มีข้อมูลค้างในเครื่อง</b>
      <p>หลังติดตั้ง Windows สำเร็จ ระบบลบพาร์ทิชันชั่วคราว (SWIMSTAGE) และคืนพื้นที่ทั้งหมดให้ไดรฟ์ C: เอง</p></div></div>`, '', true)}` : '';
  const canRelease = h.agent_version && verCmp(h.agent_version, MIN_RELEASE) >= 0;
  const relWhy = retired ? 'เครื่องนี้ถูกลบออกจากระบบแล้ว' : !canRelease ? `ต้องใช้ agent ${MIN_RELEASE} ขึ้นไป` : activeJob ? 'มีงานติดตั้ง Windows ค้างอยู่' : '';
  const release = `<button class="btn sm" data-release-staging="${esc(h.id)}" data-size="${st.size_bytes || 0}" ${relWhy ? `disabled title="${esc(relWhy)}"` : ''}>${ic('hdd')}คืนพื้นที่ที่ใช้ติดตั้ง</button>`;
  const used = (st.size_bytes || 0) - (st.free_bytes || 0), list = st.images || [];
  const canDel = h.agent_version && verCmp(h.agent_version, MIN_STAGE_DELETE) >= 0;
  const why = retired ? 'เครื่องนี้ถูกลบออกจากระบบแล้ว' : !canDel ? `ต้องใช้ agent ${MIN_STAGE_DELETE} ขึ้นไป (เครื่องนี้ ${h.agent_version || '—'})`
    : activeJob ? 'มีงานติดตั้ง Windows ค้างอยู่' : '';
  const verify = x => x.sha256_ok === true ? '<span class="badge ok">ตรวจแล้ว ไฟล์ครบ</span>'
    : x.sha256_ok === false ? '<span class="badge bad">ไฟล์ถูกแก้ไขหลังตรวจ</span>' : '<span class="badge plain">ไม่มีบันทึกการตรวจ</span>';
  return `<div style="height:16px"></div>${card('Image ที่เก็บในเครื่อง', `พาร์ทิชัน SWIMSTAGE ${esc(st.letter || '')} — ใช้ไป ${fmtBytes(used)} จาก ${fmtBytes(st.size_bytes)} · ว่าง ${fmtBytes(st.free_bytes)}`,
    `<div style="padding:12px 18px 0">${meter(st.size_bytes ? used / st.size_bytes * 100 : 0, 'ok')}</div>
    ${table(list, [
      { h: 'เวอร์ชัน', v: x => `<b class="mono" style="font-weight:500">${esc(x.version)}</b>${x.in_use ? ' <span class="badge info plain">ใช้กับงานติดตั้งที่ยังไม่จบ</span>' : ''}` },
      { h: 'ขนาด', v: x => fmtBytes(x.size_bytes), cls: 'nowrap' },
      { h: 'ความถูกต้อง', v: verify },
      { h: '', cls: 'actions', v: x => `<button class="btn sm danger" data-staged-delete="${esc(x.version)}" data-size="${x.size_bytes || 0}"
          ${retired || !canDel || activeJob || x.in_use ? `disabled title="${esc(x.in_use ? 'งานติดตั้งที่ยังไม่จบต้องใช้ image นี้' : why)}"` : ''}>${ic('trash')}ลบ</button>` },
    ], `<div class="empty" style="padding:18px">${ic('hdd')}<b>ไม่มี image เก็บในเครื่อง</b><div>หลังติดตั้ง Windows สำเร็จ ระบบลบไฟล์ที่ไม่ใช้ออกเองอัตโนมัติ</div></div>`)}
    ${why && list.length ? `<div class="mute" style="font-size:12.5px;padding:10px 18px">${ic('info', 'style="width:13px;height:13px;vertical-align:-2px"')} ${esc(why)}</div>` : ''}`, release, true)}`;
}

async function pageHost(id) {
  const [h, cmds, scripts, sites, chs, imgs, info, jobs] = await Promise.all([api('/v1/hosts/' + encodeURIComponent(id)),
    api('/v1/hosts/' + encodeURIComponent(id) + '/commands'), api('/v1/scripts'), api('/v1/sites'), api('/v1/channels'), api('/v1/images'),
    api('/v1/enroll/info').catch(() => ({})), api('/v1/hosts/' + encodeURIComponent(id) + '/jobs')]);
  jobCache = jobs;
  hostCache = h; cmdCache = cmds; scriptCache = scripts.filter(x => x.status !== 'retired'); siteCache = sites; channelCache = chs;
  imageCache = imgs; latestAgent = info.agent?.version || latestAgent;
  hostBusy = cmds.some(c => ['queued', 'delivered', 'running'].includes(c.status));
  const i = inv(h), o = i.os || {}, retired = !!h.retired_at, dis = retired ? 'disabled' : '';
  const fact = (k, v) => `<div class="fact"><span>${esc(k)}</span><b>${v}</b></div>`;
  const yes = (v, ok = 'เปิด', no = 'ปิด') => v === true ? `<span style="color:var(--ok)">${ok}</span>` : v === false ? `<span style="color:var(--bad)">${no}</span>` : '—';
  const reviewBlock = type => h.review_at && !['ping', 'collect_logs', 'refresh_inventory', 'uninstall_agent'].includes(type) ? 'disabled title="รับเครื่องกลับเข้าระบบก่อน"' : '';
  const btn = (type, cls = '') => `<button class="btn ${cls}" data-cmd="${type}" ${dis || reviewBlock(type)}>${ic(CMD[type][1])}${CMD[type][0]}</button>`;
  const outdated = latestAgent && h.agent_version && verCmp(h.agent_version, latestAgent) < 0;
  const al = hostAlerts(h), rdp = rdpState(h, cmds), lock = lockState(h);

  const summary = c => {
    const r = c.result || {};
    if (r.error) return `<span style="color:var(--bad)">${esc(r.error.slice(0, 140))}</span>`;
    if (c.type === 'ping' && r.data) return `agent ${esc(r.data.agent_version)} · นาฬิกาเครื่องต่างจาก server ${esc(r.data.clock_skew_s)} วินาที`;
    if (r.output) return `<span class="mono" style="font-size:12px">${esc(r.output.split('\n').filter(Boolean).slice(-1)[0] || '').slice(0, 140)}</span>`;
    return '<span class="mute">—</span>';
  };

  // ── Windows image section
  const ch = chs.find(c => c.id === h.channel_id);
  const activeJob = jobs.find(j => !['completed', 'failed', 'aborted', 'cancelled'].includes(j.status));
  const canReimage = h.agent_version && verCmp(h.agent_version, MIN_REIMAGE) >= 0;
  const chImgs = imgs.filter(x => x.channel_id === h.channel_id && ['published', 'pending'].includes(x.status));
  const isoImgs = imgs.filter(x => x.status === 'published' && x.channel_id !== h.channel_id && isIsoChannel(chs.find(c => c.id === x.channel_id)));
  const deployCols = [
    { h: 'Version', v: x => `<b class="mono" style="font-weight:500">${esc(x.version)}</b>${isIsoChannel(chs.find(c => c.id === x.channel_id)) ? `<div class="mute" style="font-size:12px">Windows ${esc(chs.find(c => c.id === x.channel_id)?.edition || '')}</div>` : ''}` },
    { h: 'สถานะ', v: x => badge(x.status) },
    { h: 'ขนาด', v: x => fmtBytes(x.size_bytes) },
    { h: 'สร้างเมื่อ', v: x => when(x.created_at), cls: 'nowrap' },
    { h: '', cls: 'actions', v: x => x.status !== 'published' ? '<span class="mute" style="font-size:12px">รออนุมัติ</span>'
        : `<button class="btn sm danger" data-deploy="${esc(x.id)}" ${retired || activeJob || !canReimage || h.review_at ? 'disabled' : ''}>${ic('capture')}ล้างเครื่องและติดตั้ง</button>` },
  ];
  const isoBlock = isoImgs.length ? `<div class="src-head">${ic('hdd')}<b>จากแผ่น ISO</b><span class="mute">Windows ต้นฉบับ ไม่มีโปรแกรมที่ติดตั้งไว้ในเครื่องต้นแบบ</span></div>${table(isoImgs, deployCols)}` : '';
  const imageSection = card('Windows Image', 'สิ่งที่ติดตั้งอยู่ และ image ที่จะใช้เมื่อสั่งล้างเครื่อง', `
    <div class="facts" style="padding:0 0 14px">
      ${fact('Windows บนเครื่องนี้', osShort(h) ? esc(osShort(h)) : '—')}
      ${fact('Build', o.build ? `<span class="mono">${esc(osBuild(h))}</span>` : '—')}
      ${fact('ติดตั้ง Windows เมื่อ', o.install_date ? esc(fmtDate(o.install_date)) : '—')}
      ${(() => { const x = installedOf({ ...(hostList.find(y => y.id === h.id) || {}), ...h }); return fact('Image จาก SWIM', x ? `<span translate="no">${esc(x.version)}</span> <span class="mute">${x.iso ? 'ISO' : 'Image'}${x.at ? ', ' + esc(fmtDate(x.at)) : ''}</span>` : 'ยังไม่เคยติดตั้ง'); })()}
    </div>
    <div class="field" style="max-width:360px"><label>หัวข้อ image ของเครื่องนี้</label>
      <select class="select" id="host-channel" ${dis}><option value="">— ยังไม่กำหนด —</option>${chs.map(c => `<option value="${esc(c.id)}" ${c.id === h.channel_id ? 'selected' : ''}>${esc(c.name)} · ${esc(c.edition)}</option>`).join('')}</select></div>
    ${isoImgs.length ? `<div class="src-head">${ic('builder')}<b>จากเครื่องต้นแบบ</b><span class="mute">หัวข้อ ${esc(ch?.name || 'ยังไม่กำหนด')} · Windows พร้อมโปรแกรมที่เตรียมไว้</span></div>` : ''}
    ${h.channel_id ? (chImgs.length ? table(chImgs, deployCols) : `<div class="callout warn">${ic('alert')}<div>หัวข้อ <b>${esc(ch?.name || '')}</b> ยังไม่มีเวอร์ชันที่อนุมัติแล้ว — สร้างและอนุมัติที่หน้า <a href="#/images">Images</a> ก่อน</div></div>`)
      : `<div class="callout">${ic('info')}<div>เลือกหัวข้อ image ให้เครื่องนี้ก่อน (หรือติ๊กหลายเครื่องในหน้ารายการแล้วกด "ตั้งหัวข้อ image") เพื่อดูว่าจะติดตั้งเวอร์ชันไหนได้</div></div>`}
    ${isoBlock}
    ${!canReimage && !retired ? `<div class="callout warn" style="margin:14px 0 0">${ic('alert')}<div>ต้องใช้ agent ${MIN_REIMAGE} ขึ้นไปจึงจะล้างเครื่องได้ (เครื่องนี้ ${esc(h.agent_version || '—')}) — กด <b>อัปเดต Agent</b> ด้านบนก่อน</div></div>` : ''}
    ${jobsPanel(jobs)}`);

  // ── hardware / disks / network / security
  const disks = (i.disks || []).map(d => `<div class="disk-row"><b>${esc(d.drive)}</b> <span class="mute">${esc(d.label || '')} ${esc(d.fs || '')}</span>
      <span style="margin-left:auto">ว่าง ${fmtBytes(d.free)} / ${fmtBytes(d.size)}</span>${meterBar(d.size - d.free, d.size).replace('width:110px', 'width:100%')}</div>`).join('');
  const phys = (i.physical_disks || []).map(p => `<div class="mute" style="font-size:12.5px">${esc(p.model)} · ${esc(p.media || '')} ${esc(p.bus || '')} · ${fmtBytes(p.size)} · ${esc(p.health || '')}</div>`).join('');
  const nics = (i.network || []).map(n => `<div class="nic"><b>${esc(n.name)}</b> <span class="mute">${esc(n.description || '')}</span>
      <div class="mono" style="font-size:12.5px">${(n.ipv4 || []).map(esc).join(', ') || '—'} · MAC ${esc(n.mac || '—')} · ${esc(n.speed || '')}</div>
      <div class="mute" style="font-size:12px">Gateway ${esc((n.gateway || []).join(', ') || '—')} · DNS ${esc((n.dns || []).join(', ') || '—')}</div></div>`).join('');
  const mem = i.memory || {};
  const fw = (i.firewall || []).map(f => `${esc(f.profile)} ${f.enabled ? '✓' : '✗'}`).join(' · ');
  const noInv = !h.inventory ? `<div class="callout" style="margin:0">${ic('info')}<div>ยังไม่มีข้อมูลละเอียด — ${outdated ? `agent ${esc(h.agent_version)} ยังไม่ส่งข้อมูลชุดนี้ กด <b>อัปเดต Agent</b>` : 'agent จะส่งข้อมูลมาเองภายใน 2 นาที'}</div></div>` : '';

  const running = cmds.filter(c => ['queued', 'delivered', 'running'].includes(c.status)).length;
  const cmdCols = [
    { h: 'คำสั่ง', v: c => `<b style="font-weight:500">${esc(CMD[c.type]?.[0] || c.type)}</b>${c.args && c.args.name ? `<div class="mute" style="font-size:12px" translate="no">${esc(c.args.name)}</div>` : ''}` },
    { h: 'สถานะ', v: c => cmdBadge(c.status) + (['queued', 'delivered', 'running'].includes(c.status)
        ? `<div class="mute" style="font-size:12px;margin-top:3px">${c.status === 'queued' ? 'ส่งแล้ว รอเครื่องรับ' : c.status === 'delivered' ? 'เครื่องรับแล้ว' : 'กำลังทำ'},
           <span data-elapsed-since="${Date.parse(c.status === 'queued' ? c.created_at : (c.delivered_at || c.created_at))}">—</span></div>` : '') },
    { h: 'ผลลัพธ์', v: summary },
    { h: 'สั่งโดย', v: c => esc(actorName(c.created_by)) },
    { h: 'เวลา', v: c => when(c.created_at), cls: 'nowrap' },
    { h: '', cls: 'actions', v: c => (c.status === 'queued' ? `<button class="btn sm ghost" data-cmd-cancel="${esc(c.id)}">ยกเลิก</button>` : '') +
        (c.result ? `<button class="btn sm" data-cmd-out="${esc(c.id)}">${ic('log')}ดูผล</button>` : '') },
  ];
  const clearBtn = cmds.some(c => !['queued', 'delivered', 'running'].includes(c.status))
    ? `<button class="btn sm ghost" data-clear-history="${esc(h.id)}" data-label="${esc(h.hostname || h.id)}">${ic('trash')}ล้างประวัติ</button>` : '';
  // tabs keep one screen per job: overview (act now), Windows (install), details (inventory), tools, history
  const tabs = [
    ['overview', 'ภาพรวม', ''],
    ['windows', 'Windows', activeJob ? '<i class="ht-dot busy"></i>' : ''],
    ['info', 'ข้อมูลเครื่อง', ''],
    ['tools', 'เครื่องมือ', ''],
    ['history', 'ประวัติคำสั่ง', `<span class="ht-n">${running ? `<i class="ht-dot busy"></i>${running}` : cmds.length}</span>`],
  ];
  if (!tabs.some(t => t[0] === hostTab)) hostTab = 'overview';
  const panel = (k, html) => `<div class="htab-panel" data-panel="${k}" role="tabpanel" ${k === hostTab ? '' : 'hidden'}>${html}</div>`;
  return `
  <div style="margin-bottom:12px"><a href="#/hosts" class="btn sm ghost">← เครื่องทั้งหมด</a></div>
  ${h.removing_at && !retired ? `<div class="callout warn">${ic('clock')}<div><b>กำลังถอด Agent ออกจากเครื่องนี้</b> — ส่งคำสั่งไปแล้ว ${when(h.removing_at)}
      เครื่องจะถูกลบออกจากระบบเมื่อ Agent ยืนยัน (ถ้าไม่ตอบใน 15 นาที ระบบจะลบออกเอง และถ้า Agent กลับมาภายหลังจะถูกสั่งให้ถอนตัวเอง)</div></div>` : ''}
  ${retired ? `<div class="callout bad">${ic('alert')}<div style="flex:1"><b>เครื่องนี้ถูกถอดออกจาก SWIM แล้ว</b> — ${esc(h.retired_reason || '')} · ติดตั้ง MSI ใหม่เพื่อกลับเข้าระบบ</div>
    <button class="btn sm danger" data-host-delete="${esc(h.id)}" data-label="${esc(h.hostname || h.id)}">${ic('trash')}ลบถาวร</button></div>` : ''}
  <section class="card" style="margin-bottom:16px">
    <div class="vm-head"><div class="vm-ico">${ic('hosts')}</div>
      <div class="vm-title"><div style="display:flex;gap:10px;align-items:center;flex-wrap:wrap"><b>${esc(h.hostname || h.id)}</b>${presence(h)}
          ${al.map(([l, t]) => `<span class="badge ${l} plain">${esc(t)}</span>`).join('')}${rdp.measured && rdp.tone === 'warn' ? '<span class="badge warn plain">Remote Desktop เปิดอยู่</span>' : ''}</div>
        <div class="meta"><span>${esc(hwModel(h) || 'ไม่ทราบรุ่น')}</span>${macOf(h) ? `<span translate="no">MAC ${esc(macOf(h).toUpperCase())}</span>` : '<span>ไม่ทราบ MAC</span>'}<span>${i.user ? 'ผู้ใช้: ' + esc(i.user) : 'ไม่มีผู้ใช้ล็อกอิน'}</span>
          <span title="agent ตรวจเครื่องเองทุก 2 นาที และส่งข้อมูลเมื่อมีอะไรเปลี่ยน">ตรวจอัตโนมัติทุก 2 นาที, ข้อมูลล่าสุด ${h.inventory_at ? esc(ago(h.inventory_at)) : 'ยังไม่มี'}</span></div></div>
      <div style="flex:1"></div>
      <div class="btn-group">${h.online && !retired && !activeJob ? `<button class="btn primary" data-remote>${ic('console')}รีโมตเข้าเครื่อง</button>` : ''}${(() => { const why = retired ? 'เครื่องนี้ถูกลบออกแล้ว' : h.online ? 'เครื่องเปิดอยู่แล้ว' : !macOf(h) ? 'ยังไม่รู้ MAC address ของเครื่องนี้' : activeJob ? 'มีงานติดตั้ง Windows ค้างอยู่' : '';
          return `<button class="btn ${why ? '' : 'primary'}" data-wake="${esc(h.id)}" data-label="${esc(h.hostname || h.id)}" ${why ? `disabled title="${esc(why)}"` : 'title="ส่งสัญญาณ Wake-on-LAN"'}>${ic('power')}ปลุกเครื่อง</button>`; })()}${btn('ping')}${outdated ? btn('update_agent', 'primary') : ''}</div></div>
    ${wakeBox(h) ? `<div style="padding:0 18px 14px">${wakeBox(h)}</div>` : ''}
    <div class="facts">
      ${fact('Site', `<select class="select sm" id="host-site" ${dis}><option value="">— ไม่ระบุ —</option>${sites.map(s => `<option value="${esc(s.id)}" ${s.id === h.site_id ? 'selected' : ''}>${esc(s.name)}</option>`).join('')}</select>`)}
      ${fact('Agent', `${esc(h.agent_version || '—')}${outdated ? ` <span class="badge info plain">มี ${esc(latestAgent)}</span>` : ''}
        <small class="fact-note">${outdated ? 'อัปเดตอัตโนมัติเมื่อเครื่องติดต่อเข้ามา (ไม่ทำระหว่างติดตั้ง Windows)' : 'อัปเดตอัตโนมัติเมื่อมีเวอร์ชันใหม่'}</small>`)}
      ${fact('ติดตั้งโปรแกรม', `<button class="linkish ${lock.tone}" data-htab="windows">${esc(lock.short)}</button>`)}
      ${fact('ติดต่อล่าสุด', h.online ? '<span style="color:var(--ok)">ตอนนี้</span>' : when(h.last_seen))}
      ${fact('เปิดเครื่องมา', fmtUptime(i.uptime_s))}
      ${fact('IP', `<span class="mono">${hostIPs(h).map(esc).join(', ') || '—'}</span>`)}
      ${fact('MAC', `<span class="mono" translate="no">${(inv(h).network || []).filter(n => n.mac).map(n => esc(n.mac.toUpperCase())).join('<br>') || '—'}</span>`)}
      ${fact('โดเมน / Workgroup', i.domain ? `${esc(i.domain.name)}${i.domain.joined ? ' (domain)' : ''}` : '—')}
      ${fact('ที่มา', h.install_source === 'manual_install' ? 'ลงเอง (MSI/USB)' : 'SWIM')}
      ${fact('ลงทะเบียนเมื่อ', esc(fmtDate(h.created_at)))}
    </div></section>
  <nav class="htabs" role="tablist" aria-label="ส่วนของหน้าเครื่อง">${tabs.map(([k, l, x]) => `<button role="tab" data-htab="${k}" class="${k === hostTab ? 'on' : ''}" aria-selected="${k === hostTab}">${l}${x}</button>`).join('')}</nav>
  ${panel('overview', `<div class="grid cols-2 ht-grid">
    ${card('สั่งงานเครื่องนี้', 'คำสั่งรอในคิวได้ 1 ชั่วโมง — เครื่องออฟไลน์จะได้รับเมื่อกลับมาออนไลน์', `
      <div class="cmd-group"><span>ตรวจสอบ</span><div class="btn-group">${btn('ping')}${btn('collect_logs')}</div></div>
      <div class="cmd-group"><span>เปลี่ยนแปลง</span><div class="btn-group">${btn('update_agent')}${btn('restart_agent')}${btn('reboot')}${btn('run_script')}</div></div>
      <div class="cmd-group"><span>ถอดออก</span><div class="btn-group">${btn('uninstall_agent', 'danger')}
        <button class="btn danger" data-retire ${retired || h.removing_at ? 'disabled' : ''}>${ic('x')}ลบเครื่องออกจากระบบ</button></div></div>`)}
    ${card('Remote Desktop', 'เห็นและควบคุมหน้าจอของเครื่องนี้ในเบราว์เซอร์', `<div class="remote-box">${rdpBadge(rdp)}${natWarn(h)}
      ${remotePwless(h) ? `<ul class="remote-pts"><li>${ic('check')}<span>กดแล้วเข้าหน้าจอเครื่องได้ทันที ไม่ต้องใส่รหัส</span></li>
        <li>${ic('check')}<span>เข้าด้วยบัญชีผู้ดูแลชั่วคราว <b>swim-remote</b> ที่ SWIM สร้างและปิดเองภายใน 10 นาที</span></li>
        <li>${ic('check')}<span>เปิด Remote Desktop ให้เฉพาะ SWIM server เครื่องอื่นในเครือข่ายเข้าไม่ได้</span></li><li>${ic('check')}<span>ทุกครั้งถูกบันทึกในประวัติการใช้งาน</span></li></ul>`
      : `<p>เชื่อมต่อผ่าน SWIM server เท่านั้น — เครื่องอื่นในเครือข่ายยังเข้า Remote Desktop ไม่ได้ · Windows จะถามรหัสผ่านตอนเชื่อมต่อ SWIM ไม่เก็บรหัสผ่าน · ทุกครั้งถูกบันทึกในประวัติการใช้งาน
        <br><span class="mute">agent ${esc(h.agent_version || '—')} — อัปเดตเป็น ${MIN_PWLESS} ขึ้นไป (อัตโนมัติ) แล้วจะเข้าได้โดยไม่ต้องใส่รหัส</span></p>`}
      <div class="btn-group"><button class="btn primary" data-remote ${retired || !h.online ? `disabled title="${retired ? 'เครื่องนี้ถูกลบออกแล้ว' : 'เครื่องออฟไลน์อยู่'}"` : ''}>${ic('console')}รีโมตเข้าเครื่อง</button>
        <button class="btn" data-preset="rdp-off" ${dis}>${ic('shield')}ปิด Remote Desktop</button></div></div>`)}
  </div>
    ${card('คำสั่งล่าสุด', running ? `กำลังทำ ${running} คำสั่ง` : 'อัปเดตสดเมื่อเครื่องตอบกลับ', table(cmds.slice(0, 5), cmdCols, empty('clock', 'ยังไม่มีคำสั่ง')),
      cmds.length > 5 ? `<button class="btn sm ghost" data-htab="history">ดูทั้งหมด ${cmds.length} รายการ</button>` : '', true)}`)}
  ${panel('windows', `${card('นโยบายเครื่อง', 'ควบคุมว่าใครติดตั้งโปรแกรมบนเครื่องนี้ได้', `<div class="remote-box">${stateBox('ติดตั้งโปรแกรม', lock)}${LOCK_WHAT}
      <div class="btn-group">${lock.on ? `<button class="btn" data-lock="off" ${dis}>${ic('x')}ปลดล็อก</button>` : `<button class="btn primary" data-lock="on" ${dis}>${ic('shield')}ล็อกการติดตั้งโปรแกรม</button>`}</div></div>`)}
    ${(() => { const ps = prestageState(h); return card('เก็บ image ไว้ในเครื่องล่วงหน้า', 'ล้างเครื่องได้ทันทีโดยไม่ต้องดาวน์โหลด image จาก server', `<div class="remote-box">${stateBox('Image', ps)}
      ${ps.progress != null ? `<div class="ps-bar"><i style="width:${Math.max(2, Math.min(100, ps.progress))}%"></i></div>` : ''}${PRESTAGE_WHAT}
      <div class="btn-group"><button class="btn ${h.policy?.prestage_image_id ? '' : 'primary'}" data-prestage="${esc(h.id)}" ${dis}>${ic('hdd')}${h.policy?.prestage_image_id ? 'เปลี่ยน image ที่เก็บไว้' : 'เลือก image ที่จะเก็บไว้'}</button></div></div>`); })()}
    ${imageSection}${stagedSection(h, activeJob, retired)}`)}
  ${panel('info', `  <div class="grid cols-2" style="margin-bottom:16px">
    ${card('ฮาร์ดแวร์', '', noInv || `<div class="facts" style="padding:0">
      ${fact('CPU', esc(i.cpu?.name || '—') + (i.cpu?.cores ? ` <span class="mute">(${i.cpu.cores} core / ${i.cpu.threads} thread)</span>` : ''))}
      ${fact('RAM', mem.total ? `${fmtBytes(mem.total)} <span class="mute">ว่าง ${fmtBytes(mem.free)}</span>` : '—')}
      ${fact('Serial', `<span class="mono">${esc(h.serial || '—')}</span>`)}
      ${fact('BIOS', esc(`${i.bios?.vendor || ''} ${i.bios?.version || ''}`.trim() || '—'))}
      ${fact('Firmware', esc((i.firmware || h.firmware || '—').toUpperCase()))}
      ${fact('SMBIOS UUID', `<span class="mono" style="font-size:11.5px">${esc(h.smbios_uuid || '—')}</span>`)}
      ${fact('แบตเตอรี่', i.battery ? i.battery.charge + '%' : 'ไม่มี')}
      ${fact('Time zone', esc(i.timezone || '—'))}</div>`)}
    ${card('ความปลอดภัย', '', noInv || `<div class="facts" style="padding:0">
      ${fact('Defender (realtime)', yes(i.defender?.realtime))}
      ${fact('ฐานข้อมูลไวรัส', i.defender ? `อายุ ${esc(i.defender.signature_age_days)} วัน` : '—')}
      ${fact('BitLocker (C:)', i.bitlocker ? (i.bitlocker.protection === 1 ? '<span style="color:var(--ok)">เปิด</span>' : '<span style="color:var(--warn)">ปิด</span>') : '—')}
      ${fact('Secure Boot', yes(i.secure_boot ?? h.secure_boot))}
      ${fact('TPM', i.tpm ? `${esc(i.tpm.version)} ${i.tpm.activated ? '' : '(ยังไม่ activate)'}` : esc(h.tpm_version || '—'))}
      ${fact('Firewall', fw || '—')}
      ${fact('รอรีสตาร์ต', i.pending_reboot ? '<span style="color:var(--warn)">ใช่ (มีอัปเดตรอ)</span>' : 'ไม่')}</div>`)}
  </div>
  <div class="grid cols-2" style="margin-bottom:16px">
    ${card('ดิสก์', '', noInv || (disks + (phys ? `<div style="margin-top:10px">${phys}</div>` : '')) || '—')}
    ${card('เครือข่าย', '', noInv || nics || '—')}
  </div>
`)}
  ${panel('tools', toolsCard(h, dis || (h.review_at ? 'disabled title="รับเครื่องกลับเข้าระบบก่อน"' : '')))}
  ${panel('history', card('ประวัติคำสั่ง', 'อัปเดตสดเมื่อเครื่องตอบกลับ ทุกคำสั่งยังอยู่ในประวัติการใช้งาน', table(cmds, cmdCols, empty('clock', 'ยังไม่มีคำสั่งในประวัติ')), clearBtn, true))}`;
}

function pageHostAfter() {
  const assign = (field, value) => api('/v1/hosts/site', 'POST', { host_ids: [hostCache.id], field, [field + '_id']: value || null })
    .then(() => { toast(field === 'site' ? 'ย้าย site แล้ว' : 'ตั้งหัวข้อ image แล้ว'); renderPage(); }).catch(e => toast(e.message, 'bad'));
  const s = $('#host-site'); if (s) s.onchange = () => assign('site', s.value);
  const c = $('#host-channel'); if (c) c.onchange = () => assign('channel', c.value);
}

// ───────── live updates (Server-Sent Events over fetch, so the token stays in a header) ─────────
const live = { connected: false, ctrl: null, retry: 0, timer: null };
const liveBadge = () => `<span id="live" class="badge ${live.connected ? 'ok pulse' : ''}" title="${live.connected ? 'ข้อมูลอัปเดตทันทีจาก server' : 'กำลังเชื่อมต่อใหม่…'}">${live.connected ? 'Live' : 'ไม่ได้เชื่อมต่อ'}</span>`;
const ccLive = () => `<span class="cc-live${live.connected ? '' : ' off'}" id="cc-live">${live.connected ? '<i></i>Live' : '<i></i>อัปเดตทุก 15 วินาที'}</span>`;
function setLive(on) { live.connected = on; const el = $('#live'); if (el) el.outerHTML = liveBadge(); const c = $('#cc-live'); if (c) c.outerHTML = ccLive(); }
async function startLive() {
  stopLive();
  const ctrl = new AbortController(); live.ctrl = ctrl;
  try {
    const r = await fetch('/v1/events', { headers: authHeaders(), signal: ctrl.signal, cache: 'no-store' });
    if (!r.ok) throw new Error('HTTP ' + r.status);
    setLive(true); live.retry = 0;
    const reader = r.body.getReader(), dec = new TextDecoder();
    let buf = '';
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      buf += dec.decode(value, { stream: true });
      let i;
      while ((i = buf.indexOf('\n\n')) >= 0) {
        const chunk = buf.slice(0, i); buf = buf.slice(i + 2);
        const line = chunk.split('\n').find(l => l.startsWith('data: '));
        if (line) { try { onLive(JSON.parse(line.slice(6))); } catch (e) {} }
      }
    }
  } catch (e) { if (ctrl.signal.aborted) return; }
  setLive(false);
  if (token && live.ctrl === ctrl) setTimeout(startLive, Math.min(30000, 1000 * 2 ** live.retry++));
}
function stopLive() { const c = live.ctrl; live.ctrl = null; c?.abort(); }

function onLive(e) {
  if (!e.type) return;
  if (e.type === 'job.updated' && e.host_id) {
    const d = e.data || {}, cur = jobLive[e.host_id] || {};
    jobLive[e.host_id] = { ...cur, status: d.status || cur.status || 'pe', phase: d.phase || cur.phase, progress: d.progress ?? cur.progress ?? 0, ponr: cur.ponr || d.phase === 'ponr' };
  }
  if (route() === 'hosts' && !routeArg() && viewPref.get() === 'map') {
    clearTimeout(live.timer);
    live.timer = setTimeout(() => { if (!document.querySelector('.modal') && !document.querySelector('.map.dragging') && route() === 'hosts') renderPage(); }, 400);
    return;
  }
  if (e.type === 'host.presence') {
    const on = !!e.data?.online;
    if (on && wakeMap[e.host_id] && Date.now() - wakeMap[e.host_id] < 30 * 60e3) { const hh = hostList.find(x => x.id === e.host_id); toast(`${hh?.hostname || 'เครื่อง'} ออนไลน์แล้ว`); }
    const h = hostList.find(x => x.id === e.host_id); if (h) h.online = on;
    if (hostCache && hostCache.id === e.host_id) hostCache.online = on;
    document.querySelectorAll(`[data-presence="${e.host_id}"]`).forEach(el => { el.className = 'badge' + (on ? ' ok' : ''); el.textContent = on ? 'ออนไลน์' : 'ออฟไลน์'; });
    document.querySelectorAll(`[data-dot="${e.host_id}"]`).forEach(el => el.classList.toggle('on', on));
    document.querySelectorAll(`[data-seen="${e.host_id}"]`).forEach(el => { el.innerHTML = on ? (el.classList.contains('pst-sub') ? '' : '<span style="color:var(--ok)">ตอนนี้</span>') : (el.classList.contains('pst-sub') ? 'ติดต่อล่าสุด เมื่อสักครู่' : 'เมื่อสักครู่'); });
    document.querySelectorAll(`[data-pst="${e.host_id}"]`).forEach(el => { el.className = 'pst ' + (on ? 'on' : 'off'); el.querySelector('b').textContent = on ? 'ออนไลน์' : 'ออฟไลน์'; });
    const cnt = $('[data-online-count]'); if (cnt && hostList.length) cnt.textContent = hostList.filter(x => x.online && !x.retired_at &&
      (siteFilter === 'all' || (siteFilter === 'none' ? !x.site_id : x.site_id === siteFilter))).length;
  }
  // anything else: re-render the current page shortly (coalesced), unless the admin is mid-dialog
  const r = route();
  if (r === 'schedule' && ['schedule.updated', 'job.updated', 'command.updated'].includes(e.type)) { clearTimeout(live.timer); live.timer = setTimeout(refreshSchedule, 600); return; }
  if (['hosts', 'overview'].includes(r) && e.type !== 'host.presence' || (r === 'hosts' && routeArg() === e.host_id)) {
    clearTimeout(live.timer);
    live.timer = setTimeout(() => { if (!document.querySelector('.modal') && route() === r) renderPage(); }, 400);
  }
}


// ───────── machines as a live map: server → sites → Windows machines ─────────
const viewPref = {
  get() { try { return localStorage.getItem('swim-host-view') || 'map'; } catch (e) { return 'map'; } },
  set(v) { try { localStorage.setItem('swim-host-view', v); } catch (e) {} },
};
const jobLive = {}; // host id → {status, phase, progress} from live job events
let mapQuery = '';

function viewSwitch(cur) {
  return `<div class="seg" role="tablist" aria-label="มุมมอง">
    <button class="${cur === 'map' ? 'on' : ''}" data-host-view="map" role="tab" aria-selected="${cur === 'map'}">${ic('map')}แผนผัง</button>
    <button class="${cur === 'table' ? 'on' : ''}" data-host-view="table" role="tab" aria-selected="${cur === 'table'}">${ic('list')}ตาราง</button></div>`;
}

function tileState(h) {
  const j = jobLive[h.id];
  if (j && !['completed', 'failed', 'aborted', 'cancelled'].includes(j.status)) return 'busy';
  if (j && j.status === 'failed' && j.ponr) return 'stuck';
  if (h.review_at || h.removing_at) return 'warn';
  if (!h.online) return 'off';
  const al = hostAlerts(h);
  if (al.some(a => a[0] === 'bad')) return 'bad';
  if (al.some(a => a[0] === 'warn')) return 'warn';
  return 'on';
}
const TILE_LABEL = { on: 'ออนไลน์', off: 'ออฟไลน์', warn: 'ต้องดูแล', bad: 'มีปัญหา', busy: 'กำลังติดตั้ง Windows', stuck: 'ติดตั้งไม่สำเร็จ — รอลองใหม่ใน WinPE' };

// a machine's MAC (the adapter that has an IPv4 address first)
const macOf = h => { const n = (inv(h).network || []).filter(x => x.mac); return (n.find(x => (x.ipv4 || []).length) || n[0])?.mac || h.mac || ''; };
let dupNames = new Set();
const markDups = list => { const c = {}; for (const h of list) if (!h.retired_at) { const k = String(h.hostname || '').toUpperCase(); c[k] = (c[k] || 0) + 1; } dupNames = new Set(Object.keys(c).filter(k => k && c[k] > 1)); };
// short tag that tells same-named machines apart: last two bytes of the MAC, else the IP
const dupTag = h => { if (!dupNames.has(String(h.hostname || '').toUpperCase())) return '';
  const m = macOf(h).replace(/[^0-9a-f]/gi, ''), tag = m.length >= 4 ? m.slice(-4).toUpperCase().replace(/(..)(..)/, '$1:$2') : (hostIPs(h)[0] || '').split('.').slice(-2).join('.');
  return tag ? `<span class="dup-tag" title="มีเครื่องชื่อนี้มากกว่า 1 เครื่อง แยกด้วย ${m.length >= 4 ? 'MAC' : 'IP'}" translate="no">${esc(tag)}</span>` : ''; };
let mapExpanded = new Set();
const MAP_CAP = 24;
function machineTile(h) {
  const s = tileState(h), al = hostAlerts(h).filter(a => a[0] === 'bad' || a[0] === 'warn'), j = jobLive[h.id];
  const os = osShort(h).replace('Windows ', 'Win ').replace(' Education', ' Edu').replace(' Professional', ' Pro').replace(' Enterprise', ' Ent');
  const c = diskC(h), pct = c && c.size ? Math.round((c.size - c.free) / c.size * 100) : null;
  const q = mapQuery.toLowerCase();
  const dim = q && ![h.hostname, ...hostIPs(h), inv(h).user, h.model].join(' ').toLowerCase().includes(q);
  return `<a class="mtile ${s}${dim ? ' dim' : ''}" href="#/hosts/${encodeURIComponent(h.id)}" draggable="true" data-tile="${esc(h.id)}"
      title="${esc(h.hostname)} — ${TILE_LABEL[s]}${al.length ? ' · ' + esc(al.map(a => a[1]).join(', ')) : ''}">
    <span class="mt-icon">${ic('win')}${s === 'busy' ? `<svg class="ring" viewBox="0 0 36 36"><circle cx="18" cy="18" r="16"/><circle cx="18" cy="18" r="16" class="v"
      style="stroke-dasharray:${Math.max(4, (j.progress || 0)) * 1.005} 100.5"/></svg>` : ''}<i class="mt-dot" data-dot="${esc(h.id)}"></i></span>
    <span class="mt-text"><b>${esc(h.hostname || h.id)}${dupTag(h)}</b>
      <small>${wakeState(h) === 'waiting' ? `<span style="color:var(--accent)">กำลังปลุก… <span data-elapsed-since="${wakeMap[h.id]}">—</span></span>` : h.review_at ? '<span style="color:var(--warn)">รอตรวจสอบ — กดเพื่อจัดการ</span>' : h.removing_at ? '<span style="color:var(--warn)">กำลังถอด Agent…</span>' : s === 'busy' ? `${esc(PHASE_TH[j.phase] || j.phase || 'กำลังติดตั้ง')} · ${j.progress || 0}%` : s === 'stuck' ? '<span class="t-bad">ติดตั้งไม่สำเร็จ — กดเพื่อลองใหม่</span>' : esc(hostIPs(h)[0] || 'ไม่มี IP')}</small>
      <small class="mt-img">${(() => { const x = installedOf(h); return x ? `<span class="ichip sm ${x.iso ? 'iso' : 'gm'}">${x.iso ? 'ISO' : 'Image'}</span><span translate="no">${esc(x.version)}</span>` : `<span class="ichip sm own">เดิม</span>${esc(os || 'Windows')}`; })()}${pct !== null ? `<span class="mt-c">C: ${pct}%</span>` : ''}</small></span>
    ${al.length && s !== 'busy' && s !== 'stuck' ? `<span class="mt-flag ${al.some(a => a[0] === 'bad') ? 'bad' : 'warn'}" aria-label="${al.length} เรื่องต้องดูแล">${al.length}</span>` : ''}
  </a>`;
}

async function pageMap() {
  const [all, sites, info, vms, imgs, cap] = await Promise.all([api('/v1/hosts'), api('/v1/sites'), api('/v1/enroll/info').catch(() => ({})),
    api('/v1/builder/vms').catch(() => []), api('/v1/images'), api('/v1/capacity/server')]);
  hostList = all; siteCache = sites; latestAgent = info.agent?.version || latestAgent; imageCache = imgs; markDups(all);
  for (const h of all) if (h.job) jobLive[h.id] = h.job; // server truth on every load; live events refine it
  const live = all.filter(h => !h.retired_at);
  const normal = live;
  const groups = [...sites.map(s => ({ id: s.id, name: s.name, desc: s.description, hosts: normal.filter(h => h.site_id === s.id) })),
    { id: '', name: 'ยังไม่ระบุ site', desc: 'ลากเครื่องไปวางใน site ที่ต้องการ', hosts: normal.filter(h => !h.site_id), none: true }];
  const online = live.filter(h => h.online).length, care = live.filter(h => ['warn', 'bad', 'stuck'].includes(tileState(h))).length;
  const busy = live.filter(h => tileState(h) === 'busy').length;
  const pub = imgs.filter(i => i.status === 'published'), st = cap.storage, hs = health?.status || 'down';
  const gm = vms[0];

  const siteBox = g => {
    const on = g.hosts.filter(h => h.online).length;
    return `<section class="msite${g.none ? ' none' : ''}${g.review ? ' review' : ''}${g.none && !g.hosts.length ? ' empty' : ''}" ${g.review ? '' : `data-drop-site="${esc(g.id)}"`}>
      <header><span class="ms-ico">${ic(g.review ? 'alert' : g.none ? 'info' : 'layers')}</span><div><b>${esc(g.name)}</b>
        <small>${g.review ? esc(g.desc) + ' — คลิกเครื่องเพื่อรับกลับหรือถอน Agent' : g.hosts.length ? `ออนไลน์ ${on} / ${g.hosts.length} เครื่อง` : esc(g.desc || 'ยังไม่มีเครื่อง')}</small></div>
        ${g.hosts.some(h => !h.online) ? `<button class="btn sm icon ghost" data-wake="${esc(g.hosts.filter(h => !h.online).map(h => h.id).join(','))}" data-label="เครื่องที่ออฟไลน์ใน ${esc(g.name)}" title="ปลุกเครื่องที่ออฟไลน์" aria-label="ปลุกเครื่องที่ออฟไลน์ใน ${esc(g.name)}">${ic('power')}</button>` : ''}
        ${!g.none && !g.review && g.hosts.length ? `<button class="btn sm icon ghost" data-site-cmd="${esc(g.id)}" data-site-name="${esc(g.name)}" title="สั่งงานทุกเครื่องใน site นี้" aria-label="สั่งงานทุกเครื่องใน ${esc(g.name)}">${ic('console')}</button>` : ''}</header>
      ${(() => { const q = mapQuery.toLowerCase(), match = h => !q || [h.hostname, ...hostIPs(h), inv(h).user, h.model, macOf(h), installedOf(h)?.version].join(' ').toLowerCase().includes(q);
        const cap = live.length > 300 ? 8 : MAP_CAP, list = sortSq(g.hosts).filter(match), open = q || mapExpanded.has(g.id || 'none') || list.length <= cap + 4, shown = open ? list : list.slice(0, cap);
        return `<div class="mtiles">${shown.map(machineTile).join('') || `<div class="mt-empty">${q && g.hosts.length ? 'ไม่มีเครื่องที่ตรงกับคำค้น' : g.none ? 'ทุกเครื่องมี site แล้ว' : 'ลากเครื่องมาวางที่นี่'}</div>`}</div>
          ${list.length > shown.length ? `<div class="ms-rest"><div class="sqs">${list.slice(shown.length).map(machSq).join('')}</div>
            <button class="btn sm ghost" data-map-more="${esc(g.id || 'none')}">แสดงทั้งหมด ${list.length} เครื่อง</button></div>` : mapExpanded.has(g.id || 'none') && list.length > cap + 4 ? `<button class="btn sm ghost ms-more" data-map-more="${esc(g.id || 'none')}">ย่อเหลือ ${cap} เครื่อง</button>` : ''}`; })()}
    </section>`;
  };

  return `
  <div class="map-bar">${viewSwitch('map')}
    <div class="map-stats"><span><i class="lg on"></i>ออนไลน์ ${online}</span><span><i class="lg off"></i>ออฟไลน์ ${live.length - online}</span>
      <span><i class="lg warn"></i>ต้องดูแล ${care}</span>${busy ? `<span><i class="lg busy"></i>กำลังติดตั้ง ${busy}</span>` : ''}</div>
    <div class="spacer"></div>
    <div class="search">${ic('search')}<input class="input" id="map-q" placeholder="หาเครื่อง: ชื่อ, IP, ผู้ใช้" value="${esc(mapQuery)}"></div>
    <a class="btn primary" href="#/enroll">${ic('plus')}เพิ่มเครื่อง</a></div>
  <div class="map" id="map">
    <svg class="map-wires" id="map-wires" aria-hidden="true"></svg>
    <div class="map-core">
      <a class="mnode server" href="#/home" id="map-server">
        <span class="mn-ico">${ic('db')}</span>
        <span><b>SWIM Server</b><small>${esc(location.host)} · ${hs === 'ok' ? 'ทำงานปกติ' : 'ผิดปกติ'}</small>
          <small>พื้นที่ ${st.used_pct.toFixed(0)}% · image ใช้งาน ${pub.length} เวอร์ชัน</small></span>
        <i class="mt-dot ${hs === 'ok' ? 'up' : ''}"></i></a>
      ${gm ? `<a class="mnode gm" href="#/images" title="เครื่องต้นแบบสำหรับสร้าง image">
        <span class="mn-ico">${ic('builder')}</span><span><b>${esc(gm.name)}</b><small>เครื่องต้นแบบ · ${esc(vmStateLabel(gm)[1])}</small>
          <small>${pub[0] ? 'ล่าสุด ' + esc(pub[0].version) : 'ยังไม่มี image'}</small></span></a>` : ''}
    </div>
    <div class="map-sites" id="map-sites">${groups.map(siteBox).join('')}
      <button class="msite add" data-map-new-site>${ic('plus')}<b>เพิ่ม site</b><small>เช่น อาคาร, ห้องแล็บ, แผนก</small></button></div>
  </div>
  <p class="map-hint">${ic('info')}คลิกเครื่องเพื่อดูรายละเอียดหรือติดตั้ง Windows ใหม่ · ลากเครื่องไปวางใน site อื่นเพื่อย้าย · สถานะอัปเดตเองทันที</p>`;
}

// wires from the server to each site box (recomputed on render and resize)
function drawWires() {
  const map = $('#map'), svg = $('#map-wires'), srv = $('#map-server');
  if (!map || !svg || !srv) return;
  const m = map.getBoundingClientRect(), s = srv.getBoundingClientRect();
  const x0 = s.left + s.width / 2 - m.left, y0 = s.bottom - m.top;
  svg.setAttribute('width', m.width); svg.setAttribute('height', m.height);
  const boxes = [...map.querySelectorAll('.msite:not(.add)')].filter(el => el.offsetParent);
  const top = Math.min(...boxes.map(el => el.getBoundingClientRect().top));
  svg.innerHTML = boxes.map(el => {
    const r = el.getBoundingClientRect(), x1 = r.left + r.width / 2 - m.left, y1 = r.top - m.top;
    if (r.top - top > 4) return ''; // first row only: a line would cross the boxes above
    const my = (y0 + y1) / 2;
    const on = el.querySelector('.mtile.on, .mtile.warn, .mtile.bad, .mtile.busy, .mtile.stuck');
    return `<path d="M${x0},${y0} C${x0},${my} ${x1},${my} ${x1},${y1}" class="${on ? 'live' : ''}"/>`;
  }).join('');
}
window.addEventListener('resize', () => { if (route() === 'hosts' && !routeArg()) drawWires(); });

function mapAfter() {
  drawWires();
  const mp = $('#map'); if (mp) wireTips(mp);
  const q = $('#map-q');
  // searching shows only the matching machines in every room (re-render, keep the cursor in the box)
  if (q) q.oninput = () => { mapQuery = q.value; clearTimeout(q._t); q._t = setTimeout(async () => { await renderPage(); const n = $('#map-q'); if (n) { n.focus(); n.setSelectionRange(n.value.length, n.value.length); } }, 250); };
  const map = $('#map'); if (!map) return;
  map.addEventListener('dragstart', e => {
    const t = e.target.closest('.mtile'); if (!t) return;
    e.dataTransfer.setData('text/swim-host', t.dataset.tile); e.dataTransfer.effectAllowed = 'move';
    t.classList.add('dragging'); map.classList.add('dragging');
  });
  map.addEventListener('dragend', e => { e.target.closest?.('.mtile')?.classList.remove('dragging'); map.classList.remove('dragging');
    map.querySelectorAll('.msite.over').forEach(x => x.classList.remove('over')); });
  map.addEventListener('dragover', e => {
    const box = e.target.closest('[data-drop-site]'); if (!box) return;
    e.preventDefault(); e.dataTransfer.dropEffect = 'move';
    map.querySelectorAll('.msite.over').forEach(x => x !== box && x.classList.remove('over')); box.classList.add('over');
  });
  map.addEventListener('drop', async e => {
    const box = e.target.closest('[data-drop-site]'); if (!box) return;
    e.preventDefault(); box.classList.remove('over');
    const id = e.dataTransfer.getData('text/swim-host'), h = hostList.find(x => x.id === id);
    const to = box.dataset.dropSite || null;
    if (!h || (h.site_id || null) === to) return;
    const from = h.site_id || null, toName = to ? siteCache.find(s => s.id === to)?.name : 'ไม่ระบุ site';
    try {
      await api('/v1/hosts/site', 'POST', { host_ids: [id], field: 'site', site_id: to });
      toastUndo(`ย้าย ${h.hostname} ไป ${toName} แล้ว`, async () => {
        await api('/v1/hosts/site', 'POST', { host_ids: [id], field: 'site', site_id: from }); renderPage();
      });
      renderPage();
    } catch (err) { toast(err.message, 'bad'); }
  });
}

function toastUndo(msg, undo) {
  const t = document.createElement('div'); t.className = 'toast';
  t.innerHTML = `<span>${esc(msg)}</span><button class="btn sm ghost">เลิกทำ</button>`;
  t.querySelector('button').onclick = async () => { t.remove(); try { await undo(); } catch (e) { toast(e.message, 'bad'); } };
  $('#toasts').appendChild(t); setTimeout(() => t.remove(), 7000);
}

// ───────── reimage jobs ─────────
const MIN_REIMAGE = '0.5.0';
const JOB_STATUS = { retry: ['warn', 'รอเครื่องรับคำสั่งลองใหม่'], queued: ['', 'รอเครื่องรับ'], staging: ['info', 'กำลังเตรียมเครื่อง'], handoff: ['info', 'รีบูตเข้า WinPE'], pe: ['info', 'กำลังติดตั้งใน WinPE'],
  completed: ['ok', 'สำเร็จ'], failed: ['bad', 'ล้มเหลว'], aborted: ['warn', 'ยกเลิกเอง (เครื่องเดิมปลอดภัย)'], cancelled: ['', 'ยกเลิก'] };
const PHASE_TH = { retry: 'ลองใหม่', retry_download: 'ดาวน์โหลด image สำหรับลองใหม่', queued: 'รอเครื่องรับคำสั่ง', preflight: 'ตรวจเครื่อง', staging: 'เตรียมพาร์ทิชัน staging', downloading: 'ดาวน์โหลด', drivers: 'ไดรเวอร์ + WinPE',
  prepare: 'เตรียมไฟล์งาน', rebooting: 'รีบูตเข้า WinPE', pe_start: 'WinPE เริ่มทำงาน', verify: 'ตรวจ image', ponr: 'ผ่านจุดย้อนกลับไม่ได้',
  partition: 'แบ่งดิสก์', apply: 'ติดตั้ง image', inject: 'ใส่ไดรเวอร์ + agent', boot_config: 'ตั้งค่าบูต', first_boot: 'บูต Windows ใหม่ครั้งแรก', done: 'เสร็จ', cancelled: 'ยกเลิก' };
const jobBadge = s => `<span class="badge ${JOB_STATUS[s]?.[0] || ''}${['staging', 'handoff', 'pe', 'retry'].includes(s) ? ' pulse' : ''}">${esc(JOB_STATUS[s]?.[1] || s)}</span>`;
// time left for a reinstall job, estimated from its percent (only once it has moved enough)
function jobProgress(j) {
  const start = Date.parse(j.created_at), el = Date.now() - start, p = j.progress || 0;
  const finishAt = p >= 5 && p < 100 ? Date.now() + el * (100 - p) / p : null;
  return progressBox({ percent: p, title: esc(PHASE_TH[j.phase] || j.phase || JOB_STATUS[j.status]?.[1] || ''), detail: esc(j.message || ''),
    since: start, finishAt, updatedAt: j.updated_at, staleAfter: 900, note: finishAt ? 'ประมาณจาก %' : '' });
}
const JOB_DONE = ['completed', 'failed', 'aborted', 'cancelled'];
function jobsPanel(jobs) {
  if (!jobs.length) return '';
  const h = hostCache, anyDone = jobs.some(j => JOB_DONE.includes(j.status));
  return `<div style="margin-top:16px"><div class="jobs-head"><h3>ประวัติการติดตั้ง</h3>
    ${anyDone ? `<button class="btn sm ghost" data-jobs-clear="${esc(h.id)}" data-label="${esc(h.hostname || h.id)}">${ic('trash')}ล้างประวัติการติดตั้ง</button>` : ''}</div>${jobs.slice(0, 5).map(j => {
    const d = j.disk || {}, done = ['completed', 'failed', 'aborted', 'cancelled'].includes(j.status);
    return `<div class="job-card"><div style="display:flex;gap:10px;align-items:center;flex-wrap:wrap">
        <b class="mono">${esc(j.image_version)}</b>${jobBadge(j.status)}${done ? '' : `<span class="mute" style="font-size:12.5px">${esc(PHASE_TH[j.phase] || j.phase || '')}</span>`}
        <div style="flex:1"></div><span class="mute" style="font-size:12px">${esc(j.actor.replace('user:', ''))} · ${esc(ago(j.created_at))}</span>
        ${['queued', 'staging'].includes(j.status) ? `<button class="btn sm ghost" data-job-cancel="${esc(j.id)}">ยกเลิก</button>` : ''}
        ${j.status === 'failed' && j.ponr && j === jobs[0] ? `<button class="btn sm primary" data-job-retry="${esc(j.id)}">${ic('refresh')}ลองใหม่</button>` : ''}
        ${canRehandoff(j) ? `<button class="btn sm primary" data-job-rehandoff="${esc(j.id)}" data-label="${esc((typeof h !== 'undefined' && h?.hostname) || '')}">${ic('refresh')}ลองบูตเข้า WinPE อีกครั้ง</button>` : ''}
        <button class="btn sm" data-job="${esc(j.id)}">${ic('log')}รายละเอียด</button>
        ${done ? `<button class="btn sm icon ghost" data-job-delete="${esc(j.id)}" data-version="${esc(j.image_version)}" data-label="${esc(JOB_STATUS[j.status]?.[1] || j.status)}" title="ลบรายการนี้" aria-label="ลบรายการนี้">${ic('trash')}</button>` : ''}</div>
      ${!done ? `<div style="margin:8px 0 4px">${jobProgress(j)}</div>` : `<div class="mute" style="font-size:12.5px">${esc(j.message || '')}</div>`}
      ${j.status === 'failed' && j.ponr && j === jobs[0] ? `<div class="callout warn" style="margin:8px 0 4px">${ic('alert')}<div>ล้มเหลวหลังจุดที่ย้อนกลับไม่ได้ — Windows เดิมถูกลบแล้ว
        เครื่องรออยู่ใน WinPE กด <b>ลองใหม่</b> เพื่อติดตั้งอีกครั้ง (เลือก image ได้) เครื่องจะดาวน์โหลดเองแล้วเริ่มใหม่</div></div>` : ''}
      <div class="mute" style="font-size:12px;margin-top:2px">ดิสก์: ${d.mode === 'repartition' ? `แบ่งใหม่ · C: ${d.system_gb ? d.system_gb + ' GB' : 'ทั้งหมด'}${d.data_label ? ' · ' + esc(d.data_label) + ' (ที่เหลือ)' : ''}` : 'คงพาร์ทิชันเดิม · format C:'} · ชื่อเครื่อง ${esc(j.hostname)}</div></div>`;
  }).join('')}</div>`;
}

async function openJob(id) {
  closeDrawer();
  const scrim = document.createElement('div'); scrim.className = 'scrim'; scrim.style.background = 'rgba(9,14,22,.3)';
  const d = document.createElement('aside'); d.className = 'drawer'; d.dataset.job = id;
  d.innerHTML = `<div class="drawer-head"><div class="dot-icon accent">${ic('capture')}</div><div style="flex:1;min-width:0"><h3>งานติดตั้ง <span class="mono" style="font-size:12px">${esc(id)}</span></h3>
    <div class="mute" style="font-size:12px" id="job-sum"></div></div><button class="btn icon ghost" data-close aria-label="ปิด">${ic('x')}</button></div><div id="job-body" style="overflow:auto;flex:1"></div>`;
  scrim.onclick = closeDrawer; d.querySelector('[data-close]').onclick = closeDrawer;
  document.body.append(scrim, d); document.addEventListener('keydown', escDrawer);
  const load = async () => {
    if (!document.querySelector(`.drawer[data-job="${id}"]`)) return;
    const r = await api('/v1/jobs/' + encodeURIComponent(id)), j = r.job;
    $('#job-sum').innerHTML = `${jobBadge(j.status)} · ${esc(PHASE_TH[j.phase] || j.phase || '')} · ${j.progress ?? 0}%`;
    const rh = canRehandoff(j) ? `<div class="callout warn" style="margin:12px 18px">${ic('alert')}<div style="flex:1"><b>เครื่องบูตกลับเข้า Windows เดิม ไม่ได้เข้า WinPE</b>
      <div>ไม่มีอะไรถูกแก้ในเครื่อง ไฟล์ติดตั้งยังอยู่ในเครื่อง ลองบูตเข้า WinPE อีกครั้งได้โดยไม่ต้องดาวน์โหลดและไม่ต้องใส่รหัสผ่านใหม่</div>
      <button class="btn sm primary" style="margin-top:8px" data-job-rehandoff="${esc(j.id)}" data-label="${esc(j.hostname || '')}">${ic('refresh')}ลองบูตเข้า WinPE อีกครั้ง</button></div></div>` : '';
    $('#job-body').innerHTML = rh + table(r.events.slice().reverse(), [
      { h: 'เวลา', v: e => `<span class="nowrap">${esc(new Date(e.at).toLocaleTimeString(LOC()))}</span>`, cls: 'nowrap' },
      { h: 'จาก', v: e => `<span class="badge plain">${e.source === 'pe' ? 'WinPE' : 'agent'}</span>` },
      { h: 'ขั้น', v: e => esc(PHASE_TH[e.phase] || e.phase) },
      { h: 'รายละเอียด', v: e => `<span style="${e.level === 'error' ? 'color:var(--bad)' : e.level === 'warn' ? 'color:var(--warn)' : ''}">${esc(e.data?.message || '')}</span>` },
    ], empty('clock', 'ยังไม่มีความคืบหน้า')) + (r.log ? `<div style="padding:12px 18px"><b style="font-size:13px">Log จาก WinPE</b><pre class="code" style="max-height:none;margin-top:8px">${esc(r.log)}</pre></div>` : '');
  };
  await load(); clearInterval(batchTimer); batchTimer = setInterval(load, 3000);
}

function openDeploy(imageId) {
  const h = hostCache, img = imageCache.find(x => x.id === imageId), i = inv(h);
  const diskTotal = (i.physical_disks || []).reduce((m, p) => Math.max(m, p.size || 0), 0);
  const stagingGB = Math.max(20, Math.ceil((img.size_bytes + 3 * 2 ** 30) / 2 ** 30));
  modal({ title: 'ล้างเครื่องและติดตั้ง Windows ใหม่', danger: true, wide: true, confirm: 'เริ่มติดตั้ง',
    desc: `<b>${esc(h.hostname)}</b> จะถูกล้างและติดตั้ง <b class="mono">${esc(img.version)}</b> (${fmtBytes(img.size_bytes)}) — ข้อมูลบน C: หายทั้งหมด`,
    body: `<div class="callout">${ic('info')}<div>ขั้นตอน: agent สร้างพาร์ทิชัน <b>SWIMSTAGE ${stagingGB} GB</b> (ย่อ C:), ดาวน์โหลด image + WinPE, เก็บไดรเวอร์ดิสก์/เน็ตของเครื่องนี้ แล้วรีบูตเข้า WinPE
        — ถ้ามีอะไรผิดพลาด <b>ก่อน</b>จุดย้อนกลับไม่ได้ เครื่องจะกลับเข้า Windows เดิมเอง${diskTotal ? ` · ดิสก์ ${fmtBytes(diskTotal)}` : ''}</div></div>
      <div class="field"><label>การจัดดิสก์</label>
        <label class="radio"><input type="radio" name="mode" value="refresh" checked> <b>คงพาร์ทิชันเดิม</b> — format เฉพาะไดรฟ์ Windows (C:) พาร์ทิชันอื่นเช่น D: ไม่ถูกแตะ</label>
        <label class="radio"><input type="radio" name="mode" value="repartition"> <b>แบ่งดิสก์ใหม่</b> — ลบทุกพาร์ทิชัน (ยกเว้น EFI/MSR/SWIMSTAGE) แล้วสร้างใหม่</label></div>
      <div id="repart" hidden><div class="row-fields">
        <div class="field"><label>ขนาด C: (GB)</label><input class="input" name="system_gb" type="number" min="40" placeholder="เว้นว่าง = ใช้ทั้งหมด"><span class="hint">อย่างน้อย 40 GB</span></div>
        <div class="field"><label>พาร์ทิชันข้อมูลจากพื้นที่ที่เหลือ</label><input class="input" name="data_label" placeholder="เช่น Data (เว้นว่าง = ไม่สร้าง)"></div></div>
        <div class="callout warn" style="margin:0 0 12px">${ic('alert')}<div>แบ่งดิสก์ใหม่ = ข้อมูลบน <b>ทุกพาร์ทิชัน</b> (รวม D:) หายหมด</div></div></div>
      <div class="row-fields"><div class="field"><label>ชื่อเครื่อง</label><input class="input mono" name="hostname" maxlength="15" value="${esc(h.hostname || '')}"></div>
        <div class="field"><label>บัญชีผู้ดูแลในเครื่องใหม่</label><input class="input" name="admin_user" required value="swimadmin" autocomplete="off"></div></div>
      <div class="row-fields"><div class="field"><label>รหัสผ่านบัญชีนี้</label><input class="input" name="admin_password" type="password" required minlength="8" autocomplete="new-password">
          <span class="hint">ส่งแบบเข้ารหัสถึงเครื่องนี้เท่านั้น (NaCl box) — server ไม่เก็บแบบอ่านได้</span></div>
        <div class="field"><label>ยืนยันรหัสผ่าน</label><input class="input" name="admin_password2" type="password" required minlength="8" autocomplete="new-password"></div></div>
      
      <div class="field"><label>พิมพ์ชื่อเครื่อง <b class="mono">${esc(h.hostname)}</b> เพื่อยืนยัน</label><input class="input mono" name="confirm" required autocomplete="off"></div>`,
    onConfirm: async f => {
      if (f.confirm.trim().toUpperCase() !== String(h.hostname).toUpperCase()) throw new Error('ชื่อเครื่องไม่ตรง');
      if (f.admin_password !== f.admin_password2) throw new Error('รหัสผ่านสองช่องไม่ตรงกัน');
      const disk = { mode: f.mode, system_gb: f.mode === 'repartition' && f.system_gb ? Number(f.system_gb) : 0, data_label: f.mode === 'repartition' ? (f.data_label || '').trim() : '' };
      const r = await api('/v1/hosts/' + encodeURIComponent(h.id) + '/deploy', 'POST', { image_id: img.id, disk, hostname: f.hostname, admin_user: f.admin_user,
        admin_password: f.admin_password });
      toast('เริ่มงานติดตั้งแล้ว — ติดตามความคืบหน้าได้ในหน้านี้'); await renderPage(); openJob(r.job_id);
    } });
  document.querySelectorAll('input[name=mode]').forEach(x => x.onchange = () => { $('#repart').hidden = !document.querySelector('input[name=mode][value=repartition]').checked; });
}

// ── reimage many machines at once: pick an image (golden master or ISO) in one click, check who can run it, start now or at a time
async function openBulkDeploy(ids, preset = {}) {
  const [imgs, chs] = await Promise.all([api('/v1/images'), api('/v1/channels')]);
  imageCache = imgs; channelCache = chs;
  const chOf = x => chs.find(c => c.id === x.channel_id);
  const pub = imgs.filter(x => x.status === 'published').sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at));
  const gm = pub.filter(x => !isIsoChannel(chOf(x))), iso = pub.filter(x => isIsoChannel(chOf(x)));
  if (!pub.length) return toast('ยังไม่มี image ที่อนุมัติแล้ว สร้างที่หน้า Images ก่อน', 'bad');
  const hosts = ids.map(id => hostList.find(h => h.id === id)).filter(Boolean);
  const why = h => h.retired_at ? 'ถอดออกแล้ว' : h.review_at ? 'รอตรวจสอบ' : !h.agent_version || verCmp(h.agent_version, MIN_REIMAGE) < 0 ? `agent ต่ำกว่า ${MIN_REIMAGE}`
    : (jobLive[h.id] || h.job) && !['completed', 'failed', 'aborted', 'cancelled'].includes((jobLive[h.id] || h.job).status) ? 'กำลังติดตั้งอยู่' : !h.online ? 'ออฟไลน์ (ใช้ "ตั้งเวลา" ระบบจะปลุกให้)' : '';
  const OFF = 'ออฟไลน์ (ใช้ "ตั้งเวลา" ระบบจะปลุกให้)', ok = hosts.filter(h => !why(h)), off = hosts.filter(h => why(h) === OFF), skip = hosts.filter(h => why(h) && why(h) !== OFF);
  const nFor = when => when === 'at' ? ok.length + off.length : ok.length;
  const card2 = (x, i) => { const c = chOf(x), isI = isIsoChannel(c);
    return `<label class="imgpick"><input type="radio" name="image_id" value="${esc(x.id)}" ${(preset.image_id ? preset.image_id === x.id : i === 0) ? 'checked' : ''}>
      <span class="ip-ic">${ic(isI ? 'hdd' : 'builder')}</span><span class="ip-t"><b>${esc(x.version)}</b>
      <small>${esc(isI ? `Windows ${c?.edition || ''}, จากแผ่น ISO` : `${c?.name || ''}, จากเครื่องต้นแบบ`)}</small></span><span class="ip-s">${fmtBytes(x.size_bytes)}</span></label>`; };
  const now = new Date(Date.now() + 36e5); now.setMinutes(0, 0, 0);
  const local = d => new Date(d.getTime() - d.getTimezoneOffset() * 6e4).toISOString().slice(0, 16);
  modal({ title: `ล้างเครื่องและติดตั้ง ${ok.length + off.length} เครื่อง`, danger: true, wide: true, confirm: 'ยืนยันและเริ่ม',
    desc: `ข้อมูลบน C: ของทุกเครื่องที่เลือกจะหายทั้งหมด แต่ละเครื่องใช้ชื่อเครื่องเดิม`,
    body: `<div class="bd-grid"><div>
        <div class="bd-h">เลือก image</div>
        ${gm.length ? `<div class="bd-sub">จากเครื่องต้นแบบ</div><div class="imgpicks">${gm.map(card2).join('')}</div>` : ''}
        ${iso.length ? `<div class="bd-sub">จากแผ่น ISO</div><div class="imgpicks">${iso.map((x, i) => card2(x, gm.length ? 1 : i)).join('')}</div>` : ''}
        <div class="bd-h" style="margin-top:16px">เมื่อไหร่</div>
        <div class="seg bd-when" role="radiogroup"><label><input type="radio" name="when" value="now" ${preset.at ? '' : 'checked'}><span>เริ่มทันที</span></label>
          <label><input type="radio" name="when" value="at" ${preset.at ? 'checked' : ''}><span>ตั้งเวลา</span></label></div>
        <div class="field" id="bd-at" ${preset.at ? '' : 'hidden'} style="margin-top:10px"><label>เริ่มเวลา</label><input class="input" type="datetime-local" name="at" value="${esc(preset.at || local(now))}">
          <span class="hint">งานจะอยู่ในหน้า "ตารางงาน" และเริ่มเองตามเวลา เครื่องที่ออฟไลน์ตอนนั้นจะถูกปลุกก่อน</span></div>
        <div class="bd-h" style="margin-top:16px">บัญชีผู้ดูแลในเครื่องใหม่</div>
        <div class="row-fields"><div class="field"><label>ชื่อบัญชี</label><input class="input" name="admin_user" required value="swimadmin" autocomplete="off"></div>
          <div class="field"><label>รหัสผ่าน</label><input class="input" name="admin_password" type="password" required minlength="8" autocomplete="new-password"></div>
          <div class="field"><label>ยืนยันรหัสผ่าน</label><input class="input" name="admin_password2" type="password" required minlength="8" autocomplete="new-password"></div></div>
        <label class="radio"><input type="radio" name="mode" value="refresh" checked> <b>คงพาร์ทิชันเดิม</b>, format เฉพาะไดรฟ์ Windows (C:)</label>
        <label class="radio"><input type="radio" name="mode" value="repartition"> <b>แบ่งดิสก์ใหม่</b>, ลบทุกพาร์ทิชันรวม D:</label>
      </div><div>
        <div class="bd-h">เครื่องที่จะติดตั้ง <span class="mute">${ok.length}</span></div>
        <ul class="bd-list">${ok.map(h => `<li><i class="dot on"></i><b>${esc(h.hostname || h.id)}</b><small>${esc(h.site_name || 'ไม่ระบุห้อง')}</small></li>`).join('') || '<li class="mute">ไม่มีเครื่องที่พร้อม</li>'}</ul>
        ${off.length ? `<div class="bd-h" style="margin-top:14px">ออฟไลน์ <span class="mute">${off.length}</span></div>
          <ul class="bd-list skip">${off.map(h => `<li><i class="dot"></i><b>${esc(h.hostname || h.id)}</b><small>ข้ามถ้าเริ่มทันที, ปลุกให้ถ้าตั้งเวลา</small></li>`).join('')}</ul>` : ''}
        ${skip.length ? `<div class="bd-h" style="margin-top:14px">ข้าม <span class="mute">${skip.length}</span></div>
          <ul class="bd-list skip">${skip.map(h => `<li><i class="dot"></i><b>${esc(h.hostname || h.id)}</b><small>${esc(why(h))}</small></li>`).join('')}</ul>` : ''}
        <div class="field" style="margin-top:16px"><label>พิมพ์ <b id="bd-n">${nFor(preset.at ? 'at' : 'now')}</b> (จำนวนเครื่อง) เพื่อยืนยัน</label><input class="input mono" name="confirm" required autocomplete="off" inputmode="numeric"></div>
      </div></div>`,
    onConfirm: async f => {
      const n = nFor(f.when);
      if (!n) throw new Error('ไม่มีเครื่องที่พร้อมติดตั้ง');
      if (String(f.confirm).trim() !== String(n)) throw new Error('จำนวนเครื่องไม่ตรง');
      if (f.admin_password !== f.admin_password2) throw new Error('รหัสผ่านสองช่องไม่ตรงกัน');
      const disk = { mode: f.mode, system_gb: 0, data_label: '' };
      if (f.when === 'at') {
        const at = new Date(f.at); if (!(at > new Date())) throw new Error('เลือกเวลาในอนาคต');
        await api('/v1/schedules', 'POST', { kind: 'deploy', title: `ล้างเครื่อง ${ok.length + off.length} เครื่อง`, start_at: at.toISOString(), repeat: { freq: 'none' },
          targets: { host_ids: [...ok, ...off].map(h => h.id) }, args: { image_id: f.image_id, disk, admin_user: f.admin_user, admin_password: f.admin_password } });
        toast(`ตั้งเวลาแล้ว ${at.toLocaleString(LOC(), { dateStyle: 'medium', timeStyle: 'short' })}`); hostSel.clear(); location.hash = '#/schedule'; return;
      }
      let done = 0; const fails = [];
      for (const h of ok) {
        try { await api('/v1/hosts/' + encodeURIComponent(h.id) + '/deploy', 'POST', { image_id: f.image_id, disk, hostname: h.hostname, admin_user: f.admin_user, admin_password: f.admin_password }); done++; }
        catch (e) { fails.push(`${h.hostname}: ${e.message}`); }
      }
      hostSel.clear(); renderPage();
      toast(`เริ่มติดตั้งแล้ว ${done} เครื่อง${fails.length ? `, ไม่สำเร็จ ${fails.length}: ${fails.join('; ')}` : ''}`, fails.length ? 'bad' : '');
    } });
  document.querySelectorAll('input[name=when]').forEach(x => x.onchange = () => { $('#bd-at').hidden = x.value !== 'at' || !x.checked; $('#bd-n').textContent = nFor(x.value); });
}

// ───────── schedule: a week calendar and a status board over the same occurrences ─────────
const SK = { deploy: ['ล้างเครื่องและติดตั้ง', 'capture', 'k-deploy'], reboot: ['รีบูตเครื่อง', 'power', 'k-reboot'], wake: ['ปลุกเครื่อง', 'power', 'k-wake'],
  update_agent: ['อัปเดต Agent', 'capture', 'k-agent'], shutdown: ['ปิดเครื่อง', 'power', 'k-reboot'] };
const SSTAT = { scheduled: ['ตั้งเวลาไว้', ''], running: ['กำลังทำ', 'info'], done: ['เสร็จแล้ว', 'ok'], failed: ['มีปัญหา', 'bad'], missed: ['ไม่ได้ทำ (server ปิดอยู่)', 'warn'], cancelled: ['ยกเลิก', ''] };
// month grid to plan; a day opens its own board (what is waiting, running, done, failed that day) with a 24-hour strip
let schedView = 'month', schedDay = (() => { const d = new Date(); d.setHours(0, 0, 0, 0); return d; })(), schedReady = true, schedCache = [];
const hhmm = d => d.toLocaleTimeString(LOC(), { hour: '2-digit', minute: '2-digit' });
const dayKey = d => `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
const sameDay = (a, b) => dayKey(a) === dayKey(b);
function monthRange(d) {
  const first = new Date(d.getFullYear(), d.getMonth(), 1), start = new Date(first); start.setDate(1 - first.getDay());
  const last = new Date(d.getFullYear(), d.getMonth() + 1, 0), end = new Date(last); end.setDate(last.getDate() + (6 - last.getDay()) + 1);
  return [start, end];
}
async function pageSchedule() {
  const [from, to] = schedView === 'day' ? [schedDay, new Date(schedDay.getTime() + 864e5)] : monthRange(schedDay);
  const [occ, hosts, sites] = await Promise.all([
    api(`/v1/schedules/occurrences?from=${encodeURIComponent(from.toISOString())}&to=${encodeURIComponent(to.toISOString())}`).then(r => { schedReady = true; return r; })
      .catch(e => { schedReady = e.status !== 404 && e.status !== 405; return []; }),
    api('/v1/hosts'), api('/v1/sites')]);
  hostList = hosts; siteCache = sites; schedCache = occ; markDups(hosts);
  const label = schedView === 'day' ? schedDay.toLocaleDateString(LOC(), { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric' })
    : schedDay.toLocaleDateString(LOC(), { month: 'long', year: 'numeric' });
  const waiting = occ.filter(o => o.status === 'scheduled').length, failed = occ.filter(o => ['failed', 'missed'].includes(o.status)).length;
  return `<header class="page-h2"><div><h2>ตารางงาน</h2>
      <p>${schedView === 'day' ? `${occ.length} งานในวันนี้` : waiting ? `รอทำ ${waiting} งานในเดือนนี้` : 'ยังไม่มีงานที่รอทำในเดือนนี้'}${failed ? `, มีปัญหา ${failed} งาน` : ''}</p></div></header>
    ${schedReady ? '' : `<div class="callout warn" style="margin-top:14px">${ic('info')}<div>ระบบตั้งเวลาฝั่ง server ยังไม่พร้อม หน้านี้แสดงแบบได้แล้ว แต่ยังสร้างงานจริงไม่ได้</div></div>`}
    <div class="ptool">
      <button class="btn" data-snav="0">วันนี้</button>
      <div class="wk-nav"><button class="btn icon ghost" data-snav="-1" aria-label="${schedView === 'day' ? 'วันก่อน' : 'เดือนก่อน'}">${ic('left')}</button>
        <button class="btn icon ghost" data-snav="1" aria-label="${schedView === 'day' ? 'วันถัดไป' : 'เดือนถัดไป'}">${ic('right')}</button></div>
      <b class="cal-title">${esc(label)}</b>
      <div class="spacer"></div>
      <div class="seg" role="tablist" aria-label="มุมมอง">${[['month', 'calendar', 'เดือน'], ['day', 'list', 'รายวัน']].map(([k, i, l]) =>
        `<button role="tab" class="${schedView === k ? 'on' : ''}" aria-selected="${schedView === k}" data-sview="${k}">${ic(i)}${l}</button>`).join('')}</div>
      <button class="btn primary" data-snew>${ic('plus')}สร้างงาน</button>
    </div>
    ${schedView === 'day' ? schedDayView(occ) : schedMonth(occ)}`;
}
function schedChip(o) {
  const k = SK[o.kind] || [o.kind, 'clock', 'k-other'];
  return `<button class="mev ${k[2]} st-${esc(o.status)}" data-socc="${esc(o.id)}"><i></i><span class="mt">${hhmm(new Date(o.at))}</span><span class="mn" translate="no">${esc(o.title || k[0])}</span></button>`;
}
function schedMonth(occ) {
  const [start] = monthRange(schedDay), m = schedDay.getMonth(), today = new Date();
  const rows = Math.round((monthRange(schedDay)[1] - start) / 864e5 / 7);
  const days = [...Array(rows * 7)].map((_, i) => new Date(start.getFullYear(), start.getMonth(), start.getDate() + i));
  const names = [...Array(7)].map((_, i) => new Date(2024, 0, 7 + i).toLocaleDateString(LOC(), { weekday: 'short' }));
  return `<div class="cal" style="--rows:${rows}"><div class="cal-head">${names.map(n => `<div>${esc(n)}</div>`).join('')}</div>
    <div class="cal-grid">${days.map(d => {
      const evs = occ.filter(o => sameDay(new Date(o.at), d)).sort((a, b) => Date.parse(a.at) - Date.parse(b.at));
      const first = d.getDate() === 1 ? d.toLocaleDateString(LOC(), { day: 'numeric', month: 'short' }) : d.getDate();
      return `<div class="cal-cell${d.getMonth() !== m ? ' out' : ''}${sameDay(d, today) ? ' today' : ''}">
        <button class="cal-day" data-sday="${d.getTime()}" aria-label="ดูงานวันที่ ${esc(d.toLocaleDateString(LOC(), { day: 'numeric', month: 'long' }))}"><span>${first}</span></button>
        <div class="cal-evs">${evs.slice(0, 3).map(schedChip).join('')}${evs.length > 3 ? `<button class="cal-more" data-sday="${d.getTime()}">อีก ${evs.length - 3} งาน</button>` : ''}</div>
        <button class="cal-add" data-sadd="${d.getTime()}" aria-label="สร้างงานวันนี้" title="สร้างงาน">${ic('plus')}</button></div>`; }).join('')}</div></div>`;
}
function schedDayView(occ) {
  const items = [...occ].sort((a, b) => Date.parse(a.at) - Date.parse(b.at)), now = new Date();
  const strip = `<div class="dstrip" aria-hidden="true"><div class="ds-hours">${[0, 3, 6, 9, 12, 15, 18, 21, 24].map(h => `<span style="left:${h / 24 * 100}%">${String(h).padStart(2, '0')}:00</span>`).join('')}</div>
    <div class="ds-track">${items.map(o => { const t = new Date(o.at), k = SK[o.kind] || [o.kind, 'clock', 'k-other'], l = (t.getHours() * 60 + t.getMinutes()) / 1440 * 100, w = Math.max(1.2, (o.duration_min || 60) / 1440 * 100);
      return `<i class="ds-ev ${k[2]} st-${esc(o.status)}" style="left:${l}%;width:${w}%" data-tip="${esc(o.title || k[0])}" data-tip-sub="${esc(hhmm(t))}"></i>`; }).join('')}
      ${sameDay(now, schedDay) ? `<i class="ds-now" style="left:${(now.getHours() * 60 + now.getMinutes()) / 1440 * 100}%"></i>` : ''}</div></div>`;
  return strip + (items.length ? schedBoard(items) : emptyCta('calendar', 'ไม่มีงานในวันนี้', 'สร้างงาน เช่น ปลุกเครื่องก่อนเรียน หรือล้างเครื่องทั้งห้องตอนกลางคืน', null, '', null)
    + `<div class="empty2-a"><button class="btn primary" data-sadd="${schedDay.getTime() + 9 * 36e5}">${ic('plus')}สร้างงานวันนี้</button></div>`);
}
// a run's stage at a glance: overall bar, then each machine with its install step, % and latest message
const RUN_STEPS = [['เตรียม', ['queued', 'retry', 'preflight', 'staging', 'prepare']], ['ดาวน์โหลด image', ['downloading', 'retry_download', 'drivers']],
  ['รีบูตเข้า WinPE', ['rebooting', 'pe_start', 'verify', 'ponr']], ['ติดตั้ง', ['partition', 'apply', 'inject', 'boot_config']], ['บูตครั้งแรก', ['first_boot']], ['เสร็จ', ['done']]];
// the server decides whether a job missed at hand-off can be resumed (answer file still on the machine)
const canRehandoff = j => !!j?.can_rehandoff;
const stepOf = ph => Math.max(0, RUN_STEPS.findIndex(([, ps]) => ps.includes(ph)));
function runSummary(o) {
  const r = o.result || {}, hs = o.detail?.hosts || [], n = hs.length || (r.ok || 0) + (r.fail || 0) + (r.running || 0);
  if (!n && o.status === 'scheduled') return '';
  const running = r.running ?? hs.filter(x => x.state === 'running').length, ok = r.ok ?? hs.filter(x => x.state === 'done' || x.ok).length, fail = r.fail ?? hs.filter(x => x.state === 'failed').length;
  const pct = r.progress ?? (hs.length ? hs.reduce((a, x) => a + (x.state === 'done' ? 100 : x.progress || 0), 0) / hs.length : o.status === 'done' ? 100 : 0);
  const el = Date.now() - Date.parse(o.at), finish = o.status === 'running' && pct >= 5 && pct < 100 ? new Date(Date.now() + el * (100 - pct) / pct) : null;
  return `<div class="run-sum ${o.status}"><div class="rs-top"><b>${o.status === 'running' ? `กำลังทำ ${Math.round(pct)}%` : o.status === 'done' ? (fail ? `เสร็จแล้ว ${ok} จาก ${n} เครื่อง` : 'เสร็จแล้ว') : o.status === 'failed' ? 'ไม่สำเร็จ' : esc(SSTAT[o.status]?.[0] || '')}</b>
      ${finish ? `<small>คาดว่าเสร็จประมาณ ${hhmm(finish)}</small>` : ''}</div>
    ${o.status !== 'running' && fail && n ? `<div class="rs-bar split"><i class="ok" style="width:${ok / n * 100}%"></i><i class="bad" style="width:${fail / n * 100}%"></i></div>`
      : `<div class="rs-bar"><i style="width:${Math.max(o.status === 'running' ? 3 : 0, Math.min(100, pct))}%"></i></div>`}
    <div class="rs-counts">${running ? `<span class="c-run"><i></i>กำลังทำ ${running} เครื่อง</span>` : ''}<span class="c-ok"><i></i>เสร็จ ${ok}</span>${fail ? `<span class="c-bad"><i></i>ไม่สำเร็จ ${fail}</span>` : ''}</div></div>`;
}
// the install agent reports its steps in English; show the common ones in plain Thai
const MSG_TH = [[/^this machine boots in legacy BIOS mode.*$/i, 'เครื่องนี้บูตแบบ Legacy BIOS (ดิสก์ MBR) ซึ่งรองรับเฉพาะแบบ "คง Partition เดิม" ให้เลือกตัวเลือกดิสก์นั้นแล้วสั่งใหม่'],
  [/^.*boots from the Windows partition itself.*$/i, 'เครื่อง BIOS เครื่องนี้บูตจาก partition ของ Windows โดยตรง (ไม่มี System Reserved แยก) ซึ่ง SWIM ยังไม่รองรับ ไม่มีอะไรถูกแก้ในเครื่อง'],
  [/^.*already has 4 primary partitions.*$/i, 'ดิสก์ MBR ของเครื่องนี้มีครบ 4 partition แล้ว จึงสร้างพื้นที่พักไฟล์ไม่ได้ ไม่มีอะไรถูกแก้ในเครื่อง'],
  [/^.*re-partitioning is not supported on legacy BIOS.*$/i, 'ดิสก์ MBR แบบ Legacy BIOS รองรับเฉพาะแบบ "คง Partition เดิม" ไม่มีอะไรถูกแก้ในเครื่อง'], [/^this machine does not boot in UEFI mode.*$/i, 'เครื่องนี้บูตแบบ Legacy BIOS ซึ่ง SWIM ยังติดตั้งให้ไม่ได้ ต้องเปลี่ยนเครื่องเป็นโหมด UEFI ก่อน (ดูเครื่องมือ "ตรวจความพร้อม UEFI / Windows 11") ไม่มีอะไรถูกแก้ในเครื่อง'], [/^the restart came back into the old Windows instead of SWIM WinPE.*\(attempt (\d+) of (\d+)\).*$/i, 'เครื่องบูตกลับเข้า Windows เดิม ระบบสั่งบูตเข้า WinPE ใหม่เอง (ครั้งที่ $1 จาก $2)'], [/^the machine restarted into the old Windows instead of SWIM WinPE (\d+) times in a row.*$/i, 'เครื่องบูตกลับเข้า Windows เดิม $1 ครั้งติดกัน ไม่ได้เข้า WinPE ไม่มีอะไรถูกแก้ในเครื่อง ลองสั่งใหม่ได้'], [/^the machine restarted into the old Windows instead of SWIM WinPE.*answer file was removed.*$/i, 'เครื่องบูตกลับเข้า Windows เดิม ไม่มีอะไรถูกแก้ ต้องสั่งล้างเครื่องและติดตั้งใหม่ (ใส่รหัสผ่านผู้ดูแลอีกครั้ง)'], [/^new Windows is up \((.+?)\),? agent re-enrolled.*$/i, 'Windows ใหม่บูตแล้ว ($1) agent กลับมาเชื่อมต่อแล้ว'], [/^server busy serving other machines.*$/i, 'server กำลังส่งให้เครื่องอื่น รอคิวดาวน์โหลด'],
  [/^the machine restarted into the old Windows instead of SWIM WinPE.*$/i, 'เครื่องรีสตาร์ตกลับเข้า Windows เดิม ไม่ได้เข้า WinPE ไม่มีอะไรถูกแก้ในเครื่อง ลองสั่งใหม่ได้'],[/^downloading image (\S+):?\s*(.*)$/i, 'กำลังดาวน์โหลด image $1 $2'], [/^using (?:the )?(?:stored|local|prestaged) image (\S+).*$/i, 'ใช้ image $1 ที่เก็บไว้ในเครื่อง ไม่ต้องดาวน์โหลด'],
  [/^applying image (\S+):?\s*(.*)$/i, 'กำลังติดตั้ง image $1 $2'], [/^rebooting into (?:SWIM )?WinPE.*$/i, 'กำลังรีบูตเข้า WinPE'], [/^verifying image.*$/i, 'กำลังตรวจ image'],
  [/^partitioning.*$/i, 'กำลังแบ่งดิสก์'], [/^injecting drivers.*$/i, 'กำลังใส่ไดรเวอร์และ agent'], [/^first boot.*$/i, 'Windows กำลังบูตครั้งแรก'],
  [/^windows installed.*$/i, 'ติดตั้ง Windows เสร็จ agent ติดต่อกลับแล้ว'], [/^waiting for (?:the )?machine.*$/i, 'รอเครื่องรับคำสั่ง']];
const thMsg = m => { if (!m || LANG === 'en') return m; for (const [re, to] of MSG_TH) if (re.test(m)) return m.replace(re, to).trim(); return m; };
function runHost(x, kind) {
  // handoff = the reboot into WinPE never happened (failed at step 3); cancelled = stopped, no step is current
  const cancelled = x.phase === 'cancelled' || x.job_status === 'cancelled', handoff = x.phase === 'handoff';
  const st = handoff ? 'failed' : cancelled ? 'cancelled' : x.state || (x.ok ? 'done' : 'failed'), deploy = kind === 'deploy';
  const cur = !deploy || cancelled ? -1 : st === 'done' ? RUN_STEPS.length - 1 : handoff ? 2 : stepOf(x.phase);
  const label = deploy ? (cancelled ? 'ยกเลิกแล้ว' : handoff ? 'รีบูตเข้า WinPE ไม่สำเร็จ' : st === 'done' ? 'ติดตั้งเสร็จ' : st === 'failed' ? 'ติดตั้งไม่สำเร็จ' : PHASE_TH[x.phase] || JOB_STATUS[x.job_status]?.[1] || 'กำลังเริ่ม') : st === 'done' ? 'สำเร็จ' : st === 'failed' ? 'ไม่สำเร็จ' : 'กำลังทำ';
  const hh = hostList.find(y => y.id === x.host_id);
  return `<li class="rh ${st}"><div class="rh-top"><i class="rh-dot"></i><a href="#/hosts/${encodeURIComponent(x.host_id)}" translate="no">${esc(x.hostname || x.host_id)}</a>${hh ? dupTag(hh) : ''}
      <span class="rh-state">${esc(label)}${st === 'running' && x.progress != null ? ` ${Math.round(x.progress)}%` : ''}</span>
      ${handoff && x.job_id && x.can_rehandoff ? `<button class="btn sm primary" data-job-rehandoff="${esc(x.job_id)}" data-label="${esc(x.hostname || '')}">ลองบูตเข้า WinPE อีกครั้ง</button>` : ''}
      ${x.job_id ? `<button class="btn sm ghost" data-job="${esc(x.job_id)}">ดูงานติดตั้ง</button>` : x.command_id ? `<button class="btn sm ghost" data-cmd-out="${esc(x.command_id)}">ดูผล</button>` : ''}</div>
    ${deploy ? `<ol class="rh-steps">${RUN_STEPS.map(([n], k) => `<li class="${k < cur || st === 'done' ? 'past' : k === cur ? (st === 'failed' ? 'fail' : 'now') : ''}"><span>${esc(n)}</span></li>`).join('')}</ol>` : ''}
    ${st === 'running' ? `<div class="rs-bar sm"><i style="width:${Math.max(3, Math.min(100, x.progress || 0))}%"></i></div>` : ''}
    ${x.message || x.detail ? `<small class="rh-msg" translate="no">${esc(thMsg(x.message || x.detail))}${x.woken ? ' (ปลุกเครื่องก่อน)' : ''}</small>` : ''}</li>`;
}
function schedPopHtml(o) {
  const k = SK[o.kind] || [o.kind, 'clock', 'k-other'], at = new Date(o.at), st = SSTAT[o.status] || [o.status, ''], hostsRes = o.detail?.hosts || [];
  return `<div class="sp-tools">${o.status === 'scheduled' ? `<button class="btn icon ghost" data-srun="${esc(o.schedule_id || o.id)}" aria-label="เริ่มตอนนี้" title="เริ่มตอนนี้">${ic('play')}</button>
      <button class="btn icon ghost" data-scancel="${esc(o.id)}" aria-label="ยกเลิกงานนี้" title="ยกเลิกงานนี้">${ic('trash')}</button>` : ''}
      <button class="btn icon ghost" data-spclose aria-label="ปิด">${ic('x')}</button></div>
    <div class="sp-title"><i></i><div><b translate="no">${esc(o.title || k[0])}</b><small>${esc(at.toLocaleDateString(LOC(), { weekday: 'long', day: 'numeric', month: 'long' }))}, ${hhmm(at)}</small></div></div>
    <ul class="sp-rows"><li>${ic(k[1])}<span>${esc(k[0])}</span></li><li>${ic('hosts')}<span>${esc(o.target_label || `${o.targets_count || 0} เครื่อง`)}</span></li>
      <li>${ic('refresh')}<span>${esc(o.repeat_label || 'ครั้งเดียว')}</span></li><li>${ic('clock')}<span><span class="badge ${st[1]}${o.status === 'running' ? ' pulse' : ''}">${esc(st[0])}</span></span></li>
      ${o.created_by ? `<li>${ic('audit')}<span>สร้างโดย ${esc(actorName(o.created_by))}</span></li>` : ''}</ul>
    ${runSummary(o)}
    ${o.detail?.note ? `<p class="sp-note">${esc(o.detail.note)}</p>` : ''}
    ${hostsRes.length ? `<ul class="run-hosts">${hostsRes.map(x => runHost(x, o.kind)).join('')}</ul>` : ''}
    <div class="sp-foot"><button class="btn sm" data-sday="${new Date(at.getFullYear(), at.getMonth(), at.getDate()).getTime()}">ดูงานทั้งวัน</button></div>`;
}
function schedPop(btn) {
  const o = schedCache.find(x => x.id === btn.dataset.socc); if (!o) return;
  $('#spop')?.remove();
  const k = SK[o.kind] || [o.kind, 'clock', 'k-other'];
  const p = document.createElement('div'); p.id = 'spop'; p.className = `spop ${k[2]}${o.detail?.hosts?.length ? ' wide' : ''}`; p.dataset.occ = o.id;
  p.setAttribute('role', 'dialog'); p.setAttribute('aria-label', o.title || k[0]);
  p.innerHTML = schedPopHtml(o);
  document.body.append(p);
  const r = btn.getBoundingClientRect(), w = p.offsetWidth, h = p.offsetHeight;
  let left = r.right + 10, top = r.top - 8;
  if (left + w > innerWidth - 12) left = Math.max(12, r.left - w - 10);
  if (top + h > innerHeight - 12) top = Math.max(12, innerHeight - h - 12);
  p.style.left = left + scrollX + 'px'; p.style.top = top + scrollY + 'px';
  p.querySelector('[data-spclose]').focus();
  const away = e => { if (!e.target.closest('#spop, [data-socc], .scrim, .drawer')) { p.remove(); document.removeEventListener('pointerdown', away, true); } };
  document.addEventListener('pointerdown', away, true);
  p.addEventListener('keydown', e => { if (e.key === 'Escape') { p.remove(); btn.focus(); } });
}
// re-fetch the schedule quietly and update the page and an open popover in place
let schedPoll = null;
async function refreshSchedule() {
  if (route() !== 'schedule') return;
  if (!document.querySelector('.scrim')) await renderPage();
  const p = $('#spop'), o = p && schedCache.find(x => x.id === p.dataset.occ);
  if (p && o) { const keep = p.querySelector('.run-hosts')?.scrollTop || 0; p.innerHTML = schedPopHtml(o); const rh = p.querySelector('.run-hosts'); if (rh) rh.scrollTop = keep; }
  clearTimeout(schedPoll);
  if (schedCache.some(x => x.status === 'running')) schedPoll = setTimeout(refreshSchedule, 15000);
}
ROUTES.schedule.after = () => { const st = $('.dstrip'); if (st) wireTips(st); clearTimeout(schedPoll); if (schedCache.some(x => x.status === 'running')) schedPoll = setTimeout(refreshSchedule, 15000); };
function schedBoard(occ) {
  const lanes = [['scheduled', 'ตั้งเวลาไว้'], ['running', 'กำลังทำ'], ['done', 'เสร็จแล้ว'], ['failed', 'มีปัญหา']];
  return `<div class="board">${lanes.map(([k, l]) => { const items = occ.filter(o => o.status === k || (k === 'failed' && o.status === 'missed')).sort((a, b) => Date.parse(a.at) - Date.parse(b.at));
    return `<section class="lane lane-${k}"><h4><i></i>${l}<span>${items.length}</span></h4>
      ${items.map(o => { const kk = SK[o.kind] || [o.kind, 'clock', 'k-other'], at = new Date(o.at);
        return `<button class="bcard" data-socc="${esc(o.id)}"><span class="bk ${kk[2]}">${ic(kk[1])}${esc(kk[0])}</span><b translate="no">${esc(o.title || kk[0])}</b>
          <span class="bmeta">${ic('clock')}${at.toLocaleDateString(LOC(), { weekday: 'short', day: 'numeric', month: 'short' })} ${hhmm(at)}</span>
          <span class="bmeta">${ic('hosts')}${esc(o.target_label || `${o.targets_count || 0} เครื่อง`)}${o.repeat_label ? `, ${esc(o.repeat_label)}` : ''}</span>
          ${o.status === 'running' ? `<div class="rs-bar sm"><i style="width:${Math.max(3, Math.min(100, o.result?.progress || 0))}%"></i></div><span class="bres">กำลังทำ ${o.result?.running ?? '?'} เครื่อง${o.result?.progress != null ? `, ${Math.round(o.result.progress)}%` : ''}</span>`
            : o.result ? `<span class="bres">สำเร็จ ${o.result.ok || 0}${o.result.fail ? `, ไม่สำเร็จ ${o.result.fail}` : ''}</span>` : ''}</button>`; }).join('') || '<p class="lane-empty">ไม่มีงาน</p>'}</section>`; }).join('')}</div>`;
}
// is this image already stored on the machine (SWIMSTAGE), so a reimage needs no download?
const storedOn = (h, img) => !!img && ((inv(h).prestage?.state === 'ready' && (inv(h).prestage.image_id === img.id || inv(h).prestage.version === img.version)) || [...(inv(h).staging?.images || []), ...(h.cached_images || [])].some(x => (x?.version || x?.image_id || x) === img.version || x?.image_id === img.id));
async function openScheduleNew(at, preset = {}) {
  const [imgs, chs] = await Promise.all([api('/v1/images').catch(() => []), api('/v1/channels').catch(() => [])]);
  imageCache = imgs; channelCache = chs;
  const chOf = x => chs.find(c => c.id === x.channel_id);
  const pub = imgs.filter(x => x.status === 'published').sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at));
  const gm = pub.filter(x => !isIsoChannel(chOf(x))), iso = pub.filter(x => isIsoChannel(chOf(x)));
  const d = at ? new Date(at) : new Date(Date.now() + 36e5); if (!at) d.setMinutes(0, 0, 0);
  const local = new Date(d.getTime() - d.getTimezoneOffset() * 6e4).toISOString().slice(0, 16);
  const days = ['จ', 'อ', 'พ', 'พฤ', 'ศ', 'ส', 'อา'];
  const pick = (x, first) => { const c = chOf(x), isI = isIsoChannel(c);
    return `<label class="imgpick"><input type="radio" name="image_id" value="${esc(x.id)}" ${preset.image_id ? (preset.image_id === x.id ? 'checked' : '') : first ? 'checked' : ''}>
      <span class="ip-ic">${ic(isI ? 'hdd' : 'builder')}</span><span class="ip-t"><b translate="no">${esc(x.version)}</b>
      <small>${esc(isI ? `Windows ${c?.edition || ''}, จากแผ่น ISO` : `${c?.name || ''}, จากเครื่องต้นแบบ`)}</small></span><span class="ip-s">${fmtBytes(x.size_bytes)}</span></label>`; };
  modal({ title: 'สร้างงานตั้งเวลา', wide: true, confirm: 'บันทึกงาน',
    body: `<div class="row-fields"><div class="field"><label>งาน</label><select class="select" name="kind" id="sn-kind">${Object.entries(SK).map(([k, v]) => `<option value="${k}" ${preset.kind === k ? 'selected' : ''}>${esc(v[0])}</option>`).join('')}</select></div>
        <div class="field"><label>ชื่องาน (ถ้ามี)</label><input class="input" name="title" placeholder="เช่น ล้างเครื่องห้อง E12-101 ก่อนสอบ"></div></div>
      <fieldset class="sn-deploy" id="sn-deploy">
        <div class="field"><label>Image ที่จะติดตั้ง</label>
          ${pub.length ? `${gm.length ? `<div class="bd-sub">จากเครื่องต้นแบบ</div><div class="imgpicks">${gm.map((x, k) => pick(x, k === 0)).join('')}</div>` : ''}
            ${iso.length ? `<div class="bd-sub">จากแผ่น ISO</div><div class="imgpicks">${iso.map((x, k) => pick(x, !gm.length && k === 0)).join('')}</div>` : ''}`
          : `<div class="callout warn">${ic('alert')}<div>ยังไม่มี image ที่อนุมัติแล้ว สร้างหรือนำเข้า ISO ที่หน้า Images ก่อน</div></div>`}</div>
        <div class="sn-stored" id="sn-stored" aria-live="polite"></div>
        <div class="field"><label>การจัดดิสก์</label>
          <label class="radio"><input type="radio" name="mode" value="refresh" checked> <b>คงพาร์ทิชันเดิม</b>, format เฉพาะไดรฟ์ Windows (C:)</label>
          <label class="radio"><input type="radio" name="mode" value="repartition"> <b>แบ่งดิสก์ใหม่</b>, ลบทุกพาร์ทิชันรวม D:</label></div>
        <div class="row-fields"><div class="field"><label>บัญชีผู้ดูแลในเครื่องใหม่</label><input class="input" name="admin_user" value="swimadmin" autocomplete="off"></div>
          <div class="field"><label>รหัสผ่าน</label><input class="input" name="admin_password" type="password" minlength="8" autocomplete="new-password"></div>
          <div class="field"><label>ยืนยันรหัสผ่าน</label><input class="input" name="admin_password2" type="password" minlength="8" autocomplete="new-password"></div></div>
        <p class="hint" style="margin:-4px 0 0">server เก็บรหัสผ่านแบบเข้ารหัส และลบทิ้งหลังงานครั้งเดียวทำเสร็จหรือถูกยกเลิก</p>
      </fieldset>
      <div class="row-fields"><div class="field"><label>เครื่อง</label><select class="select" name="target" id="sn-target">
          ${siteCache.map(x => `<option value="site:${esc(x.id)}" ${preset.target === 'site:' + x.id ? 'selected' : ''}>ทุกเครื่องในห้อง ${esc(x.name)} (${x.hosts})</option>`).join('')}
          ${hostList.filter(h => !h.retired_at).map(h => `<option value="host:${esc(h.id)}" ${preset.target === 'host:' + h.id ? 'selected' : ''}>${esc(h.hostname || h.id)}${h.site_name ? `, ห้อง ${esc(h.site_name)}` : ''}</option>`).join('')}</select></div>
        <div class="field"><label>เริ่มเวลา</label><input class="input" type="datetime-local" name="at" required value="${preset.at || local}"></div></div>
      <div class="field"><label>ทำซ้ำ</label><div class="seg bd-when"><label><input type="radio" name="freq" value="none" checked><span>ครั้งเดียว</span></label>
        <label><input type="radio" name="freq" value="daily"><span>ทุกวัน</span></label><label><input type="radio" name="freq" value="weekly"><span>ทุกสัปดาห์</span></label></div>
        <div class="dchips" id="sn-days" hidden>${days.map((x, k) => `<label><input type="checkbox" name="d${k + 1}" ${k === (d.getDay() + 6) % 7 ? 'checked' : ''}><span>${x}</span></label>`).join('')}</div></div>
      <p class="hint" id="sn-off">เครื่องที่ออฟไลน์ตอนถึงเวลาจะถูกปลุกก่อน แล้วรอได้ 5 นาที</p>`,
    onConfirm: async f => {
      const at2 = new Date(f.at); if (!(at2 > new Date())) throw new Error('เลือกเวลาในอนาคต');
      const [tt, tid] = f.target.split(':'), targets = tt === 'site' ? { site_id: tid } : { host_ids: [tid] };
      const repeat = { freq: f.freq, days: f.freq === 'weekly' ? [1, 2, 3, 4, 5, 6, 7].filter(k => f['d' + k]) : [] };
      let args = {};
      if (f.kind === 'deploy') {
        if (!f.image_id) throw new Error('เลือก image ที่จะติดตั้ง');
        if (!f.admin_user?.trim()) throw new Error('ใส่ชื่อบัญชีผู้ดูแล');
        if ((f.admin_password || '').length < 8) throw new Error('รหัสผ่านอย่างน้อย 8 ตัวอักษร');
        if (f.admin_password !== f.admin_password2) throw new Error('รหัสผ่านสองช่องไม่ตรงกัน');
        args = { image_id: f.image_id, disk: { mode: f.mode, system_gb: 0, data_label: '' }, admin_user: f.admin_user.trim(), admin_password: f.admin_password };
      }
      await api('/v1/schedules', 'POST', { kind: f.kind, title: f.title.trim() || undefined, start_at: at2.toISOString(), repeat, targets, args });
      toast('บันทึกงานแล้ว'); renderPage();
    } });
  const kind = $('#sn-kind'), dep = $('#sn-deploy'), target = $('#sn-target');
  const targetHosts = () => { const [tt, tid] = target.value.split(':'); return tt === 'site' ? hostList.filter(h => h.site_id === tid && !h.retired_at) : hostList.filter(h => h.id === tid); };
  const stored = () => {
    const box = $('#sn-stored'), img = pub.find(x => x.id === document.querySelector('input[name=image_id]:checked')?.value), hs = targetHosts();
    if (!img || !hs.length) { box.innerHTML = ''; return; }
    const have = hs.filter(h => storedOn(h, img)), miss = hs.filter(h => !storedOn(h, img));
    box.innerHTML = `<div class="sn-st-head">${ic(have.length === hs.length ? 'check' : 'capture')}<b>${have.length === hs.length ? `ทุกเครื่องเก็บ ${esc(img.version)} ไว้แล้ว ไม่ต้องดาวน์โหลด`
        : have.length ? `เก็บไว้แล้ว ${have.length} จาก ${hs.length} เครื่อง ที่เหลือจะดาวน์โหลดจาก server ตอนเริ่มงาน` : `ยังไม่มีเครื่องไหนเก็บ ${esc(img.version)} ไว้ จะดาวน์โหลดจาก server ตอนเริ่มงาน (${fmtBytes(img.size_bytes)} ต่อเครื่อง)`}</b></div>
      <ul class="sn-st-list">${hs.slice(0, 12).map(h => `<li class="${storedOn(h, img) ? 'ok' : ''}"><i></i><span translate="no">${esc(h.hostname || h.id)}</span><small>${storedOn(h, img) ? 'เก็บไว้ในเครื่องแล้ว' : 'ต้องดาวน์โหลด'}</small></li>`).join('')}
        ${hs.length > 12 ? `<li class="more">และอีก ${hs.length - 12} เครื่อง</li>` : ''}</ul>`;
  };
  const sync = () => {
    const on = kind.value === 'deploy';
    dep.hidden = !on; dep.disabled = !on;
    if (on) stored();
  };
  kind.onchange = sync; target.onchange = () => { if (kind.value === 'deploy') stored(); };
  document.querySelectorAll('#sn-deploy input[name=image_id]').forEach(x => x.onchange = stored);
  document.querySelectorAll('input[name=freq]').forEach(x => x.onchange = () => { $('#sn-days').hidden = !document.querySelector('input[name=freq][value=weekly]').checked; });
  sync();
}
function openScheduleItem(id) {
  const o = schedCache.find(x => x.id === id); if (!o) return;
  const k = SK[o.kind] || [o.kind], at = new Date(o.at);
  modal({ title: o.title || k[0], confirm: o.status === 'scheduled' ? 'ยกเลิกงานนี้' : 'ปิด', danger: o.status === 'scheduled',
    desc: `${esc(k[0])}, ${esc(at.toLocaleString(LOC(), { dateStyle: 'full', timeStyle: 'short' }))}`,
    body: `<div class="facts" style="padding:0">
      <div class="fact"><span>สถานะ</span><b><span class="badge ${SSTAT[o.status]?.[1] || ''}">${esc(SSTAT[o.status]?.[0] || o.status)}</span></b></div>
      <div class="fact"><span>เครื่อง</span><b>${esc(o.target_label || `${o.targets_count || 0} เครื่อง`)}</b></div>
      <div class="fact"><span>ทำซ้ำ</span><b>${esc(o.repeat_label || 'ครั้งเดียว')}</b></div>
      <div class="fact"><span>สร้างโดย</span><b>${esc(actorName(o.created_by || ''))}</b></div>
      ${o.result ? `<div class="fact"><span>ผล</span><b>สำเร็จ ${o.result.ok || 0}, ไม่สำเร็จ ${o.result.fail || 0}</b></div>` : ''}</div>`,
    onConfirm: async () => { if (o.status !== 'scheduled') return;
      await api('/v1/schedules/' + encodeURIComponent(o.schedule_id || o.id) + (o.occurrence ? '/occurrences/' + encodeURIComponent(o.occurrence) : ''), 'DELETE'); toast('ยกเลิกงานแล้ว'); renderPage(); } });
}

async function openCommandOutput(id) {
  closeDrawer();
  const c = await api('/v1/commands/' + encodeURIComponent(id));
  const r = c.result || {}, a = c.args || {};
  const scrim = document.createElement('div'); scrim.className = 'scrim'; scrim.style.background = 'rgba(9,14,22,.3)';
  const d = document.createElement('aside'); d.className = 'drawer';
  const dur = c.finished_at && (c.delivered_at || c.created_at) ? (Date.parse(c.finished_at) - Date.parse(c.delivered_at || c.created_at)) / 1000 : null;
  const host = hostList.find(h => h.id === c.host_id)?.hostname || hostCache?.hostname || c.host_id;
  const fact = (k, v) => `<div class="fact"><span>${k}</span><b>${v}</b></div>`;
  d.innerHTML = `<div class="drawer-head"><div class="dot-icon accent">${ic(CMD[c.type]?.[1] || 'log')}</div><div style="flex:1;min-width:0">
      <h3>${esc(CMD[c.type]?.[0] || c.type)}${a.name ? ' · ' + esc(a.name) : ''}</h3><div class="mute mono" style="font-size:11.5px">${esc(c.id)}</div></div>
      ${cmdBadge(c.status)}<button class="btn icon ghost" data-close aria-label="ปิด">${ic('x')}</button></div>
    <div style="overflow:auto;flex:1">
      <div class="facts" style="padding:14px 18px">
        ${fact('เครื่อง', `<a href="#/hosts/${encodeURIComponent(c.host_id)}">${esc(host)}</a>`)}${fact('สั่งโดย', esc(actorName(c.created_by)))}
        ${fact('สั่งเมื่อ', esc(fmtDate(c.created_at)))}${fact('ใช้เวลา', dur != null ? fmtClock(dur) : '—')}
        ${fact('Exit code', r.exit_code != null ? `<span class="mono">${esc(r.exit_code)}</span>` : '—')}${a.sha256 ? fact('SHA-256 ของสคริปต์', `<span class="mono" style="font-size:11px">${esc(a.sha256)}</span>`) : ''}</div>
      ${a.body ? `<div class="sub-head" style="padding:4px 18px"><h3>สคริปต์ที่รัน</h3></div><pre class="code" style="margin:0 18px 14px;max-height:280px" id="co-script"></pre>` : ''}
      <div class="sub-head" style="padding:4px 18px;display:flex;align-items:center"><h3 style="flex:1">ผลลัพธ์</h3>
        ${r.output ? `<button class="btn sm ghost" id="co-dl">${ic('capture')}ดาวน์โหลด .txt</button>` : ''}</div>
      <pre id="co-out" style="margin:0 18px 18px"></pre></div>`;
  let text = '';
  if (r.error) text += 'ERROR: ' + r.error + '\n\n';
  if (r.output) text += r.output + (r.output_truncated ? '\n\n(เครื่องส่งมามากกว่า 512 KB — เก็บไว้เฉพาะ 512 KB ท้ายสุด)' : '');
  if (r.data) text += '\n\n---- data ----\n' + JSON.stringify(r.data, null, 2);
  d.querySelector('#co-out').textContent = text || '(ไม่มีผลลัพธ์)';
  if (a.body) d.querySelector('#co-script').textContent = a.body;
  const dl = d.querySelector('#co-dl');
  if (dl) dl.onclick = () => { const blob = new Blob([text], { type: 'text/plain;charset=utf-8' }), el = document.createElement('a');
    el.href = URL.createObjectURL(blob); el.download = `${host}-${c.type}-${c.id}.txt`; document.body.appendChild(el); el.click(); el.remove(); };
  scrim.onclick = closeDrawer; d.querySelector('[data-close]').onclick = closeDrawer;
  document.body.append(scrim, d); document.addEventListener('keydown', escDrawer);
}

// ───────── Wake-on-LAN: send, then watch until the machine comes online ─────────
const wakeStore = { get() { try { return JSON.parse(localStorage.getItem('swim-wake') || '{}'); } catch (e) { return {}; } },
  set(o) { try { localStorage.setItem('swim-wake', JSON.stringify(o)); } catch (e) {} } };
let wakeMap = wakeStore.get();
function wakeState(h) {
  const t = wakeMap[h.id]; if (!t) return null;
  const age = Date.now() - t;
  if (h.online) return age < 10 * 60e3 ? 'up' : null;
  return age > 30 * 60e3 ? null : age > 5 * 60e3 ? 'late' : 'waiting';
}
function wakeBox(h) {
  const st = wakeState(h); if (!st) return '';
  if (st === 'up') return `<div class="enroll-live ok">${ic('check')}<div><b>ออนไลน์แล้ว</b><span>เครื่องตื่นหลังส่งสัญญาณปลุก ${fmtClock((Date.now() - wakeMap[h.id]) / 1000)} ที่แล้ว</span></div></div>`;
  if (st === 'waiting') return progressBox({ title: 'ส่งสัญญาณปลุกแล้ว รอเครื่องออนไลน์…', detail: 'เมื่อ Windows บูตเสร็จและ Agent ติดต่อกลับ สถานะจะเปลี่ยนเอง',
    since: wakeMap[h.id], finishAt: wakeMap[h.id] + 120e3 });
  return `<div class="callout warn">${ic('alert')}<div><b>เครื่องไม่ตอบภายใน 5 นาที</b> — ตรวจว่าเปิด Wake-on-LAN ใน BIOS/UEFI และในการ์ดเครือข่าย
    (Device Manager → Network adapter → Power Management → Allow this device to wake the computer) และปิด Fast Startup ของ Windows
    · เครื่องเสมือน (เช่น KubeVirt) ส่วนใหญ่ไม่รองรับ Wake-on-LAN
    <div style="margin-top:8px"><button class="btn sm" data-wake="${esc(h.id)}" data-label="${esc(h.hostname || h.id)}">${ic('power')}ส่งสัญญาณปลุกอีกครั้ง</button></div></div></div>`;
}
function askWake(ids, label) {
  return modal({ title: 'ปลุกเครื่อง (Wake-on-LAN)?', confirm: 'ส่งสัญญาณปลุก',
    desc: `ส่งสัญญาณปลุกไปที่ <b>${esc(label)}</b> — เครื่องที่เปิดอยู่แล้วหรือยังไม่รู้ MAC address จะถูกข้าม`,
    onConfirm: async () => {
      const r = await api('/v1/hosts/wake', 'POST', { host_ids: ids });
      for (const t of r.sent || []) wakeMap[t.host_id] = Date.now();
      wakeStore.set(wakeMap);
      const via = (r.relays || []).filter(x => x.command_id).map(x => x.hostname);
      toast(`ส่งสัญญาณปลุกแล้ว ${(r.sent || []).length} เครื่อง${via.length ? ` (ผ่าน server และ ${via.join(', ')})` : ''}${(r.skipped || []).length ? ` · ข้าม ${r.skipped.length}: ` + r.skipped.map(x => `${x.hostname || x.host_id} (${x.reason})`).join(', ') : ''}`);
      renderPage();
    } });
}
function askClearHistory(ids, label) {
  return modal({ title: 'ล้างประวัติคำสั่ง?', danger: true, confirm: 'ล้างประวัติ',
    desc: `ซ่อนคำสั่งที่จบแล้วของ <b>${esc(label)}</b> ออกจากหน้าเครื่อง — คำสั่งที่ยังรอหรือกำลังทำไม่ถูกแตะ และทุกคำสั่งยังดูย้อนหลังได้ใน <b>ประวัติการใช้งาน</b>`,
    onConfirm: async () => { const r = await api('/v1/hosts/commands/clear', 'POST', { host_ids: ids }); toast(`ล้างแล้ว ${r.cleared} คำสั่ง`); hostSel.clear(); renderPage(); } });
}

async function sendCommand(type, args = {}, reason = '') {
  const r = await api('/v1/hosts/' + encodeURIComponent(hostCache.id) + '/commands', 'POST', { type, args, reason });
  toast(`ส่ง "${CMD[type][0]}" แล้ว — รอเครื่องรับ`);
  await renderPage(); schedulePoll();
  return r;
}

// ───────── scripts ─────────
// ───────── actions (event delegation) ─────────
// a room link opens the machines list filtered to that room
document.addEventListener('click', ev => { const a = ev.target.closest('[data-site-filter-link]'); if (!a) return; siteFilter = a.dataset.siteFilterLink || 'all'; hostFilter = ''; hostQuick = 'all'; }, true);
document.addEventListener('click', async ev => {
  const b = ev.target.closest('button,[data-log]');
  if (!b) return;

  if (b.dataset.log) { const t = lastTasks.find(x => x.id === b.dataset.log); if (t) openLog(t); return; }

  if (b.dataset.approve) {
    return modal({ title: 'อนุมัติ image นี้?', confirm: 'อนุมัติและเผยแพร่',
      desc: `<b class="mono">${esc(b.dataset.label)}</b> จะกลายเป็นเวอร์ชันที่ใช้งานของหัวข้อนี้ และเวอร์ชันเดิมจะถูกแทนที่`,
      onConfirm: async () => { await api('/v1/images/' + encodeURIComponent(b.dataset.approve) + '/approve', 'POST', {}); closeDrawer(); toast('อนุมัติแล้ว'); renderPage(); } });
  }

  if (b.dataset.deploy) return openDeploy(b.dataset.deploy);
  if (b.dataset.job) return openJob(b.dataset.job);
  if (b.dataset.jobCancel) {
    return modal({ title: 'ยกเลิกงานติดตั้ง?', confirm: 'ยกเลิกงาน', desc: 'ทำได้ก่อนเครื่องรีบูตเข้า WinPE เท่านั้น — เครื่องจะไม่ถูกแตะ',
      onConfirm: async () => { await api('/v1/jobs/' + encodeURIComponent(b.dataset.jobCancel) + '/cancel', 'POST', {}); toast('ยกเลิกแล้ว'); renderPage(); } });
  }
  if (b.dataset.jobRehandoff) {
    const label = b.dataset.label || 'เครื่องนี้';
    return modal({ title: 'ลองบูตเข้า WinPE อีกครั้ง?', confirm: 'รีสตาร์ตเข้า WinPE',
      desc: `<b translate="no">${esc(label)}</b> จะรีสตาร์ตภายใน 15 วินาที แล้วบูตเข้า SWIM WinPE เพื่อติดตั้งต่อด้วยไฟล์ที่เก็บไว้ในเครื่อง ไม่ต้องดาวน์โหลดและไม่ต้องใส่รหัสผ่านใหม่
        งานที่ยังไม่บันทึกบนเครื่องจะหาย`,
      onConfirm: async () => { await api('/v1/jobs/' + encodeURIComponent(b.dataset.jobRehandoff) + '/rehandoff', 'POST', {}); toast('ส่งคำสั่งแล้ว เครื่องจะรีสตาร์ตเข้า WinPE'); closeDrawer(); renderPage(); } });
  }
  if (b.dataset.jobRetry) {
    const isoOf = x => /^iso-/.test(channelCache.find(c => c.id === x.channel_id)?.name || '');
    const imgs = imageCache.filter(x => x.status === 'published' && (!hostCache?.channel_id || x.channel_id === hostCache.channel_id) && !isoOf(x));
    const isoI = imageCache.filter(x => x.status === 'published' && isoOf(x));
    return modal({ title: 'ลองติดตั้งใหม่', confirm: 'ลองใหม่',
      desc: 'เครื่องที่รออยู่ใน WinPE จะดาวน์โหลด image ที่เลือก (และ WinPE รุ่นล่าสุด) แล้วเริ่มแบ่งดิสก์และติดตั้งใหม่ ใช้การตั้งค่าดิสก์ ชื่อเครื่อง และบัญชีผู้ดูแลเดิม',
      body: `<div class="field"><label>Image</label><select class="select" name="image_id"><optgroup label="จากเครื่องต้นแบบ">${imgs.map(x => `<option value="${esc(x.id)}">${esc(x.version)}</option>`).join('')}</optgroup>
          ${isoI.length ? `<optgroup label="จากแผ่น ISO">${isoI.map(x => `<option value="${esc(x.id)}">${esc(x.version)} · ${esc(channelCache.find(c => c.id === x.channel_id)?.edition || '')}</option>`).join('')}</optgroup>` : ''}</select>
          <span class="hint">ต้องเป็น image ที่ไม่มีไฟล์บีบอัดแบบ CompactOS (capture หลังวันที่ 25 พ.ย.)</span></div>`,
      onConfirm: async f => { await api('/v1/jobs/' + encodeURIComponent(b.dataset.jobRetry) + '/retry', 'POST', f); toast('ส่งคำสั่งลองใหม่แล้ว — เครื่องจะรับภายใน 20 วินาที'); renderPage(); } });
  }
  if (b.dataset.hostView) { viewPref.set(b.dataset.hostView); return renderPage(true); }
  if (b.dataset.siteCmd !== undefined) return openBulkCommand({ site_id: b.dataset.siteCmd }, 'ทุกเครื่องใน ' + b.dataset.siteName);
  if (b.hasAttribute('data-map-new-site')) {
    return modal({ title: 'เพิ่ม site', desc: 'Site คือกลุ่มของเครื่อง เช่น อาคาร ห้องแล็บ หรือแผนก — สร้างแล้วลากเครื่องมาวางได้เลย', confirm: 'สร้าง',
      body: '<div class="field"><label>ชื่อ site</label><input class="input" name="name" required placeholder="เช่น อาคาร A"></div>',
      onConfirm: async f => { await api('/v1/sites', 'POST', { name: f.name.trim() }); toast('สร้าง site แล้ว'); renderPage(); } });
  }
  if (b.dataset.stagedDelete) {
    const v = b.dataset.stagedDelete, h = hostCache;
    return modal({ title: 'ลบ image ที่เก็บในเครื่อง?', danger: true, confirm: 'ลบ',
      desc: `ลบ <b class="mono">${esc(v)}</b> (${fmtBytes(Number(b.dataset.size))}) ออกจากพาร์ทิชัน SWIMSTAGE ของ <b>${esc(h.hostname || h.id)}</b>
        — Windows ที่ติดตั้งอยู่ไม่ได้รับผลกระทบ ถ้าจะติดตั้งเวอร์ชันนี้อีก เครื่องจะดาวน์โหลดใหม่${h.online ? '' : '<br><br>เครื่องออฟไลน์อยู่ — คำสั่งจะรอในคิว 1 ชั่วโมง'}`,
      onConfirm: async () => { await sendCommand('delete_staged_image', { version: v }); } });
  }
  if (b.dataset.wake) return askWake(b.dataset.wake.split(','), b.dataset.label || 'เครื่องนี้');
  if (b.dataset.clearHistory) return askClearHistory([b.dataset.clearHistory], b.dataset.label);
  if (b.dataset.jobDelete) return modal({ title: 'ลบรายการนี้ออกจากประวัติ?', danger: true, confirm: 'ลบ',
    desc: `งานติดตั้ง <b class="mono">${esc(b.dataset.version)}</b> (${esc(b.dataset.label)}) — ลบเฉพาะบันทึกงานติดตั้งนี้ Windows ในเครื่องไม่ได้รับผลกระทบ (ยังดูย้อนหลังได้ในประวัติการใช้งาน)`,
    onConfirm: async () => { await api('/v1/jobs/' + encodeURIComponent(b.dataset.jobDelete), 'DELETE', {}); toast('ลบรายการแล้ว'); renderPage(); } });
  if (b.dataset.jobsClear) {
    const st = inv(hostCache).staging;
    return modal({ title: 'ล้างประวัติการติดตั้ง?', danger: true, confirm: 'ล้างประวัติ',
      desc: `ลบบันทึกงานติดตั้งที่จบแล้วทั้งหมดของ <b>${esc(b.dataset.label)}</b> — Windows ในเครื่องไม่ได้รับผลกระทบ และยังดูย้อนหลังได้ในประวัติการใช้งาน
        ${st ? '<br><br>ถ้าไม่มีงานที่ล้มเหลวค้างอยู่ ระบบจะคืนพื้นที่ที่ใช้ติดตั้ง (SWIMSTAGE) ให้ไดรฟ์ C: ด้วยอัตโนมัติ' : ''}`,
      onConfirm: async () => { await api('/v1/hosts/' + encodeURIComponent(b.dataset.jobsClear) + '/jobs', 'DELETE', {}); toast('ล้างประวัติการติดตั้งแล้ว'); renderPage(); } });
  }
  if (b.dataset.releaseStaging) {
    const h = hostCache, last = (jobCache || [])[0], keep = last && ['failed', 'aborted'].includes(last.status);
    return modal({ title: 'คืนพื้นที่ที่ใช้ติดตั้ง?', confirm: 'คืนพื้นที่',
      desc: `ลบพาร์ทิชันชั่วคราว SWIMSTAGE (${fmtBytes(Number(b.dataset.size))}) ของ <b>${esc(h.hostname || h.id)}</b> แล้วขยายไดรฟ์ C: ให้เต็มพื้นที่ — Windows และไฟล์ในเครื่องไม่ได้รับผลกระทบ
        ${keep ? '<br><br><span style="color:var(--warn)">งานติดตั้งล่าสุดไม่สำเร็จ — ถ้าคืนพื้นที่ การลองใหม่ครั้งหน้าเครื่องจะต้องดาวน์โหลด image ใหม่ทั้งหมด</span>' : ''}
        ${h.online ? '' : '<br><br>เครื่องออฟไลน์อยู่ — คำสั่งจะรอในคิว 1 ชั่วโมง'}`,
      onConfirm: () => sendCommand('release_staging') });
  }
  if (b.hasAttribute('data-log-csv')) return exportLogsCSV();
  if (b.hasAttribute('data-log-reset')) { logFilter = { actor: '', host: '', action: '', from: '', to: '', q: '' }; return renderPage(); }
  if (b.dataset.image) return openImage(b.dataset.image);
  if (b.dataset.imageDelete) {
    const ver = b.dataset.version, inUse = b.dataset.status === 'published';
    return modal({ title: 'ลบ image เวอร์ชันนี้?', danger: true, confirm: 'ลบถาวร',
      desc: `<b class="mono">${esc(b.dataset.label)}</b> — ไฟล์ image จะถูกลบออกจาก server และกู้คืนไม่ได้ (ประวัติการติดตั้งเก่ายังอยู่)`,
      body: `${inUse ? `<div class="callout bad">${ic('alert')}<div>นี่คือ<b>เวอร์ชันที่ใช้งานอยู่</b> — หลังลบ หัวข้อนี้จะไม่มีเวอร์ชันให้ติดตั้ง จนกว่าจะอนุมัติเวอร์ชันใหม่</div></div>` : ''}
        
        <div class="field"><label>พิมพ์ <b class="mono">${esc(ver)}</b> เพื่อยืนยัน</label><input class="input mono" name="confirm" required autocomplete="off"></div>`,
      onConfirm: async f => {
        if (f.confirm.trim() !== ver) throw new Error('ชื่อเวอร์ชันไม่ตรง');
        const r = await api('/v1/images/' + encodeURIComponent(b.dataset.imageDelete), 'DELETE', {});
        closeDrawer(); toast(`ลบ ${ver} แล้ว${r.file_removed ? ' — คืนพื้นที่ server แล้ว' : ''}`); await refreshMeta(); renderShell();
      } });
  }
  if (b.dataset.stage) { const [vm, k] = b.dataset.stage.split('|'); stageView[vm] = Number(k); imagesQuick = true; return renderPage(); }
  if (b.dataset.siteFilter !== undefined) { siteFilter = b.dataset.siteFilter; hostSel.clear(); if (route() !== 'hosts') { location.hash = '#/hosts'; return; } return renderPage(); }
  if (b.hasAttribute('data-sites-manage')) return openSitesManager();
  if (b.dataset.siteRename) {
    const st = siteCache.find(x => x.id === b.dataset.siteRename); document.querySelector('.scrim')?.remove();
    return modal({ title: 'เปลี่ยนชื่อ site', confirm: 'บันทึก', body: `<div class="field"><label>ชื่อ</label><input class="input" name="name" required value="${esc(st.name)}"></div>
        <div class="field"><label>คำอธิบาย</label><input class="input" name="description" value="${esc(st.description || '')}"></div>`,
      onConfirm: async f => { await api('/v1/sites/' + encodeURIComponent(st.id), 'PATCH', f); toast('บันทึกแล้ว'); renderPage(); } });
  }
  if (b.dataset.siteDelete) {
    const st = siteCache.find(x => x.id === b.dataset.siteDelete); document.querySelector('.scrim')?.remove();
    return modal({ title: `ลบ site ${st.name}?`, danger: true, confirm: 'ลบ', desc: 'ลบได้เฉพาะ site ที่ไม่มีเครื่องแล้ว',
      onConfirm: async () => { await api('/v1/sites/' + encodeURIComponent(st.id), 'DELETE'); if (siteFilter === st.id) siteFilter = 'all'; toast('ลบ site แล้ว'); renderPage(); } });
  }
  if (b.hasAttribute('data-site-command')) {
    const label = siteFilter === 'none' ? 'เครื่องที่ไม่ระบุ site' : 'ทุกเครื่องใน ' + (siteCache.find(x => x.id === siteFilter)?.name || '');
    return openBulkCommand({ site_id: siteFilter === 'none' ? '' : siteFilter }, label);
  }
  if (b.dataset.sview) { schedView = b.dataset.sview; return renderPage(); }
  if (b.dataset.snav !== undefined) {
    const n = Number(b.dataset.snav), d = new Date(schedDay);
    if (!n) { const t = new Date(); t.setHours(0, 0, 0, 0); schedDay = t; } else if (schedView === 'day') { d.setDate(d.getDate() + n); schedDay = d; } else schedDay = new Date(d.getFullYear(), d.getMonth() + n, 1);
    return renderPage();
  }
  if (b.dataset.sday) { $('#spop')?.remove(); schedDay = new Date(Number(b.dataset.sday)); schedView = 'day'; return renderPage(); }
  if (b.dataset.sadd) return openScheduleNew(Number(b.dataset.sadd) + (new Date(Number(b.dataset.sadd)).getHours() ? 0 : 20 * 36e5));
  if (b.hasAttribute('data-snew')) return openScheduleNew();
  if (b.hasAttribute('data-spclose')) return $('#spop')?.remove();
  if (b.dataset.srun) {
    const o = schedCache.find(x => (x.schedule_id || x.id) === b.dataset.srun); $('#spop')?.remove();
    return modal({ title: 'เริ่มงานนี้ตอนนี้?', confirm: 'เริ่มตอนนี้', desc: `<b>${esc(o?.title || '')}</b> จะเริ่มทันทีกับ ${esc(o?.target_label || 'เครื่องที่เลือก')} เครื่องที่ออฟไลน์จะถูกปลุกก่อน (รอได้ 5 นาที) งานตามเวลาเดิมยังอยู่`,
      onConfirm: async () => { await api('/v1/schedules/' + encodeURIComponent(b.dataset.srun) + '/run-now', 'POST', {}); toast('เริ่มงานแล้ว ผลจะขึ้นในตารางงาน'); renderPage(); } });
  }
  if (b.dataset.scancel) { $('#spop')?.remove(); return openScheduleItem(b.dataset.scancel); }
  if (b.dataset.socc) return schedPop(b);
  if (b.dataset.prestage) { const h = hostCache?.id === b.dataset.prestage ? hostCache : hostList.find(x => x.id === b.dataset.prestage); return askPrestage([h.id], h.hostname || h.id, h.policy?.prestage_image_id || ''); }
  if (b.dataset.mapMore) { const k = b.dataset.mapMore; mapExpanded.has(k) ? mapExpanded.delete(k) : mapExpanded.add(k); return renderPage(); }
  if (b.hasAttribute('data-hmore')) { hostLimit += 100; return renderPage(); }
  if (b.hasAttribute('data-hall')) { hostLimit = 1e9; return renderPage(); }
  if (b.dataset.hquick) { hostQuick = b.dataset.hquick; hostLimit = 100; return renderPage(); }
  if (b.hasAttribute('data-hosts-reset')) { hostQuick = 'all'; hostFilter = ''; siteFilter = 'all'; return renderPage(); }
  if (b.hasAttribute('data-hosts-refresh')) return renderPage();
  if (b.dataset.htab) {
    hostTab = b.dataset.htab;
    document.querySelectorAll('.htabs [data-htab]').forEach(x => { const on = x.dataset.htab === hostTab; x.classList.toggle('on', on); x.setAttribute('aria-selected', on); });
    document.querySelectorAll('.htab-panel').forEach(p => { p.hidden = p.dataset.panel !== hostTab; });
    if (!b.closest('.htabs')) $('.htabs')?.scrollIntoView({ block: 'nearest' });
    return;
  }
  if (b.dataset.lock) { const h = hostCache; return askLock([h.id], h.hostname || h.id, b.dataset.lock === 'on'); }
  if (b.dataset.hmenu) return hostMenu(b);
  if (b.dataset.me) {
    closeHostMenu();
    if (b.dataset.me === 'logout') return modal({ title: 'ออกจากระบบ?', confirm: 'ออกจากระบบ', desc: `ออกจากระบบในชื่อ <b translate="no">${esc(me?.name || '')}</b>`, onConfirm: async () => logout(false) });
    if (b.dataset.me === 'password') return modal({ title: 'เปลี่ยนรหัสผ่าน', confirm: 'บันทึกรหัสผ่านใหม่', desc: 'อย่างน้อย 10 ตัวอักษร เครื่องอื่นที่เข้าระบบด้วยบัญชีนี้จะถูกออกจากระบบ',
      body: `<div class="field"><label>รหัสผ่านปัจจุบัน</label><input class="input" type="password" name="current" autocomplete="current-password" required></div>
        <div class="row-fields"><div class="field"><label>รหัสผ่านใหม่</label><input class="input" type="password" name="new" autocomplete="new-password" minlength="10" required></div>
        <div class="field"><label>ยืนยันรหัสผ่านใหม่</label><input class="input" type="password" name="new2" autocomplete="new-password" minlength="10" required></div></div>`,
      onConfirm: async f => { if (f.new !== f.new2) throw new Error('รหัสผ่านสองช่องไม่ตรงกัน'); await api('/v1/me/password', 'POST', { current: f.current, new: f.new }); toast('เปลี่ยนรหัสผ่านแล้ว'); } });
    if (b.dataset.me === 'recovery') return modal({ title: 'สร้างรหัสสำรองชุดใหม่?', confirm: 'สร้างชุดใหม่', desc: 'รหัสสำรองชุดเดิมทั้งหมดจะใช้ไม่ได้ทันที ใส่รหัส 6 หลักจาก Google Authenticator เพื่อยืนยัน',
      body: '<input class="input otp-in sm" name="code" inputmode="numeric" autocomplete="one-time-code" pattern="[0-9]{6}" maxlength="6" required aria-label="รหัส 6 หลัก" placeholder="000000">',
      onConfirm: async f => { const r = await api('/v1/me/recovery-codes', 'POST', { code: f.code });
        setTimeout(() => { modal({ title: 'รหัสสำรองชุดใหม่', confirm: 'เก็บแล้ว', wide: true, desc: 'แสดงครั้งเดียว แต่ละตัวใช้ได้ครั้งเดียว', body: recoveryBlock(r.recovery_codes || []), onConfirm: async () => {} }); wireRecovery(r.recovery_codes || []); }, 0); } });
  }
  if (b.hasAttribute('data-u-add')) return modal({ title: 'เพิ่มผู้ดูแล', confirm: 'สร้างบัญชีและลิงก์ตั้งค่า',
    body: `<div class="row-fields"><div class="field"><label>ชื่อผู้ใช้ (ใช้ตอนเข้าระบบ)</label><input class="input" name="username" required pattern="[a-z0-9._-]{3,32}" autocapitalize="off" spellcheck="false" placeholder="เช่น somchai.k"><span class="hint">a-z 0-9 . _ - ยาว 3 ถึง 32 ตัว</span></div>
      <div class="field"><label>ชื่อที่แสดง</label><input class="input" name="display_name" required placeholder="เช่น สมชาย ก."></div></div>`,
    onConfirm: async f => { const r = await api('/v1/users', 'POST', { username: f.username.trim().toLowerCase(), display_name: f.display_name.trim() }); renderPage(); setTimeout(() => showSetupLink('ส่งลิงก์ตั้งค่าให้ผู้ดูแลใหม่', r.setup_link, f.display_name), 0); } });
  if (b.dataset.uReset) { const u = adminCache.find(x => x.id === b.dataset.uReset);
    return modal({ title: 'รีเซ็ตการยืนยันตัวตน?', danger: true, confirm: 'รีเซ็ต', desc: `<b translate="no">${esc(u?.display_name || '')}</b> จะเข้าระบบไม่ได้จนกว่าจะเปิดลิงก์ใหม่ ตั้งรหัสผ่านและสแกน QR ใหม่ ใช้เมื่อเจ้าตัวลืมรหัสหรือโทรศัพท์หาย`,
      onConfirm: async () => { const r = await api('/v1/users/' + encodeURIComponent(u.id) + '/reset-2fa', 'POST', {}); renderPage(); setTimeout(() => showSetupLink('ส่งลิงก์ตั้งค่าใหม่', r.setup_link, u.display_name || u.username), 0); } }); }
  if (b.dataset.uToggle) { const u = adminCache.find(x => x.id === b.dataset.uToggle), off = !u.disabled_at;
    return modal({ title: off ? 'ปิดใช้งานบัญชีนี้?' : 'เปิดใช้งานบัญชีนี้?', danger: off, confirm: off ? 'ปิดใช้งาน' : 'เปิดใช้งาน',
      desc: off ? `<b translate="no">${esc(u.display_name || u.username)}</b> จะถูกออกจากระบบทันทีและเข้าระบบไม่ได้ ประวัติการใช้งานยังอยู่ครบ` : `<b translate="no">${esc(u.display_name || u.username)}</b> จะเข้าระบบได้อีกครั้ง`,
      onConfirm: async () => { await api('/v1/users/' + encodeURIComponent(u.id) + (off ? '/disable' : '/enable'), 'POST', {}); toast(off ? 'ปิดใช้งานแล้ว' : 'เปิดใช้งานแล้ว'); renderPage(); } }); }
  if (b.dataset.hm) {
    const h = hostList.find(x => x.id === b.dataset.id); closeHostMenu(); if (!h) return;
    const name = h.hostname || h.id;
    if (b.dataset.hm === 'command') return openBulkCommand({ host_ids: [h.id] }, name);
    if (b.dataset.hm === 'wake') return askWake([h.id], name);
    if (b.dataset.hm === 'remote') { hostCache = h; return askRemote(); }
    if (b.dataset.hm === 'site') { hostSel.clear(); hostSel.add(h.id); const t = document.createElement('button'); t.dataset.bulk = 'site'; document.body.append(t); t.click(); t.remove(); return; }
  }
  if (b.dataset.bulk) {
    const ids = [...hostSel];
    if (b.dataset.bulk === 'clear') { hostSel.clear(); return renderPage(); }
    if (b.dataset.bulk === 'command') return openBulkCommand({ host_ids: ids }, ids.length + ' เครื่องที่เลือก');
    if (b.dataset.bulk === 'wake') { const w = ids.filter(id => { const h = hostList.find(x => x.id === id); return h && !h.online && macOf(h); }); return askWake(w, w.length + ' เครื่องที่ออฟไลน์ในที่เลือก'); }
    if (b.dataset.bulk === 'deploy') return openBulkDeploy(ids);
    if (b.dataset.bulk === 'prestage') return askPrestage(ids, ids.length + ' เครื่องที่เลือก');
    if (b.dataset.bulk === 'lock' || b.dataset.bulk === 'unlock') return askLock(ids, ids.length + ' เครื่องที่เลือก', b.dataset.bulk === 'lock');
    if (b.dataset.bulk === 'clear-history') return askClearHistory(ids, ids.length + ' เครื่องที่เลือก');
    const isSite = b.dataset.bulk === 'site';
    const opts = isSite ? siteCache.map(x => [x.id, x.name]) : (await api('/v1/channels')).map(x => [x.id, `${x.name} · ${x.edition}`]);
    return modal({ title: isSite ? `ย้าย ${ids.length} เครื่องไป site` : `ตั้งหัวข้อ image ให้ ${ids.length} เครื่อง`, confirm: 'บันทึก',
      body: `<div class="field"><label>${isSite ? 'Site' : 'Channel'}</label><select class="select" name="v"><option value="">— ${isSite ? 'ไม่ระบุ site' : 'ไม่กำหนด'} —</option>
        ${opts.map(([v, l]) => `<option value="${esc(v)}">${esc(l)}</option>`).join('')}</select></div>`,
      onConfirm: async f => { const field = isSite ? 'site' : 'channel';
        const r = await api('/v1/hosts/site', 'POST', { host_ids: ids, field, [field + '_id']: f.v || null }); toast(`อัปเดตแล้ว ${r.updated} เครื่อง`); hostSel.clear(); renderPage(); } });
  }
  if (b.dataset.batch) return openBatch(b.dataset.batch);
  if (b.dataset.hostDelete) {
    return modal({ title: 'ลบเครื่องนี้ถาวร?', danger: true, confirm: 'ลบถาวร',
      desc: `ลบ <b>${esc(b.dataset.label)}</b> ออกจากรายการ — ถ้า Agent ยังติดตั้งอยู่และกลับมาออนไลน์ จะถูกสั่งให้ถอนตัวเองอัตโนมัติ · ลบ <b>${esc(b.dataset.label)}</b> และประวัติคำสั่งทั้งหมดออกจากฐานข้อมูล — ย้อนกลับไม่ได้ (Audit log ยังเก็บบันทึกว่าเคยมีเครื่องนี้และใครลบ)`,
      onConfirm: async () => { await api('/v1/hosts/' + encodeURIComponent(b.dataset.hostDelete), 'DELETE'); toast('ลบเครื่องแล้ว');
        if (routeArg()) location.hash = '#/hosts'; else renderPage(); } });
  }
  if (b.dataset.cmdOut) return openCommandOutput(b.dataset.cmdOut).catch(e => toast(e.message, 'bad'));
  if (b.hasAttribute('data-remote')) return askRemote();
  if (b.dataset.preset) return askPreset(b.dataset.preset);
  if (b.dataset.ccview) { ccView = b.dataset.ccview; store.set('swim-cc-view', ccView); const t = $('#topo'); if (t && topoCtx) { t.outerHTML = controlCenter(topoCtx); topoEnter = false; } return; }
  if (b.dataset.tview) { topoView = b.dataset.tview; store.set('swim-topo-view', topoView); return redrawTopo(); }
  if (b.dataset.tlCat) { toolState.cat = b.dataset.tlCat; toolState.q = ''; store.set('swim-tool-cat', toolState.cat); const q = $('#tl-q'); if (q) q.value = ''; return redrawTools(); }
  if (b.hasAttribute('data-store-clean')) return askStorageCleanup();
  if (b.dataset.isoImport) return askIsoImport(b);
  if (b.dataset.isoInspect) {
    b.disabled = true;
    try { await api('/v1/builder/isos/' + encodeURIComponent(b.dataset.isoInspect) + '/inspect', 'POST', {}); toast('กำลังอ่านรุ่น Windows ในแผ่น…'); await renderPage(); schedulePoll(); }
    catch (e) { toast(e.message, 'bad'); b.disabled = false; }
    return;
  }
  if (b.dataset.cmdCancel) {
    try { await api('/v1/commands/' + encodeURIComponent(b.dataset.cmdCancel) + '/cancel', 'POST', {}); toast('ยกเลิกคำสั่งแล้ว'); renderPage(); } catch (e) { toast(e.message, 'bad'); }
    return;
  }
  if (b.dataset.cmd) {
    const t = b.dataset.cmd, h = hostCache, name = esc(h.hostname || h.id);
    if (['ping', 'collect_logs', 'refresh_inventory'].includes(t)) {
      b.disabled = true; try { await sendCommand(t); } catch (e) { toast(e.message, 'bad'); b.disabled = false; } return;
    }
    if (t === 'update_agent') {
      if (h.agent_version && verCmp(h.agent_version, '0.4.0') < 0) {
        const sc = scriptCache.find(x => x.name.startsWith('อัปเดต SWIM Agent'));
        if (!sc) return toast('agent ' + h.agent_version + ' ต้องใช้สคริปต์ "อัปเดต SWIM Agent" ที่อนุมัติแล้ว', 'bad');
        return modal({ title: 'อัปเดต Agent?', confirm: 'อัปเดต', desc: `agent ${esc(h.agent_version)} ยังไม่มีคำสั่งอัปเดตในตัว จึงใช้สคริปต์ที่อนุมัติแล้ว "${esc(sc.name)}" — ดาวน์โหลด ${esc(latestAgent)}, ตรวจ SHA-256 และย้อนกลับเองถ้าสตาร์ตไม่ได้`,
          onConfirm: () => sendCommand('run_script', { script_id: sc.id }) });
      }
      return modal({ title: 'อัปเดต Agent?', confirm: 'อัปเดต', desc: `อัปเดต <b>${name}</b> จาก ${esc(h.agent_version)} เป็น ${esc(latestAgent)} — ตรวจ SHA-256 ก่อนสลับ และย้อนกลับเองถ้าเวอร์ชันใหม่สตาร์ตไม่ได้`,
        onConfirm: () => sendCommand('update_agent') });
    }
    if (t === 'restart_agent') return modal({ title: 'รีสตาร์ต Agent?', confirm: 'รีสตาร์ต', desc: `Agent บน <b>${name}</b> จะหยุดแล้วเริ่มใหม่ภายในประมาณ 10 วินาที ผู้ใช้ไม่เห็นอะไร`, onConfirm: () => sendCommand(t) });
    if (t === 'reboot') return modal({ title: 'รีบูตเครื่อง', confirm: 'ส่งคำสั่งรีบูต', desc: `ผู้ใช้ที่ล็อกอินบน <b>${name}</b> จะเห็นข้อความเตือนและนับถอยหลัง`,
      body: `<div class="row-fields"><div class="field"><label>รีบูตในอีก</label><select class="select" name="delay_s"><option value="60">1 นาที</option><option value="300" selected>5 นาที</option><option value="900">15 นาที</option><option value="3600">1 ชั่วโมง</option></select></div></div>
        <div class="field"><label>ข้อความถึงผู้ใช้</label><input class="input" name="message" maxlength="200" value="IT จะรีสตาร์ตเครื่องนี้ กรุณาบันทึกงาน"></div>`,
      onConfirm: f => sendCommand(t, { delay_s: Number(f.delay_s), message: f.message }) });
    if (t === 'run_script') {
      const scripts = scriptCache.filter(x => x.status !== 'retired');
      modal({ title: 'รันสคริปต์', confirm: 'รันบนเครื่องนี้', wide: true, desc: `รันเป็น SYSTEM บน <b>${name}</b> — ผลลัพธ์จะกลับมาที่ประวัติคำสั่ง`,
        body: scriptPickerHTML(scripts), onConfirm: f => sendCommand(t, scriptArgs(f)) });
      return wireScriptPicker(scripts);
    }
    if (t === 'uninstall_agent') return modal({ title: 'ถอนการติดตั้ง Agent?', danger: true, confirm: 'ถอนการติดตั้ง',
      desc: `Agent จะลบตัวเอง, service, ไฟล์ และ registry ออกจาก <b>${name}</b> แล้ว server ยกเลิก token ของเครื่องทันที — จะกลับเข้าระบบได้ด้วยการติดตั้ง MSI ใหม่เท่านั้น`,
      body: `
        <div class="field"><label>พิมพ์ชื่อเครื่อง <b class="mono">${name}</b> เพื่อยืนยัน</label><input class="input mono" name="confirm" required autocomplete="off"></div>`,
      onConfirm: async f => { if (f.confirm.trim().toUpperCase() !== (h.hostname || h.id).toUpperCase()) throw new Error('ชื่อเครื่องไม่ตรง'); await sendCommand(t, {}); } });
  }
  if (b.hasAttribute('data-retire')) {
    const h = hostCache, name = esc(h.hostname || h.id);
    return modal({ title: 'ลบเครื่องออกจากระบบ?', danger: true, confirm: 'ลบออก',
      desc: h.online
        ? `<b>${name}</b> ออนไลน์อยู่ — ระบบจะ<b>ถอด Agent ออกจากเครื่องให้อัตโนมัติ</b>ก่อน แล้วลบเครื่องออกเมื่อ Agent ยืนยัน (ประวัติยังเก็บไว้)`
        : `<b>${name}</b> ออฟไลน์อยู่ — ลบออกจากระบบทันที ถ้าภายหลัง Agent บนเครื่องนี้กลับมาออนไลน์ ระบบจะสั่งให้ถอนตัวเองออกอัตโนมัติ (เครื่องจะไม่กลับมาในรายการ)`,
      onConfirm: async () => {
        const r = await api('/v1/hosts/' + encodeURIComponent(h.id) + '/retire', 'POST', {});
        toast(r.status === 'uninstalling' ? `กำลังถอด Agent ออกจาก ${h.hostname}…` : `ลบ ${h.hostname} ออกจากระบบแล้ว`); renderPage();
      } });
  }
  if (b.dataset.auditDl) {
    const r = await fetch('/v1/audit/archives/' + encodeURIComponent(b.dataset.auditDl), { headers: authHeaders() });
    if (!r.ok) return toast('ดาวน์โหลดไม่สำเร็จ (HTTP ' + r.status + ')', 'bad');
    const el = document.createElement('a'); el.href = URL.createObjectURL(await r.blob()); el.download = b.dataset.auditDl;
    document.body.appendChild(el); el.click(); el.remove(); setTimeout(() => URL.revokeObjectURL(el.href), 2000); return;
  }
  if (b.hasAttribute('data-audit-archive')) {
    return modal({ title: 'เก็บถาวรและเริ่มหน้าใหม่?', confirm: 'เก็บถาวรและเริ่มหน้าใหม่',
      desc: `ย้ายประวัติ <b>${auditTotal.toLocaleString(LOC())} รายการ</b> ไปเก็บเป็นไฟล์บน server แล้วเริ่มหน้าใหม่ ข้อมูลเดิมยังตรวจสอบย้อนหลังได้ครบ และดาวน์โหลดได้จากหน้านี้
        หน้าใหม่จะเริ่มด้วยรายการว่าใครเก็บถาวรและเมื่อไหร่`,
      onConfirm: async () => { const r = await api('/v1/audit/archive', 'POST', {}); toast(`เก็บถาวร ${Number(r.rows).toLocaleString(LOC())} รายการแล้ว`); renderPage(); } });
  }
  if (b.hasAttribute('data-enroll-close')) { enrollResult = null; return renderPage(); }
  if (b.hasAttribute('data-copy-trust')) {
    try { await navigator.clipboard.writeText($('#trust-cmd').textContent); toast('คัดลอกคำสั่งแล้ว'); }
    catch (e) { const r = document.createRange(); r.selectNodeContents($('#trust-cmd')); getSelection().removeAllRanges(); getSelection().addRange(r); toast('เลือกข้อความแล้ว กด Ctrl+C'); }
    return;
  }
  if (b.hasAttribute('data-copy-cmd')) {
    const txt = $('#ps-cmd').textContent;
    try { await navigator.clipboard.writeText(txt); toast('คัดลอกคำสั่งแล้ว'); }
    catch (e) { const r = document.createRange(); r.selectNodeContents($('#ps-cmd')); getSelection().removeAllRanges(); getSelection().addRange(r); toast('เลือกข้อความแล้ว กด Ctrl+C เพื่อคัดลอก'); }
    return;
  }
  if (b.dataset.revoke) {
    return modal({ title: 'ยกเลิก token นี้?', danger: true, confirm: 'ยกเลิก token', desc: 'คำสั่งติดตั้งที่ใช้ token นี้จะใช้ไม่ได้อีก เครื่องที่ลงทะเบียนไปแล้วยังทำงานต่อตามปกติ',
      onConfirm: async () => { await api('/v1/enrollment-tokens/' + encodeURIComponent(b.dataset.revoke) + '/revoke', 'POST', {}); toast('ยกเลิก token แล้ว'); renderPage(); } });
  }
  if (b.hasAttribute('data-new-site')) {
    return modal({ title: 'สร้าง Site', desc: 'เช่น สาขา อาคาร หรือแผนก', confirm: 'สร้าง',
      body: `<div class="field"><label>ชื่อ site</label><input class="input" name="name" required placeholder="BKK-HQ"></div>`,
      onConfirm: async f => { const r = await api('/v1/sites', 'POST', { name: f.name }); toast('สร้าง site แล้ว'); await renderPage(); const sel = $('#en-site'); if (sel) sel.value = r.id; } });
  }
  if (b.hasAttribute('data-new-channel')) {
    return modal({ title: 'หัวข้อ image ใหม่', desc: 'หนึ่งหัวข้อ = image หนึ่งชุดที่ทำเวอร์ชันใหม่ต่อไปเรื่อย ๆ เช่น Windows 11 Pro สำนักงาน หรือ Windows 11 ห้องแล็บ', confirm: 'สร้าง',
      body: `<div class="field"><label>ชื่อ</label><input class="input" name="name" required placeholder="win11-x64-pro" pattern="[A-Za-z0-9._-]+"><span class="hint">ใช้เป็นชื่อโฟลเดอร์ของไฟล์ WIM — a-z 0-9 . _ -</span></div>
        <div class="row-fields"><div class="field"><label>Release</label><input class="input" name="release" required value="11"></div>
        <div class="field"><label>Edition</label><select class="select" name="edition"><option>Pro</option><option>Education</option><option>Enterprise</option><option>Home</option></select></div></div>
        <div class="row-fields"><div class="field"><label>ดิสก์ขั้นต่ำ (GB)</label><input class="input" name="min_disk_gb" type="number" min="32" value="128"></div>
        <div class="field"><label>วันหมด support</label><input class="input" name="eol_date" type="date"></div></div>`,
      onConfirm: async f => {
        await api('/v1/channels', 'POST', { name: f.name, os: 'windows', release: f.release, arch: 'amd64', edition: f.edition,
          min_disk_gb: Number(f.min_disk_gb) || 128, eol_date: f.eol_date || null });
        toast('สร้างหัวข้อแล้ว'); renderPage();
      } });
  }


  const cardEl = b.closest('[data-vm]');
  if (!cardEl || !b.dataset.act) return;
  const vm = cardEl.dataset.vm, act = b.dataset.act, base = '/v1/builder/vms/' + encodeURIComponent(vm) + '/';
  const v = vmCache.find(x => x.name === vm) || { snapshots: [] };

  if (act === 'copy-sysprep') {
    const cmd = 'C:\\Windows\\System32\\Sysprep\\sysprep.exe /generalize /oobe /shutdown';
    try { await navigator.clipboard.writeText(cmd); toast('คัดลอกคำสั่งแล้ว — วางใน Console ด้วย Ctrl+Alt+Shift (แผง clipboard)'); }
    catch (e) { toast(cmd); }
    return;
  }
  if (act === 'console') {
    const win = window.open('about:blank', '_blank');
    try { const { url } = await api(base + 'console', 'POST', {}); await guacSignOut(); if (win) win.location = url; else location.href = url; }
    catch (e) { win && win.close(); toast(e.message, 'bad'); }
    return;
  }
  if (act.startsWith('power/')) {
    const a = act.split('/')[1], now = vmStateLabel(v)[1];
    const run = async () => {
      await api(base + act, 'POST', {});
      if (a === 'shutdown') shutdownMark.set(vm); else shutdownMark.clear(vm);
      toast({ start: `กำลังเปิด ${vm}`, shutdown: `ส่งคำสั่งปิดให้ ${vm} แล้ว — รอ Windows ปิดตัว`, reset: `Reset ${vm} แล้ว`, poweroff: `ตัดไฟ ${vm} แล้ว` }[a]);
      imagesQuick = false; setTimeout(renderPage, 1500);
    };
    const who = `<b class="mono">${esc(vm)}</b> (ตอนนี้: <b>${esc(now)}</b>)`;
    const D = {
      start: ['เปิดเครื่องต้นแบบ?', 'เปิดเครื่อง', false, `${who} จะเริ่มบูต Windows — เปิดหน้าจอ Console ได้หลังผ่านไปราว 30 วินาที`],
      shutdown: ['ปิดเครื่องต้นแบบ?', 'ปิดเครื่อง', false, `${who} จะได้รับคำสั่งให้ Windows ปิดตัวเองตามปกติ (เหมือนกดปุ่ม power หนึ่งครั้ง) — บันทึกงานในเครื่องก่อน`],
      reset: ['Reset เครื่องต้นแบบ?', 'Reset', true, `${who} จะรีสตาร์ตทันทีโดยไม่ปิด Windows ให้เรียบร้อย งานที่ยังไม่บันทึกจะหาย`],
      poweroff: ['ตัดไฟเครื่องต้นแบบ?', 'ตัดไฟ', true, `${who} จะดับทันทีเหมือนถอดปลั๊ก งานที่ยังไม่บันทึกใน Windows จะหาย — ใช้เมื่อ Windows ไม่ยอมปิดเองเท่านั้น`],
    }[a];
    return modal({ title: D[0], confirm: D[1], danger: D[2], desc: D[3], onConfirm: run });
  }
  if (act === 'snapshot') {
    const d = new Date(), stamp = `${d.getFullYear()}${String(d.getMonth() + 1).padStart(2, '0')}${String(d.getDate()).padStart(2, '0')}`;
    return modal({ title: 'สร้าง Snapshot', desc: `เก็บสภาพปัจจุบันของ <b class="mono">${esc(vm)}</b> ทั้งดิสก์, UEFI NVRAM และ TPM`, confirm: 'สร้าง Snapshot',
      body: `<div class="field"><label>ชื่อ snapshot</label><input class="input mono" name="snapshot" required pattern="[A-Za-z0-9][A-Za-z0-9_.-]{0,62}" value="pre-sysprep-${stamp}">
        <span class="hint">แนะนำให้ตั้งชื่อตามขั้นตอน เช่น <code class="k">clean-base</code>, <code class="k">pre-sysprep-v1</code></span></div>`,
      onConfirm: async f => { await api(base + 'snapshot', 'POST', { snapshot: f.snapshot }); toast('เข้าคิวสร้าง snapshot แล้ว'); renderPage(); } });
  }
  if (act === 'revert') {
    return modal({ title: 'ย้อนกลับ Snapshot?', danger: true, confirm: 'ย้อนกลับ',
      desc: `<b class="mono">${esc(vm)}</b> จะกลับไปเป็นสภาพของ <b class="mono">${esc(b.dataset.snap)}</b> — ทุกอย่างที่ทำหลังจากนั้นจะหาย`,
      onConfirm: async () => { await api(base + 'revert', 'POST', { snapshot: b.dataset.snap }); toast('เข้าคิวย้อนกลับแล้ว'); renderPage(); } });
  }
  if (act === 'delete-snapshot') {
    return modal({ title: 'ลบ Snapshot?', danger: true, confirm: 'ลบ', desc: `ลบ <b class="mono">${esc(b.dataset.snap)}</b> ถาวร จะย้อนกลับไปจุดนี้ไม่ได้อีก`,
      onConfirm: async () => { await api(base + 'delete-snapshot', 'POST', { snapshot: b.dataset.snap }); toast('เข้าคิวลบ snapshot แล้ว'); renderPage(); } });
  }
  if (act === 'media') {
    const cur = ((v.disks || []).find(d => d.device === 'cdrom')?.source || '').split('/').pop();
    return modal({ title: 'แผ่นติดตั้ง (ISO)', desc: 'ไฟล์ ISO อยู่ใน <code class="k">/srv/swim/vms/iso/</code> บน server', confirm: 'บันทึก',
      body: `<div class="field"><label>เลือกแผ่น</label><select class="select" name="iso"><option value="">— ถอดแผ่นออก —</option>
        ${isoCache.map(i => `<option value="${esc(i.name)}" ${i.name === cur ? 'selected' : ''}>${esc(i.name)} (${fmtBytes(i.size_bytes)})</option>`).join('')}</select>
        <span class="hint">หลังติดตั้ง Windows เสร็จควรถอดแผ่น เพื่อไม่ให้บูตเข้า setup ซ้ำ</span></div>
        ${isoCache.length ? '' : `<div class="callout" style="margin:0">${ic('info')}<div>ยังไม่มีไฟล์ ISO บน server — วางไฟล์ .iso ที่ <code class="k">/srv/swim/vms/iso/</code> บน server ผ่าน SSH (เช่น scp) แล้วกดรีเฟรช</div></div>`}`,
      onConfirm: async f => { await api(base + 'media', 'POST', { iso: f.iso }); toast(f.iso ? 'ใส่แผ่นแล้ว' : 'ถอดแผ่นแล้ว'); renderPage(); } });
  }
  if (act === 'capture') {
    if (!channelCache.length) {
      return modal({ title: 'ต้องมีหัวข้อ image ก่อน', desc: 'Capture ต้องเลือกว่าเป็น image หัวข้อไหน — สร้างหัวข้อที่ปุ่ม "หัวข้อใหม่" ด้านล่างของหน้านี้ก่อน', confirm: 'ตกลง', onConfirm: async () => {} });
    }
    const d = new Date(), ver = `v${d.getFullYear()}.${d.getMonth() + 1}.${d.getDate()}`;
    return modal({ title: 'Capture เป็น image เวอร์ชันใหม่', confirm: 'เริ่ม Capture',
      desc: `ระบบจะทำสำเนาชั่วคราวของ <b class="mono">${esc(vm)}</b> แล้วรัน sysprep, คลายไฟล์ที่ Windows บีบอัด, ตรวจ 6 ข้อ และจับภาพให้อัตโนมัติ —
        เครื่องต้นแบบไม่ถูกแก้ไข ใช้งานต่อได้ทันที ใช้เวลาราว 30–60 นาที`,
      body: `<div class="row-fields"><div class="field"><label>หัวข้อ image</label><select class="select" name="channel_id">${channelCache.map(c => `<option value="${esc(c.id)}">${esc(c.name)}</option>`).join('')}</select></div>
        <div class="field"><label>เวอร์ชัน</label><input class="input mono" name="version" required pattern="[A-Za-z0-9][A-Za-z0-9_.-]{0,62}" value="${ver}"></div></div>`,
      onConfirm: async f => { await api(base + 'capture', 'POST', { ...f, revert_to: '' }); toast('เริ่ม Capture แล้ว — ความคืบหน้าแสดงที่ขั้นที่ 3'); imagesQuick = false; renderPage(); } });
  }
});

window.addEventListener('hashchange', () => { closeHostMenu(); const st = /^#\/setup\?t=([\w-]+)/.exec(location.hash); if (st) { stopPolling(); stopLive(); me = null; return renderSetup({ setupToken: st[1] }); } if (me) { if (route() === 'home') { liveEnter = true; topoEnter = true; } closeDrawer(); renderShell(); schedulePoll(); } });

// ───────── translation layer ─────────
// Thai mode swaps transliterated loanwords for their international English term; English mode swaps every
// interface phrase from i18n-en.json (longest phrase first). Script output, code and form inputs are never touched.
const TH_LOAN = [['รีสตาร์ต', 'Restart'], ['รีบูต', 'Reboot'], ['รีโมต', 'Remote'], ['พาร์ทิชัน', 'Partition'], ['ไดรเวอร์', 'Driver'], ['สคริปต์', 'Script'],
  ['ดิสก์', 'Disk'], ['ไดรฟ์', 'Drive'], ['โฟลเดอร์', 'Folder'], ['ไฟล์', 'File'], ['ดาวน์โหลด', 'Download'], ['อัปเดต', 'Update'], ['ฟอร์แมต', 'Format'],
  ['โปรเซส', 'Process'], ['โปรแกรม', 'Program'], ['ไฟร์วอลล์', 'Firewall'], ['โดเมน', 'Domain'], ['เวอร์ชัน', 'Version'], ['ฮาร์ดแวร์', 'Hardware'],
  ['แบตเตอรี่', 'Battery'], ['สแนปช็อต', 'Snapshot'], ['ลิงก์', 'Link'], ['แท็บ', 'Tab'], ['เบราว์เซอร์', 'Browser'], ['ซิงก์', 'Sync'], ['บูต', 'Boot'],
  ['คอนโซล', 'Console'], ['ล็อกอิน', 'Sign-in'], ['เซสชัน', 'Session'], ['แชต', 'Chat'], ['พาสเวิร์ด', 'Password']];
const EN_RULES = [
  [/Machines online\s*(\d[\d,]*)\s*จาก\s*(\d[\d,]*)\s*เครื่อง/g, '$1 of $2 machines online'],
  [/มี\s*(\d[\d,]*)\s*เรื่องที่ต้องดู/g, '$1 issues need attention,'], [/เก็บย้อนหลัง\s*(\d+)\s*เดือน/g, 'keeps the last $1 months,'],
  [/ผิดครั้งที่\s*(\d+)\s*จาก IP นี้ใน 15 นาที/g, 'failure $1 from this IP in 15 min'], [/ลบ\s*([\d,]+)\s*รายการที่เก่ากว่า\s*(\d+)\s*วัน/g, 'deleted $1 entries older than $2 days'],
  [/ออนไลน์\s*(\d[\d,]*)\s*จาก\s*(\d[\d,]*)\s*เครื่อง/g, 'online $1 of $2 machines'], [/ออนไลน์\s*(\d[\d,]*)\s*จาก\s*(\d[\d,]*)/g, 'online $1 of $2'],
  [/จาก\s*(\d[\d,]*)\s*เครื่อง/g, 'of $1 machines'], [/(\d[\d,]*)\s*เครื่อง/g, '$1 machines'], [/(\d[\d,]*)\s*รายการ/g, '$1 entries'], [/(\d[\d,]*)\s*คำสั่ง/g, '$1 commands'], [/(\d[\d,]*)\s*งาน/g, '$1 jobs'],
  [/(\d[\d,]*)\s*นาที/g, '$1 min'], [/(\d[\d,]*)\s*ชั่วโมง/g, '$1 h'], [/(\d[\d,]*)\s*ชม\./g, '$1 h'], [/(\d[\d,]*)\s*วัน/g, '$1 days'], [/(\d[\d,]*)\s*วินาที/g, '$1 s'],
  [/(\d[\d,]*)\s*จาก\s*(\d[\d,]*)/g, '$1 of $2'], [/(\d[\d,]*)\s*ข้อ/g, '$1 checks'], [/(\d[\d,]*)\s*ครั้ง/g, '$1 times'], [/(\d[\d,]*)\s*คน/g, '$1 people']];
const TH_RE = /[฀-๿]/, SKIP_TAGS = new Set(['SCRIPT', 'STYLE', 'PRE', 'CODE', 'TEXTAREA', 'INPUT', 'KBD']);
const I18N_ATTRS = ['placeholder', 'title', 'aria-label', 'data-tip', 'data-tip-sub'];
let EN = null;
// Thai has no spaces, so text is split into words with Intl.Segmenter and only whole words or runs of words are matched
const SEG = (() => { try { return new Intl.Segmenter('th', { granularity: 'word' }); } catch (e) { return null; } })();
const LOAN = new Map(TH_LOAN);
function bySegments(t, look) {
  const segs = SEG ? [...SEG.segment(t)].map(x => x.segment) : [t], out = [];
  for (let i = 0; i < segs.length;) {
    let hit = 0;
    if (TH_RE.test(segs[i])) for (let j = Math.min(segs.length, i + 40); j > i; j--) {
      const cand = segs.slice(i, j).join(''), key = cand.trim(), v = key && look(key);
      if (v != null) { out.push(cand.startsWith(' ') ? ' ' : '', v, cand.endsWith(' ') ? ' ' : ''); hit = j - i; break; }
    }
    if (hit) i += hit; else out.push(segs[i++]);
  }
  // keep a space between two English pieces that ended up side by side
  return out.filter(x => x !== '').reduce((a, x) => a + (/[A-Za-z0-9…)%]$/.test(a) && /^[A-Za-z0-9(]/.test(x) ? ' ' : '') + x, '');
}
function trText(t) {
  if (!t || !TH_RE.test(t)) return t;
  if (LANG === 'th') {
    if (!TH_LOAN.some(([w]) => t.includes(w))) return t;
    // whole words only (a run of segments), with a space between the English term and the Thai around it
    const o = bySegments(t, key => LOAN.has(key) ? `\u0001${LOAN.get(key)}\u0001` : null)
      .replace(/\u0001+/g, m => m).replace(/(\S)\u0001/g, '$1 ').replace(/\u0001(\S)/g, ' $1').replace(/\u0001/g, '').replace(/ {2,}/g, ' ').replace(/ ([,:;)\]]|\.(?=\s|$))/g, '$1').replace(/([(\[]) /g, '$1');
    return t.startsWith(' ') || !o.startsWith(' ') ? o : o.trimStart();
  }
  if (!EN) return t;
  const k = t.trim(); if (EN[k] != null) return t.replace(k, EN[k]);
  // long phrases first (they may contain numbers), then number+unit rules, then the remaining words
  let o = bySegments(t, key => key.length >= 10 ? EN[key] ?? null : null);
  for (const [re, to] of EN_RULES) o = o.replace(re, to);
  if (TH_RE.test(o)) o = bySegments(o, key => EN[key] ?? null);
  o = o.replace(/ {2,}/g, ' ').replace(/ ([,:;)\]%]|\.(?=\s|$))/g, '$1').replace(/([(\[]) /g, '$1');
  return t.startsWith(' ') || !o.startsWith(' ') ? o : o.trimStart();
}
// set an attribute only when the text really changes, or the observer would fire forever
function trAttr(el, a) { const v = el.getAttribute(a); if (!v || !TH_RE.test(v)) return; const t = trText(v); if (t !== v) el.setAttribute(a, t); }
function trNode(root) {
  if (!root) return;
  if (root.nodeType === 3) { const p = root.parentElement; if (p && !p.closest('pre,code,textarea,[translate=no]')) { const v = trText(root.nodeValue); if (v !== root.nodeValue) root.nodeValue = v; } return; }
  if (root.nodeType !== 1 || SKIP_TAGS.has(root.tagName) || root.closest?.('[translate=no]')) {
    if (root.nodeType === 1 && root.tagName === 'INPUT') for (const a of ['placeholder', 'title', 'aria-label']) trAttr(root, a);
    return;
  }
  for (const a of I18N_ATTRS) trAttr(root, a);
  const w = document.createTreeWalker(root, NodeFilter.SHOW_TEXT | NodeFilter.SHOW_ELEMENT, { acceptNode: n => n.nodeType === 1 && SKIP_TAGS.has(n.tagName) && n.tagName !== 'INPUT' ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT });
  let n; while ((n = w.nextNode())) {
    if (n.nodeType === 3) { if (TH_RE.test(n.nodeValue) && !n.parentElement.closest('pre,code,textarea,[translate=no]')) { const v = trText(n.nodeValue); if (v !== n.nodeValue) n.nodeValue = v; } }
    else for (const a of I18N_ATTRS) trAttr(n, a);
  }
}
async function i18nInit() {
  document.documentElement.lang = LANG;
  if (LANG === 'en') {
    try { EN = await fetch('i18n-en.json?v=' + (document.querySelector('script[src*="app.js"]')?.src.split('v=')[1] || '1')).then(r => r.json()); }
    catch (e) { EN = {}; }
  }
  new MutationObserver(recs => { for (const r of recs) { if (r.type === 'characterData') trNode(r.target); else if (r.type === 'attributes') trNode(r.target); else r.addedNodes.forEach(trNode); } })
    .observe(document.body, { childList: true, subtree: true, characterData: true, attributes: true, attributeFilter: I18N_ATTRS });
  trNode(document.body);
}
function setLang(l) { try { localStorage.setItem('swim-lang', l); } catch (e) {} location.reload(); }

// ───────── boot ─────────
async function boot() {
  if (!i18nReady) { i18nReady = true; await i18nInit(); }
  if (!authCfg) await loadAuthCfg();
  const setupT = /^#\/setup\?t=([\w-]+)/.exec(location.hash);
  if (setupT && !me) return renderSetup({ setupToken: setupT[1] });
  try { me = me || await api('/v1/whoami'); } catch (e) { return renderLogin(e.status === 401 ? '' : e.message); }
  if (me?.auth === 'session' && me.recovery_left != null && me.recovery_left <= 2) toast(`เหลือรหัสสำรอง ${me.recovery_left} ตัว สร้างชุดใหม่ได้ที่เมนูบัญชี`, 'bad');
  await refreshMeta();
  renderShell();
  schedulePoll();
  startLive();
}
let i18nReady = false;
boot();
})();
