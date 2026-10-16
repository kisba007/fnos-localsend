"use strict";

// --- 小工具 ---------------------------------------------------------------

const $ = (sel) => document.querySelector(sel);

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function fmtBytes(n) {
  if (n === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(units.length - 1, Math.floor(Math.log2(n) / 10));
  return (n / 2 ** (10 * i)).toFixed(i === 0 ? 0 : 1) + " " + units[i];
}

function fmtTime(iso) {
  const d = new Date(iso);
  return d.toLocaleString("zh-CN", { dateStyle: "short", timeStyle: "short" });
}

function toast(msg, isError = true) {
  const t = $("#toast");
  t.textContent = msg;
  t.style.background = isError ? "#b3261e" : "#2e7d32";
  t.hidden = false;
  clearTimeout(toast._timer);
  toast._timer = setTimeout(() => (t.hidden = true), 5000);
}

async function api(method, url, body, signal) {
  const opts = { method, headers: {}, signal };
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const resp = await fetch(url, opts);
  const text = await resp.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { data = null; }
  if (!resp.ok) {
    throw new Error((data && (data.error || data.message)) || `${resp.status} ${resp.statusText}`);
  }
  return data;
}

// --- 主题：默认跟随系统，可手动覆盖 ----------------------------------------

const THEME_KEY = "localsend-nas-theme";
const THEME_ORDER = ["system", "light", "dark"];

const SVG = (inner) =>
  `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${inner}</svg>`;
const ICON_MONITOR = SVG('<rect x="2" y="3" width="20" height="14" rx="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/>');
const ICON_PHONE = SVG('<rect x="5" y="2" width="14" height="20" rx="2"/><line x1="12" y1="18" x2="12.01" y2="18"/>');
const ICON_SUN = SVG('<circle cx="12" cy="12" r="5"/><line x1="12" y1="1" x2="12" y2="3"/><line x1="12" y1="21" x2="12" y2="23"/><line x1="4.2" y1="4.2" x2="5.6" y2="5.6"/><line x1="18.4" y1="18.4" x2="19.8" y2="19.8"/><line x1="1" y1="12" x2="3" y2="12"/><line x1="21" y1="12" x2="23" y2="12"/><line x1="4.2" y1="19.8" x2="5.6" y2="18.4"/><line x1="18.4" y1="5.6" x2="19.8" y2="4.2"/>');
const ICON_MOON = SVG('<path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/>');

const THEME_ZH = { system: "跟随系统", light: "浅色", dark: "深色" };

function themeIcon(id) {
  if (id === "light") return ICON_SUN;
  if (id === "dark") return ICON_MOON;
  return window.matchMedia("(pointer: coarse)").matches ? ICON_PHONE : ICON_MONITOR;
}

function currentThemeId() {
  return localStorage.getItem(THEME_KEY) || "system";
}

function themeIsDark(id) {
  return id === "dark" || (id === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches);
}

function applyTheme(id) {
  const dark = themeIsDark(id);
  const root = document.documentElement;
  root.classList.toggle("dark", dark);
  root.style.colorScheme = dark ? "dark" : "light";
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) meta.content = dark ? "#0e1513" : "#f4fbf8";
  const btn = $("#themeToggle");
  if (btn) {
    btn.innerHTML = themeIcon(id);
    btn.title = `主题：${THEME_ZH[id] || id}（点击切换）`;
  }
}

function initTheme() {
  applyTheme(currentThemeId());
  window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
    if (currentThemeId() === "system") applyTheme("system");
  });
  $("#themeToggle").addEventListener("click", () => {
    const next = THEME_ORDER[(THEME_ORDER.indexOf(currentThemeId()) + 1) % THEME_ORDER.length];
    localStorage.setItem(THEME_KEY, next);
    applyTheme(next);
  });
}

// --- 状态 ------------------------------------------------------------------

const state = {
  shares: [],
  share: "",
  path: "",
  basket: new Map(),  // key "share:rel" -> {share, rel, name, size, isDir}
  devices: [],
  jobs: new Map(),    // jobId -> job
  events: new Map(),  // jobId -> EventSource
  settings: { acceptTimeoutSec: 30, dropboxShare: "", showNasNoise: false, receiveDir: "" },
};

// --- 标签页 ----------------------------------------------------------------

