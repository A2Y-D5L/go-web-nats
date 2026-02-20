// File: web/app.js
const createForm = document.getElementById("createForm");
const updateForm = document.getElementById("updateForm");
const webhookForm = document.getElementById("webhookForm");
const refreshBtn = document.getElementById("refreshBtn");
const deleteBtn = document.getElementById("deleteBtn");
const loadArtifactsBtn = document.getElementById("loadArtifactsBtn");
const copyPreviewBtn = document.getElementById("copyPreviewBtn");

const createAPIVersion = document.getElementById("createAPIVersion");
const createKind = document.getElementById("createKind");
const createName = document.getElementById("createName");
const createRuntime = document.getElementById("createRuntime");
const createCapabilities = document.getElementById("createCapabilities");
const createIngress = document.getElementById("createIngress");
const createEgress = document.getElementById("createEgress");
const createEnvironments = document.getElementById("createEnvironments");

const updateAPIVersion = document.getElementById("updateAPIVersion");
const updateKind = document.getElementById("updateKind");
const updateName = document.getElementById("updateName");
const updateRuntime = document.getElementById("updateRuntime");
const updateCapabilities = document.getElementById("updateCapabilities");
const updateIngress = document.getElementById("updateIngress");
const updateEgress = document.getElementById("updateEgress");
const updateEnvironments = document.getElementById("updateEnvironments");
const webhookRepo = document.getElementById("webhookRepo");
const webhookBranch = document.getElementById("webhookBranch");
const webhookRef = document.getElementById("webhookRef");
const webhookCommit = document.getElementById("webhookCommit");

const projectSearch = document.getElementById("projectSearch");
const phaseFilter = document.getElementById("phaseFilter");
const projectSort = document.getElementById("projectSort");
const projectStats = document.getElementById("projectStats");

const artifactSearch = document.getElementById("artifactSearch");
const artifactPreviewEl = document.getElementById("artifactPreview");

const projectsEl = document.getElementById("projects");
const selectedEl = document.getElementById("selected");
const artifactsEl = document.getElementById("artifacts");
const lastOpEl = document.getElementById("lastOp");
const opProgressEl = document.getElementById("opProgress");
const opTimelineEl = document.getElementById("opTimeline");
const statusEl = document.getElementById("status");

const fullWorkerOrder = ["registrar", "repoBootstrap", "imageBuilder", "manifestRenderer"];
const ciWorkerOrder = ["imageBuilder", "manifestRenderer"];

const defaultEnvironments = {
  dev: {
    vars: {
      LOG_LEVEL: "info",
      LOG_FORMAT: "json",
    },
  },
  prod: {
    vars: {
      LOG_LEVEL: "warn",
      LOG_FORMAT: "json",
    },
  },
};

let allProjects = [];
let selectedProject = null;
let artifactFiles = [];
let previewFilePath = "";
let currentPreviewText = "";

let currentMonitoredOpID = "";
let opMonitorTimer = null;
let opMonitorToken = 0;

function pretty(v) {
  return JSON.stringify(v, null, 2);
}

function setStatus(msg, busy = false) {
  statusEl.textContent = msg || "";
  statusEl.classList.toggle("busy", !!busy);
}

function setMuted(target, msg) {
  target.replaceChildren();
  const div = document.createElement("div");
  div.className = "muted";
  div.textContent = msg;
  target.appendChild(div);
}

function hasRealTimestamp(ts) {
  return !!ts && !String(ts).startsWith("0001-01-01");
}

function toTime(ts) {
  if (!hasRealTimestamp(ts)) return "-";
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return String(ts);
  return d.toLocaleString();
}

function duration(start, end) {
  if (!hasRealTimestamp(start) || !hasRealTimestamp(end)) return "-";
  const ms = new Date(end).getTime() - new Date(start).getTime();
  if (!Number.isFinite(ms) || ms < 0) return "-";
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

function phaseClass(phase) {
  const key = (phase || "Unknown").toLowerCase();
  switch (key) {
    case "ready":
      return "phase-ready";
    case "reconciling":
      return "phase-reconciling";
    case "deleting":
      return "phase-deleting";
    case "error":
      return "phase-error";
    case "done":
      return "phase-ready";
    case "running":
      return "phase-reconciling";
    case "pending":
      return "phase-unknown";
    default:
      return "phase-unknown";
  }
}

function makeBadge(label, extraClass) {
  const span = document.createElement("span");
  span.className = `phase-badge ${extraClass}`;
  span.textContent = label;
  return span;
}

function dateValue(ts) {
  const v = Date.parse(ts || "");
  return Number.isNaN(v) ? 0 : v;
}

function parseCapabilities(raw) {
  return String(raw || "")
    .split(/[\n,]/)
    .map((v) => v.trim())
    .filter(Boolean)
    .filter((v, idx, arr) => arr.indexOf(v) === idx);
}

function workerOrderForKind(kind) {
  return kind === "ci" ? ciWorkerOrder : fullWorkerOrder;
}

function parseEnvironments(raw, fieldLabel) {
  let parsed;
  try {
    parsed = JSON.parse(raw);
  } catch (e) {
    throw new Error(`${fieldLabel} must be valid JSON`);
  }

  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error(`${fieldLabel} must be an object`);
  }
  return parsed;
}

