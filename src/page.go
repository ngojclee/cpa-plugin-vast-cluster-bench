package main

// statusPageHTML is the unauthenticated resource-page shell. It contains no
// node data; all data loads go through the management-key-gated API with the
// key the user enters in the browser.
const statusPageHTML = `<!doctype html>
<html lang="vi">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Vast Cluster Bench</title>
<style>
:root { color-scheme: light dark; font-family: ui-sans-serif, system-ui, sans-serif; }
body { margin: 0; padding: 24px; background: Canvas; color: CanvasText; }
h1 { font-size: 20px; margin: 0 0 4px; }
.sub { opacity: .65; font-size: 12px; margin-bottom: 16px; }
.card { border: 1px solid color-mix(in srgb, CanvasText 18%, transparent); border-radius: 10px; padding: 16px; margin-bottom: 16px; }
table { border-collapse: collapse; width: 100%; font-size: 13px; }
th, td { text-align: left; padding: 6px 10px; border-bottom: 1px solid color-mix(in srgb, CanvasText 12%, transparent); vertical-align: top; }
th { font-weight: 600; }
input[type=password], input[type=text] { padding: 6px 10px; border-radius: 6px; border: 1px solid color-mix(in srgb, CanvasText 25%, transparent); background: transparent; color: inherit; }
button { padding: 6px 14px; border-radius: 6px; border: 1px solid color-mix(in srgb, CanvasText 25%, transparent); background: transparent; color: inherit; cursor: pointer; }
button:hover { background: color-mix(in srgb, CanvasText 8%, transparent); }
.ok { color: #16a34a; } .bad { color: #dc2626; } .warn { color: #d97706; }
.muted { opacity: .65; font-size: 12px; }
#error { color: #dc2626; margin: 8px 0; }
.dot { display: inline-block; width: 9px; height: 9px; border-radius: 50%; margin-right: 6px; }
.dot.ok { background: #16a34a; } .dot.bad { background: #dc2626; } .dot.warn { background: #d97706; }
.spark { width: 130px; height: 28px; }
.bar { display: inline-block; width: 70px; height: 8px; border-radius: 4px; background: color-mix(in srgb, CanvasText 12%, transparent); vertical-align: middle; margin-right: 6px; }
.bar > i { display: block; height: 100%; border-radius: 4px; background: #16a34a; }
.bar > i.warn { background: #d97706; } .bar > i.bad { background: #dc2626; }
.gpu { font-size: 11px; opacity: .8; }
#nodes td:nth-child(2), #nodes td:nth-child(3), #nodes td:nth-child(4) { white-space: nowrap; }
</style>
</head>
<body>
<h1>⚡ Vast Cluster Bench</h1>
<div class="sub" id="meta">Nhập management key và bấm Load.</div>
<div class="card">
  <label>CPA Management Key
    <input type="password" id="key" placeholder="management key" autocomplete="off">
  </label>
  <button id="load">Load</button>
  <button id="probe" title="Chạy probe ngay lập tức">⚡ Probe now</button>
  <button id="refresh" title="Làm mới dữ liệu">⟳</button>
  <span class="muted" id="meta2"></span>
  <div id="error"></div>
</div>
<div class="card">
  <label>Vast API Key (tùy chọn — nếu chưa có env <code>VAST_API_KEY</code>)
    <input type="password" id="vastkey" placeholder="vast API key" autocomplete="off" style="min-width:300px">
  </label>
  <label>SSH Private Key (tùy chọn — nếu không dùng <code>ssh-key-path</code>)
    <input type="password" id="sshkey" placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" autocomplete="off" style="min-width:360px">
  </label>
  <button id="savekeys">Lưu keys</button>
  <span class="muted" id="keysaved"></span>
</div>
<div class="card">
  <div id="content" class="muted">Nhập management key và bấm Load.</div>
</div>
<script>
const BASE = '/v0/management/plugins/vast-cluster-bench';
const $ = s => document.querySelector(s);
function headers() {
  const value = $('#key').value.trim();
  const h = { 'Content-Type': 'application/json' };
  if (value) h['Authorization'] = 'Bearer ' + value;
  return h;
}
function fmtTime(iso) { return iso ? new Date(iso).toLocaleString() : ''; }
function fmtNum(v, d) { return (v === undefined || v === null || isNaN(v)) ? '—' : Number(v).toFixed(d); }
function statusDot(status) {
  if (status === 'ok') return '<span class="dot ok"></span>ok';
  if (status === 'no_engine') return '<span class="dot warn"></span>no engine';
  if (status === 'ssh_error') return '<span class="dot bad"></span>ssh error';
  if (status === 'exited' || status === 'offline' || status === 'stopped' || status === 'paused' || status === 'error') return '<span class="dot bad"></span>' + status;
  return '<span class="dot warn"></span>' + (status || 'unknown');
}
function usageBar(pct) {
  if (pct === undefined || pct === null || pct < 0) return '<span class="muted">—</span>';
  const cls = pct >= 90 ? 'bad' : pct >= 70 ? 'warn' : '';
  return '<span class="bar"><i class="' + cls + '" style="width:' + Math.min(100, pct) + '%"></i></span>' + (pct * 100).toFixed(0) + '%';
}
function spark(el, points) {
  const w = 130, h = 28;
  const vals = points.map(p => p.decode_tok_s).filter(v => v !== undefined && v !== null && v > 0);
  let svg = '';
  if (vals.length >= 2) {
    const max = Math.max(...vals), min = Math.min(...vals);
    const pts = vals.map((v, i) => {
      const x = (i / (vals.length - 1)) * (w - 4) + 2;
      const y = h - 4 - ((v - min) / (max - min || 1)) * (h - 8);
      return x.toFixed(1) + ',' + y.toFixed(1);
    }).join(' ');
    svg = '<svg class="spark" viewBox="0 0 ' + w + ' ' + h + '" preserveAspectRatio="none"><polyline fill="none" stroke="currentColor" stroke-width="1.5" points="' + pts + '"/></svg>';
  } else {
    svg = '<span class="muted">—</span>';
  }
  return svg;
}
async function api(path, options) {
  const resp = await fetch(BASE + path, Object.assign({ headers: headers() }, options || {}));
  if (!resp.ok && resp.status !== 202) throw new Error('HTTP ' + resp.status + (resp.status === 401 ? ' (bad management key?)' : ''));
  return resp.json();
}
async function load() {
  $('#error').textContent = '';
  try {
    const data = await api('/status');
    const settings = await api('/settings');
    $('#meta').textContent = '';
    $('#meta2').textContent = 'v' + data.version + ' · interval ' + data.probe_interval + ' · env key ' + (settings.vast_api_key_env ? '✓' : '✗') + ' · ' + fmtTime(data.generated_at);
    let html = '<table id="nodes"><tr><th>Node</th><th>Trạng thái</th><th>Engine / Model</th><th>tok/s</th><th>TTFT</th><th>KV cache</th><th>GPU</th><th>Giá</th><th>tok/s per $</th><th>24h</th><th>Lần đo cuối</th></tr>';
    for (const n of data.nodes) {
      const gpu = (n.gpus || []).map(g => '<div class="gpu">GPU' + g.idx + ' ' + g.name + ' ' + g.temp_c + '°C · ' + g.w + 'W · ' + g.util_pct + '%</div>').join('');
      const hist = await api('/history?node=' + encodeURIComponent(n.name));
      html += '<tr><td><b>' + n.name + '</b></td>'
        + '<td>' + statusDot(n.status) + (n.vast && n.vast.status ? '<br><span class="muted">' + n.vast.status + '</span>' : '') + '</td>'
        + '<td>' + (n.engine || '') + (n.model ? '<br><span class="muted">' + n.model + '</span>' : '') + '</td>'
        + '<td><b>' + fmtNum(n.decode_tok_s, 1) + '</b></td>'
        + '<td>' + fmtNum(n.ttft_s * 1000, 1) + ' ms</td>'
        + '<td>' + fmtNum(n.kv_cache_tokens / 1000, 0) + 'K · ' + usageBar(n.kv_usage) + '<br><span class="muted">run ' + fmtNum(n.running, 0) + ' · q ' + fmtNum(n.queue, 0) + '</span></td>'
        + '<td>' + (gpu || '<span class="muted">—</span>') + '</td>'
        + '<td>' + (n.price_h ? '$' + n.price_h.toFixed(3) + '/h' : '—') + '</td>'
        + '<td>' + (n.price_h > 0 && n.decode_tok_s > 0 ? (n.decode_tok_s / n.price_h).toFixed(0) : '—') + '</td>'
        + '<td>' + spark(null, hist.points || []) + '</td>'
        + '<td><span class="muted">' + fmtTime(n.last_seen) + '</span></td></tr>';
    }
    html += '</table>';
    if (data.nodes.length === 0) html += '<p class="muted">Chưa có node nào. Bấm ⚡ Probe now hoặc cấu hình <code>nodes</code> trong plugins.configs.</p>';
    $('#content').innerHTML = html;
  } catch (e) {
    $('#error').textContent = String(e);
  }
}
async function probeNow() {
  $('#error').textContent = '';
  try {
    await api('/probe', { method: 'POST', body: '{}' });
    $('#meta2').textContent = '⚡ probe đang chạy… sẽ tự cập nhật';
    setTimeout(load, 4000);
  } catch (e) { $('#error').textContent = String(e); }
}
async function saveKeys() {
  $('#error').textContent = '';
  const body = { vast_api_key: $('#vastkey').value.trim(), ssh_key: $('#sshkey').value.trim() };
  try {
    await api('/settings', { method: 'POST', body: JSON.stringify(body) });
    $('#keysaved').textContent = '✓ đã lưu';
    $('#vastkey').value = ''; $('#sshkey').value = '';
    setTimeout(() => { $('#keysaved').textContent = ''; }, 2500);
    load();
  } catch (e) { $('#error').textContent = String(e); }
}
$('#load').addEventListener('click', load);
$('#probe').addEventListener('click', probeNow);
$('#refresh').addEventListener('click', load);
$('#savekeys').addEventListener('click', saveKeys);
load();
setInterval(load, 15000);
</script>
</body>
</html>
`
