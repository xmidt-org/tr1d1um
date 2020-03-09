#!/usr/bin/env sh


set -e

# check to see if this file is being run or sourced from another script
_is_sourced() {
	# https://unix.stackexchange.com/a/215279
	[ "${#FUNCNAME[@]}" -ge 2 ] \
		&& [ "${FUNCNAME[0]}" = '_is_sourced' ] \
		&& [ "${FUNCNAME[1]}" = 'source' ]
}

# check arguments for an option that would cause /tr1d1um to stop
# return true if there is one
_want_help() {
	local arg
	for arg; do
		case "$arg" in
			-'?'|--help|-v)
				return 0
				;;
		esac
	done
	return 1
}

_main() {
	# if command starts with an option, prepend tr1d1um
	if [ "${1:0:1}" = '-' ]; then
		set -- /tr1d1um "$@"
	fi
		# skip setup if they aren't running /tr1d1um or want an option that stops /tr1d1um
	if [ "$1" = '/tr1d1um' ] && ! _want_help "$@"; then
		echo "Entrypoint script for Tr1d1um Server ${VERSION} started."

		if [ ! -s /etc/tr1d1um/tr1d1um.yaml ]; then
		  echo "Building out template for file"
		  /spruce merge /tr1d1um.yaml /tmp/tr1d1um_spruce.yaml > /etc/tr1d1um/tr1d1um.yaml
		fi
	fi

	exec "$@"
}

# If we are sourced from elsewhere, don't perform any further actions
if ! _is_sourced; then
	_main "$@"
fi