// scanner.js — drives the EAN-13 barcode scanner on the /scan page using
// html5-qrcode. Loaded only on that page, after htmx.org and html5-qrcode
// have been loaded via <script> tags.
(function () {
  "use strict";

  var READER_ID = "reader";
  var RESULT_SELECTOR = "#scan-result";
  var TORCH_BUTTON_ID = "torch-toggle";

  var html5Qrcode = null;
  var scanning = false;
  var torchOn = false;

  function vibrate() {
    if (window.navigator && typeof window.navigator.vibrate === "function") {
      window.navigator.vibrate(200);
    }
  }

  function showFallbackMessage(message) {
    var target = document.querySelector(RESULT_SELECTOR);
    if (!target) {
      return;
    }
    target.innerHTML =
      '<div class="error-message" role="alert"><p>' + message + "</p></div>";
  }

  // submitCode POSTs a decoded/typed barcode to /books/scan via htmx.ajax
  // and resumes scanning once the lookup settles, whether it succeeded or
  // failed.
  function submitCode(code) {
    if (typeof window.htmx === "undefined") {
      showFallbackMessage(
        "Kunne ikke sende koden videre. Bruk manuell registrering under."
      );
      resumeScanning();
      return;
    }

    window.htmx
      .ajax("POST", "/books/scan", {
        target: RESULT_SELECTOR,
        swap: "innerHTML",
        values: { isbn: code, source: "camera" },
      })
      .finally(resumeScanning);
  }

  function resumeScanning() {
    scanning = true;
    if (html5Qrcode) {
      try {
        html5Qrcode.resume();
      } catch (err) {
        // Scanner may already be running/stopped; safe to ignore.
      }
    }
  }

  // onScanSuccess fires whenever the camera decodes an EAN-13 barcode. It
  // pauses scanning for the duration of the lookup so the same code isn't
  // submitted repeatedly while the camera keeps seeing it.
  function onScanSuccess(decodedText) {
    if (!scanning) {
      return;
    }
    scanning = false;
    vibrate();
    if (html5Qrcode) {
      html5Qrcode.pause(true);
    }
    submitCode(decodedText);
  }

  function setupTorchButton() {
    var torchBtn = document.getElementById(TORCH_BUTTON_ID);
    if (!torchBtn || !html5Qrcode || typeof html5Qrcode.getRunningTrackCameraCapabilities !== "function") {
      return;
    }

    var capabilities;
    try {
      capabilities = html5Qrcode.getRunningTrackCameraCapabilities();
    } catch (err) {
      return;
    }

    var torchFeature = capabilities && capabilities.torchFeature && capabilities.torchFeature();
    if (!torchFeature || !torchFeature.isSupported || !torchFeature.isSupported()) {
      // No torch support (e.g. iOS Safari) — keep the button hidden.
      return;
    }

    torchBtn.hidden = false;
    torchBtn.addEventListener("click", function () {
      torchOn = !torchOn;
      torchFeature
        .apply(torchOn)
        .then(function () {
          torchBtn.setAttribute("aria-pressed", String(torchOn));
          torchBtn.textContent = torchOn ? "Lommelykt av" : "Lommelykt på";
        })
        .catch(function () {
          torchOn = !torchOn; // revert on failure
        });
    });
  }

  function startScanner() {
    if (typeof window.Html5Qrcode === "undefined") {
      showFallbackMessage(
        "Kunne ikke laste skannerbiblioteket. Bruk manuell registrering under."
      );
      return;
    }

    html5Qrcode = new window.Html5Qrcode(READER_ID, {
      formatsToSupport: [window.Html5QrcodeSupportedFormats.EAN_13],
      verbose: false,
    });

    html5Qrcode
      .start(
        { facingMode: "environment" },
        { fps: 10, qrbox: { width: 260, height: 160 } },
        onScanSuccess
      )
      .then(function () {
        scanning = true;
        setupTorchButton();
      })
      .catch(function () {
        showFallbackMessage(
          "Fikk ikke tilgang til kameraet. Bruk manuell registrering under."
        );
      });
  }

  function init() {
    if (document.getElementById(READER_ID)) {
      startScanner();
    }
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }

  window.addEventListener("beforeunload", function () {
    if (html5Qrcode && scanning) {
      html5Qrcode.stop().catch(function () {});
    }
  });
})();