$("#tabs").addEventListener("click", (e) => {
  const btn = e.target.closest("button[data-tab]");
  if (!btn) return;
  document.querySelectorAll("#tabs button").forEach((b) => b.classList.toggle("active", b === btn));
  document.querySelectorAll("main > section").forEach((s) => (s.hidden = s.id !== "tab-" + btn.dataset.tab));
});

// --- 文件 / 待发清单 ----------------------------------------------------------

async function loadShares() {
  state.shares = await api("GET", "/api/shares");
  const sel = $("#shareSelect");
  sel.replaceChildren(...state.shares.map((s) => {
    const o = el("option", "", s.name);
    o.value = s.name;
    return o;
  }));
  const failed = [];
  for (const s of state.shares) {
    try {
      await fetchList(s.name, "");
      state.share = s.name;
      break;
    } catch (err) {
      failed.push(`${s.name} (${err.name === "AbortError" ? "超时" : err.message})`);
    }
  }
  if (failed.length) {
    toast(`共享目录不可访问：${failed.join(", ")}`);
  }
  if (!state.share) {
    sel.value = "";
    renderListError("没有可访问的共享目录");
    return;
  }
  sel.value = state.share;
  await navigate("");
}

$("#shareSelect").addEventListener("change", (e) => navigate("", e.target.value));

function joinRel(base, name) { return base ? base + "/" + name : name; }

let navSeq = 0;

function fetchList(share, path) {
  const ctl = new AbortController();
  const t = setTimeout(() => ctl.abort(), 15000);
  return api("GET", `/api/list?share=${encodeURIComponent(share)}&path=${encodeURIComponent(path)}`, undefined, ctl.signal)
    .finally(() => clearTimeout(t));
}

async function navigate(path, share) {
  const targetShare = share ?? state.share;
  const seq = ++navSeq;
  let entries;
  try {
    entries = await fetchList(targetShare, path);
  } catch (err) {
    if (seq !== navSeq) return;
    const msg = err.name === "AbortError" ? "共享目录不可访问（超时）" : err.message;
    renderListError(msg);
    toast(`无法列出 ${targetShare}:${path || "/"} — ${msg}`);
    if (share !== undefined) $("#shareSelect").value = state.share;
    return;
  }
  if (seq !== navSeq) return;
  state.share = targetShare;
  state.path = path;
  $("#shareSelect").value = targetShare;
  renderBreadcrumb();
  renderList(entries || []);
}

function renderListError(msg) {
  const tbody = $("#fileList tbody");
  tbody.replaceChildren();
  const td = el("td", "error", `⚠ ${msg}`);
  td.colSpan = 4;
  const tr = el("tr");
  tr.append(td);
  tbody.append(tr);
}

function renderBreadcrumb() {
  const bc = $("#breadcrumb");
  bc.replaceChildren();
  const root = el("a", "crumb", "/");
  root.addEventListener("click", () => navigate(""));
  bc.append(root);
  let acc = "";
  for (const part of state.path.split("/").filter(Boolean)) {
    acc = joinRel(acc, part);
    const link = el("a", "crumb", part);
    const target = acc;
    link.addEventListener("click", () => navigate(target));
    bc.append(el("span", "sep", " / "), link);
  }
}

function renderList(entries) {
  const tbody = $("#fileList tbody");
  tbody.replaceChildren();
  if (state.path) {
    const tr = el("tr", "dirrow");
    const name = el("td", "name", "↩ ..");
    name.colSpan = 4;
    name.addEventListener("click", () => navigate(state.path.split("/").slice(0, -1).join("/")));
    tr.append(el("td", "check"), name);
    tbody.append(tr);
  }
  for (const e of entries) {
    const tr = el("tr", e.isDir ? "dirrow" : "");
    const key = `${state.share}:${e.rel}`;
    const cb = el("input");
    cb.type = "checkbox";
    cb.checked = state.basket.has(key);
    cb.addEventListener("change", () => toggleBasket(key, e, cb.checked));
    const checkTd = el("td", "check");
    checkTd.append(cb);
    const nameTd = el("td", "name", (e.isDir ? "📁 " : "📄 ") + e.name);
    if (e.isDir) {
      nameTd.style.cursor = "pointer";
      nameTd.addEventListener("click", () => navigate(e.rel));
    }
    tr.append(checkTd, nameTd,
      el("td", "num", e.isDir ? "—" : fmtBytes(e.size)),
      el("td", "", fmtTime(e.modTime)));
    tbody.append(tr);
  }
}

