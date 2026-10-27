/* Foghorn web console.
   Plain JavaScript, no framework, no build step. Every piece of text that
   came from a computer or a person is placed with textContent, never as HTML,
   so a PC that reports a hostile "hostname" cannot inject anything. */
(function () {
  'use strict';

  // ---------- tiny DOM helper ----------
  function h(tag, attrs) {
    var el = document.createElement(tag);
    if (attrs) {
      Object.keys(attrs).forEach(function (k) {
        var v = attrs[k];
        if (v === null || v === undefined || v === false) return;
        if (k === 'class') el.className = v;
        else if (k.slice(0, 2) === 'on') el.addEventListener(k.slice(2), v);
        else if (k === 'value') el.value = v;
        else if (k === 'checked' || k === 'disabled' || k === 'selected' || k === 'required' || k === 'open') el[k] = !!v;
        else el.setAttribute(k, v === true ? '' : v);
      });
    }
    for (var i = 2; i < arguments.length; i++) append(el, arguments[i]);
    return el;
  }
  function append(el, child) {
    if (child === null || child === undefined || child === false) return;
    if (Array.isArray(child)) child.forEach(function (c) { append(el, c); });
    else el.appendChild(child.nodeType ? child : document.createTextNode(String(child)));
  }
  function clear(el) { while (el.firstChild) el.removeChild(el.firstChild); return el; }

  var LOGO = function () {
    var ns = 'http://www.w3.org/2000/svg';
    var svg = document.createElementNS(ns, 'svg');
    svg.setAttribute('viewBox', '0 0 32 32'); svg.setAttribute('aria-hidden', 'true');
    [['rect', { width: 32, height: 32, rx: 7, fill: '#F4B400' }],
     ['path', { d: 'M7 13.5v5a1.5 1.5 0 0 0 1.5 1.5H11l7 4.5v-17L11 12H8.5A1.5 1.5 0 0 0 7 13.5z', fill: '#13212C' }],
     ['path', { d: 'M21.5 11.5a6.5 6.5 0 0 1 0 9M24.5 8.5a10.5 10.5 0 0 1 0 15', fill: 'none', stroke: '#13212C', 'stroke-width': 2, 'stroke-linecap': 'round' }]
    ].forEach(function (p) {
      var e = document.createElementNS(ns, p[0]);
      Object.keys(p[1]).forEach(function (k) { e.setAttribute(k, p[1][k]); });
      svg.appendChild(e);
    });
    return svg;
  };

  // ---------- API ----------
  function api(method, path, body) {
    var opts = { method: method, headers: { 'X-Foghorn': '1' }, credentials: 'same-origin' };
    if (body !== undefined) { opts.headers['Content-Type'] = 'application/json'; opts.body = JSON.stringify(body); }
    return fetch(path, opts).then(function (res) {
      return res.text().then(function (text) {
        var data = null;
        try { data = text ? JSON.parse(text) : null; } catch (e) { /* not JSON */ }
        if (res.ok) return data;
        var err = new Error((data && data.message) || ('The server answered ' + res.status + '.'));
        err.status = res.status; err.code = data && data.error;
        if (res.status === 401 && path !== '/api/login') { S.me = null; render(); }
        if (err.code === 'password_change_required' && S.me) { S.me.must_change = true; render(); }
        throw err;
      });
    }, function () {
      throw new Error('Cannot reach the Foghorn server. Check it is running and that you are on the network.');
    });
  }

  function toast(msg, bad) {
    var t = h('div', { class: 'toast' + (bad ? ' bad' : '') }, msg);
    document.getElementById('toasts').appendChild(t);
    setTimeout(function () { t.remove(); }, bad ? 7000 : 4000);
  }
  function fail(err) { toast(err.message || String(err), true); }

  // ---------- formatting ----------
  function pad(n) { return n < 10 ? '0' + n : '' + n; }
  function clock(d) { d = new Date(d); return pad(d.getHours()) + ':' + pad(d.getMinutes()); }
  function when(iso) {
    if (!iso || iso.slice(0, 4) === '0001') return 'never';
    var d = new Date(iso), now = new Date(), secs = (now - d) / 1000;
    if (secs < 45) return 'just now';
    if (secs < 3600) return Math.round(secs / 60) + ' min ago';
    if (d.toDateString() === now.toDateString()) return 'today ' + clock(d);
    var y = new Date(now); y.setDate(now.getDate() - 1);
    if (d.toDateString() === y.toDateString()) return 'yesterday ' + clock(d);
    return d.toLocaleDateString(undefined, { day: 'numeric', month: 'short' }) + ' ' + clock(d);
  }
  function until(iso) {
    var d = new Date(iso), now = new Date();
    return d.toDateString() === now.toDateString() ? clock(d) : d.toLocaleDateString(undefined, { day: 'numeric', month: 'short' }) + ' ' + clock(d);
  }
  function plural(n, one, many) { return n + ' ' + (n === 1 ? one : many); }
  // "CN=PC,OU=Lab 1,OU=Computing,DC=x" -> "Computing / Lab 1"
  function ouPath(dn) {
    if (!dn) return '';
    var parts = dn.split(/,(?=(?:OU|CN|DC)=)/i).filter(function (p) { return /^OU=/i.test(p); })
      .map(function (p) { return p.slice(3); });
    return parts.reverse().join(' / ');
  }
  var LEVELS = { info: 'Notice', warning: 'Warning', critical: 'Urgent' };
  var FIELDS = { hostname: 'Computer name', user: 'Username', ou: 'AD organisational unit', group: 'AD group (of the user)', ip: 'IP address or subnet' };
  var FIELD_HINTS = {
    hostname: 'Wildcards work: LAB1-* matches every computer whose name starts LAB1-.',
    user: 'The Windows username of whoever is logged on, e.g. jsmith or 23*.',
    ou: 'Any part of the computer\u2019s OU path, e.g. OU=Library. Computers in sub-OUs match too.',
    group: 'An AD security group the logged-on user belongs to, e.g. Students-Year1.',
    ip: 'A subnet such as 10.20.30.0/24, or a pattern such as 10.20.30.*'
  };

  // ---------- state ----------
  var S = { me: null, org: '', version: '', groups: [], templates: [], sounds: [], meta: { icons: {} }, timers: [] };
  var draft = freshDraft();
  function freshDraft() {
    return { title: '', message: '', level: 'info', display: 'corner', require_ack: false, display_seconds: 0,
      sound: false, link: '', expires_minutes: 10, mode: 'all', group_ids: [], field: 'hostname', patterns: '',
      accent: '', label: '', icon: '', dismiss_label: '', responses: [], sound_id: '', sound_loop: false,
      when: 'now', once: '', days: [1, 2, 3, 4, 5], time: '15:50', schedule_id: null, schedule_name: '' };
  }
  function isAdmin() { return S.me && S.me.role === 'admin'; }
  function canLock() { return S.me && (S.me.role === 'admin' || S.me.can_lock); }
  var LEVEL_COLOURS = { info: '#1d6fb8', warning: '#c77700', critical: '#c3301c' };
  var DAY_NAMES = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
  function describeSchedule(sc) {
    if (sc.once) { var d = new Date(sc.once); return 'Once, ' + d.toLocaleDateString(undefined, { weekday: 'short', day: 'numeric', month: 'short' }) + ' at ' + clock(d); }
    var days = (sc.days || []).slice().sort().join(',');
    var label = days === '1,2,3,4,5' ? 'Weekdays' : days === '0,1,2,3,4,5,6' ? 'Every day' : days === '0,6' ? 'Weekends'
      : (sc.days || []).map(function (x) { return DAY_NAMES[x]; }).join(', ');
    return label + ' at ' + sc.time;
  }
  function kb(n) { return n < 1024 * 1024 ? Math.max(1, Math.round(n / 1024)) + ' KB' : (n / 1048576).toFixed(1) + ' MB'; }
  function restricted() { return S.me && S.me.role !== 'admin' && S.me.allowed_group_ids.length > 0; }
  function every(ms, fn) { S.timers.push(setInterval(fn, ms)); }
  function stopTimers() { S.timers.forEach(clearInterval); S.timers = []; }

  function buildTarget(d) {
    if (d.mode === 'all') return { all: true, group_ids: [], rules: [] };
    if (d.mode === 'groups') return { all: false, group_ids: d.group_ids.slice(), rules: [] };
    var rules = d.patterns.split(/[\n,;]+/).map(function (p) { return p.trim(); }).filter(Boolean)
      .map(function (p) { return { field: d.field, pattern: p }; });
    return { all: false, group_ids: [], rules: rules };
  }
  function specFromDraft(d) {
    var replies = d.responses.map(function (r) { return r.trim(); }).filter(Boolean);
    var ack = d.require_ack || replies.length > 0;
    return { title: d.title.trim(), message: d.message.trim(), level: d.level, display: d.display,
      require_ack: ack, display_seconds: ack ? 0 : d.display_seconds, sound: d.sound || !!d.sound_id,
      link: d.link.trim(), expires_minutes: d.expires_minutes, target: buildTarget(d),
      accent: d.accent, label: d.label.trim(), icon: d.icon, dismiss_label: replies.length ? '' : d.dismiss_label.trim(),
      responses: replies, sound_id: d.sound_id, sound_loop: (d.sound || !!d.sound_id) && d.sound_loop };
  }
  // Loads a saved spec (from a template, a sent alert or a schedule) into the builder.
  function draftFromSpec(spec, keepAudience) {
    var keep = keepAudience ? { mode: draft.mode, group_ids: draft.group_ids, field: draft.field, patterns: draft.patterns } : {};
    var d = Object.assign(freshDraft(), spec, keep);
    d.responses = (spec.responses || []).slice();
    delete d.target;
    if (!keepAudience && spec.target) {
      var tg = spec.target;
      if (tg.all) d.mode = 'all';
      else if (tg.group_ids && tg.group_ids.length) { d.mode = 'groups'; d.group_ids = tg.group_ids.slice(); }
      else if (tg.rules && tg.rules.length) { d.mode = 'custom'; d.field = tg.rules[0].field; d.patterns = tg.rules.filter(function (r) { return r.field === d.field; }).map(function (r) { return r.pattern; }).join(', '); }
    }
    return d;
  }

  // ---------- dialogs ----------
  function dialog(title, bodyNodes, buttons) {
    var dlg = h('dialog', null,
      h('div', { class: 'dh' }, h('h2', null, title)),
      h('div', { class: 'db' }, bodyNodes),
      h('div', { class: 'df' }, buttons));
    document.body.appendChild(dlg);
    dlg.addEventListener('close', function () { dlg.remove(); });
    dlg.showModal();
    return dlg;
  }
  function confirmDialog(title, bodyNodes, okLabel, danger) {
    return new Promise(function (resolve) {
      var dlg = dialog(title, bodyNodes, [
        h('button', { class: 'btn', onclick: function () { dlg.close(); resolve(false); } }, 'Cancel'),
        h('button', { class: 'btn ' + (danger ? 'danger' : 'primary'), onclick: function () { dlg.close(); resolve(true); } }, okLabel)
      ]);
      dlg.addEventListener('cancel', function () { resolve(false); });
    });
  }
  function secretDialog(title, intro, secret, outro) {
    var dlg = dialog(title, [h('p', null, intro), h('code', { class: 'secret' }, secret), outro ? h('p', { class: 'muted small' }, outro) : null], [
      h('button', { class: 'btn', onclick: function () { copy(secret); } }, 'Copy'),
      h('button', { class: 'btn primary', onclick: function () { dlg.close(); } }, 'Done')
    ]);
  }
  function copy(text) {
    if (navigator.clipboard && window.isSecureContext) {
      navigator.clipboard.writeText(text).then(function () { toast('Copied'); }, function () { toast('Select the text and press Ctrl+C', true); });
    } else {
      var ta = h('textarea', { value: text }); document.body.appendChild(ta); ta.select();
      try { document.execCommand('copy'); toast('Copied'); } catch (e) { toast('Select the text and press Ctrl+C', true); }
      ta.remove();
    }
  }

  // ---------- boot & routing ----------
  function boot() {
    api('GET', '/api/me').then(function (r) {
      S.me = r.user; S.org = r.org_name; S.version = r.version;
      return loadShared();
    }).catch(function () { /* not signed in */ }).then(render);
  }
  function loadShared() {
    if (!S.me || S.me.must_change) return Promise.resolve();
    return Promise.all([api('GET', '/api/groups'), api('GET', '/api/templates'), api('GET', '/api/sounds'), api('GET', '/api/meta')]).then(function (r) {
      S.groups = r[0] || []; S.templates = r[1] || []; S.sounds = r[2] || []; S.meta = r[3] || { icons: {} };
    });
  }
  window.addEventListener('hashchange', render);

  function render() {
    stopTimers();
    var app = clear(document.getElementById('app'));
    if (!S.me) { app.appendChild(loginView()); return; }
    if (S.me.must_change) { app.appendChild(h('div', { class: 'login' }, passwordPanel(true))); return; }

    var route = (location.hash || '#/send').slice(2).split('/');
    var views = { send: sendView, alerts: route[1] ? alertDetailView : alertsView, computers: computersView,
      groups: groupsView, settings: isAdmin() ? settingsView : null, help: helpView, account: accountView,
      lock: canLock() ? lockView : null, schedules: schedulesView, sounds: soundsView, api: apiView };
    var view = views[route[0]] || sendView;
    var main = h('main', { class: 'main' });
    app.appendChild(h('div', { class: 'shell' }, sidebar(route[0] || 'send'), main));
    document.title = (S.org ? S.org + ' \u2013 ' : '') + 'Foghorn';
    view(main, route[1]);
  }

  function sidebar(current) {
    function link(id, label) {
      return h('a', { href: '#/' + id, 'aria-current': current === id ? 'page' : null }, label);
    }
    return h('aside', { class: 'side' },
      h('div', { class: 'brand' }, LOGO(), h('div', null, h('b', null, 'Foghorn'), h('span', null, S.org || 'Desktop alerts'))),
      h('nav', { class: 'nav', 'aria-label': 'Main' },
        link('send', 'Send an alert'), canLock() ? link('lock', 'Lock screens') : null, link('alerts', 'Sent alerts'),
        link('schedules', 'Scheduled'), link('computers', 'Computers'), link('groups', 'Groups'), link('sounds', 'Sounds'),
        isAdmin() ? link('settings', 'Settings') : null, link('api', 'API'), link('help', 'Help')),
      h('div', { class: 'side-foot' },
        h('div', { class: 'credit' }, 'Built by ', h('a', { href: 'mailto:hello@archiepaice.com' }, 'Archie Paice')),
        h('div', { class: 'who' }, S.me.display_name || S.me.username),
        h('a', { href: '#/account' }, 'Change password'),
        h('a', { href: '#', onclick: function (e) { e.preventDefault(); api('POST', '/api/logout').catch(function () {}).then(function () { S.me = null; draft = freshDraft(); render(); }); } }, 'Sign out')));
  }

  // ---------- sign in ----------
  function loginView() {
    var user = h('input', { type: 'text', autocomplete: 'username', required: true, autocapitalize: 'none' });
    var pass = h('input', { type: 'password', autocomplete: 'current-password', required: true });
    var err = h('p', { class: 'error', role: 'alert' });
    var btn = h('button', { class: 'btn primary big', type: 'submit' }, 'Sign in');
    var form = h('form', { class: 'panel stack', onsubmit: function (e) {
      e.preventDefault(); btn.disabled = true; err.textContent = '';
      api('POST', '/api/login', { username: user.value, password: pass.value }).then(function (u) {
        S.me = u; return api('GET', '/api/me');
      }).then(function (r) { S.org = r.org_name; S.version = r.version; return loadShared(); })
        .then(function () { if (!location.hash) location.hash = '#/send'; render(); })
        .catch(function (e2) { err.textContent = e2.message; btn.disabled = false; pass.value = ''; pass.focus(); });
    } },
      h('div', { class: 'brand' }, LOGO(), h('div', null, h('b', null, 'Foghorn'), h('span', null, 'Desktop alerts'))),
      h('label', { class: 'field' }, h('span', null, 'Username'), user),
      h('label', { class: 'field' }, h('span', null, 'Password'), pass),
      err, btn);
    setTimeout(function () { user.focus(); }, 0);
    return h('div', { class: 'login' }, form);
  }

  function passwordPanel(forced) {
    var cur = h('input', { type: 'password', autocomplete: 'current-password', required: true });
    var nw = h('input', { type: 'password', autocomplete: 'new-password', required: true, minlength: 10 });
    var again = h('input', { type: 'password', autocomplete: 'new-password', required: true });
    var err = h('p', { class: 'error', role: 'alert' });
    return h('form', { class: 'panel stack', onsubmit: function (e) {
      e.preventDefault(); err.textContent = '';
      if (nw.value !== again.value) { err.textContent = 'The two new passwords are different.'; return; }
      api('POST', '/api/me/password', { current: cur.value, 'new': nw.value }).then(function () {
        S.me.must_change = false; toast('Password changed');
        return loadShared();
      }).then(function () { location.hash = '#/send'; render(); }).catch(function (e2) { err.textContent = e2.message; });
    } },
      h('h2', null, forced ? 'Choose your own password' : 'Change password'),
      forced ? h('p', { class: 'muted' }, 'You signed in with a temporary password. Pick one only you know before carrying on.') : null,
      h('label', { class: 'field' }, h('span', null, forced ? 'Temporary password' : 'Current password'), cur),
      h('label', { class: 'field' }, h('span', null, 'New password'), nw, h('small', null, 'At least 10 characters. A few unrelated words works well.')),
      h('label', { class: 'field' }, h('span', null, 'New password again'), again),
      err, h('button', { class: 'btn primary', type: 'submit' }, 'Change password'));
  }
  function accountView(main) {
    main.appendChild(h('div', { class: 'page-head' }, h('h1', null, 'Your account')));
    var wrap = h('div', null, passwordPanel(false));
    wrap.style.maxWidth = '460px';
    main.appendChild(wrap);
  }

  // ---------- send ----------
  function sendView(main) {
    var d = draft;
    var allowedGroups = restricted() ? S.groups.filter(function (g) { return S.me.allowed_group_ids.indexOf(g.id) >= 0; }) : S.groups;
    if (restricted()) d.mode = 'groups';
    d.group_ids = d.group_ids.filter(function (id) { return allowedGroups.some(function (g) { return g.id === id; }); });

    var preview = h('div', { class: 'screen', role: 'img', 'aria-label': 'Preview of the alert on a student\u2019s screen' });
    var notes = h('ul', { class: 'preview-notes' });
    var reach = h('div', { class: 'reach' }, 'Working out who this reaches\u2026');
    var err = h('p', { class: 'error', role: 'alert' });
    var reachTimer = null, lastReach = null;

    function radioSeg(name, options, key, cls) {
      return h('div', { class: 'seg', role: 'radiogroup' }, options.map(function (o) {
        return h('label', { class: cls ? cls + o[0] : null },
          h('input', { type: 'radio', name: name, value: o[0], checked: d[key] === o[0], onchange: function () { d[key] = o[0]; changed(); } }),
          o[1], o[2] ? h('small', null, o[2]) : null);
      }));
    }

    var title = h('input', { type: 'text', maxlength: 120, value: d.title, placeholder: 'e.g. Save your work', oninput: function () { d.title = title.value; changed(true); } });
    var counter = h('span', { class: 'counter' });
    var message = h('textarea', { maxlength: 2000, placeholder: 'What do people need to know or do?', oninput: function () { d.message = message.value; changed(true); } });
    message.value = d.message;
    var link = h('input', { type: 'url', value: d.link, placeholder: 'https://', oninput: function () { d.link = link.value; changed(true); } });

    var ack = h('input', { type: 'checkbox', checked: d.require_ack, onchange: function () { d.require_ack = ack.checked; changed(); } });
    var sound = h('input', { type: 'checkbox', checked: d.sound, onchange: function () { d.sound = sound.checked; changed(true); } });
    var autoclose = h('select', { onchange: function () { d.display_seconds = +autoclose.value; changed(true); } },
      [[0, 'Stays until someone closes it'], [15, 'Closes itself after 15 seconds'], [30, 'Closes itself after 30 seconds'], [60, 'Closes itself after 1 minute'], [300, 'Closes itself after 5 minutes'], [900, 'Closes itself after 15 minutes']]
        .map(function (o) { return h('option', { value: o[0], selected: d.display_seconds === o[0] }, o[1]); }));
    var minsToMidnight = Math.max(1, Math.round((new Date().setHours(23, 59, 0, 0) - Date.now()) / 60000));
    var expires = h('select', { onchange: function () { d.expires_minutes = +expires.value; } },
      [[2, 'No \u2013 only computers on right now'], [10, 'Yes, for the next 10 minutes'], [60, 'Yes, for the next hour'], [240, 'Yes, for the next 4 hours'], [minsToMidnight, 'Yes, until midnight'], [1440, 'Yes, for the next 24 hours']]
        .map(function (o) { return h('option', { value: o[0], selected: d.expires_minutes === o[0] }, o[1]); }));

    // who
    var whoBody = h('div', { class: 'stack' });
    function drawWho() {
      clear(whoBody);
      if (d.mode === 'groups') {
        if (!allowedGroups.length) {
          whoBody.appendChild(h('p', { class: 'notice' }, 'There are no groups yet. ', isAdmin() ? h('a', { href: '#/groups' }, 'Create one on the Groups page') : 'Ask an administrator to create one', ' \u2013 for example one per room.'));
        } else {
          whoBody.appendChild(h('div', { class: 'checklist' }, allowedGroups.map(function (g) {
            var cb = h('input', { type: 'checkbox', checked: d.group_ids.indexOf(g.id) >= 0, onchange: function () {
              d.group_ids = d.group_ids.filter(function (x) { return x !== g.id; });
              if (cb.checked) d.group_ids.push(g.id);
              changed();
            } });
            return h('label', { class: 'check' }, cb, h('span', null, g.name, h('small', null, g.online + ' on now')));
          })));
        }
      } else if (d.mode === 'custom') {
        var field = h('select', { onchange: function () { d.field = field.value; hint.textContent = FIELD_HINTS[d.field]; changed(); } },
          Object.keys(FIELDS).map(function (k) { return h('option', { value: k, selected: d.field === k }, FIELDS[k]); }));
        var pats = h('input', { type: 'text', value: d.patterns, placeholder: 'LAB1-*, LIB-PC07', oninput: function () { d.patterns = pats.value; changed(); } });
        var hint = h('small', { class: 'hint' }, FIELD_HINTS[d.field]);
        whoBody.appendChild(h('div', { class: 'rule-row' }, field, pats, h('span')));
        whoBody.appendChild(h('small', { class: 'hint' }, 'Separate several with commas. ', hint));
      }
      whoBody.appendChild(reach);
    }

    function drawPreview() {
      clear(preview);
      preview.appendChild(h('div', { class: 'window' }));
      preview.appendChild(h('div', { class: 'taskbar' }));
      var replies = d.responses.map(function (r) { return r.trim(); }).filter(Boolean);
      var ackLabel = d.dismiss_label.trim() || (d.require_ack ? 'I\u2019ve read this' : 'Dismiss');
      var accent = d.accent || LEVEL_COLOURS[d.level];
      var icon = d.icon ? h('span', { class: 'ic' }, S.meta.icons[d.icon] || '') : null;
      var buttons = replies.length
        ? replies.map(function (r, i) { return h('span', { class: 'b' + (i ? ' ghost' : '') }, r); })
        : [h('span', { class: 'b' }, ackLabel)];
      var pv = h('div', { class: 'pv ' + d.display + ' ' + d.level + (d.accent ? ' custom' : '') },
        h('div', { class: 'band' }, h('span', null, S.org || 'Foghorn'), h('span', null, d.label.trim() || LEVELS[d.level])),
        h('div', { class: 'body' }, h('div', { class: 't' }, icon, h('span', null, d.title || 'Your title appears here')),
          h('div', { class: 'm' }, d.message || 'Your message appears here.')),
        replies.length && d.display !== 'fullscreen' ? h('div', { class: 'replies' }, buttons) : null,
        h('div', { class: 'foot' }, h('span', null, 'From ' + (S.me.display_name || S.me.username) + ' at ' + clock(new Date())),
          h('div', { class: 'btns' }, d.link.trim() ? h('span', { class: 'b ghost' }, 'Open link') : null,
            replies.length && d.display !== 'fullscreen' ? null : buttons)));
      if (d.display === 'fullscreen') pv.style.background = shade(accent, 0.55);
      else pv.querySelector('.band').style.background = accent;
      if (d.display !== 'fullscreen' && icon) icon.style.color = accent;
      preview.appendChild(pv);
      clear(notes);
      var n = [];
      n.push({ corner: 'Slides in at the top-right corner, above every other window, without interrupting typing.',
        center: 'Appears in the middle of the screen, above every other window.',
        fullscreen: 'Covers every monitor until it is closed. Use it for emergencies and \u201ceyes to the front\u201d moments.' }[d.display]);
      var nReplies = d.responses.filter(function (r) { return r.trim(); }).length;
      if (nReplies) n.push('Stays up until each person picks a reply. You will see who chose what, as it happens.');
      else if (d.require_ack) n.push('Cannot be closed except with the \u201cI\u2019ve read this\u201d button, and you will see exactly who pressed it. If someone logs off without pressing it, it comes back next time they log on.');
      else if (d.display_seconds) n.push('Closes itself after ' + (d.display_seconds < 60 ? d.display_seconds + ' seconds' : (d.display_seconds / 60) + ' min') + ' if nobody closes it first.');
      if (d.sound || d.sound_id) {
        var snd = S.sounds.filter(function (x) { return x.id === d.sound_id; })[0];
        n.push((snd ? 'Plays \u201c' + snd.name + '\u201d' : 'Plays the Windows alert sound') + (d.sound_loop ? ' over and over until it is closed' : '') + ' (only audible if the PC has speakers or headphones and is not muted).');
      }
      if (d.when === 'later') n.push(d.once || d.days.length ? 'Will be sent ' + describeSchedule(d.once ? { once: d.once } : { days: d.days, time: d.time }).replace(/^Once, /, 'on ') + '.' : 'Choose when to send it.');
      n.forEach(function (t) { notes.appendChild(h('li', null, t)); });
    }

    function changed(textOnly) {
      counter.textContent = message.value.length + ' / 2000';
      var asks = d.responses.some(function (r) { return r.trim(); });
      autoclose.disabled = d.require_ack || asks;
      ack.disabled = asks; if (asks) ack.checked = true;
      sendBtn.textContent = d.schedule_id ? 'Save schedule' : d.when === 'later' ? 'Schedule alert' : 'Send alert';
      drawPreview();
      if (textOnly) return;
      clearTimeout(reachTimer);
      reachTimer = setTimeout(askReach, 300);
    }
    function askReach() {
      var t = buildTarget(d);
      if (!t.all && !t.group_ids.length && !t.rules.length) {
        lastReach = null; reach.className = 'reach zero';
        reach.textContent = d.mode === 'groups' ? 'Tick at least one group.' : 'Type a name or pattern to see who it matches.';
        return;
      }
      api('POST', '/api/reach', t).then(function (r) {
        lastReach = r;
        reach.className = 'reach' + (r.online ? '' : ' zero');
        reach.textContent = r.online
          ? 'Reaches ' + plural(r.online, 'computer', 'computers') + ' that ' + (r.online === 1 ? 'is' : 'are') + ' on right now' + (r.known > r.online ? ' (' + (r.known - r.online) + ' more are off or logged out)' : '') + '.'
          : (r.known ? 'None of the ' + plural(r.known, 'matching computer is', 'matching computers are') + ' on right now.' : 'This does not match any computer Foghorn has seen.');
        reach.title = r.sample.length ? 'Including: ' + r.sample.join(', ') : '';
      }).catch(function () { reach.textContent = ''; });
    }

    function send() {
      err.textContent = '';
      var spec = specFromDraft(d);
      if (!spec.title) { err.textContent = 'Give the alert a title.'; title.focus(); return; }
      if (d.when === 'later' || d.schedule_id) { schedule(spec); return; }
      var audience = spec.target.all ? 'every computer' : (lastReach ? plural(lastReach.online, 'computer', 'computers') : 'the chosen computers');
      var body = [h('p', null, h('b', null, spec.title)), spec.message ? h('p', { class: 'quote ' + spec.level }, spec.message) : null,
        h('p', null, 'This will pop up on ', h('b', null, audience), lastReach && spec.target.all ? ' (' + lastReach.online + ' on right now)' : '', ' straight away.'),
        spec.display === 'fullscreen' ? h('p', { class: 'notice warn' }, 'Full screen covers everything people are working on. Use it when that is what you mean.') : null];
      confirmDialog('Send this alert?', body, 'Send alert').then(function (yes) {
        if (!yes) return;
        sendBtn.disabled = true;
        api('POST', '/api/alerts', spec).then(function (r) {
          toast('Sent. ' + plural(r.online_reach, 'computer is', 'computers are') + ' showing it now.');
          draft = freshDraft();
          location.hash = '#/alerts/' + r.alert.id;
        }).catch(function (e) { err.textContent = e.message; sendBtn.disabled = false; });
      });
    }
    function schedule(spec) {
      var body = { name: d.schedule_name || spec.title, spec: spec, enabled: true };
      if (d.once) body.once = d.once; else { body.days = d.days.slice(); body.time = d.time; }
      if (!d.once && !d.days.length) { err.textContent = 'Pick a date and time, or at least one day of the week.'; return; }
      sendBtn.disabled = true;
      (d.schedule_id ? api('PUT', '/api/schedules/' + d.schedule_id, body) : api('POST', '/api/schedules', body)).then(function (sc) {
        toast('Scheduled: ' + describeSchedule(sc) + '. Next: ' + (sc.next_run && sc.next_run.slice(0, 4) !== '0001' ? until(sc.next_run) : 'not scheduled'));
        draft = freshDraft();
        location.hash = '#/schedules';
      }).catch(function (e) { err.textContent = e.message; sendBtn.disabled = false; });
    }
    function saveTemplate() {
      var name = h('input', { type: 'text', maxlength: 60, value: d.title });
      var e2 = h('p', { class: 'error' });
      var dlg = dialog('Save as a template', [h('label', { class: 'field' }, h('span', null, 'Template name'), name,
        h('small', null, 'Templates keep the wording and appearance. You always choose who receives it when you send.')), e2], [
        h('button', { class: 'btn', onclick: function () { dlg.close(); } }, 'Cancel'),
        h('button', { class: 'btn primary', onclick: function () {
          api('POST', '/api/templates', { name: name.value, spec: specFromDraft(d) }).then(function () {
            dlg.close(); toast('Template saved'); return loadShared();
          }).then(render).catch(function (x) { e2.textContent = x.message; });
        } }, 'Save template')]);
    }
    function useTemplate(t) {
      var keepWhen = { when: d.when, once: d.once, days: d.days, time: d.time, schedule_id: d.schedule_id, schedule_name: d.schedule_name };
      draft = Object.assign(draftFromSpec(t.spec, true), keepWhen); render();
    }

    var sendBtn = h('button', { class: 'btn primary big', onclick: send }, 'Send alert');
    var modes = restricted() ? null : radioSeg('mode', [['all', 'Everyone'], ['groups', 'Groups'], ['custom', 'Specific computers or people']], 'mode');
    if (modes) modes.addEventListener('change', drawWho);

    main.appendChild(h('div', { class: 'page-head' }, h('div', null, h('h1', null, d.schedule_id ? 'Edit scheduled alert' : 'Send an alert'),
      h('p', null, 'Design it on the left and watch it take shape on the right. It appears on top of whatever people are doing, within a second or two.'))));

    var tpl = h('div', { class: 'templates' }, h('span', { class: 'muted small' }, 'Start from a template:'),
      S.templates.map(function (t) {
        return h('span', { class: 'chip' }, h('button', { onclick: function () { useTemplate(t); } }, t.name),
          h('button', { class: 'x', title: 'Delete this template', 'aria-label': 'Delete template ' + t.name, onclick: function () {
            confirmDialog('Delete the \u201c' + t.name + '\u201d template?', h('p', null, 'Alerts already sent from it are not affected.'), 'Delete template', true).then(function (y) {
              if (y) api('DELETE', '/api/templates/' + t.id).then(loadShared).then(render).catch(fail);
            });
          } }, '\u00d7'));
      }));

    // --- look ---
    var swatches = h('div', { class: 'swatches' });
    function drawSwatches() {
      clear(swatches);
      [['', 'As the level'], ['#1d6fb8', 'Blue'], ['#0f7b6c', 'Teal'], ['#24794b', 'Green'], ['#c77700', 'Amber'], ['#c3301c', 'Red'], ['#6a3fb5', 'Purple'], ['#b5306e', 'Pink'], ['#33485a', 'Slate']]
        .forEach(function (c) {
          var b = h('button', { type: 'button', class: 'sw' + (d.accent === c[0] ? ' on' : '') + (c[0] ? '' : ' auto'), title: c[1], 'aria-label': 'Colour: ' + c[1], 'aria-pressed': d.accent === c[0] ? 'true' : 'false',
            onclick: function () { d.accent = c[0]; drawSwatches(); changed(true); } }, c[0] ? '' : 'Auto');
          if (c[0]) b.style.background = c[0];
          swatches.appendChild(b);
        });
      var custom = h('input', { type: 'color', 'aria-label': 'Any colour', value: d.accent || LEVEL_COLOURS[d.level], oninput: function () { d.accent = custom.value; changed(true); }, onchange: drawSwatches });
      swatches.appendChild(h('label', { class: 'sw-custom', title: 'Any colour' }, custom));
    }
    drawSwatches();
    var icons = h('div', { class: 'icons', role: 'radiogroup', 'aria-label': 'Icon' });
    function drawIcons() {
      clear(icons);
      icons.appendChild(h('button', { type: 'button', class: 'icn' + (d.icon ? '' : ' on'), 'aria-pressed': d.icon ? 'false' : 'true', onclick: function () { d.icon = ''; drawIcons(); changed(true); } }, 'None'));
      Object.keys(S.meta.icons || {}).forEach(function (k) {
        icons.appendChild(h('button', { type: 'button', class: 'icn' + (d.icon === k ? ' on' : ''), title: k, 'aria-label': 'Icon: ' + k, 'aria-pressed': d.icon === k ? 'true' : 'false',
          onclick: function () { d.icon = k; drawIcons(); changed(true); } }, S.meta.icons[k]));
      });
    }
    drawIcons();
    var label = h('input', { type: 'text', maxlength: 24, value: d.label, placeholder: LEVELS[d.level], oninput: function () { d.label = label.value; changed(true); } });
    var dismissLabel = h('input', { type: 'text', maxlength: 30, value: d.dismiss_label, placeholder: 'Dismiss', oninput: function () { d.dismiss_label = dismissLabel.value; changed(true); } });

    // --- replies ---
    var replyBox = h('div', { class: 'stack' });
    function drawReplies() {
      clear(replyBox);
      d.responses.forEach(function (r, i) {
        var inp = h('input', { type: 'text', maxlength: 30, value: r, 'aria-label': 'Reply ' + (i + 1), placeholder: ['I\u2019m safe', 'I need help', 'Not sure', 'Other'][i], oninput: function () { d.responses[i] = inp.value; changed(); } });
        replyBox.appendChild(h('div', { class: 'reply-row' }, inp, h('button', { type: 'button', class: 'btn sm', 'aria-label': 'Remove reply ' + (i + 1), onclick: function () { d.responses.splice(i, 1); drawReplies(); changed(); } }, 'Remove')));
      });
      if (d.responses.length < 4) replyBox.appendChild(h('div', { class: 'row' },
        h('button', { type: 'button', class: 'linkbtn', onclick: function () { d.responses.push(''); drawReplies(); changed(); replyBox.querySelectorAll('input')[d.responses.length - 1].focus(); } }, d.responses.length ? 'Add another reply' : 'Ask a question with reply buttons'),
        d.responses.length ? null : h('span', { class: 'muted small' }, 'e.g. \u201cI\u2019m safe\u201d / \u201cI need help\u201d. You see who picked what.')));
    }
    drawReplies();

    // --- sound ---
    var soundSel = h('select', { 'aria-label': 'Which sound', onchange: function () { d.sound_id = soundSel.value; changed(true); } },
      h('option', { value: '', selected: !d.sound_id }, 'The Windows alert sound'),
      S.sounds.map(function (x) { return h('option', { value: x.id, selected: d.sound_id === x.id }, x.name); }));
    var loop = h('input', { type: 'checkbox', checked: d.sound_loop, onchange: function () { d.sound_loop = loop.checked; changed(true); } });
    var player = null;
    var playBtn = h('button', { type: 'button', class: 'btn sm', onclick: function () {
      if (player) { player.pause(); player = null; playBtn.textContent = 'Play'; return; }
      if (!d.sound_id) { toast('The Windows sound plays on the PCs; it cannot be previewed here.'); return; }
      player = new Audio('/api/sounds/' + d.sound_id + '/file');
      player.onended = function () { player = null; playBtn.textContent = 'Play'; };
      player.play().then(function () { playBtn.textContent = 'Stop'; }).catch(function () { player = null; toast('This browser could not play it.', true); });
    } }, 'Play');
    var soundOpts = h('div', { class: 'stack sound-opts' + (d.sound || d.sound_id ? '' : ' hidden') },
      h('div', { class: 'row' }, soundSel, playBtn),
      h('label', { class: 'check' }, loop, h('span', null, 'Keep playing until it is closed', h('small', null, 'For alarms. Stops the moment the person closes or answers it.'))),
      isAdmin() ? h('small', { class: 'hint' }, h('a', { href: '#/sounds' }, 'Upload your own sounds')) : null);
    soundSel.style.maxWidth = '320px';
    sound.addEventListener('change', function () { soundOpts.classList.toggle('hidden', !sound.checked); if (!sound.checked) { d.sound_id = ''; soundSel.value = ''; } });

    // --- when ---
    var whenBody = h('div', { class: 'stack' });
    function drawWhen() {
      clear(whenBody);
      if (d.when !== 'later') return;
      var kind = d.once ? 'once' : 'repeat';
      var onceIn = h('input', { type: 'datetime-local', value: d.once || '', onchange: function () { d.once = onceIn.value; changed(true); } });
      var timeIn = h('input', { type: 'time', value: d.time, onchange: function () { d.time = timeIn.value; changed(true); } });
      var dayBoxes = h('div', { class: 'days' }, [1, 2, 3, 4, 5, 6, 0].map(function (n) {
        var cb = h('input', { type: 'checkbox', checked: d.days.indexOf(n) >= 0, onchange: function () {
          d.days = d.days.filter(function (x) { return x !== n; }); if (cb.checked) d.days.push(n); changed(true);
        } });
        return h('label', { class: 'day' }, cb, h('span', null, DAY_NAMES[n]));
      }));
      var name = h('input', { type: 'text', maxlength: 60, value: d.schedule_name, placeholder: d.title || 'e.g. Home time', oninput: function () { d.schedule_name = name.value; } });
      whenBody.appendChild(h('div', { class: 'seg' },
        h('label', null, h('input', { type: 'radio', name: 'kind', checked: kind === 'once', onchange: function () { d.once = d.once || tomorrowAt('09:00'); drawWhen(); changed(true); } }), 'Once'),
        h('label', null, h('input', { type: 'radio', name: 'kind', checked: kind === 'repeat', onchange: function () { d.once = ''; drawWhen(); changed(true); } }), 'Every week')));
      if (kind === 'once') whenBody.appendChild(h('label', { class: 'field' }, h('span', null, 'Date and time'), onceIn));
      else whenBody.appendChild(h('div', { class: 'two' }, h('div', { class: 'field' }, h('span', null, 'On'), dayBoxes), h('label', { class: 'field' }, h('span', null, 'At'), timeIn)));
      whenBody.appendChild(h('label', { class: 'field' }, h('span', null, 'Name for the schedule (optional)'), name));
      whenBody.appendChild(h('small', { class: 'hint' }, 'Uses the server\u2019s clock. It sends as you, to the people chosen above.'));
    }
    function tomorrowAt(hm) {
      var t = new Date(); t.setDate(t.getDate() + 1);
      return t.getFullYear() + '-' + pad(t.getMonth() + 1) + '-' + pad(t.getDate()) + 'T' + hm;
    }
    var whenSeg = d.schedule_id ? null : radioSeg('when', [['now', 'Send now'], ['later', 'Schedule for later']], 'when');
    if (whenSeg) whenSeg.addEventListener('change', drawWhen);
    drawWhen();

    var form = h('div', null,
      d.schedule_id ? h('p', { class: 'notice' }, 'Editing a scheduled alert. ', h('button', { class: 'linkbtn', onclick: function () { draft = freshDraft(); location.hash = '#/schedules'; } }, 'Cancel')) : null,
      S.templates.length ? tpl : null,
      h('div', { class: 'panel stack' },
        h('label', { class: 'field' }, h('span', null, 'Title'), title),
        h('label', { class: 'field' }, h('span', null, 'Message', counter), message),
        h('fieldset', null, h('span', { class: 'legend' }, 'How serious is it?'), radioSeg('level', [['info', 'Notice'], ['warning', 'Warning'], ['critical', 'Urgent']], 'level', 'lv-')),
        h('fieldset', null, h('span', { class: 'legend' }, 'Where it appears'), radioSeg('display', [['corner', 'Corner', 'routine'], ['center', 'Centre', 'hard to miss'], ['fullscreen', 'Full screen', 'covers everything']], 'display'))),
      h('div', { class: 'panel stack' },
        h('h2', null, 'Look and buttons'),
        h('div', { class: 'field' }, h('span', null, 'Icon'), icons),
        h('div', { class: 'field' }, h('span', null, 'Colour'), swatches),
        h('div', { class: 'two' },
          h('label', { class: 'field' }, h('span', null, 'Label in the coloured band'), label),
          h('label', { class: 'field' }, h('span', null, 'Close button wording'), dismissLabel)),
        h('div', { class: 'field' }, h('span', null, 'Reply buttons'), replyBox)),
      h('div', { class: 'panel stack' },
        h('h2', null, 'Who gets it'),
        restricted() ? h('p', { class: 'muted' }, 'Your account can send to these groups.') : modes,
        whoBody),
      h('div', { class: 'panel stack' },
        h('h2', null, 'Options'),
        h('label', { class: 'check' }, ack, h('span', null, 'Make people confirm they have read it', h('small', null, 'Replaces Dismiss with an \u201cI\u2019ve read this\u201d button and records who pressed it.'))),
        h('label', { class: 'check' }, sound, h('span', null, 'Play a sound')),
        soundOpts,
        h('div', { class: 'two' },
          h('label', { class: 'field' }, h('span', null, 'How long it stays on screen'), autoclose),
          h('label', { class: 'field' }, h('span', null, 'Also show it to people who log on later?'), expires)),
        h('label', { class: 'field' }, h('span', null, 'Link (optional)'), link, h('small', null, 'Adds an \u201cOpen link\u201d button that opens this address in the person\u2019s browser.'))),
      h('div', { class: 'panel stack' }, h('h2', null, 'When'), whenSeg, whenBody),
      err,
      h('div', { class: 'sendbar' }, sendBtn, h('button', { class: 'btn', onclick: saveTemplate }, 'Save as a template'),
        h('button', { class: 'linkbtn', onclick: function () { draft = freshDraft(); render(); } }, 'Clear the form')));

    main.appendChild(h('div', { class: 'send-grid' }, form,
      h('div', { class: 'preview-col' }, h('h2', null, 'What people will see'), preview, notes)));
    drawWho(); changed();
  }

  // ---------- sent alerts ----------
  function totalsText(a) {
    var t = a.totals, s = plural(t.displayed, 'computer', 'computers') + ' showed it';
    if (a.spec.require_ack) s += ', ' + t.acked + ' confirmed';
    return s;
  }
  function statusPill(a) {
    return h('span', { class: 'pill ' + a.status }, { active: 'Still delivering', finished: 'Finished', recalled: 'Recalled' }[a.status]);
  }
  function recall(a) {
    return confirmDialog('Recall \u201c' + a.spec.title + '\u201d?', h('p', null, 'It disappears from every screen still showing it and stops being delivered. People who have already read it have still read it.'), 'Recall alert', true)
      .then(function (y) { return y ? api('POST', '/api/alerts/' + a.id + '/recall').then(function () { toast('Recalled'); return true; }) : false; });
  }
  function alertsView(main) {
    main.appendChild(h('div', { class: 'page-head' }, h('div', null, h('h1', null, 'Sent alerts'), h('p', null, 'The most recent alerts, who sent them and how many screens they reached.'))));
    var wrap = h('div', { class: 'tablewrap' }); main.appendChild(wrap);
    function load() {
      api('GET', '/api/alerts?limit=150').then(function (list) {
        clear(wrap);
        if (!list.length) { wrap.appendChild(h('div', { class: 'empty' }, h('b', null, 'Nothing has been sent yet'), h('a', { href: '#/send' }, 'Send your first alert'))); return; }
        wrap.appendChild(h('table', null,
          h('thead', null, h('tr', null, h('th', null, 'Alert'), h('th', null, 'Sent to'), h('th', null, 'By'), h('th', null, 'When'), h('th', null, 'Result'), h('th'))),
          h('tbody', null, list.map(function (a) {
            return h('tr', { class: 'click', onclick: function (e) { if (e.target.tagName !== 'BUTTON') location.hash = '#/alerts/' + a.id; } },
              h('td', null, h('span', { class: 'dot ' + a.spec.level }), h('a', { href: '#/alerts/' + a.id }, a.spec.title)),
              h('td', null, a.target_summary), h('td', null, a.sender_name), h('td', { class: 'nowrap' }, when(a.created_at)),
              h('td', null, statusPill(a), ' ', h('span', { class: 'muted small' }, totalsText(a))),
              h('td', null, a.status === 'active' ? h('button', { class: 'btn sm danger', onclick: function () { recall(a).then(function (y) { if (y) load(); }).catch(fail); } }, 'Recall') : null));
          }))));
      }).catch(fail);
    }
    load(); every(5000, load);
  }

  function alertDetailView(main, id) {
    var box = h('div'); main.appendChild(box);
    function load() {
      api('GET', '/api/alerts/' + id).then(function (a) {
        clear(box);
        var t = a.totals, waiting = a.spec.require_ack ? t.displayed - t.acked : 0;
        box.appendChild(h('p', { class: 'small' }, h('a', { href: '#/alerts' }, '\u2190 All sent alerts')));
        box.appendChild(h('div', { class: 'page-head' }, h('div', null, h('h1', null, a.spec.title), h('p', null, statusPill(a), ' ', LEVELS[a.spec.level] + ' to ' + a.target_summary)),
          h('span', { class: 'spacer' }),
          h('button', { class: 'btn', onclick: function () { draft = draftFromSpec(a.spec, false); location.hash = '#/send'; } }, 'Send again\u2026'),
          a.status === 'active' ? h('button', { class: 'btn danger', onclick: function () { recall(a).then(function (y) { if (y) load(); }).catch(fail); } }, 'Recall') : null));
        box.appendChild(h('div', { class: 'panel' },
          a.spec.message ? h('p', { class: 'quote ' + a.spec.level }, a.spec.message) : h('p', { class: 'muted' }, 'No message text, just the title.'),
          h('dl', { class: 'dl' },
            h('dt', null, 'Sent by'), h('dd', null, a.sender_name + ', ' + when(a.created_at)),
            h('dt', null, 'Appearance'), h('dd', null, { corner: 'Corner pop-up', center: 'Centre of the screen', fullscreen: 'Full screen' }[a.spec.display] + (a.spec.sound ? ', with sound' : '') + (a.spec.display_seconds ? ', closes after ' + a.spec.display_seconds + ' s' : '')),
            h('dt', null, 'Delivered until'), h('dd', null, a.recalled_at ? 'Recalled ' + when(a.recalled_at) + ' by ' + a.recalled_by : (new Date(a.expires_at) > new Date() ? 'Still reaching new log-ons until ' + until(a.expires_at) : 'Finished ' + when(a.expires_at))),
            a.spec.link ? [h('dt', null, 'Link'), h('dd', null, a.spec.link)] : null)));
        var tally = t.responses || null;
        if (tally && Object.keys(tally).length) {
          var most = Math.max.apply(null, [1].concat(Object.keys(tally).map(function (k) { return tally[k]; })));
          box.appendChild(h('div', { class: 'panel' }, h('h2', null, 'Replies'),
            h('div', { class: 'tally' }, (a.spec.responses || Object.keys(tally)).map(function (k) {
              var bar = h('span', { class: 'bar' }); bar.style.width = (100 * (tally[k] || 0) / most) + '%';
              return h('div', { class: 'trow' }, h('span', { class: 'tk' }, k), h('span', { class: 'track' }, bar), h('b', null, tally[k] || 0));
            })),
            h('p', { class: 'muted small' }, (t.displayed - t.acked) + ' still to answer.')));
        }
        box.appendChild(h('div', { class: 'stats' },
          h('div', { class: 'stat' }, h('b', null, t.sent), h('span', null, 'computers it was sent to')),
          h('div', { class: 'stat' }, h('b', null, t.displayed), h('span', null, 'showed it on screen')),
          a.spec.require_ack ? h('div', { class: 'stat' }, h('b', null, t.acked), h('span', null, 'people confirmed')) : null,
          a.spec.require_ack ? h('div', { class: 'stat' }, h('b', null, waiting), h('span', null, 'still to confirm')) : null));
        if (a.archived) { box.appendChild(h('p', { class: 'notice' }, 'This alert is old enough that the computer-by-computer list has been tidied away. The totals are kept.')); return; }
        var rows = a.deliveries || [];
        var asks = (a.spec.responses || []).length > 0;
        var tw = h('div', { class: 'tablewrap' });
        if (!rows.length) tw.appendChild(h('div', { class: 'empty' }, h('b', null, 'No computer has picked this up yet'), a.status === 'active' ? 'Matching computers receive it as soon as someone logs on.' : 'No matching computer was on while it was being delivered.'));
        else tw.appendChild(h('table', null,
          h('thead', null, h('tr', null, h('th', null, 'Computer'), h('th', null, 'Logged-on user'), h('th', null, 'Shown'), h('th', null, a.spec.require_ack ? 'Confirmed' : 'Closed'), asks ? h('th', null, 'Reply') : null)),
          h('tbody', null, rows.map(function (d) {
            var closed = a.spec.require_ack ? (d.acked_at ? clock(d.acked_at) : 'Not yet')
              : (d.closed_at ? clock(d.closed_at) + ({ timeout: ' (closed itself)', recalled: ' (recalled)', dismissed: '' }[d.close_reason] || '') : (d.displayed_at ? 'Still on screen' : ''));
            return h('tr', null, h('td', null, d.hostname), h('td', null, d.user),
              h('td', null, d.displayed_at ? clock(d.displayed_at) : 'Sent, waiting for the PC to confirm'), h('td', null, closed),
              asks ? h('td', null, d.response ? h('b', null, d.response) : h('span', { class: 'muted' }, '\u2013')) : null);
          }))));
        box.appendChild(tw);
      }).catch(function (e) { clear(box).appendChild(h('p', { class: 'notice warn' }, e.message, ' ', h('a', { href: '#/alerts' }, 'Back to sent alerts'))); stopTimers(); });
    }
    load(); every(4000, load);
  }

  // ---------- computers ----------
  function computersView(main) {
    var q = h('input', { type: 'search', placeholder: 'Filter by computer, user, OU or IP', 'aria-label': 'Filter computers', oninput: draw });
    var onlyOn = h('input', { type: 'checkbox', onchange: draw });
    var summary = h('p', { class: 'muted' });
    var wrap = h('div', { class: 'tablewrap' });
    var list = [];
    q.style.maxWidth = '340px';
    main.appendChild(h('div', { class: 'page-head' }, h('div', null, h('h1', null, 'Computers'), summary)));
    main.appendChild(h('div', { class: 'row' }, q, h('label', { class: 'check' }, onlyOn, h('span', null, 'Only show computers that are on'))));
    main.appendChild(h('div', { class: 'stack' }, h('span'), wrap));
    function draw() {
      var needle = q.value.trim().toLowerCase();
      var rows = list.filter(function (c) {
        if (onlyOn.checked && !c.online) return false;
        return !needle || [c.hostname, c.user, c.domain, c.ou, c.ip].join(' ').toLowerCase().indexOf(needle) >= 0;
      });
      var on = list.filter(function (c) { return c.online; }).length;
      summary.textContent = list.length ? on + ' on right now, ' + list.length + ' seen in the last 45 days. A computer appears here once someone logs on to it with the Foghorn client installed.' : '';
      clear(wrap);
      if (!list.length) { wrap.appendChild(h('div', { class: 'empty' }, h('b', null, 'No computers have checked in yet'), 'Install the Foghorn client on a PC and log on to it. It appears here within a few seconds. The admin guide covers rolling it out with Group Policy.')); return; }
      if (!rows.length) { wrap.appendChild(h('div', { class: 'empty' }, h('b', null, 'Nothing matches that filter'))); return; }
      wrap.appendChild(h('table', null,
        h('thead', null, h('tr', null, h('th', null, 'Computer'), h('th', null, 'Logged-on user'), h('th', null, 'OU'), h('th', null, 'IP address'), h('th', null, 'Last seen'), h('th', null, 'Client'), h('th'))),
        h('tbody', null, rows.slice(0, 1500).map(function (c) {
          return h('tr', null,
            h('td', { class: 'nowrap' }, h('span', { class: 'dot' + (c.online ? ' on' : ''), title: c.online ? 'On' : 'Off or logged out' }), c.hostname),
            h('td', { title: (c.groups || []).join('\n') }, (c.domain ? c.domain + '\\' : '') + c.user),
            h('td', { title: c.ou }, ouPath(c.ou)), h('td', { class: 'mono' }, c.ip),
            h('td', { class: 'nowrap' }, c.online ? 'On now' : when(c.last_seen)), h('td', null, c.version),
            h('td', { class: 'nowrap' },
              restricted() ? null : h('button', { class: 'btn sm', onclick: function () { draft.mode = 'custom'; draft.field = 'hostname'; draft.patterns = c.hostname; location.hash = '#/send'; } }, 'Send alert'), ' ',
              isAdmin() && !c.online ? h('button', { class: 'btn sm', title: 'Remove from this list. It reappears if the computer checks in again.', onclick: function () { api('DELETE', '/api/clients/' + c.id).then(load).catch(fail); } }, 'Forget') : null));
        }))));
    }
    function load() { api('GET', '/api/clients').then(function (r) { list = r; draw(); }).catch(fail); }
    load(); every(8000, load);
  }

  // ---------- groups ----------
  function groupsView(main) {
    main.appendChild(h('div', { class: 'page-head' }, h('div', null, h('h1', null, 'Groups'),
      h('p', null, 'A group is a saved set of computers \u2013 usually a room, a department or a year group \u2013 so nobody has to remember naming patterns when they send.')),
      h('span', { class: 'spacer' }), isAdmin() ? h('button', { class: 'btn primary', onclick: function () { editGroup(null); } }, 'New group') : null));
    var box = h('div', { class: 'cards' }); main.appendChild(box);
    function draw() {
      clear(box);
      if (!S.groups.length) { box.appendChild(h('div', { class: 'panel empty' }, h('b', null, 'No groups yet'), isAdmin() ? 'Create one for each room, e.g. \u201cLab 1\u201d matching computer names LAB1-*.' : 'An administrator can create them.')); return; }
      S.groups.forEach(function (g) {
        box.appendChild(h('div', { class: 'card' }, h('h3', null, g.name),
          h('div', { class: 'muted small' }, g.online + ' on now, ' + g.known + ' known'),
          h('ul', null, g.rules.map(function (r) { return h('li', null, FIELDS[r.field].replace(' (of the user)', '') + ': ', h('span', { class: 'mono' }, r.pattern)); })),
          isAdmin() ? h('div', { class: 'row' }, h('button', { class: 'btn sm', onclick: function () { editGroup(g); } }, 'Edit'),
            h('button', { class: 'btn sm danger', onclick: function () {
              confirmDialog('Delete the group \u201c' + g.name + '\u201d?', h('p', null, 'No computers are affected. Anyone limited to this group loses access to it.'), 'Delete group', true)
                .then(function (y) { if (y) return api('DELETE', '/api/groups/' + g.id).then(refresh); }).catch(fail);
            } }, 'Delete')) : null));
      });
    }
    function refresh() { return loadShared().then(draw); }
    function editGroup(g) {
      var name = h('input', { type: 'text', maxlength: 60, value: g ? g.name : '' });
      var rules = (g ? g.rules : [{ field: 'hostname', pattern: '' }]).map(function (r) { return { field: r.field, pattern: r.pattern }; });
      var rows = h('div', { class: 'stack' }), reach = h('div', { class: 'reach' }), err = h('p', { class: 'error' }), timer;
      function ask() {
        clearTimeout(timer);
        timer = setTimeout(function () {
          api('POST', '/api/reach', { all: false, group_ids: [], rules: rules }).then(function (r) {
            reach.textContent = 'Matches ' + plural(r.known, 'computer', 'computers') + ' Foghorn has seen (' + r.online + ' on now)' + (r.sample.length ? ': ' + r.sample.join(', ') + (r.online > r.sample.length ? '\u2026' : '') : '.');
          }).catch(function () {});
        }, 300);
      }
      function drawRows() {
        clear(rows);
        rules.forEach(function (r, i) {
          var f = h('select', { 'aria-label': 'Match on', onchange: function () { r.field = f.value; hint.textContent = FIELD_HINTS[r.field]; ask(); } },
            Object.keys(FIELDS).map(function (k) { return h('option', { value: k, selected: r.field === k }, FIELDS[k]); }));
          var p = h('input', { type: 'text', 'aria-label': 'Pattern', value: r.pattern, placeholder: 'LAB1-*', oninput: function () { r.pattern = p.value; ask(); } });
          var hint = h('small', { class: 'hint' }, FIELD_HINTS[r.field]);
          rows.appendChild(h('div', null, h('div', { class: 'rule-row' }, f, p,
            rules.length > 1 ? h('button', { class: 'btn sm', 'aria-label': 'Remove this rule', onclick: function () { rules.splice(i, 1); drawRows(); ask(); } }, 'Remove') : h('span')), hint));
        });
      }
      drawRows(); ask();
      var dlg = dialog(g ? 'Edit group' : 'New group', [
        h('div', { class: 'stack' }, h('label', { class: 'field' }, h('span', null, 'Name'), name),
          h('div', null, h('span', { class: 'legend' }, 'A computer belongs to this group if it matches any of these'), rows),
          h('button', { class: 'linkbtn', onclick: function () { rules.push({ field: 'hostname', pattern: '' }); drawRows(); } }, 'Add another rule'),
          reach, err)], [
        h('button', { class: 'btn', onclick: function () { dlg.close(); } }, 'Cancel'),
        h('button', { class: 'btn primary', onclick: function () {
          var body = { name: name.value, rules: rules };
          (g ? api('PUT', '/api/groups/' + g.id, body) : api('POST', '/api/groups', body))
            .then(function () { dlg.close(); toast('Group saved'); return refresh(); }).catch(function (e) { err.textContent = e.message; });
        } }, 'Save group')]);
    }
    refresh().catch(fail);
  }

  // ---------- settings ----------
  function settingsView(main) {
    main.appendChild(h('div', { class: 'page-head' }, h('h1', null, 'Settings')));
    var box = h('div'); main.appendChild(box);
    function load() {
      Promise.all([api('GET', '/api/settings'), api('GET', '/api/users')]).then(function (r) { draw(r[0], r[1]); }).catch(fail);
    }
    function draw(st, users) {
      clear(box);
      // organisation
      var org = h('input', { type: 'text', maxlength: 80, value: st.org_name, placeholder: 'e.g. Northbrook College' });
      box.appendChild(h('div', { class: 'panel' }, h('h2', null, 'Organisation name'), h('p', null, 'Shown at the top of every alert so people know it is genuine.'),
        h('div', { class: 'row' }, org, h('button', { class: 'btn primary', onclick: function () {
          api('PUT', '/api/settings', { org_name: org.value }).then(function () { S.org = org.value.trim(); toast('Saved'); render(); }).catch(fail);
        } }, 'Save name'))));
      org.style.maxWidth = '360px';

      // client key
      var shown = false, keyEl = h('code', { class: 'secret' });
      function drawKey() { keyEl.textContent = shown ? st.client_key : st.client_key.slice(0, 6) + '\u2026 (hidden)'; }
      drawKey();
      box.appendChild(h('div', { class: 'panel' }, h('h2', null, 'Client key'),
        h('p', null, 'The desktop clients present this key when they check in. You set it once, in Group Policy or the installer. It lets a PC receive alerts; it cannot be used to send them.'),
        keyEl, h('div', { class: 'row' }, h('span'),
          h('button', { class: 'btn', onclick: function () { shown = !shown; drawKey(); } }, 'Show or hide'),
          h('button', { class: 'btn', onclick: function () { copy(st.client_key); } }, 'Copy'),
          h('button', { class: 'btn danger', onclick: function () {
            confirmDialog('Replace the client key?', h('p', null, 'Every PC stops receiving alerts until it is given the new key. Only do this if the key has leaked, and be ready to update your Group Policy straight afterwards.'), 'Replace key', true)
              .then(function (y) { if (y) return api('POST', '/api/settings/rotate-client-key').then(function () { toast('Key replaced. Update your clients.'); load(); }); }).catch(fail);
          } }, 'Replace key\u2026'))));
      box.lastChild.querySelector('.row').style.marginTop = '12px';

      // people
      var tw = h('div', { class: 'tablewrap' }, h('table', null,
        h('thead', null, h('tr', null, h('th', null, 'Person'), h('th', null, 'Username'), h('th', null, 'Can'), h('th', null, 'Last signed in'), h('th'))),
        h('tbody', null, users.map(function (u) {
          var can = u.role === 'admin' ? 'Everything' : (u.allowed_group_ids.length ? 'Send to ' + plural(u.allowed_group_ids.length, 'group', 'groups') : 'Send to anyone') + (u.can_lock ? ', lock screens' : '');
          return h('tr', null, h('td', null, u.display_name || u.username, u.disabled ? h('span', { class: 'pill recalled' }, 'Disabled') : null),
            h('td', null, u.username), h('td', null, can), h('td', null, when(u.last_login)),
            h('td', null, h('button', { class: 'btn sm', onclick: function () { editUser(u); } }, 'Edit')));
        }))));
      box.appendChild(h('div', { class: 'panel' }, h('div', { class: 'row' }, h('h2', null, 'People who can sign in'), h('span', { class: 'spacer' }),
        h('button', { class: 'btn primary', onclick: function () { editUser(null); } }, 'Add a person')),
        h('p', null, 'Administrators manage everything. Senders can only send alerts, and you can limit a sender to particular groups \u2013 a tutor to their own room, say.'), tw));

      // tokens
      var tt = st.tokens.length ? h('div', { class: 'tablewrap' }, h('table', null,
        h('thead', null, h('tr', null, h('th', null, 'Name'), h('th', null, 'Can'), h('th', null, 'Created'), h('th', null, 'Last used'), h('th'))),
        h('tbody', null, st.tokens.map(function (t) {
          return h('tr', null, h('td', null, t.name), h('td', null, (t.scopes || []).map(function (sc) { return h('span', { class: 'pill finished' }, sc); })),
            h('td', null, when(t.created_at) + ' by ' + t.created_by), h('td', null, when(t.last_used)),
            h('td', null, h('button', { class: 'btn sm danger', onclick: function () {
              confirmDialog('Delete the token \u201c' + t.name + '\u201d?', h('p', null, 'Whatever uses it stops being able to send alerts immediately.'), 'Delete token', true)
                .then(function (y) { if (y) return api('DELETE', '/api/tokens/' + t.id).then(load); }).catch(fail);
            } }, 'Delete')));
        })))) : null;
      box.appendChild(h('div', { class: 'panel' }, h('div', { class: 'row' }, h('h2', null, 'API tokens'), h('span', { class: 'spacer' }),
        h('button', { class: 'btn', onclick: function () {
          var name = h('input', { type: 'text', maxlength: 60, placeholder: 'e.g. School website' }), e2 = h('p', { class: 'error' });
          var scopes = { send: true, read: true, lock: false };
          var boxes = Object.keys(S.meta.scopes || { send: '', read: '', lock: '' }).map(function (k) {
            var cb = h('input', { type: 'checkbox', checked: scopes[k], onchange: function () { scopes[k] = cb.checked; } });
            return h('label', { class: 'check' }, cb, h('span', null, k, h('small', null, (S.meta.scopes || {})[k] || '')));
          });
          var dlg = dialog('New API token', [h('div', { class: 'stack' }, h('label', { class: 'field' }, h('span', null, 'What will use it?'), name),
            h('div', { class: 'field' }, h('span', null, 'What it may do'), boxes, h('small', null, 'Give it only what it needs. A website that only sends notices does not need \u201clock\u201d.')), e2)], [
            h('button', { class: 'btn', onclick: function () { dlg.close(); } }, 'Cancel'),
            h('button', { class: 'btn primary', onclick: function () {
              api('POST', '/api/tokens', { name: name.value, scopes: Object.keys(scopes).filter(function (k) { return scopes[k]; }) }).then(function (r) { dlg.close(); load(); secretDialog('Token created', 'Copy this token now. It is not shown again.', r.token, 'Send it as the header  Authorization: Bearer <token>. docs/API.md has examples.'); })
                .catch(function (x) { e2.textContent = x.message; });
            } }, 'Create token')]);
        } }, 'New token')),
        h('p', null, 'Let a script, a website or another system send alerts without a person signing in. Tokens never change settings. ', h('a', { href: '#/api' }, 'API reference')), tt));
      box.appendChild(webhooksPanel());

      box.appendChild(h('div', { class: 'panel' }, h('h2', null, 'About this server'),
        h('dl', { class: 'dl' }, h('dt', null, 'Version'), h('dd', null, st.version), h('dt', null, 'Data folder'), h('dd', { class: 'mono' }, st.data_dir),
          h('dt', null, 'HTTPS'), h('dd', null, st.https ? 'On' : 'Off. Traffic is unencrypted on your network. The admin guide explains how to turn HTTPS on.'),
          h('dt', null, 'Built by'),
          h('dd', null, 'Archie Paice – ', h('a', { href: 'mailto:hello@archiepaice.com' }, 'hello@archiepaice.com')))));
    }
    function editUser(u) {
      var uname = h('input', { type: 'text', maxlength: 64, value: u ? u.username : '', disabled: !!u, autocapitalize: 'none' });
      var dname = h('input', { type: 'text', maxlength: 80, value: u ? u.display_name : '', placeholder: 'Shown on alerts as \u201cFrom \u2026\u201d' });
      var role = h('select', null, h('option', { value: 'sender', selected: !u || u.role === 'sender' }, 'Sender \u2013 can send alerts'), h('option', { value: 'admin', selected: u && u.role === 'admin' }, 'Administrator \u2013 can do everything'));
      var disabled = h('input', { type: 'checkbox', checked: u && u.disabled });
      var lockOk = h('input', { type: 'checkbox', checked: u && u.can_lock });
      var lockRow = h('label', { class: 'check' }, lockOk, h('span', null, 'Can lock screens and block keyboard and mouse', h('small', null, 'Only for the computers they are allowed to send to.')));
      var allowed = (u ? u.allowed_group_ids : []).slice();
      var groupsBox = h('div', { class: 'checklist' }, S.groups.map(function (g) {
        var cb = h('input', { type: 'checkbox', checked: allowed.indexOf(g.id) >= 0, onchange: function () { allowed = allowed.filter(function (x) { return x !== g.id; }); if (cb.checked) allowed.push(g.id); } });
        return h('label', { class: 'check' }, cb, h('span', null, g.name));
      }));
      var limit = h('div', { class: 'field' }, h('span', null, 'Limit to these groups'), S.groups.length ? groupsBox : h('small', null, 'Create some groups first.'), h('small', null, 'Leave everything unticked to let them send to anyone.'));
      function sync() { limit.classList.toggle('hidden', role.value === 'admin'); lockRow.classList.toggle('hidden', role.value === 'admin'); }
      role.addEventListener('change', sync); sync();
      var err = h('p', { class: 'error' });
      function save(reset) {
        var body = { username: uname.value, display_name: dname.value, role: role.value, disabled: disabled.checked, allowed_group_ids: role.value === 'admin' ? [] : allowed, can_lock: lockOk.checked, reset_password: !!reset };
        (u ? api('PUT', '/api/users/' + u.id, body) : api('POST', '/api/users', body)).then(function (r) {
          dlg.close(); load();
          if (r.temp_password) secretDialog('Temporary password for ' + r.user.username, 'Pass this on privately. They are asked to choose their own the first time they sign in.', r.temp_password, 'It is not shown again. If it gets lost, use \u201cReset password\u201d.');
          else toast('Saved');
        }).catch(function (e) { err.textContent = e.message; });
      }
      var dlg = dialog(u ? 'Edit ' + u.username : 'Add a person', [h('div', { class: 'stack' },
        h('label', { class: 'field' }, h('span', null, 'Username'), uname), h('label', { class: 'field' }, h('span', null, 'Full name'), dname),
        h('label', { class: 'field' }, h('span', null, 'Role'), role), limit, lockRow,
        u ? h('label', { class: 'check' }, disabled, h('span', null, 'Disable this account', h('small', null, 'They are signed out and cannot sign back in.'))) : null, err)], [
        u && u.id !== S.me.id ? h('button', { class: 'btn danger', onclick: function () {
          confirmDialog('Delete ' + u.username + '?', h('p', null, 'Alerts they sent stay in the history.'), 'Delete account', true).then(function (y) { if (y) return api('DELETE', '/api/users/' + u.id).then(function () { dlg.close(); load(); }); }).catch(function (e) { err.textContent = e.message; });
        } }, 'Delete') : null,
        u ? h('button', { class: 'btn', onclick: function () { save(true); } }, 'Reset password') : null,
        h('button', { class: 'btn', onclick: function () { dlg.close(); } }, 'Cancel'),
        h('button', { class: 'btn primary', onclick: function () { save(false); } }, u ? 'Save' : 'Add person')]);
    }
    load();
  }

  // "#c3301c" darkened by k (0..1), for the full-screen preview background.
  function shade(hex, k) {
    var v = parseInt(hex.slice(1), 16);
    var r = Math.round(((v >> 16) & 255) * k), g = Math.round(((v >> 8) & 255) * k), b = Math.round((v & 255) * k);
    return 'rgb(' + r + ',' + g + ',' + b + ')';
  }

  // A compact "who" picker shared by the lock page: everyone, groups, or patterns.
  function audiencePicker(state, onChange) {
    var allowedGroups = restricted() ? S.groups.filter(function (g) { return S.me.allowed_group_ids.indexOf(g.id) >= 0; }) : S.groups;
    if (restricted()) state.mode = 'groups';
    var body = h('div', { class: 'stack' });
    function draw() {
      clear(body);
      if (state.mode === 'groups') {
        if (!allowedGroups.length) { body.appendChild(h('p', { class: 'notice' }, 'There are no groups yet.')); return; }
        body.appendChild(h('div', { class: 'checklist' }, allowedGroups.map(function (g) {
          var cb = h('input', { type: 'checkbox', checked: state.group_ids.indexOf(g.id) >= 0, onchange: function () {
            state.group_ids = state.group_ids.filter(function (x) { return x !== g.id; });
            if (cb.checked) state.group_ids.push(g.id);
            onChange();
          } });
          return h('label', { class: 'check' }, cb, h('span', null, g.name, h('small', null, g.online + ' on now')));
        })));
      } else if (state.mode === 'custom') {
        var field = h('select', { onchange: function () { state.field = field.value; onChange(); } },
          Object.keys(FIELDS).map(function (k) { return h('option', { value: k, selected: state.field === k }, FIELDS[k]); }));
        var pats = h('input', { type: 'text', value: state.patterns, placeholder: 'LAB1-*, LIB-PC07', oninput: function () { state.patterns = pats.value; onChange(); } });
        body.appendChild(h('div', { class: 'rule-row' }, field, pats, h('span')));
      }
    }
    var seg = null;
    if (!restricted()) {
      seg = h('div', { class: 'seg', role: 'radiogroup' }, [['all', 'Everyone'], ['groups', 'Groups'], ['custom', 'Specific computers']].map(function (o) {
        return h('label', null, h('input', { type: 'radio', name: 'aud', value: o[0], checked: state.mode === o[0], onchange: function () { state.mode = o[0]; draw(); onChange(); } }), o[1]);
      }));
    }
    draw();
    return h('div', { class: 'stack' }, seg, body);
  }

  // ---------- lock screens ----------
  var lockDraft = { mode: 'all', group_ids: [], field: 'hostname', patterns: '', lock_mode: 'blank', message: '', minutes: 15 };
  function lockView(main) {
    var L = lockDraft;
    main.appendChild(h('div', { class: 'page-head' }, h('div', null, h('h1', null, 'Lock screens'),
      h('p', null, 'Cover screens with a message, or freeze them, and block keyboard and mouse until you unlock them. Every lock also lifts itself at the time you choose.'))));

    var activeBox = h('div');
    var reach = h('div', { class: 'reach' });
    var err = h('p', { class: 'error', role: 'alert' });
    var reachTimer = null;
    function askReach() {
      clearTimeout(reachTimer);
      reachTimer = setTimeout(function () {
        var t = buildTarget(L);
        if (!t.all && !t.group_ids.length && !t.rules.length) { reach.className = 'reach zero'; reach.textContent = 'Choose which computers to lock.'; return; }
        api('POST', '/api/reach', t).then(function (r) {
          reach.className = 'reach' + (r.online ? '' : ' zero');
          reach.textContent = r.online ? 'Locks ' + plural(r.online, 'computer', 'computers') + ' that ' + (r.online === 1 ? 'is' : 'are') + ' on right now.' : 'None of the matching computers are on right now.';
        }).catch(function () {});
      }, 250);
    }
    var modeSeg = h('div', { class: 'seg', role: 'radiogroup' }, [['blank', 'Blank screen', 'covers it with your message'], ['freeze', 'Freeze', 'screen visible, input blocked']].map(function (o) {
      return h('label', null, h('input', { type: 'radio', name: 'lmode', checked: L.lock_mode === o[0], onchange: function () { L.lock_mode = o[0]; } }), o[1], h('small', null, o[2]));
    }));
    var msg = h('textarea', { maxlength: 500, placeholder: 'Eyes to the front, please.', oninput: function () { L.message = msg.value; } });
    msg.value = L.message; msg.style.minHeight = '70px';
    var mins = h('select', { onchange: function () { L.minutes = +mins.value; } },
      [[5, '5 minutes'], [10, '10 minutes'], [15, '15 minutes'], [30, '30 minutes'], [60, '1 hour'], [120, '2 hours'], [240, '4 hours']].map(function (o) {
        return h('option', { value: o[0], selected: L.minutes === o[0] }, 'Unlock by itself after ' + o[1]);
      }));
    var go = h('button', { class: 'btn primary big', onclick: function () {
      err.textContent = '';
      var body = { mode: L.lock_mode, message: L.message.trim(), duration_minutes: L.minutes, target: buildTarget(L) };
      confirmDialog('Lock these screens?', [h('p', null, reach.textContent),
        h('p', { class: 'muted' }, 'Keyboard and mouse stop working on those computers until you unlock them, or for ' + mins.options[mins.selectedIndex].text.replace('Unlock by itself after ', '') + '.')], 'Lock screens', true).then(function (y) {
        if (!y) return;
        go.disabled = true;
        api('POST', '/api/locks', body).then(function (r) { toast('Locked. ' + plural(r.online_reach, 'computer', 'computers') + ' locking now.'); go.disabled = false; load(); })
          .catch(function (e) { err.textContent = e.message; go.disabled = false; });
      });
    } }, 'Lock screens');

    main.appendChild(activeBox);
    main.appendChild(h('div', { class: 'panel stack' }, h('h2', null, 'Lock'),
      audiencePicker(L, askReach), reach,
      h('fieldset', null, h('span', { class: 'legend' }, 'How'), modeSeg),
      h('label', { class: 'field' }, h('span', null, 'Message on the screen'), msg),
      h('label', { class: 'field' }, h('span', null, 'Safety limit'), mins, h('small', null, 'The lock lifts itself after this even if nobody unlocks it, and even if the PC cannot reach this server.')),
      err, h('div', { class: 'sendbar' }, go)));
    askReach();

    function details(l) {
      api('GET', '/api/locks/' + l.id).then(function (full) {
        var rows = full.computers || [];
        dialog('Computers in this lock', [rows.length ? h('div', { class: 'tablewrap' }, h('table', null,
          h('thead', null, h('tr', null, h('th', null, 'Computer'), h('th', null, 'User'), h('th', null, 'Locked'), h('th', null, 'Released'))),
          h('tbody', null, rows.map(function (c) {
            return h('tr', null, h('td', null, c.hostname), h('td', null, c.user), h('td', null, c.locked_at ? clock(c.locked_at) : 'Waiting'), h('td', null, c.released_at ? clock(c.released_at) : ''));
          })))) : h('p', { class: 'muted' }, 'No computer has picked it up yet.')],
          [h('button', { class: 'btn primary', onclick: function (e) { e.target.closest('dialog').close(); } }, 'Close')]);
      }).catch(fail);
    }
    function load() {
      api('GET', '/api/locks?limit=30').then(function (list) {
        clear(activeBox);
        var live = list.filter(function (l) { return l.active; }), past = list.filter(function (l) { return !l.active; }).slice(0, 10);
        if (live.length) {
          activeBox.appendChild(h('div', { class: 'panel lock-live' }, h('div', { class: 'row' }, h('h2', null, 'Locked now'), h('span', { class: 'spacer' }),
            h('button', { class: 'btn danger', onclick: function () {
              api('POST', '/api/locks/unlock-all').then(function (r) { toast('Unlocked ' + plural(r.unlocked, 'lock', 'locks')); load(); }).catch(fail);
            } }, isAdmin() ? 'Unlock everything' : 'Unlock all mine')),
            h('div', { class: 'tablewrap' }, h('table', null, h('tbody', null, live.map(function (l) {
              return h('tr', null, h('td', null, h('b', null, l.target_summary), h('div', { class: 'muted small' }, (l.spec.mode === 'freeze' ? 'Frozen' : 'Blank screen') + (l.spec.message ? ' – “' + l.spec.message + '”' : ''))),
                h('td', null, l.sender_name), h('td', { class: 'nowrap' }, plural(l.locked, 'computer', 'computers') + ' locked'),
                h('td', { class: 'nowrap' }, 'until ' + until(l.ends_at)),
                h('td', { class: 'nowrap' }, h('button', { class: 'btn sm', onclick: function () { details(l); } }, 'Computers'), ' ',
                  h('button', { class: 'btn sm primary', onclick: function () { api('POST', '/api/locks/' + l.id + '/unlock').then(function () { toast('Unlocked'); load(); }).catch(fail); } }, 'Unlock')));
            }))))));
        }
        if (past.length) {
          activeBox.appendChild(h('details', { class: 'panel past' }, h('summary', null, 'Recent locks'),
            h('ul', null, past.map(function (l) {
              return h('li', null, when(l.created_at) + ': ' + l.target_summary + ' by ' + l.sender_name + (l.unlocked_at ? ', unlocked by ' + l.unlocked_by.replace(/^token:/, '') + ' ' + when(l.unlocked_at) : ', ran its full time'));
            }))));
        }
        if (activeBox.firstChild) activeBox.lastChild.style.marginBottom = '16px';
      }).catch(fail);
    }
    load(); every(3000, load);
  }

  // ---------- schedules ----------
  function schedulesView(main) {
    main.appendChild(h('div', { class: 'page-head' }, h('div', null, h('h1', null, 'Scheduled alerts'),
      h('p', null, 'Alerts that go out by themselves – once, or every week. Times are the server’s clock.')),
      h('span', { class: 'spacer' }), h('a', { class: 'btn primary', href: '#/send', onclick: function () { draft = freshDraft(); draft.when = 'later'; } }, 'Schedule an alert')));
    var wrap = h('div', { class: 'tablewrap' }); main.appendChild(wrap);
    function mine(sc) { return isAdmin() || sc.owner === S.me.id; }
    function save(sc, changes) {
      var body = { name: sc.name, spec: sc.spec, enabled: sc.enabled };
      if (sc.once) body.once = sc.once; else { body.days = sc.days; body.time = sc.time; }
      Object.assign(body, changes);
      return api('PUT', '/api/schedules/' + sc.id, body);
    }
    function load() {
      api('GET', '/api/schedules').then(function (list) {
        clear(wrap);
        if (!list.length) { wrap.appendChild(h('div', { class: 'empty' }, h('b', null, 'Nothing scheduled'), 'On Send an alert, choose “Schedule for later” under When.')); return; }
        wrap.appendChild(h('table', null,
          h('thead', null, h('tr', null, h('th', null, 'Alert'), h('th', null, 'When'), h('th', null, 'Next'), h('th', null, 'Last'), h('th'))),
          h('tbody', null, list.map(function (sc) {
            var next = sc.enabled && sc.next_run && sc.next_run.slice(0, 4) !== '0001' ? until(sc.next_run) : (sc.enabled ? '–' : 'Paused');
            return h('tr', null,
              h('td', null, h('span', { class: 'dot ' + sc.spec.level }), h('b', null, sc.name), h('div', { class: 'muted small' }, sc.spec.title + ' → ' + (sc.spec.target.all ? 'Everyone' : 'chosen computers'))),
              h('td', null, describeSchedule(sc)), h('td', { class: 'nowrap' }, next),
              h('td', null, sc.last_run && sc.last_run.slice(0, 4) !== '0001' ? when(sc.last_run) : 'never',
                sc.last_error ? h('div', { class: 'error small' }, sc.last_error) : null,
                sc.last_alert ? h('div', null, h('a', { class: 'small', href: '#/alerts/' + sc.last_alert }, 'See result')) : null),
              h('td', { class: 'nowrap' }, mine(sc) ? [
                h('button', { class: 'btn sm', onclick: function () { save(sc, { enabled: !sc.enabled }).then(load).catch(fail); } }, sc.enabled ? 'Pause' : 'Resume'), ' ',
                h('button', { class: 'btn sm', onclick: function () {
                  draft = Object.assign(draftFromSpec(sc.spec, false), { when: 'later', once: sc.once, days: (sc.days || []).slice(), time: sc.time || '15:50', schedule_id: sc.id, schedule_name: sc.name });
                  location.hash = '#/send';
                } }, 'Edit'), ' ',
                h('button', { class: 'btn sm', onclick: function () {
                  confirmDialog('Send “' + sc.spec.title + '” now?', h('p', null, 'This does not change when it next runs.'), 'Send now').then(function (y) {
                    if (y) api('POST', '/api/schedules/' + sc.id + '/run').then(function (r) { toast('Sent'); location.hash = '#/alerts/' + r.alert.id; }).catch(fail);
                  });
                } }, 'Send now'), ' ',
                h('button', { class: 'btn sm danger', onclick: function () {
                  confirmDialog('Delete “' + sc.name + '”?', h('p', null, 'Alerts it already sent stay in the history.'), 'Delete', true).then(function (y) {
                    if (y) api('DELETE', '/api/schedules/' + sc.id).then(load).catch(fail);
                  });
                } }, 'Delete')] : null));
          }))));
      }).catch(fail);
    }
    load(); every(15000, load);
  }

  // ---------- sounds ----------
  function soundsView(main) {
    main.appendChild(h('div', { class: 'page-head' }, h('div', null, h('h1', null, 'Sounds'),
      h('p', null, 'Your own sounds for alerts – a school bell, a fire alarm, a chime. PCs download each sound once and keep it, so it plays instantly next time.'))));
    var box = h('div'); main.appendChild(box);
    var playing = null;
    function draw() {
      clear(box);
      if (isAdmin()) {
        var file = h('input', { type: 'file', accept: '.wav,.mp3,audio/wav,audio/mpeg' });
        var name = h('input', { type: 'text', maxlength: 60, placeholder: 'e.g. Fire bell' });
        var e2 = h('p', { class: 'error' });
        file.addEventListener('change', function () { if (file.files[0] && !name.value) name.value = file.files[0].name.replace(/\.[^.]+$/, '').replace(/[-_]+/g, ' '); });
        var up = h('button', { class: 'btn primary', onclick: function () {
          e2.textContent = '';
          var f = file.files[0];
          if (!f) { e2.textContent = 'Choose a WAV or MP3 file.'; return; }
          if (f.size > (S.meta.max_sound_bytes || 5242880)) { e2.textContent = 'That file is ' + kb(f.size) + '; the limit is 5 MB. Save it as an MP3 or trim it.'; return; }
          up.disabled = true;
          fetch('/api/sounds?name=' + encodeURIComponent(name.value || f.name), { method: 'POST', credentials: 'same-origin', headers: { 'X-Foghorn': '1', 'Content-Type': f.type || 'application/octet-stream' }, body: f })
            .then(function (res) { return res.json().then(function (j) { if (!res.ok) throw new Error(j.message || 'Upload failed'); return j; }); })
            .then(function () { toast('Sound uploaded'); return loadShared(); }).then(draw)
            .catch(function (x) { e2.textContent = x.message; up.disabled = false; });
        } }, 'Upload');
        box.appendChild(h('div', { class: 'panel stack' }, h('h2', null, 'Upload a sound'),
          h('div', { class: 'two' }, h('label', { class: 'field' }, h('span', null, 'File'), file, h('small', null, 'WAV or MP3, up to 5 MB. Short is best: 1,500 PCs each download it the first time it is used.')),
            h('label', { class: 'field' }, h('span', null, 'Name'), name)), e2, h('div', null, up)));
      }
      var tw = h('div', { class: 'tablewrap' });
      if (!S.sounds.length) tw.appendChild(h('div', { class: 'empty' }, h('b', null, 'No sounds yet'), 'Alerts use the Windows alert sound until you upload one.'));
      else tw.appendChild(h('table', null, h('thead', null, h('tr', null, h('th', null, 'Sound'), h('th', null, 'Size'), h('th', null, 'Uploaded'), h('th'))),
        h('tbody', null, S.sounds.map(function (x) {
          var play = h('button', { class: 'btn sm', onclick: function () {
            if (playing) { playing.pause(); playing = null; }
            playing = new Audio('/api/sounds/' + x.id + '/file'); playing.play().catch(function () { toast('This browser could not play it.', true); });
          } }, 'Play');
          return h('tr', null, h('td', null, h('b', null, x.name), ' ', h('span', { class: 'muted small' }, x.ext.slice(1).toUpperCase())),
            h('td', null, kb(x.size)), h('td', null, when(x.uploaded_at) + ' by ' + x.uploaded_by),
            h('td', { class: 'nowrap' }, play, ' ', isAdmin() ? h('button', { class: 'btn sm danger', onclick: function () {
              confirmDialog('Delete “' + x.name + '”?', h('p', null, 'Templates and schedules that use it go back to the Windows sound.'), 'Delete', true).then(function (y) {
                if (y) api('DELETE', '/api/sounds/' + x.id).then(loadShared).then(draw).catch(fail);
              });
            } }, 'Delete') : null));
        }))));
      box.appendChild(h('div', { class: 'stack' }, h('span'), tw));
    }
    loadShared().then(draw).catch(fail);
  }

  // ---------- webhooks (in Settings) ----------
  function webhooksPanel() {
    var panel = h('div', { class: 'panel' });
    function load() {
      api('GET', '/api/webhooks').then(function (r) {
        clear(panel);
        var events = r.events || {};
        function edit(w) {
          var name = h('input', { type: 'text', maxlength: 60, value: w ? w.name : '', placeholder: 'e.g. School website' });
          var url = h('input', { type: 'url', value: w ? w.url : '', placeholder: 'https://intranet.example.sch.uk/foghorn/webhook' });
          var chosen = (w ? w.events : []).slice();
          var boxes = Object.keys(events).sort().map(function (k) {
            var cb = h('input', { type: 'checkbox', checked: chosen.indexOf(k) >= 0, onchange: function () { chosen = chosen.filter(function (x) { return x !== k; }); if (cb.checked) chosen.push(k); } });
            return h('label', { class: 'check' }, cb, h('span', null, h('span', { class: 'mono' }, k), h('small', null, events[k])));
          });
          var e2 = h('p', { class: 'error' });
          var dlg = dialog(w ? 'Edit webhook' : 'New webhook', [h('div', { class: 'stack' },
            h('label', { class: 'field' }, h('span', null, 'Name'), name),
            h('label', { class: 'field' }, h('span', null, 'Address Foghorn sends to'), url),
            h('div', { class: 'field' }, h('span', null, 'Events'), h('div', { class: 'checklist wide' }, boxes), h('small', null, 'Tick none to receive every event.')), e2)], [
            h('button', { class: 'btn', onclick: function () { dlg.close(); } }, 'Cancel'),
            h('button', { class: 'btn primary', onclick: function () {
              var body = { name: name.value, url: url.value, events: chosen, enabled: w ? w.enabled : true };
              (w ? api('PUT', '/api/webhooks/' + w.id, body) : api('POST', '/api/webhooks', body)).then(function (res) {
                dlg.close(); load();
                if (res.secret) secretDialog('Signing secret', 'Foghorn signs every delivery with this. Put it in your receiving app (for Laravel: FOGHORN_WEBHOOK_SECRET in .env) to check deliveries are genuine.', res.secret, 'It is not shown again; use “New secret” if it is lost.');
                else toast('Saved');
              }).catch(function (x) { e2.textContent = x.message; });
            } }, w ? 'Save' : 'Create webhook')]);
        }
        var list = r.webhooks || [];
        panel.appendChild(h('div', { class: 'row' }, h('h2', null, 'Webhooks'), h('span', { class: 'spacer' }), h('button', { class: 'btn', onclick: function () { edit(null); } }, 'New webhook')));
        panel.appendChild(h('p', null, 'Tell another system – your website, Teams, a logging tool – the moment something happens: an alert is answered, screens are locked. Each delivery is a signed JSON POST. ', h('a', { href: '#/api' }, 'How to receive them')));
        if (list.length) panel.appendChild(h('div', { class: 'tablewrap' }, h('table', null,
          h('thead', null, h('tr', null, h('th', null, 'Name'), h('th', null, 'Events'), h('th', null, 'Last delivery'), h('th'))),
          h('tbody', null, list.map(function (w) {
            return h('tr', null, h('td', null, h('b', null, w.name), w.enabled ? null : h('span', { class: 'pill recalled' }, 'Off'), h('div', { class: 'muted small mono' }, w.url)),
              h('td', { class: 'small' }, w.events.length ? w.events.join(', ') : 'Everything'),
              h('td', { class: 'small' }, w.last_at && w.last_at.slice(0, 4) !== '0001' ? when(w.last_at) : 'never', w.last_error ? h('div', { class: 'error' }, w.last_error) : (w.last_at && w.last_at.slice(0, 4) !== '0001' ? h('div', { class: 'ok' }, 'Delivered') : null)),
              h('td', { class: 'nowrap' },
                h('button', { class: 'btn sm', onclick: function () {
                  api('POST', '/api/webhooks/' + w.id + '/test').then(function (t) { if (t.ok) toast('Test delivered'); else toast('Test failed: ' + t.error, true); load(); }).catch(fail);
                } }, 'Test'), ' ',
                h('button', { class: 'btn sm', onclick: function () { edit(w); } }, 'Edit'), ' ',
                h('button', { class: 'btn sm', onclick: function () {
                  api('PUT', '/api/webhooks/' + w.id, { name: w.name, url: w.url, events: w.events, enabled: !w.enabled }).then(load).catch(fail);
                } }, w.enabled ? 'Turn off' : 'Turn on'), ' ',
                h('button', { class: 'btn sm', onclick: function () {
                  confirmDialog('Make a new signing secret?', h('p', null, 'The receiving app rejects deliveries until you give it the new secret.'), 'New secret', true).then(function (y) {
                    if (y) api('PUT', '/api/webhooks/' + w.id, { name: w.name, url: w.url, events: w.events, enabled: w.enabled, rotate_secret: true })
                      .then(function (res) { secretDialog('New signing secret', 'Update your receiving app with this.', res.secret); }).catch(fail);
                  });
                } }, 'New secret'), ' ',
                h('button', { class: 'btn sm danger', onclick: function () {
                  confirmDialog('Delete “' + w.name + '”?', h('p', null, 'Foghorn stops telling it anything.'), 'Delete', true).then(function (y) { if (y) api('DELETE', '/api/webhooks/' + w.id).then(load).catch(fail); });
                } }, 'Delete')));
          })))));
      }).catch(fail);
    }
    load();
    return panel;
  }

  // ---------- API reference ----------
  function apiView(main) {
    var base = location.origin;
    main.appendChild(h('div', { class: 'page-head' }, h('div', null, h('h1', null, 'API'),
      h('p', null, 'Send alerts and lock screens from your own systems – a school website, a Laravel app, the MIS, a fire panel, a PowerShell script.'))));

    function codeBlock(text) {
      return h('div', { class: 'code' }, h('button', { class: 'btn sm copy', onclick: function () { copy(text); } }, 'Copy'), h('pre', null, text));
    }
    var curl = 'curl -X POST ' + base + '/api/alerts \\\n' +
      '  -H "Authorization: Bearer fh_your_token" \\\n' +
      '  -H "Content-Type: application/json" \\\n' +
      '  -H "Idempotency-Key: parents-evening-2026-11-03" \\\n' +
      '  -d \'{"title":"Parents’ evening tonight","message":"Rooms open at 17:00.","icon":"calendar",\n' +
      '       "responses":["Coming","Can’t make it"],"target":{"all":true}}\'';
    var php = '// Laravel - see docs/INTEGRATIONS-LARAVEL.md for the full service class\n' +
      'Http::withToken(config(\'services.foghorn.token\'))\n' +
      '    ->withHeaders([\'Idempotency-Key\' => "event-{$event->id}"])\n' +
      '    ->post(config(\'services.foghorn.url\').\'/api/alerts\', [\n' +
      '        \'title\'  => \'Parents’ evening tonight\',\n' +
      '        \'level\'  => \'info\',\n' +
      '        \'target\' => [\'group_ids\' => [config(\'services.foghorn.staff_group\')]],\n' +
      '    ])->throw()->json(\'alert.id\');';
    var verify = '// Checking a webhook really came from Foghorn (PHP)\n' +
      '$expected = \'sha256=\'.hash_hmac(\'sha256\',\n' +
      '    $request->header(\'X-Foghorn-Timestamp\').\'.\'.$request->getContent(),\n' +
      '    config(\'services.foghorn.webhook_secret\'));\n' +
      'abort_unless(hash_equals($expected, (string) $request->header(\'X-Foghorn-Signature\')), 401);';

    main.appendChild(h('div', { class: 'panel stack' }, h('h2', null, 'Quick start'),
      h('ol', { class: 'steps' },
        h('li', null, isAdmin() ? h('span', null, 'Create a token in ', h('a', { href: '#/settings' }, 'Settings → API tokens'), '. Give it only the scopes it needs: ') : 'Ask an administrator for an API token with the scopes you need: ',
          h('span', { class: 'mono' }, 'send'), ', ', h('span', { class: 'mono' }, 'lock'), ', ', h('span', { class: 'mono' }, 'read'), '.'),
        h('li', null, 'Send it on every request as ', h('span', { class: 'mono' }, 'Authorization: Bearer fh_…'), '. The server address is ', h('span', { class: 'mono' }, base), '.'),
        h('li', null, 'Add an ', h('span', { class: 'mono' }, 'Idempotency-Key'), ' header when sending, so a retried request never sends the same alert twice.'),
        h('li', null, 'To hear back when people answer, add a webhook in Settings → Webhooks and check its signature.')),
      h('h3', null, 'curl'), codeBlock(curl),
      h('h3', null, 'Laravel'), codeBlock(php),
      h('h3', null, 'Receiving webhooks'), codeBlock(verify),
      h('p', { class: 'small' }, h('a', { href: '/api/openapi.json', download: 'foghorn-openapi.json' }, 'Download the OpenAPI description'),
        ' – import it into Postman or Insomnia, or generate a client library. The full guide is docs/API.md; Laravel step by step is docs/INTEGRATIONS-LARAVEL.md.')));

    var ref = h('div', { class: 'panel' }, h('h2', null, 'Reference'), h('p', { class: 'muted' }, 'Loading…'));
    main.appendChild(ref);
    fetch('/api/openapi.json').then(function (r) { return r.json(); }).then(function (spec) {
      clear(ref).appendChild(h('h2', null, 'Reference'));
      var byTag = {};
      Object.keys(spec.paths).forEach(function (path) {
        Object.keys(spec.paths[path]).forEach(function (method) {
          var op = spec.paths[path][method], tag = (op.tags || ['Other'])[0];
          (byTag[tag] = byTag[tag] || []).push({ path: path, method: method.toUpperCase(), op: op });
        });
      });
      (spec.tags || []).map(function (t) { return t.name; }).concat(Object.keys(byTag)).filter(function (t, i, a) { return byTag[t] && a.indexOf(t) === i; }).forEach(function (tag) {
        ref.appendChild(h('h3', { class: 'ref-tag' }, tag));
        byTag[tag].forEach(function (e) {
          var ex = e.op.requestBody && e.op.requestBody.content && e.op.requestBody.content['application/json'];
          var sample = ex && (ex.example || (ex.examples && ex.examples[Object.keys(ex.examples)[0]].value));
          ref.appendChild(h('details', { class: 'endpoint' },
            h('summary', null, h('span', { class: 'verb ' + e.method.toLowerCase() }, e.method), h('span', { class: 'mono' }, e.path), h('span', { class: 'muted' }, e.op.summary)),
            e.op.description ? h('p', { class: 'small' }, e.op.description) : null,
            sample ? codeBlock(JSON.stringify(sample, null, 2)) : null));
        });
      });
    }).catch(function () { clear(ref).appendChild(h('p', { class: 'notice warn' }, 'Could not load the API description.')); });
  }

  // ---------- help ----------
  function helpView(main) {
    function sec(title) { var out = [h('h2', null, title)]; for (var i = 1; i < arguments.length; i++) out.push(arguments[i]); return out; }
    function ul() { return h('ul', null, [].slice.call(arguments).map(function (t) { return h('li', null, t); })); }
    main.appendChild(h('div', { class: 'page-head' }, h('h1', null, 'Help')));
    main.appendChild(h('div', { class: 'panel help' },
      sec('Sending an alert', h('p', null, 'Write a short title that makes sense on its own, add a message if people need to do something, choose who gets it, and press Send alert. You are always shown how many computers it reaches and asked to confirm before anything goes out.'),
        h('p', null, 'Alerts arrive within a second or two on computers where someone is logged on. Nothing appears on a computer sitting at the Windows sign-in screen.')),
      sec('Choosing how it appears',
        ul('Corner \u2013 a card at the top-right. It sits above other windows but does not take the keyboard, so nobody loses what they were typing. Best for routine notices.',
          'Centre \u2013 the same card, larger, in the middle of the screen. Hard to miss.',
          'Full screen \u2013 covers every monitor until closed. Keep it for emergencies and for getting a room\u2019s attention.')),
      sec('Making sure it was read', h('p', null, 'Tick \u201cMake people confirm they have read it\u201d. The alert cannot be closed any other way, and Sent alerts shows who has confirmed and who has not. If someone logs off without confirming, the alert returns when they next log on, for as long as it is still being delivered.')),
      sec('Late arrivals', h('p', null, 'By default an alert also reaches anyone who logs on within the next 10 minutes. Shorten that for \u201cright now\u201d messages such as \u201ceyes to the front\u201d, and lengthen it for notices that matter all day, such as a room closure.')),
      sec('Taking an alert back', h('p', null, 'Open Sent alerts and press Recall. The alert vanishes from every screen still showing it and stops being delivered.')),
      sec('Templates', h('p', null, 'Set up wording you reuse, then press \u201cSave as a template\u201d. Templates remember the words and appearance but never the audience, so you always choose who gets it.')),
      sec('Designing an alert', h('p', null, 'Under \u201cLook and buttons\u201d pick an icon, a colour and the wording of the band and the close button. Add reply buttons to ask a question \u2013 \u201cAre you safe?\u201d \u2013 and Sent alerts shows who picked which reply, live.')),
      sec('Scheduling', h('p', null, 'Choose \u201cSchedule for later\u201d under When to send once at a set time, or every week on chosen days (\u201cSave your work\u201d at 15:50 every weekday, say). Scheduled lists them all; you can pause, edit or run one straight away.')),
      sec('Locking screens', h('p', null, 'Lock screens covers chosen computers with your message, or freezes them with the screen still visible, and blocks keyboard and mouse. Every lock lifts itself at the time you chose, and you can unlock early at any time. Windows always lets someone press Ctrl+Alt+Del to sign out; if they sign back in while the lock is on, it comes straight back.')),
      sec('Good practice',
        ul('Say what to do, not just what is happening: \u201cSave your work and log off by 3:55\u201d.', 'Use Urgent and Full screen sparingly so they keep their meaning.', 'Alerts carry your name. Never share your sign-in.')),
      h('p', { class: 'muted small' }, 'Foghorn ' + S.version + '. Installation, Group Policy deployment and troubleshooting are covered in the docs folder that came with the server.')));
  }

  boot();
})();
