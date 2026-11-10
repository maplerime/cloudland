package rpcs

import (
	"context"
	"fmt"
	"strconv"

	"web/src/routes"
)

func init() {
	Add("observe_connection", ObserveConnection)
}

func ObserveConnection(ctx context.Context, args []string) (string, error) {
	return observeConnection(ctx, args)
}

func observeConnection(ctx context.Context, args []string) (string, error) {
	// |:-COMMAND-:| observe_connection.sh '<ID>' '<ip1>' '<ip2>' ...
	if len(args) < 3 {
		return "", fmt.Errorf("wrong params: expected node ID and destination IPs")
	}
	id, err := strconv.ParseInt(args[1], 10, 32)
	if err != nil || id < 0 {
		return "", fmt.Errorf("invalid node ID: %q", args[1])
	}
	if err := routes.RecordObservedSuspiciousIPs(ctx, int32(id), args[2:]); err != nil {
		return "", err
	}
	return "recorded observed IPs", nil
}