function toggleBasket(key, entry, on) {
  if (on) {
    state.basket.set(key, { share: state.share, rel: entry.rel, name: entry.name, size: entry.size, isDir: entry.isDir });
  } else {
    state.basket.delete(key);
  }
  renderBasketBar();
}

function renderBasketBar() {
  const items = [...state.basket.values()];
  const total = items.reduce((a, i) => a + (i.isDir ? 0 : i.size), 0);
  $("#basketInfo").textContent = items.length
    ? `待发清单：${items.length} 项（${fmtBytes(total)}${items.some((i) => i.isDir) ? "+" : ""}）`
    : "待发清单为空";
  $("#chooseDevice").disabled = items.length === 0;
  $("#clearBasket").disabled = items.length === 0;
}

$("#clearBasket").addEventListener("click", () => {
  state.basket.clear();
  renderBasketBar();
  navigate(state.path);
});

$("#chooseDevice").addEventListener("click", () => {
  document.querySelector('#tabs button[data-tab="devices"]').click();
});

const ICON_X = SVG('<line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>');

function removeButton(title, onRemove) {
  const btn = el("button", "icon-btn remove");
  btn.innerHTML = ICON_X;
  btn.title = title;
  btn.addEventListener("click", async () => {
    try { await onRemove(); } catch (err) { toast(err.message); }
  });
  return btn;
}

// --- 设备 ----------------------------------------------------------------

const TYPE_ICONS = { mobile: "📱", desktop: "💻", web: "🌐", headless: "🖥️", server: "🗄️" };

async function loadDevices() {
  state.devices = await api("GET", "/api/devices");
  renderDevices();
}

function renderDevices() {
  const ul = $("#deviceList");
  ul.replaceChildren();
  if (!state.devices.length) {
    ul.append(el("li", "empty", "暂无设备 — 等待广播，或在上方按 IP 添加。"));
    return;
  }
  const basketEmpty = state.basket.size === 0;
  for (const d of state.devices) {
    const li = el("li", "card");
    const head = el("div", "card-head");
    head.append(
      el("span", "icon", TYPE_ICONS[d.deviceType] || "📟"),
      el("strong", "", d.alias),
      el("span", "muted", `${d.ip}:${d.port}`),
    );
    if (d.manual) head.append(el("span", "badge", "手动添加"));
    head.append(removeButton("从列表移除", async () => {
      await api("DELETE", `/api/devices/${d.fingerprint}`);
      await loadDevices();
    }));
    li.append(head);
    const meta = el("div", "muted small",
      `${d.deviceModel || "?"} · 最近在线 ${fmtTime(d.lastSeen)} · ${d.fingerprint.slice(0, 12)}…`);
    li.append(meta);
    const actions = el("div", "actions");
    const send = el("button", "", "发送待发清单");
    send.disabled = basketEmpty;
    send.addEventListener("click", () => sendBasket(d));
    actions.append(send);
    li.append(actions);
    ul.append(li);
  }
}

$("#addDevice").addEventListener("click", async () => {
  const input = $("#addrInput");
  try {
    await api("POST", "/api/devices", { address: input.value.trim() });
    input.value = "";
    await loadDevices();
  } catch (err) { toast(err.message); }
});
$("#addrInput").addEventListener("keydown", (e) => { if (e.key === "Enter") $("#addDevice").click(); });
$("#refreshDevices").addEventListener("click", () => loadDevices().catch((e) => toast(e.message)));

async function sendBasket(d) {
  const items = [...state.basket.values()].map(({ share, rel }) => ({ share, rel }));
  try {
    const { jobId } = await api("POST", "/api/send", { target: d.fingerprint, items });
    state.basket.clear();
    renderBasketBar();
    toast(`正在发送到 ${d.alias}…`, false);
    document.querySelector('#tabs button[data-tab="transfers"]').click();
    await refreshTransfers();
    watchJob(jobId);
  } catch (err) { toast(err.message); }
}

// --- 传输 --------------------------------------------------------------

const ACTIVE = new Set(["queued", "preparing", "awaiting-accept", "sending", "receiving"]);

