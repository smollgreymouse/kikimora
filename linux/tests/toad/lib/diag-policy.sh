#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later

# Policy overlay for lib/diag.sh.
# Source this after diag.sh. It keeps the existing network/test harness intact,
# but separates protocol/data-plane acceptance from application-site behavior
# and makes failure archives more complete.

DIAG_WARNING_COUNT="${DIAG_WARNING_COUNT:-0}"
DIAG_DATA_PLANE_RESULT="${DIAG_DATA_PLANE_RESULT:-NOT_RUN}"
DIAG_CHATGPT_RESULT="${DIAG_CHATGPT_RESULT:-NOT_RUN}"
DIAG_TRAFFIC_START_MS="${DIAG_TRAFFIC_START_MS:-}"

diag_warn() {
    DIAG_WARNING_COUNT=$((DIAG_WARNING_COUNT + 1))
    printf '[%s] %s\n' "$(date -Is)" "$*" >>"$DIAG_DIR/warnings.txt"
    printf 'WARNING: %s\n' "$*" >&2
}

diag_log() {
    local message="$*"

    case "$message" in
        "run strict Google 200 probe and ChatGPT access probe through isolated Toad interface")
            DIAG_TRAFFIC_START_MS="$(diag_now_ms)"
            ;;
        "PASS: Google returned 200, ChatGPT returned 2xx, response sizes passed, and address trace proved both crossed "*)
            DIAG_DATA_PLANE_RESULT="PASS"
            if [[ "$DIAG_CHATGPT_RESULT" == "NOT_RUN" ]]; then
                if [[ "$DIAG_CHATGPT_HTTP_CODE" =~ ^2[0-9][0-9]$ ]]; then
                    DIAG_CHATGPT_RESULT="PASS_2XX"
                elif [[ "$DIAG_CHATGPT_HTTP_CODE" =~ ^[1-5][0-9][0-9]$ ]]; then
                    DIAG_CHATGPT_RESULT="WARN_HTTP_${DIAG_CHATGPT_HTTP_CODE}"
                else
                    DIAG_CHATGPT_RESULT="WARN_TRANSPORT_FAILED"
                fi
            fi
            message="PASS: VPN data plane proved through $DIAG_INTERFACE; ChatGPT application result=$DIAG_CHATGPT_RESULT"
            ;;
    esac

    printf '[%s] %s\n' "$(date -Is)" "$message" | tee -a "$DIAG_DIR/command.log"
}

diag_fail() {
    local message="$*"

    case "$message" in
        "ChatGPT probe did not return 2xx with at least "*)
            if [[ "$DIAG_CHATGPT_HTTP_CODE" =~ ^[1-5][0-9][0-9]$ && "$DIAG_CHATGPT_REMOTE_IP" == "$DIAG_TEST_IP" ]]; then
                DIAG_CHATGPT_RESULT="WARN_HTTP_${DIAG_CHATGPT_HTTP_CODE}"
                diag_warn "ChatGPT returned HTTP $DIAG_CHATGPT_HTTP_CODE through $DIAG_INTERFACE; transport is proven, but application access is not 2xx"
            else
                DIAG_CHATGPT_RESULT="WARN_TRANSPORT_FAILED"
                diag_warn "ChatGPT application probe did not return a valid HTTP response through $DIAG_INTERFACE; protocol acceptance continues using the independent Google data-plane proof"
            fi
            return 0
            ;;
        "address trace did not observe ChatGPT traffic on "*)
            diag_warn "$message"
            return 0
            ;;
    esac

    diag_record_error "$message"
    printf 'ERROR: %s\n' "$message" >&2
    return 1
}

diag_collect_after_once() {
    local now

    if (( DIAG_AFTER_COLLECTED )); then
        return 0
    fi

    if [[ -n "$DIAG_TRAFFIC_START_MS" && -z "$DIAG_TRAFFIC_MS" ]]; then
        now="$(diag_now_ms)"
        DIAG_TRAFFIC_MS=$((now - DIAG_TRAFFIC_START_MS))
    fi

    if (( DIAG_NETNS_CREATED )) && diag_netns_exists && \
        sudo ip -n "$DIAG_NETNS" link show dev "$DIAG_INTERFACE" >/dev/null 2>&1; then
        if [[ -z "$DIAG_RX_AFTER" ]]; then
            DIAG_RX_AFTER="$(diag_read_counter rx_bytes 2>/dev/null || true)"
        fi
        if [[ -z "$DIAG_TX_AFTER" ]]; then
            DIAG_TX_AFTER="$(diag_read_counter tx_bytes 2>/dev/null || true)"
        fi
    fi

    collect_diag "$DIAG_DIR" after
    diag_capture_state "$DIAG_DIR/state-after.json"
    diag_collect_runtime
    DIAG_AFTER_COLLECTED=1
}

