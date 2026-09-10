(() => {
  const grid = document.querySelector(".week-grid");
  const dialog = document.getElementById("event-dialog");
  if (!grid || !dialog) return;

  const titleInput = dialog.querySelector("[name=title]");
  const locationInput = dialog.querySelector("[name=location]");
  const descriptionInput = dialog.querySelector("[name=description]");
  const startInput = dialog.querySelector("[name=starts_at]");
  const endInput = dialog.querySelector("[name=ends_at]");
  const when = dialog.querySelector("[data-when]");

  const hourStart = Number(grid.dataset.hourStart) || 0;
  const hourHeight = Number(grid.dataset.hourHeight) || 48;
  const snap = 15;
  const dayStart = hourStart * 60;

  const pad = (n) => String(n).padStart(2, "0");
  const clock = (minutes) => `${pad(Math.floor(minutes / 60) % 24)}:${pad(minutes % 60)}`;

  // Anchor to local midnight so a slot past 24:00 rolls into the next day.
  const toLocalValue = (date, minutes) => {
    const [y, m, d] = date.split("-").map(Number);
    const at = new Date(y, m - 1, d);
    at.setMinutes(minutes);
    return `${at.getFullYear()}-${pad(at.getMonth() + 1)}-${pad(at.getDate())}T${pad(at.getHours())}:${pad(at.getMinutes())}`;
  };

  const minutesAt = (column, clientY) => {
    const rect = column.getBoundingClientRect();
    const offset = Math.min(Math.max(clientY - rect.top, 0), rect.height);
    const raw = dayStart + (offset / hourHeight) * 60;
    return Math.round(raw / snap) * snap;
  };

  const selection = document.createElement("div");
  selection.className = "week-selection";
  selection.setAttribute("aria-hidden", "true");

  const place = (element, from, to) => {
    element.style.setProperty("top", `${((from - dayStart) / 60) * hourHeight}px`);
    element.style.setProperty("height", `${((to - from) / 60) * hourHeight}px`);
  };

  const resetDetails = () => {
    titleInput.value = "";
    if (locationInput) locationInput.value = "";
    if (descriptionInput) descriptionInput.value = "";
  };

  const show = () => {
    if (!dialog.open) dialog.showModal();
    titleInput.focus();
    titleInput.select();
  };

  const open = (date, from, to) => {
    resetDetails();
    startInput.value = toLocalValue(date, from);
    endInput.value = toLocalValue(date, to);
    if (when) {
      const on = new Date(`${date}T00:00`);
      when.textContent = `${on.toLocaleDateString(undefined, { weekday: "long", month: "long", day: "numeric" })}, ${clock(from)} – ${clock(to)}`;
      when.hidden = false;
    }
    show();
  };

  let drag = null;

  const endDrag = () => {
    selection.remove();
    drag = null;
  };

  grid.addEventListener("pointerdown", (event) => {
    if (event.button !== 0 || event.pointerType === "touch") return;
    const column = event.target.closest(".week-col");
    if (!column || event.target.closest(".week-event")) return;

    const at = minutesAt(column, event.clientY);
    drag = { column, anchor: at, moved: false };
    column.appendChild(selection);
    place(selection, at, at + snap);
    selection.textContent = "";
    grid.setPointerCapture(event.pointerId);
    event.preventDefault();
  });

  grid.addEventListener("pointermove", (event) => {
    if (!drag) return;
    const at = minutesAt(drag.column, event.clientY);
    const from = Math.min(drag.anchor, at);
    const to = Math.max(drag.anchor, at);
    drag.moved = to - from >= snap;
    const span = Math.max(to - from, snap);
    place(selection, from, from + span);
    selection.textContent = `${clock(from)} – ${clock(from + span)}`;
  });

  grid.addEventListener("pointerup", (event) => {
    if (!drag) return;
    const { column, anchor, moved } = drag;
    const at = minutesAt(column, event.clientY);
    const from = Math.min(anchor, at);
    const to = moved ? Math.max(anchor, at) : from + 60;
    endDrag();
    open(column.dataset.date, from, to);
  });

  grid.addEventListener("pointercancel", endDrag);

  // Touch never starts a drag, so a tap blocks out a default hour. A mouse
  // drag also emits a trailing click, which the open dialog absorbs.
  grid.addEventListener("click", (event) => {
    if (drag || dialog.open || event.target.closest(".week-event")) return;
    const column = event.target.closest(".week-col");
    if (!column) return;
    const at = minutesAt(column, event.clientY);
    open(column.dataset.date, at, at + 60);
  });

  document.querySelector("[data-new-event]")?.addEventListener("click", (event) => {
    const button = event.currentTarget;
    resetDetails();
    startInput.value = button.dataset.start || "";
    endInput.value = button.dataset.end || "";
    if (when) when.hidden = true;
    show();
  });

  dialog.querySelectorAll("[data-close-dialog]").forEach((button) => {
    button.addEventListener("click", () => dialog.close());
  });

  // Clicking the backdrop falls through to the dialog element itself.
  dialog.addEventListener("click", (event) => {
    if (event.target === dialog) dialog.close();
  });

  if (dialog.hasAttribute("data-open")) show();
})();
