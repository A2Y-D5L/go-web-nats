// Modal lifecycle, operation monitoring, project refresh/selection, and action handlers.
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

function normalizedPromotionPreviewEntries(value) {
  return Array.isArray(value) ? value : [];
}

function promotionPreviewHasBlockers() {
  return normalizedPromotionPreviewEntries(state.promotion.blockers).length > 0;
}

function renderPromotionPreviewDetails(project, validation, preview) {
  const actionLabel = state.promotion.action === "release" ? "Release" : "Promotion";
  const blockers = normalizedPromotionPreviewEntries(preview?.blockers);
  const gates = normalizedPromotionPreviewEntries(preview?.gates);
  const rolloutPlan = normalizedPromotionPreviewEntries(preview?.rollout_plan);
  const sourceRelease = preview?.source_release || null;
  const targetRelease = preview?.target_release || null;

  const sourceImage =
    validation.sourceImage || sourceRelease?.image || state.promotion.sourceImage || "unknown (not delivered)";
  const targetImage =
    validation.targetImage || targetRelease?.image || state.promotion.targetImage || "not currently deployed";

  dom.text.promotionSummary.replaceChildren(
    makeElem("p", "", `App ${project.spec?.name || project.id}`),
    makeElem("p", "", `Action ${actionLabel}`),
    makeElem("p", "", `From ${state.promotion.fromEnv || "-"}`),
    makeElem("p", "", `To ${state.promotion.toEnv || "-"}`),
    makeElem("p", "", `Source image ${sourceImage}`),
    makeElem("p", "", `Target current image ${targetImage}`),
    makeElem("p", "", `Summary ${String(preview?.change_summary || validation.reason || "Preview unavailable.")}`)
  );

  dom.containers.promotionPreviewGates.replaceChildren();
  dom.containers.promotionPreviewBlockers.replaceChildren();
  dom.containers.promotionRolloutPlan.replaceChildren();

  if (gates.length) {
    dom.containers.promotionPreviewGates.appendChild(makeElem("p", "promotion-preview-title", "Gates"));
    const gateList = makeElem("ul", "promotion-preview-items");
    for (const gate of gates) {
      const status = String(gate?.status || "warning").trim().toLowerCase();
      const row = makeElem("li", `promotion-preview-item promotion-preview-item--${status}`);
      const title = String(gate?.title || gate?.code || "Gate").trim();
      const detail = String(gate?.detail || "").trim();
      row.append(makeElem("span", "promotion-preview-label", `${title} (${status})`));
      if (detail) {
        row.append(makeElem("span", "promotion-preview-meta", detail));
      }
      gateList.appendChild(row);
    }
    dom.containers.promotionPreviewGates.appendChild(gateList);
  }

  if (blockers.length) {
    dom.containers.promotionPreviewBlockers.appendChild(makeElem("p", "promotion-preview-title", "Blockers"));
    const blockerList = makeElem("ul", "promotion-preview-items");
    for (const blocker of blockers) {
      const row = makeElem("li", "promotion-preview-item promotion-preview-item--blocked");
      const message = String(blocker?.message || blocker?.code || "Blocked").trim();
      const why = String(blocker?.why || "").trim();
      const next = String(blocker?.next_action || "").trim();
      row.append(makeElem("span", "promotion-preview-label", message));
      if (why) {
        row.append(makeElem("span", "promotion-preview-meta", why));
      }
      if (next) {
        row.append(makeElem("span", "promotion-preview-meta", `Next: ${next}`));
      }
      blockerList.appendChild(row);
    }
    dom.containers.promotionPreviewBlockers.appendChild(blockerList);
  }

  if (rolloutPlan.length) {
    dom.containers.promotionRolloutPlan.appendChild(makeElem("p", "promotion-preview-title", "Rollout plan"));
    const planList = makeElem("ol", "promotion-preview-items");
    for (const stage of rolloutPlan) {
      planList.appendChild(makeElem("li", "promotion-preview-item", String(stage || "").trim()));
    }
    dom.containers.promotionRolloutPlan.appendChild(planList);
  }

  if (blockers.length) {
    dom.text.promotionPreviewStatus.textContent = "Preview blocked. Resolve blockers before confirming this move.";
    return;
  }
  dom.text.promotionPreviewStatus.textContent = "Preview passed. Confirm the phrase to start this move.";
}