diag_write_metadata() {
    local status="$1"
    local ended_at end_ms duration_ms data_plane_result chatgpt_result

    ended_at="$(date -Is)"
    end_ms="$(diag_now_ms)"
    duration_ms=$((end_ms - DIAG_START_MS))

    data_plane_result="$DIAG_DATA_PLANE_RESULT"
    if [[ "$data_plane_result" == "NOT_RUN" && "$DIAG_RESULT" == "FAIL" ]]; then
        data_plane_result="FAIL_OR_INCOMPLETE"
    fi

    chatgpt_result="$DIAG_CHATGPT_RESULT"
    if [[ "$chatgpt_result" == "NOT_RUN" ]]; then
        if [[ "$DIAG_CHATGPT_HTTP_CODE" =~ ^2[0-9][0-9]$ ]]; then
            chatgpt_result="PASS_2XX"
        elif [[ "$DIAG_CHATGPT_HTTP_CODE" =~ ^[1-5][0-9][0-9]$ ]]; then
            chatgpt_result="WARN_HTTP_${DIAG_CHATGPT_HTTP_CODE}"
        elif [[ -n "$DIAG_CHATGPT_HTTP_CODE" || -n "$DIAG_CHATGPT_REMOTE_IP" ]]; then
            chatgpt_result="WARN_TRANSPORT_OR_RESPONSE_INVALID"
        fi
    fi

    [[ -e "$DIAG_DIR/warnings.txt" ]] || : >"$DIAG_DIR/warnings.txt"

    {
        printf 'started_at=%s\n' "$DIAG_STARTED_AT"
        printf 'ended_at=%s\n' "$ended_at"
        printf 'duration_ms=%s\n' "$duration_ms"
        printf 'result=%s\n' "$DIAG_RESULT"
        printf 'data_plane_result=%s\n' "$data_plane_result"
        printf 'chatgpt_application_result=%s\n' "$chatgpt_result"
        printf 'warning_count=%s\n' "$DIAG_WARNING_COUNT"
        printf 'exit_status=%s\n' "$status"
        printf 'protocol=%s\n' "$DIAG_PROTOCOL"
        printf 'test_scope=isolated-real-vps-preflight\n'
        printf 'network_namespace=%s\n' "${DIAG_NETNS:-unavailable}"
        printf 'uplink_interface=%s\n' "$DIAG_UPLINK_INTERFACE"
        printf 'interface=%s\n' "$DIAG_INTERFACE"
        printf 'ifindex=%s\n' "${DIAG_IFINDEX:-unavailable}"
        printf 'mtu=%s\n' "${DIAG_MTU:-unavailable}"
        printf 'google_test_host=%s\n' "$DIAG_GOOGLE_HOST"
        printf 'google_test_ip=%s\n' "${DIAG_GOOGLE_IP:-unavailable}"
        printf 'google_http_code=%s\n' "${DIAG_GOOGLE_HTTP_CODE:-unavailable}"
        printf 'google_response_bytes=%s\n' "${DIAG_GOOGLE_BYTES:-unavailable}"
        printf 'google_remote_ip=%s\n' "${DIAG_GOOGLE_REMOTE_IP:-unavailable}"
        printf 'google_minimum_bytes=%s\n' "$DIAG_GOOGLE_MIN_BYTES"
        printf 'chatgpt_test_host=%s\n' "$DIAG_TEST_HOST"
        printf 'chatgpt_test_ip=%s\n' "${DIAG_TEST_IP:-unavailable}"
        printf 'chatgpt_http_code=%s\n' "${DIAG_CHATGPT_HTTP_CODE:-unavailable}"
        printf 'chatgpt_response_bytes=%s\n' "${DIAG_CHATGPT_BYTES:-unavailable}"
        printf 'chatgpt_remote_ip=%s\n' "${DIAG_CHATGPT_REMOTE_IP:-unavailable}"
        printf 'chatgpt_minimum_bytes=%s\n' "$DIAG_CHATGPT_MIN_BYTES"
        printf 'import_ms=%s\n' "${DIAG_IMPORT_MS:-unavailable}"
        printf 'interface_wait_ms=%s\n' "${DIAG_INTERFACE_WAIT_MS:-unavailable}"
        printf 'traffic_probe_ms=%s\n' "${DIAG_TRAFFIC_MS:-unavailable}"
        printf 'rx_before=%s\n' "${DIAG_RX_BEFORE:-unavailable}"
        printf 'tx_before=%s\n' "${DIAG_TX_BEFORE:-unavailable}"
        printf 'rx_after=%s\n' "${DIAG_RX_AFTER:-unavailable}"
        printf 'tx_after=%s\n' "${DIAG_TX_AFTER:-unavailable}"
        printf 'host=%s\n' "$(hostname)"
        printf 'kernel=%s\n' "$(uname -r)"
    } >"$DIAG_DIR/metadata.txt"
}
