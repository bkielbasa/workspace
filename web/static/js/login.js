(function () {
  "use strict";

  var form = document.getElementById("login-form");
  if (!form) return;

  var errorEl = document.getElementById("error");
  var submitBtn = document.getElementById("submit");

  form.addEventListener("submit", function (event) {
    event.preventDefault();

    errorEl.hidden = true;
    submitBtn.disabled = true;
    submitBtn.textContent = "Signing in\u2026";

    var email = document.getElementById("email").value.trim();
    var password = document.getElementById("password").value;

    fetch("/login", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email: email, password: password }),
    })
      .then(function (res) {
        return res.json().then(function (data) {
          return { ok: res.ok, data: data };
        });
      })
      .then(function (result) {
        if (result.ok) {
          window.location.replace("/");
          return;
        }
        throw new Error(
          (result.data && result.data.error) || "Sign in failed."
        );
      })
      .catch(function (err) {
        errorEl.textContent = err.message || "Sign in failed.";
        errorEl.hidden = false;
        submitBtn.disabled = false;
        submitBtn.textContent = "Sign in";
      });
  });
})();
