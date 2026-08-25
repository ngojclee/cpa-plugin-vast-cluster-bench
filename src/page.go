package main

// statusPageHTML is the dashboard shell served without management auth.
// All data loads come from the plugin's own resource routes (also
// unauthenticated) so the page is read-only for everyone on the LAN.
// The management key is asked ONLY when the user actually saves keys or
// triggers a probe — and an error is shown only when that key is wrong.
const statusPageHTML = `<!doctype html>
<html lang="vi">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Vast Cluster Bench</title>
<style>
:root { color-scheme: light dark; font-family: ui-sans-serif, system-ui, sans-serif; }
* { box-sizing: border-box; }
body { margin: 0; padding: 20px; background: Canvas; color: CanvasText; }
h1 { font-size: 19px; margin: 0 0 2px; display: flex; align-items: center; gap: 8px; }
.sub { opacity: .6; font-size: 12px; margin-bottom: 14px; }
.card { border: 1px solid color-mix(in srgb, CanvasText 16%, transparent); border-radius: 10px; padding: 14px; margin-bottom: 14px; }
.toolbar { display: flex; gap: 8px; align-items: center; margin-bottom: 10px; flex-wrap: wrap; }
button { padding: 5px 12px; border-radius: 6px; border: 1px solid color-mix(in srgb, CanvasText 25%, transparent); background: transparent; color: inherit; cursor: pointer; font-size: 13px; }
button:hover { background: color-mix(in srgb, CanvasText 8%, transparent); }
button.primary { background: color-mix(in srgb, CanvasText 10%, transparent); }
input { padding: 5px 9px; border-radius: 6px; border: 1px solid color-mix(in srgb, CanvasText 25%, transparent); background: transparent; color: inherit; font-size: 13px; width: 100%; }
label { display: block; font-size: 12px; margin-bottom: 8px; }
label .muted { display: block; }
table { border-collapse: collapse; width: 100%; font-size: 12.5px; }
th, td { text-align: left; padding: 5px 8px; border-bottom: 1px solid color-mix(in srgb, CanvasText 10%, transparent); vertical-align: top; }
th { font-weight: 600; white-space: nowrap; }
.ok { color: #16a34a; } .bad { color: #dc2626; } .warn { color: #d97706; }
.muted { opacity: .6; font-size: 11px; }
.dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-right: 5px; }
.dot.ok { background: #16a34a; } .dot.bad { background: #dc2626; } .dot.warn { background: #d97706; }
.spark { width: 110px; height: 24px; display: block; }
.bar { display: inline-block; width: 60px; height: 7px; border-radius: 4px; background: color-mix(in srgb, CanvasText 12%, transparent); vertical-align: middle; margin-right: 5px; }
.bar > i { display: block; height: 100%; border-radius: 4px; background: #16a34a; }
.bar > i.warn { background: #d97706; } .bar > i.bad { background: #dc2626; }
#error { display: none; background: color-mix(in srgb, #dc2626 12%, transparent); color: #dc2626; border: 1px solid color-mix(in srgb, #dc2626 40%, transparent); padding: 7px 10px; border-radius: 6px; margin-bottom: 10px; font-size: 12.5px; }
#config-card { display: none; }
#config-card.open { display: block; }
.hint { color: color-mix(in srgb, CanvasText 55%, transparent); font-size: 11px; margin-top: 2px; }
.row { display: flex; gap: 10px; flex-wrap: wrap; align-items: flex-end; }
.row > div { flex: 1 1 220px; }
#keysaved { color: #16a34a; font-size: 12px; }
.gpu { font-size: 10.5px; opacity: .75; white-space: nowrap; }
</style>
</head>
<body>
<h1>⚡ Vast Cluster Bench</h1>
<div class="sub" id="meta">Đang tải…</div>

<div class="card">
  <div class="toolbar">
    <button id="probe" class="primary">⚡ Probe now</button>
    <button id="refresh" title="Làm mới">⟳</button>
    <button id="config-toggle">⚙ Cấu hình</button>
    <span class="muted" id="keysaved"></span>
  </div>
  <div id="error"></div>
  <div id="content" class="muted">Đang tải dữ liệu…</div>
</div>

<div class="card" id="config-card">
  <div class="row">
    <div>
      <label>Vast API Key
        <input type="password" id="vastkey" placeholder="••••••••" autocomplete="off">
        <span class="hint">Để trống + Lưu = dùng env <code>VAST_API_KEY</code>.</span>
      </label>
    </div>
    <div>
      <label>SSH Private Key
        <input type="password" id="sshkey" placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" autocomplete="off">
        <span class="hint">Để trống + Lưu = dùng <code>ssh-key-path</code>.</span>
      </label>
    </div>
    <div>
      <label>Management Key
        <input type="password" id="mkey" placeholder="management key" autocomplete="off">
        <span class="hint">Chỉ cần khi Lưu / Probe. Xem dashboard không cần.</span>
      </label>
    </div>
  </div>
  <div class="toolbar" style="margin-top:8px; margin-bottom:0">
    <button id="savekeys" class="primary">Lưu keys</button>
    <button id="clearkeys">Bỏ keys (dùng env/path)</button>
  </div>
</div>

<script>
const RES = '/v0/resource/plugins/vast-cluster-bench';
const MGT = '/v0/management/plugins/vast-cluster-bench';
const $ = s => document.querySelector(s);
function mkey() { return $('#mkey').value.trim(); }
function mgmtHeaders() {
  const h = { 'Content-Type': 'application/json' };
  const k = mkey();
  if (k) h['Authorization'] = 'Bearer ' + k;
  return h;
}
function fmtTime(iso) { return iso ? new Date(iso).toLocaleString() : ''; }
function fmtNum(v, d) { return (v === undefined || v === null || isNaN(v)) ? '—' : Number(v).toFixed(d); }
function statusDot(status) {
  if (status === 'ok') return '<span class="dot ok"></span>ok';
  if (status === 'no_engine') return '<span class="dot warn"></span>no engine';
  if (status === 'ssh_error') return '<span class="dot bad"></span>ssh error';
  if (['exited','offline','stopped','paused','error'].includes(status)) return '<span class="dot bad"></span>' + status;
  return '<span class="dot warn"></span>' + (status || 'unknown');
}
function usageBar(pct) {
  if (pct === undefined || pct === null || pct < 0) return '<span class="muted">—</span>';
  const cls = pct >= 90 ? 'bad' : pct >= 70 ? 'warn' : '';
  return '<span class="bar"><i class="' + cls + '" style="width:' + Math.min(100, pct) + '%"></i></span>' + (pct * 100).toFixed(0) + '%';
}
function showError(msg) { $('#error').textContent = msg; $('#error').style.display = msg ? 'block' : 'none'; }
async function api(url, options) {
  const resp = await fetch(url, options || {});
  if (!resp.ok && resp.status !== 202) throw new Error('HTTP ' + resp.status);
  return resp.json();
}
async function load() {
  showError('');
  try {
    const data = await api(RES + '/data');
    $('#meta').textContent = 'v' + data.version + ' · probe ' + data.probe_interval + ' · cập nhật ' + fmtTime(data.generated_at);
    let html = '<table><tr><th>Node</th><th>Trạng thái</th><th>Engine / Model</th><th>tok/s</th><th>TTFT</th><th>Prefill</th><th>KV cache</th><th>GPU</th><th>Giá</th><th>24h</th></tr>';
    for (const n of data.nodes) {
      const v = n.vast || {};
      const gpu = (n.gpus || []).map(g => '<div class="gpu">GPU' + g.idx + ' ' + g.temp_c + '°C · ' + g.w + 'W · ' + g.util_pct + '%</div>').join('');
      const host = (v.cpu_util !== undefined ? '<div class="gpu">CPU ' + fmtNum(v.cpu_util, 0) + '% · RAM ' + fmtNum(v.mem_usage, 0) + 'G · disk ' + fmtNum(v.disk_util, 0) + '%</div>' : '');
      const img = (v.image ? '<div class="gpu" title="' + (v.onstart || '') + '">' + v.image + '</div>' : '');
      let hist = [];
      try { hist = (await api(RES + '/history?node=' + encodeURIComponent(n.name))).points || []; } catch (e) {}
      html += '<tr><td><b>' + n.name + '</b>' + (v.id ? '<br><span class="muted">#' + v.id + '</span>' : '') + '</td>'
        + '<td>' + statusDot(n.status) + (v.status ? '<br><span class="muted">' + v.status + '</span>' : '') + '</td>'
        + '<td>' + (n.engine || '') + (n.model ? '<br><span class="muted">' + n.model + '</span>' : '') + img + '</td>'
        + '<td><b>' + fmtNum(n.decode_tok_s, 1) + '</b>' + (n.cache_hit > 0 ? '<br><span class="muted">hit ' + (n.cache_hit * 100).toFixed(0) + '%</span>' : '') + '</td>'
        + '<td>' + fmtNum(n.ttft_s * 1000, 1) + ' ms</td>'
        + '<td>' + fmtNum(n.prefill_tok_s, 0) + ' tok/s</td>'
        + '<td>' + fmtNum(n.kv_cache_tokens / 1000, 0) + 'K · ' + usageBar(n.kv_usage) + '<br><span class="muted">run ' + fmtNum(n.running, 0) + ' · q ' + fmtNum(n.queue, 0) + '</span></td>'
        + '<td>' + (gpu || '<span class="muted">—</span>') + host + '</td>'
        + '<td>' + (n.price_h ? '$' + n.price_h.toFixed(3) + '/h' : '—')
        + (n.requests_total > 0 ? '<br><span class="muted">' + fmtNum(n.requests_total, 0) + ' req · ' + fmtNum((n.prompt_tokens_total + n.gen_tokens_total) / 1e6, 1) + 'M tok</span>' : '') + '</td>'
        + '<td>' + spark(hist) + '</td></tr>';
    }
    html += '</table>';
    if (data.nodes.length === 0) html += '<p class="muted">Chưa có node nào. Bấm ⚡ Probe now hoặc bật máy Vast.</p>';
    $('#content').innerHTML = html;
  } catch (e) {
    $('#content').innerHTML = '<p class="muted">Không tải được dữ liệu: ' + e + '</p>';
  }
}
function spark(points) {
  const w = 110, h = 24;
  const vals = (points || []).map(p => p.decode_tok_s).filter(v => v !== undefined && v !== null && v > 0);
  if (vals.length < 2) return '<span class="muted">—</span>';
  const max = Math.max(...vals), min = Math.min(...vals);
  const pts = vals.map((v, i) => {
    const x = (i / (vals.length - 1)) * (w - 4) + 2;
    const y = h - 4 - ((v - min) / (max - min || 1)) * (h - 8);
    return x.toFixed(1) + ',' + y.toFixed(1);
  }).join(' ');
  return '<svg class="spark" viewBox="0 0 ' + w + ' ' + h + '" preserveAspectRatio="none"><polyline fill="none" stroke="currentColor" stroke-width="1.5" points="' + pts + '"/></svg>';
}
async function probeNow() {
  showError('');
  if (!mkey()) { showError('Cần management key để Probe now (nhập ở ⚙ Cấu hình).'); return; }
  try {
    await api(MGT + '/probe', { method: 'POST', headers: mgmtHeaders(), body: '{}' });
    $('#keysaved').textContent = '⚡ probe đang chạy…';
    setTimeout(load, 4000);
  } catch (e) { showError('Sai management key? HTTP ' + e.message.replace('HTTP ', '')); }
}
async function saveKeys(clear) {
  showError('');
  if (!mkey()) { showError('Cần management key để lưu (nhập ở ô Management Key).'); return; }
  const body = clear
    ? { vast_api_key: '', ssh_key: '', clear: true }
    : { vast_api_key: $('#vastkey').value.trim(), ssh_key: $('#sshkey').value.trim() };
  try {
    await api(MGT + '/settings', { method: 'POST', headers: mgmtHeaders(), body: JSON.stringify(body) });
    $('#keysaved').textContent = clear ? '✓ đã bỏ keys — dùng env VAST_API_KEY / ssh-key-path' : '✓ đã lưu';
    $('#vastkey').value = ''; $('#sshkey').value = '';
    setTimeout(() => { $('#keysaved').textContent = ''; }, 3500);
  } catch (e) { showError('Sai management key? HTTP ' + e.message.replace('HTTP ', '')); }
}
$('#probe').addEventListener('click', probeNow);
$('#refresh').addEventListener('click', load);
$('#config-toggle').addEventListener('click', () => $('#config-card').classList.toggle('open'));
$('#savekeys').addEventListener('click', () => saveKeys(false));
$('#clearkeys').addEventListener('click', () => saveKeys(true));
load();
setInterval(load, 15000);
</script>
</body>
</html>
`
