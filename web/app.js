const dom = {
  forms: {
    create: document.getElementById("createForm"),
    update: document.getElementById("updateForm"),
    webhook: document.getElementById("webhookForm"),
    deleteConfirm: document.getElementById("deleteConfirmForm"),
    promotion: document.getElementById("promotionForm"),
    promotionConfirm: document.getElementById("promotionConfirmForm"),
  },
  buttons: {
    openCreateModal: document.getElementById("openCreateModalBtn"),
    openCreateFromRail: document.getElementById("openCreateFromRailBtn"),
    openUpdateModal: document.getElementById("openUpdateModalBtn"),
    openDeleteModal: document.getElementById("openDeleteModalBtn"),
    refresh: document.getElementById("refreshBtn"),
    loadArtifacts: document.getElementById("loadArtifactsBtn"),
    copyPreview: document.getElementById("copyPreviewBtn"),
    deployDev: document.getElementById("deployDevBtn"),
    openPromotionModal: document.getElementById("openPromotionModalBtn"),
    journeyNextAction: document.getElementById("journeyNextActionBtn"),

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

    promotionModalClose: document.getElementById("promotionModalCloseBtn"),
    promotionModalCancel: document.getElementById("promotionModalCancelBtn"),
    promotionConfirm: document.getElementById("promotionConfirmBtn"),
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

    promotionFrom: document.getElementById("promotionFrom"),
    promotionTo: document.getElementById("promotionTo"),

    deleteConfirm: document.getElementById("deleteConfirmInput"),
    promotionConfirmInput: document.getElementById("promotionConfirmInput"),
  },
  text: {
    status: document.getElementById("appStatus"),

    healthLabel: document.getElementById("healthLabel"),
    healthMeta: document.getElementById("healthMeta"),
    systemProjectCount: document.getElementById("systemProjectCount"),
    systemReadyCount: document.getElementById("systemReadyCount"),
    systemActiveOp: document.getElementById("systemActiveOp"),
    systemActiveOpMeta: document.getElementById("systemActiveOpMeta"),
    systemBuilderMode: document.getElementById("systemBuilderMode"),
    systemBuilderMeta: document.getElementById("systemBuilderMeta"),

    projectStats: document.getElementById("projectStats"),
    selected: document.getElementById("selected"),
    journeyStatusLine: document.getElementById("journeyStatusLine"),

    deploySummary: document.getElementById("deploySummary"),
    deployGuardrail: document.getElementById("deployGuardrail"),
    promotionDraftSummary: document.getElementById("promotionDraftSummary"),
    promotionGuardrail: document.getElementById("promotionGuardrail"),

    artifactStats: document.getElementById("artifactStats"),
    buildkitSignal: document.getElementById("buildkitSignal"),
    artifactPreviewMeta: document.getElementById("artifactPreviewMeta"),
    artifactPreview: document.getElementById("artifactPreview"),

    deleteModalTarget: document.getElementById("deleteModalTarget"),
    deleteConfirmHint: document.getElementById("deleteConfirmHint"),
    promotionModalTitle: document.getElementById("promotionModalTitle"),
    promotionSummary: document.getElementById("promotionSummary"),
    promotionConfirmHint: document.getElementById("promotionConfirmHint"),

    opRaw: document.getElementById("lastOp"),
  },
  containers: {
    projects: document.getElementById("projects"),
    journeyMilestones: document.getElementById("journeyMilestones"),
    journeyNextAction: document.getElementById("journeyNextAction"),
    environmentMatrix: document.getElementById("environmentMatrix"),
    opProgress: document.getElementById("opProgress"),
    opTimeline: document.getElementById("opTimeline"),
    opErrorSurface: document.getElementById("opErrorSurface"),
    opHistory: document.getElementById("opHistory"),
    artifactQuickLinks: document.getElementById("artifactQuickLinks"),
    artifacts: document.getElementById("artifacts"),
    toastStack: document.getElementById("toastStack"),
  },
  envEditors: {
    create: document.getElementById("createEnvList"),
    update: document.getElementById("updateEnvList"),
  },
  modals: {
    create: document.getElementById("createModal"),
    update: document.getElementById("updateModal"),
    delete: document.getElementById("deleteModal"),
    promotion: document.getElementById("promotionModal"),
  },
};