async function openPromotionConfirmation() {
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

  state.promotion.previewLoading = true;
  state.promotion.previewError = "";
  state.promotion.blockers = [];
  state.promotion.preview = null;

  setStatus(
    `Running ${transitionVerb(validation.action || transitionActionForTarget(toEnv))} preview for ${fromEnv} -> ${toEnv}...`,
    "info"
  );

  let preview;
  try {
    preview = await requestAPI("POST", "/api/events/promotion/preview", {
      project_id: project.id,
      from_env: fromEnv,
      to_env: toEnv,
    });
  } catch (error) {
    state.promotion.previewLoading = false;
    state.promotion.previewError = error.message;
    state.promotion.preview = null;
    state.promotion.blockers = [];
    setStatus(statusMessageFromError(error), statusToneFromError(error), { toast: true });
    return;
  }

  state.promotion.previewLoading = false;
  state.promotion.previewError = "";
  state.promotion.preview = preview && typeof preview === "object" ? preview : {};
  state.promotion.blockers = normalizedPromotionPreviewEntries(state.promotion.preview?.blockers);

  state.promotion.sourceImage = validation.sourceImage;
  state.promotion.targetImage = validation.targetImage;
  state.promotion.action = String(
    state.promotion.preview?.action || validation.action || transitionActionForTarget(toEnv)
  ).toLowerCase();
  state.promotion.confirmationPhrase = `${state.promotion.action} ${fromEnv} to ${toEnv}`;
  const actionLabel = state.promotion.action === "release" ? "Release" : "Promotion";
  dom.text.promotionModalTitle.textContent =
    state.promotion.action === "release" ? "Confirm release" : "Confirm promotion";
  dom.buttons.promotionConfirm.textContent =
    state.promotion.action === "release" ? "Release environment" : "Promote environment";
  renderPromotionPreviewDetails(project, validation, state.promotion.preview);

  dom.inputs.promotionConfirmInput.value = "";
  syncPromotionConfirmationState();
  openModal("promotion");
  if (promotionPreviewHasBlockers()) {
    setStatus(`${actionLabel} preview found blockers. Resolve blockers before confirming.`, "warning", {
      toast: true,
    });
    return;
  }
  setStatus(`${actionLabel} preview is ready. Confirm to proceed.`, "success");
}

function stopOperationMonitor({ clearPayload = false } = {}) {
  if (state.operation.timer) {
    clearTimeout(state.operation.timer);
    state.operation.timer = null;
  }
  if (state.operation.eventSource) {
    state.operation.eventSource.close();
    state.operation.eventSource = null;
  }

  state.operation.token += 1;
  state.operation.failureCount = 0;
  state.operation.sseFailureCount = 0;
  state.operation.usingPolling = false;
  state.operation.terminalHandledOpID = "";
  state.operation.activeOpID = "";

  if (clearPayload) {
    state.operation.payload = null;
  }
}

function resetOperationHistory() {
  state.operation.history = [];
  state.operation.historyLoading = false;
  state.operation.historyLoadingMore = false;
  state.operation.historyError = "";
  state.operation.historyLoadMoreError = "";
  state.operation.historyNextCursor = "";
}

function operationHistoryEndpoint(projectID, cursor = "") {
  const params = new URLSearchParams();
  params.set("limit", String(operationHistoryPageLimit));
  if (cursor) {
    params.set("cursor", String(cursor));
  }
  return `/api/projects/${encodeURIComponent(projectID)}/ops?${params.toString()}`;
}

async function loadOperationHistory({ silent = false } = {}) {
  const project = getSelectedProject();
  if (!project) {
    resetOperationHistory();
    renderOperationPanel();
    return;
  }
  const projectID = project.id;

  state.operation.historyLoading = true;
  state.operation.historyError = "";
  state.operation.historyLoadMoreError = "";
  renderOperationPanel();

  try {
    const response = await requestAPI("GET", operationHistoryEndpoint(projectID));
    if (getSelectedProject()?.id !== projectID) {
      return;
    }
    const items = Array.isArray(response?.items) ? response.items : [];
    state.operation.history = [];
    for (const item of items) {
      upsertOperationHistory(item);
    }
    state.operation.historyNextCursor = String(response?.next_cursor || "").trim();

    if (state.operation.payload?.id) {
      upsertOperationHistory(state.operation.payload);
    }

    renderOperationPanel();
    if (!silent) {
      setStatus("Activity history refreshed.", "success");
    }
  } catch (error) {
    if (getSelectedProject()?.id !== projectID) {
      return;
    }
    state.operation.historyError = error.message;
    renderOperationPanel();
    if (!silent) {
      throw error;
    }
  } finally {
    if (getSelectedProject()?.id !== projectID) {
      return;
    }
    state.operation.historyLoading = false;
    renderOperationPanel();
  }
}

