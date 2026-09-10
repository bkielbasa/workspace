(() => {
  const editor = document.querySelector(".contact-edit-form");
  if (!editor) return;

  editor.addEventListener("click", (event) => {
    const addButton = event.target.closest("[data-add-field]");
    if (addButton) {
      const name = addButton.dataset.addField;
      const list = editor.querySelector(`[data-field-list="${name}"]`);
      const first = list?.querySelector(".repeatable-field");
      if (!list || !first) return;

      const row = first.cloneNode(true);
      const input = row.querySelector("input");
      const select = row.querySelector("select");
      input.value = "";
      if (select) select.value = "home";
      list.appendChild(row);
      input.focus();
      return;
    }

    const removeButton = event.target.closest("[data-remove-field]");
    if (!removeButton) return;

    const row = removeButton.closest(".repeatable-field");
    const list = row.parentElement;
    if (list.children.length === 1) {
      const input = row.querySelector("input");
      const select = row.querySelector("select");
      input.value = "";
      if (select) select.value = "home";
      input.focus();
      return;
    }
    row.remove();
  });
})();