function buildSpecFromCreateForm() {
  return {
    apiVersion: createAPIVersion.value.trim(),
    kind: createKind.value.trim(),
    name: createName.value.trim(),
    runtime: createRuntime.value.trim(),
    capabilities: parseCapabilities(createCapabilities.value),
    environments: parseEnvironments(createEnvironments.value, "Create environments"),
    networkPolicies: {
      ingress: createIngress.value,
      egress: createEgress.value,
    },
  };
}

function buildSpecFromUpdateForm() {
  return {
    apiVersion: updateAPIVersion.value.trim(),
    kind: updateKind.value.trim(),
    name: updateName.value.trim(),
    runtime: updateRuntime.value.trim(),
    capabilities: parseCapabilities(updateCapabilities.value),
    environments: parseEnvironments(updateEnvironments.value, "Update environments"),
    networkPolicies: {
      ingress: updateIngress.value,
      egress: updateEgress.value,
    },
  };
}

function setCreateDefaults() {
  createAPIVersion.value = "platform.example.com/v2";
  createKind.value = "App";
  createRuntime.value = "go_1.26";
  createCapabilities.value = "";
  createIngress.value = "internal";
  createEgress.value = "internal";
  createEnvironments.value = pretty(defaultEnvironments);
}

function setUpdateDefaults() {
  updateAPIVersion.value = "platform.example.com/v2";
  updateKind.value = "App";
  updateName.value = "";
  updateRuntime.value = "";
  updateCapabilities.value = "";
  updateIngress.value = "internal";
  updateEgress.value = "internal";
  updateEnvironments.value = pretty(defaultEnvironments);
}

function buildWebhookPayload() {
  return {
    project_id: selectedProject.id,
    repo: webhookRepo.value.trim(),
    branch: webhookBranch.value.trim(),
    ref: webhookRef.value.trim(),
    commit: webhookCommit.value.trim(),
  };
}

function getVisibleProjects() {
  const term = (projectSearch.value || "").trim().toLowerCase();
  const phase = phaseFilter.value;

  const filtered = allProjects.filter((p) => {
    if (phase !== "all" && (p.status?.phase || "") !== phase) return false;
    if (!term) return true;
    const name = (p.spec?.name || "").toLowerCase();
    const id = (p.id || "").toLowerCase();
    const runtime = (p.spec?.runtime || "").toLowerCase();
    return name.includes(term) || id.includes(term) || runtime.includes(term);
  });

  const sortKey = projectSort.value;
  filtered.sort((a, b) => {
    switch (sortKey) {
      case "name_asc":
        return (a.spec?.name || "").localeCompare(b.spec?.name || "", undefined, { sensitivity: "base" });
      case "created_asc":
        return dateValue(a.created_at) - dateValue(b.created_at);
      case "updated_desc":
      default:
        return dateValue(b.updated_at) - dateValue(a.updated_at);
    }
  });

  return filtered;
}

function clearOperationUI() {
  lastOpEl.textContent = "";
  setMuted(opProgressEl, "No operation selected.");
  setMuted(opTimelineEl, "Run create, update, or delete to see live worker progress.");
}

function stopOpMonitor() {
  if (opMonitorTimer) {
    clearTimeout(opMonitorTimer);
    opMonitorTimer = null;
  }
  opMonitorToken += 1;
  currentMonitoredOpID = "";
}

function resetArtifactsPanel() {
  artifactFiles = [];
  previewFilePath = "";
  currentPreviewText = "";
  artifactSearch.value = "";
  artifactsEl.replaceChildren();
  artifactPreviewEl.textContent = "Select a file to preview.";
  copyPreviewBtn.disabled = true;
}