async function loadMoreOperationHistory({ silent = false } = {}) {
  const project = getSelectedProject();
  if (!project) {
    return;
  }
  if (state.operation.historyLoading || state.operation.historyLoadingMore) {
    return;
  }

  const cursor = String(state.operation.historyNextCursor || "").trim();
  if (!cursor) {
    return;
  }

  const projectID = project.id;
  state.operation.historyLoadingMore = true;
  state.operation.historyLoadMoreError = "";
  renderOperationPanel();

  try {
    const response = await requestAPI("GET", operationHistoryEndpoint(projectID, cursor));
    if (getSelectedProject()?.id !== projectID) {
      return;
    }
    const items = Array.isArray(response?.items) ? response.items : [];
    for (const item of items) {
      upsertOperationHistory(item);
    }
    state.operation.historyNextCursor = String(response?.next_cursor || "").trim();
    renderOperationPanel();
  } catch (error) {
    if (getSelectedProject()?.id !== projectID) {
      return;
    }
    state.operation.historyLoadMoreError = error.message;
    renderOperationPanel();
    if (!silent) {
      throw error;
    }
  } finally {
    if (getSelectedProject()?.id !== projectID) {
      return;
    }
    state.operation.historyLoadingMore = false;
    renderOperationPanel();
  }
}

function clearSelection() {
  state.selectedProjectID = "";
  state.ui.workspaceOpen = false;
  closeAllModals();
  setUpdateDefaults();
  stopOperationMonitor({ clearPayload: true });
  resetOperationHistory();
  resetArtifacts();
  resetOverview();
  resetJourney();
  resetReleaseTimeline();
  renderAll();
}

function closeWorkspace() {
  state.ui.workspaceOpen = false;
  renderAll();
}

