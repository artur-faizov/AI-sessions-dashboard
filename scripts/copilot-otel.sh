#!/usr/bin/env bash
# Enables, checks, or disables the GitHub Copilot OpenTelemetry file export that
# the dashboard's copilot provider reads for full token usage.
#
#   scripts/copilot-otel.sh enable|status|disable
#
# Copilot (CLI and the IDE language server) writes the export only when
# COPILOT_OTEL_FILE_EXPORTER_PATH is in its environment, so "enable" puts the
# variable everywhere Copilot can be started from:
#   - login shells: a marked block in ~/.profile (and ~/.bash_profile /
#     ~/.zprofile when the shell reads those)
#   - Linux desktop session: ~/.config/environment.d/60-copilot-otel.conf, plus
#     the running systemd/D-Bus user environment
#   - JetBrains IDE launchers (jetbrains-*.desktop): Exec= prefixed with
#     `env COPILOT_OTEL_FILE_EXPORTER_PATH=...` (system launchers are copied to
#     ~/.local/share/applications first)
#   - macOS: launchctl setenv for apps started from the Dock (until logout)
# Every step is idempotent. Running Copilot processes must be restarted.
set -euo pipefail

VAR=COPILOT_OTEL_FILE_EXPORTER_PATH
COPILOT_ROOT=${COPILOT_HOME:-$HOME/.copilot}
EXPORT_PATH=${COPILOT_OTEL_PATH:-$COPILOT_ROOT/otel/copilot-otel.jsonl}
BEGIN_MARK="# >>> copilot-otel (token-monitor) >>>"
END_MARK="# <<< copilot-otel (token-monitor) <<<"
ENVD_FILE="${XDG_CONFIG_HOME:-$HOME/.config}/environment.d/60-copilot-otel.conf"
APPS_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
COPIED_KEY="X-Copilot-OTel-Copied=true"
OS=$(uname -s)

say() { printf '  %-10s %s\n' "$1" "$2"; }

case "$EXPORT_PATH" in
/*) ;;
*)
	echo "COPILOT_OTEL_PATH must be absolute: $EXPORT_PATH" >&2
	exit 2
	;;
esac

# Shell profiles that login shells of this user read.
profiles() {
	echo "$HOME/.profile"
	if [ -f "$HOME/.bash_profile" ]; then echo "$HOME/.bash_profile"; fi
	case "${SHELL:-}" in */zsh) echo "$HOME/.zprofile" ;; esac
}

has_block() { [ -f "$1" ] && grep -qF "$BEGIN_MARK" "$1"; }
mentions_var() { [ -f "$1" ] && grep -q "$VAR" "$1"; }

# rewrite FILE: replaces FILE's content with stdin, keeping its inode and mode.
rewrite() {
	local tmp
	tmp=$(mktemp)
	cat >"$tmp"
	cat "$tmp" >"$1"
	rm -f "$tmp"
}

remove_block() {
	awk -v b="$BEGIN_MARK" -v e="$END_MARK" '
		$0 == b { skip = 1; next }
		$0 == e { skip = 0; next }
		!skip' "$1" | rewrite "$1"
}

# JetBrains launchers: user ones, plus system/snap/flatpak ones not overridden by a user one.
launchers() {
	local d f seen=" "
	for d in "$APPS_DIR" /usr/share/applications /usr/local/share/applications \
		/var/lib/snapd/desktop/applications /var/lib/flatpak/exports/share/applications \
		"$HOME/.local/share/flatpak/exports/share/applications"; do
		for f in "$d"/jetbrains-*.desktop; do
			[ -f "$f" ] || continue
			case "$seen" in *" $(basename "$f") "*) continue ;; esac
			seen="$seen$(basename "$f") "
			echo "$f"
		done
	done
}

exec_prefix() {
	case "$EXPORT_PATH" in
	*[[:space:]]*) printf 'env "%s=%s" ' "$VAR" "$EXPORT_PATH" ;;
	*) printf 'env %s=%s ' "$VAR" "$EXPORT_PATH" ;;
	esac
}

patch_launcher() {
	local src=$1 dst copied=0
	dst="$APPS_DIR/$(basename "$src")"
	if [ "$src" != "$dst" ]; then
		mkdir -p "$APPS_DIR"
		cp "$src" "$dst"
		copied=1
	elif grep -q "^Exec=.*$VAR" "$dst"; then
		say ok "$dst (already set)"
		return
	fi
	awk -v p="$(exec_prefix)" -v v="$VAR" -v c="$copied" -v k="$COPIED_KEY" '
		BEGIN { gsub(/&/, "\\\\&", p) }
		/^Exec=/ && index($0, v) == 0 { sub(/^Exec=/, "Exec=" p) }
		{ print }
		c == 1 && $0 == "[Desktop Entry]" { print k }' "$dst" | rewrite "$dst"
	if [ $copied = 1 ]; then say patched "$dst (copied from $src)"; else say patched "$dst"; fi
}

unpatch_launcher() {
	local f=$1
	if grep -qxF "$COPIED_KEY" "$f"; then
		rm -f "$f"
		say removed "$f"
		return
	fi
	grep -q "^Exec=.*$VAR" "$f" || return 0
	sed -E "s/^Exec=env (\"$VAR=[^\"]*\"|$VAR=[^ ]*) /Exec=/" "$f" | rewrite "$f"
	say restored "$f"
}

refresh_desktop_db() {
	if command -v update-desktop-database >/dev/null 2>&1; then
		update-desktop-database "$APPS_DIR" >/dev/null 2>&1 || true
	fi
}

