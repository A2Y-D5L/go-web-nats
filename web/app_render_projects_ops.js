// Project rail rendering and operation timeline/progress/history rendering.
function renderProjectsList() {
  const selected = getSelectedProject();
  const visible = getVisibleProjects();

  dom.text.projectStats.textContent = `${visible.length} visible of ${state.projects.length}`;
  dom.containers.projects.replaceChildren();

  if (!visible.length) {
    dom.containers.projects.replaceChildren();
    if (state.projects.length) {
      renderEmptyState(dom.containers.projects, "No apps match current filters. Try broadening search or status.");
      return;
    }

    const empty = makeElem("div", "empty-state empty-state-cta");
    empty.append(
      makeElem("p", "", "No apps created yet."),
      makeElem("p", "helper-text", "Create your first app to open a dedicated delivery workspace.")
    );
    const cta = makeElem("button", "btn btn-primary", "Create app");
    cta.type = "button";
    cta.addEventListener("click", () => {
      openModal("create");
    });
    empty.appendChild(cta);
    dom.containers.projects.appendChild(empty);
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
  const hasRunningOperation = hasSelection && projectHasRunningOperation();

  dom.buttons.openUpdateModal.disabled = !hasSelection || hasRunningOperation;
  dom.buttons.openDeleteModal.disabled = !hasSelection || hasRunningOperation;
  dom.buttons.loadArtifacts.disabled = !hasSelection;
  dom.buttons.webhook.disabled = !hasSelection || hasRunningOperation;

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
  const summaryMessage = step?.message || op.summary_message || "";
  const summary = {
    id: op.id,
    kind: op.kind,
    status: op.status,
    requested: op.requested,
    finished: op.finished,
    error: op.error,
    summary_message: summaryMessage,
    message: summaryMessage,
    last_event_sequence: Number(op.last_event_sequence || 0),
    last_update_at: op.last_update_at || op.finished || op.requested,
  };

  const index = state.operation.history.findIndex((item) => item.id === op.id);
  if (index >= 0) {
    state.operation.history[index] = summary;
  } else {
    state.operation.history.unshift(summary);
  }

  state.operation.history.sort((a, b) => dateValue(b.requested) - dateValue(a.requested));
  state.operation.history = state.operation.history.slice(0, 20);
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

  if (state.operation.historyLoading && !state.operation.history.length) {
    renderEmptyState(dom.containers.opHistory, "Loading activity history...");
    return;
  }

  if (state.operation.historyError && !state.operation.history.length) {
    renderEmptyState(dom.containers.opHistory, `Activity history unavailable: ${state.operation.historyError}`);
    return;
  }

  if (!state.operation.history.length) {
    renderEmptyState(dom.containers.opHistory, "Completed app activities will be listed here.");
    return;
  }

  if (state.operation.historyError) {
    dom.containers.opHistory.appendChild(
      makeElem("p", "history-item-meta", `History refresh warning: ${state.operation.historyError}`)
    );
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
      )} • updated ${toLocalTime(item.last_update_at)}`
    );

    const detail = makeElem(
      "p",
      "history-item-meta",
      item.error || item.summary_message || item.message || "No detail message."
    );

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
  renderOperationTransportStatus(op);
  dom.text.opRaw.textContent = op ? pretty(op) : "";
}

function renderOperationTransportStatus(op) {
  if (!dom.text.opTransportStatus) return;

  if (!op) {
    setPanelInlineStatus(dom.text.opTransportStatus, "Realtime stream connects after an action starts.", "info");
    return;
  }

  const terminal = isTerminalOperationStatus(op.status);
  if (state.operation.usingPolling && !terminal) {
    setPanelInlineStatus(
      dom.text.opTransportStatus,
      "Realtime stream unavailable. Using polling fallback for activity updates.",
      "warning"
    );
    return;
  }

  if (state.operation.eventSource && !terminal) {
    setPanelInlineStatus(
      dom.text.opTransportStatus,
      "Realtime stream connected. Steps update live without refreshing.",
      "success"
    );
    return;
  }

  if (!terminal) {
    setPanelInlineStatus(dom.text.opTransportStatus, "Connecting realtime activity stream...", "info");
    return;
  }

  if (op.status === "done") {
    setPanelInlineStatus(
      dom.text.opTransportStatus,
      "Activity completed. Review timeline and outputs for the final result.",
      "success"
    );
    return;
  }

  setPanelInlineStatus(
    dom.text.opTransportStatus,
    "Activity failed. Review recovery hints and retry when ready.",
    "error"
  );
}

function projectHasRunningOperation() {
  return Boolean(state.operation.payload && !isTerminalOperationStatus(state.operation.payload.status));
}