const STATE_ZH = {
  queued: "排队中",
  preparing: "准备中",
  "awaiting-accept": "等待接收",
  sending: "发送中",
  receiving: "接收中",
  done: "已完成",
  failed: "失败",
  cancelled: "已取消",
  declined: "已拒绝",
};

function stateText(st) { return STATE_ZH[st] || st; }

async function refreshTransfers() {
  const jobs = await api("GET", "/api/transfers");
  state.jobs = new Map((jobs || []).map((j) => [j.id, j]));
  renderTransfers();
  for (const j of state.jobs.values()) if (ACTIVE.has(j.state)) watchJob(j.id);
  const pending = [...state.jobs.values()].find((j) => j.direction === "receive" && j.state === "awaiting-accept");
  if (pending) {
    openOffer(pending);
  } else if (offerId) {
    const cur = state.jobs.get(offerId);
    if (!cur || cur.state !== "awaiting-accept") closeOffer();
  }
}

function watchJob(id) {
  if (state.events.has(id)) return;
  const es = new EventSource(`/api/transfers/${id}/events`);
  es.onmessage = (ev) => {
    const job = JSON.parse(ev.data);
    state.jobs.set(id, job);
    renderTransfers();
    if (!ACTIVE.has(job.state)) { es.close(); state.events.delete(id); }
  };
  es.onerror = () => { es.close(); state.events.delete(id); };
  state.events.set(id, es);
}

function renderTransfers() {
  const ul = $("#transferList");
  ul.replaceChildren();
  const jobs = [...state.jobs.values()];
  const activeCount = jobs.filter((j) => ACTIVE.has(j.state)).length;
  $("#transferBadge").textContent = activeCount ? ` (${activeCount})` : "";
  if (!jobs.length) {
    ul.append(el("li", "empty", "暂无传输记录。"));
    return;
  }
  for (const j of jobs) {
    const li = el("li", "card");
    const head = el("div", "card-head");
    head.append(el("strong", "", `${j.direction === "receive" ? "←" : "→"} ${j.alias}`), el("span", `state state-${j.state}`, stateText(j.state)));
    li.append(head);

    const totalBar = el("div", "bar");
    const totalFill = el("div", "fill");
    totalFill.style.width = j.total ? `${Math.min(100, (100 * j.sent) / j.total).toFixed(1)}%` : "0%";
    totalBar.append(totalFill);
    li.append(totalBar, el("div", "muted small", `${fmtBytes(j.sent)} / ${fmtBytes(j.total)} · ${j.files.length} 个文件`));

    for (const f of j.files) {
      const row = el("div", "file-row");
      row.append(el("span", "name", f.name), el("span", "muted small", `${fmtBytes(f.sent)}/${fmtBytes(f.size)}`));
      li.append(row);
    }
    if (j.error) li.append(el("div", "error small", j.error));
    const actionsRow = el("div", "actions");
    if (ACTIVE.has(j.state)) {
      const cancel = el("button", "secondary", "取消");
      cancel.addEventListener("click", async () => {
        try { await api("POST", `/api/transfers/${j.id}/cancel`); } catch (err) { toast(err.message); }
      });
      actionsRow.append(cancel);
    } else {
      actionsRow.append(removeButton("从列表移除", async () => {
        await api("DELETE", `/api/transfers/${j.id}`);
        await refreshTransfers();
      }));
    }
    li.append(actionsRow);
    ul.append(li);
  }
}

// --- 设置菜单 ----------------------------------------------------------

async function loadSettings() {
  if (!state.shares.length) {
    try { state.shares = await api("GET", "/api/shares"); } catch { /* 弹窗缺少共享选项 */ }
  }
  state.settings = await api("GET", "/api/settings");
  $("#setTimeout").value = state.settings.acceptTimeoutSec;
  $("#setReceiveDir").value = state.settings.receiveDir || "";
  const sel = $("#setDropbox");
  sel.replaceChildren(el("option", "", "关闭（超时拒绝）"));
  sel.firstChild.value = "";
  for (const s of state.shares) {
    const o = el("option", "", s.name);
    o.value = s.name;
    sel.append(o);
  }
  let drop = state.settings.dropboxShare || "";
  if (!drop && state.settings.receiveDir && state.shares.some((s) => s.name === "inbox")) {
    drop = "inbox";
  }
  sel.value = drop;
  $("#setNoise").checked = !!state.settings.showNasNoise;
}

