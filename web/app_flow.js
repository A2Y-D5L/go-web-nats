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
  state.ui.workspaceOpen = false;
  closeAllModals();
  setUpdateDefaults();
  stopOperationMonitor({ clearPayload: true });
  resetArtifacts();
  resetJourney();
  renderAll();
}

function closeWorkspace() {
  state.ui.workspaceOpen = false;
  renderAll();
}

async function refreshProjects({ silent = false, preserveSelection = true } = {}) {
  const previousSelection = preserveSelection ? state.selectedProjectID : "";
  const projects = await requestAPI("GET", "/api/projects");

  state.projects = Array.isArray(projects) ? projects : [];

  if (previousSelection && !state.projects.some((project) => project.id === previousSelection)) {
    state.selectedProjectID = "";
    state.ui.workspaceOpen = false;
    stopOperationMonitor({ clearPayload: true });
    resetArtifacts();
    resetJourney();
  } else if (!preserveSelection) {
    state.selectedProjectID = "";
    state.ui.workspaceOpen = false;
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
    state.ui.workspaceOpen = true;
    renderAll();
    return;
  }

  state.selectedProjectID = projectID;
  state.ui.workspaceOpen = true;
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