dom.buttons.webhook = dom.forms.webhook.querySelector("button[type='submit']");

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
  staging: {
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

const buildKitArtifactSet = new Set([
  "build/buildkit-summary.txt",
  "build/buildkit-metadata.json",
  "build/buildkit.log",
]);

const preferredEnvOrder = ["dev", "staging", "prod"];

const workerOrderByKind = {
  create: ["registrar", "repoBootstrap", "imageBuilder", "manifestRenderer"],
  update: ["registrar", "repoBootstrap", "imageBuilder", "manifestRenderer"],
  delete: ["registrar", "repoBootstrap", "imageBuilder", "manifestRenderer"],
  ci: ["imageBuilder", "manifestRenderer"],
  deploy: ["deployer"],
  promote: ["promoter"],
  release: ["promoter"],
};

const workerLabelByName = {
  registrar: "Validate app setup",
  repoBootstrap: "Prepare app workspace",
  imageBuilder: "Build app image",
  manifestRenderer: "Prepare deployment manifests",
  deployer: "Deliver environment config",
  promoter: "Move release between environments",
};

const operationLabelByKind = {
  create: "Create app",
  update: "Update app",
  delete: "Delete app",
  ci: "Build app",
  deploy: "Deliver to dev",
  promote: "Promote environment",
  release: "Release to production",
};

const nextActionKindToTone = {
  build: "info",
  deploy_dev: "info",
  promote: "info",
  release: "warning",
  investigate: "error",
  none: "success",
};

const state = {
  projects: [],
  selectedProjectID: "",
  filters: {
    search: "",
    phase: "all",
    sort: "updated_desc",
  },
  status: {
    message: "",
    tone: "info",
  },
  artifacts: {
    loading: false,
    loaded: false,
    error: "",
    files: [],
    search: "",
    selectedPath: "",
    previewText: "",
    previewMeta: "Preview unavailable",
    previewIsBinary: false,
    previewBytes: 0,
    buildImageTag: "",
    envSnapshots: {},
    transitionEdges: [],
    textCache: {},
  },
  journey: {
    loading: false,
    error: "",
    data: null,
  },
  operation: {
    activeOpID: "",
    payload: null,
    timer: null,
    token: 0,
    failureCount: 0,
    history: [],
  },
  promotion: {
    fromEnv: "",
    toEnv: "",
    sourceImage: "",
    targetImage: "",
    reason: "",
    ready: false,
    action: "promote",
    confirmationPhrase: "",
  },
  ui: {
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

function elapsedSince(ts) {
  if (!hasRealTimestamp(ts)) return "-";
  const ms = Date.now() - new Date(ts).getTime();
  if (!Number.isFinite(ms) || ms < 0) return "-";
  if (ms < 1000) return `${Math.round(ms)}ms ago`;
  if (ms < 60000) return `${Math.round(ms / 1000)}s ago`;
  if (ms < 3600000) return `${Math.round(ms / 60000)}m ago`;
  return `${Math.round(ms / 3600000)}h ago`;
}

function duration(start, end) {
  if (!hasRealTimestamp(start) || !hasRealTimestamp(end)) return "-";
  const ms = new Date(end).getTime() - new Date(start).getTime();
  if (!Number.isFinite(ms) || ms < 0) return "-";
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
  return `${(ms / 60000).toFixed(1)}m`;
}

function statusToneFromError(error) {
  const msg = String(error?.message || error || "").toLowerCase();
  if (msg.includes("ignored")) return "warning";
  if (msg.includes("not found") || msg.includes("400")) return "warning";
  return "error";
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

function setStatus(message, tone = "info", { toast = false } = {}) {
  state.status.message = message || "";
  state.status.tone = tone;
  renderStatus();
  if (toast && message) {
    pushToast(message, tone);
  }
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

function pushToast(message, tone = "info") {
  const toast = makeElem("div", `toast tone-${tone}`, message);
  dom.containers.toastStack.appendChild(toast);

  const remove = () => {
    toast.classList.add("is-hidden");
    setTimeout(() => toast.remove(), 180);
  };

  setTimeout(remove, 4200);
  toast.addEventListener("click", remove);
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

function createEnvEditorCard(prefix, name = "", vars = {}) {
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
        makeElem(
          "div",
          "env-empty",
          "No environments yet. Add at least one so this app has a clear delivery path."
        )
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
  editor.appendChild(createEnvEditorCard(prefix, name, vars));
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
    const envName = String(nameInput?.value || "").trim().toLowerCase();
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

function getSelectedProject() {
  if (!state.selectedProjectID) return null;
  return state.projects.find((project) => project.id === state.selectedProjectID) || null;
}

function sortEnvironmentNames(names) {
  return [...names].sort((a, b) => {
    const ai = preferredEnvOrder.indexOf(a);
    const bi = preferredEnvOrder.indexOf(b);

    if (ai >= 0 && bi >= 0) return ai - bi;
    if (ai >= 0) return -1;
    if (bi >= 0) return 1;
    return a.localeCompare(b, undefined, { sensitivity: "base" });
  });
}

function projectEnvironmentNames(project) {
  if (!project) return [];
  const envs = new Set(["dev"]);

  const entries = Object.keys(project.spec?.environments || {});
  for (const env of entries) {
    const normalized = String(env || "").trim().toLowerCase();
    if (normalized) envs.add(normalized);
  }

  return sortEnvironmentNames([...envs]);
}

function isProductionEnvironmentName(env) {
  const name = String(env || "").trim().toLowerCase();
  return name === "prod" || name === "production";
}

function transitionActionForTarget(toEnv) {
  return isProductionEnvironmentName(toEnv) ? "release" : "promote";
}

function transitionVerb(action) {
  return action === "release" ? "release" : "promotion";
}

function transitionEndpoint(action) {
  return action === "release" ? "/api/events/release" : "/api/events/promotion";
}

function operationLabel(kind) {
  return operationLabelByKind[String(kind || "").trim()] || String(kind || "Activity");
}

function workerLabel(name) {
  return workerLabelByName[String(name || "").trim()] || String(name || "step");
}

function currentJourney() {
  return state.journey.data || null;
}

function projectMatchesSearch(project, term) {
  if (!term) return true;

  const envs = projectEnvironmentNames(project);
  const haystack = [
    project.spec?.name || "",
    project.id || "",
    project.spec?.runtime || "",
    formatRuntimeLiteral(project.spec?.runtime || ""),
    project.status?.phase || "",
    envs.join(" "),
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

function renderEmptyState(container, message) {
  container.replaceChildren(makeElem("div", "empty-state", message));
}

function renderProjectsList() {
  const selected = getSelectedProject();
  const visible = getVisibleProjects();

  dom.text.projectStats.textContent = `${visible.length} visible of ${state.projects.length}`;
  dom.containers.projects.replaceChildren();

  if (!visible.length) {
    const message = state.projects.length
      ? "No apps match current filters. Try broadening search or state."
      : "No apps created yet. Create your first app to start its delivery journey.";
    renderEmptyState(dom.containers.projects, message);
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

    const envCount = projectEnvironmentNames(project).length;

    const runtimeMeta = makeElem(
      "p",
      "project-meta emphasis",
      `${formatRuntimeLiteral(project.spec?.runtime)} • ${envCount} envs • updated ${elapsedSince(project.updated_at)}`
    );
    const idMeta = makeElem("p", "project-meta", `ID ${project.id}`);
    const msgMeta = makeElem("p", "project-meta", project.status?.message || "No recent update message");

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
  const hasSelection = Boolean(project);

  dom.buttons.openUpdateModal.disabled = !hasSelection;
  dom.buttons.openDeleteModal.disabled = !hasSelection;
  dom.buttons.loadArtifacts.disabled = !hasSelection;
  dom.buttons.webhook.disabled = !hasSelection;

  dom.text.selected.replaceChildren();

  if (!project) {
    dom.text.selected.classList.add("muted");
    dom.text.selected.textContent = "Select an app to review journey progress and run the next delivery step.";
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

  const envs = projectEnvironmentNames(project);
  const lastActivity = project.status?.last_op_kind ? operationLabel(project.status.last_op_kind) : "none";
  const row3 = makeElem("div", "project-summary-row");
  row3.append(
    makeElem("span", "project-meta", `Environments ${envs.join(", ")}`),
    makeElem("span", "project-meta", `Last activity ${lastActivity}`)
  );

  const row4 = makeElem(
    "p",
    "project-meta",
    project.status?.message || "App is ready. Continue with build, delivery, and environment moves below."
  );

  dom.text.selected.append(row1, row2, row3, row4);

  if (state.ui.modal === "delete") {
    syncDeleteConfirmationState();
  }
}

function workerOrderForKind(kind) {
  return workerOrderByKind[String(kind || "")] || [];
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

function upsertOperationHistory(op) {
  if (!op?.id) return;

  const step = Array.isArray(op.steps) && op.steps.length ? op.steps[op.steps.length - 1] : null;
  const summary = {
    id: op.id,
    kind: op.kind,
    status: op.status,
    requested: op.requested,
    finished: op.finished,
    error: op.error,
    message: step?.message || "",
  };

  const index = state.operation.history.findIndex((item) => item.id === op.id);
  if (index >= 0) {
    state.operation.history[index] = summary;
  } else {
    state.operation.history.unshift(summary);
  }

  state.operation.history.sort((a, b) => dateValue(b.requested) - dateValue(a.requested));
  state.operation.history = state.operation.history.slice(0, 12);
}

function deriveRecoveryHints(errorMessage) {
  const msg = String(errorMessage || "").toLowerCase();
  const hints = [];

  if (!msg) {
    return ["Retry after refreshing the selected app and its outputs."];
  }

  if (msg.includes("no build image found")) {
    hints.push("Run a build first so the app has an image ready to deliver.");
  }
  if (msg.includes("from_env") || msg.includes("to_env")) {
    hints.push("Verify source and target environments exist for this app and are different.");
  }
  if (msg.includes("deployment endpoint supports dev only")) {
    hints.push("Use direct delivery for dev, promotions for non-production, and release for production.");
  }
  if (msg.includes("timeout")) {
    hints.push("The action timed out. Retry and check recent activity details.");
  }
  if (msg.includes("not found")) {
    hints.push("Refresh the app list. The selected app may have been deleted or renamed.");
  }

  if (!hints.length) {
    hints.push("Review activity details and outputs, then retry the workflow.");
  }

  return hints;
}

function renderOperationProgress(op) {
  dom.containers.opProgress.replaceChildren();

  if (!op) {
    renderEmptyState(
      dom.containers.opProgress,
      "No active delivery activity. Run a journey step to see live progress."
    );
    return;
  }

  const order = workerOrderForKind(op.kind);

  let doneCount = 0;
  for (const workerName of order) {
    if (stepState(stepForWorker(op, workerName)) === "done") {
      doneCount += 1;
    }
  }

  let pct = order.length ? Math.round((doneCount / order.length) * 100) : 0;
  if (op.status === "running") pct = Math.max(12, pct);
  if (op.status === "error") pct = Math.max(20, pct);
  if (op.status === "done") pct = 100;

  const card = makeElem("div", "op-progress-card");
  const head = makeElem("div", "op-progress-head");
  head.append(
    makeElem(
      "span",
      "op-progress-title",
      `${operationLabel(op.kind)} • ${String(op.id || "").slice(0, 8)} • started ${toLocalTime(op.requested)}`
    ),
    makeBadge(op.status || "unknown", op.status || "unknown")
  );

  const track = makeElem("div", "progress-track");
  const fill = makeElem("span", "progress-fill");
  if (op.status === "error") fill.classList.add("error");
  fill.style.width = `${pct}%`;
  track.appendChild(fill);

  const meta = makeElem(
    "div",
    "helper-text",
    `${doneCount}/${order.length || 0} journey steps complete • duration ${duration(op.requested, op.finished)}`
  );

  card.append(head, track, meta);
  dom.containers.opProgress.appendChild(card);
}

function renderOperationErrorSurface(op) {
  dom.containers.opErrorSurface.replaceChildren();

  if (!op || op.status !== "error") {
    return;
  }

  const surface = makeElem("section", "recovery-surface");
  surface.appendChild(makeElem("p", "recovery-title", "Activity failed"));
  surface.appendChild(makeElem("p", "", op.error || "Unknown delivery failure"));

  const hints = deriveRecoveryHints(op.error);
  const list = makeElem("ul", "recovery-list");
  for (const hint of hints) {
    const item = makeElem("li", "", hint);
    list.appendChild(item);
  }
  surface.appendChild(list);

  dom.containers.opErrorSurface.appendChild(surface);
}

function renderOperationTimeline(op) {
  dom.containers.opTimeline.replaceChildren();

  if (!op) {
    renderEmptyState(dom.containers.opTimeline, "Activity steps appear when an app action starts.");
    return;
  }

  const order = workerOrderForKind(op.kind);

  if (!order.length) {
    renderEmptyState(dom.containers.opTimeline, "No known delivery path for this action.");
    return;
  }

  for (const workerName of order) {
    const step = stepForWorker(op, workerName);
    const stateName = stepState(step);

    const row = makeElem("article", `timeline-step timeline-step--${stateName}`);

    const head = makeElem("div", "timeline-step-head");
    head.append(makeElem("span", "timeline-step-title", workerLabel(workerName)), makeBadge(stateName, stateName));

    const bits = [];
    if (!step) {
      bits.push("waiting");
    } else {
      bits.push(`started ${toLocalTime(step.started_at)}`);
      bits.push(`ended ${toLocalTime(step.ended_at)}`);
      bits.push(`duration ${duration(step.started_at, step.ended_at)}`);
      if (step.message) bits.push(step.message);
      if (step.error) bits.push(`error ${step.error}`);
    }

    row.append(head, makeElem("p", "timeline-step-meta", bits.join(" • ")));

    if (step && Array.isArray(step.artifacts) && step.artifacts.length) {
      const artifactPreview = step.artifacts.slice(0, 4).join(", ");
      row.appendChild(
        makeElem(
          "p",
          "timeline-step-artifacts",
          `outputs ${step.artifacts.length}: ${artifactPreview}${step.artifacts.length > 4 ? ", ..." : ""}`
        )
      );
    }

    dom.containers.opTimeline.appendChild(row);
  }
}

function renderOperationHistory() {
  dom.containers.opHistory.replaceChildren();

  if (!state.operation.history.length) {
    renderEmptyState(dom.containers.opHistory, "Completed app activities will be listed here.");
    return;
  }

  for (const item of state.operation.history) {
    const card = makeElem("article", "history-item");

    const head = makeElem("div", "history-item-head");
    head.append(
      makeElem("strong", "", `${operationLabel(item.kind)} • ${String(item.id || "").slice(0, 8)}`),
      makeBadge(item.status || "unknown", item.status || "unknown")
    );

    const meta = makeElem(
      "p",
      "history-item-meta",
      `requested ${toLocalTime(item.requested)} • finished ${toLocalTime(item.finished)} • duration ${duration(
        item.requested,
        item.finished
      )}`
    );

    const detail = makeElem("p", "history-item-meta", item.error || item.message || "No detail message.");

    card.append(head, meta, detail);
    dom.containers.opHistory.appendChild(card);
  }
}

function renderOperationPanel() {
  const op = state.operation.payload;
  renderOperationProgress(op);
  renderOperationErrorSurface(op);
  renderOperationTimeline(op);
  renderOperationHistory();
  dom.text.opRaw.textContent = op ? pretty(op) : "";
}

function projectHasRunningOperation() {
  return Boolean(state.operation.payload && !isTerminalOperationStatus(state.operation.payload.status));
}

function artifactUrl(projectID, path) {
  return `/api/projects/${encodeURIComponent(projectID)}/artifacts/${encodeURIComponent(path).replaceAll("%2F", "/")}`;
}

function artifactKind(path) {
  if (path.startsWith("build/")) return "build";
  if (path.startsWith("deploy/")) return "deploy";
  if (path.startsWith("promotions/")) return "promotion";
  if (path.startsWith("releases/")) return "release";
  if (path.startsWith("repos/")) return "repo";
  if (path.startsWith("registration/")) return "registration";
  return "file";
}

function filteredArtifactFiles() {
  const term = state.artifacts.search.trim().toLowerCase();
  if (!term) return state.artifacts.files;
  return state.artifacts.files.filter((path) => path.toLowerCase().includes(term));
}

function parseTransitionEdges(files) {
  const edges = [];
  const seen = new Set();

  for (const path of files) {
    const promotionMatch = path.match(/^promotions\/([^/]+)-to-([^/]+)\//);
    const releaseMatch = path.match(/^releases\/([^/]+)-to-([^/]+)\//);
    const match = promotionMatch || releaseMatch;
    if (!match) {
      continue;
    }

    const from = match[1];
    const to = match[2];
    const action = releaseMatch ? "release" : "promote";
    const rootDir = releaseMatch ? "releases" : "promotions";
    const key = `${action}:${from}->${to}`;
    if (seen.has(key)) continue;

    seen.add(key);
    edges.push({
      from,
      to,
      action,
      key,
      renderedPath: `${rootDir}/${from}-to-${to}/rendered.yaml`,
    });
  }

  return edges;
}

function parseImageFromDeploymentManifest(rawText) {
  const text = String(rawText || "");
  for (const line of text.split("\n")) {
    const trimmed = line.trim();
    const cut = trimmed.match(/^image:\s*(.+)$/);
    if (cut && cut[1]) {
      return cut[1].trim();
    }
  }
  return "";
}

function resetArtifacts() {
  state.artifacts.loading = false;
  state.artifacts.loaded = false;
  state.artifacts.error = "";
  state.artifacts.files = [];
  state.artifacts.search = "";
  state.artifacts.selectedPath = "";
  state.artifacts.previewText = "";
  state.artifacts.previewMeta = "Preview unavailable";
  state.artifacts.previewIsBinary = false;
  state.artifacts.previewBytes = 0;
  state.artifacts.buildImageTag = "";
  state.artifacts.envSnapshots = {};
  state.artifacts.transitionEdges = [];
  state.artifacts.textCache = {};
  dom.inputs.artifactSearch.value = "";
}

function resetJourney() {
  state.journey.loading = false;
  state.journey.error = "";
  state.journey.data = null;
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

async function loadJourney({ silent = false } = {}) {
  const project = getSelectedProject();
  if (!project) {
    resetJourney();
    renderJourneyPanel();
    renderSystemStrip();
    return;
  }

  state.journey.loading = true;
  state.journey.error = "";
  renderJourneyPanel();

  try {
    const response = await requestAPI("GET", `/api/projects/${encodeURIComponent(project.id)}/journey`);
    state.journey.data = response?.journey || null;
    renderJourneyPanel();
    renderSystemStrip();
    if (!silent) {
      setStatus("Journey refreshed.", "success");
    }
  } catch (error) {
    state.journey.error = error.message;
    state.journey.data = null;
    renderJourneyPanel();
    renderSystemStrip();
    if (!silent) {
      throw error;
    }
  } finally {
    state.journey.loading = false;
    renderJourneyPanel();
  }
}

async function readArtifactText(path) {
  const project = getSelectedProject();
  if (!project || !path) return "";

  if (Object.prototype.hasOwnProperty.call(state.artifacts.textCache, path)) {
    return state.artifacts.textCache[path];
  }

  try {
    const response = await fetch(artifactUrl(project.id, path));
    if (!response.ok) {
      state.artifacts.textCache[path] = "";
      return "";
    }
    const text = await response.text();
    state.artifacts.textCache[path] = text;
    return text;
  } catch (_error) {
    state.artifacts.textCache[path] = "";
    return "";
  }
}

async function buildEnvironmentSnapshots() {
  const project = getSelectedProject();
  if (!project || !state.artifacts.loaded) {
    state.artifacts.envSnapshots = {};
    state.artifacts.transitionEdges = [];
    state.artifacts.buildImageTag = "";
    return;
  }

  const envs = projectEnvironmentNames(project);
  const fileSet = new Set(state.artifacts.files);
  const snapshots = {};

  const buildImagePath = "build/image.txt";
  if (fileSet.has(buildImagePath)) {
    state.artifacts.buildImageTag = String((await readArtifactText(buildImagePath)) || "").trim();
  } else {
    state.artifacts.buildImageTag = "";
  }

  const transitionEdges = parseTransitionEdges(state.artifacts.files);
  state.artifacts.transitionEdges = transitionEdges;

  for (const env of envs) {
    const deployDeploymentPath = `deploy/${env}/deployment.yaml`;
    const deployServicePath = `deploy/${env}/service.yaml`;
    const deployRenderedPath = `deploy/${env}/rendered.yaml`;
    const overlayImagePath = `repos/manifests/overlays/${env}/image.txt`;

    const snapshot = {
      env,
      hasDeployment: fileSet.has(deployDeploymentPath),
      hasService: fileSet.has(deployServicePath),
      hasRendered: fileSet.has(deployRenderedPath),
      deployDeploymentPath,
      deployServicePath,
      deployRenderedPath,
      overlayImagePath,
      hasOverlayImage: fileSet.has(overlayImagePath),
      imageTag: "",
      imageSource: "",
      transitionEvidence: transitionEdges.filter((edge) => edge.to === env),
    };

    if (snapshot.hasOverlayImage) {
      const overlayImage = String((await readArtifactText(overlayImagePath)) || "").trim();
      if (overlayImage) {
        snapshot.imageTag = overlayImage;
        snapshot.imageSource = "overlay marker";
      }
    }

    if (!snapshot.imageTag && snapshot.hasDeployment) {
      const deploymentText = await readArtifactText(deployDeploymentPath);
      const imageFromManifest = parseImageFromDeploymentManifest(deploymentText);
      if (imageFromManifest) {
        snapshot.imageTag = imageFromManifest;
        snapshot.imageSource = "rendered manifest";
      }
    }

    if (snapshot.hasRendered && snapshot.imageTag) {
      snapshot.state = "done";
    } else if (snapshot.hasRendered || snapshot.imageTag) {
      snapshot.state = "running";
    } else {
      snapshot.state = "pending";
    }

    snapshots[env] = snapshot;
  }

  state.artifacts.envSnapshots = snapshots;
}

function createEnvironmentSnapshotCard(snapshot) {
  const card = makeElem("article", "environment-card");
  card.dataset.env = snapshot.env;
  const transitionEvidence = Array.isArray(snapshot?.transitionEvidence) ? snapshot.transitionEvidence : [];

  const head = makeElem("div", "environment-head");
  head.append(
    makeElem("span", "environment-name", snapshot.env),
    makeBadge(
      snapshot.state === "done"
        ? "live"
        : snapshot.state === "running"
          ? "in progress"
          : "not delivered",
      snapshot.state
    )
  );

  const meta = makeElem("div", "environment-meta");
  meta.append(
    makeElem("span", "", `Image ${snapshot.imageTag || "unknown"}`),
    makeElem("span", "", `Image source ${snapshot.imageSource || "not available"}`),
    makeElem("span", "", `Delivery config rendered ${snapshot.hasRendered ? "yes" : "no"}`)
  );

  if (transitionEvidence.length) {
    const label = transitionEvidence.map((edge) => `${edge.action} ${edge.from}→${edge.to}`).join(", ");
    meta.appendChild(makeElem("span", "", `Environment moves ${label}`));
  } else {
    meta.appendChild(makeElem("span", "", "No environment moves yet"));
  }

  const links = makeElem("div", "environment-links");

  const maybeLink = (path, label) => {
    if (!state.artifacts.files.includes(path)) return;

    const anchor = makeElem("a", "link-chip", label);
    anchor.href = artifactUrl(getSelectedProject().id, path);
    anchor.target = "_blank";
    anchor.rel = "noopener";
    links.appendChild(anchor);
  };

  maybeLink(snapshot.deployRenderedPath, "rendered config");
  maybeLink(snapshot.deployDeploymentPath, "deployment");
  maybeLink(snapshot.deployServicePath, "service");
  maybeLink(snapshot.overlayImagePath, "image marker");

  if (!links.childElementCount) {
    links.appendChild(makeElem("span", "helper-text", "No environment outputs yet"));
  }

  card.append(head, meta, links);
  return card;
}

function renderEnvironmentMatrix() {
  const project = getSelectedProject();
  const container = dom.containers.environmentMatrix;
  container.replaceChildren();

  if (!project) {
    renderEmptyState(container, "Select an app to inspect environment outcomes.");
    return;
  }

  if (state.artifacts.loading && !state.artifacts.loaded) {
    renderEmptyState(container, "Loading outputs and deriving environment state...");
    return;
  }

  if (state.artifacts.error && !state.artifacts.loaded) {
    const wrap = makeElem("div", "empty-state");
    wrap.append(
      makeElem("p", "", `Environment data unavailable: ${state.artifacts.error}`),
      makeElem("p", "helper-text", "You can still run delivery steps. The API validates requests server-side.")
    );
    container.appendChild(wrap);
    return;
  }

  const envs = projectEnvironmentNames(project);

  if (!state.artifacts.loaded) {
    for (const env of envs) {
      const placeholder = {
        env,
        state: "pending",
        imageTag: "unknown until outputs load",
        imageSource: "",
        hasRendered: false,
        hasDeployment: false,
        hasService: false,
        deployRenderedPath: `deploy/${env}/rendered.yaml`,
        deployDeploymentPath: `deploy/${env}/deployment.yaml`,
        deployServicePath: `deploy/${env}/service.yaml`,
        overlayImagePath: `repos/manifests/overlays/${env}/image.txt`,
        transitionEvidence: [],
      };
      container.appendChild(createEnvironmentSnapshotCard(placeholder));
    }
    return;
  }

  const snapshots = state.artifacts.envSnapshots;
  for (const env of envs) {
    const snapshot = snapshots[env] || {
      env,
      state: "pending",
      imageTag: "",
      imageSource: "",
      hasRendered: false,
      hasDeployment: false,
      hasService: false,
      deployRenderedPath: `deploy/${env}/rendered.yaml`,
      deployDeploymentPath: `deploy/${env}/deployment.yaml`,
      deployServicePath: `deploy/${env}/service.yaml`,
      overlayImagePath: `repos/manifests/overlays/${env}/image.txt`,
      transitionEvidence: [],
    };

    container.appendChild(createEnvironmentSnapshotCard(snapshot));
  }
}

function deployGuardrailState() {
  const project = getSelectedProject();
  if (!project) {
    return {
      disabled: true,
      message: "Select an app first.",
      summary: "Choose an app to inspect build readiness before delivering.",
    };
  }

  if (projectHasRunningOperation()) {
    return {
      disabled: true,
      message: "Wait for current activity to finish before starting delivery.",
      summary: "Another app activity is currently running.",
    };
  }

  if (state.artifacts.loaded) {
    if (!state.artifacts.buildImageTag) {
      return {
        disabled: true,
        message: "No build image found. Run a source build before delivering.",
        summary: "Dev delivery requires build/image.txt.",
      };
    }

    return {
      disabled: false,
      message: "Dev delivery is ready.",
      summary: `Dev will receive image ${state.artifacts.buildImageTag}.`,
    };
  }

  if (state.artifacts.error) {
    return {
      disabled: false,
      message: "Output state unavailable. Delivery is still allowed; API validates readiness.",
      summary: "Readiness preview unavailable due to output load error.",
    };
  }

  return {
    disabled: false,
    message: "Load outputs to see exact image before delivering.",
    summary: "Dev delivery only targets the dev environment.",
  };
}

function ensurePromotionSelections(project) {
  if (!project) {
    state.promotion.fromEnv = "";
    state.promotion.toEnv = "";
    dom.inputs.promotionFrom.replaceChildren();
    dom.inputs.promotionTo.replaceChildren();
    return;
  }

  const envs = projectEnvironmentNames(project);

  const addOption = (selectEl, value) => {
    const option = document.createElement("option");
    option.value = value;
    option.textContent = value;
    selectEl.appendChild(option);
  };

  dom.inputs.promotionFrom.replaceChildren();
  dom.inputs.promotionTo.replaceChildren();

  for (const env of envs) {
    addOption(dom.inputs.promotionFrom, env);
    addOption(dom.inputs.promotionTo, env);
  }

  if (!envs.includes(state.promotion.fromEnv)) {
    state.promotion.fromEnv = envs.includes("dev") ? "dev" : envs[0] || "";
  }

  if (!envs.includes(state.promotion.toEnv) || state.promotion.toEnv === state.promotion.fromEnv) {
    const preferred = envs.find((env) => env === "staging" || env === "prod");
    state.promotion.toEnv = preferred || envs.find((env) => env !== state.promotion.fromEnv) || "";
  }

  dom.inputs.promotionFrom.value = state.promotion.fromEnv;
  dom.inputs.promotionTo.value = state.promotion.toEnv;
}

function promotionValidation(project, fromEnv, toEnv) {
  const action = transitionActionForTarget(toEnv);

  if (!project) {
    return { valid: false, reason: "Select an app first.", sourceImage: "", targetImage: "", action };
  }

  if (projectHasRunningOperation()) {
    return {
      valid: false,
      reason: `Wait for current activity to finish before ${transitionVerb(action)}.`,
      sourceImage: "",
      targetImage: "",
      action,
    };
  }

  const envs = projectEnvironmentNames(project);
  if (!fromEnv || !toEnv) {
    return {
      valid: false,
      reason: "Choose both source and target environments.",
      sourceImage: "",
      targetImage: "",
      action,
    };
  }

  if (fromEnv === toEnv) {
    return {
      valid: false,
      reason: "Source and target environments must differ.",
      sourceImage: "",
      targetImage: "",
      action,
    };
  }

  if (!envs.includes(fromEnv) || !envs.includes(toEnv)) {
    return {
      valid: false,
      reason: "Selected environments are not defined for this app.",
      sourceImage: "",
      targetImage: "",
      action,
    };
  }

  if (!state.artifacts.loaded) {
    return {
      valid: true,
      reason: `Load outputs to verify image before confirming this ${transitionVerb(action)}.`,
      sourceImage: "unknown (outputs not loaded)",
      targetImage: "unknown",
      warning: true,
      action,
    };
  }

  const sourceSnapshot = state.artifacts.envSnapshots[fromEnv];
  const targetSnapshot = state.artifacts.envSnapshots[toEnv];
  const sourceImage = sourceSnapshot?.imageTag || "";
  const targetImage = targetSnapshot?.imageTag || "not deployed";

  if (!sourceImage) {
    return {
      valid: false,
      reason: `No delivered image found in ${fromEnv}. Deliver or move that source first.`,
      sourceImage: "",
      targetImage,
      action,
    };
  }

  return {
    valid: true,
    reason: `${transitionVerb(action)} is ready for confirmation.`,
    sourceImage,
    targetImage,
    warning: false,
    action,
  };
}

function renderDeployPanel() {
  const guardrail = deployGuardrailState();
  dom.buttons.deployDev.disabled = guardrail.disabled;
  dom.text.deploySummary.textContent = guardrail.summary;
  dom.text.deployGuardrail.textContent = guardrail.message;
}

function renderPromotionPanel() {
  const project = getSelectedProject();
  ensurePromotionSelections(project);

  const fromEnv = dom.inputs.promotionFrom.value;
  const toEnv = dom.inputs.promotionTo.value;

  state.promotion.fromEnv = fromEnv;
  state.promotion.toEnv = toEnv;

  const validation = promotionValidation(project, fromEnv, toEnv);
  state.promotion.sourceImage = validation.sourceImage || "";
  state.promotion.targetImage = validation.targetImage || "";
  state.promotion.reason = validation.reason;
  state.promotion.ready = Boolean(validation.valid);
  state.promotion.action = validation.action || "promote";
  const actionLabel = state.promotion.action === "release" ? "Release" : "Promotion";

  dom.text.promotionDraftSummary.textContent = project
    ? `${actionLabel}: source ${fromEnv || "-"} (${validation.sourceImage || "unknown"}) -> target ${toEnv || "-"} (${validation.targetImage || "unknown"}).`
    : "Select an app to configure environment moves.";

  dom.text.promotionGuardrail.textContent = validation.reason;
  dom.buttons.openPromotionModal.textContent =
    state.promotion.action === "release" ? "Review release" : "Review promotion";
  dom.buttons.openPromotionModal.disabled = !validation.valid;
}

function renderActionPanels() {
  renderDeployPanel();
  renderPromotionPanel();
}

function journeyStatusLabel(status) {
  switch (status) {
    case "in_progress":
      return "in progress";
    case "complete":
      return "complete";
    case "blocked":
      return "blocked";
    case "failed":
      return "failed";
    default:
      return "pending";
  }
}

function renderJourneyPanel() {
  const project = getSelectedProject();
  const milestoneContainer = dom.containers.journeyMilestones;
  const nextActionCard = dom.containers.journeyNextAction;
  const nextActionButton = dom.buttons.journeyNextAction;

  milestoneContainer.replaceChildren();
  nextActionCard.replaceChildren();

  if (!project) {
    dom.text.journeyStatusLine.textContent = "Select an app to load journey milestones.";
    nextActionCard.textContent = "No app selected.";
    nextActionButton.disabled = true;
    nextActionButton.textContent = "Run suggested step";
    renderEmptyState(milestoneContainer, "Milestones appear after you select an app.");
    return;
  }

  if (state.journey.loading) {
    dom.text.journeyStatusLine.textContent = "Loading journey snapshot...";
    nextActionCard.textContent = "Preparing suggested next step...";
    nextActionButton.disabled = true;
    nextActionButton.textContent = "Run suggested step";
    renderEmptyState(milestoneContainer, "Loading milestones...");
    return;
  }

  if (state.journey.error) {
    dom.text.journeyStatusLine.textContent = "Journey snapshot unavailable.";
    nextActionCard.textContent = state.journey.error;
    nextActionButton.disabled = true;
    nextActionButton.textContent = "Run suggested step";
    renderEmptyState(milestoneContainer, "Journey data could not be loaded.");
    return;
  }

  const journey = currentJourney();
  if (!journey) {
    dom.text.journeyStatusLine.textContent = "Journey data is not available yet.";
    nextActionCard.textContent = "Refresh to retry.";
    nextActionButton.disabled = true;
    nextActionButton.textContent = "Run suggested step";
    renderEmptyState(milestoneContainer, "No milestones to show yet.");
    return;
  }

  dom.text.journeyStatusLine.textContent = journey.summary || "Journey status is available.";

  const nextAction = journey.next_action || { kind: "none", label: "No suggested action", detail: "" };
  nextActionCard.textContent = `${nextAction.label || "No suggested action"}: ${nextAction.detail || "No action needed."}`;
  nextActionButton.dataset.actionKind = nextAction.kind || "none";
  nextActionButton.dataset.fromEnv = nextAction.from_env || "";
  nextActionButton.dataset.toEnv = nextAction.to_env || "";
  nextActionButton.textContent = nextAction.label || "Run suggested step";
  nextActionButton.disabled = !["build", "deploy_dev", "promote", "release"].includes(nextAction.kind);

  const milestones = Array.isArray(journey.milestones) ? journey.milestones : [];
  if (!milestones.length) {
    renderEmptyState(milestoneContainer, "No milestones available.");
    return;
  }

  for (const milestone of milestones) {
    const stateName = String(milestone.status || "pending");
    const stateClass =
      stateName === "in_progress"
        ? "running"
        : stateName === "blocked"
          ? "warning"
          : stateName === "complete"
            ? "done"
            : stateName === "failed"
              ? "error"
              : "pending";

    const row = makeElem("article", `timeline-step timeline-step--${stateClass}`);
    const head = makeElem("div", "timeline-step-head");
    head.append(
      makeElem("span", "timeline-step-title", milestone.title || milestone.id || "Milestone"),
      makeBadge(journeyStatusLabel(stateName), stateClass)
    );
    row.append(head, makeElem("p", "timeline-step-meta", milestone.detail || ""));
    milestoneContainer.appendChild(row);
  }
}

function renderBuildKitSignal() {
  const signal = dom.text.buildkitSignal;

  if (!state.artifacts.loaded) {
    signal.className = "buildkit-signal muted";
    signal.textContent = "Output insight unavailable until outputs are loaded.";
    return;
  }

  const present = [...buildKitArtifactSet].filter((file) => state.artifacts.files.includes(file));
  if (present.length === buildKitArtifactSet.size) {
    signal.className = "buildkit-signal present";
    signal.textContent = "Detailed build evidence is present (summary, metadata, and log).";
    return;
  }

  const missing = [...buildKitArtifactSet].filter((file) => !state.artifacts.files.includes(file));
  signal.className = "buildkit-signal missing";
  signal.textContent = `Some detailed build evidence is missing: ${missing.join(", ")}`;
}

function renderArtifactQuickLinks() {
  const project = getSelectedProject();
  const container = dom.containers.artifactQuickLinks;
  container.replaceChildren();

  if (!project || !state.artifacts.loaded) return;

  const links = [];

  if (state.artifacts.files.includes("build/image.txt")) {
    links.push({ label: "build image", path: "build/image.txt" });
  }

  for (const env of projectEnvironmentNames(project)) {
    const rendered = `deploy/${env}/rendered.yaml`;
    if (state.artifacts.files.includes(rendered)) {
      links.push({ label: `${env} rendered`, path: rendered });
    }
  }

  for (const edge of state.artifacts.transitionEdges.slice(0, 6)) {
    if (state.artifacts.files.includes(edge.renderedPath)) {
      links.push({ label: `${edge.action} ${edge.from}->${edge.to}`, path: edge.renderedPath });
    }
  }

  if (!links.length) {
    container.appendChild(makeElem("span", "helper-text", "No quick links yet."));
    return;
  }

  for (const linkInfo of links.slice(0, 10)) {
    const anchor = makeElem("a", "link-chip", linkInfo.label);
    anchor.href = artifactUrl(project.id, linkInfo.path);
    anchor.target = "_blank";
    anchor.rel = "noopener";
    container.appendChild(anchor);
  }
}

function renderArtifactsPanel() {
  const project = getSelectedProject();
  const filtered = filteredArtifactFiles();

  if (!project) {
    dom.text.artifactStats.textContent = "Select an app first.";
    dom.containers.artifactQuickLinks.replaceChildren();
    dom.containers.artifacts.replaceChildren();
    renderEmptyState(dom.containers.artifacts, "Pick an app to inspect outputs.");
    dom.text.artifactPreview.classList.add("muted");
    dom.text.artifactPreview.textContent = "Select an output to preview.";
    dom.text.artifactPreviewMeta.textContent = "Preview unavailable";
    dom.buttons.copyPreview.disabled = true;
    dom.text.buildkitSignal.className = "buildkit-signal muted";
    dom.text.buildkitSignal.textContent = "Output insight unavailable until outputs are loaded.";
    return;
  }

  if (state.artifacts.loading && !state.artifacts.loaded) {
    dom.text.artifactStats.textContent = "Loading outputs...";
  } else if (!state.artifacts.loaded) {
    dom.text.artifactStats.textContent = "Outputs ready to load";
  } else {
    dom.text.artifactStats.textContent = `${filtered.length} visible of ${state.artifacts.files.length}`;
  }

  renderBuildKitSignal();
  renderArtifactQuickLinks();

  dom.containers.artifacts.replaceChildren();

  if (state.artifacts.error && !state.artifacts.loaded) {
    renderEmptyState(dom.containers.artifacts, `Output load failed: ${state.artifacts.error}`);
  } else if (!state.artifacts.loaded) {
    renderEmptyState(dom.containers.artifacts, "Load outputs to inspect delivery results.");
  } else if (!filtered.length) {
    const message = state.artifacts.files.length
      ? "No outputs match this filter."
      : "No outputs are available for this app yet.";
    renderEmptyState(dom.containers.artifacts, message);
  } else {
    for (const path of filtered) {
      const row = makeElem("div", "artifact-row");
      if (path === state.artifacts.selectedPath) row.classList.add("selected");

      const link = makeElem("a", "artifact-link");
      link.href = artifactUrl(project.id, path);
      link.target = "_blank";
      link.rel = "noopener";

      link.append(makeElem("span", "artifact-path", path), makeElem("span", "artifact-kind", artifactKind(path)));

      const previewButton = makeElem("button", "btn btn-subtle", "Preview");
      previewButton.type = "button";
      previewButton.addEventListener("click", async () => {
        setStatus(`Loading preview for ${path}`, "info");
        try {
          await previewArtifact(path);
          setStatus(`Preview loaded for ${path}`, "success");
        } catch (error) {
          setStatus(error.message, statusToneFromError(error), { toast: true });
        }
      });

      row.append(link, previewButton);
      dom.containers.artifacts.appendChild(row);
    }
  }

  dom.text.artifactPreviewMeta.textContent = state.artifacts.previewMeta;
  dom.text.artifactPreview.textContent = state.artifacts.previewText || "Select an output to preview.";
  dom.text.artifactPreview.classList.toggle("muted", !state.artifacts.previewText || state.artifacts.previewIsBinary);
  dom.buttons.copyPreview.disabled = !state.artifacts.previewText || state.artifacts.previewIsBinary;
}

function renderSystemStrip() {
  const selected = getSelectedProject();
  const readyCount = state.projects.filter((project) => project.status?.phase === "Ready").length;

  dom.text.systemProjectCount.textContent = String(state.projects.length);
  dom.text.systemReadyCount.textContent = `${readyCount} delivery-ready`;

  const op = state.operation.payload;
  if (op) {
    dom.text.systemActiveOp.textContent = operationLabel(op.kind);
    dom.text.systemActiveOpMeta.textContent = `${op.status || "pending"} • ${String(op.id || "").slice(0, 8)}`;
  } else if (selected?.status?.last_op_kind) {
    dom.text.systemActiveOp.textContent = operationLabel(selected.status.last_op_kind);
    dom.text.systemActiveOpMeta.textContent = selected.status.last_op_id || "No activity id";
  } else {
    dom.text.systemActiveOp.textContent = "None selected";
    dom.text.systemActiveOpMeta.textContent = "Choose an app to focus";
  }

  const nextAction = currentJourney()?.next_action || null;
  if (nextAction) {
    dom.text.systemBuilderMode.textContent = nextAction.label || "No next action";
    dom.text.systemBuilderMeta.textContent = nextAction.detail || "No follow-up needed.";
  } else if (!selected) {
    dom.text.systemBuilderMode.textContent = "Waiting";
    dom.text.systemBuilderMeta.textContent = "Select an app to see guidance";
  } else {
    dom.text.systemBuilderMode.textContent = "Refresh needed";
    dom.text.systemBuilderMeta.textContent = "Load journey snapshot to see next best step";
  }

  const hasProjects = state.projects.length > 0;
  const hasErrors = state.projects.some((project) => project.status?.phase === "Error");

  if (!hasProjects) {
    dom.text.healthLabel.textContent = "Idle";
    dom.text.healthMeta.textContent = "No apps created yet";
  } else if (hasErrors) {
    dom.text.healthLabel.textContent = "Needs attention";
    dom.text.healthMeta.textContent = "One or more apps need recovery";
  } else {
    dom.text.healthLabel.textContent = "Healthy";
    dom.text.healthMeta.textContent = "App delivery journeys are progressing";
  }
}

function renderAll() {
  renderStatus();
  renderProjectsList();
  renderSelectionPanel();
  renderJourneyPanel();
  renderEnvironmentMatrix();
  renderActionPanels();
  renderOperationPanel();
  renderArtifactsPanel();
  renderSystemStrip();
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

function syncPromotionConfirmationState() {
  const expected = state.promotion.confirmationPhrase;
  const typed = String(dom.inputs.promotionConfirmInput.value || "").trim();
  const valid = Boolean(expected) && typed === expected;

  dom.buttons.promotionConfirm.disabled = !valid;
  dom.text.promotionConfirmHint.textContent = expected
    ? `Type "${expected}" exactly to confirm.`
    : "Move summary unavailable.";
}

function openModal(modalName) {
  if (!dom.modals[modalName]) return;

  const project = getSelectedProject();
  if ((modalName === "update" || modalName === "delete" || modalName === "promotion") && !project) {
    setStatus("Select an app first.", "warning");
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

function openPromotionConfirmation() {
  const project = getSelectedProject();
  if (!project) {
    setStatus("Select an app first.", "warning");
    return;
  }

  const fromEnv = state.promotion.fromEnv;
  const toEnv = state.promotion.toEnv;
  const validation = promotionValidation(project, fromEnv, toEnv);

  if (!validation.valid) {
    setStatus(validation.reason, "warning", { toast: true });
    return;
  }

  state.promotion.sourceImage = validation.sourceImage;
  state.promotion.targetImage = validation.targetImage;
  state.promotion.action = validation.action || transitionActionForTarget(toEnv);
  state.promotion.confirmationPhrase = `${state.promotion.action} ${fromEnv} to ${toEnv}`;
  const actionLabel = state.promotion.action === "release" ? "Release" : "Promotion";
  dom.text.promotionModalTitle.textContent =
    state.promotion.action === "release" ? "Confirm release" : "Confirm promotion";
  dom.buttons.promotionConfirm.textContent =
    state.promotion.action === "release" ? "Release environment" : "Promote environment";

  dom.text.promotionSummary.replaceChildren(
    makeElem("p", "", `App ${project.spec?.name || project.id}`),
    makeElem("p", "", `Action ${actionLabel}`),
    makeElem("p", "", `From ${fromEnv}`),
    makeElem("p", "", `To ${toEnv}`),
    makeElem("p", "", `Source image ${validation.sourceImage || "unknown"}`),
    makeElem("p", "", `Target current image ${validation.targetImage || "unknown"}`),
    makeElem("p", "", `Outputs loaded ${state.artifacts.loaded ? "yes" : "no"}`)
  );

  dom.inputs.promotionConfirmInput.value = "";
  syncPromotionConfirmationState();
  openModal("promotion");
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
  }
}

function clearSelection() {
  state.selectedProjectID = "";
  closeAllModals();
  setUpdateDefaults();
  stopOperationMonitor({ clearPayload: true });
  resetArtifacts();
  resetJourney();
  renderAll();
}

async function refreshProjects({ silent = false, preserveSelection = true } = {}) {
  const previousSelection = preserveSelection ? state.selectedProjectID : "";
  const projects = await requestAPI("GET", "/api/projects");

  state.projects = Array.isArray(projects) ? projects : [];

  if (previousSelection && !state.projects.some((project) => project.id === previousSelection)) {
    state.selectedProjectID = "";
    stopOperationMonitor({ clearPayload: true });
    resetArtifacts();
    resetJourney();
  } else if (!preserveSelection) {
    state.selectedProjectID = "";
    stopOperationMonitor({ clearPayload: true });
    resetArtifacts();
    resetJourney();
  }

  const selected = getSelectedProject();
  if (selected?.status?.last_op_id) {
    if (state.operation.activeOpID !== selected.status.last_op_id) {
      await startOperationMonitor(selected.status.last_op_id, { announce: false });
    }
  } else if (!selected) {
    stopOperationMonitor({ clearPayload: true });
    resetJourney();
  }

  renderAll();

  if (selected) {
    await loadJourney({ silent: true });
  }

  if (!silent) {
    setStatus("Apps refreshed.", "success");
  }
}

async function startOperationMonitor(opID, { announce = true } = {}) {
  if (!opID) {
    stopOperationMonitor({ clearPayload: true });
    renderOperationPanel();
    renderSystemStrip();
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
      upsertOperationHistory(op);
      renderOperationPanel();
      renderSystemStrip();

      if (isTerminalOperationStatus(op.status)) {
        state.operation.timer = null;

        if (announce) {
          const tone = op.status === "done" ? "success" : "error";
          setStatus(`${operationLabel(op.kind)} finished with status ${op.status}.`, tone, { toast: true });
        }

        try {
          await refreshProjects({ silent: true, preserveSelection: true });
        } catch (_error) {
          // Keep operation visibility even if refresh fails.
        }

        if (getSelectedProject()) {
          try {
            await loadArtifacts({ silent: true });
            await loadJourney({ silent: true });
          } catch (_error) {
            // Keep operation view even if refresh fails.
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
      setStatus(`Activity monitor warning: ${error.message}`, "warning");
      state.operation.timer = setTimeout(poll, backoff);
    }
  };

  await poll();
}

async function loadArtifacts({ silent = false } = {}) {
  const project = getSelectedProject();
  if (!project) {
    throw new Error("Select an app first.");
  }

  state.artifacts.loading = true;
  state.artifacts.error = "";
  renderEnvironmentMatrix();
  renderArtifactsPanel();

  try {
    const response = await requestAPI("GET", `/api/projects/${encodeURIComponent(project.id)}/artifacts`);
    const files = Array.isArray(response.files) ? response.files : [];

    state.artifacts.loaded = true;
    state.artifacts.files = [...files].sort((a, b) => a.localeCompare(b));
    state.artifacts.textCache = {};

    if (!state.artifacts.files.includes(state.artifacts.selectedPath)) {
      state.artifacts.selectedPath = "";
      state.artifacts.previewText = "";
      state.artifacts.previewMeta = "Preview unavailable";
      state.artifacts.previewIsBinary = false;
      state.artifacts.previewBytes = 0;
    }

    await buildEnvironmentSnapshots();
    renderEnvironmentMatrix();
    renderActionPanels();
    renderArtifactsPanel();
    renderSystemStrip();
    await loadJourney({ silent: true });

    if (!silent) {
      setStatus(`Loaded ${state.artifacts.files.length} outputs.`, "success", { toast: true });
    }
  } catch (error) {
    state.artifacts.error = error.message;
    if (!state.artifacts.loaded) {
      state.artifacts.files = [];
    }
    renderEnvironmentMatrix();
    renderActionPanels();
    renderArtifactsPanel();
    throw error;
  } finally {
    state.artifacts.loading = false;
    renderEnvironmentMatrix();
    renderArtifactsPanel();
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
    throw new Error("Select an app first.");
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
    state.artifacts.previewText = `Binary file (${bytes.length} bytes). Download via artifact link.`;
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

function selectProject(projectID) {
  if (projectID === state.selectedProjectID) {
    renderProjectsList();
    return;
  }

  state.selectedProjectID = projectID;
  resetArtifacts();
  resetJourney();

  const selected = getSelectedProject();
  syncUpdateForm(selected);

  if (!selected?.status?.last_op_id) {
    stopOperationMonitor({ clearPayload: true });
  } else if (state.operation.activeOpID !== selected.status.last_op_id) {
    void startOperationMonitor(selected.status.last_op_id, { announce: false });
  }

  renderAll();
  setStatus("");

  void loadArtifacts({ silent: true }).catch((error) => {
    setStatus(`Outputs unavailable: ${error.message}`, "warning");
  });

  void loadJourney({ silent: true }).catch((error) => {
    setStatus(`Journey unavailable: ${error.message}`, "warning");
  });
}

async function handleCreateSubmit(event) {
  event.preventDefault();
  setStatus("Creating app...", "info");

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
    setStatus("App created.", "success", { toast: true });
  } catch (error) {
    setStatus(error.message, statusToneFromError(error), { toast: true });
  }
}

async function handleUpdateSubmit(event) {
  event.preventDefault();

  const project = getSelectedProject();
  if (!project) {
    setStatus("Select an app first.", "warning");
    return;
  }

  setStatus("Saving app changes...", "info");

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
    setStatus("App updated.", "success", { toast: true });
  } catch (error) {
    setStatus(error.message, statusToneFromError(error), { toast: true });
  }
}

async function handleWebhookSubmit(event) {
  event.preventDefault();

  const project = getSelectedProject();
  if (!project) {
    setStatus("Select an app first.", "warning");
    return;
  }

  setStatus("Starting build from source change...", "info");

  try {
    const payload = buildWebhookPayload(project.id);
    const response = await requestAPI("POST", "/api/webhooks/source", payload);

    if (!response.accepted) {
      setStatus(`Build trigger ignored: ${response.reason || "not accepted"}`, "warning", { toast: true });
      return;
    }

    if (response.op?.id) {
      await startOperationMonitor(response.op.id, { announce: true });
    }

    await refreshProjects({ silent: true, preserveSelection: true });
    setStatus("Build run accepted.", "success", { toast: true });
  } catch (error) {
    setStatus(error.message, statusToneFromError(error), { toast: true });
  }
}

async function handleDeployDevClick() {
  const project = getSelectedProject();
  if (!project) {
    setStatus("Select an app first.", "warning");
    return;
  }

  const guardrail = deployGuardrailState();
  if (guardrail.disabled) {
    setStatus(guardrail.message, "warning", { toast: true });
    return;
  }

  setStatus(`Delivering dev environment for ${project.spec?.name || project.id}...`, "info");

  try {
    const response = await requestAPI("POST", "/api/events/deployment", {
      project_id: project.id,
      environment: "dev",
    });

    if (response.op?.id) {
      await startOperationMonitor(response.op.id, { announce: true });
    }

    await refreshProjects({ silent: true, preserveSelection: true });
    setStatus("Dev delivery accepted.", "success", { toast: true });
  } catch (error) {
    setStatus(error.message, statusToneFromError(error), { toast: true });
  }
}

async function handlePromotionFormSubmit(event) {
  event.preventDefault();
  openPromotionConfirmation();
}

async function handlePromotionConfirmSubmit(event) {
  event.preventDefault();

  const project = getSelectedProject();
  if (!project) {
    setStatus("Select an app first.", "warning");
    return;
  }

  const fromEnv = state.promotion.fromEnv;
  const toEnv = state.promotion.toEnv;
  const validation = promotionValidation(project, fromEnv, toEnv);

  if (!validation.valid) {
    setStatus(validation.reason, "warning", { toast: true });
    closeModal("promotion");
    return;
  }

  const typed = String(dom.inputs.promotionConfirmInput.value || "").trim();
  if (typed !== state.promotion.confirmationPhrase) {
    setStatus("Move confirmation phrase does not match.", "warning");
    syncPromotionConfirmationState();
    return;
  }

  const action = validation.action || transitionActionForTarget(toEnv);
  const actionLabel = action === "release" ? "Release" : "Promotion";
  setStatus(`${actionLabel} ${fromEnv} to ${toEnv}...`, "warning");

  try {
    const response = await requestAPI("POST", transitionEndpoint(action), {
      project_id: project.id,
      from_env: fromEnv,
      to_env: toEnv,
    });

    if (response.op?.id) {
      await startOperationMonitor(response.op.id, { announce: true });
    }

    closeModal("promotion");
    await refreshProjects({ silent: true, preserveSelection: true });
    setStatus(`${actionLabel} ${fromEnv} -> ${toEnv} accepted.`, "success", { toast: true });
  } catch (error) {
    setStatus(error.message, statusToneFromError(error), { toast: true });
  }
}

async function handleDeleteConfirmSubmit(event) {
  event.preventDefault();

  const project = getSelectedProject();
  if (!project) {
    setStatus("Select an app first.", "warning");
    return;
  }

  const expected = (project.spec?.name || project.id || "").trim();
  const typed = String(dom.inputs.deleteConfirm.value || "").trim();
  if (!expected || typed !== expected) {
    setStatus("Deletion confirmation does not match app name.", "warning");
    syncDeleteConfirmationState();
    return;
  }

  setStatus("Deleting app...", "warning");

  try {
    const response = await requestAPI("POST", "/api/events/registration", {
      action: "delete",
      project_id: project.id,
    });

    if (response.op) {
      state.operation.payload = response.op;
      upsertOperationHistory(response.op);
      renderOperationPanel();
    }

    closeModal("delete");
    clearSelection();
    await refreshProjects({ silent: true, preserveSelection: false });
    setStatus("App deleted.", "success", { toast: true });
  } catch (error) {
    setStatus(error.message, statusToneFromError(error), { toast: true });
  }
}

async function handleLoadArtifactsClick() {
  const project = getSelectedProject();
  if (!project) {
    setStatus("Select an app first.", "warning");
    return;
  }

  setStatus(`Loading outputs for ${project.spec?.name || project.id}...`, "info");

  try {
    await loadArtifacts({ silent: false });
  } catch (error) {
    setStatus(error.message, statusToneFromError(error), { toast: true });
  }
}

async function handleCopyPreviewClick() {
  if (dom.buttons.copyPreview.disabled) return;

  try {
    await navigator.clipboard.writeText(state.artifacts.previewText);
    setStatus("Output preview copied to clipboard.", "success", { toast: true });
  } catch (error) {
    setStatus(`Copy failed: ${error.message}`, "error", { toast: true });
  }
}

async function handleJourneyNextActionClick() {
  const project = getSelectedProject();
  if (!project) {
    setStatus("Select an app first.", "warning");
    return;
  }

  const kind = String(dom.buttons.journeyNextAction.dataset.actionKind || "none");
  const fromEnv = String(dom.buttons.journeyNextAction.dataset.fromEnv || "");
  const toEnv = String(dom.buttons.journeyNextAction.dataset.toEnv || "");

  if (kind === "build") {
    dom.forms.webhook.requestSubmit();
    return;
  }

  if (kind === "deploy_dev") {
    await handleDeployDevClick();
    return;
  }

  if ((kind === "promote" || kind === "release") && fromEnv && toEnv) {
    state.promotion.fromEnv = fromEnv;
    state.promotion.toEnv = toEnv;
    renderPromotionPanel();
    openPromotionConfirmation();
    return;
  }

  const tone = nextActionKindToTone[kind] || "info";
  setStatus("No suggested step to run right now.", tone);
}

function bindEvents() {
  dom.buttons.openCreateModal.addEventListener("click", () => {
    openModal("create");
  });

  dom.buttons.openCreateFromRail.addEventListener("click", () => {
    openModal("create");
  });

  dom.buttons.openUpdateModal.addEventListener("click", () => {
    openModal("update");
  });

  dom.buttons.openDeleteModal.addEventListener("click", () => {
    openModal("delete");
  });

  dom.buttons.refresh.addEventListener("click", async () => {
    setStatus("Refreshing apps...", "info");
    try {
      await refreshProjects({ silent: false, preserveSelection: true });
      if (getSelectedProject()) {
        await loadArtifacts({ silent: true });
        await loadJourney({ silent: true });
      }
    } catch (error) {
      setStatus(error.message, statusToneFromError(error), { toast: true });
    }
  });

  dom.buttons.deployDev.addEventListener("click", () => {
    void handleDeployDevClick();
  });

  dom.buttons.journeyNextAction.addEventListener("click", () => {
    void handleJourneyNextActionClick();
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

  dom.forms.promotion.addEventListener("submit", (event) => {
    void handlePromotionFormSubmit(event);
  });

  dom.forms.promotionConfirm.addEventListener("submit", (event) => {
    void handlePromotionConfirmSubmit(event);
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

  dom.buttons.openPromotionModal.addEventListener("click", (event) => {
    event.preventDefault();
    openPromotionConfirmation();
  });

  dom.buttons.createModalClose.addEventListener("click", () => closeModal("create"));
  dom.buttons.createModalCancel.addEventListener("click", () => closeModal("create"));
  dom.buttons.updateModalClose.addEventListener("click", () => closeModal("update"));
  dom.buttons.updateModalCancel.addEventListener("click", () => closeModal("update"));
  dom.buttons.deleteModalClose.addEventListener("click", () => closeModal("delete"));
  dom.buttons.deleteModalCancel.addEventListener("click", () => closeModal("delete"));
  dom.buttons.promotionModalClose.addEventListener("click", () => closeModal("promotion"));
  dom.buttons.promotionModalCancel.addEventListener("click", () => closeModal("promotion"));

  dom.inputs.deleteConfirm.addEventListener("input", () => {
    syncDeleteConfirmationState();
  });

  dom.inputs.promotionConfirmInput.addEventListener("input", () => {
    syncPromotionConfirmationState();
  });

  dom.inputs.promotionFrom.addEventListener("change", () => {
    state.promotion.fromEnv = dom.inputs.promotionFrom.value;
    renderPromotionPanel();
  });

  dom.inputs.promotionTo.addEventListener("change", () => {
    state.promotion.toEnv = dom.inputs.promotionTo.value;
    renderPromotionPanel();
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

  setStatus("Loading apps...", "info");
  try {
    await refreshProjects({ silent: true, preserveSelection: true });
    if (getSelectedProject()) {
      await loadArtifacts({ silent: true });
      await loadJourney({ silent: true });
    }
    setStatus("", "info");
  } catch (error) {
    setStatus(error.message, statusToneFromError(error), { toast: true });
  }
}

void init();
