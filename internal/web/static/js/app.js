// app.js — small site-wide htmx glue, loaded on every page (deferred, after
// htmx.org). Surfaces failed htmx requests to the user instead of leaving
// the page looking frozen.
(function () {
  "use strict";

  var TOAST_ID = "toast";
  var hideTimer = null;

  function showToast(message) {
    var toast = document.getElementById(TOAST_ID);
    if (!toast) {
      return;
    }
    toast.textContent = message;
    toast.hidden = false;
    window.clearTimeout(hideTimer);
    hideTimer = window.setTimeout(function () {
      toast.hidden = true;
    }, 6000);
  }

  // The server answered, but with an error status (htmx doesn't swap those).
  document.body.addEventListener("htmx:responseError", function (event) {
    var status = event.detail && event.detail.xhr ? event.detail.xhr.status : 0;
    if (status === 404) {
      showToast("Fant ikke det du ba om.");
    } else if (status === 403) {
      showToast("Forespørselen ble avvist. Last inn siden på nytt og prøv igjen.");
    } else {
      showToast("Noe gikk galt. Prøv igjen.");
    }
  });

  // The request never got an answer (offline, DNS, dropped connection).
  document.body.addEventListener("htmx:sendError", function () {
    showToast("Ingen kontakt med serveren. Sjekk nettforbindelsen og prøv igjen.");
  });
})();
