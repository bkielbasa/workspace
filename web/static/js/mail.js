(() => {
  const composeDock = document.getElementById('compose-dock');
  const openBtn = document.getElementById('compose-open-btn');
  const closeBtn = document.getElementById('compose-close-btn');
  const discardBtn = document.getElementById('compose-discard-btn');
  const toInput = document.getElementById('compose-to');
  const subjectInput = document.getElementById('compose-subject');
  const bodyInput = document.getElementById('compose-body');
  const selectAll = document.getElementById('mail-select-all');

  function openCompose(to = '', subject = '', body = '') {
    if (!composeDock) return;
    composeDock.hidden = false;
    composeDock.classList.add('is-open');

    if (to && toInput) toInput.value = to;
    if (subject && subjectInput) subjectInput.value = subject;
    if (body && bodyInput) bodyInput.value = body;

    if (toInput && !toInput.value) {
      toInput.focus();
    } else if (bodyInput) {
      bodyInput.focus();
    }
  }

  function closeCompose() {
    if (!composeDock) return;
    composeDock.hidden = true;
    composeDock.classList.remove('is-open');
  }

  if (openBtn) {
    openBtn.addEventListener('click', () => {
      openCompose();
    });
  }

  if (closeBtn) {
    closeBtn.addEventListener('click', closeCompose);
  }

  if (discardBtn) {
    discardBtn.addEventListener('click', () => {
      if (toInput) toInput.value = '';
      if (subjectInput) subjectInput.value = '';
      if (bodyInput) bodyInput.value = '';
      closeCompose();
    });
  }

  document.querySelectorAll('[data-reply-btn]').forEach((btn) => {
    btn.addEventListener('click', () => {
      const to = btn.getAttribute('data-to') || '';
      const subject = btn.getAttribute('data-subject') || '';
      openCompose(to, subject);
    });
  });

  if (selectAll) {
    selectAll.addEventListener('change', () => {
      const isChecked = selectAll.checked;
      document.querySelectorAll('.mail-row-check').forEach((chk) => {
        chk.checked = isChecked;
      });
    });
  }

  // Keyboard shortcut: Escape to close compose
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && composeDock && !composeDock.hidden) {
      closeCompose();
    }
    // Press 'c' to open compose if not typing in an input/textarea
    if (
      e.key === 'c' &&
      !e.ctrlKey &&
      !e.metaKey &&
      !e.altKey &&
      composeDock &&
      composeDock.hidden &&
      !['INPUT', 'TEXTAREA'].includes(document.activeElement?.tagName)
    ) {
      e.preventDefault();
      openCompose();
    }
  });
})();
