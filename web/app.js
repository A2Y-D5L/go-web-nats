const dom = {
  forms: {
    create: document.getElementById("createForm"),
    update: document.getElementById("updateForm"),
    webhook: document.getElementById("webhookForm"),
    deleteConfirm: document.getElementById("deleteConfirmForm"),
  },
  buttons: {
    openCreateModal: document.getElementById("openCreateModalBtn"),
    openUpdateModal: document.getElementById("openUpdateModalBtn"),
    openDeleteModal: document.getElementById("openDeleteModalBtn"),
    refresh: document.getElementById("refreshBtn"),
    loadArtifacts: document.getElementById("loadArtifactsBtn"),
    copyPreview: document.getElementById("copyPreviewBtn"),
    createAddEnv: document.getElementById("createAddEnvBtn"),
    createCleanKeys: document.getElementById("createCleanKeysBtn"),
    updateAddEnv: document.getElementById("updateAddEnvBtn"),
    updateCleanKeys: document.getElementById("updateCleanKeysBtn"),
    createModalClose: document.getElementById("createModalCloseBtn"),
    createModalCancel: document.getElementById("createModalCancelBtn"),
    updateModalClose: document.getElementById("updateModalCloseBtn"),
    updateModalCancel: document.getElementById("updateModalCancelBtn"),
    deleteModalClose: document.getElementById("deleteModalCloseBtn"),
    deleteModalCancel: document.getElementById("deleteModalCancelBtn"),
    deleteConfirm: document.getElementById("deleteConfirmBtn"),
  },
  inputs: {
    createAPIVersion: document.getElementById("createAPIVersion"),
    createKind: document.getElementById("createKind"),
    createName: document.getElementById("createName"),
    createRuntime: document.getElementById("createRuntime"),
    createCapabilities: document.getElementById("createCapabilities"),
    createIngress: document.getElementById("createIngress"),
    createEgress: document.getElementById("createEgress"),

    updateAPIVersion: document.getElementById("updateAPIVersion"),
    updateKind: document.getElementById("updateKind"),
    updateName: document.getElementById("updateName"),
    updateRuntime: document.getElementById("updateRuntime"),
    updateCapabilities: document.getElementById("updateCapabilities"),
    updateIngress: document.getElementById("updateIngress"),
    updateEgress: document.getElementById("updateEgress"),

    webhookRepo: document.getElementById("webhookRepo"),
    webhookBranch: document.getElementById("webhookBranch"),
    webhookRef: document.getElementById("webhookRef"),
    webhookCommit: document.getElementById("webhookCommit"),

    projectSearch: document.getElementById("projectSearch"),
    phaseFilter: document.getElementById("phaseFilter"),
    projectSort: document.getElementById("projectSort"),
    artifactSearch: document.getElementById("artifactSearch"),
    deleteConfirm: document.getElementById("deleteConfirmInput"),
  },
  text: {
    healthLabel: document.getElementById("healthLabel"),
    healthMeta: document.getElementById("healthMeta"),
    systemProjectCount: document.getElementById("systemProjectCount"),
    systemReadyCount: document.getElementById("systemReadyCount"),
    systemActiveOp: document.getElementById("systemActiveOp"),
    systemActiveOpMeta: document.getElementById("systemActiveOpMeta"),
    systemBuilderMode: document.getElementById("systemBuilderMode"),
    systemBuilderMeta: document.getElementById("systemBuilderMeta"),
    status: document.getElementById("appStatus"),
    projectStats: document.getElementById("projectStats"),
    selected: document.getElementById("selected"),
    artifactStats: document.getElementById("artifactStats"),
    buildkitSignal: document.getElementById("buildkitSignal"),
    artifactPreviewMeta: document.getElementById("artifactPreviewMeta"),
    artifactPreview: document.getElementById("artifactPreview"),
    opRaw: document.getElementById("lastOp"),
    deleteModalTarget: document.getElementById("deleteModalTarget"),
    deleteConfirmHint: document.getElementById("deleteConfirmHint"),
  },
  containers: {
    projects: document.getElementById("projects"),
    artifacts: document.getElementById("artifacts"),
    opProgress: document.getElementById("opProgress"),
    opTimeline: document.getElementById("opTimeline"),
    opInsights: document.getElementById("opInsights"),
  },
  runtime: {
    timelineButton: document.getElementById("runtimeViewTimeline"),
    artifactsButton: document.getElementById("runtimeViewArtifacts"),
    timelinePanel: document.getElementById("runtimeTimelinePanel"),
    artifactsPanel: document.getElementById("runtimeArtifactsPanel"),
  },
  envEditors: {
    create: document.getElementById("createEnvList"),
    update: document.getElementById("updateEnvList"),
  },
  modals: {
    create: document.getElementById("createModal"),
    update: document.getElementById("updateModal"),
    delete: document.getElementById("deleteModal"),
  },
};

dom.buttons.webhook = dom.forms.webhook.querySelector("button[type='submit']");

const fullWorkerOrder = ["registrar", "repoBootstrap", "imageBuilder", "manifestRenderer"];
const ciWorkerOrder = ["imageBuilder", "manifestRenderer"];
const buildKitArtifactSet = new Set([
  "build/buildkit-summary.txt",
  "build/buildkit-metadata.json",
  "build/buildkit.log",
]);
const runtimeProfiles = [
  { value: "go_1.26", label: "Go version 1.26 (recommended)" },
  { value: "go_1.25", label: "Go version 1.25" },
  { value: "go_1.24", label: "Go version 1.24" },
  { value: "go_1.23", label: "Go version 1.23" },
];
const runtimeLabelByValue = new Map(runtimeProfiles.map((profile) => [profile.value, profile.label]));

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

const state = {
  projects: [],
  filters: {
    search: "",
    phase: "all",
    sort: "updated_desc",
  },
  selectedProjectID: "",
  status: {
    message: "",
    tone: "info",
  },
  artifacts: {
    loaded: false,
    files: [],
    search: "",
    selectedPath: "",
    previewText: "",
    previewMeta: "Preview unavailable",
    previewIsBinary: false,
    previewBytes: 0,
  },
  operation: {
    activeOpID: "",
    payload: null,
    timer: null,
    token: 0,
    failureCount: 0,
  },
  ui: {
    runtimeView: "timeline",
    modal: "none",
  },
};

function pretty(value) {
  return JSON.stringify(value, null, 2);
}

function hasRealTimestamp(ts) {
  return Boolean(ts) && !String(ts).startsWith("0001-01-01");
}

function dateValue(ts) {
  const value = Date.parse(ts || "");
  return Number.isNaN(value) ? 0 : value;
}

function toLocalTime(ts) {
  if (!hasRealTimestamp(ts)) return "-";
  const date = new Date(ts);
  if (Number.isNaN(date.getTime())) return String(ts);
  return date.toLocaleString();
}

function duration(start, end) {
  if (!hasRealTimestamp(start) || !hasRealTimestamp(end)) return "-";
  const ms = new Date(end).getTime() - new Date(start).getTime();
  if (!Number.isFinite(ms) || ms < 0) return "-";
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
  return `${(ms / 60000).toFixed(1)}m`;
}

