// vukem-studio: theme, keyboard shortcuts and dialogs. No dependencies.
(function () {
	"use strict";

	var root = document.documentElement;
	var stored = null;

	try {
		stored = localStorage.getItem("studio-theme");
	} catch (e) {
		stored = null;
	}

	var dark = stored ? stored === "dark" : window.matchMedia("(prefers-color-scheme: dark)").matches;
	root.classList.toggle("dark", dark);

	function onReady(fn) {
		if (document.readyState === "loading") {
			document.addEventListener("DOMContentLoaded", fn);
		} else {
			fn();
		}
	}

	function typing(el) {
		return el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.tagName === "SELECT" || el.isContentEditable);
	}

	onReady(function () {
		document.addEventListener("click", function (e) {
			var t = e.target.closest("[data-theme-toggle]");

			if (t) {
				var now = root.classList.toggle("dark");

				try {
					localStorage.setItem("studio-theme", now ? "dark" : "light");
				} catch (err) {
					/* storage can be unavailable; the choice then lasts for the page */
				}

				return;
			}

			var open = e.target.closest("[data-open]");

			if (open) {
				var d = document.getElementById(open.getAttribute("data-open"));

				if (d && d.showModal) {
					d.showModal();
				}
			}

			if (e.target.closest("[data-shortcuts]")) {
				var s = document.getElementById("shortcuts");

				if (s && s.showModal) {
					s.showModal();
				}
			}
		});

		var armed = false;

		document.addEventListener("keydown", function (e) {
			if (e.ctrlKey && e.key === "Enter") {
				var f = document.querySelector("form[data-console]");

				if (f) {
					e.preventDefault();
					f.requestSubmit();
				}

				return;
			}

			if (typing(e.target) || e.ctrlKey || e.metaKey || e.altKey) {
				return;
			}

			var link;

			if (armed) {
				armed = false;

				if (e.key === "t") {
					link = document.querySelector("[data-nav=tables]");
				} else if (e.key === "c") {
					link = document.querySelector("[data-nav=console]");
				}

				if (link) {
					e.preventDefault();
					window.location.href = link.getAttribute("href");
				}

				return;
			}

			switch (e.key) {
				case "/":
					var filter = document.querySelector("[data-filter]");

					if (filter) {
						e.preventDefault();
						filter.focus();
					}

					break;
				case "g":
					armed = true;
					break;
				case "[":
					link = document.querySelector("a[rel=prev]");
					break;
				case "]":
					link = document.querySelector("a[rel=next]");
					break;
				case "?":
					var help = document.getElementById("shortcuts");

					if (help && help.showModal) {
						help.showModal();
					}

					break;
			}

			if (link && (e.key === "[" || e.key === "]")) {
				window.location.href = link.getAttribute("href");
			}
		});
	});
})();
