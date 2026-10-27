document.documentElement.classList.add("js");

document.querySelectorAll(".workspace-menu").forEach((menu) => {
  const summary = menu.querySelector("summary");
  menu.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && menu.open) {
      menu.open = false;
      summary?.focus();
    }
  });
  menu.querySelector("[data-close-menu]")?.addEventListener("click", () => {
    menu.open = false;
    summary?.focus();
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
