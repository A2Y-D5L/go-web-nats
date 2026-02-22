// API calls, journey loading, artifact parsing, and deploy/promotion readiness state.
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
  state.artifacts.builderRequestedMode = "";
  state.artifacts.builderEffectiveMode = "";
  state.artifacts.builderFallbackReason = "";
  state.artifacts.builderPolicyError = "";
  state.artifacts.builderModeWarning = "";
  state.artifacts.builderModeExplicit = false;
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
    const err = new Error(`${method} ${url} -> ${response.status}: ${text}`);
    err.status = response.status;
    err.payload = payload;
    err.userMessage =
      typeof payload === "string"
        ? payload
        : String(payload?.reason || payload?.message || text);
    throw err;
  }

  return payload;
}

async function loadSystemStatus({ silent = false } = {}) {
  state.system.loading = true;
  state.system.error = "";
  renderSystemStrip();

  try {
    const response = await requestAPI("GET", "/api/system");
    state.system.data = response && typeof response === "object" ? response : null;
    renderSystemStrip();
    if (!silent) {
      setStatus("Runtime status refreshed.", "success");
    }
  } catch (error) {
    state.system.error = error.message;
    if (!silent) {
      throw error;
    }
  } finally {
    state.system.loading = false;
    renderSystemStrip();
  }
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

  const publishPath = "build/publish-local-daemon.json";
  if (fileSet.has(publishPath)) {
    try {
      const publishRaw = await readArtifactText(publishPath);
      const publish = JSON.parse(String(publishRaw || "{}"));
      state.artifacts.builderRequestedMode = String(publish.requested_builder_mode || "").trim();
      state.artifacts.builderEffectiveMode = String(
        publish.effective_builder_mode || publish.builder_mode || ""
      ).trim();
      state.artifacts.builderFallbackReason = String(publish.builder_mode_fallback_reason || "").trim();
      state.artifacts.builderPolicyError = String(publish.builder_mode_policy_error || "").trim();
      state.artifacts.builderModeWarning = String(publish.builder_mode_warning || "").trim();
      state.artifacts.builderModeExplicit = Boolean(publish.builder_mode_explicit);
    } catch (_error) {
      state.artifacts.builderRequestedMode = "";
      state.artifacts.builderEffectiveMode = "";
      state.artifacts.builderFallbackReason = "";
      state.artifacts.builderPolicyError = "";
      state.artifacts.builderModeWarning = "";
      state.artifacts.builderModeExplicit = false;
    }
  } else {
    state.artifacts.builderRequestedMode = "";
    state.artifacts.builderEffectiveMode = "";
    state.artifacts.builderFallbackReason = "";
    state.artifacts.builderPolicyError = "";
    state.artifacts.builderModeWarning = "";
    state.artifacts.builderModeExplicit = false;
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

function buildGuardrailState() {
  const project = getSelectedProject();
  if (!project) {
    return {
      disabled: true,
      message: "Select an app first.",
      summary: "Choose an app to trigger a source build.",
      tone: "warning",
    };
  }

  if (projectHasRunningOperation()) {
    const opKind = state.operation.payload?.kind;
    return {
      disabled: true,
      message: `Wait for ${operationLabel(opKind)} to finish before starting another build.`,
      summary: "Another app activity is currently running.",
      tone: "warning",
    };
  }

  return {
    disabled: false,
    message: "Build trigger is ready.",
    summary: "Build uses source/main with refs/heads/main by default.",
    tone: "info",
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

  if (projectHasRunningOperation()) {
    setPanelInlineStatus(
      dom.text.deployPanelStatus,
      "Delivery controls are paused while current activity is in progress.",
      "warning"
    );
    return;
  }

  setPanelInlineStatus(
    dom.text.deployPanelStatus,
    guardrail.disabled ? "Resolve delivery guardrails before continuing." : "Ready: deliver the latest built image to dev.",
    guardrail.disabled ? "warning" : "success"
  );
}

function renderBuildPanel() {
  const guardrail = buildGuardrailState();
  dom.buttons.webhook.disabled = guardrail.disabled;
  dom.inputs.webhookRepo.disabled = guardrail.disabled;
  dom.inputs.webhookBranch.disabled = guardrail.disabled;
  dom.inputs.webhookRef.disabled = guardrail.disabled;
  dom.inputs.webhookCommit.disabled = guardrail.disabled;

  setPanelInlineStatus(dom.text.buildPanelStatus, `${guardrail.summary} ${guardrail.message}`, guardrail.tone);
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
  dom.inputs.promotionFrom.disabled = projectHasRunningOperation();
  dom.inputs.promotionTo.disabled = projectHasRunningOperation();

  if (projectHasRunningOperation()) {
    setPanelInlineStatus(
      dom.text.promotionPanelStatus,
      "Environment moves are paused while current activity is in progress.",
      "warning"
    );
    return;
  }

  setPanelInlineStatus(
    dom.text.promotionPanelStatus,
    validation.valid
      ? `${actionLabel} path ready. Review move details, then confirm.`
      : "Adjust environment selection or readiness checks before continuing.",
    validation.valid ? "success" : "warning"
  );
}

function renderActionPanels() {
  renderBuildPanel();
  renderDeployPanel();
  renderPromotionPanel();
}
