// Mobile nav toggle. Smooth scrolling is handled in CSS (scroll-behavior).
(function () {
  "use strict";

  var toggle = document.querySelector(".nav-toggle");
  var nav = document.getElementById("site-nav");
  if (!toggle || !nav) return;

  function setOpen(open) {
    nav.classList.toggle("open", open);
    toggle.setAttribute("aria-expanded", String(open));
  }

  toggle.addEventListener("click", function () {
    setOpen(!nav.classList.contains("open"));
  });

  // Close the menu after picking a destination on mobile.
  nav.addEventListener("click", function (event) {
    if (event.target.closest("a")) setOpen(false);
  });

  // Close on Escape for keyboard users.
  document.addEventListener("keydown", function (event) {
    if (event.key === "Escape" && nav.classList.contains("open")) {
      setOpen(false);
      toggle.focus();
    }
  });
})();