async function refreshProjects({ silent = false, preserveSelection = true } = {}) {
  const previousSelection = preserveSelection ? state.selectedProjectID : "";
  const [projects] = await Promise.all([
    requestAPI("GET", "/api/projects"),
    loadSystemStatus({ silent: true }),
  ]);

  state.projects = Array.isArray(projects) ? projects : [];

  if (previousSelection && !state.projects.some((project) => project.id === previousSelection)) {
    state.selectedProjectID = "";
    state.ui.workspaceOpen = false;
    stopOperationMonitor({ clearPayload: true });
    resetOperationHistory();
    resetArtifacts();
    resetOverview();
    resetJourney();
    resetReleaseTimeline();
  } else if (!preserveSelection) {
    state.selectedProjectID = "";
    state.ui.workspaceOpen = false;
    stopOperationMonitor({ clearPayload: true });
    resetOperationHistory();
    resetArtifacts();
    resetOverview();
    resetJourney();
    resetReleaseTimeline();
  }

  const selected = getSelectedProject();
  if (selected?.status?.last_op_id) {
    if (state.operation.activeOpID !== selected.status.last_op_id) {
      await startOperationMonitor(selected.status.last_op_id, { announce: false });
    }
  } else if (!selected) {
    stopOperationMonitor({ clearPayload: true });
    resetOperationHistory();
    resetOverview();
    resetJourney();
    resetReleaseTimeline();
  }

  if (selected) {
    await loadOperationHistory({ silent: true });
  }

  renderAll();

  if (selected) {
    await loadJourney({ silent: true });
    await loadReleaseTimeline({ silent: true });
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
  if (state.operation.activeOpID === opID && state.operation.eventSource) {
    return;
  }

  stopOperationMonitor({ clearPayload: false });
  state.operation.activeOpID = opID;
  const token = state.operation.token;

  const closeOperationEventSource = () => {
    if (!state.operation.eventSource) return;
    state.operation.eventSource.close();
    state.operation.eventSource = null;
  };

  const finalizeTerminalOperation = async (op) => {
    if (!op || state.operation.terminalHandledOpID === op.id) return;
    state.operation.terminalHandledOpID = op.id;
    closeOperationEventSource();
    if (state.operation.timer) {
      clearTimeout(state.operation.timer);
      state.operation.timer = null;
    }

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
        await loadReleaseTimeline({ silent: true });
      } catch (_error) {
        // Keep operation view even if refresh fails.
      }
    }
  };

  const fetchLatestOp = async () => {
    const op = await requestAPI("GET", `/api/ops/${encodeURIComponent(opID)}`);
    if (token !== state.operation.token) return null;

    state.operation.payload = op;
    state.operation.failureCount = 0;
    state.operation.sseFailureCount = 0;
    upsertOperationHistory(op);
    renderOperationPanel();
    renderSystemStrip();

    if (isTerminalOperationStatus(op.status)) {
      await finalizeTerminalOperation(op);
    }
    return op;
  };

  const startPolling = async () => {
    if (state.operation.usingPolling) return;
    state.operation.usingPolling = true;
    closeOperationEventSource();
    renderOperationPanel();

    const poll = async () => {
      if (token !== state.operation.token) return;

      try {
        const op = await fetchLatestOp();
        if (token !== state.operation.token || !op) return;
        if (isTerminalOperationStatus(op.status)) {
          state.operation.timer = null;
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
  };

  const startSSE = () => {
    if (typeof window.EventSource === "undefined") {
      void startPolling();
      return;
    }

    const source = new EventSource(`/api/ops/${encodeURIComponent(opID)}/events`);
    state.operation.eventSource = source;
    state.operation.usingPolling = false;
    renderOperationPanel();

    const streamEvents = [
      "op.bootstrap",
      "op.status",
      "step.started",
      "step.ended",
      "step.artifacts",
      "op.completed",
      "op.failed",
      "op.heartbeat",
    ];

    const onEvent = (event) => {
      if (token !== state.operation.token) return;

      state.operation.sseFailureCount = 0;

      if (event.type === "op.heartbeat") {
        return;
      }

      void fetchLatestOp().catch((error) => {
        if (token !== state.operation.token) return;
        state.operation.failureCount += 1;
        setStatus(`Activity stream warning: ${error.message}`, "warning");
      });

      if (event.type === "op.completed" || event.type === "op.failed") {
        closeOperationEventSource();
      }
    };

    streamEvents.forEach((eventName) => source.addEventListener(eventName, onEvent));

    source.onerror = () => {
      if (token !== state.operation.token) return;
      state.operation.sseFailureCount += 1;
      if (state.operation.sseFailureCount < 4 || state.operation.usingPolling) {
        return;
      }
      setStatus("Realtime stream disconnected repeatedly. Falling back to polling.", "warning", { toast: true });
      closeOperationEventSource();
      void startPolling();
    };
  };

  try {
    const first = await fetchLatestOp();
    if (token !== state.operation.token) return;
    if (!first || isTerminalOperationStatus(first.status)) {
      return;
    }
  } catch (error) {
    if (token !== state.operation.token) return;
    setStatus(`Activity monitor warning: ${error.message}`, "warning");
    await startPolling();
    return;
  }

  startSSE();
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
    state.ui.workspaceOpen = true;
    renderAll();
    return;
  }

  state.selectedProjectID = projectID;
  state.ui.workspaceOpen = true;
  resetOperationHistory();
  resetArtifacts();
  resetOverview();
  resetJourney();
  resetReleaseTimeline();

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

  void loadReleaseTimeline({ silent: true }).catch((error) => {
    setStatus(`Release timeline unavailable: ${error.message}`, "warning");
  });

  void loadOperationHistory({ silent: true }).catch((error) => {
    setStatus(`Activity history unavailable: ${error.message}`, "warning");
  });
}

function primeAcceptedOperation(op) {
  if (!op?.id) return;
  state.operation.payload = op;
  upsertOperationHistory(op);
  renderOperationPanel();
  renderSystemStrip();
  renderActionPanels();
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
      primeAcceptedOperation(response.op);
      await startOperationMonitor(response.op.id, { announce: true });
    }

    closeModal("create");
    setStatus("App creation accepted. Live activity is now tracking progress.", "success", { toast: true });
  } catch (error) {
    setStatus(statusMessageFromError(error), statusToneFromError(error), { toast: true });
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
      primeAcceptedOperation(response.op);
      await startOperationMonitor(response.op.id, { announce: true });
    }

    closeModal("update");
    setStatus("App update accepted. Live activity is now tracking progress.", "success", { toast: true });
  } catch (error) {
    setStatus(statusMessageFromError(error), statusToneFromError(error), { toast: true });
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
    const payload = buildWebhookPayload(project.id, { generateCommit: true });
    const response = await requestAPI("POST", "/api/webhooks/source", payload);

    if (!response.accepted) {
      setStatus(`Build trigger ignored: ${response.reason || "not accepted"}`, "warning", { toast: true });
      return;
    }

    if (response.op?.id) {
      primeAcceptedOperation(response.op);
      await startOperationMonitor(response.op.id, { announce: true });
    }

    await refreshProjects({ silent: true, preserveSelection: true });
    setStatus("Build run accepted. Live activity is now tracking progress.", "success", { toast: true });
  } catch (error) {
    setStatus(statusMessageFromError(error), statusToneFromError(error), { toast: true });
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
      primeAcceptedOperation(response.op);
      await startOperationMonitor(response.op.id, { announce: true });
    }

    await refreshProjects({ silent: true, preserveSelection: true });
    setStatus("Dev delivery accepted. Live activity is now tracking progress.", "success", { toast: true });
  } catch (error) {
    setStatus(statusMessageFromError(error), statusToneFromError(error), { toast: true });
  }
}

