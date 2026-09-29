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

echo "passed: $pass, failed: $fail"
[ $fail -eq 0 ]
