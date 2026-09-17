/* Gallery lightbox: click a thumbnail for the large view with label form.
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
  var labelForm = modal.querySelector("[data-label-form]");
  var labelInput = modal.querySelector("#photo-modal-label");
  var items = Array.prototype.slice.call(document.querySelectorAll(".gallery-open"));
  var current = -1;

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

  function render(item) {
    var path = item.getAttribute("data-path");
    var name = item.getAttribute("data-name");
    var kind = item.getAttribute("data-kind");
    var label = item.getAttribute("data-label") || "";
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
    title.textContent = label || name;
    metaName.textContent = name;
    metaAdded.textContent = fmtBytes(item.getAttribute("data-size")) + " · " + item.getAttribute("data-modified");
    download.href = fileURL(path);
    labelInput.value = label;
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

  labelForm.addEventListener("submit", function (ev) {
    ev.preventDefault();
    if (current < 0) return;
    var item = items[current];
    var path = item.getAttribute("data-path");
    var label = labelInput.value.trim();
    var btn = labelForm.querySelector('button[type="submit"]');
    btn.disabled = true;
    fetch("/gallery/label?format=json", {
      method: "POST",
      headers: {
        "Content-Type": "application/x-www-form-urlencoded",
        "X-CSRF-Token": csrf,
        "Accept": "application/json",
      },
      body: "path=" + encodeURIComponent(path) + "&label=" + encodeURIComponent(label),
    })
      .then(function (resp) {
        if (!resp.ok) throw new Error("save failed");
        return resp.json();
      })
      .then(function (data) {
        var saved = (data && data.label) || "";
        item.setAttribute("data-label", saved);
        title.textContent = saved || item.getAttribute("data-name");
        var caption = item.parentElement.querySelector(".gallery-caption");
        if (caption) caption.textContent = saved || item.getAttribute("data-name");
        btn.textContent = "Saved ✓";
        setTimeout(function () { btn.textContent = "Save label"; }, 1500);
      })
      .catch(function () {
        btn.textContent = "Retry save";
      })
      .then(function () { btn.disabled = false; });
  });
})();
