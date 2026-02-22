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