async function handlePromotionFormSubmit(event) {
  event.preventDefault();
  await openPromotionConfirmation();
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
  if (state.promotion.previewLoading) {
    setStatus("Transition preview is still running. Wait for it to finish.", "warning");
    return;
  }
  if (!state.promotion.preview) {
    setStatus("Transition preview is unavailable. Review move again before confirming.", "warning", { toast: true });
    return;
  }
  if (promotionPreviewHasBlockers()) {
    setStatus("Preview has blockers. Resolve blockers before confirming this move.", "warning", { toast: true });
    syncPromotionConfirmationState();
    return;
  }

  const typed = String(dom.inputs.promotionConfirmInput.value || "").trim();
  if (typed !== state.promotion.confirmationPhrase) {
    setStatus("Move confirmation phrase does not match.", "warning");
    syncPromotionConfirmationState();
    return;
  }

  const action = String(state.promotion.action || validation.action || transitionActionForTarget(toEnv))
    .trim()
    .toLowerCase();
  const actionLabel = action === "release" ? "Release" : "Promotion";
  setStatus(`${actionLabel} ${fromEnv} to ${toEnv}...`, "warning");

  try {
    const response = await requestAPI("POST", transitionEndpoint(action), {
      project_id: project.id,
      from_env: fromEnv,
      to_env: toEnv,
    });

    if (response.op?.id) {
      primeAcceptedOperation(response.op);
      await startOperationMonitor(response.op.id, { announce: true });
    }

    closeModal("promotion");
    await refreshProjects({ silent: true, preserveSelection: true });
    setStatus(`${actionLabel} ${fromEnv} -> ${toEnv} accepted. Live activity is now tracking progress.`, "success", {
      toast: true,
    });
  } catch (error) {
    setStatus(statusMessageFromError(error), statusToneFromError(error), { toast: true });
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

    if (response.op?.id) {
      primeAcceptedOperation(response.op);
      await startOperationMonitor(response.op.id, { announce: true });
    }

    closeModal("delete");
    await refreshProjects({ silent: true, preserveSelection: true });
    setStatus("App deletion accepted. Live activity is now tracking progress.", "success", { toast: true });
  } catch (error) {
    setStatus(statusMessageFromError(error), statusToneFromError(error), { toast: true });
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
    setStatus(statusMessageFromError(error), statusToneFromError(error), { toast: true });
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
    await openPromotionConfirmation();
    return;
  }

  const tone = nextActionKindToTone[kind] || "info";
  setStatus("No suggested step to run right now.", tone);
}
