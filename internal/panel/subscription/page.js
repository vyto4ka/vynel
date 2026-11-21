(function () {
  var cfgEl = document.getElementById('sub-cfg');
  var L = {};
  var ssrOS = '';
  if (cfgEl && cfgEl.textContent) {
    try {
      var parsed = JSON.parse(cfgEl.textContent);
      L = parsed.labels || {};
      ssrOS = parsed.detectedOS || '';
    } catch (_) {}
  }

  function copyValue(text, inputEl) {
    if (!text) return Promise.resolve(false);
    function syncFallback() {
      try {
        if (inputEl && document.body.contains(inputEl)) {
          inputEl.focus();
          inputEl.select();
          if (inputEl.setSelectionRange) inputEl.setSelectionRange(0, text.length);
          if (document.execCommand && document.execCommand('copy')) return true;
        }
      } catch (_) {}
      try {
        var ta = document.createElement('textarea');
        ta.value = text;
        ta.setAttribute('readonly', '');
        ta.style.position = 'fixed';
        ta.style.top = '-9999px';
        ta.style.left = '-9999px';
        document.body.appendChild(ta);
        ta.focus();
        ta.select();
        if (ta.setSelectionRange) ta.setSelectionRange(0, text.length);
        var ok = !!(document.execCommand && document.execCommand('copy'));
        document.body.removeChild(ta);
        return ok;
      } catch (_) {
        return false;
      }
    }
    if (navigator.clipboard && window.isSecureContext) {
      return navigator.clipboard.writeText(text).then(function () { return true; }, function () {
        return syncFallback();
      });
    }
    return Promise.resolve(syncFallback());
  }

  var uInput = document.getElementById('u');
  if (uInput) {
    uInput.addEventListener('click', function () {
      uInput.select();
      if (uInput.setSelectionRange) uInput.setSelectionRange(0, uInput.value.length);
    });
  }

  var cBtn = document.getElementById('c');
  if (cBtn && uInput) {
    var cSpan = cBtn.querySelector('span');
    var cTimer = null;
    cBtn.addEventListener('click', function () {
      copyValue(uInput.value, uInput).then(function () {
        if (!cSpan) return;
        cSpan.textContent = L.copied || 'Copied';
        if (cTimer) clearTimeout(cTimer);
        cTimer = setTimeout(function () {
          cSpan.textContent = L.copy || 'Copy';
        }, 1600);
      });
    });
  }

  var bars = document.getElementById('chart-bars');
  var info = document.getElementById('chart-info');
  if (bars && info) {
    var cols = [].slice.call(bars.querySelectorAll('.day-col'));
    function showDay(el) {
      cols.forEach(function (x) { x.classList.remove('on'); });
      el.classList.add('on');
      info.textContent = '';
      var b = document.createElement('b');
      b.textContent = el.dataset.date || '';
      info.appendChild(b);
      info.appendChild(document.createTextNode(' · ↓ ' + (el.dataset.down || '0') + ' · ↑ ' + (el.dataset.up || '0')));
    }
    cols.forEach(function (el) {
      el.addEventListener('click', function () { showDay(el); });
      el.addEventListener('mouseenter', function () { showDay(el); });
    });
  }

  function detectClientOS() {
    var uaDataPlat = (navigator.userAgentData && navigator.userAgentData.platform) ? String(navigator.userAgentData.platform).toLowerCase() : '';
    if (uaDataPlat.indexOf('win') >= 0) return 'windows';
    if (uaDataPlat.indexOf('android') >= 0) return 'android';
    if (uaDataPlat.indexOf('ios') >= 0) return 'ios';
    if (uaDataPlat.indexOf('mac') >= 0) {
      if (navigator.maxTouchPoints && navigator.maxTouchPoints > 1) return 'ios';
      return 'macos';
    }
    if (uaDataPlat.indexOf('linux') >= 0 || uaDataPlat.indexOf('chrome os') >= 0 || uaDataPlat.indexOf('cros') >= 0) return 'linux';

    var ua = navigator.userAgent || '';
    var plat = navigator.platform || '';
    if (/android/i.test(ua)) return 'android';
    if (/iphone|ipad|ipod/i.test(ua)) return 'ios';
    if (/macintosh|mac os x|macintel/i.test(ua) || /mac/i.test(plat)) {
      if (navigator.maxTouchPoints && navigator.maxTouchPoints > 1) return 'ios';
      return 'macos';
    }
    if (/windows|win64|win32|wow64/i.test(ua) || /win/i.test(plat)) return 'windows';
    if (/linux|x11|cros/i.test(ua) || /linux/i.test(plat)) return 'linux';
    return ssrOS || 'ios';
  }

  var detected = detectClientOS();
  var osNames = { ios: 'iOS', android: 'Android', windows: 'Windows', macos: 'macOS', linux: 'Linux' };
  var tabsWrap = document.getElementById('os-tabs');
  var grid = document.getElementById('apps-grid');
  var badge = document.getElementById('os-badge');
  var gTitle = document.getElementById('guide-title');

  if (tabsWrap && grid) {
    var tabs = [].slice.call(tabsWrap.querySelectorAll('.os-tab'));
    var cards = [].slice.call(grid.querySelectorAll('.app'));

    tabs.forEach(function (t) {
      t.classList.toggle('detected', t.dataset.os === detected);
    });
    if (badge && osNames[detected]) {
      badge.textContent = (L.yourOS || '') + osNames[detected];
      badge.style.display = 'inline-flex';
    }

    function hasPlatform(ps, os) {
      if (!ps) return true;
      var parts = ps.split(/\s+/);
      for (var k = 0; k < parts.length; k++) {
        if (parts[k] === os) return true;
      }
      return false;
    }

    function selectOS(os) {
      tabs.forEach(function (t) { t.classList.toggle('on', t.dataset.os === os); });
      var shown = 0;
      cards.forEach(function (el) {
        var ps = (el.dataset.p || '').trim();
        var ok = hasPlatform(ps, os);
        el.style.display = ok ? '' : 'none';
        var isRec = ok && shown === 0;
        el.classList.toggle('rec', isRec);
        var tag = el.querySelector('.rec-tag');
        if (tag) tag.style.display = isRec ? '' : 'none';
        var add = el.querySelector('.app-add');
        var cp = el.querySelector('.app-copy');
        if (add && cp) {
          var noScheme = el.dataset.id === 'keqdroid' && os === 'linux';
          add.style.display = noScheme ? 'none' : '';
          cp.style.display = noScheme ? '' : 'none';
        }
        if (ok) shown++;
      });
      if (!shown) {
        cards.forEach(function (el, idx) {
          el.style.display = '';
          el.classList.toggle('rec', idx === 0);
          var tag = el.querySelector('.rec-tag');
          if (tag) tag.style.display = idx === 0 ? '' : 'none';
        });
      }
      if (gTitle && osNames[os]) {
        gTitle.textContent = (L.howOn || '') + osNames[os];
      }
    }

    grid.addEventListener('click', function (e) {
      var b = e.target && e.target.closest ? e.target.closest('.app-copy') : null;
      if (!b) return;
      var sp = b.querySelector('span');
      var val = uInput ? uInput.value : '';
      copyValue(val, uInput).then(function () {
        if (!sp) return;
        sp.textContent = L.copied || 'Copied';
        setTimeout(function () {
          sp.textContent = L.copyLink || 'Copy link';
        }, 1600);
      });
    });

    tabs.forEach(function (t) {
      t.addEventListener('click', function () {
        selectOS(t.dataset.os);
      });
    });

    selectOS(detected);
  }

  var dCard = document.getElementById('devices-card');
  if (dCard && dCard.dataset.base) {
    var base = dCard.dataset.base;
    var list = document.getElementById('dev-list');
    var counter = document.getElementById('dev-counter');
    dCard.addEventListener('click', function (e) {
      var btn = e.target && e.target.closest ? e.target.closest('.dev-del') : null;
      if (!btn || btn.disabled) return;
      if (!btn.classList.contains('arm')) {
        btn.classList.add('arm');
        btn.textContent = L.sure || 'Sure?';
        setTimeout(function () {
          if (btn.isConnected && btn.classList.contains('arm')) {
            btn.classList.remove('arm');
            btn.textContent = L.del || 'Delete';
          }
        }, 3500);
        return;
      }
      var id = btn.dataset.id;
      btn.disabled = true;
      btn.textContent = '…';
      fetch(base + encodeURIComponent(id) + '/delete', { method: 'POST', headers: { 'X-Vynel': '1' } })
        .then(function (r) {
          if (!r.ok) throw new Error('err');
          var row = btn.closest('.dev-row');
          if (row) row.remove();
          if (list) {
            var left = list.querySelectorAll('.dev-row').length;
            if (!left) {
              list.innerHTML = '';
              var emp = document.createElement('div');
              emp.className = 'empty-dev';
              emp.textContent = L.noDev || '';
              list.appendChild(emp);
            }
            if (counter && counter.textContent) {
              counter.textContent = counter.textContent.replace(/^\d+/, String(left));
            }
          }
        })
        .catch(function () {
          btn.disabled = false;
          btn.classList.remove('arm');
          btn.textContent = L.err || 'Error';
        });
    });
  }
})();