function elapsedSince(ts) {
  if (!hasRealTimestamp(ts)) return "-";
  const ms = Date.now() - new Date(ts).getTime();
  if (!Number.isFinite(ms) || ms < 0) return "-";
  if (ms < 1000) return `${Math.round(ms)}ms ago`;
  if (ms < 60000) return `${Math.round(ms / 1000)}s ago`;
  if (ms < 3600000) return `${Math.round(ms / 60000)}m ago`;
  return `${Math.round(ms / 3600000)}h ago`;
}

function statusToneFromError(error) {
  const msg = String(error?.message || error || "").toLowerCase();
  if (msg.includes("ignored")) return "warning";
  if (msg.includes("not found") || msg.includes("400")) return "warning";
  return "error";
}

function setStatus(message, tone = "info") {
  state.status.message = message || "";
  state.status.tone = tone;
  renderStatus();
}

function renderStatus() {
  const target = dom.text.status;
  const message = state.status.message.trim();
  target.textContent = message;
  target.className = "status-banner";

  if (!message) {
    target.classList.add("empty");
    return;
  }

  target.classList.remove("empty");
  target.classList.add(`tone-${state.status.tone || "info"}`);
}

function makeElem(tag, className, text) {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (text !== undefined) element.textContent = text;
  return element;
}

function phaseClass(phase) {
  const key = String(phase || "unknown").toLowerCase();
  return `phase-${key}`;
}

function makeBadge(label, phase) {
  const badge = makeElem("span", "phase-badge", label || "unknown");
  badge.classList.add(phaseClass(phase));
  return badge;
}

function parseCapabilities(raw) {
  return String(raw || "")
    .split(/[\n,]/)
    .map((part) => part.trim())
    .filter(Boolean)
    .filter((part, index, list) => list.indexOf(part) === index);
}

function formatRuntimeLiteral(runtimeLiteral) {
  const literal = String(runtimeLiteral || "").trim();
  if (!literal) return "Not set";

  const mapped = runtimeLabelByValue.get(literal);
  if (mapped) return mapped;

  const normalized = literal.replace(/[_-]+/g, " ").trim();
  if (!normalized) return "Custom runtime profile";
  return `Custom runtime profile (${normalized})`;
}

function populateRuntimeSelect(selectEl) {
  selectEl.replaceChildren();
  for (const profile of runtimeProfiles) {
    const option = document.createElement("option");
    option.value = profile.value;
    option.textContent = profile.label;
    selectEl.appendChild(option);
  }
}

function ensureRuntimeOption(selectEl, runtimeLiteral) {
  const literal = String(runtimeLiteral || "").trim();
  if (!literal) return;

  const existing = Array.from(selectEl.options).find((option) => option.value === literal);
  if (existing) {
    selectEl.value = literal;
    return;
  }

  const option = document.createElement("option");
  option.value = literal;
  option.textContent = formatRuntimeLiteral(literal);
  option.dataset.customRuntime = "true";
  selectEl.appendChild(option);
  selectEl.value = literal;
}

function initRuntimePickers() {
  populateRuntimeSelect(dom.inputs.createRuntime);
  populateRuntimeSelect(dom.inputs.updateRuntime);
}

function sanitizeVarKey(raw) {
  const source = String(raw || "").trim().toUpperCase();
  const replaced = source.replace(/[^A-Z0-9_]+/g, "_").replace(/_+/g, "_");
  if (!replaced) return "";
  if (/^[A-Z_]/.test(replaced)) return replaced;
  return `_${replaced}`;
}

function createVarRow(prefix, key = "", value = "") {
  const row = makeElem("div", "env-var-row");

  const keyLabel = makeElem("label", "field");
  const keyText = makeElem("span", "", "Var key");
  const keyInput = makeElem("input");
  keyInput.className = "env-var-key";
  keyInput.placeholder = "LOG_LEVEL";
  keyInput.value = key;
  keyInput.addEventListener("blur", () => {
    keyInput.value = sanitizeVarKey(keyInput.value);
  });
  keyLabel.append(keyText, keyInput);

  const valueLabel = makeElem("label", "field");
  const valueText = makeElem("span", "", "Value");
  const valueInput = makeElem("input");
  valueInput.className = "env-var-value";
  valueInput.placeholder = "info";
  valueInput.value = value;
  valueLabel.append(valueText, valueInput);

  const cleanButton = makeElem("button", "btn btn-subtle", "Clean key");
  cleanButton.type = "button";
  cleanButton.addEventListener("click", () => {
    keyInput.value = sanitizeVarKey(keyInput.value);
  });

  const removeButton = makeElem("button", "btn btn-subtle", "Remove");
  removeButton.type = "button";
  removeButton.addEventListener("click", () => {
    row.remove();
    syncEnvEditorEmptyState(prefix);
  });

  row.append(keyLabel, valueLabel, cleanButton, removeButton);
  return row;
}

function createEnvironmentCard(prefix, name = "", vars = {}) {
  const card = makeElem("article", "env-card");

  const head = makeElem("div", "env-card-head");
  const nameLabel = makeElem("label", "field");
  const nameText = makeElem("span", "", "Environment name");
  const nameInput = makeElem("input");
  nameInput.className = "env-name";
  nameInput.placeholder = "dev";
  nameInput.value = name;
  nameLabel.append(nameText, nameInput);

  const removeEnvButton = makeElem("button", "btn btn-subtle", "Remove environment");
  removeEnvButton.type = "button";
  removeEnvButton.addEventListener("click", () => {
    card.remove();
    syncEnvEditorEmptyState(prefix);
  });
  head.append(nameLabel, removeEnvButton);

  const varsList = makeElem("div", "env-vars-list");
  const entries = Object.entries(vars || {});
  for (const [key, value] of entries) {
    varsList.appendChild(createVarRow(prefix, key, String(value)));
  }

  const actions = makeElem("div", "env-card-actions");
  const addVarButton = makeElem("button", "btn btn-subtle", "Add variable");
  addVarButton.type = "button";
  addVarButton.addEventListener("click", () => {
    varsList.appendChild(createVarRow(prefix));
    syncEnvEditorEmptyState(prefix);
  });
  actions.appendChild(addVarButton);

  card.append(head, varsList, actions);
  return card;
}

function getEnvironmentCards(prefix) {
  const editor = dom.envEditors[prefix];
  return Array.from(editor.querySelectorAll(".env-card"));
}

function syncEnvEditorEmptyState(prefix) {
  const editor = dom.envEditors[prefix];
  const cards = getEnvironmentCards(prefix);
  const empty = editor.querySelector(".env-empty");
  if (!cards.length) {
    if (!empty) {
      editor.appendChild(
        makeElem("div", "env-empty", "No environments yet. Add one to define where your app runs.")
      );
    }
    return;
  }
  if (empty) {
    empty.remove();
  }
}

function addEnvironmentCard(prefix, name = "", vars = {}) {
  const editor = dom.envEditors[prefix];
  const empty = editor.querySelector(".env-empty");
  if (empty) {
    empty.remove();
  }
  editor.appendChild(createEnvironmentCard(prefix, name, vars));
}

