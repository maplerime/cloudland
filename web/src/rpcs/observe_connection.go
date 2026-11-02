package rpcs

import (
	"context"
	"fmt"
	"net"
	"strconv"

	. "web/src/common"
)

func init() {
	Add("observe_connection", ObserveConnection)
}

func ObserveConnection(ctx context.Context, args []string) (string, error) {
	return observeConnection(ctx, args, HyperExecute)
}

func observeConnection(ctx context.Context, args []string, execute func(context.Context, string, string) error) (string, error) {
	// |:-COMMAND-:| observe_connection.sh '<ID>' '<ip1>' '<ip2>' ...
	if len(args) < 3 {
		return "", fmt.Errorf("wrong params: expected node ID and destination IPs")
	}
	id, err := strconv.ParseInt(args[1], 10, 32)
	if err != nil || id < 0 {
		return "", fmt.Errorf("invalid node ID: %q", args[1])
	}
	// Every argument after the node ID is an IP. Validate before dispatching.
	ips := []string{}
	seen := map[string]bool{}
	for _, value := range args[2:] {
		ip := net.ParseIP(value)
		if ip == nil {
			return "", fmt.Errorf("invalid destination IP: %q", value)
		}
		address := ip.String()
		if !seen[address] {
			seen[address] = true
			ips = append(ips, address)
		}
	}
	var firstErr error
	for _, ip := range ips {
		command := fmt.Sprintf("/opt/cloudland/scripts/kvm/operation/block_ip.sh '%s'", ip)
		if err := execute(ctx, "toall=", command); err != nil {
			logger.Errorf("Node %d: failed to broadcast block for observed IP %s: %v", id, ip, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if firstErr != nil {
		return "", firstErr
	}
	return fmt.Sprintf("submitted %d observed IPs for blocking", len(ips)), nil
}
