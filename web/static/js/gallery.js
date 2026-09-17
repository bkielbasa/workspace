/* Gallery lightbox: click a thumbnail for the large view with tag editor.
 * Stays on the page; the old separate detail route is gone. */
(function () {
  "use strict";

  var modal = document.getElementById("photo-modal");
  if (!modal) return;
  var csrf = modal.getAttribute("data-csrf") || "";
  var stage = modal.querySelector(".photo-modal-stage");
  var title = modal.querySelector(".photo-name");
  var metaName = modal.querySelector('[data-meta="name"]');
  var metaAdded = modal.querySelector('[data-meta="added"]');
  var download = modal.querySelector('[data-meta="download"]');
  var tagChips = modal.querySelector("[data-tag-chips]");
  var tagForm = modal.querySelector("[data-tag-form]");
  var tagInput = modal.querySelector("#photo-modal-tag");
  var tagSuggest = modal.querySelector("[data-tag-suggest]");
  var knownTags = Array.prototype.map.call(
    document.querySelectorAll("#tag-suggestions option"),
    function (o) { return o.value; }
  );
  var items = Array.prototype.slice.call(document.querySelectorAll(".gallery-open"));
  var current = -1;
  var currentTags = [];

  function fmtBytes(n) {
    n = Number(n) || 0;
    if (n < 1024) return n + " B";
    if (n < 1048576) return (n / 1024).toFixed(1) + " KB";
    if (n < 1073741824) return (n / 1048576).toFixed(1) + " MB";
    return (n / 1073741824).toFixed(1) + " GB";
  }

  function fileURL(path) {
    return "/gallery/file?path=" + encodeURIComponent(path);
  }

  function tagsOf(item) {
    return (item.getAttribute("data-tags") || "").split(",").filter(function (t) { return t !== ""; });
  }

  function renderChips() {
    if (!tagChips) return; // tag store unwired: editor hidden, modal still works.
    tagChips.innerHTML = "";
    if (currentTags.length === 0) {
      var none = document.createElement("p");
      none.className = "field-hint";
      none.textContent = "No tags yet — add the first below.";
      tagChips.appendChild(none);
      return;
    }
    currentTags.forEach(function (tag) {
      var chip = document.createElement("span");
      chip.className = "tag-chip tag-chip-removable";
      chip.textContent = tag + " ";
      var x = document.createElement("button");
      x.type = "button";
      x.setAttribute("aria-label", "Remove " + tag);
      x.textContent = "×";
      x.addEventListener("click", function () {
        currentTags = currentTags.filter(function (t) { return t !== tag; });
        saveTags();
      });
      chip.appendChild(x);
      tagChips.appendChild(chip);
    });
  }

  function syncCaption(item) {
    var caption = item.parentElement.querySelector(".gallery-caption");
    if (!caption) return;
    caption.innerHTML = "";
    var tags = tagsOf(item);
    if (tags.length === 0) {
      caption.textContent = item.getAttribute("data-name");
      return;
    }
    tags.forEach(function (tag) {
      var chip = document.createElement("span");
      chip.className = "tag-chip";
      chip.textContent = tag;
      caption.appendChild(chip);
    });
  }

  function saveTags() {
    if (current < 0) return;
    var item = items[current];
    var path = item.getAttribute("data-path");
    fetch("/gallery/tags?format=json", {
      method: "POST",
      headers: {
        "Content-Type": "application/x-www-form-urlencoded",
        "X-CSRF-Token": csrf,
        "Accept": "application/json",
      },
      body: "path=" + encodeURIComponent(path) + "&tags=" + encodeURIComponent(currentTags.join(", ")),
    })
      .then(function (resp) {
        if (!resp.ok) throw new Error("save failed");
        return resp.json();
      })
      .then(function (data) {
        currentTags = (data && data.tags) || [];
        item.setAttribute("data-tags", currentTags.join(","));
        title.textContent = currentTags.join(", ") || item.getAttribute("data-name");
        renderChips();
        syncCaption(item);
      })
      .catch(function () {
        renderChips();
        var err = document.createElement("p");
        err.className = "compose-error";
        err.textContent = "Could not save — retry.";
        tagChips.appendChild(err);
      });
  }
  function render(item) {
    var path = item.getAttribute("data-path");
    var name = item.getAttribute("data-name");
    var kind = item.getAttribute("data-kind");
    currentTags = tagsOf(item);
    var hasPreview = item.getAttribute("data-has-preview") === "true";
    stage.innerHTML = "";
    if (kind === "video") {
      var video = document.createElement("video");
      video.src = fileURL(path);
      video.controls = true;
      video.className = "photo-large";
      stage.appendChild(video);
    } else if (kind === "image" || (kind === "heic" && hasPreview)) {
      var img = document.createElement("img");
      img.src = kind === "image" ? fileURL(path) : "/gallery/preview?path=" + encodeURIComponent(path);
      img.alt = name;
      img.className = "photo-large";
      stage.appendChild(img);
    } else {
      var empty = document.createElement("div");
      empty.className = "mail-empty-state";
      empty.textContent = "Preview is still rendering — try the download.";
      stage.appendChild(empty);
    }
    title.textContent = currentTags.join(", ") || name;
    metaName.textContent = name;
    metaAdded.textContent = fmtBytes(item.getAttribute("data-size")) + " · " + item.getAttribute("data-modified");
    download.href = fileURL(path);
    renderChips();
    if (tagInput) tagInput.value = "";
    hideSuggest();
  }

  function open(idx) {
    if (idx < 0 || idx >= items.length) return;
    current = idx;
    render(items[current]);
    modal.hidden = false;
    document.body.style.overflow = "hidden";
  }

  function close() {
    modal.hidden = true;
    document.body.style.overflow = "";
    var media = stage.querySelector("video");
    if (media) media.pause();
    current = -1;
  }

  function step(dir) {
    if (current < 0 || items.length === 0) return;
    open((current + dir + items.length) % items.length);
  }

  items.forEach(function (item, idx) {
    item.addEventListener("click", function () { open(idx); });
  });
  modal.querySelector(".photo-modal-prev").addEventListener("click", function () { step(-1); });
  modal.querySelector(".photo-modal-next").addEventListener("click", function () { step(1); });
  modal.querySelectorAll("[data-close]").forEach(function (el) {
    el.addEventListener("click", close);
  });
  document.addEventListener("keydown", function (ev) {
    if (modal.hidden) return;
    if (ev.key === "Escape") close();
    if (ev.key === "ArrowLeft") step(-1);
    if (ev.key === "ArrowRight") step(1);
  });

  if (tagForm) tagForm.addEventListener("submit", function (ev) {
    ev.preventDefault();
    if (current < 0 || !tagInput) return;
    addTag(tagInput.value.trim());
  });

  function addTag(value) {
    if (current < 0 || !value) return;
    var exists = currentTags.some(function (t) { return t.toLowerCase() === value.toLowerCase(); });
    if (!exists) {
      currentTags.push(value);
      saveTags();
    }
    if (tagInput) tagInput.value = "";
    hideSuggest();
  }

  // Custom suggestions: the native datalist popup is unreliable on mobile
  // Safari, so filter the known tags here and offer tap-to-add.
  function hideSuggest() {
    if (tagSuggest) tagSuggest.hidden = true;
  }

  function showSuggest() {
    if (!tagSuggest || !tagInput || current < 0) return;
    var q = tagInput.value.trim().toLowerCase();
    var matches = knownTags.filter(function (t) {
      if (t.toLowerCase().indexOf(q) < 0) return false;
      return !currentTags.some(function (c) { return c.toLowerCase() === t.toLowerCase(); });
    }).slice(0, 6);
    // Empty query shows all unused tags; a full exact match shows nothing.
    if (q !== "" && matches.some(function (t) { return t.toLowerCase() === q; }) && matches.length === 1) {
      hideSuggest();
      return;
    }
    tagSuggest.innerHTML = "";
    if (matches.length === 0) {
      hideSuggest();
      return;
    }
    matches.forEach(function (tag) {
      var b = document.createElement("button");
      b.type = "button";
      b.className = "tag-suggest-item";
      b.textContent = tag;
      b.addEventListener("click", function () { addTag(tag); });
      tagSuggest.appendChild(b);
    });
    tagSuggest.hidden = false;
  }

  if (tagInput) {
    tagInput.addEventListener("input", showSuggest);
    tagInput.addEventListener("focus", showSuggest);
    tagInput.addEventListener("blur", function () { setTimeout(hideSuggest, 150); });
  }
})();
