// Shared DOM bindings, constants, state, and foundational helpers.
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
    closeWorkspace: document.getElementById("closeWorkspaceBtn"),
    openUpdateModal: document.getElementById("openUpdateModalBtn"),
    openDeleteModal: document.getElementById("openDeleteModalBtn"),
    refresh: document.getElementById("refreshBtn"),
    loadArtifacts: document.getElementById("loadArtifactsBtn"),
    copyPreview: document.getElementById("copyPreviewBtn"),
    deployDev: document.getElementById("deployDevBtn"),
    buildLatest: document.getElementById("buildLatestBtn"),
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
    workspaceHeading: document.getElementById("workspaceHeading"),

    projectStats: document.getElementById("projectStats"),
    selected: document.getElementById("selected"),
    journeyStatusLine: document.getElementById("journeyStatusLine"),

    deploySummary: document.getElementById("deploySummary"),
    deployGuardrail: document.getElementById("deployGuardrail"),
    deployPanelStatus: document.getElementById("deployPanelStatus"),
    buildPanelStatus: document.getElementById("buildPanelStatus"),
    promotionDraftSummary: document.getElementById("promotionDraftSummary"),
    promotionGuardrail: document.getElementById("promotionGuardrail"),
    promotionPanelStatus: document.getElementById("promotionPanelStatus"),

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
    opTransportStatus: document.getElementById("opTransportStatus"),
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
    landingPanel: document.getElementById("landingPanel"),
    workspaceShell: document.getElementById("workspaceShell"),
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

dom.buttons.webhook = dom.buttons.buildLatest || dom.forms.webhook.querySelector("button[type='submit']");

const defaultSourceWebhookPayload = {
  repo: "source",
  branch: "main",
  ref: "refs/heads/main",
};

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
    builderRequestedMode: "",
    builderEffectiveMode: "",
    builderFallbackReason: "",
    builderPolicyError: "",
    builderModeWarning: "",
    builderModeExplicit: false,
  },
  journey: {
    loading: false,
    error: "",
    data: null,
  },
  operation: {
    activeOpID: "",
    payload: null,
    eventSource: null,
    usingPolling: false,
    timer: null,
    token: 0,
    failureCount: 0,
    sseFailureCount: 0,
    terminalHandledOpID: "",
    history: [],
    historyLoading: false,
    historyError: "",
    historyNextCursor: "",
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
    workspaceOpen: false,
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
  if (error?.status === 409 || msg.includes("409")) return "warning";
  if (msg.includes("ignored")) return "warning";
  if (msg.includes("not found") || msg.includes("400")) return "warning";
  return "error";
}

function statusMessageFromError(error) {
  const status = Number(error?.status || 0);
  const payload = error?.payload;
  if (status === 409) {
    const activeOp = payload?.active_op || {};
    const activeID = String(activeOp.id || "").trim();
    const activeKind = String(activeOp.kind || "operation").trim();
    const activeStatus = String(activeOp.status || "running").trim();
    const activeSummary = activeID
      ? `${activeKind} ${activeID.slice(0, 8)} (${activeStatus})`
      : `${activeKind} (${activeStatus})`;
    return `Another operation is already active for this app (${activeSummary}). Wait for it to finish, then retry.`;
  }

  const userMessage = String(error?.userMessage || "").trim();
  if (userMessage) return userMessage;
  return String(error?.message || error || "Request failed");
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

function setPanelInlineStatus(target, message, tone = "info") {
  if (!target) return;
  target.textContent = message || "";
  target.className = "panel-inline-status";
  target.classList.add(`tone-${tone || "info"}`);
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

function generatedWebhookCommitHint() {
  const now = Date.now().toString(16);
  const random = Math.floor(Math.random() * 0xffffff)
    .toString(16)
    .padStart(6, "0");
  return `manual-${now}-${random}`;
}

function buildWebhookPayload(projectID, { generateCommit = false } = {}) {
  const repo = String(dom.inputs.webhookRepo.value || defaultSourceWebhookPayload.repo).trim();
  const branch = String(dom.inputs.webhookBranch.value || defaultSourceWebhookPayload.branch).trim();
  const ref = String(dom.inputs.webhookRef.value || defaultSourceWebhookPayload.ref).trim();
  let commit = String(dom.inputs.webhookCommit.value || "").trim();
  if (!commit && generateCommit) {
    commit = generatedWebhookCommitHint();
  }

  return {
    project_id: projectID,
    repo: repo || defaultSourceWebhookPayload.repo,
    branch: branch || defaultSourceWebhookPayload.branch,
    ref: ref || defaultSourceWebhookPayload.ref,
    commit,
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
    project.status?.message || "",
    project.status?.last_op_kind || "",
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
