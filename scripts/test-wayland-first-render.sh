#!/usr/bin/env bash
set -euo pipefail

iterations=${WAYLAND_STRESS_ITERATIONS:-100}
settle_seconds=${WAYLAND_STRESS_SETTLE_SECONDS:-1}
binary=${GO2TV_WAYLAND_BINARY:-build/go2tv}
failure_dir=${GO2TV_WAYLAND_STRESS_FAILURE_DIR:-build/wayland-stress}

if [[ -z ${WAYLAND_DISPLAY:-} ]]; then
	echo "WAYLAND_DISPLAY is required" >&2
	exit 1
fi
if [[ ! -x $binary ]]; then
	echo "Go2TV binary is not executable: $binary" >&2
	exit 1
fi
if ! [[ $iterations =~ ^[1-9][0-9]*$ ]]; then
	echo "WAYLAND_STRESS_ITERATIONS must be a positive integer" >&2
	exit 1
fi
if ! [[ $settle_seconds =~ ^[0-9]+([.][0-9]+)?$ ]]; then
	echo "WAYLAND_STRESS_SETTLE_SECONDS must be a non-negative number" >&2
	exit 1
fi

stress_tmp=$(mktemp -d)
app_pid=
cleanup() {
	if [[ -n $app_pid ]] && kill -0 "$app_pid" 2>/dev/null; then
		kill -TERM "$app_pid" 2>/dev/null || true
		wait "$app_pid" 2>/dev/null || true
	fi
	rm -rf "$stress_tmp"
}
trap cleanup EXIT INT TERM

stop_app() {
	if ! kill -0 "$app_pid" 2>/dev/null; then
		wait "$app_pid" 2>/dev/null || true
		app_pid=
		return
	fi

	kill -INT "$app_pid" 2>/dev/null || true
	for _ in $(seq 1 100); do
		if ! kill -0 "$app_pid" 2>/dev/null; then
			wait "$app_pid" 2>/dev/null || true
			app_pid=
			return
		fi
		sleep 0.01
	done

	kill -TERM "$app_pid" 2>/dev/null || true
	wait "$app_pid" 2>/dev/null || true
	app_pid=
}

last_configure() {
	sed -nE 's/.*xdg_toplevel#[0-9]+[.]configure\(([0-9]+), ([0-9]+),.*/\1 \2/p' "$1" |
		awk '$1 > 0 && $2 > 0 { pair = $1 " " $2 } END { print pair }'
}

last_destination() {
	local main_surface viewport
	main_surface=$(sed -nE 's/.*get_xdg_surface\(new id xdg_surface#[0-9]+, wl_surface#([0-9]+)\).*/\1/p' "$1" | tail -n 1)
	if [[ -z $main_surface ]]; then
		return
	fi
	viewport=$(sed -nE "s/.*get_viewport\\(new id wp_viewport#([0-9]+), wl_surface#$main_surface\\).*/\\1/p" "$1" | tail -n 1)
	if [[ -z $viewport ]]; then
		return
	fi

	sed -nE "s/.*wp_viewport#$viewport[.]set_destination\\(([0-9]+), ([0-9]+)\\).*/\\1 \\2/p" "$1" |
		awk '$1 > 0 && $2 > 0 { pair = $1 " " $2 } END { print pair }'
}

hyprland_client_size() {
	hyprctl -j clients 2>/dev/null |
		jq -r --argjson pid "$1" 'first(.[] | select(.pid == $pid and .mapped == true) | "\(.size[0]) \(.size[1])") // empty'
}

capture_failure() {
	local iteration=$1 log_path=$2
	cp "$log_path" "$failure_dir/launch-$iteration.log"
	if [[ $check_hyprland == true ]]; then
		hyprctl -j clients >"$failure_dir/launch-$iteration-hyprland-clients.json" 2>/dev/null || true
		hyprctl -j monitors >"$failure_dir/launch-$iteration-hyprland-monitors.json" 2>/dev/null || true
		if command -v grim >/dev/null 2>&1; then
			geometry=$(jq -r --argjson pid "$app_pid" \
				'first(.[] | select(.pid == $pid) | "\(.at[0]),\(.at[1]) \(.size[0])x\(.size[1])") // empty' \
				"$failure_dir/launch-$iteration-hyprland-clients.json")
			if [[ -n $geometry ]]; then
				grim -g "$geometry" "$failure_dir/launch-$iteration.png" 2>/dev/null || true
			fi
		fi
	elif command -v grim >/dev/null 2>&1; then
		grim "$failure_dir/launch-$iteration.png" 2>/dev/null || true
	fi
}

mkdir -p "$failure_dir"
check_hyprland=false
if command -v hyprctl >/dev/null 2>&1 && command -v jq >/dev/null 2>&1 && hyprctl -j monitors >/dev/null 2>&1; then
	check_hyprland=true
	hyprctl -j monitors >"$failure_dir/hyprland-monitors.json"
fi
results_path=$failure_dir/results.tsv
printf 'iteration\tconfigure\tviewport\thyprland\n' >"$results_path"
echo "Wayland first-render stress: $iterations launches, ${settle_seconds}s settle"
if [[ $check_hyprland == true ]]; then
	echo "Hyprland client geometry checks enabled"
fi

for iteration in $(seq 1 "$iterations"); do
	log_path=$stress_tmp/wayland-$iteration.log
	config_path=$stress_tmp/config-$iteration
	mkdir -p "$config_path"

	WAYLAND_DEBUG=1 \
	XDG_CONFIG_HOME="$config_path" \
	LANG=en_US.UTF-8 \
	"$binary" > /dev/null 2>"$log_path" &
	app_pid=$!

	mapped=false
	for _ in $(seq 1 250); do
		if ! kill -0 "$app_pid" 2>/dev/null; then
			break
		fi
		if [[ -n $(last_configure "$log_path") && -n $(last_destination "$log_path") ]]; then
			mapped=true
			break
		fi
		sleep 0.02
	done

	if [[ $mapped != true ]]; then
		capture_failure "$iteration" "$log_path"
		stop_app
		echo "launch $iteration did not map; artifacts: $failure_dir" >&2
		exit 1
	fi

	sleep "$settle_seconds"
	configure=$(last_configure "$log_path")
	destination=$(last_destination "$log_path")
	hyprland_size=
	if [[ $check_hyprland == true ]]; then
		hyprland_size=$(hyprland_client_size "$app_pid")
	fi
	printf '%d\t%s\t%s\t%s\n' "$iteration" "$configure" "$destination" "$hyprland_size" >>"$results_path"
	if [[ $configure != "$destination" || ($check_hyprland == true && $configure != "$hyprland_size") ]]; then
		capture_failure "$iteration" "$log_path"
		stop_app
		echo "launch $iteration mismatch: configure=$configure viewport=$destination hyprland=$hyprland_size" >&2
		echo "artifacts: $failure_dir" >&2
		exit 1
	fi

	stop_app
	if (( iteration % 10 == 0 || iteration == iterations )); then
		echo "passed $iteration/$iterations"
	fi
done

echo "Wayland first-render stress passed: $iterations/$iterations"
echo "results: $results_path"
