#!/bin/bash

passwd -e root

chage -M 90 -W 7 root

if [ -f /etc/os-release ]; then
    . /etc/os-release
    OS=$ID
else
    OS=$(uname -s)
fi

echo "Detected OS: $OS"

case "$OS" in
    ubuntu|debian)
        export DEBIAN_FRONTEND=noninteractive
        apt-get update
        apt-get install -y libpam-pwquality
        ;;
    centos|rhel|almalinux|rocky)
        yum install -y pam_pwquality
        if command -v authselect > /dev/null; then
            authselect enable-feature with-pwquality
            authselect apply-changes
        fi
        ;;
    *)
        echo "Unsupported OS for automatic pwquality setup"
        ;;
esac

cat <<EOF > /etc/security/pwquality.conf
minlen = 12
minclass = 3
retry = 3
dcredit = -1
ucredit = -1
lcredit = -1
ocredit = -1
EOF

echo "Set password policy done."
