(() => {
  const grid = document.querySelector(".week-grid");
  const dialog = document.getElementById("event-dialog");
  if (!grid || !dialog) return;

  const form = dialog.querySelector("form");
  const dialogTitle = dialog.querySelector("[data-dialog-title]");
  const dialogSubmit = dialog.querySelector("[data-dialog-submit]");
  const titleInput = dialog.querySelector("[name=title]");
  const locationInput = dialog.querySelector("[name=location]");
  const descriptionInput = dialog.querySelector("[name=description]");
  const startInput = dialog.querySelector("[name=starts_at]");
  const endInput = dialog.querySelector("[name=ends_at]");
  const when = dialog.querySelector("[data-when]");
  const alldayArea = document.querySelector(".week-allday");

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

  const formatDateTimeRange = (startStr, endStr) => {
    if (!startStr || !endStr) return "";
    const s = new Date(startStr);
    const e = new Date(endStr);
    if (isNaN(s.getTime()) || isNaN(e.getTime())) return "";
    const sameDay = s.toDateString() === e.toDateString();
    const datePart = s.toLocaleDateString(undefined, { weekday: "long", month: "long", day: "numeric" });
    const startTime = clock(s.getHours() * 60 + s.getMinutes());
    const endTime = clock(e.getHours() * 60 + e.getMinutes());
    if (sameDay) {
      return `${datePart}, ${startTime} – ${endTime}`;
    }
    const endPart = e.toLocaleDateString(undefined, { weekday: "long", month: "long", day: "numeric" });
    return `${datePart} ${startTime} – ${endPart} ${endTime}`;
  };

  const openNew = (date, from, to) => {
    if (form) form.action = "/calendars";
    if (dialogTitle) dialogTitle.textContent = "New event";
    if (dialogSubmit) dialogSubmit.textContent = "Add event";
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

  const openEdit = (el) => {
    const id = el.dataset.id;
    if (!id) return;
    if (form) form.action = `/calendars/${id}`;
    if (dialogTitle) dialogTitle.textContent = "Edit event";
    if (dialogSubmit) dialogSubmit.textContent = "Save changes";

    titleInput.value = el.dataset.title || "";
    if (locationInput) locationInput.value = el.dataset.location || "";
    if (descriptionInput) descriptionInput.value = el.dataset.description || "";
    startInput.value = el.dataset.start || "";
    endInput.value = el.dataset.end || "";

    if (when) {
      const formatted = formatDateTimeRange(el.dataset.start, el.dataset.end);
      if (formatted) {
        when.textContent = formatted;
        when.hidden = false;
      } else {
        when.hidden = true;
      }
    }
    show();
  };

  // Double-click to edit an event
  document.addEventListener("dblclick", (event) => {
    if (event.target.closest(".icon-btn")) return;
    const target = event.target.closest(".week-event, .week-chip");
    if (!target) return;
    event.preventDefault();
    openEdit(target);
  });

  const reschedule = (el, startVal, endVal) => {
    const id = el.dataset.id;
    if (!id) return;

    const csrf = dialog.querySelector("[name=_csrf]")?.value || "";
    const week = dialog.querySelector("[name=week]")?.value || "";

    const submitForm = document.createElement("form");
    submitForm.method = "POST";
    submitForm.action = `/calendars/${id}`;
    submitForm.hidden = true;

    const addField = (name, value) => {
      const input = document.createElement("input");
      input.type = "hidden";
      input.name = name;
      input.value = value;
      submitForm.appendChild(input);
    };

    addField("_csrf", csrf);
    addField("week", week);
    addField("title", el.dataset.title || "");
    addField("location", el.dataset.location || "");
    addField("description", el.dataset.description || "");
    addField("starts_at", startVal);
    addField("ends_at", endVal);

    document.body.appendChild(submitForm);
    submitForm.submit();
  };

  // --- Drag & Drop state ---
  let pendingEventDrag = null;
  let activeEventDrag = null;
  let pendingChipDrag = null;
  let activeChipDrag = null;
  let slotDrag = null;
  let didDrag = false;

  const markDragFinished = () => {
    didDrag = true;
    setTimeout(() => {
      didDrag = false;
    }, 120);
  };

  // Ghost element for timed events
  const eventGhost = document.createElement("div");
  eventGhost.className = "week-event-ghost";
  eventGhost.innerHTML = `<strong></strong><span></span>`;

  // Ghost element for all-day chips
  const chipGhost = document.createElement("div");
  chipGhost.className = "week-chip-ghost";
  chipGhost.innerHTML = `<span></span>`;

  // Pointerdown on grid (slot drag or timed event drag)
  grid.addEventListener("pointerdown", (event) => {
    if (event.button !== 0 || event.pointerType === "touch") return;
    if (event.target.closest(".icon-btn")) return;

    const eventEl = event.target.closest(".week-event");
    if (eventEl) {
      const column = eventEl.closest(".week-col");
      if (!column) return;
      const start = new Date(eventEl.dataset.start);
      const end = new Date(eventEl.dataset.end);
      const duration = Math.max(snap, Math.round((end.getTime() - start.getTime()) / 60000));
      const startMinutes = start.getHours() * 60 + start.getMinutes();
      const pointerMinutes = minutesAt(column, event.clientY);
      const grabOffset = pointerMinutes - startMinutes;

      pendingEventDrag = {
        eventEl,
        column,
        startX: event.clientX,
        startY: event.clientY,
        startMinutes,
        duration,
        grabOffset,
        pointerId: event.pointerId,
      };
      return;
    }

    // Drag-to-create on empty grid slot
    const column = event.target.closest(".week-col");
    if (!column) return;

    const at = minutesAt(column, event.clientY);
    slotDrag = { column, anchor: at, moved: false };
    column.appendChild(selection);
    place(selection, at, at + snap);
    selection.textContent = "";
    grid.setPointerCapture(event.pointerId);
    event.preventDefault();
  });

  // Pointerdown on all-day chips
  alldayArea?.addEventListener("pointerdown", (event) => {
    if (event.button !== 0 || event.pointerType === "touch") return;
    if (event.target.closest(".icon-btn")) return;

    const chipEl = event.target.closest(".week-chip");
    if (chipEl) {
      const column = chipEl.closest(".week-allday-col");
      if (!column) return;
      pendingChipDrag = {
        chipEl,
        column,
        startX: event.clientX,
        startY: event.clientY,
        pointerId: event.pointerId,
      };
    }
  });

  // Global pointermove
  window.addEventListener("pointermove", (event) => {
    // Check pending timed event drag
    if (pendingEventDrag) {
      const dist = Math.hypot(event.clientX - pendingEventDrag.startX, event.clientY - pendingEventDrag.startY);
      if (dist >= 5) {
        activeEventDrag = pendingEventDrag;
        pendingEventDrag = null;
        try {
          grid.setPointerCapture(activeEventDrag.pointerId);
        } catch (_) {}
        activeEventDrag.eventEl.classList.add("is-rescheduling");
        eventGhost.querySelector("strong").textContent = activeEventDrag.eventEl.dataset.title || "";
        activeEventDrag.column.appendChild(eventGhost);
      }
    }

    // Active timed event drag
    if (activeEventDrag) {
      const columns = Array.from(grid.querySelectorAll(".week-col"));
      let targetCol = activeEventDrag.column;
      for (const col of columns) {
        const rect = col.getBoundingClientRect();
        if (event.clientX >= rect.left && event.clientX <= rect.right) {
          targetCol = col;
          break;
        }
      }
      if (columns.length > 0) {
        if (event.clientX < columns[0].getBoundingClientRect().left) targetCol = columns[0];
        if (event.clientX > columns[columns.length - 1].getBoundingClientRect().right) targetCol = columns[columns.length - 1];
      }
      activeEventDrag.currentCol = targetCol;

      const at = minutesAt(targetCol, event.clientY);
      let newStart = at - activeEventDrag.grabOffset;
      newStart = Math.round(newStart / snap) * snap;
      newStart = Math.max(0, Math.min(24 * 60 - activeEventDrag.duration, newStart));
      activeEventDrag.currentStartMinutes = newStart;

      if (eventGhost.parentElement !== targetCol) {
        targetCol.appendChild(eventGhost);
      }
      const top = ((newStart - dayStart) / 60) * hourHeight;
      const height = (activeEventDrag.duration / 60) * hourHeight;
      eventGhost.style.setProperty("top", `${top}px`);
      eventGhost.style.setProperty("height", `${Math.max(22, height)}px`);
      eventGhost.querySelector("span").textContent = `${clock(newStart)} – ${clock(newStart + activeEventDrag.duration)}`;
      return;
    }

    // Check pending all-day chip drag
    if (pendingChipDrag) {
      const dist = Math.hypot(event.clientX - pendingChipDrag.startX, event.clientY - pendingChipDrag.startY);
      if (dist >= 5) {
        activeChipDrag = pendingChipDrag;
        pendingChipDrag = null;
        activeChipDrag.chipEl.classList.add("is-rescheduling");
        chipGhost.querySelector("span").textContent = activeChipDrag.chipEl.dataset.title || "";
        activeChipDrag.column.appendChild(chipGhost);
      }
    }

    // Active all-day chip drag
    if (activeChipDrag && alldayArea) {
      const columns = Array.from(alldayArea.querySelectorAll(".week-allday-col"));
      let targetCol = activeChipDrag.column;
      for (const col of columns) {
        const rect = col.getBoundingClientRect();
        if (event.clientX >= rect.left && event.clientX <= rect.right) {
          targetCol = col;
          break;
        }
      }
      activeChipDrag.currentCol = targetCol;
      if (chipGhost.parentElement !== targetCol) {
        targetCol.appendChild(chipGhost);
      }
      return;
    }

    // Slot drag (drag-to-create)
    if (slotDrag) {
      const at = minutesAt(slotDrag.column, event.clientY);
      const from = Math.min(slotDrag.anchor, at);
      const to = Math.max(slotDrag.anchor, at);
      slotDrag.moved = to - from >= snap;
      const span = Math.max(to - from, snap);
      place(selection, from, from + span);
      selection.textContent = `${clock(from)} – ${clock(from + span)}`;
    }
  });

  // Global pointerup
  window.addEventListener("pointerup", (event) => {
    if (pendingEventDrag) {
      pendingEventDrag = null;
    }

    if (activeEventDrag) {
      markDragFinished();
      const dragInfo = activeEventDrag;
      activeEventDrag = null;
      eventGhost.remove();
      dragInfo.eventEl.classList.remove("is-rescheduling");

      const targetCol = dragInfo.currentCol || dragInfo.column;
      const targetDate = targetCol.dataset.date;
      const newStartMinutes = dragInfo.currentStartMinutes != null ? dragInfo.currentStartMinutes : dragInfo.startMinutes;
      const newEndMinutes = newStartMinutes + dragInfo.duration;

      const newStartVal = toLocalValue(targetDate, newStartMinutes);
      const newEndVal = toLocalValue(targetDate, newEndMinutes);

      if (newStartVal === dragInfo.eventEl.dataset.start && newEndVal === dragInfo.eventEl.dataset.end) {
        return;
      }
      reschedule(dragInfo.eventEl, newStartVal, newEndVal);
      return;
    }

    if (pendingChipDrag) {
      pendingChipDrag = null;
    }

    if (activeChipDrag) {
      markDragFinished();
      const dragInfo = activeChipDrag;
      activeChipDrag = null;
      chipGhost.remove();
      dragInfo.chipEl.classList.remove("is-rescheduling");

      const targetCol = dragInfo.currentCol || dragInfo.column;
      const originDateStr = dragInfo.column.dataset.date;
      const targetDateStr = targetCol.dataset.date;

      if (targetDateStr && originDateStr && targetDateStr !== originDateStr) {
        const originDate = new Date(`${originDateStr}T00:00`);
        const targetDate = new Date(`${targetDateStr}T00:00`);
        const diffDays = Math.round((targetDate.getTime() - originDate.getTime()) / (24 * 60 * 60 * 1000));
        if (diffDays !== 0) {
          const s = new Date(dragInfo.chipEl.dataset.start);
          const e = new Date(dragInfo.chipEl.dataset.end);
          s.setDate(s.getDate() + diffDays);
          e.setDate(e.getDate() + diffDays);
          const newStartVal = `${s.getFullYear()}-${pad(s.getMonth() + 1)}-${pad(s.getDate())}T${pad(s.getHours())}:${pad(s.getMinutes())}`;
          const newEndVal = `${e.getFullYear()}-${pad(e.getMonth() + 1)}-${pad(e.getDate())}T${pad(e.getHours())}:${pad(e.getMinutes())}`;
          reschedule(dragInfo.chipEl, newStartVal, newEndVal);
        }
      }
      return;
    }

    // Slot drag finish
    if (slotDrag) {
      const { column, anchor, moved } = slotDrag;
      const at = minutesAt(column, event.clientY);
      const from = Math.min(anchor, at);
      const to = moved ? Math.max(anchor, at) : from + 60;
      selection.remove();
      slotDrag = null;
      openNew(column.dataset.date, from, to);
    }
  });

  const cancelAllDrags = () => {
    pendingEventDrag = null;
    if (activeEventDrag) {
      eventGhost.remove();
      activeEventDrag.eventEl.classList.remove("is-rescheduling");
      activeEventDrag = null;
    }
    pendingChipDrag = null;
    if (activeChipDrag) {
      chipGhost.remove();
      activeChipDrag.chipEl.classList.remove("is-rescheduling");
      activeChipDrag = null;
    }
    if (slotDrag) {
      selection.remove();
      slotDrag = null;
    }
  };

  window.addEventListener("pointercancel", cancelAllDrags);
  window.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      cancelAllDrags();
    }
  });

  // Touch never starts a drag, so a tap blocks out a default hour. A mouse
  // drag also emits a trailing click, which we absorb if dragging just finished.
  grid.addEventListener("click", (event) => {
    if (didDrag || slotDrag || dialog.open || event.target.closest(".week-event")) return;
    const column = event.target.closest(".week-col");
    if (!column) return;
    const at = minutesAt(column, event.clientY);
    openNew(column.dataset.date, at, at + 60);
  });

  document.querySelector("[data-new-event]")?.addEventListener("click", (event) => {
    const button = event.currentTarget;
    if (form) form.action = "/calendars";
    if (dialogTitle) dialogTitle.textContent = "New event";
    if (dialogSubmit) dialogSubmit.textContent = "Add event";
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
