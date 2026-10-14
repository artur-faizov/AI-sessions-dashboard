#!/usr/bin/env bash
# Wraps a built dashboard binary in a minimal macOS .app bundle so it can be
# launched from Finder, Spotlight, or the Dock (the Dock only accepts .app).
#
#   scripts/macos-app.sh <binary> <out-dir>
#
# The binary is copied into the bundle, so the .app is self-contained. The
# dashboard opens itself in the browser; its output goes to
# ~/Library/Logs/TokenMonitor.log.
set -euo pipefail

bin=${1:?usage: macos-app.sh <binary> <out-dir>}
out=${2:?usage: macos-app.sh <binary> <out-dir>}
app="$out/TokenMonitor.app"

rm -rf "$app"
mkdir -p "$app/Contents/MacOS"
cp "$bin" "$app/Contents/MacOS/dashboard"

cat > "$app/Contents/MacOS/TokenMonitor" <<'EOF'
#!/bin/zsh
exec "${0:A:h}/dashboard" >> ~/Library/Logs/TokenMonitor.log 2>&1
EOF
chmod +x "$app/Contents/MacOS/TokenMonitor" "$app/Contents/MacOS/dashboard"

cat > "$app/Contents/Info.plist" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>TokenMonitor</string>
  <key>CFBundleIdentifier</key><string>local.tokenmonitor.dashboard</string>
  <key>CFBundleExecutable</key><string>TokenMonitor</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleVersion</key><string>1.0</string>
</dict>
</plist>
EOF

echo "Built $app"
