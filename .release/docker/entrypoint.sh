#!/usr/bin/env sh
# SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
# SPDX-License-Identifier: Apache-2.0
set -e

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
        echo "Entrypoint script for tr1d1um Server ${VERSION} started."

        if [ ! -s /etc/tr1d1um/tr1d1um.yaml ]; then
            echo "Building out template for file"
            /bin/spruce merge /tmp/tr1d1um_spruce.yaml > /etc/tr1d1um/tr1d1um.yaml
        fi
    fi

    exec "$@"
}

_main "$@"
