package store

import "testing"

func TestCurrentWeiboUserIDFromChannel(t *testing.T) {
	tests := []struct {
		name    string
		channel string
		want    string
	}{
		{name: "private IM channel", channel: "/im/7608757850", want: "7608757850"},
		{name: "surrounding whitespace", channel: "  /im/1401527553\n", want: "1401527553"},
		{name: "missing prefix", channel: "7608757850", want: ""},
		{name: "non numeric uid", channel: "/im/user-1", want: ""},
		{name: "empty uid", channel: "/im/", want: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := currentWeiboUserIDFromChannel(test.channel); got != test.want {
				t.Fatalf("currentWeiboUserIDFromChannel(%q) = %q, want %q", test.channel, got, test.want)
			}
		})
	}
}
