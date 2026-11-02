package rpcs

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestObserveConnectionReport(t *testing.T) {
	if Get("observe_connection") == nil {
		t.Fatal("RPC not registered")
	}
	cmd, args := DecodeCommand("observe_connection.sh '3' '192.0.2.10' '198.51.100.20' '192.0.2.10'")
	if cmd != "observe_connection" || !reflect.DeepEqual(args, []string{"observe_connection.sh", "3", "192.0.2.10", "198.51.100.20", "192.0.2.10"}) {
		t.Fatalf("wrong report decoding: %s %q", cmd, args)
	}
	var commands []string
	status, err := observeConnection(context.Background(), args, func(_ context.Context, control, command string) error {
		if control != "toall=" {
			t.Fatalf("wrong control: %s", control)
		}
		commands = append(commands, command)
		return nil
	})
	want := []string{"/opt/cloudland/scripts/kvm/operation/block_ip.sh '192.0.2.10'", "/opt/cloudland/scripts/kvm/operation/block_ip.sh '198.51.100.20'"}
	if err != nil || status == "" || !reflect.DeepEqual(commands, want) {
		t.Fatalf("dispatch: %q %v %q", status, err, commands)
	}
}

func TestObserveConnectionRejectInvalidReport(t *testing.T) {
	for _, args := range [][]string{nil, {"observe_connection.sh", "3"}, {"observe_connection.sh", "bad", "192.0.2.1"}, {"observe_connection.sh", "-1", "192.0.2.1"}, {"observe_connection.sh", "3", "192.0.2.1", "1.2.3.4';touch /tmp/injected"}, {"observe_connection.sh", "3", ""}} {
		_, err := observeConnection(context.Background(), args, func(context.Context, string, string) error {
			t.Fatal("invalid report issued a command")
			return nil
		})
		if err == nil {
			t.Fatalf("accepted %q", args)
		}
	}
}

func TestObserveConnectionContinuesOnDispatchFailure(t *testing.T) {
	calls := 0
	failure := errors.New("SCI unavailable")
	_, err := observeConnection(context.Background(), []string{"observe_connection.sh", "3", "192.0.2.1", "192.0.2.2"}, func(context.Context, string, string) error {
		calls++
		if calls == 1 {
			return failure
		}
		return nil
	})
	if calls != 2 || !errors.Is(err, failure) {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
