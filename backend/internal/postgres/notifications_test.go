// Checks notification-listener configuration rejects missing dependencies and invalid
// channels.

package postgres

import (
	"context"
	"testing"
)

func TestListenForNotificationsRejectsIncompleteConfiguration(t *testing.T) {
	for _, test := range []struct {
		name    string
		channel string
		notify  func(string)
	}{
		{name: "missing all"},
		{name: "missing callback", channel: "channel"},
		{name: "missing pool", channel: "channel", notify: func(string) {}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ListenForNotifications(context.Background(), nil, test.channel, test.notify); err == nil {
				t.Fatal("invalid notification configuration accepted")
			}
		})
	}
}
