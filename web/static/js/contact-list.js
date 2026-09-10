(() => {
  const form = document.querySelector("[data-contact-search-form]");
  const input = form?.querySelector("input[type=search]");
  const contacts = [...document.querySelectorAll("[data-contact-search]")];
  const count = document.querySelector("[data-contact-count]");
  const noResults = document.querySelector("[data-no-search-results]");
  if (!form || !input || contacts.length === 0) return;

  const filter = () => {
    const query = input.value.trim().toLocaleLowerCase();
    let visible = 0;

    contacts.forEach((contact) => {
      const matches = !query || contact.dataset.contactSearch.toLocaleLowerCase().includes(query);
      contact.hidden = !matches;
      if (matches) visible++;

      const contactURL = new URL(contact.href);
      if (query) contactURL.searchParams.set("q", input.value.trim());
      else contactURL.searchParams.delete("q");
      contact.href = contactURL;
    });

    count.textContent = `${visible} ${visible === 1 ? "person" : "people"}`;
    noResults.hidden = visible !== 0;

    const url = new URL(window.location.href);
    if (query) url.searchParams.set("q", input.value.trim());
    else url.searchParams.delete("q");
    history.replaceState(null, "", url);
  };

  input.addEventListener("input", filter);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    filter();
  });
})();
