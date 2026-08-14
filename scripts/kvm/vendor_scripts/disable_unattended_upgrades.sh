#!/bin/bash
# TIC-1067 / PET-1835: disable unattended-upgrades on Ubuntu/Debian images

if [ -f /etc/os-release ]; then
    . /etc/os-release
    OS=$ID
else
    OS=$(uname -s)
fi

echo "Detected OS: $OS"

case "$OS" in
    ubuntu|debian)
        systemctl stop    unattended-upgrades 2>/dev/null || true
        systemctl disable unattended-upgrades 2>/dev/null || true
        systemctl mask    unattended-upgrades 2>/dev/null || true

        # Also silence the daily apt timers that can wake the service even when
        # masked, so there are no spurious activation attempts in the journal.
        for unit in apt-daily.timer apt-daily-upgrade.timer; do
            systemctl stop    "$unit" 2>/dev/null || true
            systemctl disable "$unit" 2>/dev/null || true
            systemctl mask    "$unit" 2>/dev/null || true
        done

        # Belt-and-suspenders: disable at the apt layer too.  systemctl mask
        # stops systemd from starting the service, but a direct invocation of
        # `unattended-upgrade` or an apt hook can still run it.  Setting the
        # periodic knobs to 0 prevents that path as well.
        conf=/etc/apt/apt.conf.d/20auto-upgrades
        if [ -f "$conf" ]; then
            sed -i 's/APT::Periodic::Update-Package-Lists "[0-9]*/APT::Periodic::Update-Package-Lists "0/' "$conf"
            sed -i 's/APT::Periodic::Unattended-Upgrade "[0-9]*/APT::Periodic::Unattended-Upgrade "0/' "$conf"
        else
            cat <<'EOF' > "$conf"
APT::Periodic::Update-Package-Lists "0";
APT::Periodic::Unattended-Upgrade "0";
EOF
        fi

        echo "unattended-upgrades disabled."
        ;;
    *)
        echo "Not Ubuntu/Debian -- nothing to do."
        ;;
esac
