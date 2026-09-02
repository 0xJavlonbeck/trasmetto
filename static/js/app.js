(() => {
  let form;
  let fileInput;
  let submitButton;
  let selectedFile;
  let uploadState;
  let inlineError;
  let maxUploadBytes = 0;

  const syncSubmitState = () => {
    if (!fileInput || !submitButton) {
      return;
    }
    const validationError = selectedFilesLimitError();
    setInlineError(validationError);
    submitButton.disabled = fileInput.files.length === 0 || validationError !== "";
    if (!selectedFile) {
      return;
    }
    if (fileInput.files.length === 0) {
      selectedFile.textContent = "No file selected";
      return;
    }
    if (fileInput.files.length === 1) {
      selectedFile.textContent = fileInput.files[0].name;
      return;
    }
    selectedFile.textContent = `${fileInput.files.length} files selected`;
  };

  const selectedFilesSize = () => {
    if (!fileInput) {
      return 0;
    }

    return Array.from(fileInput.files).reduce((total, file) => total + file.size, 0);
  };

  const selectedFilesLimitError = () => {
    if (!fileInput || maxUploadBytes <= 0 || fileInput.files.length === 0) {
      return "";
    }

    const size = selectedFilesSize();
    if (size <= maxUploadBytes) {
      return "";
    }

    return `Upload limit ${formatBytes(maxUploadBytes)} exceeded`;
  };

  const formatBytes = (bytes) => {
    if (bytes >= 1024 * 1024 * 1024) {
      return `${trimNumber(bytes / (1024 * 1024 * 1024))}GB`;
    }
    if (bytes >= 1024 * 1024) {
      return `${trimNumber(bytes / (1024 * 1024))}MB`;
    }
    if (bytes >= 1024) {
      return `${trimNumber(bytes / 1024)}KB`;
    }
    return `${bytes}B`;
  };

  const trimNumber = (value) => {
    return value.toFixed(1).replace(/\.0$/, "");
  };

  const setInlineError = (message) => {
    if (!inlineError) {
      return;
    }

    const text = message.trim();
    inlineError.textContent = text;
    inlineError.hidden = text === "";
    if (selectedFile) {
      selectedFile.hidden = text !== "";
    }
  };

  const setUploadState = (text) => {
    const state = uploadState || document.querySelector("[data-upload-state]");
    if (state) {
      state.textContent = text;
    }
  };

  const setProgress = (percent) => {
    const value = Math.max(0, Math.min(100, Math.round(percent)));
    setUploadState(`Uploading ${value}%`);
  };

  const resetProgress = () => {
    setUploadState("");
  };

  const showError = (message) => {
    setInlineError(message);
    const errorBox = document.querySelector("[data-upload-error]");
    if (!errorBox) {
      return;
    }
    errorBox.textContent = "";
    errorBox.hidden = true;
  };

  const clearError = () => {
    const errorBox = document.querySelector("[data-upload-error]");
    if (!errorBox) {
      return;
    }
    setInlineError("");
    errorBox.textContent = "";
    errorBox.hidden = true;
  };

  const redirectToLogin = () => {
    const base = document.body.dataset.loginUrl;
    if (!base) {
      return false;
    }
    window.location.assign(`${base}?next=${encodeURIComponent(window.location.pathname + window.location.search)}`);
    return true;
  };

  // The flag lives on <body> so it does not depend on the upload form being
  // rendered; a read-only directory hides that form but still shows Delete.
  const uploadNeedsLogin = () => {
    if (document.body.dataset.writeNeedsLogin === "1") {
      return true;
    }
    const form = document.querySelector("[data-upload-form]");
    return !!(form && form.dataset.uploadNeedsLogin === "1");
  };

  const clearUploadNeedsLogin = () => {
    delete document.body.dataset.writeNeedsLogin;
    const form = document.querySelector("[data-upload-form]");
    if (form) {
      delete form.dataset.uploadNeedsLogin;
    }
  };

  const openLoginModal = () =>
    new Promise((resolve) => {
      const overlay = document.querySelector("[data-login-modal]");
      const form = document.querySelector("[data-login-form]");
      const userEl = document.querySelector("[data-login-user]");
      const passEl = document.querySelector("[data-login-pass]");
      const errEl = document.querySelector("[data-login-error]");
      const cancelBtn = document.querySelector("[data-login-cancel]");
      const submitBtn = document.querySelector("[data-login-submit]");
      const loginUrl = document.body.dataset.loginUrl;
      if (!overlay || !form || !loginUrl) {
        resolve(false);
        return;
      }

      userEl.value = "";
      passEl.value = "";
      setLoginError(errEl, "");
      submitBtn.disabled = false;
      overlay.hidden = false;
      userEl.focus();

      const close = (result) => {
        overlay.hidden = true;
        form.removeEventListener("submit", onSubmit);
        cancelBtn.removeEventListener("click", onCancel);
        overlay.removeEventListener("click", onOverlay);
        document.removeEventListener("keydown", onKey);
        resolve(result);
      };
      const onCancel = () => close(false);
      const onOverlay = (event) => {
        if (event.target === overlay) {
          close(false);
        }
      };
      const onKey = (event) => {
        if (event.key === "Escape") {
          close(false);
        }
      };
      const onSubmit = async (event) => {
        event.preventDefault();
        setLoginError(errEl, "");
        submitBtn.disabled = true;
        const body = new URLSearchParams();
        body.append("username", userEl.value);
        body.append("password", passEl.value);
        try {
          const res = await fetch(loginUrl, {
            method: "POST",
            headers: { "X-Trasmetto-Login": "1", "Content-Type": "application/x-www-form-urlencoded" },
            body,
          });
          submitBtn.disabled = false;
          if (res.ok) {
            clearUploadNeedsLogin();
            close(true);
            return;
          }
          setLoginError(errEl, "Authentication failed. Check your username and password.");
          passEl.focus();
        } catch {
          submitBtn.disabled = false;
          setLoginError(errEl, "Login failed: network error.");
        }
      };

      form.addEventListener("submit", onSubmit);
      cancelBtn.addEventListener("click", onCancel);
      overlay.addEventListener("click", onOverlay);
      document.addEventListener("keydown", onKey);
    });

  const setLoginError = (box, message) => {
    if (!box) {
      return;
    }
    box.textContent = message;
    box.hidden = message === "";
  };

  const requireUploadAuth = async () => {
    if (!uploadNeedsLogin()) {
      return true;
    }
    return openLoginModal();
  };

  const syncFilterBar = (doc) => {
    const next = doc.querySelector("[data-filter-bar]");
    const current = document.querySelector("[data-filter-bar]");
    if (current && next) {
      current.replaceWith(next);
    } else if (current) {
      current.remove();
    } else if (next) {
      const listing = document.querySelector(".listing");
      if (listing) {
        listing.before(next);
      }
    }
  };

  const refreshListing = async () => {
    try {
      const response = await fetch(window.location.pathname, {
        headers: { "X-Trasmetto-Partial": "1" },
      });
      const html = await response.text();
      const doc = new DOMParser().parseFromString(html, "text/html");
      const nextListing = doc.querySelector(".listing");
      const listing = document.querySelector(".listing");
      if (listing && nextListing) {
        listing.replaceWith(nextListing);
        syncFilterBar(doc);
        bindFilter();
        updateZipBar();
        // The rows are new elements, so the remembered order must be retaken
        // before the chosen sort is re-applied.
        captureServerOrder();
        applySort();
      }
    } catch {
    }
  };

  const handleSubmit = (event) => {
    event.preventDefault();
    if (fileInput.files.length === 0) {
      syncSubmitState();
      return;
    }
    const validationError = selectedFilesLimitError();
    if (validationError !== "") {
      setInlineError(validationError);
      syncSubmitState();
      return;
    }

    clearError();
    setProgress(0);
    submitButton.disabled = true;

    const request = new XMLHttpRequest();
    request.open("POST", form.action);

    request.upload.addEventListener("progress", (uploadEvent) => {
      if (!uploadEvent.lengthComputable) {
        setUploadState("Uploading");
        return;
      }
      setProgress((uploadEvent.loaded / uploadEvent.total) * 100);
    });

    request.addEventListener("load", () => {
      if (request.status >= 200 && request.status < 300) {
        setProgress(100);
        fileInput.value = "";
        syncSubmitState();
        setUploadState("File accepted");
        refreshListing();
        return;
      }

      if (request.status === 401) {
        resetProgress();
        submitButton.disabled = fileInput.files.length === 0 || selectedFilesLimitError() !== "";
        openLoginModal();
        return;
      }
      resetProgress();
      showError(request.responseText || `upload failed: HTTP ${request.status}`);
      submitButton.disabled = fileInput.files.length === 0 || selectedFilesLimitError() !== "";
    });

    request.addEventListener("error", () => {
      resetProgress();
      showError("upload failed: connection closed before server response; check timeout settings");
      submitButton.disabled = fileInput.files.length === 0 || selectedFilesLimitError() !== "";
    });

    request.addEventListener("timeout", () => {
      resetProgress();
      showError("upload failed: request timed out");
      submitButton.disabled = fileInput.files.length === 0 || selectedFilesLimitError() !== "";
    });

    request.addEventListener("abort", () => {
      resetProgress();
      showError("upload cancelled");
      submitButton.disabled = fileInput.files.length === 0 || selectedFilesLimitError() !== "";
    });

    request.send(new FormData(form));
  };

  const randomStem = () => {
    const chars = "abcdefghijklmnopqrstuvwxyz0123456789";
    let out = "";
    for (let i = 0; i < 6; i += 1) {
      out += chars[Math.floor(Math.random() * chars.length)];
    }
    return out;
  };

  const setPasteError = (box, message) => {
    if (!box) {
      return;
    }
    box.textContent = message;
    box.hidden = message === "";
  };

  const sendPaste = (action, text, name, onError, onDone) => {
    const blob = new Blob([text], { type: "text/plain" });
    if (maxUploadBytes > 0 && blob.size > maxUploadBytes) {
      onError(`Upload limit ${formatBytes(maxUploadBytes)} exceeded`);
      return;
    }

    const data = new FormData();
    data.append("files", blob, name);

    clearError();
    setProgress(0);

    const request = new XMLHttpRequest();
    request.open("POST", action || window.location.pathname);
    request.upload.addEventListener("progress", (uploadEvent) => {
      if (!uploadEvent.lengthComputable) {
        setUploadState("Uploading");
        return;
      }
      setProgress((uploadEvent.loaded / uploadEvent.total) * 100);
    });
    request.addEventListener("load", () => {
      if (request.status >= 200 && request.status < 300) {
        setProgress(100);
        let savedName = "";
        const header = request.getResponseHeader("X-Trasmetto-Saved");
        if (header) {
          try {
            savedName = decodeURIComponent(header.split(",")[0]);
          } catch {
            savedName = header.split(",")[0];
          }
        }
        refreshListing();
        onDone(savedName || name);
        return;
      }
      if (request.status === 401) {
        resetProgress();
        onError("");
        openLoginModal();
        return;
      }
      resetProgress();
      onError(request.responseText || `save failed: HTTP ${request.status}`);
    });
    request.addEventListener("error", () => {
      resetProgress();
      onError("save failed: connection closed before server response; check timeout settings");
    });
    request.addEventListener("timeout", () => {
      resetProgress();
      onError("save failed: request timed out");
    });
    request.send(data);
  };

  const openPasteModal = (action) => {
    const overlay = document.querySelector("[data-paste-modal]");
    const content = document.querySelector("[data-paste-content]");
    const nameInput = document.querySelector("[data-paste-name]");
    const errorBox = document.querySelector("[data-paste-error]");
    const saveBtn = document.querySelector("[data-paste-save]");
    const cancelBtn = document.querySelector("[data-paste-cancel]");
    if (!overlay || !content || !nameInput || !saveBtn || !cancelBtn) {
      return;
    }

    content.value = "";
    nameInput.value = "";
    setPasteError(errorBox, "");
    saveBtn.disabled = false;
    overlay.hidden = false;
    content.focus();

    const close = () => {
      overlay.hidden = true;
      saveBtn.removeEventListener("click", onSave);
      cancelBtn.removeEventListener("click", onCancel);
      overlay.removeEventListener("click", onOverlay);
      document.removeEventListener("keydown", onKey);
    };
    const onCancel = () => close();
    const onOverlay = (event) => {
      if (event.target === overlay) {
        close();
      }
    };
    const onKey = (event) => {
      if (event.key === "Escape") {
        close();
      }
    };
    const onSave = () => {
      const text = content.value;
      if (text === "") {
        setPasteError(errorBox, "Nothing to save: paste some content first.");
        return;
      }
      let name = nameInput.value.trim();
      const autoNamed = name === "";
      if (autoNamed) {
        name = `note-${randomStem()}.txt`;
      }
      setPasteError(errorBox, "");
      saveBtn.disabled = true;
      sendPaste(
        action,
        text,
        name,
        (message) => {
          saveBtn.disabled = false;
          setPasteError(errorBox, message);
        },
        (savedName) => {
          setUploadState(autoNamed ? `Saved as ${savedName}` : "File accepted");
          close();
        },
      );
    };

    saveBtn.addEventListener("click", onSave);
    cancelBtn.addEventListener("click", onCancel);
    overlay.addEventListener("click", onOverlay);
    document.addEventListener("keydown", onKey);
  };

  const bindUploadForm = () => {
    form = document.querySelector("[data-upload-form]");
    if (!form) {
      return;
    }

    fileInput = form.querySelector('input[type="file"]');
    submitButton = form.querySelector('button[type="submit"]');
    selectedFile = form.querySelector("[data-selected-file]");
    uploadState = form.querySelector("[data-upload-state]");
    inlineError = form.querySelector("[data-upload-inline-error]");
    maxUploadBytes = Number.parseInt(form.dataset.maxUploadBytes || "0", 10);
    if (Number.isNaN(maxUploadBytes)) {
      maxUploadBytes = 0;
    }
    if (!fileInput || !submitButton || form.dataset.uploadBound === "1") {
      syncSubmitState();
      return;
    }

    form.dataset.uploadBound = "1";
    fileInput.addEventListener("change", () => {
      resetProgress();
      syncSubmitState();
    });
    const pasteOpen = form.querySelector("[data-paste-open]");
    if (pasteOpen) {
      pasteOpen.addEventListener("click", async () => {
        if (await requireUploadAuth()) {
          openPasteModal(form.action);
        }
      });
    }
    form.addEventListener("submit", handleSubmit);
    syncSubmitState();
  };

  const ensureLockedNotice = (uploadSlot) => {
    let locked = uploadSlot.querySelector("[data-upload-locked]");
    if (locked) {
      return locked;
    }

    locked = document.createElement("div");
    locked.className = "upload-locked";
    locked.dataset.uploadLocked = "";
    uploadSlot.prepend(locked);
    return locked;
  };

  const syncUploadSlot = (doc) => {
    const uploadSlot = document.querySelector(".upload-slot");
    const nextSlot = doc.querySelector(".upload-slot");
    if (!uploadSlot || !nextSlot) {
      return;
    }

    const currentForm = uploadSlot.querySelector("[data-upload-form]");
    const currentFileInput = currentForm && currentForm.querySelector('input[type="file"]');
    const shouldPreserveForm = currentForm && currentFileInput && currentFileInput.files.length > 0;
    if (!shouldPreserveForm) {
      uploadSlot.replaceWith(nextSlot);
      bindUploadForm();
      return;
    }

    const nextForm = nextSlot.querySelector("[data-upload-form]");
    const nextLocked = nextSlot.querySelector("[data-upload-locked]");
    const locked = ensureLockedNotice(uploadSlot);

    if (nextForm) {
      currentForm.hidden = false;
      currentForm.action = nextForm.getAttribute("action") || window.location.pathname;
      locked.hidden = true;
    } else if (nextLocked) {
      currentForm.hidden = true;
      locked.textContent = nextLocked.textContent;
      locked.hidden = false;
    } else {
      currentForm.hidden = true;
      locked.hidden = true;
    }

    bindUploadForm();
    syncSubmitState();
  };

  const applyPage = (doc) => {
    const nextStatusbar = doc.querySelector(".statusbar");
    const statusbar = document.querySelector(".statusbar");
    if (statusbar && nextStatusbar) {
      statusbar.replaceWith(nextStatusbar);
    }

    syncUploadSlot(doc);

    syncFilterBar(doc);

    const nextListing = doc.querySelector(".listing");
    const listing = document.querySelector(".listing");
    if (listing && nextListing) {
      listing.replaceWith(nextListing);
    }
    bindFilter();
    captureServerOrder();

    const nextZipBar = doc.querySelector("[data-zip-bar]");
    const zipBar = document.querySelector("[data-zip-bar]");
    if (zipBar && nextZipBar) {
      zipBar.replaceWith(nextZipBar);
    } else if (zipBar) {
      zipBar.remove();
    } else if (nextZipBar) {
      const currentListing = document.querySelector(".listing");
      if (currentListing) {
        currentListing.after(nextZipBar);
      }
    }
    updateZipBar();
    applySort();

    const nextTitle = doc.querySelector("title");
    if (nextTitle) {
      document.title = nextTitle.textContent;
    }
  };

  const navigateTo = async (url, replace = false) => {
    try {
      const response = await fetch(url, {
        headers: { "X-Trasmetto-Partial": "1" },
      });
      const contentType = response.headers.get("content-type") || "";
      if (!contentType.includes("text/html")) {
        return;
      }
      const html = await response.text();
      const doc = new DOMParser().parseFromString(html, "text/html");
      applyPage(doc);
      if (replace) {
        history.replaceState(null, "", url);
      } else {
        history.pushState(null, "", url);
      }
    } catch {
      window.location.assign(url);
    }
  };

  const openModal = ({ message, confirmLabel = "download", showCancel = true }) =>
    new Promise((resolve) => {
      const overlay = document.querySelector("[data-modal]");
      const messageEl = document.querySelector("[data-modal-message]");
      const confirmBtn = document.querySelector("[data-modal-confirm]");
      const cancelBtn = document.querySelector("[data-modal-cancel]");
      if (!overlay || !messageEl || !confirmBtn || !cancelBtn) {
        resolve(false);
        return;
      }

      messageEl.textContent = message;
      confirmBtn.textContent = confirmLabel;
      cancelBtn.hidden = !showCancel;
      overlay.hidden = false;

      const close = (result) => {
        overlay.hidden = true;
        confirmBtn.removeEventListener("click", onConfirm);
        cancelBtn.removeEventListener("click", onCancel);
        overlay.removeEventListener("click", onOverlay);
        document.removeEventListener("keydown", onKey);
        resolve(result);
      };
      const onConfirm = () => close(true);
      const onCancel = () => close(false);
      const onOverlay = (event) => {
        if (event.target === overlay) {
          close(false);
        }
      };
      const onKey = (event) => {
        if (event.key === "Escape") {
          close(false);
        }
      };

      confirmBtn.addEventListener("click", onConfirm);
      cancelBtn.addEventListener("click", onCancel);
      overlay.addEventListener("click", onOverlay);
      document.addEventListener("keydown", onKey);
      confirmBtn.focus();
    });

  const showAlert = (message) => openModal({ message, confirmLabel: "ok", showCancel: false });
  const showConfirm = (message) => openModal({ message });

  const selectedItems = () =>
    Array.from(document.querySelectorAll(".row-check:checked")).map((c) => c.value);

  const formatSize = (bytes) => {
    if (!Number.isFinite(bytes) || bytes < 0) {
      return "";
    }
    const units = ["B", "KB", "MB", "GB", "TB"];
    let value = bytes;
    let unit = 0;
    while (value >= 1024 && unit < units.length - 1) {
      value /= 1024;
      unit += 1;
    }
    const num = unit === 0 ? value : value >= 100 ? Math.round(value) : value.toFixed(1).replace(/\.0$/, "");
    return `${num} ${units[unit]}`;
  };

  const submitSelectionForm = (url, items) => {
    const form = document.createElement("form");
    form.method = "POST";
    form.action = url;
    form.style.display = "none";
    for (const item of items) {
      const input = document.createElement("input");
      input.type = "hidden";
      input.name = "items";
      input.value = item;
      form.appendChild(input);
    }
    document.body.appendChild(form);
    form.submit();
    form.remove();
  };

  const precheckZip = async (url, items) => {
    try {
      let res;
      if (items.length === 0) {
        res = await fetch(url, { method: "HEAD" });
      } else {
        const body = new URLSearchParams();
        for (const item of items) {
          body.append("items", item);
        }
        res = await fetch(url, {
          method: "POST",
          headers: { "X-Trasmetto-Precheck": "1", "Content-Type": "application/x-www-form-urlencoded" },
          body,
        });
      }
      if (res.ok) {
        const size = Number.parseInt(res.headers.get("X-Trasmetto-Zip-Size") || "", 10);
        return { ok: true, size: Number.isNaN(size) ? null : size };
      }
      if (res.status === 401 && redirectToLogin()) {
        return { ok: false, error: "" };
      }
      return { ok: false, error: res.headers.get("X-Trasmetto-Zip-Error") || `zip failed: HTTP ${res.status}` };
    } catch {
      return { ok: false, error: "zip failed: network error" };
    }
  };

  const sortState = { key: "", descending: false };
  let serverOrder = [];

  const captureServerOrder = () => {
    const body = document.querySelector(".listing tbody");
    serverOrder = body ? Array.from(body.querySelectorAll("tr[data-name]")) : [];
  };

  const rowSortValue = (row, key) => {
    if (key === "size") {
      return Number(row.dataset.bytes || 0);
    }
    return (row.dataset.name || "").toLowerCase();
  };

  const restoreRows = (body, rows) => {
    const parent = body.querySelector("tr.parent");
    rows.forEach((row) => body.appendChild(row));
    if (parent) {
      body.insertBefore(parent, body.firstChild);
    }
    const emptyRow = body.querySelector("[data-filter-empty]");
    if (emptyRow) {
      body.appendChild(emptyRow);
    }
  };

  const applySort = () => {
    const body = document.querySelector(".listing tbody");
    if (!body) {
      updateSortArrows();
      return;
    }
    if (!sortState.key) {
      if (serverOrder.length > 0) {
        restoreRows(body, serverOrder);
      }
      updateSortArrows();
      return;
    }
    const rows = Array.from(body.querySelectorAll("tr[data-name]"));
    if (rows.length === 0) {
      updateSortArrows();
      return;
    }
    rows.sort((left, right) => {
      // Directories, links and files stay in their own bands.
      const groupDiff = Number(left.dataset.group || 0) - Number(right.dataset.group || 0);
      if (groupDiff !== 0) {
        return groupDiff;
      }
      const a = rowSortValue(left, sortState.key);
      const b = rowSortValue(right, sortState.key);
      let result = 0;
      if (a < b) {
        result = -1;
      } else if (a > b) {
        result = 1;
      } else {
        // Equal sizes read better alphabetically than in arbitrary order.
        const nameA = rowSortValue(left, "name");
        const nameB = rowSortValue(right, "name");
        result = nameA < nameB ? -1 : nameA > nameB ? 1 : 0;
        return sortState.descending ? -result : result;
      }
      return sortState.descending ? -result : result;
    });
    restoreRows(body, rows);
    updateSortArrows();
  };

  function updateSortArrows() {
    document.querySelectorAll("[data-sort]").forEach((button) => {
      const arrow = button.querySelector(".sort-arrow");
      const active = button.dataset.sort === sortState.key;
      button.classList.toggle("sorted", active);
      if (arrow) {
        arrow.textContent = active ? (sortState.descending ? "\u2193" : "\u2191") : "";
      }
    });
  }

  const toggleSort = (key) => {
    if (sortState.key !== key) {
      sortState.key = key;
      sortState.descending = false;
    } else if (!sortState.descending) {
      sortState.descending = true;
    } else {
      // Third click clears the sort and restores the original listing.
      sortState.key = "";
      sortState.descending = false;
    }
    applySort();
  };

  // formatCount keeps the counter narrow: exact up to 999, then 1.2K, 1.2M.
  const formatCount = (value) => {
    if (value < 1000) {
      return String(value);
    }
    const units = [
      { limit: 1e9, suffix: "B" },
      { limit: 1e6, suffix: "M" },
      { limit: 1e3, suffix: "K" },
    ];
    for (const unit of units) {
      if (value >= unit.limit) {
        const scaled = value / unit.limit;
        // Truncate rather than round, so 1999 reads as 1.9K and the counter
        // never claims more items than are there. One decimal below 10.
        const text =
          scaled < 10
            ? String(Math.floor(scaled * 10) / 10)
            : String(Math.floor(scaled));
        return text + unit.suffix;
      }
    }
    return String(value);
  };

  const applyFilter = () => {
    const listing = document.querySelector(".listing");
    const input = document.querySelector("[data-filter-input]");
    if (!listing || !input) {
      return;
    }
    const query = input.value.trim().toLowerCase();
    const emptyRow = listing.querySelector("[data-filter-empty]");
    const countEl = document.querySelector("[data-filter-count]");

    let total = 0;
    let shown = 0;
    listing.querySelectorAll("tbody tr").forEach((row) => {
      if (row.classList.contains("parent") || row.hasAttribute("data-filter-empty") || row.querySelector("td.empty")) {
        return;
      }
      total += 1;
      const nameEl = row.querySelector(".name");
      const name = (nameEl ? nameEl.textContent : "").toLowerCase();
      const match = query === "" || name.includes(query);
      row.classList.toggle("filter-hidden", !match);
      if (match) {
        shown += 1;
      }
    });

    if (emptyRow) {
      emptyRow.hidden = !(query !== "" && shown === 0);
    }
    if (countEl) {
      countEl.textContent =
        query === ""
          ? `${formatCount(total)} item${total === 1 ? "" : "s"}`
          : `${formatCount(shown)} / ${formatCount(total)}`;
      countEl.title = query === "" ? `${total} items` : `${shown} of ${total} items`;
    }
    updateZipBar();
  };

  const bindFilter = () => {
    const input = document.querySelector("[data-filter-input]");
    if (!input) {
      return;
    }
    if (input.dataset.filterBound !== "1") {
      input.dataset.filterBound = "1";
      input.addEventListener("input", applyFilter);
      input.addEventListener("keydown", (event) => {
        if (event.key === "Escape") {
          input.value = "";
          applyFilter();
          input.blur();
        }
      });
    }
    applyFilter();
  };

  const updateZipBar = () => {
    const bar = document.querySelector("[data-zip-bar]");
    if (!bar) {
      return;
    }
    const button = bar.querySelector("[data-zip-download]");
    const info = bar.querySelector("[data-zip-info]");
    const count = selectedItems().length;
    if (button) {
      const allLabel = button.dataset.allLabel || "Download folder .zip";
      button.textContent = count === 0 ? allLabel : `Download ${formatCount(count)} selected .zip`;
    }
    if (info) {
      info.textContent =
        count === 0
          ? info.dataset.idleText || "Zips the whole folder unless items are checked"
          : `${formatCount(count)} item${count > 1 ? "s" : ""} selected`;
    }
    const deleteBtn = bar.querySelector("[data-delete-open]");
    if (deleteBtn) {
      deleteBtn.disabled = count === 0;
      deleteBtn.textContent = count === 0 ? "Delete" : `Delete ${formatCount(count)}`;
    }
  };

  const manageURL = () => {
    const bar = document.querySelector("[data-zip-bar]");
    return (bar && bar.dataset.manageUrl) || "";
  };

  const sendManage = (body, onError, onDone) => {
    const url = manageURL();
    if (!url) {
      onError("This folder cannot be modified.");
      return;
    }
    const request = new XMLHttpRequest();
    request.open("POST", url);
    request.setRequestHeader("Content-Type", "application/x-www-form-urlencoded");
    request.addEventListener("load", () => {
      if (request.status >= 200 && request.status < 300) {
        onDone();
        return;
      }
      onError((request.responseText || "The operation failed.").trim());
    });
    request.addEventListener("error", () => onError("Network error."));
    request.send(body);
  };

  const openMkdirModal = () => {
    const overlay = document.querySelector("[data-mkdir-modal]");
    const nameInput = overlay && overlay.querySelector("[data-mkdir-name]");
    const errorBox = overlay && overlay.querySelector("[data-mkdir-error]");
    const saveBtn = overlay && overlay.querySelector("[data-mkdir-save]");
    const cancelBtn = overlay && overlay.querySelector("[data-mkdir-cancel]");
    if (!overlay || !nameInput || !saveBtn || !cancelBtn) {
      return;
    }

    nameInput.value = "";
    setPasteError(errorBox, "");
    overlay.hidden = false;
    nameInput.focus();

    const close = () => {
      overlay.hidden = true;
      saveBtn.removeEventListener("click", onSave);
      cancelBtn.removeEventListener("click", close);
      overlay.removeEventListener("click", onOverlay);
      document.removeEventListener("keydown", onKey);
      nameInput.removeEventListener("keydown", onEnter);
    };
    const onOverlay = (event) => {
      if (event.target === overlay) {
        close();
      }
    };
    const onKey = (event) => {
      if (event.key === "Escape") {
        close();
      }
    };
    function onSave() {
      const name = nameInput.value.trim();
      if (name === "") {
        setPasteError(errorBox, "Enter a folder name.");
        return;
      }
      saveBtn.disabled = true;
      sendManage(
        "op=mkdir&name=" + encodeURIComponent(name),
        (message) => {
          saveBtn.disabled = false;
          setPasteError(errorBox, message);
        },
        () => {
          saveBtn.disabled = false;
          close();
          navigateTo(window.location.href, true);
        },
      );
    }
    const onEnter = (event) => {
      if (event.key === "Enter") {
        event.preventDefault();
        onSave();
      }
    };

    saveBtn.addEventListener("click", onSave);
    cancelBtn.addEventListener("click", close);
    overlay.addEventListener("click", onOverlay);
    document.addEventListener("keydown", onKey);
    nameInput.addEventListener("keydown", onEnter);
  };

  const openDeleteModal = () => {
    const items = selectedItems();
    if (items.length === 0) {
      return;
    }
    const overlay = document.querySelector("[data-delete-modal]");
    const list = overlay && overlay.querySelector("[data-delete-list]");
    const errorBox = overlay && overlay.querySelector("[data-delete-error]");
    const confirmBtn = overlay && overlay.querySelector("[data-delete-confirm]");
    const cancelBtn = overlay && overlay.querySelector("[data-delete-cancel]");
    if (!overlay || !confirmBtn || !cancelBtn) {
      return;
    }

    if (list) {
      list.textContent = items.join(", ");
    }
    setPasteError(errorBox, "");
    overlay.hidden = false;
    confirmBtn.focus();

    const close = () => {
      overlay.hidden = true;
      confirmBtn.removeEventListener("click", onConfirm);
      cancelBtn.removeEventListener("click", close);
      overlay.removeEventListener("click", onOverlay);
      document.removeEventListener("keydown", onKey);
    };
    const onOverlay = (event) => {
      if (event.target === overlay) {
        close();
      }
    };
    const onKey = (event) => {
      if (event.key === "Escape") {
        close();
      }
    };
    function onConfirm() {
      confirmBtn.disabled = true;
      const body = items.map((item) => "items=" + encodeURIComponent(item)).join("&");
      sendManage(
        "op=delete&" + body,
        (message) => {
          confirmBtn.disabled = false;
          setPasteError(errorBox, message);
        },
        () => {
          confirmBtn.disabled = false;
          close();
          navigateTo(window.location.href, true);
        },
      );
    }

    confirmBtn.addEventListener("click", onConfirm);
    cancelBtn.addEventListener("click", close);
    overlay.addEventListener("click", onOverlay);
    document.addEventListener("keydown", onKey);
  };

  // Once the archive is on its way the ticks have served their purpose;
  // leaving them on invites a second, unintended download.
  const clearSelection = () => {
    document.querySelectorAll(".row-check:checked").forEach((box) => {
      box.checked = false;
    });
    updateZipBar();
  };

  const handleZipClick = async (url) => {
    const items = selectedItems();
    const info = await precheckZip(url, items);
    if (!info.ok) {
      showAlert(info.error);
      return;
    }
    const label =
      items.length === 0
        ? "this folder"
        : `${items.length} selected item${items.length > 1 ? "s" : ""}`;
    const sizeSuffix = info.size != null ? ` [${formatSize(info.size)}]` : "";
    const confirmed = await showConfirm(`Download ${label} as a zip?${sizeSuffix}`);
    if (!confirmed) {
      return;
    }
    if (items.length === 0) {
      window.location.assign(url);
      return;
    }
    submitSelectionForm(url, items);
    clearSelection();
  };

  document.addEventListener("click", (event) => {
    const browse = event.target.closest(".file-button");
    if (browse && event.button === 0 && uploadNeedsLogin()) {
      event.preventDefault();
      requireUploadAuth().then((ok) => {
        if (ok) {
          const input = document.querySelector("[data-file-input]");
          if (input) {
            input.click();
          }
        }
      });
      return;
    }

    const sortBtn = event.target.closest("[data-sort]");
    if (sortBtn && event.button === 0) {
      toggleSort(sortBtn.dataset.sort);
      return;
    }

    // Ask for the login up front, the way upload and paste do, instead of
    // letting the user fill in the dialog and then rejecting it.
    const mkdirBtn = event.target.closest("[data-mkdir-open]");
    if (mkdirBtn && event.button === 0) {
      requireUploadAuth().then((ok) => {
        if (ok) {
          openMkdirModal();
        }
      });
      return;
    }

    const deleteBtn = event.target.closest("[data-delete-open]");
    if (deleteBtn && event.button === 0 && !deleteBtn.disabled) {
      requireUploadAuth().then((ok) => {
        if (ok) {
          openDeleteModal();
        }
      });
      return;
    }

    const zipBtn = event.target.closest("[data-zip-download]");
    if (zipBtn && event.button === 0) {
      const url = zipBtn.dataset.archiveUrl || "";
      if (url) {
        handleZipClick(url);
      }
      return;
    }

    const link = event.target.closest("[data-dir-link]");
    if (!link || event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
      return;
    }

    event.preventDefault();
    navigateTo(link.href);
  });

  window.addEventListener("popstate", () => {
    navigateTo(window.location.href, true);
  });

  document.addEventListener("change", (event) => {
    if (event.target.closest(".row-check")) {
      updateZipBar();
    }
  });

  const stageFiles = (files) => {
    const form = document.querySelector("[data-upload-form]");
    const input = form && form.querySelector('input[type="file"]');
    if (!input || !files || files.length === 0) {
      return;
    }
    const transfer = new DataTransfer();
    for (const file of files) {
      transfer.items.add(file);
    }
    input.files = transfer.files;
    input.dispatchEvent(new Event("change", { bubbles: true }));
  };

  const dragHasFiles = (event) =>
    !!event.dataTransfer && Array.from(event.dataTransfer.types || []).includes("Files");
  const uploadsEnabled = () => !!document.querySelector("[data-upload-form]");
  const setDropOverlay = (visible) => {
    const overlay = document.querySelector("[data-drop-overlay]");
    if (overlay) {
      overlay.hidden = !visible;
    }
  };
  let dragDepth = 0;

  document.addEventListener("dragenter", (event) => {
    if (!dragHasFiles(event)) {
      return;
    }
    event.preventDefault();
    dragDepth += 1;
    if (uploadsEnabled()) {
      setDropOverlay(true);
    }
  });
  document.addEventListener("dragover", (event) => {
    if (!dragHasFiles(event)) {
      return;
    }
    event.preventDefault();
    event.dataTransfer.dropEffect = uploadsEnabled() ? "copy" : "none";
  });
  document.addEventListener("dragleave", (event) => {
    if (!dragHasFiles(event)) {
      return;
    }
    dragDepth -= 1;
    if (dragDepth <= 0) {
      dragDepth = 0;
      setDropOverlay(false);
    }
  });
  document.addEventListener("drop", (event) => {
    if (!dragHasFiles(event)) {
      return;
    }
    event.preventDefault();
    dragDepth = 0;
    setDropOverlay(false);
    if (!uploadsEnabled()) {
      return;
    }
    stageFiles(event.dataTransfer.files);
  });

  document.addEventListener("keydown", (event) => {
    if (event.key !== "/" || event.metaKey || event.ctrlKey || event.altKey) {
      return;
    }
    const active = document.activeElement;
    if (active && (active.tagName === "INPUT" || active.tagName === "TEXTAREA" || active.isContentEditable)) {
      return;
    }
    const input = document.querySelector("[data-filter-input]");
    if (input) {
      event.preventDefault();
      input.focus();
    }
  });

  bindUploadForm();
  bindFilter();
  captureServerOrder();
})();
