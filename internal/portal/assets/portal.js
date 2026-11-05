const themeRoot = document.documentElement;
const themeKey = "clipp-relay-appearance";
try {
  const savedTheme = localStorage.getItem(themeKey);
  if (savedTheme === "dark" || savedTheme === "light") themeRoot.dataset.theme = savedTheme;
} catch {
  // The server-rendered dark appearance remains complete when storage is unavailable.
}

function updateThemeControl(button) {
  const light = themeRoot.dataset.theme === "light";
  button.setAttribute("aria-checked", String(!light));
  button.querySelector(".theme-label").textContent = light ? "Light mode" : "Dark mode";
}

document.querySelectorAll("[data-theme-toggle]").forEach((button) => {
  updateThemeControl(button);
  button.addEventListener("click", () => {
    const nextTheme = themeRoot.dataset.theme === "light" ? "dark" : "light";
    themeRoot.dataset.theme = nextTheme;
    try {
      localStorage.setItem(themeKey, nextTheme);
    } catch {
      // Theme still changes for this page even when preference storage is blocked.
    }
    document.querySelectorAll("[data-theme-toggle]").forEach(updateThemeControl);
  });
});

document.documentElement.classList.add("js");

// Fetch the same account-scoped page used by the table's Show more link.
// Replacing the shared graph/table fragment keeps grouping and page size in sync.
function setupHistory(root, distanceFromRight = 0) {
  const scroll = root.querySelector(".history-chart-scroll");
  const more = root.querySelector("[data-history-more]");
  if (!scroll) return;
  scroll.scrollLeft = Math.max(0, scroll.scrollWidth - scroll.clientWidth - distanceFromRight);
  let loading = false;
  let dragging = null;
  const loadMore = async () => {
    if (loading || !more?.isConnected) return;
    loading = true;
    const distance = scroll.scrollWidth - scroll.clientWidth - scroll.scrollLeft;
    const tableOpen = root.querySelector(".history-table-details")?.open;
    more.setAttribute("aria-busy", "true");
    try {
      const response = await fetch(more.href, { credentials: "same-origin" });
      if (!response.ok) throw new Error("History request failed");
      const page = new DOMParser().parseFromString(await response.text(), "text/html");
      const next = page.querySelector("[data-history-data]");
      if (!next) throw new Error("History is unavailable");
      root.replaceWith(next);
      const details = next.querySelector(".history-table-details");
      if (details) details.open = !!tableOpen;
      const status = next.querySelector("[data-history-status]");
      if (status) status.textContent = "Older history loaded.";
      setupHistory(next, distance);
    } catch {
      more.removeAttribute("aria-busy");
      const status = root.querySelector("[data-history-status]");
      if (status) status.textContent = "Could not load older history. Use Show more to try again.";
      loading = false;
    }
  };
  scroll.addEventListener("scroll", () => {
    if (scroll.scrollWidth > scroll.clientWidth && scroll.scrollLeft <= 12) loadMore();
  }, { passive: true });
  scroll.addEventListener("pointerdown", (event) => {
    if (event.pointerType !== "mouse" || event.button !== 0) return;
    dragging = { x: event.clientX, left: scroll.scrollLeft };
    scroll.setPointerCapture(event.pointerId);
  });
  scroll.addEventListener("pointermove", (event) => {
    if (dragging) scroll.scrollLeft = dragging.left + dragging.x - event.clientX;
  });
  scroll.addEventListener("pointerup", () => { dragging = null; });
  scroll.addEventListener("pointercancel", () => { dragging = null; });
  more?.addEventListener("click", (event) => {
    event.preventDefault();
    loadMore();
  });
  // Fill wide viewports until there is content to scroll or history is exhausted.
  if (more && scroll.scrollWidth <= scroll.clientWidth + 1) loadMore();
}
document.querySelectorAll("[data-history-data]").forEach(setupHistory);

document.querySelectorAll(".workspace-menu").forEach((menu) => {
  const summary = menu.querySelector("summary");
  menu.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && menu.open) {
      menu.open = false;
      summary?.focus();
    }
  });
});

document.querySelectorAll("[data-copy]").forEach((button) => {
  button.addEventListener("click", async () => {
    const input = document.getElementById(button.dataset.copy);
    const status = document.querySelector("[data-copy-status]");
    if (!input || !status) return;
    try {
      await navigator.clipboard.writeText(input.value);
      status.textContent = "Discovery URL copied. Paste it into Clipp’s relay settings.";
      button.textContent = "Copied";
    } catch {
      input.focus();
      input.select();
      button.textContent = "Copy URL";
      status.textContent = "Select and copy the URL, then paste it into Clipp’s relay settings.";
    }
  });
});

const setup = document.querySelector("#connection-setup");
const connect = document.querySelector(".connect-clipp");
if (setup && connect && typeof HTMLDialogElement !== "undefined" && typeof HTMLDialogElement.prototype.showModal === "function") {
  const dialog = document.createElement("dialog");
  dialog.id = "connect-dialog";
  dialog.className = "connect-dialog";
  dialog.setAttribute("aria-labelledby", "setup-title");
  const close = document.createElement("button");
  close.type = "button";
  close.className = "secondary dialog-close";
  close.dataset.closeConnect = "";
  close.setAttribute("aria-label", "Close Connect Clipp setup");
  close.textContent = "Close";
  setup.prepend(close);
  dialog.append(setup);
  document.querySelector("main.page")?.append(dialog);

  connect.addEventListener("click", (event) => {
    event.preventDefault();
    connect.focus();
    dialog.showModal();
    setup.querySelector("#discovery-url")?.focus();
  });
  close.addEventListener("click", () => dialog.close());
}

document.querySelectorAll("[data-plan-preview]").forEach((select) => {
  const output = document.getElementById(select.dataset.planPreview);
  if (!output) return;
  const update = () => {
    const option = select.selectedOptions[0];
    if (option) output.textContent = option.dataset.summary || "Selected Relay Quota plan";
  };
  select.addEventListener("change", update);
  update();
});
