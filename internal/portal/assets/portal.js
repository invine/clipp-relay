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