function setEnvironmentsInEditor(prefix, environments) {
  const editor = dom.envEditors[prefix];
  editor.replaceChildren();
  const entries = Object.entries(environments || {});
  for (const [name, cfg] of entries) {
    addEnvironmentCard(prefix, name, cfg?.vars || {});
  }
  syncEnvEditorEmptyState(prefix);
}

function cleanVarKeysInEditor(prefix) {
  const editor = dom.envEditors[prefix];
  const keys = Array.from(editor.querySelectorAll(".env-var-key"));
  let changed = 0;
  for (const input of keys) {
    const cleaned = sanitizeVarKey(input.value);
    if (input.value !== cleaned) {
      input.value = cleaned;
      changed += 1;
    }
  }
  return changed;
}

function collectEnvironments(prefix, label) {
  const cards = getEnvironmentCards(prefix);
  if (!cards.length) {
    throw new Error(`${label} requires at least one environment`);
  }

  const environments = {};
  for (const card of cards) {
    const nameInput = card.querySelector(".env-name");
    const envName = String(nameInput?.value || "").trim();
    if (!envName) {
      throw new Error(`${label} has an environment without a name`);
    }
    if (environments[envName]) {
      throw new Error(`${label} contains duplicate environment name "${envName}"`);
    }

    const vars = {};
    const keyInputs = card.querySelectorAll(".env-var-key");
    const valueInputs = card.querySelectorAll(".env-var-value");

    for (let index = 0; index < keyInputs.length; index += 1) {
      const rawKey = keyInputs[index].value;
      const cleanedKey = sanitizeVarKey(rawKey);
      keyInputs[index].value = cleanedKey;

      const value = String(valueInputs[index]?.value || "");
      if (!cleanedKey && value === "") {
        continue;
      }
      if (!cleanedKey) {
        throw new Error(`${label} has a variable with empty key`);
      }
      if (!/^[A-Z_][A-Z0-9_]*$/.test(cleanedKey)) {
        throw new Error(`${label} has invalid variable key "${cleanedKey}"`);
      }
      vars[cleanedKey] = value;
    }

    environments[envName] = { vars };
  }
  return environments;
}

function workerOrderForKind(kind) {
  return kind === "ci" ? ciWorkerOrder : fullWorkerOrder;
}

function getSelectedProject() {
  if (!state.selectedProjectID) return null;
  return state.projects.find((project) => project.id === state.selectedProjectID) || null;
}

function buildCreateSpec() {
  return {
    apiVersion: "platform.example.com/v2",
    kind: "App",
    name: dom.inputs.createName.value.trim(),
    runtime: dom.inputs.createRuntime.value.trim(),
    capabilities: parseCapabilities(dom.inputs.createCapabilities.value),
    environments: collectEnvironments("create", "Create environments"),
    networkPolicies: {
      ingress: dom.inputs.createIngress.value,
      egress: dom.inputs.createEgress.value,
    },
  };
}

function buildUpdateSpec() {
  return {
    apiVersion: "platform.example.com/v2",
    kind: "App",
    name: dom.inputs.updateName.value.trim(),
    runtime: dom.inputs.updateRuntime.value.trim(),
    capabilities: parseCapabilities(dom.inputs.updateCapabilities.value),
    environments: collectEnvironments("update", "Update environments"),
    networkPolicies: {
      ingress: dom.inputs.updateIngress.value,
      egress: dom.inputs.updateEgress.value,
    },
  };
}

function buildWebhookPayload(projectID) {
  return {
    project_id: projectID,
    repo: dom.inputs.webhookRepo.value.trim(),
    branch: dom.inputs.webhookBranch.value.trim(),
    ref: dom.inputs.webhookRef.value.trim(),
    commit: dom.inputs.webhookCommit.value.trim(),
  };
}

function setCreateDefaults() {
  dom.inputs.createAPIVersion.value = "platform.example.com/v2";
  dom.inputs.createKind.value = "App";
  dom.inputs.createName.value = "";
  ensureRuntimeOption(dom.inputs.createRuntime, "go_1.26");
  dom.inputs.createCapabilities.value = "";
  dom.inputs.createIngress.value = "internal";
  dom.inputs.createEgress.value = "internal";
  setEnvironmentsInEditor("create", defaultEnvironments);
}

function setUpdateDefaults() {
  dom.inputs.updateAPIVersion.value = "platform.example.com/v2";
  dom.inputs.updateKind.value = "App";
  dom.inputs.updateName.value = "";
  ensureRuntimeOption(dom.inputs.updateRuntime, "go_1.26");
  dom.inputs.updateCapabilities.value = "";
  dom.inputs.updateIngress.value = "internal";
  dom.inputs.updateEgress.value = "internal";
  setEnvironmentsInEditor("update", defaultEnvironments);
}

function syncUpdateForm(project) {
  const spec = project?.spec || {};
  dom.inputs.updateAPIVersion.value = spec.apiVersion || "platform.example.com/v2";
  dom.inputs.updateKind.value = spec.kind || "App";
  dom.inputs.updateName.value = spec.name || "";
  ensureRuntimeOption(dom.inputs.updateRuntime, spec.runtime || "go_1.26");
  dom.inputs.updateCapabilities.value = Array.isArray(spec.capabilities) ? spec.capabilities.join(",") : "";
  dom.inputs.updateIngress.value = spec.networkPolicies?.ingress || "internal";
  dom.inputs.updateEgress.value = spec.networkPolicies?.egress || "internal";
  setEnvironmentsInEditor("update", spec.environments || defaultEnvironments);
}

function projectMatchesSearch(project, term) {
  if (!term) return true;
  const haystack = [
    project.spec?.name || "",
    project.id || "",
    project.spec?.runtime || "",
    formatRuntimeLiteral(project.spec?.runtime || ""),
    project.status?.phase || "",
  ]
    .join(" ")
    .toLowerCase();
  return haystack.includes(term.toLowerCase());
}

function getVisibleProjects() {
  const term = state.filters.search.trim().toLowerCase();
  const phase = state.filters.phase;

  const filtered = state.projects.filter((project) => {
    if (phase !== "all" && project.status?.phase !== phase) return false;
    return projectMatchesSearch(project, term);
  });

  const sortKey = state.filters.sort;
  filtered.sort((a, b) => {
    if (sortKey === "name_asc") {
      return (a.spec?.name || "").localeCompare(b.spec?.name || "", undefined, {
        sensitivity: "base",
      });
    }
    if (sortKey === "created_asc") {
      return dateValue(a.created_at) - dateValue(b.created_at);
    }
    return dateValue(b.updated_at) - dateValue(a.updated_at);
  });

  return filtered;
}

function resetArtifacts() {
  state.artifacts.loaded = false;
  state.artifacts.files = [];
  state.artifacts.selectedPath = "";
  state.artifacts.previewText = "";
  state.artifacts.previewMeta = "Preview unavailable";
  state.artifacts.previewIsBinary = false;
  state.artifacts.previewBytes = 0;
  state.artifacts.search = "";
  dom.inputs.artifactSearch.value = "";
  renderArtifactsPanel();
}

