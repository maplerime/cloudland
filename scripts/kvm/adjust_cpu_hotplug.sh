#!/bin/bash

source /opt/cloudland/scripts/cloudrc

domain=$1
limit_percent=$2

if [ -z "$domain" ] || [ -z "$limit_percent" ]; then
    echo "Usage: $0 <domain_name> <limit_percent>"
    echo "  limit_percent: CPU limit percentage (0-100), or 'restore' to remove limits"
    exit 1
fi

# 检查虚拟机是否存在
if ! virsh dominfo "$domain" >/dev/null 2>&1; then
    echo "Domain $domain does not exist"
    exit 1
fi

# 如果是恢复操作，移除CPU限制
if [ "$limit_percent" = "restore" ]; then
    echo "Restoring CPU resources for $domain (removing limits)"

    # 获取CPU核数和period
    vcpu_count=$(virsh dominfo "$domain" | grep "CPU(s)" | awk '{print $2}')
    current_period=$(virsh schedinfo "$domain" | grep vcpu_period | awk '{print $3}')

    if [ -z "$current_period" ] || [ "$current_period" -eq 0 ]; then
        current_period=100000
    fi

    # 设置quota为核心数乘以period，相当于允许全速运行（100% × 核心数）
    # full_speed_quota=$((current_period * vcpu_count))

    # 恢复到真正的无限制状态（KVM无限制特殊值）
    full_speed_quota=17592186044415

    echo "Setting full speed quota: $full_speed_quota (${current_period} * ${vcpu_count} cores = 100% per core)"

    # 设置运行时配置
    virsh schedinfo "$domain" --set vcpu_quota=$full_speed_quota --live
    live_result=$?

    # 设置持久化配置
    virsh schedinfo "$domain" --set vcpu_quota=$full_speed_quota --config
    config_result=$?

    if [ $live_result -eq 0 ] && [ $config_result -eq 0 ]; then
        echo "Successfully restored CPU resources for $domain (full speed)"
        # 验证结果
        echo "Current configuration after restore:"
        virsh schedinfo "$domain" | grep -E 'vcpu_quota|vcpu_period'
        exit 0
    else
        echo "Failed to restore CPU resources for $domain"
        [ $live_result -ne 0 ] && echo "  - Live restore failed"
        [ $config_result -ne 0 ] && echo "  - Persistent restore failed"
        exit 1
    fi
fi

# 验证限制百分比范围
if [ "$limit_percent" -lt 1 ] || [ "$limit_percent" -gt 100 ]; then
    echo "Invalid limit percentage: $limit_percent (must be 1-100)"
    exit 1
fi

# 获取CPU核数
vcpu_count=$(virsh dominfo "$domain" | grep "CPU(s)" | awk '{print $2}')
if [ -z "$vcpu_count" ] || [ "$vcpu_count" -eq 0 ]; then
    echo "Error: Cannot get CPU count for domain $domain"
    exit 1
fi
echo "Domain $domain has $vcpu_count vCPU(s)"

# 获取当前的vcpu_period值
current_period=$(virsh schedinfo "$domain" | grep vcpu_period | awk '{print $3}')
if [ -z "$current_period" ] || [ "$current_period" -eq 0 ]; then
    # 如果获取不到或为0，使用默认值100000
    current_period=100000
    echo "Using default vcpu_period: $current_period"
else
    echo "Current vcpu_period: $current_period"
fi

# 计算quota值 (period * 核数 * 百分比)
# 对于多核CPU，总的配额应该是 period * 核数 * 百分比
quota_value=$((current_period * vcpu_count * limit_percent / 100))
echo "Calculated vcpu_quota: $quota_value (${limit_percent}% of ${current_period} * ${vcpu_count} cores)"

# 同时设置live和config配置
echo "Setting CPU limit for $domain to ${limit_percent}%..."

# 设置运行时配置（立即生效）
virsh schedinfo "$domain" --set vcpu_quota=$quota_value --live
live_result=$?

# 设置持久化配置（重启后生效）
virsh schedinfo "$domain" --set vcpu_quota=$quota_value --config
config_result=$?

# 检查执行结果
if [ $live_result -eq 0 ] && [ $config_result -eq 0 ]; then
    echo "Successfully set CPU limit for $domain to ${limit_percent}%"

    # 验证设置结果
    echo "Current live configuration:"
    virsh schedinfo "$domain" | grep vcpu_quota

    echo "Persistent configuration:"
    virsh dumpxml "$domain" --inactive | sed -n '/<cputune>/,/<\/cputune>/p'

    exit 0
else
    echo "Failed to set CPU limit for $domain"
    [ $live_result -ne 0 ] && echo "  - Live configuration failed"
    [ $config_result -ne 0 ] && echo "  - Persistent configuration failed"
    exit 1
fi
