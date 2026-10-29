package main

const adminIndexHTML = `
<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>网关管家 · 控制台</title>
<style>
  :root {
    --bg:#0d1117; --panel:#161b22; --panel2:#1c2230; --line:#263244;
    --fg:#e6edf3; --muted:#8b98a9; --accent:#4f9cf9; --ok:#3fb950;
    --warn:#d29922; --err:#f85149; --violet:#a371f7; --cyan:#39c5cf;
  }
  *{box-sizing:border-box}
  body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.55 "Segoe UI","Microsoft YaHei",system-ui,sans-serif}
  header{display:flex;align-items:center;gap:14px;padding:13px 22px;border-bottom:1px solid var(--line);background:var(--panel);position:sticky;top:0;z-index:9}
  header h1{font-size:16px;margin:0;letter-spacing:.5px}
  header .tag{font-size:12px;color:var(--muted)}
  header .spacer{flex:1}
  input,button,textarea,select{font:inherit;color:var(--fg);background:var(--panel2);border:1px solid var(--line);border-radius:8px;padding:6px 10px}
  input[type=checkbox]{width:16px;height:16px;vertical-align:-3px}
  button{cursor:pointer}
  button:hover{border-color:#3a4a63}
  button.primary{background:var(--accent);border-color:var(--accent);color:#06101e;font-weight:600}
  button.ghost{background:transparent}
  button.danger{color:var(--err);border-color:#5c2a28}
  nav{display:flex;gap:6px;padding:12px 22px 0;flex-wrap:wrap}
  nav button{border:1px solid transparent;background:transparent;color:var(--muted);padding:7px 14px;border-radius:9px 9px 0 0}
  nav button.active{color:var(--fg);background:var(--panel);border-color:var(--line);border-bottom-color:var(--panel)}
  main{padding:18px 22px 70px}
  section{display:none}
  section.active{display:block}
  .cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(160px,1fr));gap:12px;margin-bottom:18px}
  .card{background:var(--panel);border:1px solid var(--line);border-radius:12px;padding:13px 15px}
  .card .k{font-size:12px;color:var(--muted)}
  .card .v{font-size:20px;font-weight:600;margin-top:3px}
  table{width:100%;border-collapse:collapse;background:var(--panel);border:1px solid var(--line);border-radius:12px;overflow:hidden}
  th,td{padding:9px 12px;text-align:left;border-bottom:1px solid var(--line);vertical-align:top}
  th{font-size:12px;color:var(--muted);font-weight:600;background:var(--panel2)}
  tr:last-child td{border-bottom:none}
  .pill{display:inline-block;font-size:11px;padding:2px 8px;border-radius:999px;border:1px solid var(--line)}
  .pill.ok{color:var(--ok);border-color:#1f6f36;background:#0f2a19}
  .pill.warn{color:var(--warn);border-color:#6b5216;background:#291f0b}
  .pill.err{color:var(--err);border-color:#7a2320;background:#2a1211}
  .pill.off{color:var(--muted)}
  .row{display:flex;gap:10px;align-items:center;margin-bottom:12px;flex-wrap:wrap}
  .muted{color:var(--muted)}
  .mono{font-family:Consolas,"Cascadia Mono",monospace;font-size:12.5px}
  textarea{width:100%;min-height:150px;font-family:Consolas,"Cascadia Mono",monospace;font-size:12.5px}
  .bar{height:6px;border-radius:4px;background:#20293a;overflow:hidden;min-width:70px}
  .bar > i{display:block;height:100%}
  .keytable{margin:0;border:none;background:transparent}
  .keytable th,.keytable td{border-bottom:1px dashed var(--line);font-size:12px;padding:5px 8px}
  .canvas{display:flex;flex-direction:column;gap:10px;margin:14px 0}
  .node{display:flex;gap:12px;align-items:flex-start;background:var(--panel);border:1px solid var(--line);border-radius:12px;padding:12px 14px;cursor:grab}
  .node.dragging{opacity:.4}
  .node.over{border-color:var(--accent);box-shadow:0 0 0 2px rgba(79,156,249,.25)}
  .node .grip{color:#3f4b60;font-size:18px;line-height:1.1;user-select:none}
  .node-main{flex:1;min-width:0}
  .node-head{display:flex;gap:8px;align-items:center;flex-wrap:wrap}
  .badge{font-size:11px;padding:2px 9px;border-radius:999px;border:1px solid var(--line);text-transform:uppercase;letter-spacing:.4px}
  .badge.inject{color:var(--cyan);border-color:#1d5a5f;background:#0b2426}
  .badge.override{color:var(--violet);border-color:#463183;background:#1c1630}
  .badge.header{color:var(--warn);border-color:#6b5216;background:#291f0b}
  .badge.deny{color:var(--err);border-color:#7a2320;background:#2a1211}
  .badge.match{color:var(--ok);border-color:#1f6f36;background:#0f2a19}
  .node-cond{font-size:12.5px;color:var(--muted);margin-top:4px}
  .node-sum{font-size:12.5px;margin-top:4px;white-space:pre-wrap;word-break:break-word}
  .node-ops{display:flex;gap:6px;flex-direction:column}
  .editor{margin-top:12px;padding-top:12px;border-top:1px dashed var(--line);display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:10px}
  .editor .full{grid-column:1/-1}
  .field{display:flex;flex-direction:column;gap:4px}
  .field label{font-size:12px;color:var(--muted)}
  .split{display:grid;grid-template-columns:minmax(240px,320px) 1fr;gap:14px}
  .panel{background:var(--panel);border:1px solid var(--line);border-radius:12px;padding:14px}
  .tpl{display:flex;flex-direction:column;gap:6px;max-height:520px;overflow:auto;padding-right:4px}
  .tpl button{text-align:left;display:flex;flex-direction:column;gap:2px;padding:9px 11px}
  .tpl button.active{border-color:var(--accent);background:#15243a}
  .tpl .n{font-weight:600}
  .tpl .c{font-size:11.5px;color:var(--muted)}
  pre{margin:0;white-space:pre-wrap;word-break:break-all;font-family:Consolas,"Cascadia Mono",monospace;font-size:12.5px}
  #logs pre{max-height:80vh;overflow:auto}
  .heat{height:5px;border-radius:4px;background:#20293a;overflow:hidden;margin-top:8px}
  .heat > i{display:block;height:100%}
  .heat-label{font-size:11.5px;color:var(--muted);margin-top:4px}
  .node.hot{border-color:#4a2530}
  .node.warm{border-color:#4a3a1c}
  .lv-ERR{color:var(--err)} .lv-WARN{color:var(--warn)} .lv-INFO{color:var(--fg)}
</style>
</head>
<body>
<header>
  <h1>网关管家</h1>
  <span class="tag" id="tagline">本机网关聚合 · 策略注入</span>
  <span class="spacer"></span>
  <input id="token" placeholder="admin token（未设置可留空）" style="width:200px" />
  <button id="saveToken">记住</button>
  <button class="primary" id="refresh">刷新</button>
</header>

<nav>
  <button data-tab="status" class="active">状态</button>
  <button data-tab="upstreams">上游</button>
  <button data-tab="rules">规则</button>
  <button data-tab="profiles">策略</button>
  <button data-tab="logs">日志</button>
  <button data-tab="config">配置</button>
</nav>

<main>
  <section id="tab-status" class="active">
    <div class="cards" id="cards"></div>
    <div class="panel" style="margin-bottom:14px">
      <div class="row">
        <b>A/B 实验</b>
        <span class="muted">config.experiments：按权重分臂，每臂可指定策略与上游</span>
      </div>
      <table id="expTable"><thead><tr>
        <th style="width:150px">实验</th><th style="width:120px">臂</th><th style="width:80px">次数</th>
        <th style="width:80px">错误</th><th style="width:110px">平均延迟</th><th>token 合计</th>
      </tr></thead><tbody></tbody></table>
    </div>

    <div class="panel" style="margin-bottom:14px">
      <div class="row">
        <b>联邦总览</b>
        <span class="muted">并行拉取各实例的 /admin/api/status（在 config.federation.peers 里配置）</span>
        <span style="flex:1"></span>
        <button id="reloadFed">刷新</button>
      </div>
      <table id="fedTable"><thead><tr>
        <th style="width:140px">实例</th><th style="width:70px">状态</th><th style="width:80px">版本</th>
        <th style="width:90px">运行时长</th><th style="width:80px">请求</th><th style="width:90px">规则拦截</th>
        <th style="width:110px">上游(异常)</th><th style="width:70px">规则</th><th style="width:80px">延迟</th>
      </tr></thead><tbody></tbody></table>
      <div class="muted" id="fedHint"></div>
    </div>

    <table id="retryTable"><thead><tr><th style="width:240px">参数</th><th>值</th></tr></thead><tbody></tbody></table>
  </section>

  <section id="tab-upstreams">
    <div class="row">
      <button id="checkNow">立即健康检查</button>
      <span class="muted">同优先级内按 <b>权重</b> 分流（0 = 均分）；分流次数看 /metrics 的 pojiagw_routed_total</span>
      <span class="muted">key 池按健康分选路：401/403 重罚 5 倍冷却，429 轻度冷却，连续 2 次失败进入冷却</span>
    </div>
    <table id="upTable"><thead><tr>
      <th>ID</th><th>名称</th><th>状态</th><th>熔断</th><th>优先级</th><th>权重</th><th>请求 / 成功 / 失败</th>
      <th>并发</th><th>最近</th><th>延迟</th><th>模型</th>
    </tr></thead><tbody></tbody></table>
  </section>

  <section id="tab-rules">
    <div class="row">
      <span class="muted">拖拽卡片即可调整执行顺序，条件为空表示总是命中</span>
      <span class="spacer" style="flex:1"></span>
      <button class="primary" id="saveRules">保存规则</button>
      <button id="reloadRules">还原</button>
    </div>
    <div class="row">
      <span class="muted">新增节点：</span>
      <button data-add="match">条件</button>
      <button data-add="inject">注入策略</button>
      <button data-add="override">改写参数</button>
      <button data-add="header">附加请求头</button>
      <button data-add="deny">拦截</button>
    </div>
    <div class="canvas" id="ruleCanvas"></div>
    <div class="panel" style="margin-bottom:14px">
      <div class="row">
        <b>规则热力</b>
        <span class="muted">命中次数 / 跳过次数 / 拦截次数，越红表示该节点越常生效</span>
        <span style="flex:1"></span>
        <button class="danger" id="resetHeat">清零统计</button>
      </div>
      <table id="heatTable"><thead><tr>
        <th style="width:180px">节点</th><th style="width:90px">命中</th><th style="width:90px">跳过</th>
        <th style="width:90px">拦截</th><th style="width:120px">命中占比</th><th>热力</th><th style="width:110px">最近命中</th>
      </tr></thead><tbody></tbody></table>
    </div>

    <div class="panel" style="margin-bottom:14px">
      <div class="row">
        <b>拦截回放</b>
        <span class="muted">最近被规则拦下的请求（Header 已脱敏），改完规则可一键复现</span>
        <span style="flex:1"></span>
        <button id="reloadReplay">刷新</button>
        <button class="danger" id="clearReplay">清空</button>
      </div>
      <table id="replayTable"><thead><tr>
        <th style="width:150px">时间</th><th style="width:190px">端点</th><th style="width:130px">模型</th>
        <th style="width:60px">状态</th><th style="width:150px">命中规则</th><th>提示</th><th style="width:90px">操作</th>
      </tr></thead><tbody></tbody></table>
      <div id="replayOut" class="panel mono" style="display:none;margin-top:12px"></div>
    </div>

    <div class="panel" style="margin-bottom:14px">
      <div class="row">
        <b>成功请求采样</b>
        <span class="muted" id="sampleHint">按 server.sample_every 抽样留档（已脱敏），可用于回归验证</span>
        <span style="flex:1"></span>
        <button id="reloadSamples">刷新</button>
        <button class="danger" id="clearSamples">清空</button>
      </div>
      <table id="sampleTable"><thead><tr>
        <th style="width:150px">时间</th><th style="width:190px">端点</th><th style="width:120px">模型</th>
        <th style="width:70px">状态</th><th style="width:110px">上游</th><th style="width:80px">耗时</th><th style="width:90px">操作</th>
      </tr></thead><tbody></tbody></table>
      <div id="sampleOut" class="panel mono" style="display:none;margin-top:12px"></div>
    </div>

    <div class="panel" style="margin-bottom:14px">
      <div class="row">
        <b>批量回归</b>
        <span class="muted">用一批历史请求跑一遍当前规则链，看拦截与命中分布</span>
        <span style="flex:1"></span>
        <select id="batchFrom">
          <option value="both">采样 + 回放</option>
          <option value="samples">只用采样</option>
          <option value="replay">只用回放</option>
        </select>
        <input id="batchLimit" type="number" value="100" style="width:90px" />
        <button class="primary" id="runBatch">运行回归</button>
      </div>
      <div id="batchOut" class="panel mono" style="display:none"></div>
    </div>

    <details class="panel" style="margin-bottom:14px">
      <summary><b>文本模式（规则书 DSL）</b> <span class="muted">与拖拽 UI 双向同步</span></summary>
      <div class="row" style="margin-top:12px">
        <button id="dslGen">从当前规则生成</button>
        <button id="dslCheck">校验</button>
        <button class="primary" id="dslApply">解析并应用</button>
        <span class="muted" id="dslHint"></span>
      </div>
      <textarea id="dslBox" spellcheck="false" style="min-height:220px" placeholder="- id: r-tag&#10;  name: 打标记&#10;  type: header&#10;  action:&#10;    header_name: X-Gateway&#10;    header_value: pojiagw"></textarea>
    </details>

    <div class="panel">
      <div class="row"><b>规则试算</b><span class="muted">不发送真实请求，只跑一遍规则链</span></div>
      <div class="row">
        <input id="testModel" value="gpt-4o-mini" style="width:180px" placeholder="model" />
        <input id="testPath" value="/v1/chat/completions" style="width:220px" placeholder="path" />
        <button id="runTest">试算</button>
      </div>
      <textarea id="testBody" style="min-height:110px">{ "model": "gpt-4o-mini", "messages": [ { "role": "user", "content": "hi" } ] }</textarea>
      <div id="testOut" class="panel mono" style="margin-top:10px;display:none"></div>
    </div>
  </section>

  <section id="tab-profiles">
    <div class="split">
      <div class="panel">
        <div class="row"><b>模板库</b><span class="muted" id="tplCount"></span></div>
        <div class="row">
          <input id="tplSearch" placeholder="搜索名称 / 标签" style="width:100%" />
        </div>
        <div class="tpl" id="tplList"></div>
      </div>
      <div>
        <div class="panel" style="margin-bottom:14px">
          <div class="row"><b id="tplTitle">选择一个模板</b><span class="muted" id="tplMeta"></span></div>
          <textarea id="tplText" style="min-height:170px" placeholder="模板指令内容"></textarea>
          <div class="row" style="margin-top:10px">
            <select id="applyMode"><option value="new">新建策略</option><option value="overwrite">覆盖已有策略</option></select>
            <select id="applyTarget"></select>
            <input id="applyName" placeholder="策略名称（可选）" style="width:190px" />
            <button class="primary" id="applyTpl">应用到策略</button>
            <button id="saveTpl">保存为自定义模板</button>
            <button class="danger" id="delTpl">删除自定义模板</button>
          </div>
          <div class="muted" id="applyHint"></div>
        </div>
        <table id="profTable"><thead><tr><th>ID</th><th>名称</th><th>启用</th><th>适用模型</th><th>指令预览</th></tr></thead><tbody></tbody></table>
      </div>
    </div>
  </section>

  <section id="tab-logs">
    <div class="row"><span class="muted">保留最近 500 条</span></div>
    <table id="logs"><thead><tr><th style="width:90px">时间</th><th style="width:70px">级别</th><th>内容</th></tr></thead><tbody></tbody></table>
  </section>

  <section id="tab-config">
    <div class="panel" style="margin-bottom:14px">
      <div class="row">
        <b>配置快照</b>
        <span class="muted" id="snapHint">每次保存配置 / 改规则 / 应用模板前会自动留一版，可一键回滚</span>
        <span style="flex:1"></span>
        <button id="newSnap">新建快照</button>
        <button id="reloadSnap">刷新</button>
      </div>
      <table id="snapTable"><thead><tr>
        <th style="width:180px">时间</th><th style="width:90px">大小</th>
        <th style="width:70px">上游</th><th style="width:70px">策略</th><th style="width:70px">规则</th>
        <th>名称</th><th style="width:210px">操作</th>
      </tr></thead><tbody></tbody></table>
    </div>

    <div class="panel" style="margin-bottom:14px">
      <div class="row">
        <b>快照 Git 历史</b>
        <span class="muted" id="gitHint">开启 server.snapshot_git 后，每次快照都会 commit 到快照目录的本地仓库</span>
        <span style="flex:1"></span>
        <button id="reloadGit">刷新</button>
        <button id="commitGit">手动提交</button>
      </div>
      <table id="gitTable"><thead><tr><th style="width:110px">提交</th><th style="width:220px">时间</th><th>说明</th></tr></thead><tbody></tbody></table>
    </div>

    <div class="row">
      <button class="primary" id="saveCfg">保存并热加载</button>
      <button id="loadCfg">重新读取</button>
      <span class="muted" id="cfgHint"></span>
    </div>
    <textarea id="cfgBox" spellcheck="false" style="min-height:430px"></textarea>
  </section>
</main>

<script>
'use strict';
var state = { status:null, rules:[], templates:[], editing:-1, dragFrom:-1, selectedTpl:null, dirty:false };

function token(){ return document.getElementById('token').value.trim(); }
function headers(){
  var h = { 'Content-Type':'application/json' };
  var t = token();
  if (t) { h['X-Admin-Token'] = t; }
  return h;
}
function esc(s){
  return String(s === undefined || s === null ? '' : s)
    .replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}
function splitList(v){
  return String(v||'').split(',').map(function(x){ return x.trim(); }).filter(function(x){ return x.length>0; });
}
function api(path, opts){
  opts = opts || {};
  opts.headers = headers();
  return fetch('/admin/api/' + path, opts).then(function(r){
    return r.text().then(function(txt){
      var data = null;
      try { data = txt ? JSON.parse(txt) : null; } catch(e) { data = { raw: txt }; }
      return { ok: r.ok, status: r.status, body: data };
    });
  });
}
function post(path, obj){
  return api(path, { method:'POST', body: JSON.stringify(obj) });
}
function note(msg){
  document.getElementById('tagline').textContent = msg;
}

document.querySelectorAll('nav button').forEach(function(b){
  b.addEventListener('click', function(){
    document.querySelectorAll('nav button').forEach(function(x){ x.classList.remove('active'); });
    document.querySelectorAll('section').forEach(function(x){ x.classList.remove('active'); });
    b.classList.add('active');
    document.getElementById('tab-' + b.dataset.tab).classList.add('active');
  });
});
document.getElementById('saveToken').addEventListener('click', function(){
  try { localStorage.setItem('pojiagw_token', token()); } catch(e){}
  load();
});
document.getElementById('refresh').addEventListener('click', load);

function load(){
  api('status').then(function(r){
    if (!r.ok) { note('状态读取失败：HTTP ' + r.status); return; }
    state.status = r.body;
    render(r.body);
  }).catch(function(e){ note('连接失败：' + e.message); });
  api('logs?limit=200').then(function(r){ renderLogs((r.body && r.body.logs) || []); }).catch(function(){});
}

function render(d){
  note(d.service + ' ' + d.version + ' · ' + d.bind + ':' + d.port + ' · 鉴权' + (d.auth?'开启':'关闭') + ' · key 策略 ' + d.key_strategy);
  var s = d.stats;
  var cards = [
    ['运行时长', d.uptime], ['上游 / 规则', (d.upstreams||[]).length + ' / ' + (d.rules||[]).length],
    ['总请求', s.requests], ['策略注入', s.injected], ['规则注入', s.rule_inject],
    ['规则拦截', s.rule_deny], ['未注入', s.no_profile],
    ['上游错误', s.upstream_errors], ['鉴权失败', s.auth_failures],
    ['状态持久化', d.persist && d.persist.enabled ? '已开启' : '关闭'],
    ['指标端点', '/metrics']
  ];
  document.getElementById('cards').innerHTML = cards.map(function(c){
    return '<div class="card"><div class="k">' + esc(c[0]) + '</div><div class="v">' + esc(c[1]) + '</div></div>';
  }).join('');

  var rt = d.retry || {};
  var rows = [
    ['max_retries', rt.max_retries], ['failure_threshold', rt.failure_threshold],
    ['cooldown_seconds', rt.cooldown_seconds], ['request_timeout_seconds', rt.request_timeout_seconds],
    ['retry_status', (rt.retry_status||[]).join(', ')]
  ];
  document.getElementById('retryTable').querySelector('tbody').innerHTML = rows.map(function(r){
    return '<tr><td class="mono">' + esc(r[0]) + '</td><td class="mono">' + esc(r[1]) + '</td></tr>';
  }).join('');

  renderUpstreams(d.upstreams || []);
  renderProfiles(d.profiles || []);
  if (!state.dirty) { state.rules = (d.rules || []).map(function(r){ return JSON.parse(JSON.stringify(r)); }); }
  renderExperiments(d);
  renderRules();
  renderHeat(d);
  buildApplyTargets(d.profiles || []);
}

function renderExperiments(d){
  var body = document.getElementById('expTable').querySelector('tbody');
  if (!body) { return; }
  var exps = d.experiments || [];
  var stats = d.experiment_stats || {};
  var rows = [];
  exps.forEach(function(exp){
    (exp.arms || []).forEach(function(arm){
      var st = (stats[exp.id] || {})[arm.id] || { count:0, errors:0, latency_ms_sum:0, tokens_sum:0 };
      var avg = st.count ? Math.round(st.latency_ms_sum / st.count) : 0;
      rows.push('<tr>' +
        '<td class="mono">' + esc(exp.id) + ' <span class="muted">' + esc(exp.name||'') + '</span></td>' +
        '<td class="mono">' + esc(arm.id) + ' <span class="muted">权重' + (arm.weight||1) + '</span></td>' +
        '<td class="mono">' + st.count + '</td>' +
        '<td class="mono">' + st.errors + '</td>' +
        '<td class="mono">' + (avg ? avg + ' ms' : '-') + '</td>' +
        '<td class="mono">' + (st.tokens_sum||0) + '</td>' +
        '</tr>');
    });
  });
  body.innerHTML = rows.join('') || '<tr><td colspan="6" class="muted">未配置实验（config.experiments）</td></tr>';
}

function renderUpstreams(list){
  var body = document.getElementById('upTable').querySelector('tbody');
  if (!list.length) { body.innerHTML = '<tr><td colspan="11" class="muted">没有配置上游</td></tr>'; return; }
  var html = '';
  list.forEach(function(u){
    var cls='off', text='停用';
    if (u.enabled) {
      if (u.cooldown_until) { cls='warn'; text='冷却中'; }
      else if (u.healthy) { cls='ok'; text='正常'; }
      else { cls='err'; text='异常'; }
    }
    html += '<tr>' +
      '<td class="mono">' + esc(u.id) + '</td>' +
      '<td>' + esc(u.name) + '</td>' +
      '<td><span class="pill ' + cls + '">' + text + '</span></td>' +
      '<td>' + (function(){ var c = u.circuit || 'closed'; var m2 = {closed:'ok', half_open:'warn', open:'err'}; return '<span class="pill ' + (m2[c]||'off') + '">' + c + '</span>'; })() + '</td>' +
      '<td>' + esc(u.priority) + '</td>' +
      '<td>' + (u.weight ? '<span class="pill ok">' + u.weight + '</span>' : '<span class="muted">均分</span>') + '</td>' +
      '<td class="mono">' + u.requests + ' / ' + u.successes + ' / ' + u.failures + '</td>' +
      '<td>' + u.inflight + '</td>' +
      '<td class="mono">' + (u.last_status||'-') + (u.last_error ? ' <span class="muted">' + esc(u.last_error) + '</span>' : '') + '</td>' +
      '<td>' + (u.latency_ms ? u.latency_ms + ' ms' : '-') + '</td>' +
      '<td class="mono muted">' + esc((u.models||[]).slice(0,4).join(', ')) + '</td>' +
      '</tr>';
    var keys = u.keys || [];
    if (keys.length) {
      html += '<tr><td colspan="11" style="background:#111722">' +
        '<table class="keytable"><thead><tr><th>key</th><th>评分</th><th>状态</th><th>成功 / 失败</th><th>最近</th><th>延迟</th></tr></thead><tbody>' +
        keys.map(function(k){
          var color = k.score >= 60 ? 'var(--ok)' : (k.score >= 30 ? 'var(--warn)' : 'var(--err)');
          var st = k.state === 'ok' ? '<span class="pill ok">健康</span>' :
                   (k.state === 'degraded' ? '<span class="pill warn">降权</span>' :
                   (k.state === 'bad' ? '<span class="pill err">低分</span>' : '<span class="pill warn">冷却中</span>'));
          return '<tr>' +
            '<td class="mono">' + esc(k.masked) + '</td>' +
            '<td><div class="row" style="margin:0;gap:6px"><div class="bar" style="flex:1"><i style="width:' + Math.round(k.score) + '%;background:' + color + '"></i></div><span class="mono">' + k.score + '</span></div></td>' +
            '<td>' + st + '</td>' +
            '<td class="mono">' + k.successes + ' / ' + k.failures + '</td>' +
            '<td class="mono">' + (k.last_status||'-') + '</td>' +
            '<td class="mono">' + (k.latency_ms ? k.latency_ms + 'ms' : '-') + '</td>' +
            '</tr>';
        }).join('') + '</tbody></table></td></tr>';
    } else {
      html += '<tr><td colspan="11" class="muted mono" style="background:#111722">无 key（匿名上游）</td></tr>';
    }
  });
  body.innerHTML = html;
}

function renderProfiles(list){
  var body = document.getElementById('profTable').querySelector('tbody');
  if (!list.length) { body.innerHTML = '<tr><td colspan="5" class="muted">还没有策略</td></tr>'; return; }
  body.innerHTML = list.map(function(p){
    var preview = (p.instruction||'').replace(/\n/g,' ');
    if (preview.length > 130) { preview = preview.slice(0,130) + '…'; }
    return '<tr><td class="mono">' + esc(p.id) + '</td><td>' + esc(p.name) + '</td>' +
      '<td><span class="pill ' + (p.enabled?'ok':'off') + '">' + (p.enabled?'启用':'停用') + '</span></td>' +
      '<td class="mono muted">' + esc((p.models||[]).join(', ') || '（全部）') + '</td>' +
      '<td class="muted">' + esc(preview) + '</td></tr>';
  }).join('');
}

function buildApplyTargets(profiles){
  var sel = document.getElementById('applyTarget');
  var mode = document.getElementById('applyMode').value;
  if (mode === 'new') {
    sel.innerHTML = '<option value="">自动生成 id</option>' +
      profiles.map(function(p){ return '<option value="' + esc(p.id) + '">' + esc(p.id) + '（' + esc(p.name) + '）</option>'; }).join('');
    sel.disabled = true;
  } else {
    sel.disabled = false;
    sel.innerHTML = profiles.map(function(p){ return '<option value="' + esc(p.id) + '">' + esc(p.id) + '（' + esc(p.name) + '）</option>'; }).join('');
  }
}
document.getElementById('applyMode').addEventListener('change', function(){
  buildApplyTargets((state.status && state.status.profiles) || []);
});

var TYPE_LABEL = { match:'条件', inject:'注入', override:'改写', header:'请求头', deny:'拦截' };

function condText(r){
  var c = r.condition || {};
  var parts = [];
  if (c.models && c.models.length) { parts.push('model ~ ' + c.models.join(' | ')); }
  if (c.paths && c.paths.length) { parts.push('path ~ ' + c.paths.join(' | ')); }
  if (c.header_key) { parts.push('header ' + c.header_key + (c.header_value ? ' 含 ' + c.header_value : '')); }
  if (c.body_contains) { parts.push('body 含 "' + c.body_contains + '"'); }
  if (c.expr) { parts.push('表达式 ' + c.expr); }
  return parts.length ? ('条件：' + parts.join(' · ')) : '条件：总是命中';
}
function sumText(r){
  if (r.type === 'inject') { return '注入：' + (r.instruction || ('引用策略 ' + (r.profile_id||''))); }
  if (r.type === 'override') {
    var bits = [];
    if (r.set_model) { bits.push('model → ' + r.set_model); }
    if (r.set_params) { bits.push('参数 ' + JSON.stringify(r.set_params)); }
    return bits.length ? bits.join(' · ') : '（未设置改写内容）';
  }
  if (r.type === 'header') { return '请求头：' + (r.header_name||'') + ' = ' + (r.header_value||''); }
  if (r.type === 'deny') { return '拦截：HTTP ' + (r.status||403) + ' · ' + (r.message||''); }
  return '仅作为条件标记，不影响请求';
}

function renderRules(){
  var canvas = document.getElementById('ruleCanvas');
  if (!state.rules.length) {
    canvas.innerHTML = '<div class="panel muted">还没有规则节点。用上方按钮添加，拖拽可排序。</div>';
    return;
  }
  canvas.innerHTML = state.rules.map(function(r, i){
    var cls = r.disabled ? ' disabled' : '';
    var h = '<div class="node' + cls + '" draggable="true" data-i="' + i + '">' +
      '<div class="grip">≡</div>' +
      '<div class="node-main">' +
        '<div class="node-head">' +
          '<span class="badge ' + esc(r.type) + '">' + esc(TYPE_LABEL[r.type] || r.type) + '</span>' +
          '<b>' + esc(r.name || r.id) + '</b>' +
          '<span class="muted mono">' + esc(r.id) + '</span>' +
          (r.disabled ? '<span class="pill off">已停用</span>' : '<span class="pill ok">启用</span>') +
        '</div>' +
        '<div class="node-cond">' + esc(condText(r)) + '</div>' +
        '<div class="node-sum">' + esc(sumText(r)) + '</div>' +
        heatHTML(r.id) +
        (state.editing === i ? editorHTML(r, i) : '') +
      '</div>' +
      '<div class="node-ops">' +
        '<button data-act="edit" data-i="' + i + '">' + (state.editing === i ? '收起' : '编辑') + '</button>' +
        '<button data-act="del" data-i="' + i + '">删除</button>' +
        '<button data-act="up" data-i="' + i + '">上移</button>' +
        '<button data-act="down" data-i="' + i + '">下移</button>' +
      '</div>' +
    '</div>';
    return h;
  }).join('');
  bindCanvas();
}

function editorHTML(r, i){
  var c = r.condition || {};
  var h = '<div class="editor">';
  h += field('名称', '<input data-f="name" value="' + esc(r.name) + '" />');
  h += field('条件：模型包含（逗号分隔）', '<input data-f="cond.models" value="' + esc((c.models||[]).join(',')) + '" placeholder="mini, coder" />');
  h += field('条件：路径前缀（逗号分隔）', '<input data-f="cond.paths" value="' + esc((c.paths||[]).join(',')) + '" placeholder="/v1/messages" />');
  h += field('条件：请求头名', '<input data-f="cond.header_key" value="' + esc(c.header_key||'') + '" />');
  h += field('条件：请求头包含值', '<input data-f="cond.header_value" value="' + esc(c.header_value||'') + '" />');
  h += field('条件：请求体包含文本', '<input data-f="cond.body_contains" value="' + esc(c.body_contains||'') + '" />');
  h += field('条件表达式（model ~ "正则" and path == "/v1/messages"）', '<input data-f="cond.expr" value="' + esc(c.expr||'') + '" placeholder="model ~ \"mini|micro\" and header:X-Client == android" />', true);
  if (r.type === 'inject') {
    var opts = ['<option value="">（不引用）</option>'].concat(((state.status&&state.status.profiles)||[]).map(function(p){
      return '<option value="' + esc(p.id) + '"' + (r.profile_id===p.id?' selected':'') + '>' + esc(p.id) + '（' + esc(p.name) + '）</option>';
    })).join('');
    h += field('引用策略（留空则用下面的文本）', '<select data-f="profile_id">' + opts + '</select>');
    h += field('注入指令', '<textarea data-f="instruction">' + esc(r.instruction||'') + '</textarea>', true);
  }
  if (r.type === 'override') {
    h += field('改写 model 为', '<input data-f="set_model" value="' + esc(r.set_model||'') + '" />');
    h += field('改写参数（JSON，例如 {"temperature":0.2}）', '<textarea data-f="set_params">' + esc(r.set_params?JSON.stringify(r.set_params):'') + '</textarea>', true);
  }
  if (r.type === 'header') {
    h += field('请求头名', '<input data-f="header_name" value="' + esc(r.header_name||'') + '" />');
    h += field('请求头值', '<input data-f="header_value" value="' + esc(r.header_value||'') + '" />');
  }
  if (r.type === 'deny') {
    h += field('HTTP 状态码', '<input data-f="status" type="number" value="' + esc(r.status||403) + '" />');
    h += field('提示信息', '<input data-f="message" value="' + esc(r.message||'') + '" />');
  }
  h += '<div class="field"><label>停用此节点</label><label><input type="checkbox" data-f="disabled"' + (r.disabled?' checked':'') + ' /> 停用</label></div>';
  h += '<div class="full row" style="margin:0"><button class="primary" data-act="apply" data-i="' + i + '">保存节点</button>' +
       '<button data-act="cancel" data-i="' + i + '">取消</button></div>';
  h += '</div>';
  return h;
}
function field(label, inner, full){
  return '<div class="field' + (full?' full':'') + '"><label>' + esc(label) + '</label>' + inner + '</div>';
}

function ruleStat(id){
  var m = (state.status && state.status.rule_stats) || {};
  return m[id] || { matched:0, skipped:0, denied:0, last_hit:0 };
}
function heatHTML(id){
  var st = ruleStat(id);
  var total = st.matched + st.skipped;
  if (!total) { return '<div class="heat"><i style="width:0"></i></div><div class="heat-label">尚未命中</div>'; }
  var ratio = Math.round(st.matched * 100 / total);
  var color = ratio >= 70 ? 'var(--err)' : (ratio >= 30 ? 'var(--warn)' : 'var(--ok)');
  return '<div class="heat"><i style="width:' + ratio + '%;background:' + color + '"></i></div>' +
    '<div class="heat-label">命中 ' + st.matched + ' · 跳过 ' + st.skipped + ' · 拦截 ' + st.denied + ' · 占比 ' + ratio + '%</div>';
}
function renderHeat(d){
  var stats = d.rule_stats || {};
  var rules = state.rules || [];
  var rows = rules.map(function(r){
    var st = stats[r.id] || { matched:0, skipped:0, denied:0, last_hit:0 };
    var total = st.matched + st.skipped;
    var ratio = total ? Math.round(st.matched * 100 / total) : 0;
    var color = ratio >= 70 ? 'var(--err)' : (ratio >= 30 ? 'var(--warn)' : 'var(--ok)');
    var when = st.last_hit ? new Date(st.last_hit * 1000).toLocaleTimeString() : '-';
    return '<tr>' +
      '<td class="mono">' + esc(r.id) + ' <span class="muted">' + esc(r.name||'') + '</span></td>' +
      '<td class="mono">' + st.matched + '</td>' +
      '<td class="mono">' + st.skipped + '</td>' +
      '<td class="mono">' + st.denied + '</td>' +
      '<td class="mono">' + ratio + '%</td>' +
      '<td><div class="bar"><i style="width:' + ratio + '%;background:' + color + '"></i></div></td>' +
      '<td class="mono muted">' + when + '</td>' +
      '</tr>';
  }).join('');
  document.getElementById('heatTable').querySelector('tbody').innerHTML =
    rows || '<tr><td colspan="7" class="muted">还没有规则</td></tr>';
}

function bindCanvas(){
  var canvas = document.getElementById('ruleCanvas');
  canvas.querySelectorAll('button[data-act]').forEach(function(btn){
    btn.addEventListener('click', function(){
      var act = btn.dataset.act, i = parseInt(btn.dataset.i, 10);
      if (act === 'edit') { state.editing = (state.editing === i ? -1 : i); renderRules(); }
      else if (act === 'del') { state.rules.splice(i,1); state.editing = -1; state.dirty = true; renderRules(); }
      else if (act === 'up') { if (i>0) { moveRule(i, i-1); } }
      else if (act === 'down') { if (i < state.rules.length-1) { moveRule(i, i+1); } }
      else if (act === 'cancel') { state.editing = -1; renderRules(); }
      else if (act === 'apply') { applyNode(i); }
    });
  });
  canvas.querySelectorAll('.node').forEach(function(node){
    node.addEventListener('dragstart', function(ev){
      state.dragFrom = parseInt(node.dataset.i,10);
      node.classList.add('dragging');
      try { ev.dataTransfer.setData('text/plain', node.dataset.i); ev.dataTransfer.effectAllowed='move'; } catch(e){}
    });
    node.addEventListener('dragend', function(){ node.classList.remove('dragging'); });
    node.addEventListener('dragover', function(ev){ ev.preventDefault(); node.classList.add('over'); });
    node.addEventListener('dragleave', function(){ node.classList.remove('over'); });
    node.addEventListener('drop', function(ev){
      ev.preventDefault(); node.classList.remove('over');
      var to = parseInt(node.dataset.i,10), from = state.dragFrom;
      if (!isNaN(from) && !isNaN(to) && from !== to) { moveRule(from, to); }
    });
  });
}

function moveRule(from, to){
  var item = state.rules.splice(from,1)[0];
  state.rules.splice(to,0,item);
  state.dirty = true;
  state.editing = -1;
  renderRules();
}

function applyNode(i){
  var node = document.querySelector('.node[data-i="' + i + '"]');
  if (!node) { return; }
  var r = state.rules[i];
  var cond = {};
  node.querySelectorAll('[data-f]').forEach(function(el){
    var f = el.dataset.f;
    var v = el.type === 'checkbox' ? el.checked : el.value;
    if (f === 'name') { r.name = v; }
    else if (f === 'disabled') { r.disabled = !!v; }
    else if (f === 'instruction') { r.instruction = v; }
    else if (f === 'profile_id') { r.profile_id = v; }
    else if (f === 'set_model') { r.set_model = v; }
    else if (f === 'set_params') {
      if (v.trim()) { try { r.set_params = JSON.parse(v); } catch(e) { alert('参数不是合法 JSON'); return; } }
      else { delete r.set_params; }
    }
    else if (f === 'header_name') { r.header_name = v; }
    else if (f === 'header_value') { r.header_value = v; }
    else if (f === 'status') { r.status = parseInt(v,10) || 0; }
    else if (f === 'message') { r.message = v; }
    else if (f === 'cond.models') { var a = splitList(v); if (a.length) { cond.models = a; } }
    else if (f === 'cond.paths') { var b = splitList(v); if (b.length) { cond.paths = b; } }
    else if (f === 'cond.header_key') { if (v) { cond.header_key = v; } }
    else if (f === 'cond.header_value') { if (v) { cond.header_value = v; } }
    else if (f === 'cond.body_contains') { if (v) { cond.body_contains = v; } }
    else if (f === 'cond.expr') { if (v) { cond.expr = v; } }
  });
  if (Object.keys(cond).length) { r.condition = cond; } else { delete r.condition; }
  state.dirty = true;
  state.editing = -1;
  renderRules();
}

document.querySelectorAll('button[data-add]').forEach(function(btn){
  btn.addEventListener('click', function(){
    var type = btn.dataset.add;
    var id = 'rule-' + Date.now().toString(36);
    var base = { id:id, name:TYPE_LABEL[type] + '节点', type:type };
    if (type === 'inject') { base.instruction = ''; }
    if (type === 'deny') { base.status = 403; base.message = '请求被规则拦截'; }
    if (type === 'match') { base.condition = {}; }
    state.rules.push(base);
    state.dirty = true;
    state.editing = state.rules.length - 1;
    renderRules();
  });
});

document.getElementById('saveRules').addEventListener('click', function(){
  post('rules', { rules: state.rules }).then(function(r){
    if (r.ok) { state.dirty = false; note('规则已保存，共 ' + state.rules.length + ' 个节点'); load(); }
    else { alert('保存失败：' + JSON.stringify(r.body)); }
  });
});
document.getElementById('reloadRules').addEventListener('click', function(){
  state.editing = -1; state.dirty = false; load();
});
document.getElementById('resetHeat').addEventListener('click', function(){
  fetch('/admin/api/rules/stats', { method:'DELETE', headers: headers() }).then(function(){
    note('规则统计已清零'); load();
  });
});
document.getElementById('runTest').addEventListener('click', function(){
  var body = null;
  try { body = JSON.parse(document.getElementById('testBody').value || '{}'); }
  catch(e) { alert('请求体不是合法 JSON'); return; }
  post('rules/test', {
    model: document.getElementById('testModel').value,
    path: document.getElementById('testPath').value,
    body: body
  }).then(function(r){
    var out = document.getElementById('testOut');
    out.style.display = 'block';
    if (!r.ok) { out.innerHTML = '<span class="lv-ERR">试算失败：' + esc(JSON.stringify(r.body)) + '</span>'; return; }
    var d = r.body;
    var html = '<div><b>命中</b>：' + esc((d.matched||[]).join(', ') || '（无）') + '</div>' +
      '<div><b>跳过</b>：' + esc((d.skipped||[]).join(', ') || '（无）') + '</div>' +
      '<div><b>拦截</b>：' + (d.denied ? '<span class="lv-ERR">' + esc(d.status) + ' ' + esc(d.message) + '</span>' : '否') + '</div>';
    if (d.model) { html += '<div><b>模型改写</b>：' + esc(d.model) + '</div>'; }
    if (d.instruction) { html += '<div><b>规则注入</b>：<pre>' + esc(d.instruction) + '</pre></div>'; }
    if (d.params) { html += '<div><b>参数</b>：<pre>' + esc(JSON.stringify(d.params)) + '</pre></div>'; }
    if (d.headers) { html += '<div><b>请求头</b>：<pre>' + esc(JSON.stringify(d.headers)) + '</pre></div>'; }
    if (d.profile) { html += '<div><b>回落到策略</b>：' + esc(d.profile) + '</div>'; }
    if (d.preview_body) { html += '<div style="margin-top:8px"><b>最终请求体</b><pre>' + esc(d.preview_body) + '</pre></div>'; }
    out.innerHTML = html;
  });
});

function loadTemplates(){
  api('templates').then(function(r){
    state.templates = (r.body && r.body.templates) || [];
    renderTemplates();
  });
}
function renderTemplates(){
  var q = (document.getElementById('tplSearch').value || '').toLowerCase();
  var list = state.templates.filter(function(t){
    if (!q) { return true; }
    var hay = (t.name + ' ' + t.category + ' ' + (t.tags||[]).join(' ')).toLowerCase();
    return hay.indexOf(q) >= 0;
  });
  document.getElementById('tplCount').textContent = list.length + ' / ' + state.templates.length;
  document.getElementById('tplList').innerHTML = list.map(function(t){
    var active = state.selectedTpl && state.selectedTpl.id === t.id ? ' active' : '';
    return '<button class="' + active.trim() + '" data-tpl="' + esc(t.id) + '">' +
      '<span class="n">' + esc(t.name) + (t.builtin ? ' <span class="pill off">内置</span>' : ' <span class="pill ok">自定义</span>') + '</span>' +
      '<span class="c">' + esc(t.category) + (t.tags && t.tags.length ? ' · ' + esc(t.tags.join('/')) : '') + '</span>' +
      '</button>';
  }).join('') || '<div class="muted">没有匹配的模板</div>';
  document.querySelectorAll('#tplList button').forEach(function(b){
    b.addEventListener('click', function(){
      var t = state.templates.filter(function(x){ return x.id === b.dataset.tpl; })[0];
      state.selectedTpl = t || null;
      renderTemplates();
      renderTplEditor();
    });
  });
}
function renderTplEditor(){
  var t = state.selectedTpl;
  document.getElementById('tplTitle').textContent = t ? t.name : '选择一个模板';
  document.getElementById('tplMeta').textContent = t ? (t.category + (t.builtin ? ' · 内置（不可覆盖）' : ' · 自定义')) : '';
  document.getElementById('tplText').value = t ? t.instruction : '';
}
document.getElementById('tplSearch').addEventListener('input', renderTemplates);
document.getElementById('applyTpl').addEventListener('click', function(){
  if (!state.selectedTpl) { alert('先选一个模板'); return; }
  var mode = document.getElementById('applyMode').value;
  var target = document.getElementById('applyTarget').value;
  post('templates/apply', {
    template_id: state.selectedTpl.id,
    profile_id: target,
    mode: mode,
    name: document.getElementById('applyName').value
  }).then(function(r){
    document.getElementById('applyHint').textContent = r.ok ? '已应用并热加载' : ('失败：' + JSON.stringify(r.body));
    if (r.ok) { load(); }
  });
});
document.getElementById('saveTpl').addEventListener('click', function(){
  var t = state.selectedTpl;
  var text = document.getElementById('tplText').value.trim();
  if (!text) { alert('模板内容为空'); return; }
  var id = t && !t.builtin ? t.id : 'tpl-' + Date.now().toString(36);
  var name = t ? (t.name + (t.builtin ? '（副本）' : '')) : '自定义模板';
  post('templates', { id:id, name:name, category: t ? t.category : '自定义', instruction:text, tags: t ? t.tags : [] }).then(function(r){
    if (r.ok) { document.getElementById('applyHint').textContent = '已保存为自定义模板'; loadTemplates(); }
    else { alert('保存失败：' + JSON.stringify(r.body)); }
  });
});
document.getElementById('delTpl').addEventListener('click', function(){
  var t = state.selectedTpl;
  if (!t || t.builtin) { alert('内置模板不能删除'); return; }
  post('templates/delete', { id: t.id }).then(function(r){
    if (r.ok) { state.selectedTpl = null; loadTemplates(); renderTplEditor(); }
  });
});

function renderLogs(logs){
  var body = document.getElementById('logs').querySelector('tbody');
  body.innerHTML = logs.slice().reverse().map(function(l){
    return '<tr><td class="mono">' + esc(l.time) + '</td>' +
      '<td class="mono lv-' + esc(l.level) + '">' + esc(l.level) + '</td>' +
      '<td class="mono"><pre>' + esc(l.message) + '</pre></td></tr>';
  }).join('') || '<tr><td colspan="3" class="muted">暂无日志</td></tr>';
}

document.getElementById('checkNow').addEventListener('click', function(){
  post('check', {}).then(function(){ setTimeout(load, 800); });
});
document.getElementById('loadCfg').addEventListener('click', loadConfig);
document.getElementById('saveCfg').addEventListener('click', function(){
  var text = document.getElementById('cfgBox').value;
  var parsed;
  try { parsed = JSON.parse(text); } catch(e) {
    document.getElementById('cfgHint').textContent = 'JSON 语法错误：' + e.message;
    return;
  }
  post('config', parsed).then(function(r){
    document.getElementById('cfgHint').textContent = r.ok ? '已保存并热加载' : ('保存失败：' + JSON.stringify(r.body));
    if (r.ok) { load(); loadSnapshots(); }
  });
});
function loadReplay(){
  api('replay').then(function(r){
    if (!r.ok) { return; }
    renderReplay((r.body && r.body.entries) || []);
  });
}
function renderReplay(list){
  var body = document.getElementById('replayTable').querySelector('tbody');
  if (!list.length) { body.innerHTML = '<tr><td colspan="7" class="muted">还没有被拦截的请求</td></tr>'; return; }
  body.innerHTML = list.map(function(e){
    var when = new Date(e.time * 1000).toLocaleString();
    return '<tr>' +
      '<td class="mono">' + esc(when) + '</td>' +
      '<td class="mono">' + esc(e.method + ' ' + e.path) + '</td>' +
      '<td class="mono">' + esc(e.model) + '</td>' +
      '<td class="mono lv-ERR">' + e.status + '</td>' +
      '<td class="mono muted">' + esc((e.rules||[]).join(', ') || '-') + '</td>' +
      '<td class="muted">' + esc(e.message) + '</td>' +
      '<td><button data-replay="' + esc(e.id) + '">复现</button></td>' +
      '</tr>';
  }).join('');
  body.querySelectorAll('button[data-replay]').forEach(function(b){
    b.addEventListener('click', function(){ replayAgain(b.dataset.replay); });
  });
}
function replayAgain(id){
  post('replay/test', { id: id }).then(function(r){
    var out = document.getElementById('replayOut');
    out.style.display = 'block';
    if (!r.ok) { out.innerHTML = '<span class="lv-ERR">复现失败：' + esc(JSON.stringify(r.body)) + '</span>'; return; }
    var d = r.body;
    var html = '<div><b>记录</b>：' + esc(d.entry.id + ' · ' + d.entry.method + ' ' + d.entry.path + ' · ' + d.entry.model) + '</div>' +
      '<div><b>当前命中</b>：' + esc((d.matched||[]).join(', ') || '（无）') + '</div>' +
      '<div><b>当前跳过</b>：' + esc((d.skipped||[]).join(', ') || '（无）') + '</div>' +
      '<div><b>会不会被拦</b>：' + (d.denied ? '<span class="lv-ERR">会（' + esc(d.status) + ' ' + esc(d.message) + '）</span>' : '<span style="color:var(--ok)">不会</span>') + '</div>';
    if (d.instruction) { html += '<div><b>将注入</b>：<pre>' + esc(d.instruction) + '</pre></div>'; }
    if (d.preview_body) { html += '<div style="margin-top:8px"><b>最终请求体</b><pre>' + esc(d.preview_body) + '</pre></div>'; }
    html += '<div class="muted" style="margin-top:8px">原请求体：' + esc(d.entry.truncated ? '（已截断，' + d.entry.body_size + ' 字节）' : '') + '<pre>' + esc(d.entry.body) + '</pre></div>';
    out.innerHTML = html;
  });
}
document.getElementById('reloadReplay').addEventListener('click', loadReplay);
document.getElementById('clearReplay').addEventListener('click', function(){
  fetch('/admin/api/replay', { method: 'DELETE', headers: headers() }).then(function(){ note('拦截回放已清空'); loadReplay(); });
});

function loadFederation(){
  api('federation').then(function(r){
    if (!r.ok) { return; }
    renderFederation(r.body || {});
  }).catch(function(){});
}
function renderFederation(d){
  var body = document.getElementById('fedTable').querySelector('tbody');
  var peers = d.peers || [];
  var rows = peers.map(function(p){
    var pill = p.ok ? '<span class="pill ok">在线</span>' : '<span class="pill err">离线</span>';
    return '<tr>' +
      '<td class="mono">' + esc(p.id) + '<div class="muted">' + esc(p.base_url) + '</div></td>' +
      '<td>' + pill + '</td>' +
      '<td class="mono">' + esc(p.version || '-') + '</td>' +
      '<td class="mono">' + esc(p.uptime || '-') + '</td>' +
      '<td class="mono">' + (p.requests || 0) + '</td>' +
      '<td class="mono">' + (p.rule_denies || 0) + '</td>' +
      '<td class="mono">' + (p.upstreams || 0) + (p.unhealthy ? ' <span class="lv-ERR">(' + p.unhealthy + ')</span>' : '') + '</td>' +
      '<td class="mono">' + (p.rules || 0) + '</td>' +
      '<td class="mono">' + (p.latency_ms || 0) + 'ms</td>' +
      '</tr>' + (p.error ? '<tr><td colspan="9" class="lv-ERR mono">' + esc(p.error) + '</td></tr>' : '');
  }).join('');
  body.innerHTML = rows || '<tr><td colspan="9" class="muted">未配置联邦 peers（config.federation.peers）</td></tr>';
  document.getElementById('fedHint').textContent =
    (d.enabled ? '联邦已启用' : '联邦已关闭') + ' · peers ' + (d.peer_count || 0) +
    ' · 在线 ' + (d.peer_healthy || 0) + ' · 这些实例合计处理 ' + (d.peer_requests || 0) + ' 次请求';
}
document.getElementById('reloadFed').addEventListener('click', loadFederation);

document.getElementById('runBatch').addEventListener('click', function(){
  var from = document.getElementById('batchFrom').value;
  var limit = parseInt(document.getElementById('batchLimit').value || '100', 10);
  post('rules/batch', { from: from, limit: limit }).then(function(r){
    var out = document.getElementById('batchOut');
    out.style.display = 'block';
    if (!r.ok) { out.innerHTML = '<span class="lv-ERR">回归失败：' + esc(JSON.stringify(r.body)) + '</span>'; return; }
    var d = r.body;
    var tally = Object.keys(d.rule_matches || {}).sort().map(function(id){
      return '<tr><td class="mono">' + esc(id) + '</td><td class="mono">' + d.rule_matches[id] + '</td></tr>';
    }).join('') || '<tr><td colspan="2" class="muted">无命中</td></tr>';
    var items = (d.items || []).slice(0, 50).map(function(it){
      return '<tr><td class="mono">' + esc(it.source) + '</td><td class="mono">' + esc(it.model) + '</td>' +
        '<td class="mono">' + esc(it.path) + '</td>' +
        '<td>' + (it.denied ? '<span class="lv-ERR">拦截 ' + it.status + '</span>' : '<span style="color:var(--ok)">放行</span>') + '</td>' +
        '<td class="mono muted">' + esc((it.matched || []).join(', ')) + '</td></tr>';
    }).join('');
    out.innerHTML = '<div><b>总计</b> ' + d.total + ' · <b>拦截</b> ' + d.denied + ' · <b>放行</b> ' + d.allowed + '</div>' +
      '<table style="margin-top:8px"><thead><tr><th>规则</th><th>命中次数</th></tr></thead><tbody>' + tally + '</tbody></table>' +
      '<table style="margin-top:12px"><thead><tr><th>来源</th><th>模型</th><th>路径</th><th>结果</th><th>命中规则</th></tr></thead><tbody>' +
      (items || '<tr><td colspan="5" class="muted">没有可回归的请求</td></tr>') + '</tbody></table>';
  });
});

function loadSamples(){
  api('samples').then(function(r){
    if (!r.ok) { return; }
    renderSamples((r.body && r.body.entries) || []);
    var hint = document.getElementById('sampleHint');
    if (r.body) { hint.textContent = '每 ' + (r.body.every || 0) + ' 个成功请求留 1 条，保留 ' + (r.body.limit || 0) + ' 条（0 表示未开启）'; }
  });
}
function renderSamples(list){
  var body = document.getElementById('sampleTable').querySelector('tbody');
  if (!list.length) { body.innerHTML = '<tr><td colspan="7" class="muted">还没有采样记录（设置 server.sample_every > 0 开启）</td></tr>'; return; }
  body.innerHTML = list.map(function(e){
    var when = new Date(e.time * 1000).toLocaleString();
    return '<tr>' +
      '<td class="mono">' + esc(when) + '</td>' +
      '<td class="mono">' + esc(e.method + ' ' + e.path) + '</td>' +
      '<td class="mono">' + esc(e.model) + '</td>' +
      '<td class="mono">' + e.status + '</td>' +
      '<td class="mono muted">' + esc(e.upstream || '-') + '</td>' +
      '<td class="mono">' + (e.latency_ms || 0) + 'ms</td>' +
      '<td><button data-sample="' + esc(e.id) + '">复现</button></td>' +
      '</tr>';
  }).join('');
  body.querySelectorAll('button[data-sample]').forEach(function(b){
    b.addEventListener('click', function(){ replaySample(b.dataset.sample); });
  });
}
function replaySample(id){
  post('samples/test', { id: id }).then(function(r){
    var out = document.getElementById('sampleOut');
    out.style.display = 'block';
    if (!r.ok) { out.innerHTML = '<span class="lv-ERR">复现失败：' + esc(JSON.stringify(r.body)) + '</span>'; return; }
    var d = r.body;
    out.innerHTML = '<div><b>记录</b>：' + esc(d.entry.id + ' · ' + d.entry.model + ' · ' + d.entry.upstream) + '</div>' +
      '<div><b>当前命中</b>：' + esc((d.matched||[]).join(', ') || '（无）') + '</div>' +
      '<div><b>现在会被拦</b>：' + (d.denied ? '<span class="lv-ERR">会</span>' : '<span style="color:var(--ok)">不会</span>') + '</div>' +
      (d.instruction ? '<div><b>将注入</b>：<pre>' + esc(d.instruction) + '</pre></div>' : '') +
      (d.preview_body ? '<div style="margin-top:8px"><b>最终请求体</b><pre>' + esc(d.preview_body) + '</pre></div>' : '');
  });
}
document.getElementById('reloadSamples').addEventListener('click', loadSamples);
document.getElementById('clearSamples').addEventListener('click', function(){
  fetch('/admin/api/samples', { method: 'DELETE', headers: headers() }).then(function(){ note('采样已清空'); loadSamples(); });
});

function loadDSL(){
  api('rules/dsl').then(function(r){
    if (r.ok && r.body && r.body.text) { document.getElementById('dslBox').value = r.body.text; }
  });
}
document.getElementById('dslGen').addEventListener('click', loadDSL);
document.getElementById('dslCheck').addEventListener('click', function(){
  post('rules/dsl/preview', { text: document.getElementById('dslBox').value }).then(function(r){
    var hint = document.getElementById('dslHint');
    if (r.body && r.body.ok) { hint.textContent = '解析通过：' + r.body.count + ' 条规则'; }
    else { hint.textContent = '解析失败：' + ((r.body && r.body.error) || '未知错误'); }
  });
});
document.getElementById('dslApply').addEventListener('click', function(){
  post('rules/dsl', { text: document.getElementById('dslBox').value }).then(function(r){
    var hint = document.getElementById('dslHint');
    if (r.ok) { hint.textContent = '已应用 ' + r.body.count + ' 条规则'; state.dirty = false; load(); loadDSL(); }
    else { hint.textContent = '应用失败：' + JSON.stringify(r.body); }
  });
});

function loadGitLog(){
  api('snapshots/git/log?limit=20').then(function(r){
    var body = document.getElementById('gitTable').querySelector('tbody');
    if (!r.ok) { return; }
    var d = r.body || {};
    if (!d.available) {
      body.innerHTML = '<tr><td colspan="3" class="muted">未检测到 git，无法使用快照版本库</td></tr>';
      return;
    }
    var entries = d.entries || [];
    body.innerHTML = entries.map(function(e){
      return '<tr><td class="mono">' + esc(e.hash) + '</td><td class="mono">' + esc(e.time) + '</td><td class="mono muted">' + esc(e.subject) + '</td></tr>';
    }).join('') || '<tr><td colspan="3" class="muted">还没有提交</td></tr>';
    if (d.error) { document.getElementById('gitHint').textContent = 'git 提示：' + d.error; }
    else if (d.dir) { document.getElementById('gitHint').textContent = '仓库目录 ' + d.dir; }
  });
}
document.getElementById('reloadGit').addEventListener('click', loadGitLog);
document.getElementById('commitGit').addEventListener('click', function(){
  var msg = prompt('提交说明', 'manual');
  if (msg === null) { return; }
  post('snapshots/git/commit', { message: msg }).then(function(r){
    if (r.ok) { loadGitLog(); } else { alert('提交失败：' + JSON.stringify(r.body)); }
  });
});

function loadSnapshots(){
  api('snapshots').then(function(r){
    if (!r.ok) { return; }
    renderSnapshots((r.body && r.body.snapshots) || []);
    var hint = document.getElementById('snapHint');
    if (r.body && r.body.dir) {
      hint.textContent = '目录 ' + r.body.dir + ' · 保留 ' + r.body.limit + ' 版';
    }
  });
}
function renderSnapshots(list){
  var body = document.getElementById('snapTable').querySelector('tbody');
  if (!list.length) { body.innerHTML = '<tr><td colspan="7" class="muted">还没有快照</td></tr>'; return; }
  body.innerHTML = list.map(function(s){
    var when = new Date(s.time * 1000).toLocaleString();
    var size = (s.size / 1024).toFixed(1) + ' KB';
    return '<tr>' +
      '<td class="mono">' + esc(when) + '</td>' +
      '<td class="mono">' + size + '</td>' +
      '<td class="mono">' + (s.upstreams||0) + '</td>' +
      '<td class="mono">' + (s.profiles||0) + '</td>' +
      '<td class="mono">' + (s.rules||0) + '</td>' +
      '<td class="mono muted">' + esc(s.name) + '</td>' +
      '<td>' +
        '<button data-snap-view="' + esc(s.name) + '">查看</button> ' +
        '<button data-snap-roll="' + esc(s.name) + '">回滚</button> ' +
        '<button class="danger" data-snap-del="' + esc(s.name) + '">删除</button>' +
      '</td></tr>';
  }).join('');
  body.querySelectorAll('button[data-snap-view]').forEach(function(b){
    b.addEventListener('click', function(){ viewSnapshot(b.dataset.snapView); });
  });
  body.querySelectorAll('button[data-snap-roll]').forEach(function(b){
    b.addEventListener('click', function(){ rollbackSnapshot(b.dataset.snapRoll); });
  });
  body.querySelectorAll('button[data-snap-del]').forEach(function(b){
    b.addEventListener('click', function(){ deleteSnapshotUI(b.dataset.snapDel); });
  });
}
function viewSnapshot(name){
  api('snapshots/get?name=' + encodeURIComponent(name)).then(function(r){
    if (!r.ok) { alert('读取失败：' + JSON.stringify(r.body)); return; }
    document.getElementById('cfgBox').value = JSON.stringify(r.body, null, 2);
    document.getElementById('cfgHint').textContent = '正在查看快照 ' + name + '（未应用，点保存才会生效）';
  });
}
function rollbackSnapshot(name){
  if (!confirm('回滚到 ' + name + ' ？当前配置会先自动存一版。')) { return; }
  post('snapshots/rollback', { name: name }).then(function(r){
    if (r.ok) { document.getElementById('cfgHint').textContent = '已回滚到 ' + name; load(); loadConfig(); loadSnapshots(); }
    else { alert('回滚失败：' + JSON.stringify(r.body)); }
  });
}
function deleteSnapshotUI(name){
  if (!confirm('删除快照 ' + name + ' ？')) { return; }
  post('snapshots/delete', { name: name }).then(function(r){
    if (r.ok) { loadSnapshots(); } else { alert('删除失败：' + JSON.stringify(r.body)); }
  });
}
document.getElementById('newSnap').addEventListener('click', function(){
  var note = prompt('快照备注（可留空）', 'manual');
  if (note === null) { return; }
  post('snapshots/create', { note: note }).then(function(r){
    if (r.ok) { loadSnapshots(); } else { alert('创建失败：' + JSON.stringify(r.body)); }
  });
});
document.getElementById('reloadSnap').addEventListener('click', loadSnapshots);

function loadConfig(){
  api('config').then(function(r){
    if (!r.ok) { document.getElementById('cfgHint').textContent = '读取失败'; return; }
    document.getElementById('cfgBox').value = JSON.stringify(r.body, null, 2);
    document.getElementById('cfgHint').textContent = '';
  });
}

try {
  var saved = localStorage.getItem('pojiagw_token');
  if (saved) { document.getElementById('token').value = saved; }
} catch(e){}

loadConfig();
loadTemplates();
loadSnapshots();
loadGitLog();
loadReplay();
loadSamples();
loadDSL();
loadFederation();
load();
setInterval(load, 3000);
setInterval(loadTemplates, 30000);
</script>
</body>
</html>
`
