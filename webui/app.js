'use strict';

(function () {
  const PAGE_META = {
    status: { title: '概览看板', desc: '本机网关聚合、多 Key 健康选路与策略规则实时状态' },
    upstreams: { title: '上游节点', desc: '多上游优先级路由、熔断退避与 Key 池 12 段健康打分' },
    channels: { title: '账号渠道', desc: '内置厂商渠道模板、浏览器 CDP 登录抓取与探活诊断' },
    logs: { title: '实时日志', desc: '网关内存环形日志缓冲，支持按级别与关键字实时检索' },
    config: { title: '配置与快照', desc: '全局 JSON 配置无感热重载、自动备份快照与 Git 版本库' },
    keys: { title: '接入密钥', desc: '调用网关所需的 API Key 创建、展示、复制与吊销' }
  };

  const TYPE_LABEL = {
    match: '条件',
    inject: '注入',
    override: '改写',
    header: '请求头',
    deny: '拦截'
  };

  const TYPE_CHIP = {
    match: 'success',
    inject: 'cyan',
    override: 'violet',
    header: 'warning',
    deny: 'danger'
  };

  const state = {
    tab: 'status',
    ruleSub: 'canvas',
    cfgSub: 'editor',
    paused: false,
    status: null,
    logs: [],
    rulesHash: '',
    dirty: false,
    editing: -1,
    dragFrom: -1,
    templates: [],
    selectedTpl: null,
    channels: { presets: [], channels: [] },
    expandedUpstreams: new Set(),
    upstreamQuery: '',
    upstreamFilter: 'all',
    presetQuery: '',
    presetProto: 'all',
    logQuery: '',
    logLevel: 'ALL',
    endpointUrl: 'http://127.0.0.1:7878/v1',
    keys: [],
    keyAuth: false,
    keyReveal: {},
    lastRequests: null,
    sparkSamples: [],
    pendingReqs: 0
  };

  const loginState = { id: null, timer: null, tokenKey: '' };

  function $(id) {
    return document.getElementById(id);
  }

  function esc(val) {
    return String(val === undefined || val === null ? '' : val)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  function splitList(val) {
    return String(val || '')
      .split(',')
      .map(function (s) { return s.trim(); })
      .filter(Boolean);
  }

  function formatCompact(n) {
    const num = Number(n || 0);
    if (!Number.isFinite(num)) return '0';
    if (num >= 1000000) return (num / 1000000).toFixed(1) + 'M';
    if (num >= 10000) return (num / 1000).toFixed(1) + 'k';
    return String(num);
  }

  function setBusy(delta) {
    state.pendingReqs = Math.max(0, state.pendingReqs + delta);
    const mark = $('brandMark');
    const rbtn = $('refreshBtn');
    if (mark) mark.classList.toggle('brand-mark--loading', state.pendingReqs > 0);
    if (rbtn) rbtn.classList.toggle('refresh-spinning', state.pendingReqs > 0);
  }

  /* ---------- Token & HTTP Client ---------- */
  function getToken() {
    const el = $('tokenInput');
    return el ? el.value.trim() : '';
  }

  function updateTokenDot() {
    const dot = $('tokenConfiguredDot');
    if (dot) dot.hidden = !getToken();
  }

  function buildHeaders() {
    const h = { 'Content-Type': 'application/json' };
    const t = getToken();
    if (t) h['X-Admin-Token'] = t;
    return h;
  }

  function api(path, opts) {
    const options = Object.assign({}, opts || {});
    options.headers = buildHeaders();
    setBusy(1);
    return fetch('/admin/api/' + path, options)
      .then(function (r) {
        return r.text().then(function (txt) {
          let data = null;
          try {
            data = txt ? JSON.parse(txt) : null;
          } catch (e) {
            data = { raw: txt };
          }
          return { ok: r.ok, status: r.status, body: data };
        });
      })
      .finally(function () {
        setBusy(-1);
      });
  }

  function post(path, payload) {
    return api(path, { method: 'POST', body: JSON.stringify(payload || {}) });
  }

  function errMsg(res) {
    if (!res) return '未知错误';
    if (res.body && typeof res.body === 'object') {
      if (res.body.error) {
        return typeof res.body.error === 'string'
          ? res.body.error
          : (res.body.error.message || JSON.stringify(res.body.error));
      }
      if (res.body.message) return res.body.message;
    }
    return 'HTTP ' + res.status;
  }

  /* ---------- Bottom-Right Studio Toaster ---------- */
  function toast(title, desc, tone) {
    const stack = $('toastStack');
    if (!stack) return;
    const item = document.createElement('div');
    item.className = 'toast';
    const dotState = tone === 'error' ? 'danger' : (tone === 'warn' ? 'warn' : 'ok');
    item.innerHTML =
      '<span class="status-dot" data-state="' + dotState + '" style="margin-top:5px"></span>' +
      '<div class="toast-body">' +
        '<div class="toast-title">' + esc(title) + '</div>' +
        (desc ? '<div class="toast-desc">' + esc(desc) + '</div>' : '') +
      '</div>';
    stack.appendChild(item);
    setTimeout(function () {
      item.style.opacity = '0';
      item.style.transform = 'translateX(14px)';
      item.style.transition = 'opacity 160ms ease, transform 160ms ease';
      setTimeout(function () {
        if (item.parentNode) item.parentNode.removeChild(item);
      }, 180);
    }, 3200);
  }

  /* ---------- Accessible Studio Modal ---------- */
  let activeModalClose = null;

  function closeModal(result) {
    const bd = $('modalBackdrop');
    if (bd) bd.hidden = true;
    const cb = activeModalClose;
    activeModalClose = null;
    if (cb) cb(result);
  }

  function confirmModal(opts) {
    return new Promise(function (resolve) {
      const bd = $('modalBackdrop');
      $('modalTitle').textContent = opts.title || '确认操作';
      $('modalBody').innerHTML = '<p class="muted" style="line-height:1.6">' + esc(opts.description || '') + '</p>';
      const btnCls = opts.danger ? 'btn danger sm' : 'btn primary sm';
      $('modalFoot').innerHTML =
        '<button type="button" class="btn ghost sm" id="mCancelBtn">取消</button>' +
        '<button type="button" class="' + btnCls + '" id="mOkBtn">' + esc(opts.confirmText || '确认') + '</button>';
      activeModalClose = resolve;
      bd.hidden = false;
      $('mCancelBtn').onclick = function () { closeModal(false); };
      $('mOkBtn').onclick = function () { closeModal(true); };
      $('mOkBtn').focus();
    });
  }

  function formModal(opts) {
    return new Promise(function (resolve) {
      const bd = $('modalBackdrop');
      $('modalTitle').textContent = opts.title || '填写信息';
      let html = '';
      if (opts.description) {
        html += '<p class="muted" style="font-size:12px;line-height:1.5">' + esc(opts.description) + '</p>';
      }
      (opts.fields || []).forEach(function (f) {
        html += '<div class="field">' +
          '<label for="mf_' + esc(f.name) + '">' + esc(f.label) + '</label>' +
          '<input type="' + esc(f.type || 'text') + '" id="mf_' + esc(f.name) + '" data-mf="' + esc(f.name) + '" value="' + esc(f.defaultValue || '') + '" placeholder="' + esc(f.placeholder || '') + '" />' +
        '</div>';
      });
      $('modalBody').innerHTML = html;
      $('modalFoot').innerHTML =
        '<button type="button" class="btn ghost sm" id="mCancelBtn">取消</button>' +
        '<button type="button" class="btn primary sm" id="mOkBtn">' + esc(opts.confirmText || '保存') + '</button>';
      activeModalClose = resolve;
      bd.hidden = false;
      const submit = function () {
        const out = {};
        $('modalBody').querySelectorAll('[data-mf]').forEach(function (inp) {
          out[inp.dataset.mf] = inp.value;
        });
        closeModal(out);
      };
      $('mCancelBtn').onclick = function () { closeModal(null); };
      $('mOkBtn').onclick = submit;
      const firstInput = $('modalBody').querySelector('input');
      if (firstInput) {
        firstInput.focus();
        firstInput.select();
        firstInput.addEventListener('keydown', function (ev) {
          if (ev.key === 'Enter') {
            ev.preventDefault();
            submit();
          }
        });
      }
    });
  }

  /* ---------- 12-Segment Runtime Meter (cli2api signature) ---------- */
  function renderMeter(percent, tone) {
    const clamped = Math.max(0, Math.min(100, Number(percent || 0)));
    const filled = Math.round((clamped / 100) * 12);
    let segs = '';
    for (let i = 0; i < 12; i++) {
      const on = i < filled ? ' data-on="true" data-tone="' + esc(tone) + '"' : '';
      segs += '<span class="runtime-meter__seg"' + on + '></span>';
    }
    return '<span class="runtime-meter" aria-hidden="true">' + segs + '</span>';
  }

  /* ---------- Navigation, Sidebar & Theme ---------- */
  function switchTab(tab) {
    if (!PAGE_META[tab]) return;
    state.tab = tab;
    document.querySelectorAll('[data-sidebar-item]').forEach(function (btn) {
      btn.classList.toggle('sidebar-item-active', btn.dataset.tab === tab);
    });
    document.querySelectorAll('.tab-view').forEach(function (sec) {
      sec.classList.toggle('active', sec.id === 'tab-' + tab);
    });
    $('pageTitle').textContent = PAGE_META[tab].title;
    $('pageDesc').textContent = PAGE_META[tab].desc;
    if (tab === 'keys') loadKeys();
    closeMobileDrawer();
  }

  function closeMobileDrawer() {
    $('sidebar').classList.remove('mobile-open');
    $('sidebarBackdrop').classList.remove('open');
  }

  function switchCfgSub(sub) {
    state.cfgSub = sub;
    document.querySelectorAll('#configSubtabs .seg-btn').forEach(function (b) {
      b.classList.toggle('active', b.dataset.cfgsub === sub);
    });
    ['editor', 'snapshots'].forEach(function (k) {
      const pane = $('cfg-sub-' + k);
      if (pane) pane.classList.toggle('active', k === sub);
    });
  }

  function pushSparkSample(totalReqs) {
    const num = Number(totalReqs || 0);
    let delta = 0;
    if (state.lastRequests !== null && num >= state.lastRequests) {
      delta = num - state.lastRequests;
    }
    state.lastRequests = num;
    state.sparkSamples.push(delta);
    if (state.sparkSamples.length > 20) state.sparkSamples.shift();
    renderSparkline();
  }

  function renderSparkline() {
    const pts = state.sparkSamples;
    const pathEl = $('liveSparkPath');
    const areaEl = $('liveSparkArea');
    if (!pathEl || !areaEl || !pts.length) return;
    const w = 112;
    const h = 16;
    const max = Math.max(1, Math.max.apply(null, pts));
    const step = pts.length > 1 ? w / (pts.length - 1) : w;
    const coords = pts.map(function (v, idx) {
      const x = (2 + idx * step).toFixed(1);
      const y = (19 - (v / max) * h).toFixed(1);
      return [x, y];
    });
    if (coords.length === 1) {
      coords.push(['114.0', coords[0][1]]);
    }
    const line = coords.map(function (c, i) { return (i === 0 ? 'M ' : 'L ') + c[0] + ' ' + c[1]; }).join(' ');
    const area = line + ' L ' + coords[coords.length - 1][0] + ' 20 L ' + coords[0][0] + ' 20 Z';
    pathEl.setAttribute('d', line);
    areaEl.setAttribute('d', area);
  }

  function setConnStatus(ok, errText) {
    const stateVal = ok ? 'ok' : 'danger';
    $('headerStatusDot').setAttribute('data-state', stateVal);
    $('sidebarDot').setAttribute('data-state', stateVal);
    $('liveStripDot').setAttribute('data-state', stateVal);
    $('sidebarStateText').textContent = ok ? '运行中' : (errText || '连接中断');
    $('sidebarProxyState').textContent = ok ? 'online' : 'offline';
  }

  function loadStatus() {
    api('status')
      .then(function (r) {
        if (!r.ok) {
          setConnStatus(false, r.status === 401 ? '需要密钥' : ('HTTP ' + r.status));
          if (r.status === 401) {
            $('tokenPopover').hidden = false;
          }
          return;
        }
        state.status = r.body;
        setConnStatus(true);
        renderStatus(r.body);
      })
      .catch(function () {
        setConnStatus(false, '离线');
      });

    api('logs?limit=200')
      .then(function (r) {
        if (r.ok && r.body) {
          state.logs = r.body.logs || [];
          renderLogs();
        }
      })
      .catch(function () {});
  }

/* ---------- Channel Account Pool Cards ---------- */
  function accountStateInfo(k) {
    const coolingLeft = Number(k.cooldown_until || 0) - Math.floor(Date.now() / 1000);
    const score = Number(k.score || 0);
    if (coolingLeft > 0) return { tone: 'warn', label: '冷却 ' + coolingLeft + 's' };
    if (k.state && k.state !== 'ok') return { tone: 'warn', label: String(k.state) };
    if (score < 40) return { tone: 'danger', label: '低分' };
    if (score < 70) return { tone: 'warn', label: '观察中' };
    return { tone: 'ok', label: '可用' };
  }

  function renderAccountCards(d) {
    const grid = $('accountCards');
    if (!grid) return;
    const ups = d.upstreams || [];
    const cards = [];
    let total = 0;
    let activeUps = 0;

    ups.forEach(function (u) {
      const keys = u.keys || [];
      if (keys.length) activeUps++;
      keys.forEach(function (k, idx) {
        total++;
        const info = accountStateInfo(k);
        const reqs = Number(k.requests || 0);
        const okCount = Number(k.successes || 0);
        const rate = reqs > 0 ? Math.round((okCount / reqs) * 100) + '%' : '—';
        const latency = Number(k.latency_ms || 0) > 0 ? (Number(k.latency_ms) / 1000).toFixed(1) + 's' : '—';
        const coolingLeft = Math.max(0, Number(k.cooldown_until || 0) - Math.floor(Date.now() / 1000));
        const chipTone = info.tone === 'ok' ? 'success' : (info.tone === 'danger' ? 'danger' : 'warning');
        cards.push(
          '<article class="account-card">' +
            '<header class="account-head">' +
              '<span class="status-dot" data-state="' + info.tone + '"></span>' +
              '<div class="account-id">' +
                '<strong>账号 #' + (idx + 1) + '</strong>' +
                '<span class="mono">' + esc(k.masked || '-') + '</span>' +
              '</div>' +
              '<span class="chip ' + chipTone + '">' + esc(info.label) + '</span>' +
            '</header>' +
            '<div class="account-channel">' + esc(u.name || u.id || '') + '</div>' +
            '<div class="account-score">' +
              '<strong>' + Math.round(Number(k.score || 0)) + '</strong>' +
              renderMeter(k.score, info.tone) +
            '</div>' +
            '<dl class="account-stats">' +
              '<div><dt>请求</dt><dd>' + reqs + '</dd></div>' +
              '<div><dt>成功率</dt><dd>' + rate + '</dd></div>' +
              '<div><dt>延迟</dt><dd>' + latency + '</dd></div>' +
              '<div><dt>冷却</dt><dd>' + (coolingLeft > 0 ? coolingLeft + 's' : '—') + '</dd></div>' +
            '</dl>' +
          '</article>'
        );
      });
    });

    grid.innerHTML = cards.join('');
    const summary = $('accountPoolSummary');
    if (summary) summary.textContent = total + ' 个账号 · ' + activeUps + ' 个渠道';
  }

  function renderStatus(d) {
    const s = d.stats || {};
    const ups = d.upstreams || [];

    if (d.version) $('versionBadge').textContent = 'v' + d.version;
    if (d.bind === '0.0.0.0' || d.bind === '::' || d.bind === '' || !d.bind) {
      /* 通配监听时客户端实际要用浏览器访问的地址，而不是 0.0.0.0 */
      state.endpointUrl = location.origin + '/v1';
    } else {
      state.endpointUrl = d.endpoint || ('http://' + (d.bind || '127.0.0.1') + ':' + (d.port || 7878) + '/v1');
    }
    $('endpointText').textContent = state.endpointUrl.replace(/^https?:\/\//, '');

    let healthyUps = 0;
    let totalInflight = 0;
    ups.forEach(function (u) {
      if (u.enabled && u.healthy && !u.cooldown_until) healthyUps++;
      totalInflight += Number(u.inflight || 0);
    });

    /* Sidebar counters & live status */
    $('navUpstreamCount').textContent = String(ups.length);

    $('sidebarUpChip').textContent = healthyUps + '/' + ups.length;
    $('sidebarUpChip').className = 'chip mono ' + (ups.length === 0 || healthyUps > 0 ? 'success' : 'warning');
    $('sidebarUpMeta').textContent = healthyUps + '/' + ups.length;
    $('sidebarInflightNum').textContent = String(totalInflight);
    $('sidebarInflightDot').setAttribute('data-state', totalInflight > 0 ? 'ok' : '');

    /* LiveStrip */
    $('liveInflight').textContent = String(totalInflight);
    $('liveLoadBar').style.width = Math.min(100, totalInflight * 12) + '%';
    $('liveKeyStrategy').textContent = d.key_strategy || 'round_robin';
    $('liveAuthState').textContent = d.auth ? '已开启' : '开放访问';
    pushSparkSample(s.requests || 0);

    /* Primary 4 KPI Cards */
    const totalReq = Number(s.requests || 0);
    const profInj = Number(s.injected || 0);
    const ruleInj = Number(s.rule_inject || 0);
    const passthrough = Number(s.no_profile || 0);
    const denied = Number(s.rule_deny || 0);
    const upErr = Number(s.upstream_errors || 0);
    const authErr = Number(s.auth_failures || 0);
    const totalGuarded = denied + upErr + authErr;

    $('kpiRequests').textContent = formatCompact(totalReq);
    $('kpiUptimeFoot').textContent = '运行时长 ' + (d.uptime || '—') + ' · ' + (d.bind || '127.0.0.1') + ':' + (d.port || 7878);
    $('kpiUpstreams').textContent = healthyUps + ' / ' + ups.length;
    $('dotUpstreams').setAttribute('data-state', ups.length === 0 || healthyUps > 0 ? 'ok' : 'danger');
    $('kpiRoutingFoot').textContent = '上游 ' + ups.length + ' 个 · 健康 ' + healthyUps + ' 个';

    $('kpiInjected').textContent = formatCompact(profInj + ruleInj);
    $('kpiInjectFoot').textContent = '策略 ' + profInj + ' · 规则 ' + ruleInj + ' · 透传 ' + passthrough;

    $('kpiErrors').textContent = formatCompact(totalGuarded);
    $('dotErrors').setAttribute('data-state', upErr > 0 ? 'warn' : 'ok');
    $('kpiErrorFoot').textContent = '拦截 ' + denied + ' · 上游错 ' + upErr + ' · 鉴权错 ' + authErr;

    /* Secondary 3 Metric Cards */
    const okRate = totalReq > 0 ? Math.max(0, Math.round(((totalReq - upErr - authErr) / totalReq) * 100)) : 100;
    $('secHealthRate').textContent = okRate + '%';
    $('secHealthDetail').textContent = '总计 ' + totalReq + ' 次请求 · 上游错误 ' + upErr;
    $('secKeySkips').textContent = formatCompact(s.key_skips || 0);
    $('secKeySkipDetail').textContent = '选路策略 ' + (d.key_strategy || 'round_robin');
    const persistOn = Boolean(d.persist && d.persist.enabled);
    $('secPersistState').textContent = persistOn ? '已开启' : '未开启';
    $('secPersistDetail').textContent = persistOn ? '状态自动落盘保护' : '重启后重置内存计数';

    /* Governance Distribution Bar */
    const distItems = [
      { label: '策略注入', count: profInj, color: 'var(--cyan)' },
      { label: '规则注入', count: ruleInj, color: 'var(--violet)' },
      { label: '原样透传', count: passthrough, color: 'var(--success)' },
      { label: '规则拦截', count: denied, color: 'var(--warning)' },
      { label: '异常与鉴权失败', count: upErr + authErr, color: 'var(--danger)' }
    ];
    const distSum = distItems.reduce(function (acc, it) { return acc + it.count; }, 0);
    if (distSum === 0) {
      $('trafficDistBar').innerHTML = '<div class="dist-seg" style="width:100%;background:var(--separator)"></div>';
    } else {
      $('trafficDistBar').innerHTML = distItems
        .filter(function (it) { return it.count > 0; })
        .map(function (it) {
          const pct = Math.max(1.5, (it.count / distSum) * 100).toFixed(1);
          return '<div class="dist-seg" style="width:' + pct + '%;background:' + it.color + '" title="' + esc(it.label) + ': ' + it.count + '"></div>';
        })
        .join('');
    }
    $('trafficDistLegend').innerHTML = distItems
      .map(function (it) {
        const pct = distSum ? Math.round((it.count / distSum) * 100) + '%' : '0%';
        return '<div class="legend-row">' +
          '<span class="legend-left"><i class="legend-swatch" style="background:' + it.color + '"></i>' + esc(it.label) + '</span>' +
          '<span class="mono tabular fg-strong">' + it.count + ' <span class="muted">(' + pct + ')</span></span>' +
        '</div>';
      })
      .join('');

    /* Retry & Circuit Config */
    const rt = d.retry || {};
    const retryPairs = [
      ['最大重试次数', (rt.max_retries ?? 0) + ' 次'],
      ['熔断失败阈值', (rt.failure_threshold ?? 0) + ' 次'],
      ['初始冷却时长', (rt.cooldown_seconds ?? 0) + ' s'],
      ['单次请求超时', (rt.request_timeout_seconds ?? 0) + ' s'],
      ['半开探测上限', (rt.half_open_limit ?? 1) + ' 并发'],
      ['重试状态码', (rt.retry_status || []).join(', ') || '—']
    ];
    $('retryGrid').innerHTML = retryPairs
      .map(function (p) {
        return '<div class="kv-item"><span class="kv-k">' + esc(p[0]) + '</span><span class="kv-v mono">' + esc(p[1]) + '</span></div>';
      })
      .join('');

    renderExperiments(d);
    renderUpstreams(ups);
    renderAccountCards(d);
  }

  /* ---------- Experiments & Federation ---------- */
  function renderExperiments(d) {
    const tbody = $('expTable').querySelector('tbody');
    const exps = d.experiments || [];
    const stats = d.experiment_stats || {};
    const rows = [];
    exps.forEach(function (exp) {
      (exp.arms || []).forEach(function (arm) {
        const st = (stats[exp.id] || {})[arm.id] || { count: 0, errors: 0, latency_ms_sum: 0, tokens_sum: 0 };
        const avg = st.count ? Math.round(st.latency_ms_sum / st.count) : 0;
        rows.push(
          '<tr class="hoverable">' +
            '<td><span class="mono fg-strong">' + esc(exp.id) + '</span>' + (exp.name ? ' <span class="muted">· ' + esc(exp.name) + '</span>' : '') + '</td>' +
            '<td><span class="mono">' + esc(arm.id) + '</span> <span class="chip soft mono">w=' + (arm.weight || 1) + '</span></td>' +
            '<td class="num mono">' + st.count + '</td>' +
            '<td class="num mono">' + (st.errors ? '<span style="color:var(--danger)">' + st.errors + '</span>' : '0') + '</td>' +
            '<td class="num mono">' + (avg ? avg + ' ms' : '—') + '</td>' +
            '<td class="num mono">' + formatCompact(st.tokens_sum || 0) + '</td>' +
          '</tr>'
        );
      });
    });
    tbody.innerHTML = rows.join('') || '<tr><td colspan="6" class="empty-state">暂未配置 A/B 实验（可在 config.experiments 中声明）</td></tr>';
  }

  function loadFederation() {
    api('federation')
      .then(function (r) {
        if (r.ok && r.body) renderFederation(r.body);
      })
      .catch(function () {});
  }

  function renderFederation(d) {
    const tbody = $('fedTable').querySelector('tbody');
    const peers = d.peers || [];
    const rows = peers.map(function (p) {
      const chip = p.ok ? '<span class="chip success">在线</span>' : '<span class="chip danger">离线</span>';
      return '<tr class="hoverable">' +
        '<td><div class="mono fg-strong">' + esc(p.id) + '</div><div class="muted mono" style="font-size:11.5px">' + esc(p.base_url) + '</div></td>' +
        '<td>' + chip + '</td>' +
        '<td class="mono">' + esc(p.version || '—') + '</td>' +
        '<td class="mono">' + esc(p.uptime || '—') + '</td>' +
        '<td class="num mono">' + (p.requests || 0) + '</td>' +
        '<td class="num mono">' + (p.rule_denies || 0) + '</td>' +
        '<td class="num mono">' + (p.upstreams || 0) + (p.unhealthy ? ' <span style="color:var(--danger)">(' + p.unhealthy + ')</span>' : '') + '</td>' +
        '<td class="num mono">' + (p.rules || 0) + '</td>' +
        '<td class="num mono">' + (p.latency_ms ? p.latency_ms + ' ms' : '—') + '</td>' +
      '</tr>' +
      (p.error ? '<tr><td colspan="9" class="mono" style="color:var(--danger);font-size:12px">' + esc(p.error) + '</td></tr>' : '');
    }).join('');
    tbody.innerHTML = rows || '<tr><td colspan="9" class="empty-state">暂未配置联邦节点（config.federation.peers）</td></tr>';
    $('fedHint').textContent =
      (d.enabled ? '联邦已启用' : '联邦未启用') +
      ' · 节点 ' + (d.peer_count || 0) +
      ' · 在线 ' + (d.peer_healthy || 0) +
      ' · 累计跨域请求 ' + (d.peer_requests || 0);
  }

  /* ---------- Upstreams & Expandable 12-Seg Key Pool ---------- */
  function upstreamStatusInfo(u) {
    if (!u.enabled) return { kind: 'off', chip: 'soft', text: '已停用' };
    if (u.cooldown_until) {
      const left = Math.max(0, u.cooldown_until - Math.floor(Date.now() / 1000));
      return { kind: 'issue', chip: 'warning', text: left > 0 ? ('冷却 ' + left + 's') : '冷却中' };
    }
    if (u.healthy) return { kind: 'ok', chip: 'success', text: '健康' };
    return { kind: 'issue', chip: 'danger', text: '异常' };
  }

  function renderUpstreams(list) {
    const tbody = $('upTable').querySelector('tbody');
    const q = state.upstreamQuery.toLowerCase();
    const filtered = (list || []).filter(function (u) {
      const st = upstreamStatusInfo(u);
      if (state.upstreamFilter !== 'all' && st.kind !== state.upstreamFilter) return false;
      if (!q) return true;
      const hay = [u.id, u.name, u.base_url || '', (u.models || []).join(' ')].join(' ').toLowerCase();
      return hay.indexOf(q) >= 0;
    });

    if (!filtered.length) {
      tbody.innerHTML = '<tr><td colspan="9" class="empty-state">' + (list && list.length ? '没有匹配筛选条件的上游节点' : '尚未配置任何上游节点') + '</td></tr>';
      return;
    }

    let html = '';
    filtered.forEach(function (u) {
      const st = upstreamStatusInfo(u);
      const cState = u.circuit || 'closed';
      const cChip = cState === 'closed' ? 'success' : (cState === 'half_open' ? 'warning' : 'danger');
      const expanded = state.expandedUpstreams.has(u.id);
      const keys = u.keys || [];
      const modelsText = (u.models || []).slice(0, 4).join(', ') + ((u.models || []).length > 4 ? ' +' + ((u.models || []).length - 4) : '');

      let budgetHtml = '';
      if (u.budget) {
        const b = u.budget;
        const parts = [];
        if (b.max_requests) parts.push('请求 ' + b.requests + '/' + b.max_requests);
        if (b.max_tokens) parts.push('Token ' + formatCompact(b.tokens) + '/' + formatCompact(b.max_tokens));
        budgetHtml = '<div class="budget-mini mono">' +
          (b.exhausted ? '<span class="chip danger">额度耗尽</span>' : '<span class="chip soft">日预算</span>') +
          '<span>' + esc(parts.join(' · ')) + '</span></div>';
      }

      html += '<tr class="hoverable" data-up-row="' + esc(u.id) + '">' +
        '<td><button type="button" class="expand-btn" data-toggle-up="' + esc(u.id) + '" aria-expanded="' + (expanded ? 'true' : 'false') + '" title="展开 Key 池明细">' +
          '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><polyline points="9 18 15 12 9 6"/></svg>' +
        '</button></td>' +
        '<td>' +
          '<div class="fg-strong">' + esc(u.name || u.id) + ' <span class="muted mono" style="font-weight:400;font-size:12px">' + esc(u.id) + '</span></div>' +
          (u.base_url ? '<div class="muted mono" style="font-size:11.5px">' + esc(u.base_url) + '</div>' : '') +
        '</td>' +
        '<td><span class="chip ' + st.chip + '">' + esc(st.text) + '</span></td>' +
        '<td><span class="chip ' + cChip + ' mono">' + esc(cState) + '</span></td>' +
        '<td class="mono"><span class="chip soft">P' + (u.priority ?? 0) + '</span> ' + (u.weight ? '<span class="chip cyan">w=' + u.weight + '</span>' : '<span class="muted">均分</span>') + '</td>' +
        '<td class="num mono">' + (u.requests || 0) + ' / <span style="color:var(--success)">' + (u.successes || 0) + '</span> / <span style="color:' + (u.failures ? 'var(--danger)' : 'var(--muted)') + '">' + (u.failures || 0) + '</span></td>' +
        '<td class="num mono">' + (u.inflight || 0) + '</td>' +
        '<td class="mono">' + (u.latency_ms ? u.latency_ms + ' ms' : '—') +
          (u.last_status ? ' · HTTP ' + u.last_status : '') +
          (u.last_error ? '<div class="muted" style="font-size:11px;max-width:220px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" title="' + esc(u.last_error) + '">' + esc(u.last_error) + '</div>' : '') +
        '</td>' +
        '<td><div class="mono muted" style="font-size:12px">' + esc(modelsText || '通配') + '</div>' + budgetHtml + '</td>' +
      '</tr>';

      if (expanded) {
        html += '<tr class="keypool-row"><td colspan="9"><div class="keypool-drawer">';
        if (!keys.length) {
          html += '<div class="muted mono" style="font-size:12px">匿名上游（未配置 API Key 池）</div>';
        } else {
          html += '<div class="keypool-head"><span>共 ' + keys.length + ' 把密钥参与选路</span><span>分数低于阈值或连续失败自动进入冷却</span></div>' +
            '<table class="keypool-table"><thead><tr>' +
              '<th>密钥掩码</th><th>健康分 (0-100)</th><th>状态</th><th class="num">成功 / 失败</th><th>最近状态</th><th class="num">延迟</th>' +
            '</tr></thead><tbody>' +
            keys.map(function (k) {
              const tone = k.score >= 60 ? 'ok' : (k.score >= 30 ? 'warn' : 'danger');
              const kChip = k.state === 'ok' ? '<span class="chip success">健康</span>'
                : (k.state === 'degraded' ? '<span class="chip warning">降权</span>'
                : (k.state === 'bad' ? '<span class="chip danger">低分</span>' : '<span class="chip warning">冷却中</span>'));
              return '<tr>' +
                '<td class="mono fg-strong">' + esc(k.masked) + '</td>' +
                '<td><span class="meter-cell">' + renderMeter(k.score, tone) + '<span class="mono tabular">' + Math.round(k.score) + '</span></span></td>' +
                '<td>' + kChip + '</td>' +
                '<td class="num mono">' + (k.successes || 0) + ' / ' + (k.failures || 0) + '</td>' +
                '<td class="mono">' + (k.last_status || '—') + '</td>' +
                '<td class="num mono">' + (k.latency_ms ? k.latency_ms + ' ms' : '—') + '</td>' +
              '</tr>';
            }).join('') +
            '</tbody></table>';
        }
        html += '</div></td></tr>';
      }
    });

    tbody.innerHTML = html;
    tbody.querySelectorAll('[data-toggle-up]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        const id = btn.dataset.toggleUp;
        if (state.expandedUpstreams.has(id)) state.expandedUpstreams.delete(id);
        else state.expandedUpstreams.add(id);
        renderUpstreams((state.status && state.status.upstreams) || []);
      });
    });
  }

  /* ---------- Channels & Browser CDP Login ---------- */
  function loadChannels() {
    api('channels')
      .then(function (r) {
        if (!r.ok) return;
        state.channels = r.body || { presets: [], channels: [] };
        renderChannels();
      })
      .catch(function () {});
  }

  function renderChannels() {
    const presets = state.channels.presets || [];
    const configured = state.channels.channels || [];
    $('navChannelCount').textContent = String(configured.length);

    /* Configured Channels Table */
    const cbody = $('channelTable').querySelector('tbody');
    if (!configured.length) {
      cbody.innerHTML = '<tr><td colspan="9" class="empty-state">尚未添加账号渠道，可从下方模板库选择「网页登录」或「手动配置」接入</td></tr>';
    } else {
      cbody.innerHTML = configured.map(function (c) {
        return '<tr class="hoverable">' +
          '<td class="mono fg-strong">' + esc(c.id) + '</td>' +
          '<td>' + esc(c.name) + '</td>' +
          '<td class="mono muted">' + esc(c.preset) + '</td>' +
          '<td class="mono">' + esc(c.has_token ? c.token_masked : '未填写') + ((c.key_count || 0) > 1 ? ' <span class="chip soft">' + c.key_count + ' 个账号</span>' : '') + '</td>' +
          '<td>' + (c.enabled ? '<span class="chip success">已启用</span>' : '<span class="chip soft">已停用</span>') + '</td>' +
          '<td class="num mono">' + (c.priority || 0) + '</td>' +
          '<td class="num mono">' + (c.weight || 0) + '</td>' +
          '<td class="mono muted">' + esc((c.models || []).join(', ') || '模板默认') + '</td>' +
          '<td><div class="row-actions">' +
            '<button type="button" class="btn outline xs" data-test-channel="' + esc(c.id) + '">探活</button>' +
            '<button type="button" class="btn danger-soft xs" data-del-channel="' + esc(c.id) + '">删除</button>' +
          '</div></td>' +
        '</tr>';
      }).join('');

      cbody.querySelectorAll('[data-test-channel]').forEach(function (b) {
        b.addEventListener('click', function () { testChannel(b.dataset.testChannel); });
      });
      cbody.querySelectorAll('[data-del-channel]').forEach(function (b) {
        b.addEventListener('click', function () { removeChannel(b.dataset.delChannel); });
      });
    }

    /* Presets Table */
    const q = state.presetQuery.toLowerCase();
    const pbody = $('presetTable').querySelector('tbody');
    const filteredPresets = presets.filter(function (p) {
      if (state.presetProto === 'openai' && p.protocol !== 'openai') return false;
      if (state.presetProto === 'adapter' && p.protocol === 'openai') return false;
      if (!q) return true;
      const hay = [p.id, p.vendor, p.name, p.notes || '', p.base_url].join(' ').toLowerCase();
      return hay.indexOf(q) >= 0;
    });

    pbody.innerHTML = filteredPresets.map(function (p) {
      const protoChip = p.protocol === 'openai'
        ? '<span class="chip success mono">openai</span>'
        : '<span class="chip warning mono">needs_adapter</span>';
      return '<tr class="hoverable">' +
        '<td class="mono fg-strong">' + esc(p.id) + '</td>' +
        '<td class="mono muted">' + esc(p.vendor) + '</td>' +
        '<td>' + protoChip + '</td>' +
        '<td class="mono muted" style="font-size:12px">' + esc((p.base_url || '') + (p.chat_path || '')) + '</td>' +
        '<td class="muted">' + esc(p.name + (p.notes ? ' · ' + p.notes : '')) + '</td>' +
        '<td><div class="row-actions">' +
          '<button type="button" class="btn primary xs" data-login-channel="' + esc(p.id) + '">网页登录</button>' +
          '<button type="button" class="btn secondary xs" data-add-channel="' + esc(p.id) + '">手动配置</button>' +
        '</div></td>' +
      '</tr>';
    }).join('') || '<tr><td colspan="6" class="empty-state">没有匹配的内置渠道模板</td></tr>';

    pbody.querySelectorAll('[data-login-channel]').forEach(function (b) {
      b.addEventListener('click', function () { startChannelLogin(b.dataset.loginChannel); });
    });
    pbody.querySelectorAll('[data-add-channel]').forEach(function (b) {
      b.addEventListener('click', function () { openAddChannelModal(b.dataset.addChannel); });
    });
  }

  function openAddChannelModal(presetId) {
    const preset = (state.channels.presets || []).find(function (p) { return p.id === presetId; }) || { id: presetId, name: presetId };
    formModal({
      title: '配置渠道 · ' + (preset.name || presetId),
      description: '保存后将自动作为上游节点 ch-' + presetId + ' 参与网关选路（匿名渠道可填 public）。',
      confirmText: '保存渠道',
      fields: [
        { name: 'id', label: '渠道 ID', defaultValue: presetId, placeholder: presetId },
        { name: 'name', label: '显示名称', defaultValue: preset.name || presetId, placeholder: '自定义名称' },
        { name: 'token', label: '访问凭证 (Token / Cookie)', defaultValue: '', placeholder: '粘贴 Bearer Token 或完整 Cookie' },
        { name: 'priority', label: '路由优先级（数字越小越优先）', type: 'number', defaultValue: '0' },
        { name: 'weight', label: '同级分流权重（0 为均分）', type: 'number', defaultValue: '0' }
      ]
    }).then(function (vals) {
      if (!vals) return;
      post('channels', {
        preset: presetId,
        id: (vals.id || presetId).trim(),
        name: (vals.name || '').trim(),
        token: (vals.token || 'public').trim(),
        priority: parseInt(vals.priority || '0', 10) || 0,
        weight: parseInt(vals.weight || '0', 10) || 0
      }).then(function (r) {
        if (r.ok) {
          toast('渠道已保存', '上游节点 ' + (r.body.upstream_id || '') + ' 已就绪', 'success');
          loadChannels();
          loadStatus();
        } else {
          toast('保存渠道失败', errMsg(r), 'error');
        }
      });
    });
  }

  function startChannelLogin(presetId) {
    post('channels/login/start', { preset: presetId }).then(function (r) {
      if (!r.ok) {
        toast('启动网页登录失败', errMsg(r), 'error');
        return;
      }
      loginState.id = r.body.id;
      $('loginPanel').hidden = false;
      $('loginTitle').textContent = '网页登录：' + presetId;
      $('loginHint').textContent = (r.body.login_hint ? r.body.login_hint + ' · ' : '') + '已通过 ' + (r.body.browser || 'Chromium') + ' 打开登录窗口';
      $('loginStatus').textContent = '等待浏览器登录完成...';
      if (loginState.timer) clearInterval(loginState.timer);
      loginState.timer = setInterval(pollChannelLogin, 1500);
      pollChannelLogin();
    });
  }

  function pollChannelLogin() {
    if (!loginState.id) return;
    api('channels/login/status?id=' + encodeURIComponent(loginState.id)).then(function (r) {
      if (!r.ok) return;
      const d = r.body || {};
      const el = $('loginStatus');
      if (!d.active) {
        el.textContent = '登录会话已关闭';
        return;
      }
      if (d.error) {
        el.textContent = '读取会话存储异常：' + d.error;
        return;
      }
      loginState.tokenKey = d.token_key || '';
      const parts = [];
      if (d.cookie_count) parts.push('Cookie ' + d.cookie_count + ' 项 (' + (d.cookie_names || []).slice(0, 5).join(', ') + ')');
      if (d.storage_keys && d.storage_keys.length) parts.push('Storage ' + d.storage_keys.length + ' 项');
      if (d.token_key) parts.push('识别凭证键 ' + d.token_key + ' ' + (d.token_key_mask || ''));
      el.textContent = (d.ready ? '✓ 已捕获有效凭证：' : '等待登录态写入：') + (parts.join(' · ') || '监听中...');
    });
  }

  function stopChannelLogin() {
    if (loginState.timer) {
      clearInterval(loginState.timer);
      loginState.timer = null;
    }
    loginState.id = null;
  }

  function testChannel(id) {
    post('channels/test', { id: id }).then(function (r) {
      const drawer = $('channelOut');
      const pre = $('channelOutPre');
      drawer.hidden = false;
      const d = r.body || {};
      if (d.skipped) {
        pre.textContent = '跳过探活：' + (d.message || '');
        toast('该渠道不支持模型探活', d.message || '', 'warn');
        return;
      }
      pre.textContent =
        '探活结果：' + (d.ok ? '成功' : '失败') +
        ' · HTTP ' + (d.status || '—') +
        ' · 耗时 ' + (d.latency_ms || 0) + ' ms\n' +
        (d.url ? '探测地址：' + d.url + '\n' : '') +
        (d.body || d.message || '');
      toast(d.ok ? '渠道探活通过' : '渠道探活异常', id + ' · ' + (d.latency_ms || 0) + ' ms', d.ok ? 'success' : 'error');
    });
  }

  function removeChannel(id) {
    confirmModal({
      title: '删除账号渠道',
      description: '确定要移除渠道「' + id + '」及其关联上游 ch-' + id + ' 吗？修改前会自动创建配置快照。',
      confirmText: '确认删除',
      danger: true
    }).then(function (ok) {
      if (!ok) return;
      api('channels?id=' + encodeURIComponent(id), { method: 'DELETE' }).then(function (r) {
        if (r.ok) {
          toast('渠道已移除', id, 'success');
          loadChannels();
          loadStatus();
        } else {
          toast('删除失败', errMsg(r), 'error');
        }
      });
    });
  }

  /* ---------- Rules Pipeline Canvas & Heat ---------- */
  function renderLogs() {
    const logs = state.logs || [];
    const errCount = logs.filter(function (l) { return l.level === 'ERR'; }).length;
    const badge = $('navLogBadge');
    badge.hidden = errCount === 0;
    badge.textContent = String(errCount);

    const q = state.logQuery.toLowerCase();
    const filtered = logs.slice().reverse().filter(function (l) {
      if (state.logLevel !== 'ALL' && l.level !== state.logLevel) return false;
      if (!q) return true;
      return String(l.message || '').toLowerCase().indexOf(q) >= 0 || String(l.time || '').toLowerCase().indexOf(q) >= 0;
    });

    $('logCountHint').textContent = filtered.length + ' / ' + logs.length + ' 条';
    const tbody = $('logsTable').querySelector('tbody');
    if (!filtered.length) {
      tbody.innerHTML = '<tr><td colspan="3" class="empty-state">没有匹配的运行日志</td></tr>';
      return;
    }

    tbody.innerHTML = filtered.map(function (l) {
      const lvChip = l.level === 'ERR' ? 'danger' : (l.level === 'WARN' ? 'warning' : 'soft');
      return '<tr class="hoverable">' +
        '<td class="mono muted">' + esc(l.time) + '</td>' +
        '<td><span class="chip ' + lvChip + ' mono">' + esc(l.level) + '</span></td>' +
        '<td><pre>' + esc(l.message) + '</pre></td>' +
      '</tr>';
    }).join('');
  }

  /* ---------- Config, Snapshots & Git ---------- */
  function showNotice(id, text, kind) {
    const el = $(id);
    if (!el) return;
    if (!text) {
      el.hidden = true;
      return;
    }
    el.hidden = false;
    el.className = 'inline-notice' + (kind ? ' ' + kind : '');
    el.textContent = text;
  }

  function loadConfig() {
    api('config').then(function (r) {
      if (!r.ok) {
        showNotice('cfgHint', '读取配置失败：' + errMsg(r), 'err');
        return;
      }
      $('cfgBox').value = JSON.stringify(r.body, null, 2);
      showNotice('cfgHint', '');
    });
  }

  function loadSnapshots() {
    api('snapshots').then(function (r) {
      if (!r.ok) return;
      const d = r.body || {};
      const list = d.snapshots || [];
      if (d.dir) {
        $('snapHint').textContent = '存储目录：' + d.dir + ' · 自动保留最近 ' + (d.limit || 0) + ' 个快照版本';
      }
      const tbody = $('snapTable').querySelector('tbody');
      if (!list.length) {
        tbody.innerHTML = '<tr><td colspan="7" class="empty-state">暂无配置快照记录</td></tr>';
        return;
      }
      tbody.innerHTML = list.map(function (s) {
        const when = new Date(s.time * 1000).toLocaleString();
        const size = (s.size / 1024).toFixed(1) + ' KB';
        return '<tr class="hoverable">' +
          '<td class="mono muted">' + esc(when) + '</td>' +
          '<td class="num mono">' + size + '</td>' +
          '<td class="num mono">' + (s.upstreams || 0) + '</td>' +
          '<td class="num mono">' + (s.profiles || 0) + '</td>' +
          '<td class="num mono">' + (s.rules || 0) + '</td>' +
          '<td class="mono fg-strong">' + esc(s.name) + '</td>' +
          '<td><div class="row-actions">' +
            '<button type="button" class="btn outline xs" data-snap-view="' + esc(s.name) + '">预览</button>' +
            '<button type="button" class="btn secondary xs" data-snap-roll="' + esc(s.name) + '">回滚</button>' +
            '<button type="button" class="btn danger-soft xs" data-snap-del="' + esc(s.name) + '">删除</button>' +
          '</div></td>' +
        '</tr>';
      }).join('');

      tbody.querySelectorAll('[data-snap-view]').forEach(function (b) {
        b.addEventListener('click', function () {
          const name = b.dataset.snapView;
          api('snapshots/get?name=' + encodeURIComponent(name)).then(function (res) {
            if (!res.ok) {
              toast('读取快照失败', errMsg(res), 'error');
              return;
            }
            $('cfgBox').value = JSON.stringify(res.body, null, 2);
            switchCfgSub('editor');
            showNotice('cfgHint', '正在预览历史快照「' + name + '」（尚未生效，点击「保存并热加载」才会覆盖当前配置）', '');
            toast('已载入快照预览', name, 'info');
          });
        });
      });

      tbody.querySelectorAll('[data-snap-roll]').forEach(function (b) {
        b.addEventListener('click', function () {
          const name = b.dataset.snapRoll;
          confirmModal({
            title: '回滚配置快照',
            description: '确定回滚到快照「' + name + '」吗？回滚前会自动备份当前配置。',
            confirmText: '立即回滚',
            danger: false
          }).then(function (ok) {
            if (!ok) return;
            post('snapshots/rollback', { name: name }).then(function (res) {
              if (res.ok) {
                toast('已回滚至快照', name, 'success');
                loadStatus();
                loadConfig();
                loadSnapshots();
                loadGitLog();
              } else {
                toast('回滚失败', errMsg(res), 'error');
              }
            });
          });
        });
      });

      tbody.querySelectorAll('[data-snap-del]').forEach(function (b) {
        b.addEventListener('click', function () {
          const name = b.dataset.snapDel;
          confirmModal({
            title: '删除配置快照',
            description: '确定永久删除快照文件「' + name + '」吗？',
            confirmText: '确认删除',
            danger: true
          }).then(function (ok) {
            if (!ok) return;
            post('snapshots/delete', { name: name }).then(function (res) {
              if (res.ok) {
                toast('快照已删除', name, 'success');
                loadSnapshots();
              } else {
                toast('删除失败', errMsg(res), 'error');
              }
            });
          });
        });
      });
    });
  }

  function loadGitLog() {
    api('snapshots/git/log?limit=20').then(function (r) {
      if (!r.ok) return;
      const d = r.body || {};
      const tbody = $('gitTable').querySelector('tbody');
      if (!d.available) {
        tbody.innerHTML = '<tr><td colspan="3" class="empty-state">宿主机未检测到 git 命令，快照版本库不可用</td></tr>';
        return;
      }
      const entries = d.entries || [];
      tbody.innerHTML = entries.map(function (e) {
        return '<tr class="hoverable">' +
          '<td class="mono fg-strong">' + esc(e.hash) + '</td>' +
          '<td class="mono muted">' + esc(e.time) + '</td>' +
          '<td class="mono">' + esc(e.subject) + '</td>' +
        '</tr>';
      }).join('') || '<tr><td colspan="3" class="empty-state">暂无 Git 提交记录</td></tr>';

      if (d.error) $('gitHint').textContent = 'Git 状态提示：' + d.error;
      else if (d.dir) $('gitHint').textContent = '本地版本库路径：' + d.dir;
    });
  }

  /* ---------- Event Wiring & Bootstrap ---------- */
  function bindEvents() {
    /* Sidebar & Mobile Drawer */
    document.querySelectorAll('[data-sidebar-item]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        switchTab(btn.dataset.tab);
      });
    });

    $('sidebarCollapseBtn').addEventListener('click', function () {
      const root = document.documentElement;
      const next = root.getAttribute('data-sidebar-collapsed') === '1' ? '0' : '1';
      if (next === '1') root.setAttribute('data-sidebar-collapsed', '1');
      else root.removeAttribute('data-sidebar-collapsed');
      try { localStorage.setItem('gateway_manager_sidebar_collapsed', next); } catch (e) {}
    });

    $('menuToggle').addEventListener('click', function () {
      $('sidebar').classList.add('mobile-open');
      $('sidebarBackdrop').classList.add('open');
    });
    $('sidebarBackdrop').addEventListener('click', closeMobileDrawer);

    /* Copy Endpoint */
    $('copyEndpointBtn').addEventListener('click', function () {
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(state.endpointUrl).then(function () {
          toast('已复制代理地址', state.endpointUrl, 'success');
        });
      } else {
        toast('代理接入地址', state.endpointUrl, 'info');
      }
    });

    /* Topbar Toolbar */
    $('autoRefreshBtn').addEventListener('click', function () {
      state.paused = !state.paused;
      $('autoRefreshBtn').setAttribute('aria-pressed', state.paused ? 'true' : 'false');
      $('autoRefreshBtn').title = state.paused ? '恢复自动刷新' : '暂停自动刷新';
      toast(state.paused ? '已暂停自动刷新' : '已恢复 3 秒自动刷新', '', 'info');
    });

    $('refreshBtn').addEventListener('click', function () {
      loadStatus();
      if (state.tab === 'channels') loadChannels();
      if (state.tab === 'config') { loadSnapshots(); loadGitLog(); }
      if (state.tab === 'keys') loadKeys();
    });

    $('themeToggleBtn').addEventListener('click', function () {
      const root = document.documentElement;
      const next = root.getAttribute('data-theme') === 'light' ? 'dark' : 'light';
      root.setAttribute('data-theme', next);
      try { localStorage.setItem('gateway_manager_theme', next); } catch (e) {}
    });

    /* Admin Token Popover */
    $('tokenBtn').addEventListener('click', function (ev) {
      ev.stopPropagation();
      const pop = $('tokenPopover');
      pop.hidden = !pop.hidden;
      if (!pop.hidden) $('tokenInput').focus();
    });
    $('tokenPopover').addEventListener('click', function (ev) {
      ev.stopPropagation();
    });
    document.addEventListener('click', function () {
      $('tokenPopover').hidden = true;
    });
    $('toggleTokenVis').addEventListener('click', function () {
      const inp = $('tokenInput');
      const isPwd = inp.type === 'password';
      inp.type = isPwd ? 'text' : 'password';
      $('toggleTokenVis').textContent = isPwd ? '隐藏' : '显示';
    });
    $('clearTokenBtn').addEventListener('click', function () {
      $('tokenInput').value = '';
      try { localStorage.removeItem('gateway_manager_token'); } catch (e) {}
      updateTokenDot();
      toast('已清空本地管理密钥', '', 'info');
    });
    $('saveTokenBtn').addEventListener('click', function () {
      try { localStorage.setItem('gateway_manager_token', getToken()); } catch (e) {}
      updateTokenDot();
      $('tokenPopover').hidden = true;
      toast('管理密钥已保存', '正在重新同步网关状态...', 'success');
      bootstrapAll();
    });
    /* /metrics 需要 admin token：点链接时自动带上，避免新开页 401 */
    const metricsLink = document.querySelector('a.live-link[href="/metrics"]');
    if (metricsLink) metricsLink.addEventListener('click', function (ev) {
      const t = getToken();
      if (!t) return;
      ev.preventDefault();
      window.open('/metrics?token=' + encodeURIComponent(t), '_blank', 'noopener');
    });

    /* Modal Close */
    $('modalCloseBtn').addEventListener('click', function () { closeModal(null); });
    $('modalBackdrop').addEventListener('click', function (ev) {
      if (ev.target === $('modalBackdrop')) closeModal(null);
    });
    document.addEventListener('keydown', function (ev) {
      if (ev.key === 'Escape' && !$('modalBackdrop').hidden) closeModal(null);
    });

    /* Federation & Upstreams */
    $('reloadFedBtn').addEventListener('click', loadFederation);
    $('upstreamSearch').addEventListener('input', function (ev) {
      state.upstreamQuery = ev.target.value || '';
      renderUpstreams((state.status && state.status.upstreams) || []);
    });
    document.querySelectorAll('#upstreamFilter .seg-btn').forEach(function (btn) {
      btn.addEventListener('click', function () {
        document.querySelectorAll('#upstreamFilter .seg-btn').forEach(function (x) { x.classList.remove('active'); });
        btn.classList.add('active');
        state.upstreamFilter = btn.dataset.filter;
        renderUpstreams((state.status && state.status.upstreams) || []);
      });
    });
    $('checkNowBtn').addEventListener('click', function () {
      post('check', {}).then(function (r) {
        if (r.ok) {
          toast('已触发全量健康探测', '稍后自动刷新探测结果', 'success');
          setTimeout(loadStatus, 800);
        }
      });
    });

    /* Channels */
    $('reloadChannelsBtn').addEventListener('click', loadChannels);
    $('closeChannelOutBtn').addEventListener('click', function () { $('channelOut').hidden = true; });
    $('presetSearch').addEventListener('input', function (ev) {
      state.presetQuery = ev.target.value || '';
      renderChannels();
    });
    document.querySelectorAll('#presetProtoFilter .seg-btn').forEach(function (btn) {
      btn.addEventListener('click', function () {
        document.querySelectorAll('#presetProtoFilter .seg-btn').forEach(function (x) { x.classList.remove('active'); });
        btn.classList.add('active');
        state.presetProto = btn.dataset.proto;
        renderChannels();
      });
    });
    $('loginSaveBtn').addEventListener('click', function () {
      if (!loginState.id) return;
      post('channels/login/finish', { id: loginState.id, token_key: loginState.tokenKey || '' }).then(function (r) {
        if (r.ok) {
          toast('网页登录凭证已保存', '渠道 ' + r.body.id + ' 已启用', 'success');
          $('loginPanel').hidden = true;
          stopChannelLogin();
          loadChannels();
          loadStatus();
        } else {
          toast('保存凭证失败', errMsg(r), 'error');
        }
      });
    });
    $('loginCancelBtn').addEventListener('click', function () {
      if (loginState.id) {
        api('channels/login/cancel?id=' + encodeURIComponent(loginState.id), { method: 'POST' });
      }
      stopChannelLogin();
      $('loginPanel').hidden = true;
      toast('已取消网页登录会话', '', 'info');
    });

    /* Logs */
    $('logSearch').addEventListener('input', function (ev) {
      state.logQuery = ev.target.value || '';
      renderLogs();
    });
    document.querySelectorAll('#logLevelFilter .seg-btn').forEach(function (btn) {
      btn.addEventListener('click', function () {
        document.querySelectorAll('#logLevelFilter .seg-btn').forEach(function (x) { x.classList.remove('active'); });
        btn.classList.add('active');
        state.logLevel = btn.dataset.lv;
        renderLogs();
      });
    });
    $('reloadLogsBtn').addEventListener('click', loadStatus);

    /* Config, Snapshots & Git */
    document.querySelectorAll('#configSubtabs .seg-btn').forEach(function (btn) {
      btn.addEventListener('click', function () { switchCfgSub(btn.dataset.cfgsub); });
    });
    $('fmtCfgBtn').addEventListener('click', function () {
      try {
        const obj = JSON.parse($('cfgBox').value || '{}');
        $('cfgBox').value = JSON.stringify(obj, null, 2);
        showNotice('cfgHint', '✓ JSON 格式规范', 'ok');
      } catch (e) {
        showNotice('cfgHint', 'JSON 语法错误：' + e.message, 'err');
      }
    });
    $('loadCfgBtn').addEventListener('click', function () {
      loadConfig();
      toast('已重新读取线上配置', '', 'info');
    });
    $('saveCfgBtn').addEventListener('click', function () {
      let parsed;
      try {
        parsed = JSON.parse($('cfgBox').value);
      } catch (e) {
        showNotice('cfgHint', 'JSON 语法错误：' + e.message, 'err');
        toast('配置 JSON 语法错误', e.message, 'error');
        return;
      }
      post('config', parsed).then(function (r) {
        if (r.ok) {
          showNotice('cfgHint', '✓ 配置已落盘并完成热加载', 'ok');
          toast('配置已保存并热加载', '旧版本已自动留存快照', 'success');
          loadStatus();
          loadSnapshots();
          loadGitLog();
        } else {
          showNotice('cfgHint', '保存失败：' + errMsg(r), 'err');
          toast('保存配置失败', errMsg(r), 'error');
        }
      });
    });

    $('reloadSnapBtn').addEventListener('click', loadSnapshots);
    $('newSnapBtn').addEventListener('click', function () {
      formModal({
        title: '新建配置快照',
        description: '立即将当前内存与磁盘配置保存为一份独立历史快照。',
        confirmText: '创建快照',
        fields: [
          { name: 'note', label: '快照备注标识', defaultValue: 'manual', placeholder: '例如 before-upgrade' }
        ]
      }).then(function (vals) {
        if (!vals) return;
        post('snapshots/create', { note: (vals.note || 'manual').trim() }).then(function (r) {
          if (r.ok) {
            toast('配置快照已创建', (r.body.snapshot && r.body.snapshot.name) || '', 'success');
            loadSnapshots();
            loadGitLog();
          } else {
            toast('创建快照失败', errMsg(r), 'error');
          }
        });
      });
    });

    $('newKeyBtn').addEventListener('click', createAccessKey);
    $('reloadKeysBtn').addEventListener('click', loadKeys);
    $('copyBaseBtn').addEventListener('click', function () {
      copyText(location.origin + '/v1', '已复制 Base URL');
    });

    $('reloadGitBtn').addEventListener('click', loadGitLog);
    $('commitGitBtn').addEventListener('click', function () {
      formModal({
        title: '提交快照至本地 Git 仓库',
        description: '将快照目录下的变更提交一条 Git Commit 记录。',
        confirmText: '提交 Commit',
        fields: [
          { name: 'message', label: 'Commit 说明', defaultValue: 'manual snapshot commit' }
        ]
      }).then(function (vals) {
        if (!vals) return;
        post('snapshots/git/commit', { message: (vals.message || '').trim() }).then(function (r) {
          if (r.ok) {
            toast('Git 提交成功', vals.message || '', 'success');
            loadGitLog();
          } else {
            toast('Git 提交失败', errMsg(r), 'error');
          }
        });
      });
    });
  }


  /* ---------- Access Keys (接入密钥) ---------- */
  function copyText(text, okTitle) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(function () {
        toast(okTitle || '已复制', text, 'success');
      }).catch(function () {
        toast('复制失败', '请手动选中文本复制', 'error');
      });
      return;
    }
    toast('当前环境不支持自动复制', text, 'warn');
  }

  function loadKeys() {
    api('keys?reveal=1').then(function (r) {
      if (!r.ok) {
        if (r.status === 401) toast('需要管理密钥', '请先在右上角钥匙图标填入 X-Admin-Token', 'warn');
        return;
      }
      state.keys = (r.body && r.body.keys) || [];
      state.keyAuth = !!(r.body && r.body.auth_enabled);
      renderKeys();
    }).catch(function () {});
  }

  function renderKeys() {
    const count = state.keys.length;
    const navCount = $('navKeyCount');
    if (navCount) navCount.textContent = String(count);

    const chip = $('keyAuthChip');
    if (chip) {
      chip.innerHTML = state.keyAuth
        ? '<span class="chip success">已开启鉴权</span>'
        : '<span class="chip warning">未开启鉴权</span>';
    }
    const hint = $('keyAuthHint');
    if (hint) {
      hint.textContent = state.keyAuth
        ? '外部调用必须携带 Authorization: Bearer <密钥> 或 x-api-key 请求头，否则返回 401'
        : '密钥列表为空时网关不做鉴权，任何能访问本端口的客户端都可直接调用';
    }

    const tbody = $('keyTable') ? $('keyTable').querySelector('tbody') : null;
    if (tbody) {
      if (!count) {
        tbody.innerHTML = '<tr><td colspan="4" class="empty-state">还没有接入密钥，点击右上角「新建密钥」生成一把</td></tr>';
      } else {
        tbody.innerHTML = state.keys.map(function (k, i) {
          const revealed = !!state.keyReveal[i];
          const shown = revealed && k.key ? k.key : k.masked;
          return '<tr class="hoverable">' +
            '<td><code class="mono key-value">' + esc(shown) + '</code></td>' +
            '<td class="num mono">' + (k.length || 0) + '</td>' +
            '<td><span class="chip success">生效中</span></td>' +
            '<td><div class="row-actions">' +
              '<button type="button" class="btn outline xs" data-key-toggle="' + i + '">' + (revealed ? '隐藏' : '显示') + '</button>' +
              '<button type="button" class="btn secondary xs" data-key-copy="' + i + '">复制</button>' +
              '<button type="button" class="btn danger-soft xs" data-key-revoke="' + i + '">吊销</button>' +
            '</div></td>' +
          '</tr>';
        }).join('');

        tbody.querySelectorAll('[data-key-toggle]').forEach(function (b) {
          b.addEventListener('click', function () {
            const i = Number(b.dataset.keyToggle);
            state.keyReveal[i] = !state.keyReveal[i];
            renderKeys();
          });
        });
        tbody.querySelectorAll('[data-key-copy]').forEach(function (b) {
          b.addEventListener('click', function () {
            const item = state.keys[Number(b.dataset.keyCopy)];
            if (item && item.key) copyText(item.key, '已复制密钥');
          });
        });
        tbody.querySelectorAll('[data-key-revoke]').forEach(function (b) {
          b.addEventListener('click', function () {
            const item = state.keys[Number(b.dataset.keyRevoke)];
            if (item) revokeAccessKey(item);
          });
        });
      }
    }
    renderKeySamples();
  }

  function renderKeySamples() {
    const base = location.origin + '/v1';
    const first = state.keys[0];
    const sampleKey = (first && first.key) || 'sk-gw-请先创建密钥';

    const elBase = $('keyBaseUrl');
    if (elBase) elBase.textContent = base;
    const elKey = $('keySample');
    if (elKey) elKey.textContent = first ? first.masked : '尚未创建';

    const curl = $('keyCurl');
    if (curl) {
      curl.textContent = [
        'curl ' + base + '/chat/completions \\',
        '  -H "Authorization: Bearer ' + sampleKey + '" \\',
        '  -H "Content-Type: application/json" \\',
        '  -d \'{"model":"deepseek-chat","messages":[{"role":"user","content":"你好"}]}\''
      ].join('\n');
    }
  }

  function createAccessKey() {
    formModal({
      title: '新建接入密钥',
      description: '留空则自动生成 sk-gw- 开头的随机密钥；也可粘贴自定义值（至少 8 位）。创建后立即热加载生效。',
      confirmText: '创建密钥',
      fields: [
        { name: 'key', label: '自定义密钥（可留空）', placeholder: '留空自动生成' }
      ]
    }).then(function (vals) {
      if (!vals) return;
      const payload = {};
      const custom = (vals.key || '').trim();
      if (custom) payload.key = custom;
      post('keys', payload).then(function (r) {
        if (r.ok) {
          state.keyReveal = {};
          toast('密钥已创建', (r.body && r.body.masked) || '', 'success');
          loadKeys();
        } else {
          toast('创建失败', errMsg(r), 'error');
        }
      });
    });
  }

  function revokeAccessKey(item) {
    confirmModal({
      title: '吊销接入密钥',
      description: '吊销后使用该密钥的客户端会立刻收到 401。操作前会自动创建配置快照，可随时回滚。',
      confirmText: '吊销',
      danger: true
    }).then(function (ok) {
      if (!ok) return;
      api('keys?key=' + encodeURIComponent(item.key || ''), { method: 'DELETE' }).then(function (r) {
        if (r.ok) {
          state.keyReveal = {};
          toast('密钥已吊销', item.masked || '', 'success');
          loadKeys();
        } else {
          toast('吊销失败', errMsg(r), 'error');
        }
      });
    });
  }

  function bootstrapAll() {
    loadStatus();
    loadConfig();
    loadSnapshots();
    loadGitLog();
    loadFederation();
    loadChannels();
    loadKeys();
  }

  /* Restore Token：URL ?token= 优先（控制台链接可直接分享），其次 localStorage */
  try {
    const urlToken = new URLSearchParams(location.search).get('token');
    if (urlToken && urlToken.trim()) {
      $('tokenInput').value = urlToken.trim();
      try { localStorage.setItem('gateway_manager_token', urlToken.trim()); } catch (e) {}
      history.replaceState(null, '', location.pathname);
    } else {
      const savedToken = localStorage.getItem('gateway_manager_token');
      if (savedToken) $('tokenInput').value = savedToken;
    }
  } catch (e) {}
  updateTokenDot();

  bindEvents();
  bootstrapAll();

  /* 连接前也从无鉴权的 /health 取真实版本号、预填端点显示 */
  try {
    fetch('/health').then(function (r) { return r.json(); }).then(function (h) {
      if (h && h.version) $('versionBadge').textContent = 'v' + h.version;
    }).catch(function () {});
    const ep = $('endpointText');
    if (ep && !ep.textContent) ep.textContent = location.host + '/v1';
  } catch (e) {}

  /* Visibility-aware smart polling (matches cli2api behavior) */
  setInterval(function () {
    if (state.paused || document.hidden) return;
    loadStatus();
  }, 3000);

  setInterval(function () {
    if (state.paused || document.hidden) return;
  }, 30000);

  document.addEventListener('visibilitychange', function () {
    if (!document.hidden && !state.paused) {
      loadStatus();
    }
  });
})();
