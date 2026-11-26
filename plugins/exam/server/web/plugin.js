/* SWIM exam plugin — the console half. The server serves this file only while the plugin is
   enabled; the console loads it from GET /v1/plugins and it registers itself with SWIM.plugin().
   It talks only to /v1/exam. */
(() => {
  'use strict';
  // helpers of the console, handed over by app.js through init() (app.js keeps them private)
  let api, modal, toast, esc, ic, card, stateBox, empty, when, ago, LOC, authHeaders, verCmp, inv, $, route, routeArg, renderPage, getHost, getSel;
  function init(c) { ({ api, modal, toast, esc, ic, card, stateBox, empty, when, ago, LOC, authHeaders, verCmp, inv, $, route, routeArg, renderPage, getHost, getSel } = c); }
  let cfg = null, cfgAt = 0, profiles = [], hostState = new Map(), pageData = null, filter = { host: '', kind: 'violations' }, timer = null;
  const shots = new Map(); // event id → object URL
  const KIND = { app_blocked: ['bad', 'เปิดโปรแกรมที่ห้าม'], app_background: ['', 'ปิดโปรแกรมเบื้องหลัง'], keyword: ['bad', 'พบคำที่เฝ้าดู'], periodic: ['', 'ภาพตามรอบเวลา'],
    watcher_stopped: ['warn', 'ตัวเฝ้าดูถูกปิด'], settings_changed: ['warn', 'การตั้งค่าถูกแก้'] };
  const WSTATE = { front: 'อยู่ด้านหน้า', open: 'เปิดอยู่บนจอ', minimized: 'ย่ออยู่' };
  const GROUP = { ai: 'โปรแกรม AI', chat: 'โปรแกรมแชต', remote: 'โปรแกรมควบคุมเครื่องจากระยะไกล' };

  async function ensure(force) {
    if (cfg && !force && Date.now() - cfgAt < 60000) return cfg;
    cfg = await api('/v1/exam/config'); cfgAt = Date.now();
    profiles = (await api('/v1/exam/profiles').catch(() => ({ items: [] }))).items || [];
    return cfg;
  }
  // a machine nobody touched for a while shows the last thing that was on screen: say so beside the picture
  const idleNote = e => { const n = Number(e.detail?.idle_s); return n >= 120 ? `ไม่มีการใช้คีย์บอร์ดหรือเมาส์มาแล้ว ${Math.round(n / 60)} นาที ภาพอาจเป็นหน้าจอค้าง` : ''; };
  const lines = v => String(v || '').split(/[\n,]+/).map(x => x.trim()).filter(Boolean);
  const clock = t => t ? new Date(t).toLocaleString(LOC(), { dateStyle: 'medium', timeStyle: 'medium' }) : '';

  // ── state of one machine: desired (server) against measured (agent) ──
  function stateOf(row, online) {
    const m = row?.measured || null, on = !!row?.on;
    const seen = m?.checked_at ? `ตรวจจากเครื่องเมื่อ ${ago(m.checked_at)}` : '';
    const tail = x => [x, seen].filter(Boolean).join(', ');
    if (on && m?.error) return { tone: 'bad', label: 'เปิดโหมดสอบไม่สำเร็จ', on, detail: tail(m.error) };
    if (on && m?.desired && m.applied) {
      const c = row.config || {}, watch = (c.app_groups || []).length || (c.block_apps || []).length || c.watch_ai_words || (c.keywords || []).length || c.shot_every_min > 0;
      if (watch && !m.watching) return { tone: 'warn', label: 'เปิดอยู่ ยังไม่ได้เฝ้าดูหน้าจอ', on, detail: tail('ตัวเฝ้าดูเริ่มทำงานเมื่อมีผู้ใช้เข้าใช้งานเครื่อง') };
      return { tone: 'ok', label: 'เปิดอยู่', on, detail: tail(summary(c)) };
    }
    if (on) return { tone: 'info', label: 'กำลังเปิดโหมดสอบ', on, detail: tail(m?.problem || (online ? 'ส่งให้เครื่องแล้ว รอเครื่องยืนยัน' : 'เครื่องออฟไลน์อยู่ จะเปิดเมื่อกลับมาออนไลน์')) };
    if (m?.desired) return { tone: 'info', label: 'กำลังปิดโหมดสอบ', on, detail: tail(online ? 'ส่งให้เครื่องแล้ว รอเครื่องยืนยัน' : 'จะปิดเมื่อเครื่องกลับมาออนไลน์') };
    return { tone: '', label: 'ปิดอยู่', on, detail: 'ไม่มีการจำกัดหรือเฝ้าดูเครื่องนี้' };
  }
  function summary(c) {
    const p = [];
    if (c.web_mode === 'allow') p.push(`เปิดได้เฉพาะ ${(c.sites || []).length} เว็บ`);
    if (c.web_mode === 'block') p.push('บล็อกเว็บตามรายการ');
    const apps = (c.app_groups || []).length + (c.block_apps || []).length; if (apps) p.push('ปิดโปรแกรมที่ห้าม');
    if (c.block_usb) p.push('ปิด USB เก็บข้อมูล');
    if (c.shot_on_violation) p.push('ถ่ายภาพเมื่อทำผิด');
    if (c.shot_every_min > 0) p.push(`ถ่ายภาพทุก ${c.shot_every_min} นาที`);
    return p.join(', ') || 'ไม่มีข้อจำกัด';
  }

  // ── the set-up form ──
  const chk = (name, label, on, hint = '') => `<label class="check"><input type="checkbox" name="${name}" ${on ? 'checked' : ''}><span>${label}${hint ? `<small class="mute ex-hint">${hint}</small>` : ''}</span></label>`;
  function formHtml(c, profileName) {
    const mode = c.web_mode || 'allow', radio = (v, label, hint) => `<label class="check"><input type="radio" name="web_mode" value="${v}" ${mode === v ? 'checked' : ''}><span>${label}<small class="mute ex-hint">${hint}</small></span></label>`;
    return `<div class="ex-form">
      <div class="field"><label for="ex-profile">เริ่มจากชุดการตั้งค่า</label><select class="select" id="ex-profile" name="_profile">
        <option value="">ค่าที่แนะนำสำหรับการสอบผ่านเว็บ</option>${profiles.map(p => `<option value="${esc(p.id)}" ${p.name === profileName ? 'selected' : ''}>${esc(p.name)}</option>`).join('')}</select></div>
      <fieldset class="ex-set"><legend>เว็บ</legend>
        ${radio('allow', 'เปิดได้เฉพาะเว็บที่กำหนด', 'เข้มที่สุด เหมาะกับการสอบผ่านระบบสอบออนไลน์')}
        <div class="field ex-sub" data-ex-show="allow"><label for="ex-sites">เว็บที่อนุญาต บรรทัดละหนึ่งเว็บ</label>
          <textarea class="input mono" id="ex-sites" name="sites" rows="3" placeholder="exam.example.ac.th" spellcheck="false" translate="no">${esc((c.sites || []).join('\n'))}</textarea>
          <small class="mute">โดเมนย่อยของแต่ละเว็บเปิดได้ด้วย</small></div>
        ${radio('block', 'เปิดได้ทุกเว็บ ยกเว้นที่ห้าม', 'เหมาะกับการสอบแบบเปิดตำรา')}
        <div class="ex-sub" data-ex-show="block">${chk('block_ai_sites', `บล็อกเว็บ AI ที่รู้จัก ${cfg.ai_sites.length} เว็บ`, c.block_ai_sites !== false, 'เช่น ChatGPT, Claude, Gemini, Copilot, Perplexity, DeepSeek')}
          <div class="field"><label for="ex-block">เว็บที่ห้ามเพิ่มเติม บรรทัดละหนึ่งเว็บ</label>
          <textarea class="input mono" id="ex-block" name="block_sites" rows="2" spellcheck="false" translate="no">${esc((c.block_sites || []).join('\n'))}</textarea></div></div>
        ${radio('open', 'ไม่จำกัดเว็บ', 'ใช้เมื่อต้องการเฝ้าดูหรือจำกัดโปรแกรมอย่างเดียว')}
      </fieldset>
      <fieldset class="ex-set"><legend>เบราว์เซอร์</legend>
        ${chk('no_extensions', 'ปิดส่วนขยาย', c.no_extensions, 'กันส่วนขยาย AI และ VPN ในเบราว์เซอร์')}
        ${chk('no_private', 'ปิดหน้าต่างแบบส่วนตัว', c.no_private)}
        ${chk('no_devtools', 'ปิดเครื่องมือนักพัฒนา', c.no_devtools)}
      </fieldset>
      <fieldset class="ex-set"><legend>โปรแกรมที่ห้ามเปิด</legend>
        ${Object.keys(cfg.app_groups).map(g => chk('group_' + g, GROUP[g] || g, (c.app_groups || []).includes(g), esc(cfg.app_groups[g].slice(0, 6).join(', ')) + ' และอื่น ๆ')).join('')}
        <div class="field"><label for="ex-apps">โปรแกรมอื่นที่ห้าม ใส่ชื่อไฟล์โปรแกรม บรรทัดละหนึ่งชื่อ</label>
          <textarea class="input mono" id="ex-apps" name="block_apps" rows="2" placeholder="notepad" spellcheck="false" translate="no">${esc((c.block_apps || []).join('\n'))}</textarea>
          <small class="mute">โปรแกรมในรายการจะถูกปิดทันทีที่เปิด และถูกบันทึกเป็นหลักฐาน</small></div>
      </fieldset>
      <fieldset class="ex-set"><legend>เครื่อง</legend>
        ${chk('no_copilot', 'ปิด Windows Copilot และแถบข้างของ Edge', c.no_copilot)}
        ${chk('block_usb', 'ปิด USB เก็บข้อมูล', c.block_usb, 'แฟลชไดรฟ์และฮาร์ดดิสก์พกพา เมาส์และคีย์บอร์ดใช้ได้ตามปกติ')}
      </fieldset>
      <fieldset class="ex-set"><legend>หลักฐาน</legend>
        ${chk('shot_on_violation', 'ถ่ายภาพหน้าจอทุกครั้งที่พบการทำผิด', c.shot_on_violation, 'ภาพเดียวกันไม่เกินนาทีละครั้ง')}
        ${chk('watch_ai_words', 'เฝ้าดูชื่อหน้าต่างที่มีชื่อ AI', c.watch_ai_words, esc(cfg.ai_words.slice(0, 6).join(', ')))}
        <div class="field"><label for="ex-words">คำอื่นที่ต้องการเฝ้าดูในชื่อหน้าต่าง บรรทัดละหนึ่งคำ</label>
          <textarea class="input" id="ex-words" name="keywords" rows="2" spellcheck="false" translate="no">${esc((c.keywords || []).join('\n'))}</textarea></div>
        <div class="field"><label for="ex-every">ถ่ายภาพหน้าจอตามรอบเวลา</label><select class="select" id="ex-every" name="shot_every_min">
          ${[0, 1, 2, 5, 10, 15, 30].map(n => `<option value="${n}" ${Number(c.shot_every_min || 0) === n ? 'selected' : ''}>${n ? `ทุก ${n} นาที` : 'ไม่ถ่าย ถ่ายเฉพาะตอนทำผิด'}</option>`).join('')}</select></div>
      </fieldset>
      <fieldset class="ex-set"><legend>บันทึกไว้ใช้ครั้งหน้า</legend>
        <div class="field"><label for="ex-save">ชื่อชุดการตั้งค่า เว้นว่างถ้าไม่ต้องการบันทึก</label><input class="input" id="ex-save" name="_save" maxlength="80" value="${esc(profileName || '')}" autocomplete="off"></div>
      </fieldset>
      <div class="callout">${ic('info')}<div>ผู้ใช้ที่นั่งหน้าเครื่องจะเห็นข้อความแจ้งว่าเครื่องอยู่ในโหมดสอบและมีการบันทึกภาพหน้าจอ หลักฐานถูกลบอัตโนมัติหลัง ${cfg.keep_days} วัน</div></div>
      <p class="mute ex-note">ควรล็อกการติดตั้งโปรแกรมควบคู่กัน ไม่เช่นนั้นผู้ใช้ที่เป็นผู้ดูแลเครื่องยังติดตั้งเบราว์เซอร์อื่นได้ โหมดสอบไม่ได้ควบคุมโทรศัพท์หรืออุปกรณ์อื่นของผู้สอบ</p></div>`;
  }
  function readForm(fd) {
    const groups = Object.keys(cfg.app_groups).filter(g => fd['group_' + g]);
    return { web_mode: fd.web_mode || 'open', sites: lines(fd.sites), block_sites: lines(fd.block_sites), block_ai_sites: !!fd.block_ai_sites,
      no_extensions: !!fd.no_extensions, no_devtools: !!fd.no_devtools, no_private: !!fd.no_private, app_groups: groups, block_apps: lines(fd.block_apps),
      no_copilot: !!fd.no_copilot, block_usb: !!fd.block_usb, watch_ai_words: !!fd.watch_ai_words, keywords: lines(fd.keywords),
      shot_on_violation: !!fd.shot_on_violation, shot_every_min: Number(fd.shot_every_min || 0) };
  }
  function wireForm() {
    const f = [...document.querySelectorAll('.scrim form')].pop(); if (!f) return;
    const sync = () => { const m = f.querySelector('[name=web_mode]:checked')?.value; f.querySelectorAll('[data-ex-show]').forEach(el => { el.hidden = el.dataset.exShow !== m; }); };
    f.addEventListener('change', ev => {
      if (ev.target.name === 'web_mode') sync();
      if (ev.target.name === '_profile') { // load a saved set-up into the form
        const p = profiles.find(x => x.id === ev.target.value), body = f.querySelector('.modal-body');
        body.innerHTML = formHtml(p ? p.config : cfg.recommended, p ? p.name : ''); sync();
      }
    });
    sync();
  }

  // target: {host_ids} or {site_id}
  async function askOn(target, label, current, profileName) {
    await ensure();
    modal({ title: current ? 'แก้การตั้งค่าโหมดสอบ?' : 'เปิดโหมดสอบ?', confirm: current ? 'บันทึกและส่งให้เครื่อง' : 'เปิดโหมดสอบ', wide: true,
      desc: `ใช้กับ <b>${esc(label)}</b> เครื่องที่ออฟไลน์จะได้รับเมื่อกลับมาออนไลน์`,
      body: formHtml(current || cfg.recommended, profileName || ''),
      onConfirm: async fd => {
        const config = readForm(fd), name = String(fd._save || '').trim();
        if (name) { await api('/v1/exam/profiles', 'POST', { name, config }); cfgAt = 0; }
        const r = await api('/v1/exam/apply', 'POST', { ...target, on: true, config, profile_name: name });
        done(r, true);
      } });
    wireForm();
  }
  function askOff(target, label) {
    modal({ title: 'ปิดโหมดสอบ?', confirm: 'ปิดโหมดสอบ', desc: `<b>${esc(label)}</b> จะกลับมาใช้เว็บ โปรแกรม และ USB ได้ตามปกติ หลักฐานที่เก็บไว้แล้วยังอยู่`,
      onConfirm: async () => done(await api('/v1/exam/apply', 'POST', { ...target, on: false }), false) });
  }
  function done(r, on) {
    const sk = r?.skipped?.length || 0;
    toast(`${on ? 'ส่งโหมดสอบ' : 'ส่งคำสั่งปิดโหมดสอบ'}แล้ว ${r?.updated ?? 0} เครื่อง${sk ? `, ข้าม ${sk} เครื่อง` : ''}`);
    hostState.clear(); getSel().clear(); renderPage();
  }

  // ── machine page card ──
  function hostCard(h, dis) {
    const st = hostState.get(h.id);
    if (!st || Date.now() - st.at > 8000) refreshHost(h.id);
    const row = st?.row ? { ...st.row, measured: inv(h).plugins?.exam || st.row.measured } : { on: false, measured: inv(h).plugins?.exam || null };
    const s = stateOf(row, h.online), old = cfg && (!h.agent_version || verCmp(h.agent_version, cfg.min_agent) < 0), n = st?.row?.findings_24h || 0;
    return card('โหมดสอบ', 'จำกัดเว็บและโปรแกรมระหว่างการสอบ และเก็บภาพหน้าจอเป็นหลักฐาน', `<div class="remote-box" id="exam-card">${stateBox('โหมดสอบ', s)}
      <div class="btn-group">${s.on ? `<button class="btn" data-ex="edit" ${dis}>${ic('settings')}แก้การตั้งค่า</button><button class="btn" data-ex="off" ${dis}>${ic('x')}ปิดโหมดสอบ</button>`
        : `<button class="btn primary" data-ex="on" ${dis || (old ? 'disabled title="Agent ของเครื่องนี้ยังไม่รองรับ รออัปเดตอัตโนมัติ"' : '')}>${ic('shield')}เปิดโหมดสอบ</button>`}
        <a class="btn ghost" href="#/exam?host=${encodeURIComponent(h.id)}">${ic('log')}ดูหลักฐาน${n ? ` (${n} รายการใน 24 ชั่วโมง)` : ''}</a></div></div>`);
  }
  async function refreshHost(id) {
    const prev = hostState.get(id); hostState.set(id, { at: Date.now(), row: prev?.row || null });
    try {
      await ensure();
      const row = (await api('/v1/exam/hosts?host_id=' + encodeURIComponent(id))).items?.[0] || null;
      const changed = JSON.stringify(row) !== JSON.stringify(prev?.row || null) || !prev;
      hostState.set(id, { at: Date.now(), row });
      if (changed && route() === 'hosts' && routeArg() === id) renderPage();
    } catch (e) { /* the core page works without this card */ }
  }
  const bulkButtons = () => `<button class="btn sm" data-bulk-ex="on">${ic('shield')}เปิดโหมดสอบ</button><button class="btn sm" data-bulk-ex="off">ปิดโหมดสอบ</button>`;

  // ── the exam page ──
  async function page() {
    await ensure(true);
    const q = new URLSearchParams(location.hash.split('?')[1] || '');
    if (q.get('host')) { filter.host = q.get('host'); history.replaceState(null, '', '#/exam'); }
    const ev = new URLSearchParams({ limit: '60' }); if (filter.host) ev.set('host_id', filter.host); if (filter.kind) ev.set('kind', filter.kind);
    const [hosts, events, sites] = await Promise.all([api('/v1/exam/hosts'), api('/v1/exam/events?' + ev), api('/v1/sites').catch(() => [])]);
    pageData = { hosts: hosts.items || [], events: events.items || [], sites: Array.isArray(sites) ? sites : (sites.items || []) };
    const on = pageData.hosts.filter(h => h.on);
    const hostRows = pageData.hosts.map(h => { const s = stateOf(h, h.online); return `<tr>
      <td><a href="#/hosts/${encodeURIComponent(h.host_id)}"><b>${esc(h.hostname || h.host_id)}</b></a></td>
      <td><span class="badge ${s.tone}">${esc(s.label)}</span></td><td>${esc(h.profile_name || (h.on ? 'กำหนดเอง' : ''))}</td>
      <td>${h.findings_24h ? `<button class="linkish bad" data-ex-filter="${esc(h.host_id)}">${h.findings_24h} รายการ</button>` : '<span class="mute">ไม่มี</span>'}</td>
      <td>${h.last_finding ? when(h.last_finding) : ''}</td>
      <td class="right">${h.on ? `<button class="btn sm" data-ex-row="edit" data-id="${esc(h.host_id)}">แก้</button> <button class="btn sm" data-ex-row="off" data-id="${esc(h.host_id)}">ปิด</button>` : ''}</td></tr>`; }).join('');
    const hostOpts = pageData.hosts.map(h => `<option value="${esc(h.host_id)}" ${filter.host === h.host_id ? 'selected' : ''}>${esc(h.hostname || h.host_id)}</option>`).join('');
    return `<div class="ex-page">${card('เริ่มการสอบ', 'เลือกห้อง แล้วกำหนดสิ่งที่อนุญาตระหว่างการสอบ', `<div class="remote-box">
        <ul class="remote-pts"><li>${ic('check')}<span>จำกัดเว็บ โปรแกรม และ USB ตามที่กำหนด และปิดช่องทางเลี่ยงในเบราว์เซอร์</span></li>
          <li>${ic('check')}<span>เมื่อพบการทำผิด ระบบถ่ายภาพหน้าจอพร้อมเวลา ชื่อผู้ใช้ และโปรแกรม ส่งมาเก็บที่ server</span></li>
          <li>${ic('check')}<span>เป็นส่วนเสริม แยกจากการติดตั้ง Windows และการสั่งงานเครื่อง</span></li></ul>
        <div class="btn-group"><select class="select ex-room" id="ex-room" aria-label="ห้อง">${pageData.sites.map(s => `<option value="${esc(s.id)}">${esc(s.name || s.id)}</option>`).join('') || '<option value="">ยังไม่มีห้อง</option>'}</select>
          <button class="btn primary" data-ex-room="on">${ic('shield')}เปิดโหมดสอบทั้งห้อง</button><button class="btn" data-ex-room="off">ปิดโหมดสอบทั้งห้อง</button>
          <span class="mute ex-note">เลือกเป็นรายเครื่องได้จากหน้าเครื่อง</span></div></div>`)}
      ${card('เครื่องในโหมดสอบ', on.length ? `เปิดอยู่ ${on.length} เครื่อง` : 'ยังไม่มีเครื่องที่เปิดโหมดสอบ', hostRows ? `<div class="table-wrap"><table class="t"><thead><tr><th>เครื่อง</th><th>สถานะ</th><th>ชุดการตั้งค่า</th><th>พบใน 24 ชั่วโมง</th><th>ล่าสุด</th><th></th></tr></thead><tbody>${hostRows}</tbody></table></div>`
        : empty('shield', 'ยังไม่มีเครื่องในโหมดสอบ', ''), '', true)}
      ${card('หลักฐาน', `ลบเองได้ทุกเมื่อ รายการที่ไม่ได้ลบจะถูกลบอัตโนมัติหลัง ${cfg.keep_days} วัน`, `<div class="ex-filter"><select class="select" id="ex-f-host" aria-label="เครื่อง"><option value="">ทุกเครื่อง</option>${hostOpts}</select>
          <select class="select" id="ex-f-kind" aria-label="ประเภท"><option value="violations" ${filter.kind === 'violations' ? 'selected' : ''}>เฉพาะการทำผิด</option><option value="" ${filter.kind === '' ? 'selected' : ''}>ทั้งหมด รวมภาพตามรอบเวลาและโปรแกรมเบื้องหลัง</option>
          ${Object.entries(KIND).map(([k, v]) => `<option value="${k}" ${filter.kind === k ? 'selected' : ''}>${v[1]}</option>`).join('')}</select>
          <span class="spacer"></span><button class="btn" data-ex-clear="1" ${pageData.events.length ? '' : 'disabled'}>${ic('trash')}ลบหลักฐานตามตัวกรองนี้</button></div>
        ${pageData.events.length ? `<div class="ex-grid">${pageData.events.map(evCard).join('')}</div>` : empty('check', 'ไม่พบรายการ', '<p class="mute">ยังไม่มีการทำผิดตามตัวกรองนี้</p>')}`)}
      ${card('ชุดการตั้งค่าที่บันทึกไว้', 'ใช้ซ้ำได้ทุกการสอบ', profiles.length ? `<ul class="ex-profiles">${profiles.map(p => `<li><div><b>${esc(p.name)}</b><small class="mute">${esc(summary(p.config))}</small></div>
          <button class="btn sm ghost" data-ex-del="${esc(p.id)}" data-name="${esc(p.name)}" aria-label="ลบ ${esc(p.name)}">${ic('trash')}</button></li>`).join('')}</ul>`
        : '<p class="mute ex-note">ยังไม่มี ตั้งชื่อในช่องบันทึกตอนเปิดโหมดสอบเพื่อเก็บไว้ใช้ครั้งหน้า</p>')}</div>`;
  }
  const what = e => { const d = e.detail || {}; return e.kind === 'keyword' ? `คำว่า "${d.word || ''}" ในหน้าต่าง ${d.window || ''}` : e.kind === 'app_blocked' ? `${d.process || ''}${d.window ? ` หน้าต่าง "${d.window}" ${WSTATE[d.window_state] || ''}` : ''} ถูกปิด`
    : e.kind === 'app_background' ? `${d.process || ''} ทำงานอยู่เบื้องหลังโดยไม่มีหน้าต่าง จึงถูกปิดและไม่นับเป็นการทำผิด`
    : e.kind === 'periodic' ? (d.window || '') : e.kind === 'watcher_stopped' ? 'ตัวเฝ้าดูหยุดทำงานและถูกเริ่มใหม่' : (d.problem || ''); };
  function evCard(e) {
    const k = KIND[e.kind] || ['', e.kind];
    return `<article class="ex-ev"><button class="ex-shot" data-ex-open="${esc(e.id)}" ${e.has_shot ? '' : 'disabled'} aria-label="เปิดภาพหลักฐาน">
        ${e.has_shot ? `<img data-ex-img="${esc(e.id)}" alt="" ${shots.has(e.id) ? `src="${shots.get(e.id)}"` : ''}>` : `<span class="mute">ไม่มีภาพ</span>`}</button>
      <div class="ex-meta"><span class="badge ${k[0]}">${esc(k[1])}</span><b>${esc(e.hostname || e.host_id)}</b>
        <button class="btn sm ghost ex-del" data-ex-rm="${esc(e.id)}" aria-label="ลบหลักฐานนี้" title="ลบหลักฐานนี้">${ic('trash')}</button>
        <time>${esc(clock(e.at))}</time>${e.detail?.user ? `<span class="mute">ผู้ใช้ ${esc(e.detail.user)}</span>` : ''}<p translate="no">${esc(what(e))}</p>${idleNote(e) ? `<p class="ex-idle">${ic('alert')}${esc(idleNote(e))}</p>` : ''}</div></article>`;
  }
  async function loadShot(id) {
    if (shots.has(id)) return shots.get(id);
    const r = await fetch('/v1/exam/events/' + encodeURIComponent(id) + '/shot', { headers: authHeaders() });
    if (!r.ok) throw new Error(r.status === 409 ? 'ไฟล์ภาพไม่ตรงกับค่าที่บันทึกไว้ตอนรับ' : 'เปิดภาพไม่ได้');
    const u = URL.createObjectURL(await r.blob()); shots.set(id, u); return u;
  }
  function after() {
    clearTimeout(timer);
    const fh = $('#ex-f-host'), fk = $('#ex-f-kind');
    if (fh) fh.onchange = () => { filter.host = fh.value; renderPage(); };
    if (fk) fk.onchange = () => { filter.kind = fk.value; renderPage(); };
    (async () => { for (const img of document.querySelectorAll('img[data-ex-img]:not([src])')) { try { img.src = await loadShot(img.dataset.exImg); } catch (e) { img.replaceWith(Object.assign(document.createElement('span'), { className: 'mute', textContent: e.message })); } } })();
    timer = setTimeout(() => { if (route() === 'exam' && !document.querySelector('.scrim')) renderPage(); }, 20000);
  }
  async function openShot(id) {
    const e = pageData?.events.find(x => x.id === id); if (!e) return;
    let url; try { url = await loadShot(id); } catch (err) { return toast(err.message, 'bad'); }
    const k = KIND[e.kind] || ['', e.kind];
    modal({ title: `${k[1]} บน ${e.hostname || e.host_id}`, confirm: 'ปิด', wide: true,
      body: `<img class="ex-full" src="${url}" alt="ภาพหน้าจอของ ${esc(e.hostname || '')}">
        <dl class="ex-facts"><div><dt>เวลาบนเครื่อง</dt><dd>${esc(clock(e.at))}</dd></div><div><dt>server ได้รับ</dt><dd>${esc(clock(e.received_at))}</dd></div>
          <div><dt>ผู้ใช้</dt><dd translate="no">${esc(e.detail?.user || 'ไม่ทราบ')}</dd></div><div><dt>รายละเอียด</dt><dd translate="no">${esc(what(e))}</dd></div>
          ${idleNote(e) ? `<div><dt>การใช้งานเครื่อง</dt><dd>${esc(idleNote(e))}</dd></div>` : ''}
          <div><dt>SHA-256 ของไฟล์</dt><dd class="mono ex-hash" translate="no">${esc(e.shot_sha256)}</dd></div></dl>
        <a class="btn" href="${url}" download="${esc((e.hostname || e.host_id) + '-' + String(e.at).replace(/[:.]/g, '-'))}.jpg">${ic('hdd')}บันทึกภาพ</a>`,
      onConfirm: async () => {} });
  }

  // forget cached pictures of deleted findings (ids), or all of them (null)
  function drop(ids) { for (const [id, u] of [...shots]) if (!ids || ids.includes(id)) { URL.revokeObjectURL(u); shots.delete(id); } }
  document.addEventListener('click', ev => {
    const b = ev.target.closest('[data-ex],[data-bulk-ex],[data-ex-room],[data-ex-row],[data-ex-del],[data-ex-open],[data-ex-filter],[data-ex-rm],[data-ex-clear]'); if (!b) return;
    const d = b.dataset;
    if (d.ex) { const h = getHost(), row = hostState.get(h.id)?.row; return d.ex === 'off' ? askOff({ host_ids: [h.id] }, h.hostname || h.id) : askOn({ host_ids: [h.id] }, h.hostname || h.id, d.ex === 'edit' ? row?.config : null, d.ex === 'edit' ? row?.profile_name : ''); }
    if (d.bulkEx) { const ids = [...getSel()], label = ids.length + ' เครื่องที่เลือก'; return d.bulkEx === 'on' ? askOn({ host_ids: ids }, label) : askOff({ host_ids: ids }, label); }
    if (d.exRoom) { const sel = $('#ex-room'); if (!sel?.value) return toast('ยังไม่มีห้อง สร้างห้องได้ที่หน้าตั้งค่า', 'bad'); const label = 'ห้อง ' + sel.selectedOptions[0].textContent; return d.exRoom === 'on' ? askOn({ site_id: sel.value }, label) : askOff({ site_id: sel.value }, label); }
    if (d.exRow) { const h = pageData?.hosts.find(x => x.host_id === d.id); if (!h) return; return d.exRow === 'off' ? askOff({ host_ids: [h.host_id] }, h.hostname || h.host_id) : askOn({ host_ids: [h.host_id] }, h.hostname || h.host_id, h.config, h.profile_name); }
    if (d.exDel) return modal({ title: 'ลบชุดการตั้งค่า?', confirm: 'ลบ', danger: true, desc: `ลบ <b>${esc(d.name)}</b> ออกจากรายการ เครื่องที่ใช้ชุดนี้อยู่ไม่เปลี่ยน`,
      onConfirm: async () => { await api('/v1/exam/profiles/' + encodeURIComponent(d.exDel), 'DELETE'); cfgAt = 0; toast('ลบชุดการตั้งค่าแล้ว'); renderPage(); } });
    if (d.exOpen) return openShot(d.exOpen);
    if (d.exRm) { const e = pageData?.events.find(x => x.id === d.exRm); if (!e) return;
      return modal({ title: 'ลบหลักฐานนี้?', confirm: 'ลบ', danger: true, desc: `${esc((KIND[e.kind] || ['', e.kind])[1])} บน <b>${esc(e.hostname || e.host_id)}</b> เวลา ${esc(clock(e.at))}`,
        body: `<div class="callout warn">${ic('alert')}<div>ภาพหน้าจอและรายละเอียดจะถูกลบถาวร กู้คืนไม่ได้</div></div>`,
        onConfirm: async () => { await api('/v1/exam/events/' + encodeURIComponent(e.id), 'DELETE'); drop([e.id]); toast('ลบหลักฐานแล้ว 1 รายการ'); renderPage(); } }); }
    if (d.exClear) { const host = pageData?.hosts.find(x => x.host_id === filter.host), kind = filter.kind === 'violations' ? 'เฉพาะการทำผิด' : filter.kind ? (KIND[filter.kind] || ['', filter.kind])[1] : 'ทุกประเภท';
      return modal({ title: 'ลบหลักฐานตามตัวกรองนี้?', confirm: 'ลบทั้งหมดที่ตรง', danger: true,
        desc: `เครื่อง: <b>${esc(filter.host ? (host?.hostname || filter.host) : 'ทุกเครื่อง')}</b><br>ประเภท: <b>${esc(kind)}</b>`,
        body: `<div class="callout warn">${ic('alert')}<div>ลบทุกรายการที่ตรงกับตัวกรอง รวมรายการที่ไม่ได้แสดงในหน้านี้ ภาพหน้าจอจะถูกลบถาวร กู้คืนไม่ได้</div></div>`,
        onConfirm: async () => { const r = await api('/v1/exam/events/delete', 'POST', { host_id: filter.host, kind: filter.kind, confirm: true }); drop(null); toast(`ลบหลักฐานแล้ว ${r.deleted} รายการ`); renderPage(); } }); }
    if (d.exFilter) { filter.host = d.exFilter; filter.kind = 'violations'; return renderPage(); }
  });

  const EN = { "ตั้งโหมดสอบ": "Set exam mode", "โหมดสอบ": "Exam mode", "จำกัดเว็บที่เปิดได้ระหว่างการสอบ": "Limit which websites open during an exam", "เปิดโหมดสอบไม่สำเร็จ": "Exam mode could not be applied", "กำลังเปิดโหมดสอบ": "Turning exam mode on", "กำลังปิดโหมดสอบ": "Turning exam mode off", "จะเปิดเมื่อกลับมาออนไลน์": "it turns on when the machine is back online", "จะปิดเมื่อเครื่องกลับมาออนไลน์": "it turns off when the machine is back online", "ปิดโหมดสอบได้ทุกเมื่อ เบราว์เซอร์กลับเป็นปกติ": "Turn exam mode off at any time and browsers are back to normal", "ปิดโหมดสอบ?": "Turn exam mode off?", "ปิดโหมดสอบ": "Turn exam mode off", "เปิดโหมดสอบ?": "Turn exam mode on?", "เปิดโหมดสอบ": "Turn exam mode on", "เว็บที่อนุญาต บรรทัดละหนึ่งเว็บ": "Allowed sites, one per line", "ส่งโหมดสอบ": "Exam mode sent,", "ส่งคำสั่งปิดโหมดสอบ": "Exam mode off sent,", "Agent ของเครื่องนี้ยังไม่รองรับ รออัปเดตอัตโนมัติ": "This machine's agent does not support it yet. It updates itself.", "จำกัดเว็บและโปรแกรมระหว่างการสอบ และเก็บภาพหน้าจอเป็นหลักฐาน": "Limit websites and programs during an exam, and keep screenshots as evidence", "บันทึกชุดการตั้งค่าสอบ": "Exam set-up saved", "ลบชุดการตั้งค่าสอบ": "Exam set-up deleted", "เปิดโปรแกรมที่ห้าม": "Blocked program opened", "พบคำที่เฝ้าดู": "Watched word found", "ภาพตามรอบเวลา": "Timed screenshot", "ตัวเฝ้าดูถูกปิด": "Watcher was stopped", "การตั้งค่าถูกแก้": "Settings were changed", "โปรแกรม AI": "AI programs", "โปรแกรมแชต": "Chat programs", "โปรแกรมควบคุมเครื่องจากระยะไกล": "Remote control programs", "เปิดอยู่ ยังไม่ได้เฝ้าดูหน้าจอ": "On, the screen is not watched yet", "ตัวเฝ้าดูเริ่มทำงานเมื่อมีผู้ใช้เข้าใช้งานเครื่อง": "the watcher starts when a user signs in", "ไม่มีการจำกัดหรือเฝ้าดูเครื่องนี้": "This machine is not limited or watched", "บล็อกเว็บตามรายการ": "listed sites blocked", "ปิดโปรแกรมที่ห้าม": "blocked programs closed", "ปิด USB เก็บข้อมูล": "USB storage off", "ถ่ายภาพเมื่อทำผิด": "screenshot on violation", "ไม่มีข้อจำกัด": "no limits", "เริ่มจากชุดการตั้งค่า": "Start from a set-up", "ค่าที่แนะนำสำหรับการสอบผ่านเว็บ": "Recommended for a web exam", "เปิดได้เฉพาะเว็บที่กำหนด": "Only the listed sites open", "เข้มที่สุด เหมาะกับการสอบผ่านระบบสอบออนไลน์": "Strictest. For exams taken in an online exam system.", "โดเมนย่อยของแต่ละเว็บเปิดได้ด้วย": "Sub-domains of each site open too", "เปิดได้ทุกเว็บ ยกเว้นที่ห้าม": "Every site opens except the blocked ones", "เหมาะกับการสอบแบบเปิดตำรา": "For open-book exams", "เช่น ChatGPT, Claude, Gemini, Copilot, Perplexity, DeepSeek": "such as ChatGPT, Claude, Gemini, Copilot, Perplexity, DeepSeek", "เว็บที่ห้ามเพิ่มเติม บรรทัดละหนึ่งเว็บ": "More blocked sites, one per line", "ไม่จำกัดเว็บ": "No web limits", "ใช้เมื่อต้องการเฝ้าดูหรือจำกัดโปรแกรมอย่างเดียว": "Use it to watch or to limit programs only", "ปิดส่วนขยาย": "Extensions off", "กันส่วนขยาย AI และ VPN ในเบราว์เซอร์": "Stops AI and VPN extensions in the browser", "ปิดหน้าต่างแบบส่วนตัว": "Private windows off", "ปิดเครื่องมือนักพัฒนา": "Developer tools off", "โปรแกรมที่ห้ามเปิด": "Programs that must not run", "และอื่น ๆ": "and more", "โปรแกรมอื่นที่ห้าม ใส่ชื่อไฟล์โปรแกรม บรรทัดละหนึ่งชื่อ": "Other blocked programs: the program file name, one per line", "โปรแกรมในรายการจะถูกปิดทันทีที่เปิด และถูกบันทึกเป็นหลักฐาน": "A listed program is closed as soon as it starts and recorded as evidence", "ปิด Windows Copilot และแถบข้างของ Edge": "Windows Copilot and the Edge sidebar off", "แฟลชไดรฟ์และฮาร์ดดิสก์พกพา เมาส์และคีย์บอร์ดใช้ได้ตามปกติ": "Flash drives and portable disks. Mice and keyboards keep working.", "หลักฐาน": "Evidence", "ถ่ายภาพหน้าจอทุกครั้งที่พบการทำผิด": "Screenshot with every violation", "ภาพเดียวกันไม่เกินนาทีละครั้ง": "At most once a minute for the same thing", "เฝ้าดูชื่อหน้าต่างที่มีชื่อ AI": "Watch window titles for AI names", "คำอื่นที่ต้องการเฝ้าดูในชื่อหน้าต่าง บรรทัดละหนึ่งคำ": "Other words to watch for in window titles, one per line", "ถ่ายภาพหน้าจอตามรอบเวลา": "Timed screenshots", "ไม่ถ่าย ถ่ายเฉพาะตอนทำผิด": "None, only on a violation", "บันทึกไว้ใช้ครั้งหน้า": "Save for next time", "ชื่อชุดการตั้งค่า เว้นว่างถ้าไม่ต้องการบันทึก": "Set-up name. Leave empty to not save.", "ผู้ใช้ที่นั่งหน้าเครื่องจะเห็นข้อความแจ้งว่าเครื่องอยู่ในโหมดสอบและมีการบันทึกภาพหน้าจอ หลักฐานถูกลบอัตโนมัติหลัง": "The person at the machine sees a notice that it is in exam mode and that screenshots are recorded. Evidence is deleted automatically after", "ควรล็อกการติดตั้งโปรแกรมควบคู่กัน ไม่เช่นนั้นผู้ใช้ที่เป็นผู้ดูแลเครื่องยังติดตั้งเบราว์เซอร์อื่นได้ โหมดสอบไม่ได้ควบคุมโทรศัพท์หรืออุปกรณ์อื่นของผู้สอบ": "Use it together with the install lock, otherwise a user who is a local admin can still install another browser. Exam mode does not control the examinee's phone or other devices.", "แก้การตั้งค่าโหมดสอบ?": "Change the exam set-up?", "บันทึกและส่งให้เครื่อง": "Save and send", "แก้การตั้งค่า": "Change set-up", "จะกลับมาใช้เว็บ โปรแกรม และ USB ได้ตามปกติ หลักฐานที่เก็บไว้แล้วยังอยู่": "can use the web, programs and USB as usual again. Evidence already collected stays.", "ดูหลักฐาน": "View evidence", "รายการใน 24 ชั่วโมง": "in 24 hours", "โมดูลโหมดสอบปิดอยู่บน server นี้": "The exam module is off on this server", "เริ่มการสอบ": "Start an exam", "เลือกห้อง แล้วกำหนดสิ่งที่อนุญาตระหว่างการสอบ": "Pick a room, then set what is allowed during the exam", "จำกัดเว็บ โปรแกรม และ USB ตามที่กำหนด และปิดช่องทางเลี่ยงในเบราว์เซอร์": "Limits websites, programs and USB as set, and closes the ways around in the browser", "เมื่อพบการทำผิด ระบบถ่ายภาพหน้าจอพร้อมเวลา ชื่อผู้ใช้ และโปรแกรม ส่งมาเก็บที่ server": "On a violation it takes a screenshot with the time, user and program, and stores it on the server", "เปิดโหมดสอบทั้งห้อง": "Turn exam mode on for the room", "ปิดโหมดสอบทั้งห้อง": "Turn exam mode off for the room", "เลือกเป็นรายเครื่องได้จากหน้าเครื่อง": "Pick single machines on the Machines page", "เครื่องในโหมดสอบ": "Machines in exam mode", "ยังไม่มีเครื่องที่เปิดโหมดสอบ": "No machine has exam mode on", "ยังไม่มีเครื่องในโหมดสอบ": "No machines in exam mode yet", "ชุดการตั้งค่า": "Set-up", "พบใน 24 ชั่วโมง": "Found in 24 hours", "เฉพาะการทำผิด": "Violations only", "ทั้งหมด รวมภาพตามรอบเวลา": "Everything, timed screenshots included", "ไม่พบรายการ": "Nothing found", "ยังไม่มีการทำผิดตามตัวกรองนี้": "No violations match this filter", "ชุดการตั้งค่าที่บันทึกไว้": "Saved set-ups", "ใช้ซ้ำได้ทุกการสอบ": "Reusable for every exam", "ยังไม่มี ตั้งชื่อในช่องบันทึกตอนเปิดโหมดสอบเพื่อเก็บไว้ใช้ครั้งหน้า": "None yet. Give a name in the save field when turning exam mode on to keep the set-up.", "ถูกปิด": "was closed", "ตัวเฝ้าดูหยุดทำงานและถูกเริ่มใหม่": "The watcher stopped and was started again", "ไม่มีภาพ": "No screenshot", "เปิดภาพหลักฐาน": "Open the screenshot", "ไฟล์ภาพไม่ตรงกับค่าที่บันทึกไว้ตอนรับ": "The file does not match the hash recorded on arrival", "เปิดภาพไม่ได้": "Cannot open the screenshot", "เวลาบนเครื่อง": "Time on the machine", "server ได้รับ": "Server received", "SHA-256 ของไฟล์": "File SHA-256", "บันทึกภาพ": "Save image", "ยังไม่มีห้อง สร้างห้องได้ที่หน้าตั้งค่า": "No rooms yet. Create one in Settings.", "ลบชุดการตั้งค่า?": "Delete this set-up?", "ออกจากรายการ เครื่องที่ใช้ชุดนี้อยู่ไม่เปลี่ยน": "from the list. Machines using it do not change.", "ลบชุดการตั้งค่าแล้ว": "Set-up deleted", "ปิดโปรแกรมเบื้องหลัง": "Background program closed", "ทำงานอยู่เบื้องหลังโดยไม่มีหน้าต่าง จึงถูกปิดและไม่นับเป็นการทำผิด": "ran in the background without a window, so it was closed and is not counted as a violation", "อยู่ด้านหน้า": "in front", "เปิดอยู่บนจอ": "open on screen", "ย่ออยู่": "minimized", "ทั้งหมด รวมภาพตามรอบเวลาและโปรแกรมเบื้องหลัง": "Everything, including timed screenshots and background programs", "ไม่มีการใช้คีย์บอร์ดหรือเมาส์มาแล้ว": "No keyboard or mouse use for", "ภาพอาจเป็นหน้าจอค้าง": "the picture may be a stale screen", "การใช้งานเครื่อง": "Machine use", "ลบหลักฐานการสอบ": "Exam evidence deleted", "ลบหลักฐานตามตัวกรองนี้": "Delete evidence matching this filter", "ลบหลักฐานตามตัวกรองนี้?": "Delete evidence matching this filter?", "ลบหลักฐานนี้": "Delete this evidence", "ลบหลักฐานนี้?": "Delete this evidence?", "ภาพหน้าจอและรายละเอียดจะถูกลบถาวร กู้คืนไม่ได้": "The screenshot and its details are deleted for good and cannot be restored", "ลบทั้งหมดที่ตรง": "Delete all matching", "ประเภท:": "Kind:", "ทุกประเภท": "Every kind", "ลบทุกรายการที่ตรงกับตัวกรอง รวมรายการที่ไม่ได้แสดงในหน้านี้ ภาพหน้าจอจะถูกลบถาวร กู้คืนไม่ได้": "Deletes every entry that matches the filter, including those not shown on this page. Screenshots are deleted for good and cannot be restored.", "ลบหลักฐานแล้ว": "Evidence deleted:", "ลบเองได้ทุกเมื่อ รายการที่ไม่ได้ลบจะถูกลบอัตโนมัติหลัง": "Delete it yourself at any time. What you keep is deleted automatically after", "เป็นส่วนเสริม แยกจากการติดตั้ง Windows และการสั่งงานเครื่อง": "An add-on, separate from Windows installs and machine commands", "ส่วนเสริม": "Add-ons" };
  window.SWIM.plugin({ name: 'exam', title: 'โหมดสอบ', sub: 'จำกัดเว็บและโปรแกรมระหว่างการสอบ และเก็บภาพหน้าจอเป็นหลักฐาน', icon: 'shield',
    init, page, after, hostCard, bulkButtons, en: EN,
    actions: { 'exam.apply': 'ตั้งโหมดสอบ', 'exam.profile_saved': 'บันทึกชุดการตั้งค่าสอบ', 'exam.profile_deleted': 'ลบชุดการตั้งค่าสอบ', 'exam.evidence_deleted': 'ลบหลักฐานการสอบ', 'command.set_exam': 'ตั้งโหมดสอบ (ส่งซ้ำอัตโนมัติ)' },
    commands: { set_exam: ['ตั้งโหมดสอบ', 'shield'] } });
})();
