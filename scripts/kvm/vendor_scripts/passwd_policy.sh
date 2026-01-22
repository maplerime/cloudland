#!/bin/bash


# 强制下次登录必须改密
passwd -e root

# 设置 90 天有效期策略
# -M 90 (最大天数), -W 7 (过期前7天警告)
chage -M 90 -W 7 root

# --- 2. 安装 pwquality 并在 PAM 中启用 ---
case "$OS" in
    ubuntu|debian)
        export DEBIAN_FRONTEND=noninteractive
        apt-get update
        apt-get install -y libpam-pwquality
        # Debian/Ubuntu 通常在安装后会自动配置好 PAM
        ;;
    centos|rhel|almalinux|rocky)
        yum install -y pam_pwquality
        # RHEL 8+ 建议使用 authselect 确保 PAM 模块开启
        if command -v authselect > /dev/null; then
            authselect enable-feature with-pwquality
            authselect apply-changes
        fi
        ;;
    *)
        echo "Unsupported OS for automatic pwquality setup"
        ;;
esac

# --- 3. 统一设置复杂度策略 ---
# 这里的规则：最小12位，包含3类字符，重试3次
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
