"use strict";

// ============================ 基础工具 ============================

const $ = (sel, root = document) => root.querySelector(sel);

// h("div.cls#id", {attrs}, ...children)
function h(tag, attrs, ...children) {
  const m = tag.match(/^([a-z0-9]+)?((?:[.#][\w-]+)*)$/i);
  const el = document.createElement(m[1] || "div");
  for (const part of (m[2] || "").match(/[.#][\w-]+/g) || []) {
    if (part[0] === ".") el.classList.add(part.slice(1)); else el.id = part.slice(1);
  }
  // 第二个参数不是属性对象时当作子节点（注意数字 0 也是合法的子节点）
  if (attrs != null && (typeof attrs !== "object" || attrs instanceof Node || Array.isArray(attrs))) {
    children.unshift(attrs);
    attrs = null;
  }
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k.startsWith("on")) el.addEventListener(k.slice(2).toLowerCase(), v);
    else if (k === "class") el.className += " " + v;
    else if (k === "style" && typeof v === "object") Object.assign(el.style, v);
    else if (k === "html") el.innerHTML = v;
    else if (k in el && typeof v !== "string") el[k] = v;
    else el.setAttribute(k, v === true ? "" : v);
  }
  append(el, children);
  return el;
}
function append(el, children) {
  for (const c of children.flat(Infinity)) {
    if (c == null || c === false) continue;
    el.appendChild(c instanceof Node ? c : document.createTextNode(String(c)));
  }
}

const ICONS = {
  grid: '<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
  list: '<path d="M8 6h13M8 12h13M8 18h13M3.5 6h.01M3.5 12h.01M3.5 18h.01"/>',
  folder: '<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>',
  tool: '<path d="M14.7 6.3a4 4 0 0 0-5.4 5.2L3 17.8V21h3.2l6.3-6.3a4 4 0 0 0 5.2-5.4l-2.6 2.6-2.3-.6-.6-2.3z"/>',
  gear: '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/>',
  search: '<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>',
  refresh: '<path d="M21 12a9 9 0 1 1-2.6-6.4L21 8"/><path d="M21 3v5h-5"/>',
  download: '<path d="M12 3v12m0 0-4-4m4 4 4-4M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2"/>',
  upload: '<path d="M12 21V9m0 0-4 4m4-4 4 4M4 7V5a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v2"/>',
  x: '<path d="M18 6 6 18M6 6l12 12"/>',
  edit: '<path d="M4 20h4L19 9a2.8 2.8 0 0 0-4-4L4 16z"/><path d="m13.5 6.5 4 4"/>',
  check: '<path d="m5 12 5 5L20 7"/>',
  alert: '<path d="M12 9v4m0 4h.01M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z"/>',
  info: '<circle cx="12" cy="12" r="9"/><path d="M12 16v-4m0-4h.01"/>',
  undo: '<path d="M9 14 4 9l5-5"/><path d="M4 9h11a5 5 0 0 1 0 10h-3"/>',
  merge: '<path d="M6 3v6a6 6 0 0 0 6 6h6M18 21v-6M6 21V3"/><circle cx="6" cy="3" r="0.5"/>',
  image: '<rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="9" cy="9" r="2"/><path d="m21 15-5-5L5 21"/>',
  db: '<ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v14c0 1.7 3.6 3 8 3s8-1.3 8-3V5M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3"/>',
  key: '<circle cx="7.5" cy="15.5" r="4.5"/><path d="m10.7 12.3 9.3-9.3M16 7l3 3M18 5l2 2"/>',
  power: '<path d="M12 2v10M18.4 6.6a9 9 0 1 1-12.8 0"/>',
  gamepad: '<rect x="2" y="6" width="20" height="12" rx="4"/><path d="M6 12h4M8 10v4M15 11h.01M18 13h.01"/>',
  link: '<path d="M10 14a5 5 0 0 0 7 0l3-3a5 5 0 0 0-7-7l-1 1M14 10a5 5 0 0 0-7 0l-3 3a5 5 0 0 0 7 7l1-1"/>',
  chevron: '<path d="m9 18 6-6-6-6"/>',
  file: '<path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><path d="M14 3v5h5"/>',
  sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>',
};
function icon(name, cls = "") {
  const s = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  s.setAttribute("viewBox", "0 0 24 24");
  s.setAttribute("class", "i " + cls);
  s.innerHTML = ICONS[name] || "";
  return s;
}

function fmtSize(b) {
  if (b == null) return "";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (b >= 1024 && i < u.length - 1) { b /= 1024; i++; }
  return (i >= 3 ? b.toFixed(2) : i === 2 ? b.toFixed(1) : Math.round(b)) + " " + u[i];
}
function fmtVer(v) {
  if (v == null || v < 0) return "未知";
  if (v === 0) return "v0";
  return v % 65536 === 0 ? `v${v}（补丁 #${v / 65536}）` : `v${v}`;
}
function fmtVerShort(v) {
  if (v == null || v < 0) return "?";
  return v % 65536 === 0 ? `#${v / 65536}` : `v${v}`;
}
function fmtTime(s) {
  if (!s || s.startsWith("0001")) return "";
  const d = new Date(s);
  return d.toLocaleString("zh-CN", { hour12: false });
}
function initial(name) { return (name || "?").trim().charAt(0).toUpperCase(); }
function debounce(fn, ms) { let t; return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); }; }
function store(k, v) { try { if (v === undefined) return localStorage.getItem("sm." + k); localStorage.setItem("sm." + k, v); } catch { return null; } }

const SOURCE_LABEL = { cnmt: "CNMT 解密", nca: "NCA 比对", ticket: "票据", filename: "文件名", manual: "手动指定", "": "未识别" };
const SOURCE_TONE = { cnmt: "ok", nca: "ok", ticket: "info", filename: "warn", manual: "accent", "": "bad" };
const STATE_TONE = { latest: "ok", outdated: "warn", unknown: "", norecord: "", unidentified: "bad" };
const MODE_LABEL = { rename: "文件夹改名", move: "移动文件", create: "新建文件夹", inplace: "仅改文件名" };

// ============================ API ============================

async function api(method, path, body, raw, onResponse) {
  if (method !== "GET" && !path.endsWith("/plan") && path !== "/api/jobs/cancel") clearPlan();
  const opt = { method, headers: { "X-Requested-With": "switchmgr" } };
  if (body !== undefined) {
    if (raw) { opt.body = body; opt.headers["Content-Type"] = "text/csv"; }
    else { opt.body = JSON.stringify(body); opt.headers["Content-Type"] = "application/json"; }
  }
  const res = await fetch(path, opt);
  const text = await res.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { data = text; }
  if (!res.ok) throw new Error((data && data.error) || text || res.statusText);
  if (onResponse) onResponse(res);
  return data;
}

function toast(msg, kind = "") {
  const el = h("div.toast", { class: kind }, kind === "err" ? icon("alert") : kind === "ok" ? icon("check") : null, h("span", msg));
  $("#toasts").appendChild(el);
  setTimeout(() => el.remove(), kind === "err" ? 6000 : 3200);
}
async function guard(fn) {
  try { return await fn(); } catch (e) { toast(e.message, "err"); }
}

// ============================ 状态 ============================

const S = {
  state: null,
  games: null,
  view: "library",
  filter: store("filter") || "all",
  q: "",
  layout: store("layout") || "grid",
  sort: store("sort") || "name",
  plan: null,
  planToken: "",
  planEpoch: 0,
  planSel: new Set(),
  planOpen: new Set(),
  jobSince: 0,
  jobId: 0,
  jobLogs: [],
  jobOpen: false,
  onJobDone: [],
};

function clearPlan() {
  S.plan = null;
  S.planToken = "";
  S.planSel.clear();
  S.planOpen.clear();
  S.planEpoch++;
}
function normalizeMoves(item) {
  return { ...item, moves: Array.isArray(item.moves) ? item.moves : [] };
}

async function loadState() {
  const state = await api("GET", "/api/state");
  if (S.state && state.planGeneration !== S.state.planGeneration) clearPlan();
  S.state = state;
  renderSide();
  if (S.state.job && S.state.job.state === "running") pollJob();
  return S.state;
}
async function loadGames() {
  S.games = await api("GET", "/api/games");
  return S.games;
}

// ============================ 后台任务 ============================

let pollTimer = null;
async function startJob(method, path, body, onDone) {
  const job = await api(method, path, body);
  if (onDone) S.onJobDone.push(onDone);
  S.jobId = job.id; S.jobSince = 0; S.jobLogs = [];
  pollJob();
  return job;
}
function pollJob() {
  clearTimeout(pollTimer);
  const tick = async () => {
    let data;
    try { data = await api("GET", "/api/jobs/current?since=" + S.jobSince); } catch { pollTimer = setTimeout(tick, 1500); return; }
    const job = data.job;
    if (!job) { renderJob(null); return; }
    if (job.id !== S.jobId) { S.jobId = job.id; S.jobLogs = []; S.jobSince = 0; }
    if (data.logs && data.logs.length) { S.jobLogs.push(...data.logs); S.jobSince += data.logs.length; }
    renderJob(job);
    if (job.state === "running") { pollTimer = setTimeout(tick, 600); return; }
    // 结束
    if (job.state === "done") toast(`${job.title}完成${job.summary ? "：" + job.summary : ""}`, "ok");
    else if (job.state === "failed") toast(`${job.title}失败：${job.error}`, "err");
    else toast(`${job.title}已取消`);
    const cbs = S.onJobDone.splice(0);
    await guard(loadState);
    if (["scan", "organize", "undo", "merge", "xci2nsp", "fetch-db", "redundant"].includes(job.kind)) await guard(loadGames);
    cbs.forEach(cb => cb(job));
    render();
    setTimeout(() => { if (S.jobId === job.id && job.state !== "running" && !S.jobOpen) renderJob(null); }, 6000);
  };
  tick();
}

function renderJob(job) {
  const bar = $("#jobbar");
  if (!job) { bar.classList.add("hidden"); return; }
  bar.classList.remove("hidden");
  const running = job.state === "running";
  const pct = job.progress >= 0 ? Math.round(job.progress * 100) : null;
  bar.replaceChildren(
    h("div.jb-top",
      running ? h("div.spinner") : icon(job.state === "done" ? "check" : "alert"),
      h("div.jb-title", job.title),
      h("div.jb-stage.grow", running ? (job.stage || "处理中…") : (job.summary || job.error || "")),
      pct != null && running ? h("span.mono.faint", pct + "%") : null,
      h("button.btn.sm.ghost", { onclick: () => { S.jobOpen = !S.jobOpen; renderJob(job); } }, S.jobOpen ? "收起日志" : "日志"),
      running ? h("button.btn.sm", { onclick: () => guard(() => api("POST", "/api/jobs/cancel")) }, "取消")
        : h("button.btn.sm.icon.ghost", { title: "关闭", onclick: () => { S.jobOpen = false; renderJob(null); } }, icon("x"))),
    h("div.bar", { class: pct == null && running ? "indet" : "" }, h("i", { style: { width: (running ? pct ?? 0 : 100) + "%" } })),
    S.jobOpen ? h("pre", S.jobLogs.join("\n") || "（暂无日志）") : null,
  );
  const pre = $("pre", bar);
  if (pre) pre.scrollTop = pre.scrollHeight;
}
function jobRunning() { return S.state && S.state.job && S.state.job.state === "running"; }

// ============================ 侧边栏 ============================

function renderSide() {
  const st = S.state;
  const db = st.db, keys = st.keys;
  const dbTone = db.loaded ? (db.warnings && db.warnings.length ? "warn" : "ok") : db.error ? "bad" : "warn";
  const keyTone = keys.loaded ? "ok" : keys.error ? "bad" : "";
  $("#side-status").replaceChildren(
    h("div.row", h("span.dot", { class: dbTone }), db.loaded ? `数据库 · ${db.stats.namesHK + db.stats.namesCN} 条中文名` : db.error ? "数据库未加载" : "数据库加载中…"),
    h("div.row", h("span.dot", { class: keyTone }), keys.loaded ? "密钥已加载（精确识别）" : keys.error ? "密钥读取失败" : "未配置密钥（可选）"),
    h("div.row", h("span.dot", { class: st.library.scannedAt && !st.library.scannedAt.startsWith("0001") ? "ok" : "" }),
      st.library.games ? `${st.library.games} 个游戏 · ${st.library.files} 个文件` : "尚未扫描"),
    h("div.foot",
      h("span.faint.mono.grow", st.version),
      h("button.btn.sm.icon.ghost", { title: "切换深浅色", onclick: toggleTheme }, icon("sun")),
      st.desktop ? h("button.btn.sm.icon.ghost", { title: "退出程序", onclick: quitApp }, icon("power")) : null),
  );
}
function toggleTheme() {
  const cur = document.documentElement.dataset.theme;
  const dark = cur ? cur === "dark" : matchMedia("(prefers-color-scheme: dark)").matches;
  document.documentElement.dataset.theme = dark ? "light" : "dark";
  store("theme", document.documentElement.dataset.theme);
}
async function quitApp() {
  if (!(await confirmDialog("退出程序", "关闭后台服务？浏览器页面将无法继续使用。", "退出"))) return;
  await guard(() => api("POST", "/api/shutdown"));
  document.body.replaceChildren(h("div.empty", icon("power"), h("h3", "已退出"), h("p", "可以关闭此页面了。")));
}

// ============================ 路由 ============================

function route() {
  const v = (location.hash.match(/^#\/(\w+)/) || [])[1] || "library";
  S.view = ["library", "organize", "tools", "settings"].includes(v) ? v : "library";
  document.querySelectorAll("#nav a").forEach(a => a.classList.toggle("active", a.dataset.view === S.view));
  render();
}
function render() {
  const main = $("#main");
  const views = { library: viewLibrary, organize: viewOrganize, tools: viewTools, settings: viewSettings };
  main.replaceChildren(views[S.view]());
}

// ============================ 游戏库 ============================

function gameCounts(games) {
  const c = { all: games.length, outdated: 0, dlc: 0, unidentified: 0, latest: 0, unknown: 0, size: 0, noapp: 0, redundant: 0, redundantSize: 0 };
  for (const g of games) {
    c.size += g.totalSize;
    if (g.tid && !g.hasApp) c.noapp++;
    for (const f of g.files) if (f.redundant) { c.redundant++; c.redundantSize += f.size; }
    if (g.state === "outdated") c.outdated++;
    if (g.state === "latest") c.latest++;
    if (g.state === "unidentified") c.unidentified++;
    if (g.state === "unknown" || g.state === "norecord") c.unknown++;
    if (g.dlcTotal > g.dlcOwned) c.dlc++;
  }
  return c;
}
function filterGames(games) {
  const q = S.q.trim().toLowerCase();
  let out = games.filter(g => {
    switch (S.filter) {
      case "outdated": if (g.state !== "outdated") return false; break;
      case "dlc": if (!(g.dlcTotal > g.dlcOwned)) return false; break;
      case "unidentified": if (g.state !== "unidentified") return false; break;
      case "latest": if (g.state !== "latest") return false; break;
      case "unknown": if (g.state !== "unknown" && g.state !== "norecord") return false; break;
      case "noapp": if (!g.tid || g.hasApp) return false; break;
      case "redundant": if (!g.redundant) return false; break;
    }
    if (!q) return true;
    return g.name.toLowerCase().includes(q) || (g.tid || "").toLowerCase().includes(q) ||
      g.files.some(f => f.name.toLowerCase().includes(q)) || (g.publisher || "").toLowerCase().includes(q);
  });
  const by = {
    name: (a, b) => a.name.localeCompare(b.name, "zh-CN"),
    size: (a, b) => b.totalSize - a.totalSize,
    behind: (a, b) => b.behind - a.behind || a.name.localeCompare(b.name, "zh-CN"),
    release: (a, b) => (b.release || "").localeCompare(a.release || ""),
    dlc: (a, b) => (b.dlcTotal - b.dlcOwned) - (a.dlcTotal - a.dlcOwned),
  }[S.sort];
  return out.sort(by);
}

function stateBadge(g, small) {
  let text = g.stateLabel;
  if (g.state === "outdated") text = small ? `落后 ${g.behind} 版` : `可更新 · 落后 ${g.behind} 个版本`;
  return h("span.badge", { class: STATE_TONE[g.state] || "" }, text);
}
function dlcBadge(g) {
  if (!g.dlcTotal && !(g.dlcOwned > 0)) return null;
  const full = g.dlcTotal && g.dlcOwned >= g.dlcTotal;
  return h("span.badge", { class: full ? "ok" : "outline", title: g.dlcApprox ? "按文件名标注估算" : "" },
    `DLC ${g.dlcOwned}${g.dlcApprox ? "≈" : ""}/${g.dlcTotal || "?"}`);
}
function cover(url, name, cls = "cover") {
  const box = h("div", { class: cls });
  const ph = h("div.ph", initial(name));
  box.appendChild(ph);
  if (url) {
    const img = h("img", { src: url, alt: "", loading: "lazy", referrerpolicy: "no-referrer" });
    img.onload = () => ph.remove();
    img.onerror = () => img.remove();
    box.prepend(img);
  }
  return box;
}

function viewLibrary() {
  const st = S.state;
  const wrap = h("div");
  const noRoot = !st.config.libraryRoot;
  wrap.appendChild(h("div.page-head",
    h("div", h("h1", "游戏库"), h("p", st.library.root ? h("span.mono", st.library.root) : "扫描本地 Switch 游戏文件，比对版本与 DLC")),
    h("div.head-actions",
      exportMenu(),
      h("button.btn", { onclick: importNames, title: "导入用 Excel 编辑过的中文名表" }, icon("upload"), "导入中文名"),
      h("button.btn.primary", { onclick: scan, disabled: noRoot }, icon("refresh"), "扫描"))));

  if (noRoot) {
    wrap.appendChild(h("div.panel", h("div.empty", icon("folder"), h("h3", "先告诉我游戏在哪"),
      h("p", "选择存放 NSP / XCI / NSZ 的目录。可以是本机目录，也可以是挂载的 NAS 共享（如 \\\\NAS\\模拟器\\Switch 或 /mnt/user/模拟器/Switch）。"),
      h("button.btn.primary", { onclick: () => pickPath("游戏库目录", "", false, async p => { await saveConfig({ libraryRoot: p }); if (!jobRunning()) scan(); }) }, icon("folder"), "选择游戏库目录"))));
    return wrap;
  }
  if (!S.games || (!S.games.length && !st.library.scannedAt?.startsWith("2"))) {
    if (jobRunning() && st.job.kind === "scan") {
      wrap.appendChild(h("div.panel", h("div.empty", h("div.spinner.big"), h("h3", "正在载入游戏库…"),
        h("p", "读取目录与扫描缓存。只有新增或改动过的文件需要重新读取文件表。"))));
      return wrap;
    }
    wrap.appendChild(h("div.panel", h("div.empty", icon("gamepad"), h("h3", "还没有扫描"),
      h("p", "扫描只读取文件表，不修改任何文件。有缓存后，重新扫描几乎是瞬间完成。"),
      h("button.btn.primary", { onclick: scan }, icon("refresh"), "开始扫描"))));
    return wrap;
  }

  const games = S.games;
  const c = gameCounts(games);
  const stat = (key, label, value, tone, extra) => h("button.stat", { class: S.filter === key ? "on" : "", onclick: () => { S.filter = S.filter === key && key !== "all" ? "all" : key; store("filter", S.filter); render(); } },
    h("div.k", tone ? h("span.dot", { class: tone }) : null, label), h("div.v", value, extra ? h("small", extra) : null));
  wrap.appendChild(h("div.stats",
    stat("all", "全部游戏", c.all, "", fmtSize(c.size)),
    stat("outdated", "可更新", c.outdated, "warn"),
    stat("dlc", "缺 DLC", c.dlc, "info"),
    stat("latest", "已最新", c.latest, "ok"),
    stat("unidentified", "未识别", c.unidentified, c.unidentified ? "bad" : ""),
    c.noapp || S.filter === "noapp" ? stat("noapp", "缺本体", c.noapp, "warn") : null,
    c.redundant || S.filter === "redundant" ? stat("redundant", "冗余文件", c.redundant, "", fmtSize(c.redundantSize)) : null));

  const list = filterGames(games);
  const searchInput = h("input.input", { placeholder: "搜索游戏名、TitleID、文件名…", value: S.q });
  const results = h("div");
  searchInput.addEventListener("input", debounce(() => { S.q = searchInput.value; results.replaceChildren(renderGameList(filterGames(games))); }, 120));
  wrap.appendChild(h("div.toolbar",
    h("div.search", icon("search"), searchInput),
    h("select.select", { onchange: e => { S.sort = e.target.value; store("sort", S.sort); render(); } },
      [["name", "按名称"], ["behind", "按落后版本"], ["dlc", "按缺 DLC"], ["size", "按大小"], ["release", "按发售日"]].map(([v, t]) => h("option", { value: v, selected: S.sort === v }, t))),
    h("div.grow"),
    S.filter !== "all" ? h("button.btn.sm.ghost", { onclick: () => { S.filter = "all"; store("filter", "all"); render(); } }, icon("x"), "清除筛选") : null,
    h("div.seg",
      h("button", { class: S.layout === "grid" ? "on" : "", title: "封面", onclick: () => { S.layout = "grid"; store("layout", "grid"); render(); } }, icon("grid")),
      h("button", { class: S.layout === "list" ? "on" : "", title: "列表", onclick: () => { S.layout = "list"; store("layout", "list"); render(); } }, icon("list")))));
  results.appendChild(renderGameList(list));
  wrap.appendChild(results);
  return wrap;
}

function renderGameList(list) {
  if (!list.length) return h("div.panel", h("div.empty", icon("search"), h("h3", "没有符合条件的游戏")));
  if (S.layout === "grid") {
    return h("div.grid", list.map(g => h("div.card", { onclick: () => openDetail(g.key) },
      (() => {
        const cv = cover(g.icon, g.name);
        cv.appendChild(h("div.corner", stateBadge(g, true), g.tid && !g.hasApp ? h("span.badge.warn", "缺本体") : null,
          g.supportsZh ? h("span.badge.info", "中文") : null));
        return cv;
      })(),
      h("div.card-body",
        h("div.card-title", { title: g.name }, g.name),
        g.dlcTotal ? h("div.meter", { class: g.dlcOwned < g.dlcTotal ? "warn" : "", title: `DLC ${g.dlcOwned}/${g.dlcTotal}` },
          h("i", { style: { width: Math.min(100, 100 * g.dlcOwned / g.dlcTotal) + "%" } })) : null,
        h("div.card-meta",
          h("span.mono", g.tid || "未识别"),
          h("span", g.dlcTotal ? `DLC ${g.dlcOwned}/${g.dlcTotal}` : fmtSize(g.totalSize)))))));
  }
  return h("div.panel.table-wrap", h("table.t",
    h("thead", h("tr", ["游戏", "状态", "本地版本", "最新", "DLC", "文件", "大小"].map(t => h("th", t)))),
    h("tbody", list.map(g => h("tr.click", { onclick: () => openDetail(g.key) },
      h("td", h("div.tcell-game", cover(g.icon, g.name, "thumb"),
        h("div", h("div", { style: { fontWeight: 600 } }, g.name), h("div.mono.faint", g.tid || g.files[0].rel)))),
      h("td", stateBadge(g, true)),
      h("td.num.mono", fmtVerShort(g.localVersion)),
      h("td.num.mono", g.latest >= 0 ? fmtVerShort(g.latest) : "—"),
      h("td", dlcBadge(g) || h("span.faint", "—")),
      h("td.num", g.files.length),
      h("td.num", fmtSize(g.totalSize)))))));
}

function exportMenu() {
  const sel = h("select.select", {
    onchange: e => { if (e.target.value) location.href = "/api/export/" + e.target.value; e.target.value = ""; },
    title: "导出为 CSV（UTF-8 BOM，Excel 直接打开）",
  },
    h("option", { value: "" }, "导出 CSV…"),
    h("option", { value: "todo.csv" }, "待补清单（可更新 / 缺 DLC）"),
    h("option", { value: "library.csv" }, "完整游戏库清单"),
    h("option", { value: "names.csv" }, "中文名表（用 Excel 批量改名）"));
  return sel;
}

async function scan() {
  await guard(() => startJob("POST", "/api/scan"));
}

function importNames() {
  const inp = h("input", { type: "file", accept: ".csv,text/csv" });
  inp.onchange = async () => {
    const f = inp.files[0];
    if (!f) return;
    const text = await f.text();
    const r = await guard(() => api("POST", "/api/import/names", text, true));
    if (r) { toast(`导入完成：新增/更新 ${r.changed} 条（共 ${r.total} 条自定义名）`, "ok"); await loadGames(); render(); }
  };
  inp.click();
}

// ============================ 详情抽屉 ============================

let detailTab = "overview";
async function openDetail(key) {
  const d = await guard(() => api("GET", "/api/games/" + encodeURIComponent(key)));
  if (!d) return;
  const root = $("#drawer-root");
  const close = () => { root.replaceChildren(); document.removeEventListener("keydown", esc); };
  const esc = e => { if (e.key === "Escape" && !$("#modal-root").children.length) close(); };
  document.addEventListener("keydown", esc);
  const g = d.game, info = d.info || {}, patches = d.patches || [], dlcs = d.dlcs || [];
  const body = h("div.drawer-body");
  const tabs = [
    ["overview", "概览"], ["files", "文件", g.files.length],
    ...(g.tid ? [["patches", "补丁", patches.length], ["dlc", "DLC", dlcs.length ? `${g.dlcOwned}/${dlcs.length}` : 0]] : []),
  ];
  if (!tabs.some(t => t[0] === detailTab)) detailTab = "overview";
  const tabBar = h("div.tabs");
  const paint = () => {
    tabBar.replaceChildren(...tabs.map(([k, t, n]) => h("button", { class: detailTab === k ? "on" : "", onclick: () => { detailTab = k; paint(); } }, t, n != null ? h("span.n", n) : null)));
    body.replaceChildren(({ overview: tabOverview, files: tabFiles, patches: tabPatches, dlc: tabDLC })[detailTab](d, { close, reopen: () => { close(); openDetail(key); } }));
    body.scrollTop = 0;
  };
  const hero = h("div.drawer-hero",
    info.banner ? h("div.bg", { style: { backgroundImage: `url("${info.banner}")` } }) : null,
    h("button.btn.icon.ghost.drawer-close", { onclick: close, title: "关闭 (Esc)" }, icon("x")),
    h("div.inner",
      cover(g.icon, g.name, "hero-cover"),
      h("div.grow",
        h("h2", g.name, g.tid ? h("button.btn.sm.icon.ghost", { title: "编辑中文名", onclick: () => editName(g, info, () => { close(); openDetail(key); loadGames().then(render); }) }, icon("edit")) : null),
        h("div.sub",
          g.tid ? h("span.mono", g.tid) : h("span", "TitleID 未识别"),
          info.publisher ? h("span", info.publisher) : null,
          info.release ? h("span", info.release) : null,
          g.nameSource === "custom" ? h("span", "（自定义名称）") : null),
        h("div.badges", stateBadge(g), dlcBadge(g), g.tid && !g.hasApp ? h("span.badge.warn", { title: "只有更新或 DLC 文件，没有游戏本体" }, "缺本体") : null,
          g.redundant ? h("span.badge.warn", `${g.redundant} 个冗余文件`) : null,
          g.supportsZh ? h("span.badge.info", "支持中文") : null,
          h("span.badge.outline", `本地 ${fmtVer(g.localVersion)}`),
          g.latest >= 0 ? h("span.badge.outline", `最新 ${fmtVer(g.latest)}${g.latestDate ? " · " + g.latestDate : ""}`) : null))));
  root.replaceChildren(h("div.overlay", { onclick: close }), h("div.drawer", hero, tabBar, body));
  paint();
}

function tabOverview(d, ctx) {
  const g = d.game, info = d.info || {};
  const box = h("div.stack");
  if (g.state === "unidentified") {
    box.appendChild(h("div.callout.warn", icon("alert"), h("div",
      h("b", "识别不出这个文件属于哪个游戏。"),
      h("div", "常见于从 XCI 转换来的 NSP（没有票据），且 NCA 与数据库对不上。配置 prod.keys 后可直接解密识别；也可以手动指定。"),
      h("div", { style: { marginTop: "8px" } }, h("button.btn.sm", { onclick: () => manualMatch(g.files[0], ctx.close) }, "手动匹配游戏")))));
  }
  if (info.intro) box.appendChild(h("div", { style: { fontSize: "15px", fontWeight: 600 } }, info.intro));
  const kv = h("dl.kv");
  const add = (k, v) => { if (v) kv.append(h("dt", k), h("dd", v)); };
  add("库中名称", info.libName && info.libName !== g.name ? info.libName : null);
  add("港区原名", info.nameHK && info.nameHK !== info.libName ? info.nameHK : null);
  add("类型", info.category);
  add("开发商", info.developer);
  add("游玩人数", info.players ? info.players + " 人" : null);
  add("官方体积", info.size ? fmtSize(info.size) : null);
  add("本地占用", fmtSize(g.totalSize) + ` · ${g.files.length} 个文件`);
  add("语言", info.languages && info.languages.length ? [...new Set(info.languages)].join(" / ") : null);
  box.appendChild(kv);
  if (info.desc) box.appendChild(h("div.prose", info.desc));
  if (info.screenshots && info.screenshots.length) {
    box.appendChild(h("div.shots", info.screenshots.slice(0, 6).map(u => h("a", { href: u, target: "_blank", rel: "noreferrer", title: "查看大图" },
      h("img", { src: u, loading: "lazy", referrerpolicy: "no-referrer", alt: "", onerror: e => e.target.parentNode.remove() })))));
  }
  if (!info.desc && g.state !== "unidentified") box.appendChild(h("p.faint", "数据库中没有该游戏的简介。"));
  return box;
}

function tabFiles(d, ctx) {
  const g = d.game;
  return h("div.stack", g.files.map(f => h("div.file-row",
    h("div.name", f.name),
    h("div.meta",
      h("span.badge", { class: SOURCE_TONE[f.source] || "" }, SOURCE_LABEL[f.source] ?? f.source),
      f.redundant ? h("span.badge.warn", { title: "可在「工具 → 清理冗余文件」里移走" }, "冗余 · " + f.redundant) : null,
      h("span", f.kind), h("span", fmtSize(f.size)), f.version >= 0 ? h("span", fmtVer(f.version)) : null,
      h("span.grow"),
      h("button.btn.sm.ghost", { onclick: () => manualMatch(f, ctx.close) }, f.source === "manual" ? "修改指定" : "手动指定")),
    h("div.mono.faint", f.rel),
    f.contents && f.contents.length > 1 ? h("div.contents", f.contents.slice(0, 40).map(c =>
      h("span.badge.outline.mono", { title: fmtVer(c.ver) }, `${c.kind} ${c.tid}${c.ver > 0 ? " " + fmtVerShort(c.ver) : ""}`)),
      f.contents.length > 40 ? h("span.faint", `…共 ${f.contents.length} 项`) : null) : null,
    f.error ? h("div", { style: { color: "var(--bad)", fontSize: "12.5px" } }, "读取失败：" + f.error) : null)));
}

function tabPatches(d) {
  const ps = d.patches || [];
  if (!ps.length) return h("div.empty", h("p", "库中没有该游戏的更新记录（可能从未发布过更新）。"));
  return h("div.panel.table-wrap", h("table.t",
    h("thead", h("tr", h("th", "版本"), h("th", "补丁序号"), h("th", "发布日期"), h("th", "本地"))),
    h("tbody", ps.map(p => h("tr",
      h("td.mono", p.version, p.isLatest ? h("span.badge.accent", { style: { marginLeft: "8px" } }, "最新") : null),
      h("td.mono", p.version % 65536 === 0 ? "#" + p.version / 65536 : "—"),
      h("td", p.date),
      h("td", p.included ? h("span.badge.ok", icon("check"), "已包含") : h("span.faint", "未包含")))))));
}

function tabDLC(d) {
  const all = d.dlcs || [];
  if (!all.length) return h("div.empty", h("p", "库中没有该游戏的 DLC 记录。"));
  let mode = "all";
  const list = h("div.panel");
  const seg = h("div.seg");
  const nOwn = all.filter(x => x.owned).length;
  const paint = () => {
    seg.replaceChildren(...[["all", "全部", all.length], ["missing", "未拥有", all.length - nOwn], ["owned", "已拥有", nOwn]].map(([k, t, n]) =>
      h("button", { class: mode === k ? "on" : "", onclick: () => { mode = k; paint(); } }, t, h("span.n", n))));
    const items = all.filter(x => mode === "all" || (mode === "owned") === x.owned);
    list.replaceChildren(...items.map(x => {
      const desc = h("div.desc.hidden", [x.intro, x.desc].filter(Boolean).join("\n\n") || "（数据库中没有该 DLC 的简介）");
      return h("div.dlc-item", { onclick: () => desc.classList.toggle("hidden") },
        h("div.owned-mark", { class: x.owned ? "yes" : "no", title: x.owned ? "已拥有" : "未拥有" }, x.owned ? icon("check") : null),
        h("div.ico", x.icon ? h("img", { src: x.icon, loading: "lazy", referrerpolicy: "no-referrer", alt: "" }) : null),
        h("div.grow",
          h("div", { style: { fontWeight: 500 } }, x.name || h("span.faint", "（库中无名称）")),
          h("div.faint", h("span.mono", x.tid), x.date ? "  ·  " + x.date : ""),
          desc));
    }));
    if (!items.length) list.replaceChildren(h("div.empty", h("p", "没有条目")));
  };
  paint();
  return h("div.stack",
    d.game.dlcApprox ? h("div.callout", icon("info"), h("div", "该游戏的 DLC 拥有数来自文件名标注（如「+6DLC」），无法精确到具体是哪几个。配置 prod.keys 或数据库能匹配 NCA 时可精确识别。")) : null,
    h("div.row", seg), list);
}

// ---------- 编辑中文名 ----------
function editName(g, info, done) {
  const inp = h("input.input", { value: g.nameSource === "custom" ? g.name : (info.libName || g.name), style: { width: "100%" } });
  modal({
    title: "编辑中文名",
    desc: "自定义名称优先于数据库，整理与导出都会使用。留空保存即恢复为库中名称。",
    body: h("div.stack",
      h("dl.kv", h("dt", "TitleID"), h("dd.mono", g.tid), h("dt", "库中名称"), h("dd", info.libName || "—"),
        info.nameHK && info.nameHK !== info.libName ? [h("dt", "港区原名"), h("dd", info.nameHK)] : null),
      inp,
      h("div.row", h("button.btn.sm", { onclick: () => { inp.value = info.libName || ""; inp.focus(); } }, "填入库中名称"),
        h("button.btn.sm", { onclick: () => { inp.value = ""; inp.focus(); } }, "清空"))),
    ok: "保存",
    onOk: async () => { await api("PUT", "/api/names/" + g.tid, { name: inp.value }); toast("已保存", "ok"); done(); },
  });
  setTimeout(() => { inp.focus(); inp.select(); }, 50);
}

// ---------- 手动匹配 ----------
function manualMatch(file, closeDrawer) {
  if (!S.state.db.loaded) { toast("数据库未加载，无法搜索", "err"); return; }
  const q = h("input.input", { placeholder: "输入中文名片段（如「异度」「宝可梦」）或 TitleID", style: { width: "100%" } });
  const list = h("div.picker-list");
  let sel = null;
  const guess = file.name.replace(/\.[^.]+$/, "").replace(/\[[^\]]*\]|\([^)]*\)/g, " ").trim();
  q.value = guess;
  const search = debounce(async () => {
    const hits = await guard(() => api("GET", "/api/search?q=" + encodeURIComponent(q.value))) || [];
    sel = null;
    list.replaceChildren(...hits.map(x => {
      const it = h("div.it", { onclick: () => { sel = x; list.querySelectorAll(".it").forEach(e => e.classList.remove("sel")); it.classList.add("sel"); } },
        h("div.thumb", x.icon ? h("img", { src: x.icon, loading: "lazy", referrerpolicy: "no-referrer", alt: "" }) : initial(x.name)),
        h("div.grow", h("div", x.name), h("div.mono.faint", x.tid, x.release ? "  ·  " + x.release : "")),
        x.isDLC ? h("span.badge", "DLC") : null);
      return it;
    }));
    if (!hits.length) list.replaceChildren(h("div.empty", { style: { padding: "30px" } }, h("p", "没有结果。港区库里部分游戏用英文名，可以试试英文。")));
  }, 250);
  q.addEventListener("input", search);
  search();
  modal({
    title: "手动指定游戏",
    desc: h("span", "文件：", h("span.mono", file.name)),
    wide: true,
    body: h("div.stack", q, list),
    extra: file.source === "manual" ? h("button.btn.danger", {
      onclick: async () => { await guard(() => startJob("PUT", "/api/manual", { file: file.name, tid: "" })); closeModal(); closeDrawer && closeDrawer(); },
    }, "清除指定") : null,
    ok: "确定",
    onOk: async () => {
      if (!sel) throw new Error("请先在列表里选中一个游戏");
      await startJob("PUT", "/api/manual", { file: file.name, tid: sel.tid });
      closeDrawer && closeDrawer();
    },
  });
  setTimeout(() => q.focus(), 50);
}

// ============================ 整理 ============================

function viewOrganize() {
  const st = S.state;
  const wrap = h("div");
  wrap.appendChild(h("div.page-head",
    h("div", h("h1", "整理"), h("p", "按中文名重命名游戏文件夹与文件。先生成方案逐条确认，勾选的才会执行；每次执行都会生成可一键还原的撤销日志。")),
    h("div.head-actions", h("button.btn.primary", { onclick: makePlan, disabled: !S.games }, icon("refresh"), S.plan ? "重新生成方案" : "生成整理方案"))));

  const tpl = st.config.templates || {};
  wrap.appendChild(h("div.callout", icon("info"), h("div",
    h("div", "文件夹：", h("span.mono", tpl.folder || st.defaults.folder), "　文件：", h("span.mono", tpl.file || st.defaults.file)),
    h("div.faint", "在「设置」里可以修改命名模板。{ver} 与 {dlc} 会保留版本与 DLC 信息，整理后重新扫描仍能识别。"))));
  wrap.appendChild(h("div", { style: { height: "16px" } }));

  if (!S.games) {
    wrap.appendChild(h("div.panel", h("div.empty", icon("gamepad"), h("h3", "请先扫描游戏库"), h("button.btn.primary", { onclick: () => { location.hash = "#/library"; } }, "去扫描"))));
  } else if (S.plan) {
    wrap.appendChild(renderPlan());
  } else {
    wrap.appendChild(h("div.panel", h("div.empty", icon("folder"), h("h3", "生成方案不会改动任何文件"), h("p", "方案里会列出每个游戏的文件夹与文件将被改成什么名字，你可以逐条勾选。"),
      h("button.btn.primary", { onclick: makePlan }, "生成整理方案"))));
  }
  wrap.appendChild(h("div", { style: { height: "24px" } }));
  wrap.appendChild(undoPanel());
  return wrap;
}

async function makePlan() {
  clearPlan();
  const epoch = S.planEpoch;
  let token = "";
  const data = await guard(() => api("POST", "/api/organize/plan", {}, false, res => { token = res.headers.get("X-Plan-Token") || ""; }));
  if (!data || epoch !== S.planEpoch) return;
  const items = data.map(normalizeMoves);
  S.plan = items;
  S.planToken = token;
  S.planSel = new Set(items.filter(i => !i.blocked).map(i => i.key));
  S.planOpen = new Set();
  render();
}

function renderPlan() {
  const items = (S.plan || []).map(normalizeMoves);
  if (!items.length) return h("div.panel", h("div.empty", icon("check"), h("h3", "所有游戏都已整理好"), h("p", "没有识别出中文名的游戏不会被改动。")));
  const box = h("div");
  const counts = {};
  items.forEach(i => { counts[i.mode] = (counts[i.mode] || 0) + 1; });
  box.appendChild(h("div.toolbar",
    h("span.muted", `共 ${items.length} 个游戏：`),
    Object.entries(counts).map(([m, n]) => h("span.badge", MODE_LABEL[m] + " " + n)),
    h("div.grow"),
    h("button.btn.sm", { onclick: () => { items.forEach(i => !i.blocked && S.planSel.add(i.key)); render(); } }, "全选"),
    h("button.btn.sm", { onclick: () => { S.planSel.clear(); render(); } }, "全不选"),
    h("button.btn.sm", { onclick: () => { if (S.planOpen.size) S.planOpen.clear(); else items.forEach(i => S.planOpen.add(i.key)); render(); } }, S.planOpen.size ? "全部收起" : "全部展开")));
  const list = h("div");
  for (const it of items) {
    const open = S.planOpen.has(it.key);
    const cb = h("input.check", { type: "checkbox", checked: S.planSel.has(it.key), disabled: !!it.blocked,
      onchange: e => { e.target.checked ? S.planSel.add(it.key) : S.planSel.delete(it.key); updateFoot(); } });
    list.appendChild(h("div.plan-item", { class: it.blocked ? "blocked" : "" },
      h("div.plan-top", cb,
        h("div.grow",
          h("div.row", h("span.title", it.name), h("span.badge", { class: it.mode === "rename" ? "info" : it.mode === "create" ? "accent" : "" }, MODE_LABEL[it.mode]),
            h("span.faint", `${it.moves.length} 个文件`)),
          h("div.path-flow", h("div", it.oldDir === "." ? "（库根目录）" : it.oldDir), it.targetDir !== it.oldDir ? h("div.to", it.targetDir) : null),
          it.blocked ? h("ul.warn-list", h("li", "⛔ " + it.blocked)) : null,
          it.warnings && it.warnings.length ? h("ul.warn-list", it.warnings.map(w => h("li", "⚠ " + w))) : null),
        it.moves.length ? h("button.btn.sm.ghost", { onclick: () => { open ? S.planOpen.delete(it.key) : S.planOpen.add(it.key); render(); } }, open ? "收起" : "文件") : null),
      open ? h("div.plan-files", it.moves.map(m => h("div.mv", h("div.from", m.from.split("/").pop()), h("div.to", "→ " + m.to.split("/").pop())))) : null));
  }
  box.appendChild(list);
  const mediaSel = h("select.select", {},
    [["none", "不处理媒体"], ["keep", "媒体留在原目录只改名（ES-DE 标准）"], ["into-folder", "媒体移进各游戏文件夹"]].map(([v, t]) =>
      h("option", { value: v, selected: (S.state.config.mediaMode || "none") === v }, t)));
  const selInfo = h("span.muted");
  const applyBtn = h("button.btn.primary", { onclick: () => applyPlan(mediaSel.value) }, "执行整理");
  function updateFoot() {
    selInfo.textContent = `已选 ${S.planSel.size} / ${items.filter(i => !i.blocked).length}`;
    applyBtn.disabled = !S.planSel.size;
  }
  updateFoot();
  box.appendChild(h("div.sticky-foot", selInfo, h("div.grow"), h("span.faint", "图片与 gamelist.xml："), mediaSel, applyBtn));
  return box;
}

async function applyPlan(mediaMode) {
  const n = S.planSel.size;
  const token = S.planToken, keys = [...S.planSel], epoch = S.planEpoch;
  const ok = await confirmDialog("确认执行整理",
    h("div.stack", h("div", `即将整理 ${n} 个游戏，会改动文件夹名与文件名。`),
      h("div.callout", icon("info"), h("div", "执行前会写下整理清单（_before_*.txt），执行中每一步都会实时写入撤销日志（_undo_*.json），可以随时一键还原。已存在的文件绝不会被覆盖。"))),
    "执行");
  if (!ok || epoch !== S.planEpoch) return;
  await guard(() => startJob("POST", "/api/organize/apply", { keys, mediaMode, token }, clearPlan));
}

function undoPanel() {
  const body = h("div.panel-body", h("span.faint", "加载中…"));
  const panel = h("div.panel",
    h("div.panel-head", h("div", h("h2", "撤销记录"), h("p", "整理、媒体同步、移走冗余文件都会在库目录下写 _undo_*.json；旧版 SwitchRomMgr 生成的也能识别。"))),
    body);
  api("GET", "/api/undo").then(list => {
    if (!list.length) { body.replaceChildren(h("span.faint", "暂无撤销记录。")); return; }
    body.replaceChildren(h("div.stack", list.map(u => h("div.row",
      icon("undo"),
      h("div.grow", h("div", u.title || u.file), h("div.faint", h("span.mono", u.file), "  ·  ", u.created, "  ·  ", `${u.moves} 次移动`),
        u.error ? h("div", { style: { color: "var(--bad)" } }, u.error) : null),
      h("button.btn.sm", { disabled: !!u.error, onclick: () => doUndo(u) }, "撤销")))));
  }).catch(e => body.replaceChildren(h("span.faint", e.message)));
  return panel;
}

async function doUndo(u) {
  const ok = await confirmDialog("撤销操作",
    h("div.stack", h("div", `将倒序还原「${u.title || u.file}」中的 ${u.ops} 步操作（其中 ${u.moves} 次文件移动）。`),
      u.sample && u.sample.length ? h("div.path-flow", u.sample.map(s => [h("div", s.to), h("div.to", s.from)])) : null),
    "撤销");
  if (ok) await guard(() => startJob("POST", "/api/undo/apply", { file: u.file }));
}

// ============================ 工具 ============================

function viewTools() {
  const wrap = h("div");
  wrap.appendChild(h("div.page-head", h("div", h("h1", "工具"), h("p", "合并与转换都是纯容器层操作，不接触加密数据；完成后逐文件 SHA-256 校验，源文件不删除。"))));
  const merge = h("div.panel-body", h("span.faint", S.games ? "加载中…" : "请先扫描游戏库"));
  const xci = h("div.panel-body", h("span.faint", S.games ? "加载中…" : "请先扫描游戏库"));
  const grid = h("div.stack",
    redundantPanel(),
    h("div.panel", h("div.panel-head", h("div", h("h2", "合并 NSP"), h("p", "把同一游戏的本体 + 更新 + DLC 拼成一个 NSP。NCA 按内容哈希自动去重。"))), merge),
    h("div.panel", h("div.panel-head", h("div", h("h2", "XCI → NSP"), h("p", "提取卡带 secure 分区。卡带不含票据，产物是「NCA 容器」，仅适合归档。"))), xci),
    mediaToolPanel(),
    dbPanel());
  wrap.appendChild(grid);
  if (S.games) {
    api("GET", "/api/merge").then(d => {
      const sel = new Set();
      if (!d.merge.length) merge.replaceChildren(h("span.faint", "没有可合并的游戏（每个游戏只有一个文件，或缺少本体）。"));
      else {
        const btn = h("button.btn.primary", { disabled: true, onclick: async () => {
          const jobs = d.merge.filter(j => sel.has(j.key));
          const total = jobs.reduce((s, j) => s + j.bytes, 0);
          if (await confirmDialog("确认合并", `将合并 ${jobs.length} 个游戏，约需额外空间 ${fmtSize(total)}（合并期间源文件与产物同时存在）。`, "开始合并"))
            guard(() => startJob("POST", "/api/merge/apply", { keys: [...sel] }));
        } }, "合并选中");
        merge.replaceChildren(h("div.stack",
          d.merge.map(j => h("label.row", { style: { alignItems: "flex-start", cursor: "pointer" } },
            h("input.check", { type: "checkbox", disabled: !!j.note && j.note.includes("已存在"), onchange: e => { e.target.checked ? sel.add(j.key) : sel.delete(j.key); btn.disabled = !sel.size; } }),
            h("div.grow", h("div", { style: { fontWeight: 600 } }, j.name, "  ", h("span.faint", `${j.inputs.length} 个文件 · ${fmtSize(j.bytes)}`)),
              h("div.path-flow", j.inputs.map(i => h("div", i.split("/").pop())), h("div.to", j.output.split("/").pop())),
              j.note ? h("div", { style: { color: "var(--warn)", fontSize: "12.5px" } }, j.note) : null))),
          h("div.row", h("div.grow"), btn)));
      }
      if (!d.xci || !d.xci.length) xci.replaceChildren(h("span.faint", "库里没有 XCI 文件。"));
      else xci.replaceChildren(h("div.stack", d.xci.map(f => h("div.row",
        icon("file"), h("div.grow", h("div", f.name), h("div.faint", fmtSize(f.size), "  ·  ", f.rel)),
        h("button.btn.sm", { onclick: async () => {
          if (await confirmDialog("XCI → NSP", h("div.stack", h("div", "输出到同目录的同名 .nsp。"), h("div.callout.warn", icon("alert"), h("div", "卡带不含票据（.tik），产物能否安装取决于你的环境是否已有对应票据，仅作容器格式归档。"))), "转换"))
            guard(() => startJob("POST", "/api/xci2nsp", { rel: f.rel }));
        } }, "转换")))));
    }).catch(e => merge.replaceChildren(h("span.faint", e.message)));
  }
  return wrap;
}

function redundantPanel() {
  const body = h("div.panel-body", h("span.faint", S.games ? "加载中…" : "请先扫描游戏库"));
  const panel = h("div.panel",
    h("div.panel-head", h("div", h("h2", "清理冗余文件"),
      h("p", "旧版更新、内容重复的文件，以及已经合并进 (merged) 大包的源文件。只判断内容能被精确识别的文件，移走后可在「整理 → 撤销记录」里还原。"))),
    body);
  if (!S.games) return panel;
  api("GET", "/api/redundant").then(d => {
    const bin = d.binFiles ? h("div.faint", `${d.dir}/ 里已有 ${d.binFiles} 个之前移走的文件（${fmtSize(d.binSize)}），确认无误后可以自行删除该目录。`) : null;
    if (!d.files.length) {
      body.replaceChildren(h("div.stack", h("span.faint", "没有发现冗余文件。"), bin));
      return;
    }
    const sel = new Set(d.files.map(f => f.rel));
    const info = h("span.muted");
    const btn = h("button.btn.primary", { onclick: async () => {
      const picked = d.files.filter(f => sel.has(f.rel));
      const size = picked.reduce((s, f) => s + f.size, 0);
      const ok = await confirmDialog("移走冗余文件",
        h("div.stack", h("div", `将把 ${picked.length} 个文件（${fmtSize(size)}）移到库根目录的 ${d.dir}/ 下，保留原来的目录结构。`),
          h("div.callout", icon("info"), h("div", "不会删除任何文件。移动会写入撤销日志，可以一键还原；确认游戏都能正常使用后，再自行删除该目录释放空间。"))),
        "移走");
      if (ok) guard(() => startJob("POST", "/api/redundant/apply", { rels: [...sel] }));
    } }, `移到 ${d.dir}/`);
    const boxes = [];
    const upd = () => {
      const size = d.files.filter(f => sel.has(f.rel)).reduce((s, f) => s + f.size, 0);
      info.textContent = `已选 ${sel.size} / ${d.files.length} · 可释放 ${fmtSize(size)}`;
      btn.disabled = !sel.size;
    };
    const groups = [];
    let cur = null;
    for (const f of d.files) {
      if (!cur || cur.key !== f.key) groups.push(cur = { key: f.key, name: f.name, files: [] });
      cur.files.push(f);
    }
    body.replaceChildren(h("div.stack",
      h("div.row",
        h("button.btn.sm", { onclick: () => { d.files.forEach(f => sel.add(f.rel)); boxes.forEach(b => { b.checked = true; }); upd(); } }, "全选"),
        h("button.btn.sm", { onclick: () => { sel.clear(); boxes.forEach(b => { b.checked = false; }); upd(); } }, "全不选"),
        h("div.grow"), info, btn),
      groups.map(g => h("div",
        h("div", { style: { fontWeight: 600, marginBottom: "4px" } }, g.name),
        g.files.map(f => {
          const cb = h("input.check", { type: "checkbox", checked: true, onchange: e => { e.target.checked ? sel.add(f.rel) : sel.delete(f.rel); upd(); } });
          boxes.push(cb);
          return h("label.row", { style: { alignItems: "flex-start", cursor: "pointer", padding: "4px 0" } }, cb,
            h("div.grow",
              h("div.row", { style: { gap: "8px" } }, h("span", { style: { wordBreak: "break-all" } }, f.rel.split("/").pop()), h("span.badge.warn", f.reason), h("span.faint", fmtSize(f.size))),
              h("div.path-flow", h("div", f.rel), (f.keptBy || []).map(k => h("div.keep", "保留 " + k.split("/").pop())))));
        }))),
      bin));
    upd();
  }).catch(e => body.replaceChildren(h("span.faint", e.message)));
  return panel;
}

function mediaToolPanel() {
  const out = h("div");
  const mode = h("select.select", {}, h("option", { value: "keep" }, "留在原目录只改名（ES-DE 标准）"), h("option", { value: "into-folder" }, "移进各游戏文件夹"));
  const preview = async () => {
    const d = await guard(() => api("POST", "/api/media/plan", { mode: mode.value }));
    if (!d) return;
    const p = normalizeMoves(d.plan);
    out.replaceChildren(h("div.stack", { style: { marginTop: "12px" } },
      h("div.muted", `依据清单 ${d.manifest}（${d.entries} 条映射）：媒体改动 ${p.moves.length} 个，gamelist.xml 修正 ${p.pathFixes} 个路径、${p.mediaFixes} 个媒体引用。`),
      p.moves.length ? h("div.path-flow", p.moves.slice(0, 12).map(m => [h("div", m.from), h("div.to", m.to)]), p.moves.length > 12 ? h("div", `…其余 ${p.moves.length - 12} 项`) : null) : null,
      p.unmatched && p.unmatched.length ? h("div.faint", `对不上任何 ROM 的媒体 ${p.unmatched.length} 个（保持原样）`) : null,
      p.orphans && p.orphans.length ? h("div.faint", `gamelist.xml 里指向不存在文件的条目 ${p.orphans.length} 个`) : null,
      h("div.row", h("div.grow"), h("button.btn.primary", { disabled: !p.moves.length && !p.pathFixes && !p.mediaFixes,
        onclick: () => guard(() => startJob("POST", "/api/media/apply", { mode: mode.value })) }, "执行"))));
  };
  return h("div.panel",
    h("div.panel-head", h("div", h("h2", "同步媒体与 gamelist.xml"), h("p", "ROM 改名后，按最近一次整理清单修正图片文件名与 gamelist.xml（自动备份，可撤销）。整理时也可以直接勾选一并处理。"))),
    h("div.panel-body", h("div.row", mode, h("button.btn", { onclick: preview }, "预览")), out));
}

function dbPanel() {
  const db = S.state.db;
  return h("div.panel",
    h("div.panel-head", h("div", h("h2", "元数据库"), h("p", "来自 blawar/titledb：版本历史、CNMT 内容表、港区/国行中文名。国内网络会自动回退到镜像。"))),
    h("div.panel-body", h("div.row",
      icon("db"),
      h("div.grow",
        db.loaded ? h("div", `版本 ${db.stats.versions} · CNMT ${db.stats.cnmts} · NCA 索引 ${db.stats.ncas} · 中文名 ${db.stats.namesHK + db.stats.namesCN}`) : h("div", db.error || "未加载"),
        h("div.faint", h("span.mono", db.dir), db.fileTime ? "  ·  文件时间 " + fmtTime(db.fileTime) : ""),
        (db.warnings || []).map(w => h("div", { style: { color: "var(--warn)" } }, w))),
      h("button.btn", { onclick: () => guard(async () => { S.state = await api("POST", "/api/db/reload"); renderSide(); render(); toast("已重新加载", "ok"); if (jobRunning()) pollJob(); }) }, "重新加载"),
      h("button.btn.primary", { onclick: () => guard(() => startJob("POST", "/api/db/fetch")) }, icon("download"), "下载 / 更新"))));
}

// ============================ 设置 ============================

async function saveConfig(patch) {
  const cfg = Object.assign({}, S.state.config, patch);
  S.state = await api("PUT", "/api/config", cfg);
  renderSide();
  if (jobRunning()) pollJob(); // 改了游戏库目录或数据库目录时，服务端会自动开始扫描
  return S.state;
}

function viewSettings() {
  const st = S.state, cfg = st.config;
  const tpl = cfg.templates || {};
  const f = {
    libraryRoot: h("input.input.mono.grow", { value: cfg.libraryRoot || "", placeholder: "/mnt/user/模拟器/Switch 或 \\\\NAS\\模拟器\\Switch" }),
    dbDir: h("input.input.mono.grow", { value: cfg.dbDir || "" }),
    keysPath: h("input.input.mono.grow", { value: cfg.keysPath || "", placeholder: "可选：prod.keys 的路径" }),
    folder: h("input.input.mono.grow", { value: tpl.folder || "", placeholder: st.defaults.folder }),
    file: h("input.input.mono.grow", { value: tpl.file || "", placeholder: st.defaults.file }),
    simplified: h("input", { type: "checkbox", checked: cfg.simplified }),
    media: h("select.select", {}, [["none", "不处理"], ["keep", "留在原目录只改名（ES-DE 标准）"], ["into-folder", "移进各游戏文件夹"]].map(([v, t]) => h("option", { value: v, selected: (cfg.mediaMode || "none") === v }, t))),
  };
  const pv = h("div.preview-box");
  const example = { name: "异度神剑2", title: "Xenoblade Chronicles 2", tid: "0100F3400332C000", kind: "APP", ver: "[v917504]", dlc: "[+6DLC]" };
  const renderTpl = (t) => { let s = t; for (const [k, v] of Object.entries(example)) s = s.split("{" + k + "}").join(v); return s.replace(/\[\]|\(\)/g, ""); };
  const upd = () => { pv.textContent = renderTpl(f.folder.value || st.defaults.folder) + "/" + renderTpl(f.file.value || st.defaults.file) + ".nsp"; };
  f.folder.oninput = f.file.oninput = upd; upd();
  let lastTpl = f.file;
  f.folder.onfocus = () => { lastTpl = f.folder; }; f.file.onfocus = () => { lastTpl = f.file; };
  const tokens = ["{name}", "{title}", "{tid}", "{kind}", "{ver}", "{dlc}"].map(t => h("span.tok", { onclick: () => {
    const el = lastTpl; const s = el.selectionStart ?? el.value.length;
    el.value = el.value.slice(0, s) + t + el.value.slice(el.selectionEnd ?? s); el.focus(); upd();
  } }, t));

  const save = async () => {
    await guard(async () => {
      await saveConfig({
        libraryRoot: f.libraryRoot.value, dbDir: f.dbDir.value, keysPath: f.keysPath.value,
        simplified: f.simplified.checked, mediaMode: f.media.value,
        templates: { folder: f.folder.value.trim(), file: f.file.value.trim() },
      });
      toast("设置已保存", "ok");
      if (S.state.keys.error) toast("密钥：" + S.state.keys.error, "err");
      await loadGames().catch(() => {});
      render();
    });
  };
  const browse = (inp, title, files) => h("button.btn", { onclick: () => pickPath(title, inp.value, files, p => { inp.value = p; }) }, "浏览…");
  const keys = st.keys;
  return h("div",
    h("div.page-head", h("div", h("h1", "设置"), h("p", h("span", "数据目录："), h("span.mono", st.dataDir))), h("div.head-actions", h("button.btn.primary", { onclick: save }, "保存设置"))),
    h("div.stack",
      h("div.panel", h("div.panel-head", h("h2", "路径")), h("div.panel-body",
        h("div.field", h("label.l", "游戏库目录"), h("div", h("div.row", f.libraryRoot, browse(f.libraryRoot, "游戏库目录")),
          h("div.hint", "扫描时会跳过隐藏目录、回收站（#recycle / $RECYCLE.BIN）和 @eaDir 等 NAS 系统目录。"))),
        h("div.field", h("label.l", "元数据库目录"), h("div", h("div.row", f.dbDir, browse(f.dbDir, "元数据库目录")),
          h("div.hint", "存放 versions.json / cnmts.json / HK.zh.json / CN.zh.json。可以直接指向旧版 SwitchRomMgr 的 titledb 文件夹。"))),
        h("div.field", h("label.l", "prod.keys（可选）"), h("div", h("div.row", f.keysPath, browse(f.keysPath, "选择 prod.keys", true)),
          h("div.hint", "提供后直接解密 CNMT，精确读出每个文件包含的 TitleID、版本与 DLC，不再依赖文件名。只用到 header_key 与 key_area_key_application_XX；本工具不附带任何密钥。"),
          keys.loaded ? h("div.hint", { style: { color: "var(--ok)" } }, `✓ 已加载，最高密钥代数 ${keys.maxGen}`) : keys.error ? h("div.hint", { style: { color: "var(--bad)" } }, "✗ " + keys.error) : null)))),
      h("div.panel", h("div.panel-head", h("h2", "名称与整理")), h("div.panel-body",
        h("div.field", h("label.l", "繁体转简体"), h("div", h("label.switch", f.simplified, h("span.track"), h("span", "港区繁体名自动转成简体")),
          h("div.hint", "字符级转换未必等于官方译名（如「薩爾達」→「萨尔达」，官方为「塞尔达」），遇到可在详情里编辑中文名。"))),
        h("div.field", h("label.l", "文件夹命名"), h("div", f.folder)),
        h("div.field", h("label.l", "文件命名"), h("div", f.file,
          h("div.hint", "点击插入：", tokens),
          h("div.hint", "{ver} → [v917504]，{dlc} → [+6DLC]，为空时自动省略。文件名模板必须包含 {tid}。"),
          pv)),
        h("div.field", h("label.l", "默认媒体处理"), h("div", f.media, h("div.hint", "整理时同步修正 images/ 里的封面与 gamelist.xml（ES-DE / EmulationStation）。"))))),
      h("div.row", h("div.grow"), h("button.btn.primary", { onclick: save }, "保存设置"))));
}

// ============================ 弹窗 ============================

function modal({ title, desc, body, ok = "确定", cancel = "取消", onOk, wide, extra, danger }) {
  const root = $("#modal-root");
  const okBtn = h("button.btn.primary", { class: danger ? "danger" : "" }, ok);
  const close = () => { root.replaceChildren(); document.removeEventListener("keydown", key); };
  const key = e => { if (e.key === "Escape") close(); if (e.key === "Enter" && e.target.tagName === "INPUT") okBtn.click(); };
  okBtn.onclick = async () => {
    okBtn.disabled = true;
    try { if (onOk) await onOk(); close(); } catch (e) { toast(e.message, "err"); } finally { okBtn.disabled = false; }
  };
  const box = h("div.modal", { class: wide ? "wide" : "" },
    h("div.modal-head", h("h3", title), desc ? h("p", desc) : null),
    body ? h("div.modal-body", body) : null,
    h("div.modal-foot", extra, h("div.grow"), cancel ? h("button.btn", { onclick: close }, cancel) : null, okBtn));
  root.replaceChildren(h("div.modal-wrap", { onmousedown: e => { if (e.target === e.currentTarget) close(); } }, box));
  document.addEventListener("keydown", key);
  return { close };
}
function closeModal() { $("#modal-root").replaceChildren(); }
function confirmDialog(title, content, okText = "确定") {
  return new Promise(res => {
    let done = false;
    const m = modal({ title, body: typeof content === "string" ? h("div", content) : content, ok: okText, onOk: () => { done = true; res(true); } });
    const obs = new MutationObserver(() => { if (!$("#modal-root").children.length) { obs.disconnect(); if (!done) res(false); } });
    obs.observe($("#modal-root"), { childList: true });
    return m;
  });
}

// 目录/文件选择器（浏览服务器端文件系统：桌面模式即本机，Docker 模式即容器内挂载）
function pickPath(title, start, files, onPick) {
  let cur = start || "";
  let picked = null;
  const crumb = h("div.crumb");
  const list = h("div.picker-list");
  const manual = h("input.input.mono", { value: start || "", placeholder: "也可以直接粘贴路径", style: { width: "100%" } });
  const load = async (p) => {
    const d = await guard(() => api("GET", "/api/fs?path=" + encodeURIComponent(p) + (files ? "&files=1" : "")));
    if (!d) return;
    cur = d.path; picked = null;
    crumb.textContent = cur || "（选择起点）";
    if (cur) manual.value = cur;
    const items = [];
    if (cur) items.push(h("div.it", { onclick: () => load(d.parent || "") }, icon("undo"), h("span.faint", "上一级")));
    for (const e of d.entries || []) {
      const it = h("div.it", {
        onclick: () => { if (e.isDir) load(e.path); else { picked = e.path; manual.value = e.path; list.querySelectorAll(".it").forEach(x => x.classList.remove("sel")); it.classList.add("sel"); } },
      }, icon(e.isDir ? "folder" : "file"), h("span.grow", e.name));
      items.push(it);
    }
    if (!(d.entries || []).length) items.push(h("div.it.faint", "（空）"));
    list.replaceChildren(...items);
  };
  load(start && !files ? start : start ? start.replace(/[\\/][^\\/]*$/, "") : "");
  modal({
    title, wide: true,
    desc: S.state.remote ? "这里浏览的是运行本程序的机器（Docker 模式下为容器内路径）。" : "",
    body: h("div.stack", crumb, list, manual),
    ok: files ? "选择此文件" : "选择此目录",
    onOk: () => { const v = manual.value.trim() || picked || cur; if (!v) throw new Error("请选择路径"); onPick(v); },
  });
}

// ============================ 启动 ============================

(async function init() {
  const theme = store("theme");
  if (theme) document.documentElement.dataset.theme = theme;
  document.querySelectorAll("[data-icon]").forEach(el => el.replaceWith(icon(el.dataset.icon)));
  window.addEventListener("hashchange", route);
  try {
    await loadState();
    await loadGames();
  } catch (e) {
    toast(e.message, "err");
  }
  route();
  // 数据库在后台加载，完成后刷新一次侧栏
  if (S.state && !S.state.db.loaded && !S.state.db.error) {
    const wait = async () => {
      await guard(loadState);
      if (!S.state.db.loaded && !S.state.db.error) setTimeout(wait, 800); else render();
    };
    setTimeout(wait, 800);
  }
})();