enable() {
	echo "Enabling Copilot OpenTelemetry export to $EXPORT_PATH"
	mkdir -p "$(dirname "$EXPORT_PATH")"

	local p f found=0
	while IFS= read -r p; do
		if has_block "$p"; then
			remove_block "$p"
		elif mentions_var "$p"; then
			say skip "$p (already sets $VAR outside the managed block)"
			continue
		fi
		printf '%s\nexport %s="%s"\n%s\n' "$BEGIN_MARK" "$VAR" "$EXPORT_PATH" "$END_MARK" >>"$p"
		say profile "$p"
	done < <(profiles)

	if [ "$OS" = Linux ]; then
		mkdir -p "$(dirname "$ENVD_FILE")"
		printf '# GitHub Copilot OpenTelemetry token usage export (token-monitor)\n%s=%s\n' "$VAR" "$EXPORT_PATH" >"$ENVD_FILE"
		say env.d "$ENVD_FILE"
		if command -v systemctl >/dev/null 2>&1 &&
			systemctl --user set-environment "$VAR=$EXPORT_PATH" >/dev/null 2>&1; then
			say session "systemd user environment updated"
		fi
		if command -v dbus-update-activation-environment >/dev/null 2>&1 &&
			env "$VAR=$EXPORT_PATH" dbus-update-activation-environment --systemd "$VAR" >/dev/null 2>&1; then
			say session "D-Bus activation environment updated"
		fi
		while IFS= read -r f; do
			found=1
			patch_launcher "$f"
		done < <(launchers)
		if [ $found = 1 ]; then refresh_desktop_db; else say launcher "no jetbrains-*.desktop launcher found"; fi
	elif [ "$OS" = Darwin ]; then
		launchctl setenv "$VAR" "$EXPORT_PATH" && say launchctl "set for this login session (Dock-started apps)"
	fi

	echo
	echo "Fully quit and restart Copilot CLI sessions and IDEs so they pick up $VAR."
	echo "Current shell: export $VAR=\"$EXPORT_PATH\" (new login shells get it automatically)"
	echo "Check with: make copilot-otel-status"
}

status() {
	local rc=0 p f
	echo "Copilot OpenTelemetry export: $EXPORT_PATH"
	if [ -f "$EXPORT_PATH" ]; then
		say file "$(wc -c <"$EXPORT_PATH" | tr -d ' ') bytes, modified $(date -r "$EXPORT_PATH" '+%F %T')"
	else
		say file "missing (created once a restarted Copilot makes a model call)"
	fi
	while IFS= read -r p; do
		if has_block "$p"; then
			say profile "$p: managed block"
		elif mentions_var "$p"; then
			say profile "$p: sets $VAR (manual)"
		else
			say profile "$p: not set"
			rc=1
		fi
	done < <(profiles)
	if [ "$OS" = Linux ]; then
		if grep -qs "^$VAR=" "$(dirname "$ENVD_FILE")"/*.conf; then
			say env.d "set"
		else
			say env.d "not set"
			rc=1
		fi
		while IFS= read -r f; do
			if [ "$f" = "$APPS_DIR/$(basename "$f")" ] && grep -q "^Exec=.*$VAR" "$f"; then
				say launcher "$f: set"
			else
				say launcher "$f: not set"
				rc=1
			fi
		done < <(launchers)
		local pid cmd val any=0
		for pid in $(pgrep -f copilot 2>/dev/null || true); do
			[ "$pid" = $$ ] && continue
			cmd=$({ tr '\0' ' ' <"/proc/$pid/cmdline"; } 2>/dev/null) || continue
			case "$cmd" in
			*copilot-language-server* | *@github/copilot* | copilot\ * | */copilot\ *) ;;
			*) continue ;;
			esac
			any=1
			val=$({ tr '\0' '\n' <"/proc/$pid/environ"; } 2>/dev/null | sed -n "s/^$VAR=//p" | head -n1) || true
			if [ -n "$val" ]; then
				say running "pid $pid exports to $val: ${cmd:0:80}"
			else
				say running "pid $pid has no $VAR, restart it: ${cmd:0:80}"
				rc=1
			fi
		done
		if [ $any = 0 ]; then say running "no Copilot process running"; fi
	elif [ "$OS" = Darwin ]; then
		local v
		v=$(launchctl getenv "$VAR" || true)
		if [ -n "$v" ]; then say launchctl "$v"; else
			say launchctl "not set"
			rc=1
		fi
	fi
	echo
	if [ $rc = 0 ]; then
		echo "Export is enabled everywhere checked."
	else
		echo "Not enabled everywhere: run 'make copilot-otel-enable', then restart what is listed above."
	fi
}

disable() {
	echo "Disabling the managed Copilot OpenTelemetry settings (export files are kept)"
	local p f
	while IFS= read -r p; do
		if has_block "$p"; then
			remove_block "$p"
			say removed "$p block"
		elif mentions_var "$p"; then
			say manual "$p sets $VAR outside the managed block; edit it by hand"
		fi
	done < <(profiles)
	if [ "$OS" = Linux ]; then
		if [ -f "$ENVD_FILE" ]; then
			rm -f "$ENVD_FILE"
			say removed "$ENVD_FILE"
		fi
		if command -v systemctl >/dev/null 2>&1; then
			systemctl --user unset-environment "$VAR" >/dev/null 2>&1 || true
		fi
		for f in "$APPS_DIR"/jetbrains-*.desktop; do
			if [ -f "$f" ]; then unpatch_launcher "$f"; fi
		done
		refresh_desktop_db
	elif [ "$OS" = Darwin ]; then
		launchctl unsetenv "$VAR" || true
	fi
	echo "Restart Copilot CLI sessions and IDEs to stop the export."
}

case "${1:-}" in
enable) enable ;;
status) status ;;
disable) disable ;;
*)
	echo "usage: $0 enable|status|disable" >&2
	exit 2
	;;
esac
