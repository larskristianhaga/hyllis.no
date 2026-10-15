// scanner.js — drives the EAN-13 barcode scanner on the /scan page using
// html5-qrcode. Loaded only on that page, after htmx.org and html5-qrcode
// have been loaded via <script> tags.
(function () {
  "use strict";

  var READER_ID = "reader";
  var RESULT_SELECTOR = "#scan-result";
  var TORCH_BUTTON_ID = "torch-toggle";
  var LOADING_ID = "scan-loading";
  var AUTO_ADD_TOGGLE_ID = "auto-add-toggle";

  var html5Qrcode = null;
  var scanning = false;
  var torchOn = false;

  function vibrate() {
    if (window.navigator && typeof window.navigator.vibrate === "function") {
      window.navigator.vibrate(200);
    }
  }

  // beep plays a short confirmation tone when a code is read, so the user
  // gets feedback even where haptics are unsupported (iOS Safari).
  // Best-effort: any failure (no WebAudio, autoplay policy) is ignored.
  function beep() {
    try {
      var Ctx = window.AudioContext || window.webkitAudioContext;
      if (!Ctx) {
        return;
      }
      var ctx = new Ctx();
      var osc = ctx.createOscillator();
      var gain = ctx.createGain();
      osc.type = "sine";
      osc.frequency.value = 880;
      gain.gain.value = 0.08;
      osc.connect(gain);
      gain.connect(ctx.destination);
      osc.start();
      osc.stop(ctx.currentTime + 0.12);
      osc.onended = function () {
        ctx.close();
      };
    } catch (err) {
      // ignore
    }
  }

  // setLoading shows/hides the loading indicator for the camera path.
  // htmx.ajax() has no hx-indicator option, so toggle htmx's own
  // "htmx-request" class on the indicator element by hand.
  function setLoading(on) {
    var el = document.getElementById(LOADING_ID);
    if (el) {
      el.classList.toggle("htmx-request", on);
    }
  }

  // cameraErrorMessage turns a camera start failure into an actionable
  // message. Every message points at the manual form, which always works.
  function cameraErrorMessage(err) {
    var name = err && err.name ? err.name : "";
    var text = String(err || "");
    var manual = " Du kan fortsatt registrere ISBN manuelt under.";

    if (!window.isSecureContext) {
      return "Kameraet krever en sikker (HTTPS) tilkobling." + manual;
    }
    if (name === "NotAllowedError" || name === "SecurityError" || /NotAllowed|permission|denied/i.test(text)) {
      return (
        "Tilgang til kameraet er blokkert. Tillat kamera for denne siden i nettleserinnstillingene og last siden på nytt." +
        manual
      );
    }
    if (name === "NotFoundError" || name === "OverconstrainedError" || /no camera|not found/i.test(text)) {
      return "Fant ikke noe kamera på denne enheten." + manual;
    }
    if (name === "NotReadableError" || name === "AbortError") {
      return "Kameraet er i bruk av en annen app. Lukk den og last siden på nytt." + manual;
    }
    return "Kunne ikke starte kameraet." + manual;
  }

  function showFallbackMessage(message) {
    var target = document.querySelector(RESULT_SELECTOR);
    if (!target) {
      return;
    }
    target.innerHTML =
      '<div class="error-message" role="alert"><p>' + message + "</p></div>";
  }

  // isAutoAddEnabled reports whether the "Legg til automatisk" toggle is
  // checked — when it is, scanSubmit skips the confirm-before-adding step
  // so scanning many books in a row doesn't need a tap after each one.
  function isAutoAddEnabled() {
    var toggle = document.getElementById(AUTO_ADD_TOGGLE_ID);
    return !!toggle && toggle.checked;
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

    setLoading(true);
    window.htmx
      .ajax("POST", "/books/scan", {
        target: RESULT_SELECTOR,
        swap: "innerHTML",
        values: { isbn: code, source: "camera", auto: isAutoAddEnabled() ? "true" : "" },
      })
      .finally(function () {
        setLoading(false);
        resumeScanning();
      });
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
    beep();
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
      .catch(function (err) {
        showFallbackMessage(cameraErrorMessage(err));
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
