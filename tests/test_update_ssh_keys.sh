#!/bin/bash
# Local tests for PET-1981 ssh key injection scripts. Run: bash tests/test_update_ssh_keys.sh
cd "$(dirname "$0")/.."
ROOT=$(pwd)
pass=0; fail=0
check() { # $1=name $2=expected $3=actual
    if [ "$2" == "$3" ]; then pass=$((pass+1)); else fail=$((fail+1)); echo "FAIL: $1"; echo "  want: $2"; echo "  got:  $3"; fi
}

# ---------- guest_ssh_keys.sh ----------
T=$(mktemp -d)
ME=$(id -un)
mkdir -p $T/bin $T/home
cat > $T/bin/getent <<EOF
#!/bin/sh
[ "\$2" = "$ME" ] && echo "$ME:x:0:0::$T/home:/bin/sh"
exit 0
EOF
chmod +x $T/bin/getent
G="env PATH=$T/bin:$PATH sh $ROOT/scripts/kvm/guest_ssh_keys.sh"
AK=$T/home/.ssh/authorized_keys
perm() { stat -f %Lp "$1" 2>/dev/null || stat -c %a "$1"; }

printf 'k1 a\nk2 b\n' | $G add $ME
check "add creates file" "$(printf 'k1 a\nk2 b')" "$(cat $AK)"
check "ssh dir perm" "700" "$(perm $T/home/.ssh)"
check "authorized_keys perm" "600" "$(perm $AK)"

echo 'manual m' >> $AK
printf 'k2 b\nk3 c\n' | $G add $ME
check "add dedupes" "$(printf 'k1 a\nk2 b\nmanual m\nk3 c')" "$(cat $AK)"

printf 'k1 a\nnot-there x\n' | $G remove $ME
check "remove keeps others" "$(printf 'k2 b\nmanual m\nk3 c')" "$(cat $AK)"

printf 'k2 b\nmanual m\nk3 c\n' | $G remove $ME
check "remove to empty rc" "0" "$?"
check "remove to empty" "" "$(cat $AK)"

printf 'r1 z\n' | $G reset $ME
check "reset overwrites" "r1 z" "$(cat $AK)"

printf '' | $G reset $ME
check "reset empty clears" "" "$(cat $AK)"

err=$(printf 'k1 a\n' | $G add nosuchuser 2>&1 >/dev/null); rc=$?
check "unknown user rc" "1" "$rc"
check "unknown user msg" "user nosuchuser not found" "$err"
rm -rf $T

# ---------- update_ssh_keys.sh (fake virsh) ----------
# Fake virsh behaviour is driven by env: FAKE_STATE, FAKE_OS, FAKE_PING_RC,
# FAKE_SSHKEYS_RC, FAKE_SSHKEYS_OUT, FAKE_EXEC_RC, FAKE_EXIT_CODE. Every call is logged to $T/virsh.log.
T=$(mktemp -d)
mkdir -p $T/kvm $T/bin
cp $ROOT/scripts/kvm/update_ssh_keys.sh $ROOT/scripts/kvm/guest_ssh_keys.sh $T/kvm/
cat > $T/cloudrc <<'EOF'
function die() { echo $1; exit -1; }
function log_debug() { :; }
EOF
cat > $T/bin/timeout <<'EOF'
#!/bin/bash
shift; exec "$@"
EOF
cat > $T/bin/virsh <<'EOF'
#!/bin/bash
echo "$*" >> $FAKE_LOG
case "$1" in
domstate) echo "${FAKE_STATE:-running}" ;;
set-user-sshkeys)
    [ -n "$FAKE_SSHKEYS_OUT" ] && echo "$FAKE_SSHKEYS_OUT" >&2
    exit ${FAKE_SSHKEYS_RC:-0} ;;
qemu-agent-command)
    req="${@: -1}"
    case "$req" in
    *guest-ping*) exit ${FAKE_PING_RC:-0} ;;
    *guest-get-osinfo*) echo '{"return":{"id":"'${FAKE_OS:-ubuntu}'"}}' ;;
    *guest-exec-status*) echo '{"return":{"exited":true,"exitcode":'${FAKE_EXIT_CODE:-0}',"err-data":"'$(printf 'boom' | base64)'"}}' ;;
    *guest-exec*) [ "${FAKE_EXEC_RC:-0}" -ne 0 ] && { echo "error: command disabled" >&2; exit 1; }; echo '{"return":{"pid":42}}' ;;
    esac ;;
esac
EOF
chmod +x $T/bin/timeout $T/bin/virsh
KEYS_B64=$(printf 'ssh-ed25519 AAAA a@b' | base64)
run_hyper() { # $1=action, rest = env assignments
    local action=$1; shift
    : > $T/virsh.log
    echo "$KEYS_B64" | env PATH=$T/bin:$PATH FAKE_LOG=$T/virsh.log "$@" bash $T/kvm/update_ssh_keys.sh 7 9 $action ubuntu | grep '|:-COMMAND-:|'
}
CB="|:-COMMAND-:| update_ssh_keys.sh '7'"

check "not running" "$CB 'failed' 'instance is not running'" "$(run_hyper add FAKE_STATE='shut off')"
check "agent down" "$CB 'failed' 'guest agent not responding'" "$(run_hyper add FAKE_PING_RC=1)"
check "windows" "$CB 'failed' 'windows is not supported'" "$(run_hyper add FAKE_OS=mswindows)"
check "A add success" "$CB 'success' ''" "$(run_hyper add)"
check "A add flags" "1" "$(grep -c '^set-user-sshkeys inst-9 ubuntu --file ' $T/virsh.log)"
run_hyper remove >/dev/null
check "A remove flag" "1" "$(grep -c '^set-user-sshkeys inst-9 ubuntu --remove --file ' $T/virsh.log)"
: > $T/virsh.log
echo "" | env PATH=$T/bin:$PATH FAKE_LOG=$T/virsh.log bash $T/kvm/update_ssh_keys.sh 7 9 reset ubuntu >/dev/null
check "A reset-empty no --file" "set-user-sshkeys inst-9 ubuntu --reset" "$(grep '^set-user-sshkeys' $T/virsh.log | sed 's/ *$//')"
check "A-other-error quote stripped" "$CB 'failed' 'error: user dont exist'" \
    "$(run_hyper add FAKE_SSHKEYS_RC=1 FAKE_SSHKEYS_OUT="error: user don't exist")"
check "B fallback success" "$CB 'success' ''" \
    "$(run_hyper add FAKE_SSHKEYS_RC=1 FAKE_SSHKEYS_OUT='error: Command guest-ssh-add-authorized-keys has not been found')"
check "B used guest-exec" "1" "$(grep -c '"guest-exec","arguments":{"path":"/bin/sh"' $T/virsh.log)"
check "B exit nonzero" "$CB 'failed' 'boom'" \
    "$(run_hyper add FAKE_SSHKEYS_RC=1 FAKE_SSHKEYS_OUT='not supported' FAKE_EXIT_CODE=1)"
check "B exec disabled" "$CB 'failed' 'guest agent supports neither guest-ssh nor guest-exec'" \
    "$(run_hyper add FAKE_SSHKEYS_RC=1 FAKE_SSHKEYS_OUT='has been disabled' FAKE_EXEC_RC=1)"
rm -rf $T

echo "passed: $pass, failed: $fail"
[ $fail -eq 0 ]
