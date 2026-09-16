(() => {
  const form = document.getElementById("drive-rename-form");
  if (form) {
    document.querySelectorAll("[data-rename-btn]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const next = prompt("Rename to:", btn.dataset.name || "");
        if (!next || next === btn.dataset.name) return;
        document.getElementById("drive-rename-path").value = btn.dataset.path || "";
        document.getElementById("drive-rename-name").value = next;
        form.submit();
      });
    });
  }
  document.querySelectorAll("[data-confirm]").forEach((f) => {
    f.addEventListener("submit", (event) => {
      if (!confirm(f.dataset.confirm || "Are you sure?")) event.preventDefault();
    });
  });
  document.querySelectorAll("[data-autosubmit]").forEach((input) => {
    input.addEventListener("change", () => {
      if (input.files && input.files.length > 0 && input.form) input.form.submit();
    });
  });
})();
