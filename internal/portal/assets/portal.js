document.documentElement.classList.add("js");

document.querySelectorAll("[data-history-interval]").forEach((form) => {
  const start = form.querySelector('[name="start"]');
  const end = form.querySelector('[name="end"]');
  const validate = () => {
    end.setCustomValidity("");
    if (!start.value || !end.value) return;
    const [year, month, day] = start.value.split("-").map(Number);
    const lastDay = new Date(Date.UTC(year + 1, month, 0)).getUTCDate();
    const limit = `${String(year + 1).padStart(4, "0")}-${String(month).padStart(2, "0")}-${String(Math.min(day, lastDay)).padStart(2, "0")}`;
    if (end.value < start.value || end.value > limit) end.setCustomValidity("Choose dates in order, no more than one calendar year apart.");
  };
  start.addEventListener("input", validate);
  end.addEventListener("input", validate);
  form.addEventListener("submit", validate);
});

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
