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
  button.textContent = light ? "Dark theme" : "Light theme";
  button.setAttribute("aria-label", `Switch to ${light ? "dark" : "light"} appearance`);
  button.setAttribute("aria-pressed", String(light));
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
  close.className = "dialog-close";
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
