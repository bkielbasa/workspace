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
  var albumChecks = modal.querySelector("[data-album-checks]");
  var knownTags = Array.prototype.map.call(
    document.querySelectorAll("#tag-suggestions option"),
    function (o) { return o.value; }
  );
  var progress = document.querySelector("[data-upload-progress]");
  var current = -1;
  var currentPath = "";
  var currentTags = [];

  // Live list: HTMX swaps replace grid nodes, so never cache them.
  function liveItems() {
    return Array.prototype.slice.call(document.querySelectorAll(".gallery-open"));
  }

  function refreshLayout() {
    if (typeof htmx === "undefined") return;
    htmx.ajax("GET", "/gallery/content" + window.location.search, {
      target: "#gallery-content",
      swap: "outerHTML",
    });
  }

  // After any content swap, re-point the open modal at the fresh nodes.
  document.body.addEventListener("htmx:afterSwap", function (ev) {
    if (!ev.detail || !ev.detail.target || ev.detail.target.id !== "gallery-content") return;
    knownTags = Array.prototype.map.call(
      document.querySelectorAll("#tag-suggestions option"),
      function (o) { return o.value; }
    );
    paintSelection();
    if (currentPath === "") return;
    var found = -1;
    liveItems().forEach(function (item, idx) {
      if (item.getAttribute("data-path") === currentPath) found = idx;
    });
    if (found < 0) {
      close();
      return;
    }
    current = found;
    render(liveItems()[current]);
  });

  // Keep the sidebar filter across upload posts.
  document.body.addEventListener("htmx:configRequest", function (ev) {
    if (!ev.detail || ev.detail.path.indexOf("/api/upload") < 0) return;
    var q = new URLSearchParams(window.location.search);
    ["album", "tag"].forEach(function (key) {
      var val = q.get(key);
      if (val) ev.detail.parameters[key] = val;
    });
  });

  // Upload progress on the header bar.
  document.body.addEventListener("htmx:xhr:progress", function (ev) {
    if (!progress || !ev.detail || !ev.detail.total) return;
    progress.hidden = false;
    progress.value = Math.round((ev.detail.loaded / ev.detail.total) * 100);
  });
  document.body.addEventListener("htmx:afterRequest", function () {
    if (progress) {
      progress.hidden = true;
      progress.value = 0;
    }
  });

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

  function albumsOf(item) {
    return (item.getAttribute("data-albums") || "").split(",").filter(function (t) { return t !== ""; });
  }

  function renderAlbumChecks(item) {
    if (!albumChecks) return;
    var mine = albumsOf(item);
    albumChecks.querySelectorAll("input[data-album-id]").forEach(function (box) {
      box.checked = mine.indexOf(box.getAttribute("data-album-id")) >= 0;
    });
  }

  function toggleAlbum(box) {
    if (current < 0) return;
    var item = liveItems()[current];
    if (!item) return;
    var path = item.getAttribute("data-path");
    var albumID = box.getAttribute("data-album-id");
    var add = box.checked ? "1" : "0";
    fetch("/gallery/albums/toggle?format=json", {
      method: "POST",
      headers: {
        "Content-Type": "application/x-www-form-urlencoded",
        "X-CSRF-Token": csrf,
        "Accept": "application/json",
      },
      body: "album_id=" + encodeURIComponent(albumID) + "&path=" + encodeURIComponent(path) + "&add=" + add,
    })
      .then(function (resp) {
        if (!resp.ok) throw new Error("toggle failed");
        return resp.json();
      })
      .then(function (data) {
        var mine = albumsOf(item).filter(function (id) { return id !== albumID; });
        if (data && data.in_album) mine.push(albumID);
        item.setAttribute("data-albums", mine.join(","));
        refreshLayout();
      })
      .catch(function () {
        box.checked = !box.checked;
      });
  }

  if (albumChecks) albumChecks.addEventListener("change", function (ev) {
    if (ev.target && ev.target.matches("input[data-album-id]")) toggleAlbum(ev.target);
  });

  // ---- Multi-select -------------------------------------------------
  // Paths, not nodes: HTMX swaps throw the nodes away on every refresh.
  var selected = [];
  var selectMode = false;
  var lastIndex = -1;

  function isSelected(path) { return selected.indexOf(path) >= 0; }

  function setSelected(path, on) {
    var at = selected.indexOf(path);
    if (on && at < 0) selected.push(path);
    if (!on && at >= 0) selected.splice(at, 1);
  }

  // Paint selection onto whatever nodes exist right now, and drop any
  // paths that vanished (deleted elsewhere, or filtered out of view).
  function paintSelection() {
    var live = liveItems();
    var alive = {};
    live.forEach(function (item) {
      var path = item.getAttribute("data-path");
      alive[path] = true;
      var on = isSelected(path);
      item.classList.toggle("selected", on);
      // Only a toggle while selecting; outside that it just opens a photo.
      if (selectMode) {
        item.setAttribute("aria-pressed", on ? "true" : "false");
      } else {
        item.removeAttribute("aria-pressed");
      }
    });
    selected = selected.filter(function (p) { return alive[p]; });

    var bar = document.querySelector("[data-select-bar]");
    var count = document.querySelector("[data-select-count]");
    if (count) count.textContent = String(selected.length);
    if (bar) bar.hidden = !selectMode;
    document.body.classList.toggle("select-mode", selectMode);
    var toggle = document.querySelector("[data-select-toggle]");
    if (toggle) {
      toggle.textContent = selectMode ? "Done" : "Select";
      toggle.setAttribute("aria-pressed", selectMode ? "true" : "false");
    }
  }

  function exitSelect() {
    selectMode = false;
    selected = [];
    lastIndex = -1;
    paintSelection();
  }

  document.addEventListener("click", function (ev) {
    var toggle = ev.target.closest ? ev.target.closest("[data-select-toggle]") : null;
    if (!toggle) return;
    selectMode = !selectMode;
    if (!selectMode) selected = [];
    paintSelection();
  });

  document.addEventListener("click", function (ev) {
    var clear = ev.target.closest ? ev.target.closest("[data-select-clear]") : null;
    if (clear) exitSelect();
  });

  // Escape leaves select mode, but only when the lightbox isn't using it.
  document.addEventListener("keydown", function (ev) {
    if (ev.key === "Escape" && selectMode && modal.hidden) exitSelect();
  });

  // ---- Bulk actions ---------------------------------------------------
  function bulkAlbum(albumID, paths) {
    return postForm("/gallery/albums/toggle", {album_id: albumID, path: paths, add: "1"});
  }

  function bulkDelete(paths) {
    return postForm("/gallery/delete", {path: paths});
  }

  // Tags are per-photo (each keeps its own list), so this walks the
  // selection one request at a time instead of flooding the server.
  function bulkTag(tag, paths) {
    return paths.reduce(function (chain, path) {
      return chain.then(function () {
        var btn = document.querySelector('.gallery-open[data-path="' + cssEscape(path) + '"]');
        var tags = btn ? tagsOf(btn) : [];
        if (tags.some(function (t) { return t.toLowerCase() === tag.toLowerCase(); })) {
          return null;
        }
        tags.push(tag);
        return postForm("/gallery/tags", {path: path, tags: tags.join(", ")});
      });
    }, Promise.resolve());
  }

  function cssEscape(value) {
    return String(value).replace(/["\\]/g, "\\$&");
  }

  document.addEventListener("change", function (ev) {
    var pick = ev.target.closest ? ev.target.closest("[data-bulk-album]") : null;
    if (!pick || !pick.value || selected.length === 0) return;
    var albumID = pick.value;
    pick.value = "";
    bulkAlbum(albumID, selected.slice()).then(function () {
      exitSelect();
      refreshLayout();
    }).catch(function () {});
  });

  document.addEventListener("keydown", function (ev) {
    if (ev.key !== "Enter") return;
    var input = ev.target.closest ? ev.target.closest("[data-bulk-tag]") : null;
    if (!input) return;
    ev.preventDefault();
    var tag = input.value.trim();
    if (!tag || selected.length === 0) return;
    input.value = "";
    bulkTag(tag, selected.slice()).then(function () {
      exitSelect();
      refreshLayout();
    }).catch(function () {});
  });

  document.addEventListener("click", function (ev) {
    var del = ev.target.closest ? ev.target.closest("[data-bulk-delete]") : null;
    if (!del || selected.length === 0) return;
    var n = selected.length;
    if (!window.confirm("Delete " + n + (n === 1 ? " photo" : " photos") + "? This cannot be undone.")) {
      return;
    }
    bulkDelete(selected.slice()).then(function () {
      exitSelect();
      refreshLayout();
    }).catch(function () {});
  });

  // Drag & drop filing (desktop pointers; touch uses the modal instead):
  // drag a thumbnail onto a sidebar album to file it, onto a tag to tag it.
  document.addEventListener("dragstart", function (ev) {
    var btn = ev.target.closest ? ev.target.closest(".gallery-open") : null;
    if (!btn || !ev.dataTransfer) return;
    var path = btn.getAttribute("data-path");
    // Dragging any selected thumbnail drags the whole selection.
    var payload = isSelected(path) && selected.length > 0 ? selected.slice() : [path];
    ev.dataTransfer.setData("text/photo-path", JSON.stringify(payload));
    ev.dataTransfer.effectAllowed = "copy";
  });

  function dropTarget(el) {
    return el && el.closest ? el.closest("[data-drop-album],[data-drop-tag]") : null;
  }

  document.addEventListener("dragover", function (ev) {
    var row = dropTarget(ev.target);
    if (!row || !ev.dataTransfer) return;
    ev.preventDefault();
    ev.dataTransfer.dropEffect = "copy";
    row.classList.add("drop-hint");
  });

  document.addEventListener("dragleave", function (ev) {
    var row = dropTarget(ev.target);
    if (row) row.classList.remove("drop-hint");
  });

  document.addEventListener("dragend", function () {
    document.querySelectorAll(".drop-hint").forEach(function (el) {
      el.classList.remove("drop-hint");
    });
  });

  // Array values repeat the key (path=a&path=b), which is what the bulk
  // handlers read off r.Form.
  function postForm(url, params) {
    var parts = [];
    Object.keys(params).forEach(function (k) {
      var value = params[k];
      var list = Array.isArray(value) ? value : [value];
      list.forEach(function (v) {
        parts.push(encodeURIComponent(k) + "=" + encodeURIComponent(v));
      });
    });
    var body = parts.join("&");
    return fetch(url + "?format=json", {
      method: "POST",
      headers: {
        "Content-Type": "application/x-www-form-urlencoded",
        "X-CSRF-Token": csrf,
        "Accept": "application/json",
      },
      body: body,
    }).then(function (resp) {
      if (!resp.ok) throw new Error("drop failed");
      return resp.json();
    });
  }

  document.addEventListener("drop", function (ev) {
    var row = dropTarget(ev.target);
    if (!row || !ev.dataTransfer) return;
    ev.preventDefault();
    row.classList.remove("drop-hint");
    var raw = ev.dataTransfer.getData("text/photo-path");
    if (!raw) return;
    var paths;
    try {
      paths = JSON.parse(raw);
    } catch (e) {
      paths = [raw];
    }
    if (!Array.isArray(paths) || paths.length === 0) return;

    var albumID = row.getAttribute("data-drop-album");
    var tag = row.getAttribute("data-drop-tag");
    var done = function () {
      exitSelect();
      refreshLayout();
    };
    if (albumID) {
      bulkAlbum(albumID, paths).then(done).catch(function () {});
      return;
    }
    if (tag) {
      bulkTag(tag, paths).then(done).catch(function () {});
    }
  });

  function saveTags() {
    if (current < 0) return;
    var item = liveItems()[current];
    if (!item) return;
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
        refreshLayout();
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
      if (hasPreview) {
        video.poster = "/gallery/preview?path=" + encodeURIComponent(path);
      }
      video.controls = true;
      video.preload = "metadata";
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
    renderAlbumChecks(item);
  }

  function open(idx) {
    var list = liveItems();
    if (idx < 0 || idx >= list.length) return;
    current = idx;
    currentPath = list[current].getAttribute("data-path");
    render(list[current]);
    modal.hidden = false;
    document.body.style.overflow = "hidden";
  }

  function close() {
    modal.hidden = true;
    document.body.style.overflow = "";
    var media = stage.querySelector("video");
    if (media) media.pause();
    current = -1;
    currentPath = "";
  }

  function step(dir) {
    var list = liveItems();
    if (current < 0 || list.length === 0) return;
    open((current + dir + list.length) % list.length);
  }

  // Delegated: grid nodes are replaced by HTMX swaps.
  document.addEventListener("click", function (ev) {
    var btn = ev.target.closest ? ev.target.closest(".gallery-open") : null;
    if (!btn) return;
    var list = liveItems();
    var idx = list.indexOf(btn);

    // Ctrl/Cmd-click starts a selection without arming the mode first;
    // once in select mode plain clicks select instead of opening.
    if (selectMode || ev.metaKey || ev.ctrlKey || ev.shiftKey) {
      ev.preventDefault();
      selectMode = true;
      var path = btn.getAttribute("data-path");
      if (ev.shiftKey && lastIndex >= 0 && idx >= 0) {
        var from = Math.min(lastIndex, idx);
        var to = Math.max(lastIndex, idx);
        for (var i = from; i <= to; i++) {
          setSelected(list[i].getAttribute("data-path"), true);
        }
      } else {
        setSelected(path, !isSelected(path));
      }
      lastIndex = idx;
      paintSelection();
      return;
    }
    open(idx);
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
