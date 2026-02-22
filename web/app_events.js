// DOM event bindings and frontend bootstrap initialization.
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