async function saveSettings() {
  try {
    state.settings = await api("PUT", "/api/settings", {
      acceptTimeoutSec: Math.max(5, Math.min(300, parseInt($("#setTimeout").value, 10) || 30)),
      dropboxShare: $("#setDropbox").value,
      showNasNoise: $("#setNoise").checked,
      receiveDir: ($("#setReceiveDir").value || "").trim(),
    });
  } catch (err) { toast(err.message); }
}

$("#settingsBtn").addEventListener("click", (e) => {
  e.stopPropagation();
  $("#settingsMenu").hidden = !$("#settingsMenu").hidden;
});
document.addEventListener("click", (e) => {
  if (!$("#settingsMenu").hidden && !e.target.closest("#settingsMenu") && !e.target.closest("#settingsBtn")) {
    $("#settingsMenu").hidden = true;
  }
});
$("#setTimeout").addEventListener("change", saveSettings);
$("#setDropbox").addEventListener("change", saveSettings);
$("#setReceiveDir").addEventListener("change", saveSettings);
$("#setNoise").addEventListener("change", async () => {
  await saveSettings();
  navigate(state.path);
});

// --- 接收弹窗 ----------------------------------------------------------

let offerId = null;
let offerTimer = null;

function openOffer(job) {
  if (offerId === job.id) return;
  offerId = job.id;
  $("#rmTitle").textContent = `← ${job.alias} 想发送文件`;
  const files = $("#rmFiles");
  files.replaceChildren();
  for (const f of job.files) {
    const row = el("div", "row");
    row.append(el("span", "name", f.name), el("span", "muted small", fmtBytes(f.size)));
    files.append(row);
  }
  files.append(el("div", "muted small", `${job.files.length} 个文件，共 ${fmtBytes(job.total)}`));
  const dest = $("#rmDest");
  dest.replaceChildren(...state.shares.map((s) => {
    const o = el("option", "", s.name);
    o.value = s.name;
    return o;
  }));
  if (state.settings.receiveDir && state.shares.some((s) => s.name === "inbox")) {
    dest.value = "inbox";
  }

  const timeout = state.settings.acceptTimeoutSec || 30;
  const drop = state.settings.dropboxShare;
  const deadline = Date.now() + timeout * 1000;
  const bar = $("#rmCountdown");
  const caption = $("#rmCaption");
  clearInterval(offerTimer);
  offerTimer = setInterval(() => {
    const left = Math.max(0, Math.ceil((deadline - Date.now()) / 1000));
    bar.style.width = `${(100 * left) / timeout}%`;
    caption.textContent = drop
      ? `还剩 ${left} 秒 — 将保存到 "${drop}"`
      : `还剩 ${left} 秒 — 将拒绝请求`;
    if (left <= 0) closeOffer();
  }, 250);
  $("#receiveModal").hidden = false;
}

function closeOffer() {
  clearInterval(offerTimer);
  offerTimer = null;
  offerId = null;
  $("#receiveModal").hidden = true;
}

async function decideOffer(accept) {
  const id = offerId;
  if (!id) return;
  try {
    await api("POST", `/api/receive/${id}/decision`, { accept, share: $("#rmDest").value });
  } catch (err) { toast(err.message); }
  closeOffer();
  await refreshTransfers();
}

$("#rmAccept").addEventListener("click", () => decideOffer(true));
$("#rmDecline").addEventListener("click", () => decideOffer(false));

function startGlobalEvents() {
  const es = new EventSource("/api/events");
  es.onmessage = (ev) => {
    const job = JSON.parse(ev.data);
    if (job.direction === "receive" && job.state === "awaiting-accept") openOffer(job);
  };
  es.onerror = () => {}; // EventSource 自动重连
}

// --- 启动 ------------------------------------------------------------------

(async function boot() {
  initTheme();
  renderBasketBar();
  try { await loadShares(); } catch (err) { toast(err.message); }
  const results = await Promise.allSettled([loadDevices(), refreshTransfers(), loadSettings()]);
  for (const r of results) if (r.status === "rejected") toast(r.reason.message);
  startGlobalEvents();
  setInterval(() => { if (!$("#tab-devices").hidden) loadDevices().catch(() => {}); }, 5000);
  setInterval(() => { if (!$("#tab-transfers").hidden) refreshTransfers().catch(() => {}); }, 3000);
})();