function setNoSelection() {
  selectedProject = null;
  selectedEl.textContent = "None selected.";
  setUpdateDefaults();
  resetArtifactsPanel();
  stopOpMonitor();
  clearOperationUI();
  renderProjects();
}

async function api(method, url, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers["content-type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const r = await fetch(url, opts);
  if (!r.ok) {
    const t = await r.text();
    throw new Error(`${method} ${url} -> ${r.status}: ${t}`);
  }
  const ct = r.headers.get("content-type") || "";
  if (ct.includes("application/json")) return await r.json();
  return await r.text();
}

function renderProjects() {
  const visible = getVisibleProjects();
  projectStats.textContent = `${visible.length} visible of ${allProjects.length}`;

  projectsEl.replaceChildren();
  if (!visible.length) {
    const empty = document.createElement("div");
    empty.className = "muted";
    empty.textContent = allProjects.length
      ? "No projects match the active filters."
      : "No projects yet.";
    projectsEl.appendChild(empty);
    return;
  }

  for (const p of visible) {
    const item = document.createElement("div");
    item.className = "project";
    if (selectedProject?.id === p.id) item.classList.add("selected");

    const head = document.createElement("div");
    head.className = "project-head";

    const title = document.createElement("div");
    title.className = "title";
    title.textContent = p.spec?.name || "(unnamed)";

    const badge = makeBadge(p.status?.phase || "Unknown", phaseClass(p.status?.phase));
    head.append(title, badge);

    const idMeta = document.createElement("div");
    idMeta.className = "meta";
    idMeta.textContent = `id: ${p.id}`;

    const runtimeMeta = document.createElement("div");
    runtimeMeta.className = "meta";
    runtimeMeta.textContent = `runtime: ${p.spec?.runtime || "n/a"}`;

    const msgMeta = document.createElement("div");
    msgMeta.className = "meta";
    msgMeta.textContent = p.status?.message || "";

    item.append(head, idMeta, runtimeMeta, msgMeta);
    item.onclick = () => selectProject(p);
    projectsEl.appendChild(item);
  }
}

function selectProject(project, opts = {}) {
  const { preserveArtifacts = false, preserveOperation = false } = opts;
  selectedProject = project;

  selectedEl.replaceChildren();

  const name = document.createElement("div");
  const nameBold = document.createElement("b");
  nameBold.textContent = project.spec?.name || "(unnamed)";
  name.appendChild(nameBold);

  const idLine = document.createElement("div");
  idLine.className = "muted";
  idLine.textContent = `id: ${project.id}`;

  const runtimeLine = document.createElement("div");
  runtimeLine.className = "muted";
  runtimeLine.textContent = `runtime: ${project.spec?.runtime || "n/a"}`;

  const phaseLine = document.createElement("div");
  phaseLine.className = "row";
  const phaseLabel = document.createElement("span");
  phaseLabel.className = "muted";
  phaseLabel.textContent = "phase:";
  phaseLine.append(phaseLabel, makeBadge(project.status?.phase || "Unknown", phaseClass(project.status?.phase)));

  selectedEl.append(name, idLine, runtimeLine, phaseLine);

  const spec = project.spec || {};
  updateAPIVersion.value = spec.apiVersion || "platform.example.com/v2";
  updateKind.value = spec.kind || "App";
  updateName.value = spec.name || "";
  updateRuntime.value = spec.runtime || "";
  updateCapabilities.value = Array.isArray(spec.capabilities) ? spec.capabilities.join(",") : "";
  updateIngress.value = spec.networkPolicies?.ingress || "internal";
  updateEgress.value = spec.networkPolicies?.egress || "internal";
  updateEnvironments.value = pretty(spec.environments || defaultEnvironments);

  if (!preserveArtifacts) {
    resetArtifactsPanel();
  }

  const lastOpID = project.status?.last_op_id || "";
  if (!lastOpID) {
    if (!preserveOperation) {
      stopOpMonitor();
      clearOperationUI();
    }
  } else if (!preserveOperation || currentMonitoredOpID !== lastOpID) {
    startOpMonitor(lastOpID);
  }

  renderProjects();
  setStatus("");
}

async function refreshProjects() {
  const projects = await api("GET", "/api/projects");
  allProjects = Array.isArray(projects) ? projects : [];

  const selectedID = selectedProject?.id;
  renderProjects();

  if (!selectedID) return;

  const latest = allProjects.find((x) => x.id === selectedID);
  if (latest) {
    selectProject(latest, { preserveArtifacts: true, preserveOperation: true });
  } else {
    setNoSelection();
  }
}

function workerStep(op, worker) {
  const steps = op.steps || [];
  for (let i = steps.length - 1; i >= 0; i--) {
    if (steps[i].worker === worker) return steps[i];
  }
  return null;
}

function stepState(step) {
  if (!step) return "pending";
  if (step.error) return "error";
  if (hasRealTimestamp(step.ended_at)) return "done";
  if (hasRealTimestamp(step.started_at)) return "running";
  return "pending";
}

function isTerminalOpStatus(status) {
  return status === "done" || status === "error";
}

function renderOperationProgress(op) {
  opProgressEl.replaceChildren();

  const status = op.status || "unknown";
  const order = workerOrderForKind(op.kind);
  let doneCount = 0;

  for (const worker of order) {
    const state = stepState(workerStep(op, worker));
    if (state === "done") doneCount += 1;
  }

  if (status === "done") doneCount = order.length;

  let pct = Math.round((doneCount / order.length) * 100);
  if (status === "running") pct = Math.max(10, pct);
  if (status === "error") pct = Math.max(25, pct);

  const card = document.createElement("div");
  card.className = "op-progress";

  const summary = document.createElement("div");
  summary.className = "op-summary";

  const left = document.createElement("div");
  left.textContent = `op ${String(op.id || "").slice(0, 8)} - ${op.kind || "unknown"}`;

  const statusBadge = makeBadge(status, phaseClass(status));
  summary.append(left, statusBadge);

  const bar = document.createElement("div");
  bar.className = "op-bar";
  const fill = document.createElement("span");
  fill.style.width = `${pct}%`;
  if (status === "error") {
    fill.style.background = "linear-gradient(90deg, #d92d20, #b42318)";
  }
  bar.appendChild(fill);

  card.append(summary, bar);
  opProgressEl.appendChild(card);
}

function renderOperationTimeline(op) {
  opTimelineEl.replaceChildren();

  for (const worker of workerOrderForKind(op.kind)) {
    const step = workerStep(op, worker);
    const state = stepState(step);

    const row = document.createElement("div");
    row.className = `timeline-item ${state}`;

    const head = document.createElement("div");
    head.className = "timeline-head";

    const title = document.createElement("span");
    title.textContent = worker;

    const stateLabel = state[0].toUpperCase() + state.slice(1);
    const stateBadge = makeBadge(stateLabel, phaseClass(state));

    head.append(title, stateBadge);

    const meta = document.createElement("div");
    meta.className = "timeline-meta";

    if (!step) {
      meta.textContent = "Waiting for this worker.";
    } else {
      const details = [];
      details.push(`started: ${toTime(step.started_at)}`);
      details.push(`ended: ${toTime(step.ended_at)}`);
      details.push(`duration: ${duration(step.started_at, step.ended_at)}`);
      if (step.message) details.push(`message: ${step.message}`);
      if (step.error) details.push(`error: ${step.error}`);
      if (Array.isArray(step.artifacts) && step.artifacts.length) {
        details.push(`artifacts: ${step.artifacts.length}`);
      }
      meta.textContent = details.join(" | ");
    }

    row.append(head, meta);
    opTimelineEl.appendChild(row);
  }
}

function renderOperation(op) {
  lastOpEl.textContent = pretty(op);
  renderOperationProgress(op);
  renderOperationTimeline(op);
}

async function startOpMonitor(opID) {
  if (!opID) {
    stopOpMonitor();
    clearOperationUI();
    return;
  }

  stopOpMonitor();
  currentMonitoredOpID = opID;
  const token = opMonitorToken;

  const poll = async () => {
    if (token !== opMonitorToken) return;

    try {
      const op = await api("GET", `/api/ops/${encodeURIComponent(opID)}`);
      if (token !== opMonitorToken) return;

      renderOperation(op);

      if (!isTerminalOpStatus(op.status)) {
        opMonitorTimer = setTimeout(poll, 850);
      }
    } catch (e) {
      if (token !== opMonitorToken) return;
      setStatus(`Operation monitor warning: ${e.message}`);
      opMonitorTimer = setTimeout(poll, 1500);
    }
  };

  await poll();
}

function artifactUrl(file) {
  return `/api/projects/${encodeURIComponent(selectedProject.id)}/artifacts/${encodeURIComponent(file).replaceAll("%2F", "/")}`;
}

function isProbablyText(bytes) {
  if (!bytes.length) return true;

  const sample = bytes.subarray(0, Math.min(bytes.length, 512));
  let suspicious = 0;

  for (const b of sample) {
    if (b === 0) return false;
    const isControl = b < 32 && b !== 9 && b !== 10 && b !== 13;
    if (isControl) suspicious += 1;
  }

  return suspicious / sample.length < 0.08;
}

function renderArtifactsList() {
  artifactsEl.replaceChildren();

  const term = (artifactSearch.value || "").trim().toLowerCase();
  const files = artifactFiles.filter((f) => f.toLowerCase().includes(term));

  if (!files.length) {
    const empty = document.createElement("div");
    empty.className = "muted";
    empty.textContent = artifactFiles.length
      ? "No artifacts match the current filter."
      : "No artifacts found.";
    artifactsEl.appendChild(empty);
    return;
  }

  for (const file of files) {
    const row = document.createElement("div");
    row.className = "artifact-item";
    if (previewFilePath === file) row.classList.add("selected");

    const a = document.createElement("a");
    a.href = artifactUrl(file);
    a.textContent = file;
    a.className = "file";
    a.target = "_blank";

    const previewBtn = document.createElement("button");
    previewBtn.type = "button";
    previewBtn.className = "preview-btn";
    previewBtn.textContent = "Preview";
    previewBtn.onclick = async () => {
      setStatus(`Previewing ${file}...`, true);
      try {
        await previewArtifact(file);
        setStatus(`Previewed ${file}.`);
      } catch (e) {
        setStatus(`Error: ${e.message}`);
      } finally {
        statusEl.classList.remove("busy");
      }
    };

    row.append(a, previewBtn);
    artifactsEl.appendChild(row);
  }
}

async function loadArtifacts() {
  if (!selectedProject) return;
  const r = await api("GET", `/api/projects/${encodeURIComponent(selectedProject.id)}/artifacts`);
  const files = Array.isArray(r.files) ? r.files : [];
  artifactFiles = [...files].sort((a, b) => a.localeCompare(b));

  if (!artifactFiles.includes(previewFilePath)) {
    previewFilePath = "";
    currentPreviewText = "";
    artifactPreviewEl.textContent = "Select a file to preview.";
    copyPreviewBtn.disabled = true;
  }

  renderArtifactsList();
}

async function previewArtifact(file) {
  if (!selectedProject) return;
  previewFilePath = file;
  renderArtifactsList();

  artifactPreviewEl.classList.remove("muted");
  artifactPreviewEl.textContent = "Loading preview...";
  copyPreviewBtn.disabled = true;

  const res = await fetch(artifactUrl(file));
  if (!res.ok) {
    const t = await res.text();
    throw new Error(`preview failed (${res.status}): ${t}`);
  }

  const buf = await res.arrayBuffer();
  const bytes = new Uint8Array(buf);

  if (!bytes.length) {
    currentPreviewText = "";
    artifactPreviewEl.textContent = "(empty file)";
    copyPreviewBtn.disabled = false;
    return;
  }

  if (!isProbablyText(bytes)) {
    currentPreviewText = "";
    artifactPreviewEl.classList.add("muted");
    artifactPreviewEl.textContent = `Binary file (${bytes.length} bytes). Open via file link for download.`;
    copyPreviewBtn.disabled = true;
    return;
  }

  const maxBytes = 20000;
  const truncated = bytes.length > maxBytes;
  const sliced = bytes.subarray(0, maxBytes);
  const text = new TextDecoder("utf-8", { fatal: false }).decode(sliced);

  currentPreviewText = truncated
    ? `${text}\n\n--- preview truncated at ${maxBytes} bytes ---`
    : text;
  artifactPreviewEl.textContent = currentPreviewText;
  copyPreviewBtn.disabled = false;
}

refreshBtn.onclick = async () => {
  setStatus("Refreshing...", true);
  try {
    await refreshProjects();
    setStatus("Refreshed.");
  } catch (e) {
    setStatus(`Error: ${e.message}`);
  } finally {
    statusEl.classList.remove("busy");
  }
};

createForm.onsubmit = async (e) => {
  e.preventDefault();
  setStatus("Creating (live progress enabled)...", true);
  try {
    const spec = buildSpecFromCreateForm();
    const res = await api("POST", "/api/events/registration", {
      action: "create",
      spec,
    });

    await refreshProjects();

    if (res.project) {
      const latest = allProjects.find((x) => x.id === res.project.id) || res.project;
      selectProject(latest, { preserveArtifacts: false, preserveOperation: true });
    }

    if (res.op?.id) {
      await startOpMonitor(res.op.id);
    }

    setStatus("Created.");
  } catch (e) {
    setStatus(`Error: ${e.message}`);
  } finally {
    statusEl.classList.remove("busy");
  }
};

updateForm.onsubmit = async (e) => {
  e.preventDefault();
  if (!selectedProject) return;

  setStatus("Updating (live progress enabled)...", true);
  try {
    const spec = buildSpecFromUpdateForm();
    const res = await api("POST", "/api/events/registration", {
      action: "update",
      project_id: selectedProject.id,
      spec,
    });

    await refreshProjects();

    if (res.project) {
      const latest = allProjects.find((x) => x.id === res.project.id) || res.project;
      selectProject(latest, { preserveArtifacts: true, preserveOperation: true });
    }

    if (res.op?.id) {
      await startOpMonitor(res.op.id);
    }

    setStatus("Updated.");
  } catch (e) {
    setStatus(`Error: ${e.message}`);
  } finally {
    statusEl.classList.remove("busy");
  }
};

webhookForm.onsubmit = async (e) => {
  e.preventDefault();
  if (!selectedProject) {
    setStatus("Select a project first.");
    return;
  }

  setStatus("Triggering CI webhook...", true);
  try {
    const payload = buildWebhookPayload();
    const res = await api("POST", "/api/webhooks/source", payload);
    if (!res.accepted) {
      setStatus(`Webhook ignored: ${res.reason || "not accepted"}`);
      return;
    }

    if (res.op?.id) {
      await startOpMonitor(res.op.id);
    }

    await refreshProjects();
    const latest = allProjects.find((x) => x.id === selectedProject.id);
    if (latest) {
      selectProject(latest, { preserveArtifacts: true, preserveOperation: true });
    }
    setStatus("CI triggered from source webhook.");
  } catch (e) {
    setStatus(`Error: ${e.message}`);
  } finally {
    statusEl.classList.remove("busy");
  }
};

deleteBtn.onclick = async () => {
  if (!selectedProject) return;
  const ok = confirm(`Delete project "${selectedProject.spec.name}"?\nThis cleans up all artifacts on disk.`);
  if (!ok) return;

  setStatus("Deleting (live progress enabled)...", true);
  try {
    const res = await api("POST", "/api/events/registration", {
      action: "delete",
      project_id: selectedProject.id,
    });

    if (res.op) {
      renderOperation(res.op);
    }

    setNoSelection();
    await refreshProjects();
    setStatus("Deleted.");
  } catch (e) {
    setStatus(`Error: ${e.message}`);
  } finally {
    statusEl.classList.remove("busy");
  }
};

loadArtifactsBtn.onclick = async () => {
  if (!selectedProject) {
    setStatus("Select a project first.");
    return;
  }

  setStatus("Loading artifacts...", true);
  try {
    await loadArtifacts();
    setStatus(`Artifacts loaded (${artifactFiles.length}).`);
  } catch (e) {
    setStatus(`Error: ${e.message}`);
  } finally {
    statusEl.classList.remove("busy");
  }
};

copyPreviewBtn.onclick = async () => {
  if (copyPreviewBtn.disabled) return;
  try {
    await navigator.clipboard.writeText(currentPreviewText);
    setStatus("Preview copied to clipboard.");
  } catch (e) {
    setStatus(`Copy failed: ${e.message}`);
  }
};

artifactSearch.oninput = () => {
  renderArtifactsList();
};

projectSearch.oninput = () => {
  renderProjects();
};

phaseFilter.onchange = () => {
  renderProjects();
};

projectSort.onchange = () => {
  renderProjects();
};

document.addEventListener("keydown", (e) => {
  if (e.metaKey || e.ctrlKey || e.altKey) return;

  const tagName = String(e.target?.tagName || "").toLowerCase();
  const typing = tagName === "input" || tagName === "textarea" || e.target?.isContentEditable;

  if (!typing && e.key === "/") {
    e.preventDefault();
    projectSearch.focus();
    projectSearch.select();
    return;
  }

  if (typing) return;

  const key = e.key.toLowerCase();
  if (key === "r") {
    e.preventDefault();
    refreshBtn.click();
  } else if (key === "a") {
    e.preventDefault();
    loadArtifactsBtn.click();
  }
});

(async () => {
  setCreateDefaults();
  setUpdateDefaults();
  clearOperationUI();
  setStatus("Loading...", true);
  try {
    await refreshProjects();
    setStatus("");
  } catch (e) {
    setStatus(`Error: ${e.message}`);
  } finally {
    statusEl.classList.remove("busy");
  }
})();
