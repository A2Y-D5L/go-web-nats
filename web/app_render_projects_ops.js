// Project rail rendering and operation timeline/progress/history rendering.
function makeSignalChip(text, className = "") {
  const classes = ["signal-chip"];
  if (className) classes.push(className);
  return makeElem("span", classes.join(" "), text);
}

function renderProjectsList() {
  const selected = getSelectedProject();
  const visible = getVisibleProjects();

  dom.text.projectStats.textContent = `${visible.length} shown • ${state.projects.length} total`;
  dom.containers.projects.replaceChildren();

  if (!visible.length) {
    dom.containers.projects.replaceChildren();
    if (state.projects.length) {
      renderEmptyState(dom.containers.projects, "No apps match this filter.");
      return;
    }

    const empty = makeElem("div", "empty-state empty-state-cta");
    empty.append(
      makeElem("p", "", "No apps yet."),
      makeElem("p", "helper-text", "Create one to start the flow.")
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
    const runtimeLabel = formatRuntimeLiteral(project.spec?.runtime).replace(/\s+\(recommended\)/i, "");
    const signals = makeElem("div", "project-signals");
    signals.append(
      makeSignalChip(runtimeLabel, "signal-chip-runtime"),
      makeSignalChip(`${envCount} env${envCount === 1 ? "" : "s"}`, "signal-chip-env"),
      makeSignalChip(`updated ${elapsedSince(project.updated_at)}`, "signal-chip-updated")
    );

    const idMeta = makeElem("p", "project-meta", `ID ${String(project.id || "").slice(0, 12)}`);
    const msgMeta = makeElem("p", "project-meta", project.status?.message || "No recent updates");

    item.append(titleRow, signals, idMeta, msgMeta);

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
    dom.text.selected.textContent = "Pick an app to unlock delivery controls.";
    return;
  }

  dom.text.selected.classList.remove("muted");

  const row1 = makeElem("div", "project-summary-row");
  row1.append(
    makeElem("strong", "", project.spec?.name || "(unnamed)"),
    makeBadge(project.status?.phase || "Unknown", project.status?.phase || "unknown")
  );

  const row2 = makeElem("div", "project-signals");
  row2.append(
    makeSignalChip(`ID ${String(project.id || "").slice(0, 12)}`, "signal-chip-id"),
    makeSignalChip(formatRuntimeLiteral(project.spec?.runtime).replace(/\s+\(recommended\)/i, ""), "signal-chip-runtime")
  );

  const envs = projectEnvironmentNames(project);
  const lastActivity = project.status?.last_op_kind ? operationLabel(project.status.last_op_kind) : "none";
  const row3 = makeElem("div", "project-signals");
  row3.append(
    makeSignalChip(`envs ${envs.join(", ")}`, "signal-chip-env"),
    makeSignalChip(`last ${lastActivity}`, "signal-chip-activity")
  );

  const row4 = makeElem(
    "p",
    "project-meta",
    project.status?.message || "Ready for next step."
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

function normalizeHistorySequence(value) {
  const parsed = Number(value || 0);
  if (!Number.isFinite(parsed) || parsed < 0) {
    return 0;
  }
  return parsed;
}

function operationHistorySummaryFromPayload(op) {
  const step = Array.isArray(op?.steps) && op.steps.length ? op.steps[op.steps.length - 1] : null;
  const summaryMessage = String(step?.message || op?.summary_message || "").trim();

  return {
    id: op.id,
    kind: op.kind,
    status: op.status,
    requested: op.requested,
    finished: op.finished,
    error: op.error,
    summary_message: summaryMessage,
    message: summaryMessage,
    last_event_sequence: normalizeHistorySequence(op.last_event_sequence),
    last_update_at: op.last_update_at || op.finished || op.requested,
  };
}

function shouldReplaceOperationHistoryEntry(existing, candidate) {
  if (!existing) {
    return true;
  }

  const existingTerminal = isTerminalOperationStatus(existing.status);
  const candidateTerminal = isTerminalOperationStatus(candidate.status);
  if (existingTerminal && !candidateTerminal) {
    return false;
  }
  if (!existingTerminal && candidateTerminal) {
    return true;
  }

  const existingSeq = normalizeHistorySequence(existing.last_event_sequence);
  const candidateSeq = normalizeHistorySequence(candidate.last_event_sequence);
  if (candidateSeq > existingSeq) {
    return true;
  }
  if (candidateSeq < existingSeq) {
    return false;
  }

  const existingUpdatedAt = dateValue(existing.last_update_at || existing.finished || existing.requested);
  const candidateUpdatedAt = dateValue(candidate.last_update_at || candidate.finished || candidate.requested);
  if (candidateUpdatedAt > existingUpdatedAt) {
    return true;
  }
  if (candidateUpdatedAt < existingUpdatedAt) {
    return false;
  }

  const existingDetailLen = String(existing.error || existing.summary_message || existing.message || "").trim().length;
  const candidateDetailLen = String(
    candidate.error || candidate.summary_message || candidate.message || ""
  ).trim().length;
  return candidateDetailLen > existingDetailLen;
}

function upsertOperationHistory(op) {
  if (!op?.id) return;

  const summary = operationHistorySummaryFromPayload(op);

  const index = state.operation.history.findIndex((item) => item.id === op.id);
  if (index >= 0) {
    if (shouldReplaceOperationHistoryEntry(state.operation.history[index], summary)) {
      state.operation.history[index] = summary;
    }
  } else {
    state.operation.history.unshift(summary);
  }

  state.operation.history.sort((a, b) => {
    const requestedDiff = dateValue(b.requested) - dateValue(a.requested);
    if (requestedDiff !== 0) {
      return requestedDiff;
    }
    const sequenceDiff =
      normalizeHistorySequence(b.last_event_sequence) - normalizeHistorySequence(a.last_event_sequence);
    if (sequenceDiff !== 0) {
      return sequenceDiff;
    }
    return dateValue(b.last_update_at) - dateValue(a.last_update_at);
  });
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
    renderEmptyState(dom.containers.opProgress, "No active operation.");
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
    `${doneCount}/${order.length || 0} steps • duration ${duration(op.requested, op.finished)}`
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
    renderEmptyState(dom.containers.opTimeline, "Steps appear while operations run.");
    return;
  }

  const order = workerOrderForKind(op.kind);

  if (!order.length) {
    renderEmptyState(dom.containers.opTimeline, "No known worker path for this operation.");
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
      bits.push("queued");
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
    renderEmptyState(dom.containers.opHistory, "Loading history...");
    return;
  }

  if (state.operation.historyError && !state.operation.history.length) {
    renderEmptyState(dom.containers.opHistory, `Activity history unavailable: ${state.operation.historyError}`);
    return;
  }

  if (!state.operation.history.length) {
    renderEmptyState(dom.containers.opHistory, "Completed operations appear here.");
    return;
  }

  if (state.operation.historyError) {
    dom.containers.opHistory.appendChild(
      makeElem("p", "history-item-meta", `History refresh warning: ${state.operation.historyError}`)
    );
  }
  if (state.operation.historyLoadMoreError) {
    dom.containers.opHistory.appendChild(
      makeElem("p", "history-item-meta", `Load-more warning: ${state.operation.historyLoadMoreError}`)
    );
  }

  const historyFragment = document.createDocumentFragment();
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
    historyFragment.appendChild(card);
  }
  dom.containers.opHistory.appendChild(historyFragment);

  if (state.operation.historyLoadingMore || state.operation.historyNextCursor) {
    const actions = makeElem("div", "history-actions");
    const loadMoreBtn = makeElem(
      "button",
      "btn btn-subtle",
      state.operation.historyLoadingMore ? "Loading older..." : "Load older"
    );
    loadMoreBtn.type = "button";
    loadMoreBtn.disabled = state.operation.historyLoading || state.operation.historyLoadingMore;
    loadMoreBtn.addEventListener("click", () => {
      void loadMoreOperationHistory({ silent: true }).catch((error) => {
        setStatus(`Activity history unavailable: ${error.message}`, "warning");
      });
    });
    actions.appendChild(loadMoreBtn);
    dom.containers.opHistory.appendChild(actions);
  }
}

function releaseTimelineStateClass(record) {
  const kind = String(record?.op_kind || "").trim();
  if (kind === "release") {
    return "warning";
  }
  return "done";
}

function renderReleaseTimelinePanel() {
  const container = dom.containers.releaseTimeline;
  const statusEl = dom.text.releaseTimelineStatus;
  const rollbackEl = dom.text.rollbackTargetSummary;
  const refreshBtn = dom.buttons.refreshReleaseTimeline;
  const rollbackReviewBtn = dom.buttons.openRollbackModal;
  const envSelect = dom.inputs.releaseTimelineEnvironment;

  container.replaceChildren();

  const project = getSelectedProject();
  if (!project) {
    envSelect.replaceChildren();
    envSelect.disabled = true;
    refreshBtn.disabled = true;
    rollbackReviewBtn.disabled = true;
    rollbackEl.textContent = "Rollback target not selected. Choose a release entry to prepare rollback context.";
    setPanelInlineStatus(statusEl, "Select an app to inspect release timeline.", "info");
    renderEmptyState(container, "Release records appear after environment deliveries complete.");
    return;
  }

  const environment = ensureReleaseTimelineSelection(project);
  envSelect.disabled = state.releaseTimeline.loading || state.releaseTimeline.loadingMore;
  refreshBtn.disabled = state.releaseTimeline.loading || state.releaseTimeline.loadingMore || !environment;

  const selectedRecord = state.releaseTimeline.items.find(
    (item) => String(item?.id || "").trim() === state.releaseTimeline.selectedReleaseID
  );
  if (selectedRecord) {
    rollbackEl.textContent = `Rollback target prepared: ${String(selectedRecord.id || "").slice(0, 8)} in ${
      selectedRecord.environment || "environment"
    } (${selectedRecord.image || "unknown image"}).`;
    state.rollback.environment = String(selectedRecord.environment || "").trim().toLowerCase();
    state.rollback.releaseID = String(selectedRecord.id || "").trim();
  } else {
    rollbackEl.textContent = "No rollback target selected.";
    state.rollback.environment = "";
    state.rollback.releaseID = "";
  }
  rollbackReviewBtn.disabled =
    !selectedRecord || state.releaseTimeline.loading || state.releaseTimeline.loadingMore || projectHasRunningOperation();

  if (state.releaseTimeline.loading && !state.releaseTimeline.items.length) {
    setPanelInlineStatus(statusEl, `Loading ${environment || "selected"} release timeline...`, "info");
    renderEmptyState(container, "Loading release records...");
    return;
  }

  if (state.releaseTimeline.error && !state.releaseTimeline.items.length) {
    setPanelInlineStatus(statusEl, `Release timeline unavailable: ${state.releaseTimeline.error}`, "warning");
    renderEmptyState(container, "Release timeline data could not be loaded.");
    return;
  }

  if (!state.releaseTimeline.items.length) {
    setPanelInlineStatus(
      statusEl,
      `No release records yet for ${environment || "selected"} environment.`,
      "info"
    );
    renderEmptyState(container, "Release records appear after deploy/promote/release.");
    return;
  }

  setPanelInlineStatus(
    statusEl,
    `${state.releaseTimeline.items.length} release records loaded for ${
      environment || "selected"
    } environment.`,
    "success"
  );

  if (state.releaseTimeline.loadMoreError) {
    container.appendChild(
      makeElem("p", "history-item-meta", `Load-more warning: ${state.releaseTimeline.loadMoreError}`)
    );
  }

  for (const record of state.releaseTimeline.items) {
    const row = makeElem("article", `timeline-step timeline-step--${releaseTimelineStateClass(record)}`);
    const head = makeElem("div", "timeline-step-head");
    head.append(
      makeElem(
        "span",
        "timeline-step-title",
        `${String(record.delivery_stage || record.op_kind || "delivery").toUpperCase()} • ${String(record.id || "").slice(0, 8)}`
      ),
      makeBadge(String(record.environment || "unknown"), "live")
    );

    const bits = [
      `created ${toLocalTime(record.created_at)}`,
      `op ${String(record.op_kind || "unknown")}`,
      `from ${String(record.from_env || "-")}`,
      `to ${String(record.to_env || record.environment || "-")}`,
      `image ${String(record.image || "unknown")}`,
    ];
    if (record.rendered_path) {
      bits.push(`artifact ${record.rendered_path}`);
    }
    row.append(head, makeElem("p", "timeline-step-meta", bits.join(" • ")));

    const actions = makeElem("div", "history-actions");
    const prepareBtn = makeElem(
      "button",
      "btn btn-subtle",
      state.releaseTimeline.selectedReleaseID === record.id ? "Selected" : "Use for rollback"
    );
    prepareBtn.type = "button";
    prepareBtn.addEventListener("click", () => {
      state.releaseTimeline.selectedReleaseID = String(record.id || "").trim();
      state.releaseTimeline.environment = String(record.environment || state.releaseTimeline.environment || "")
        .trim()
        .toLowerCase();
      resetRollbackReviewState();
      state.rollback.environment = state.releaseTimeline.environment;
      state.rollback.releaseID = state.releaseTimeline.selectedReleaseID;
      renderReleaseTimelinePanel();
      setStatus(
        `Rollback target prepared from release ${String(record.id || "").slice(0, 8)}. Review rollback to run preflight.`,
        "info",
        { toast: true }
      );
    });
    actions.appendChild(prepareBtn);

    row.appendChild(actions);
    container.appendChild(row);
  }

  if (state.releaseTimeline.loadingMore || state.releaseTimeline.nextCursor) {
    const actions = makeElem("div", "history-actions");
    const loadMoreBtn = makeElem(
      "button",
      "btn btn-subtle",
      state.releaseTimeline.loadingMore ? "Loading older..." : "Load older"
    );
    loadMoreBtn.type = "button";
    loadMoreBtn.disabled = state.releaseTimeline.loading || state.releaseTimeline.loadingMore;
    loadMoreBtn.addEventListener("click", () => {
      void loadReleaseTimeline({ silent: true, append: true }).catch((error) => {
        setStatus(`Release timeline unavailable: ${error.message}`, "warning");
      });
    });
    actions.appendChild(loadMoreBtn);
    container.appendChild(actions);
  }
}

function renderOperationPanel() {
  const op = state.operation.payload;
  renderOperationProgress(op);
  renderOperationErrorSurface(op);
  renderOperationTimeline(op);
  renderOperationHistory();
  renderReleaseTimelinePanel();
  renderOperationTransportStatus(op);
  dom.text.opRaw.textContent = op ? pretty(op) : "";
}

function renderOperationTransportStatus(op) {
  if (!dom.text.opTransportStatus) return;

  if (!op) {
    setPanelInlineStatus(dom.text.opTransportStatus, "Realtime stream connects when operation starts.", "info");
    return;
  }

  const terminal = isTerminalOperationStatus(op.status);
  if (state.operation.usingPolling && !terminal) {
    setPanelInlineStatus(
      dom.text.opTransportStatus,
      "Realtime unavailable. Using polling fallback.",
      "warning"
    );
    return;
  }

  if (state.operation.eventSource && !terminal) {
    setPanelInlineStatus(
      dom.text.opTransportStatus,
      "Realtime connected. Steps update live.",
      "success"
    );
    return;
  }

  if (!terminal) {
    setPanelInlineStatus(dom.text.opTransportStatus, "Connecting realtime stream...", "info");
    return;
  }

  if (op.status === "done") {
    setPanelInlineStatus(
      dom.text.opTransportStatus,
      "Operation completed. Review timeline and outputs.",
      "success"
    );
    return;
  }

  setPanelInlineStatus(
    dom.text.opTransportStatus,
    "Operation failed. Review hints and retry.",
    "error"
  );
}

function projectHasRunningOperation() {
  return Boolean(state.operation.payload && !isTerminalOperationStatus(state.operation.payload.status));
}
