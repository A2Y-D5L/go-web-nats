// High-level panel rendering and render orchestration helpers.
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

function journeyNextActionInstruction(nextAction) {
  const kind = String(nextAction?.kind || "none");
  const fromEnv = String(nextAction?.from_env || "");
  const toEnv = String(nextAction?.to_env || "");
  const detail = String(nextAction?.detail || "").trim();

  let summary = "No follow-up action is required right now.";
  if (kind === "build") {
    summary = "Starts a build from source/main and streams progress in Activity.";
  } else if (kind === "deploy_dev") {
    summary = "Deploys the latest built image to dev for validation.";
  } else if (kind === "promote") {
    summary = `Opens promotion review for moving ${fromEnv || "source"} to ${toEnv || "target"}.`;
  } else if (kind === "release") {
    summary = `Opens release review for moving ${fromEnv || "source"} to ${toEnv || "production"}.`;
  } else if (kind === "investigate") {
    summary = "Review recent activity and outputs before retrying.";
  }

  if (!detail) return summary;
  return `${summary} ${detail}`;
}

function renderWorkspaceShell() {
  const project = getSelectedProject();
  const shouldShowWorkspace = state.ui.workspaceOpen && Boolean(project);

  dom.containers.workspaceShell.classList.toggle("is-hidden", !shouldShowWorkspace);
  dom.containers.landingPanel.classList.toggle("is-hidden", shouldShowWorkspace);

  dom.text.workspaceHeading.textContent = shouldShowWorkspace
    ? project.spec?.name || project.id
    : "Selected app";
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

  const journey = currentJourney();
  if ((state.overview.loading && !journey) || (state.journey.loading && !journey)) {
    dom.text.journeyStatusLine.textContent = "Loading journey snapshot...";
    nextActionCard.textContent = "Preparing suggested next step...";
    nextActionButton.disabled = true;
    nextActionButton.textContent = "Run suggested step";
    renderEmptyState(milestoneContainer, "Loading milestones...");
    return;
  }

  const journeyError = journey ? "" : state.journey.error || state.overview.error;
  if (journeyError) {
    dom.text.journeyStatusLine.textContent = "Journey snapshot unavailable.";
    nextActionCard.textContent = journeyError;
    nextActionButton.disabled = true;
    nextActionButton.textContent = "Run suggested step";
    renderEmptyState(milestoneContainer, "Journey data could not be loaded.");
    return;
  }

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
  nextActionCard.textContent = journeyNextActionInstruction(nextAction);
  nextActionButton.dataset.actionKind = nextAction.kind || "none";
  nextActionButton.dataset.fromEnv = nextAction.from_env || "";
  nextActionButton.dataset.toEnv = nextAction.to_env || "";
  nextActionButton.textContent = nextAction.label || "Run suggested step";
  nextActionButton.disabled = !["build", "deploy_dev", "promote", "release"].includes(nextAction.kind);

  if (projectHasRunningOperation()) {
    nextActionButton.disabled = true;
    nextActionCard.textContent = "Suggested step is temporarily paused while current app activity is running.";
  }

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

  const requestedMode = state.artifacts.builderRequestedMode || "";
  const effectiveMode = state.artifacts.builderEffectiveMode || "";
  const modeFallbackReason = state.artifacts.builderFallbackReason || "";
  const modePolicyError = state.artifacts.builderPolicyError || "";
  const modeWarning = state.artifacts.builderModeWarning || "";
  const modeContext = [];
  if (requestedMode) modeContext.push(`requested ${requestedMode}`);
  if (effectiveMode) modeContext.push(`effective ${effectiveMode}`);
  if (state.artifacts.builderModeExplicit) modeContext.push("explicit request");
  if (modeFallbackReason) modeContext.push(`fallback ${modeFallbackReason}`);
  if (modeWarning) modeContext.push(`warning ${modeWarning}`);
  if (modePolicyError) modeContext.push(`policy ${modePolicyError}`);
  if (effectiveMode === "artifact") {
    signal.className = "buildkit-signal muted";
    signal.textContent = modeContext.length
      ? `Builder mode context: ${modeContext.join(" • ")}`
      : "Builder mode context is unavailable.";
    return;
  }

  const present = [...buildKitArtifactSet].filter((file) => state.artifacts.files.includes(file));
  if (present.length === buildKitArtifactSet.size) {
    signal.className = "buildkit-signal present";
    if (modeContext.length) {
      signal.textContent = `Detailed build evidence is present. ${modeContext.join(" • ")}`;
    } else {
      signal.textContent = "Detailed build evidence is present (summary, metadata, and log).";
    }
    return;
  }

  const missing = [...buildKitArtifactSet].filter((file) => !state.artifacts.files.includes(file));
  signal.className = "buildkit-signal missing";
  if (modeContext.length) {
    signal.textContent = `Some detailed build evidence is missing: ${missing.join(", ")}. ${modeContext.join(" • ")}`;
  } else {
    signal.textContent = `Some detailed build evidence is missing: ${missing.join(", ")}`;
  }
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
  const readyCount = state.projects.filter((project) => project.status?.phase === "Ready").length;

  dom.text.systemProjectCount.textContent = String(state.projects.length);
  dom.text.systemReadyCount.textContent = `${readyCount} delivery-ready`;

  const system = state.system.data;
  if (state.system.loading && !system) {
    dom.text.healthLabel.textContent = "Loading";
    dom.text.healthMeta.textContent = "Fetching runtime capability state";
    dom.text.systemActiveOp.textContent = "Loading";
    dom.text.systemActiveOpMeta.textContent = "Reading realtime transport status";
    dom.text.systemBuilderMode.textContent = "Loading";
    dom.text.systemBuilderMeta.textContent = "Reading builder mode";
    return;
  }

  if (!system) {
    dom.text.healthLabel.textContent = state.system.error ? "Unavailable" : "Unknown";
    dom.text.healthMeta.textContent = state.system.error
      ? "Runtime status endpoint failed"
      : "Runtime status not loaded yet";
    dom.text.systemActiveOp.textContent = "Unknown";
    dom.text.systemActiveOpMeta.textContent = "Realtime transport data unavailable";
    dom.text.systemBuilderMode.textContent = "Unknown";
    dom.text.systemBuilderMeta.textContent = "Builder mode data unavailable";
    return;
  }

  const nats = system.nats && typeof system.nats === "object" ? system.nats : {};
  const realtime = system.realtime && typeof system.realtime === "object" ? system.realtime : {};

  const sseEnabled = Boolean(realtime.sse_enabled);
  const replayWindowNumber = Number(realtime.sse_replay_window);
  const replayWindow = Number.isFinite(replayWindowNumber)
    ? `${replayWindowNumber} events`
    : String(realtime.sse_replay_window || "n/a");
  const heartbeat = String(realtime.sse_heartbeat_interval || "n/a").trim() || "n/a";
  dom.text.systemActiveOp.textContent = sseEnabled ? "SSE enabled" : "SSE unavailable";
  dom.text.systemActiveOpMeta.textContent = `heartbeat ${heartbeat} • replay ${replayWindow}`;

  const requestedMode = String(system.builder_mode_requested || "unknown").trim() || "unknown";
  const effectiveMode = String(system.builder_mode_effective || "unknown").trim() || "unknown";
  const builderMeta = [`requested ${requestedMode}`];
  const builderReason = String(system.builder_mode_reason || "").trim();
  const runtimeVersion = String(system.version || "").trim();
  if (builderReason) {
    builderMeta.push(builderReason);
  }
  if (runtimeVersion) {
    builderMeta.push(`version ${runtimeVersion}`);
  }
  dom.text.systemBuilderMode.textContent = effectiveMode;
  dom.text.systemBuilderMeta.textContent = builderMeta.join(" • ");

  const natsStoreMode = String(nats.store_dir_mode || "unknown").trim() || "unknown";
  const natsEmbedded = Boolean(nats.embedded);
  const httpAddr = String(system.http_addr || "").trim();
  const artifactsRoot = String(system.artifacts_root || "").trim();
  const watcherEnabled = Boolean(system.commit_watcher_enabled);
  const serverTime = String(system.time || "").trim();

  dom.text.healthLabel.textContent = httpAddr ? `HTTP ${httpAddr}` : "Runtime";
  const healthMeta = [
    `${natsEmbedded ? "embedded" : "external"} nats`,
    `${natsStoreMode} store`,
    watcherEnabled ? "watcher enabled" : "watcher disabled",
  ];
  if (artifactsRoot) {
    healthMeta.push(`artifacts ${artifactsRoot}`);
  }
  if (serverTime) {
    healthMeta.push(`updated ${toLocalTime(serverTime)}`);
  }
  if (state.system.error) {
    healthMeta.push("last refresh failed");
  }
  dom.text.healthMeta.textContent = healthMeta.join(" • ");
}

function renderAll() {
  renderStatus();
  renderWorkspaceShell();
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
