#!/usr/bin/env sh
# Downloads htmx and html5-qrcode into internal/web/static/js so they can be
# served from our own origin instead of unpkg.com. After running it:
#   1. layout.html: use <script src="{{asset "js/htmx.min.js"}}"></script>
#   2. scan.html:   use <script src="{{asset "js/html5-qrcode.min.js"}}"></script>
#   3. middleware.go: drop https://unpkg.com from script-src
set -eu

dest="$(dirname "$0")/../internal/web/static/js"

curl -fsSL "https://unpkg.com/htmx.org@2.0.9/dist/htmx.min.js" -o "$dest/htmx.min.js"
curl -fsSL "https://unpkg.com/html5-qrcode@2.3.8/html5-qrcode.min.js" -o "$dest/html5-qrcode.min.js"

echo "Downloaded to $dest"