function stopOperationMonitor({ clearPayload = false } = {}) {
  if (state.operation.timer) {
    clearTimeout(state.operation.timer);
    state.operation.timer = null;
  }
  state.operation.token += 1;
  state.operation.failureCount = 0;
  state.operation.activeOpID = "";

  if (clearPayload) {
    state.operation.payload = null;
    renderOperationPanel();
  }
}

function clearSelection() {
  state.selectedProjectID = "";
  closeAllModals();
  setUpdateDefaults();
  stopOperationMonitor({ clearPayload: true });
  resetArtifacts();
  renderSelectionPanel();
  renderProjectsList();
  renderSystemStrip();
}

async function requestAPI(method, url, body) {
  const options = {
    method,
    headers: {},
  };

  if (body !== undefined) {
    options.headers["content-type"] = "application/json";
    options.body = JSON.stringify(body);
  }

  const response = await fetch(url, options);
  const contentType = response.headers.get("content-type") || "";

  let payload;
  if (contentType.includes("application/json")) {
    payload = await response.json();
  } else {
    payload = await response.text();
  }

  if (!response.ok) {
    const text = typeof payload === "string" ? payload : pretty(payload);
    throw new Error(`${method} ${url} -> ${response.status}: ${text}`);
  }

  return payload;
}

function renderEmptyState(container, message) {
  container.replaceChildren(makeElem("div", "empty-state", message));
}

function renderProjectsList() {
  const selected = getSelectedProject();
  const visible = getVisibleProjects();
  dom.text.projectStats.textContent = `${visible.length} visible of ${state.projects.length}`;

  dom.containers.projects.replaceChildren();
  if (!visible.length) {
    const msg = state.projects.length
      ? "No apps match these filters yet. Try clearing search or phase."
      : "You do not have any apps yet. Create your first app to get started.";
    renderEmptyState(dom.containers.projects, msg);
    return;
  }

  for (const project of visible) {
    const item = makeElem("article", "project-item");
    item.tabIndex = 0;
    item.setAttribute("role", "option");
    item.setAttribute("aria-selected", String(project.id === selected?.id));
    if (project.id === selected?.id) {
      item.classList.add("selected");
    }

    const titleRow = makeElem("div", "project-title-row");
    titleRow.append(
      makeElem("span", "project-title", project.spec?.name || "(unnamed)"),
      makeBadge(project.status?.phase || "Unknown", project.status?.phase || "unknown")
    );

    const runtimeMeta = makeElem(
      "p",
      "project-meta emphasis",
      `${formatRuntimeLiteral(project.spec?.runtime)} - updated ${elapsedSince(project.updated_at)}`
    );
    const idMeta = makeElem("p", "project-meta", `id ${project.id}`);
    const msgMeta = makeElem("p", "project-meta", project.status?.message || "no status message");

    item.append(titleRow, runtimeMeta, idMeta, msgMeta);

    item.addEventListener("click", () => {
      selectProject(project.id);
    });

    item.addEventListener("keydown", (event) => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        selectProject(project.id);
      }
    });

    dom.containers.projects.appendChild(item);
  }
}

function renderSelectionPanel() {
  const project = getSelectedProject();
  dom.text.selected.replaceChildren();

  const hasSelection = Boolean(project);
  dom.buttons.openDeleteModal.disabled = !hasSelection;
  dom.buttons.loadArtifacts.disabled = !hasSelection;
  dom.buttons.openUpdateModal.disabled = !hasSelection;
  dom.buttons.webhook.disabled = !hasSelection;

  if (!project) {
    dom.text.selected.classList.add("muted");
    dom.text.selected.textContent = "Select an app to view its configuration and release journey.";
    return;
  }

  dom.text.selected.classList.remove("muted");

  const row1 = makeElem("div", "project-summary-row");
  row1.append(
    makeElem("strong", "", project.spec?.name || "(unnamed)"),
    makeBadge(project.status?.phase || "Unknown", project.status?.phase || "unknown")
  );

  const row2 = makeElem("div", "project-summary-row");
  row2.append(
    makeElem("span", "project-meta emphasis", `ID ${project.id}`),
    makeElem("span", "project-meta emphasis", formatRuntimeLiteral(project.spec?.runtime))
  );

  const row3 = makeElem("div", "project-summary-row");
  row3.append(
    makeElem("span", "project-meta", `last op ${project.status?.last_op_kind || "none"}`),
    makeElem("span", "project-meta", `op id ${project.status?.last_op_id || "-"}`)
  );

  const row4 = makeElem("p", "project-meta", project.status?.message || "");

  dom.text.selected.append(row1, row2, row3, row4);

  if (state.ui.modal === "delete") {
    syncDeleteConfirmationState();
  }
}

