#!/bin/sh
set -eu

cloud_console_url="${VITE_CLOUD_CONSOLE_URL:-}"
case "$cloud_console_url" in
  https://?*|http://localhost?*|http://127.0.0.1?*) ;;
  *)
    echo "VITE_CLOUD_CONSOLE_URL must be an absolute HTTPS URL (or localhost HTTP URL)" >&2
    exit 1
    ;;
esac

# Keep the generated JavaScript safe without introducing a runtime dependency
# into this minimal Nginx image. The URL is decoded by the browser before the
# SPA module loads.
encoded_cloud_console_url="$(printf '%s' "$cloud_console_url" | base64 | tr -d '\n')"
printf 'window.__AURORA_RUNTIME_CONFIG__ = Object.freeze({"cloudConsoleUrl":decodeURIComponent(escape(atob("%s")))});\n' "$encoded_cloud_console_url" \
  > /usr/share/nginx/html/runtime-config.js
