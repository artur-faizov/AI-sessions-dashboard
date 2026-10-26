#!/usr/bin/env bash
# Wraps a built dashboard binary in a macOS .app bundle so it can be launched
# from Finder, Spotlight, or the Dock (the Dock only accepts .app).
#
#   scripts/macos-app.sh <binary> <out-dir>
#
# The bundle is a stay-open AppleScript applet: launching it starts the
# dashboard (which opens the browser), clicking the Dock icon again reopens the
# page, and Quit stops the dashboard. A plain shell-script bundle can't do
# this: macOS kills the server when the script exits, and if the script stays
# running instead, Dock clicks and Quit are ignored. The binary is copied
# into the bundle, so the .app is self-contained. Output goes to
# ~/Library/Logs/TokenMonitor.log.
set -euo pipefail

bin=${1:?usage: macos-app.sh <binary> <out-dir>}
out=${2:?usage: macos-app.sh <binary> <out-dir>}
app="$out/TokenMonitor.app"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

cat > "$tmp/main.applescript" <<'EOF'
on run
	ctl("open")
end run

on reopen
	ctl("open")
end reopen

on quit
	ctl("stop")
	continue quit
end quit

on ctl(action)
	set script_ to POSIX path of (path to me) & "Contents/Resources/ctl.sh"
	do shell script quoted form of script_ & " " & action
end ctl
EOF

# ctl.sh open: start the dashboard, or reopen its URL if it is already up.
# ctl.sh stop: stop it.
cat > "$tmp/ctl.sh" <<'EOF'
#!/bin/zsh
bin="${0:A:h}/dashboard"
log=~/Library/Logs/TokenMonitor.log
case $1 in
  open)
    if pgrep -qf "^$bin"; then
      url=$(grep -o 'http://localhost:[0-9]*' "$log" | tail -1)
      open "${url:-http://localhost:8787}"
    else
      nohup "$bin" >> "$log" 2>&1 &
    fi ;;
  stop)
    pkill -f "^$bin" || true ;;
esac
EOF

rm -rf "$app"
mkdir -p "$out"
osacompile -s -o "$app" "$tmp/main.applescript"
cp "$tmp/ctl.sh" "$app/Contents/Resources/ctl.sh"
cp "$bin" "$app/Contents/Resources/dashboard"
chmod +x "$app/Contents/Resources/ctl.sh" "$app/Contents/Resources/dashboard"
plutil -replace CFBundleName -string TokenMonitor "$app/Contents/Info.plist"
plutil -replace CFBundleIdentifier -string local.tokenmonitor.dashboard "$app/Contents/Info.plist"
# Re-sign ad hoc: the files added above invalidate osacompile's signature.
codesign --force --deep -s - "$app" 2>/dev/null

echo "Built $app"