function stepForWorker(op, workerName) {
  if (!op || !Array.isArray(op.steps)) return null;
  for (let idx = op.steps.length - 1; idx >= 0; idx -= 1) {
    if (op.steps[idx].worker === workerName) return op.steps[idx];
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

function isTerminalOperationStatus(status) {
  return status === "done" || status === "error";
}

function renderOperationProgress(op) {
  dom.containers.opProgress.replaceChildren();

  if (!op) {
    renderEmptyState(
      dom.containers.opProgress,
      "No active operation yet. Create or update an app to see step-by-step progress."
    );
    return;
  }

  const card = makeElem("div", "op-progress-card");
  const head = makeElem("div", "op-progress-head");
  const title = makeElem(
    "span",
    "op-progress-title",
    `op ${String(op.id || "").slice(0, 8)} - ${op.kind || "unknown"} - requested ${toLocalTime(op.requested)}`
  );
  const badge = makeBadge(op.status || "unknown", op.status || "unknown");
  head.append(title, badge);

  const order = workerOrderForKind(op.kind);
  let doneCount = 0;
  for (const workerName of order) {
    if (stepState(stepForWorker(op, workerName)) === "done") {
      doneCount += 1;
    }
  }

  let pct = Math.round((doneCount / Math.max(order.length, 1)) * 100);
  if (op.status === "running") pct = Math.max(12, pct);
  if (op.status === "error") pct = Math.max(25, pct);
  if (op.status === "done") pct = 100;

  const track = makeElem("div", "progress-track");
  const fill = makeElem("span", "progress-fill");
  if (op.status === "error") fill.classList.add("error");
  fill.style.width = `${pct}%`;
  track.appendChild(fill);

  const meta = makeElem(
    "div",
    "helper-text",
    `${doneCount}/${order.length} steps complete - total duration ${duration(op.requested, op.finished)}`
  );

  card.append(head, track, meta);
  dom.containers.opProgress.appendChild(card);
}

function renderOperationTimeline(op) {
  dom.containers.opTimeline.replaceChildren();

  if (!op) {
    renderEmptyState(dom.containers.opTimeline, "Pipeline timeline appears here once an app operation starts.");
    return;
  }

  const order = workerOrderForKind(op.kind);

  for (const workerName of order) {
    const step = stepForWorker(op, workerName);
    const stateName = stepState(step);

    const row = makeElem("article", `timeline-step timeline-step--${stateName}`);

    const head = makeElem("div", "timeline-step-head");
    const title = makeElem("span", "timeline-step-title", workerName);
    const badge = makeBadge(stateName, stateName);
    head.append(title, badge);

    const metaBits = [];
    if (!step) {
      metaBits.push("waiting for this worker");
    } else {
      metaBits.push(`started ${toLocalTime(step.started_at)}`);
      metaBits.push(`ended ${toLocalTime(step.ended_at)}`);
      metaBits.push(`duration ${duration(step.started_at, step.ended_at)}`);
      if (step.message) metaBits.push(step.message);
      if (step.error) metaBits.push(`error ${step.error}`);
    }

    const meta = makeElem("p", "timeline-step-meta", metaBits.join(" - "));
    row.append(head, meta);

    if (step && Array.isArray(step.artifacts) && step.artifacts.length) {
      const previewList = step.artifacts.slice(0, 4).join(", ");
      const artifactMeta = makeElem(
        "p",
        "timeline-step-artifacts",
        `artifacts ${step.artifacts.length}: ${previewList}${step.artifacts.length > 4 ? ", ..." : ""}`
      );
      row.appendChild(artifactMeta);
    }

    dom.containers.opTimeline.appendChild(row);
  }
}

function renderOperationInsights(op) {
  dom.containers.opInsights.replaceChildren();

  if (!op) {
    return;
  }

  const order = workerOrderForKind(op.kind).join(" -> ");
  const imageBuilderStep = stepForWorker(op, "imageBuilder");
  const imageArtifacts = Array.isArray(imageBuilderStep?.artifacts) ? imageBuilderStep.artifacts : [];
  const hasBuildKitMetadata = imageArtifacts.some((path) => buildKitArtifactSet.has(path));
  const hasDockerfile = imageArtifacts.includes("build/Dockerfile");
  const hasImageTag = imageArtifacts.includes("build/image.txt");

  const cards = [
    {
      label: "Operation",
      value: `${op.kind} ${op.status}`,
      meta: `requested ${toLocalTime(op.requested)} - finished ${toLocalTime(op.finished)}`,
    },
    {
      label: "Worker Path",
      value: order,
      meta: op.kind === "ci" ? "CI starts at imageBuilder." : "Registration runs full pipeline.",
    },
    {
      label: "imageBuilder Outputs",
      value: `${imageArtifacts.length} artifacts`,
      meta: `Dockerfile ${hasDockerfile ? "yes" : "no"} - image tag ${hasImageTag ? "yes" : "no"}`,
    },
    {
      label: "BuildKit Metadata",
      value: hasBuildKitMetadata ? "Present" : "Not in op step",
      meta: hasBuildKitMetadata
        ? "buildkit-summary, metadata, or log detected"
        : "load project artifacts to verify persisted files",
    },
  ];

  for (const info of cards) {
    const card = makeElem("article", "insight-card");
    card.append(
      makeElem("p", "insight-label", info.label),
      makeElem("p", "insight-value", info.value),
      makeElem("p", "insight-meta", info.meta)
    );
    dom.containers.opInsights.appendChild(card);
  }
}

function setRuntimeView(view) {
  if (view !== "timeline" && view !== "artifacts") return;
  state.ui.runtimeView = view;
  renderRuntimePanels();
}

function renderRuntimePanels() {
  const timelineActive = state.ui.runtimeView === "timeline";
  dom.runtime.timelinePanel.classList.toggle("is-hidden", !timelineActive);
  dom.runtime.artifactsPanel.classList.toggle("is-hidden", timelineActive);
  dom.runtime.timelineButton.classList.toggle("is-active", timelineActive);
  dom.runtime.artifactsButton.classList.toggle("is-active", !timelineActive);
  dom.runtime.timelineButton.setAttribute("aria-selected", String(timelineActive));
  dom.runtime.artifactsButton.setAttribute("aria-selected", String(!timelineActive));
}

function syncDeleteConfirmationState() {
  const project = getSelectedProject();
  const expected = (project?.spec?.name || project?.id || "").trim();
  const typed = String(dom.inputs.deleteConfirm.value || "").trim();
  const valid = Boolean(expected) && typed === expected;
  dom.buttons.deleteConfirm.disabled = !valid;
  dom.text.deleteConfirmHint.textContent = expected
    ? `Type "${expected}" exactly to enable deletion.`
    : "Select an app first.";
}

function openModal(modalName) {
  if (!dom.modals[modalName]) return;
  const project = getSelectedProject();
  if ((modalName === "update" || modalName === "delete") && !project) {
    setStatus("Select an app first", "warning");
    return;
  }

  if (modalName === "create") {
    setCreateDefaults();
  } else if (modalName === "update") {
    syncUpdateForm(project);
  } else if (modalName === "delete") {
    const appName = project?.spec?.name || project?.id || "";
    dom.text.deleteModalTarget.textContent = `Target app: ${appName}`;
    dom.inputs.deleteConfirm.value = "";
    syncDeleteConfirmationState();
  }

  for (const [key, modalEl] of Object.entries(dom.modals)) {
    modalEl.classList.toggle("is-hidden", key !== modalName);
  }
  state.ui.modal = modalName;

  const modalEl = dom.modals[modalName];
  const firstInput = modalEl.querySelector("input:not([type='hidden']), select, textarea");
  if (firstInput) {
    firstInput.focus();
  }
}

function closeModal(modalName) {
  if (!dom.modals[modalName]) return;
  dom.modals[modalName].classList.add("is-hidden");
  if (state.ui.modal === modalName) {
    state.ui.modal = "none";
  }
}

function closeAllModals() {
  for (const modalEl of Object.values(dom.modals)) {
    modalEl.classList.add("is-hidden");
  }
  state.ui.modal = "none";
}

function renderOperationPanel() {
  const op = state.operation.payload;
  renderOperationProgress(op);
  renderOperationTimeline(op);
  renderOperationInsights(op);
  dom.text.opRaw.textContent = op ? pretty(op) : "";
}

function artifactUrl(projectID, path) {
  return `/api/projects/${encodeURIComponent(projectID)}/artifacts/${encodeURIComponent(path).replaceAll("%2F", "/")}`;
}

function artifactKind(path) {
  if (path.startsWith("build/")) return "build";
  if (path.startsWith("deploy/")) return "deploy";
  if (path.startsWith("registration/")) return "registration";
  if (path.startsWith("repos/")) return "repo";
  return "file";
}

function filteredArtifactFiles() {
  const term = state.artifacts.search.trim().toLowerCase();
  if (!term) return state.artifacts.files;
  return state.artifacts.files.filter((path) => path.toLowerCase().includes(term));
}

function renderBuildKitSignal() {
  const signal = dom.text.buildkitSignal;

  if (!state.artifacts.loaded) {
    signal.className = "buildkit-signal muted";
    signal.textContent = "BuildKit metadata unavailable until artifacts are loaded.";
    return;
  }

  const present = [...buildKitArtifactSet].filter((file) => state.artifacts.files.includes(file));
  if (present.length === buildKitArtifactSet.size) {
    signal.className = "buildkit-signal present";
    signal.textContent = "BuildKit metadata found: build/buildkit-summary.txt, build/buildkit-metadata.json, build/buildkit.log";
    return;
  }

  const missing = [...buildKitArtifactSet].filter((file) => !state.artifacts.files.includes(file));
  signal.className = "buildkit-signal missing";
  signal.textContent = `BuildKit metadata missing: ${missing.join(", ")}`;
}

function renderArtifactsPanel() {
  const project = getSelectedProject();
  const filtered = filteredArtifactFiles();

  if (!project) {
    dom.text.artifactStats.textContent = "Select an app first.";
    dom.containers.artifacts.replaceChildren();
    renderEmptyState(dom.containers.artifacts, "Pick an app to explore its generated artifacts.");
    dom.text.artifactPreview.classList.add("muted");
    dom.text.artifactPreview.textContent = "Select an artifact to open a preview.";
    dom.text.artifactPreviewMeta.textContent = "Preview unavailable";
    dom.buttons.copyPreview.disabled = true;
    dom.text.buildkitSignal.className = "buildkit-signal muted";
    dom.text.buildkitSignal.textContent = "BuildKit metadata unavailable until artifacts are loaded.";
    return;
  }

  if (!state.artifacts.loaded) {
    dom.text.artifactStats.textContent = "Artifacts are ready to load";
  } else {
    dom.text.artifactStats.textContent = `${filtered.length} visible of ${state.artifacts.files.length}`;
  }

  renderBuildKitSignal();
  dom.containers.artifacts.replaceChildren();

  if (!state.artifacts.loaded) {
    renderEmptyState(dom.containers.artifacts, "Click Load to fetch this app's artifact inventory.");
  } else if (!filtered.length) {
    const message = state.artifacts.files.length
      ? "No artifacts match this filter yet."
      : "No artifacts are available for this app yet.";
    renderEmptyState(dom.containers.artifacts, message);
  } else {
    for (const path of filtered) {
      const row = makeElem("div", "artifact-row");
      if (path === state.artifacts.selectedPath) row.classList.add("selected");

      const link = makeElem("a", "artifact-link");
      link.href = artifactUrl(project.id, path);
      link.target = "_blank";

      const p1 = makeElem("span", "artifact-path", path);
      const p2 = makeElem("span", "artifact-kind", artifactKind(path));
      link.append(p1, p2);

      const previewButton = makeElem("button", "btn btn-subtle", "Preview");
      previewButton.type = "button";
      previewButton.addEventListener("click", async () => {
        setStatus(`Loading preview for ${path}`, "info");
        try {
          await previewArtifact(path);
          setStatus(`Preview loaded for ${path}`, "success");
        } catch (error) {
          setStatus(error.message, statusToneFromError(error));
        }
      });

      row.append(link, previewButton);
      dom.containers.artifacts.appendChild(row);
    }
  }

  dom.text.artifactPreviewMeta.textContent = state.artifacts.previewMeta;
  dom.text.artifactPreview.textContent = state.artifacts.previewText || "Select an artifact to open a preview.";
  dom.text.artifactPreview.classList.toggle("muted", !state.artifacts.previewText || state.artifacts.previewIsBinary);
  dom.buttons.copyPreview.disabled = !state.artifacts.previewText || state.artifacts.previewIsBinary;
}

function renderSystemStrip() {
  const selected = getSelectedProject();
  const readyCount = state.projects.filter((project) => project.status?.phase === "Ready").length;

  dom.text.systemProjectCount.textContent = String(state.projects.length);
  dom.text.systemReadyCount.textContent = `${readyCount} ready`;

  const op = state.operation.payload;
  if (op) {
    dom.text.systemActiveOp.textContent = `${op.kind} ${op.status}`;
    dom.text.systemActiveOpMeta.textContent = `${String(op.id || "").slice(0, 8)} - ${workerOrderForKind(op.kind).length} steps`;
  } else if (selected?.status?.last_op_kind) {
    dom.text.systemActiveOp.textContent = selected.status.last_op_kind;
    dom.text.systemActiveOpMeta.textContent = selected.status.last_op_id || "No op id";
  } else {
    dom.text.systemActiveOp.textContent = "None";
    dom.text.systemActiveOpMeta.textContent = "No operation selected";
  }

  const builderKnown = state.artifacts.loaded && [...buildKitArtifactSet].some((f) => state.artifacts.files.includes(f));
  dom.text.systemBuilderMode.textContent = builderKnown ? "buildkit" : "buildkit (default)";
  dom.text.systemBuilderMeta.textContent = state.artifacts.loaded
    ? builderKnown
      ? "BuildKit metadata artifacts detected"
      : "BuildKit files not found in this project"
    : "Metadata artifacts not loaded";

  const hasProjects = state.projects.length > 0;
  const hasErrors = state.projects.some((project) => project.status?.phase === "Error");
  if (!hasProjects) {
    dom.text.healthLabel.textContent = "Idle";
    dom.text.healthMeta.textContent = "No projects registered";
  } else if (hasErrors) {
    dom.text.healthLabel.textContent = "Attention";
    dom.text.healthMeta.textContent = "At least one project is in Error phase";
  } else {
    dom.text.healthLabel.textContent = "Operational";
    dom.text.healthMeta.textContent = "Registration and CI pathways available";
  }
}

function renderAll() {
  renderStatus();
  renderProjectsList();
  renderSelectionPanel();
  renderRuntimePanels();
  renderOperationPanel();
  renderArtifactsPanel();
  renderSystemStrip();
}

async function refreshProjects({ silent = false, preserveSelection = true } = {}) {
  const previousSelection = preserveSelection ? state.selectedProjectID : "";
  const projects = await requestAPI("GET", "/api/projects");
  state.projects = Array.isArray(projects) ? projects : [];

  if (previousSelection && !state.projects.some((project) => project.id === previousSelection)) {
    state.selectedProjectID = "";
    stopOperationMonitor({ clearPayload: true });
    resetArtifacts();
  } else if (!preserveSelection) {
    state.selectedProjectID = "";
  }

  renderProjectsList();
  renderSelectionPanel();
  renderSystemStrip();

  const selected = getSelectedProject();
  if (selected?.status?.last_op_id) {
    if (state.operation.activeOpID !== selected.status.last_op_id) {
      await startOperationMonitor(selected.status.last_op_id, { announce: false });
    }
  } else if (!selected) {
    stopOperationMonitor({ clearPayload: true });
  }

  if (!silent) {
    setStatus("Projects refreshed", "success");
  }
}

function selectProject(projectID) {
  if (projectID === state.selectedProjectID) {
    renderProjectsList();
    return;
  }

  state.selectedProjectID = projectID;
  resetArtifacts();

  const selected = getSelectedProject();
  syncUpdateForm(selected);

  if (!selected?.status?.last_op_id) {
    stopOperationMonitor({ clearPayload: true });
  } else if (state.operation.activeOpID !== selected.status.last_op_id) {
    void startOperationMonitor(selected.status.last_op_id, { announce: false });
  }

  renderSelectionPanel();
  renderProjectsList();
  renderSystemStrip();
  setStatus("");
}

async function startOperationMonitor(opID, { announce = true } = {}) {
  if (!opID) {
    stopOperationMonitor({ clearPayload: true });
    return;
  }

  if (state.operation.activeOpID === opID && state.operation.timer) {
    return;
  }

  stopOperationMonitor({ clearPayload: false });
  state.operation.activeOpID = opID;
  const token = state.operation.token;

  const poll = async () => {
    if (token !== state.operation.token) return;

    try {
      const op = await requestAPI("GET", `/api/ops/${encodeURIComponent(opID)}`);
      if (token !== state.operation.token) return;

      state.operation.payload = op;
      state.operation.failureCount = 0;
      renderOperationPanel();
      renderSystemStrip();

      if (isTerminalOperationStatus(op.status)) {
        state.operation.timer = null;
        if (announce) {
          const tone = op.status === "done" ? "success" : "error";
          setStatus(`Operation ${op.kind} finished with status ${op.status}`, tone);
        }

        try {
          await refreshProjects({ silent: true, preserveSelection: true });
        } catch (_error) {
          // Keep terminal operation visible even if refresh fails.
        }

        if (state.artifacts.loaded) {
          try {
            await loadArtifacts({ silent: true });
          } catch (_error) {
            // Artifact refresh failure should not break op visibility.
          }
        }
        return;
      }

      const delay = op.status === "running" ? 1200 : 1600;
      state.operation.timer = setTimeout(poll, delay);
    } catch (error) {
      if (token !== state.operation.token) return;

      state.operation.failureCount += 1;
      const backoff = Math.min(5000, 1500 + state.operation.failureCount * 700);
      setStatus(`Operation monitor warning: ${error.message}`, "warning");
      state.operation.timer = setTimeout(poll, backoff);
    }
  };

  await poll();
}

async function loadArtifacts({ silent = false } = {}) {
  const project = getSelectedProject();
  if (!project) {
    throw new Error("Select an app first");
  }

  const response = await requestAPI("GET", `/api/projects/${encodeURIComponent(project.id)}/artifacts`);
  const files = Array.isArray(response.files) ? response.files : [];
  state.artifacts.loaded = true;
  state.artifacts.files = [...files].sort((a, b) => a.localeCompare(b));

  if (!state.artifacts.files.includes(state.artifacts.selectedPath)) {
    state.artifacts.selectedPath = "";
    state.artifacts.previewText = "";
    state.artifacts.previewMeta = "Preview unavailable";
    state.artifacts.previewIsBinary = false;
    state.artifacts.previewBytes = 0;
  }

  renderArtifactsPanel();
  renderSystemStrip();

  if (!silent) {
    setStatus(`Loaded ${state.artifacts.files.length} artifacts`, "success");
  }
}

function isProbablyText(bytes) {
  if (!bytes.length) return true;

  const sample = bytes.subarray(0, Math.min(bytes.length, 512));
  let suspicious = 0;

  for (const value of sample) {
    if (value === 0) return false;
    const isControl = value < 32 && value !== 9 && value !== 10 && value !== 13;
    if (isControl) suspicious += 1;
  }

  return suspicious / sample.length < 0.08;
}

async function previewArtifact(path) {
  const project = getSelectedProject();
  if (!project) {
    throw new Error("Select an app first");
  }

  state.artifacts.selectedPath = path;
  state.artifacts.previewText = "Loading preview...";
  state.artifacts.previewMeta = path;
  state.artifacts.previewIsBinary = false;
  renderArtifactsPanel();

  const response = await fetch(artifactUrl(project.id, path));
  if (!response.ok) {
    const text = await response.text();
    throw new Error(`Preview failed (${response.status}): ${text}`);
  }

  const buffer = await response.arrayBuffer();
  const bytes = new Uint8Array(buffer);
  state.artifacts.previewBytes = bytes.length;

  if (!bytes.length) {
    state.artifacts.previewText = "(empty file)";
    state.artifacts.previewMeta = `${path} - empty`;
    state.artifacts.previewIsBinary = false;
    renderArtifactsPanel();
    return;
  }

  if (!isProbablyText(bytes)) {
    state.artifacts.previewText = `Binary file (${bytes.length} bytes). Download from the artifact link.`;
    state.artifacts.previewMeta = `${path} - binary`;
    state.artifacts.previewIsBinary = true;
    renderArtifactsPanel();
    return;
  }

  const maxBytes = 20000;
  const truncated = bytes.length > maxBytes;
  const sliced = bytes.subarray(0, maxBytes);
  const decoded = new TextDecoder("utf-8", { fatal: false }).decode(sliced);

  state.artifacts.previewText = truncated
    ? `${decoded}\n\n--- preview truncated at ${maxBytes} bytes ---`
    : decoded;
  state.artifacts.previewMeta = `${path} - ${bytes.length} bytes${truncated ? " (truncated preview)" : ""}`;
  state.artifacts.previewIsBinary = false;
  renderArtifactsPanel();
}

async function handleCreateSubmit(event) {
  event.preventDefault();
  setRuntimeView("timeline");
  setStatus("Creating project via registration API...", "info");

  try {
    const spec = buildCreateSpec();
    const response = await requestAPI("POST", "/api/events/registration", {
      action: "create",
      spec,
    });

    await refreshProjects({ silent: true, preserveSelection: true });

    if (response.project?.id) {
      selectProject(response.project.id);
    }

    if (response.op?.id) {
      await startOperationMonitor(response.op.id, { announce: true });
    }

    closeModal("create");
    setStatus("Project created", "success");
  } catch (error) {
    setStatus(error.message, statusToneFromError(error));
  }
}

async function handleUpdateSubmit(event) {
  event.preventDefault();
  setRuntimeView("timeline");
  const project = getSelectedProject();
  if (!project) {
    setStatus("Select an app first", "warning");
    return;
  }

  setStatus("Submitting update registration event...", "info");

  try {
    const spec = buildUpdateSpec();
    const response = await requestAPI("POST", "/api/events/registration", {
      action: "update",
      project_id: project.id,
      spec,
    });

    await refreshProjects({ silent: true, preserveSelection: true });

    if (response.project?.id) {
      selectProject(response.project.id);
    }

    if (response.op?.id) {
      await startOperationMonitor(response.op.id, { announce: true });
    }

    closeModal("update");
    setStatus("Project updated", "success");
  } catch (error) {
    setStatus(error.message, statusToneFromError(error));
  }
}

async function handleWebhookSubmit(event) {
  event.preventDefault();
  setRuntimeView("timeline");
  const project = getSelectedProject();
  if (!project) {
    setStatus("Select an app first", "warning");
    return;
  }

  setStatus("Triggering source webhook event...", "info");

  try {
    const payload = buildWebhookPayload(project.id);
    const response = await requestAPI("POST", "/api/webhooks/source", payload);

    if (!response.accepted) {
      setStatus(`Webhook ignored: ${response.reason || "not accepted"}`, "warning");
      return;
    }

    if (response.op?.id) {
      await startOperationMonitor(response.op.id, { announce: true });
    }

    await refreshProjects({ silent: true, preserveSelection: true });
    setStatus("CI operation accepted from source webhook", "success");
  } catch (error) {
    setStatus(error.message, statusToneFromError(error));
  }
}

async function handleDeleteConfirmSubmit(event) {
  event.preventDefault();
  const project = getSelectedProject();
  if (!project) {
    setStatus("Select an app first", "warning");
    return;
  }

  const expected = (project.spec?.name || project.id || "").trim();
  const typed = String(dom.inputs.deleteConfirm.value || "").trim();
  if (!expected || typed !== expected) {
    setStatus("Deletion confirmation does not match the app name", "warning");
    syncDeleteConfirmationState();
    return;
  }

  setStatus("Submitting delete registration event...", "warning");

  try {
    const response = await requestAPI("POST", "/api/events/registration", {
      action: "delete",
      project_id: project.id,
    });

    if (response.op) {
      state.operation.payload = response.op;
      renderOperationPanel();
    }

    closeModal("delete");
    clearSelection();
    await refreshProjects({ silent: true, preserveSelection: false });
    setStatus("Project deleted", "success");
  } catch (error) {
    setStatus(error.message, statusToneFromError(error));
  }
}

async function handleLoadArtifactsClick() {
  const project = getSelectedProject();
  if (!project) {
    setStatus("Select an app first", "warning");
    return;
  }

  setRuntimeView("artifacts");
  setStatus(`Loading artifacts for ${project.spec?.name || project.id}...`, "info");
  try {
    await loadArtifacts({ silent: false });
  } catch (error) {
    setStatus(error.message, statusToneFromError(error));
  }
}

async function handleCopyPreviewClick() {
  if (dom.buttons.copyPreview.disabled) return;
  try {
    await navigator.clipboard.writeText(state.artifacts.previewText);
    setStatus("Artifact preview copied to clipboard", "success");
  } catch (error) {
    setStatus(`Copy failed: ${error.message}`, "error");
  }
}

function bindEvents() {
  dom.buttons.openCreateModal.addEventListener("click", () => {
    openModal("create");
  });

  dom.buttons.openUpdateModal.addEventListener("click", () => {
    openModal("update");
  });

  dom.buttons.openDeleteModal.addEventListener("click", () => {
    openModal("delete");
  });

  dom.buttons.refresh.addEventListener("click", async () => {
    setStatus("Refreshing projects...", "info");
    try {
      await refreshProjects({ silent: false, preserveSelection: true });
    } catch (error) {
      setStatus(error.message, statusToneFromError(error));
    }
  });

  dom.forms.create.addEventListener("submit", (event) => {
    void handleCreateSubmit(event);
  });

  dom.forms.update.addEventListener("submit", (event) => {
    void handleUpdateSubmit(event);
  });

  dom.forms.webhook.addEventListener("submit", (event) => {
    void handleWebhookSubmit(event);
  });

  dom.forms.deleteConfirm.addEventListener("submit", (event) => {
    void handleDeleteConfirmSubmit(event);
  });

  dom.buttons.loadArtifacts.addEventListener("click", () => {
    void handleLoadArtifactsClick();
  });

  dom.buttons.copyPreview.addEventListener("click", () => {
    void handleCopyPreviewClick();
  });

  dom.buttons.createModalClose.addEventListener("click", () => {
    closeModal("create");
  });

  dom.buttons.createModalCancel.addEventListener("click", () => {
    closeModal("create");
  });

  dom.buttons.updateModalClose.addEventListener("click", () => {
    closeModal("update");
  });

  dom.buttons.updateModalCancel.addEventListener("click", () => {
    closeModal("update");
  });

  dom.buttons.deleteModalClose.addEventListener("click", () => {
    closeModal("delete");
  });

  dom.buttons.deleteModalCancel.addEventListener("click", () => {
    closeModal("delete");
  });

  dom.inputs.deleteConfirm.addEventListener("input", () => {
    syncDeleteConfirmationState();
  });

  dom.buttons.createAddEnv.addEventListener("click", () => {
    addEnvironmentCard("create");
    syncEnvEditorEmptyState("create");
  });

  dom.buttons.updateAddEnv.addEventListener("click", () => {
    addEnvironmentCard("update");
    syncEnvEditorEmptyState("update");
  });

  dom.buttons.createCleanKeys.addEventListener("click", () => {
    const changed = cleanVarKeysInEditor("create");
    setStatus(`Create form keys cleaned: ${changed}`, "success");
  });

  dom.buttons.updateCleanKeys.addEventListener("click", () => {
    const changed = cleanVarKeysInEditor("update");
    setStatus(`Update form keys cleaned: ${changed}`, "success");
  });

  dom.runtime.timelineButton.addEventListener("click", () => {
    setRuntimeView("timeline");
  });

  dom.runtime.artifactsButton.addEventListener("click", () => {
    setRuntimeView("artifacts");
  });

  dom.inputs.projectSearch.addEventListener("input", () => {
    state.filters.search = dom.inputs.projectSearch.value;
    renderProjectsList();
  });

  dom.inputs.phaseFilter.addEventListener("change", () => {
    state.filters.phase = dom.inputs.phaseFilter.value;
    renderProjectsList();
  });

  dom.inputs.projectSort.addEventListener("change", () => {
    state.filters.sort = dom.inputs.projectSort.value;
    renderProjectsList();
  });

  dom.inputs.artifactSearch.addEventListener("input", () => {
    state.artifacts.search = dom.inputs.artifactSearch.value;
    renderArtifactsPanel();
  });

  document.querySelectorAll("[data-modal-close]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const modalName = btn.getAttribute("data-modal-close");
      if (modalName) {
        closeModal(modalName);
      }
    });
  });

  document.addEventListener("keydown", (event) => {
    if (event.metaKey || event.ctrlKey || event.altKey) return;

    const tagName = String(event.target?.tagName || "").toLowerCase();
    const typing = tagName === "input" || tagName === "textarea" || event.target?.isContentEditable;

    const key = event.key.toLowerCase();
    if (key === "escape" && state.ui.modal !== "none") {
      event.preventDefault();
      closeAllModals();
      return;
    }

    if (!typing && event.key === "/") {
      event.preventDefault();
      dom.inputs.projectSearch.focus();
      dom.inputs.projectSearch.select();
      return;
    }

    if (typing) return;

    if (key === "r") {
      event.preventDefault();
      dom.buttons.refresh.click();
    }

    if (key === "a") {
      event.preventDefault();
      setRuntimeView("artifacts");
      dom.buttons.loadArtifacts.click();
    }
  });
}

async function init() {
  initRuntimePickers();
  setCreateDefaults();
  setUpdateDefaults();
  syncUpdateForm(null);

  dom.inputs.phaseFilter.value = state.filters.phase;
  dom.inputs.projectSort.value = state.filters.sort;

  bindEvents();
  renderAll();

  setStatus("Loading projects...", "info");
  try {
    await refreshProjects({ silent: true, preserveSelection: true });
    setStatus("", "info");
  } catch (error) {
    setStatus(error.message, statusToneFromError(error));
  }
}

void init();
