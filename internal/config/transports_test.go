package config_test

import (
	"testing"

	"clipp-relay/internal/config"
)

func validTransportConfig() config.Config {
	var c config.Config
	c.Version = 1
	c.PortalOrigin = "https://portal.example.test"
	c.WSSHostname = "relay.example.test"
	c.PublicClients.AndroidRedirect = "clipp-relay://oauth/callback"
	c.PublicClients.ExtensionRedirect = "https://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.chromiumapp.org/clipp-relay"
	c.Listeners.Public = "127.0.0.1:18080"
	c.Listeners.Private = "127.0.0.1:18081"
	c.Database.Mode = "external"
	c.Database.Host = "db.example.test"
	c.Database.Port = 5432
	c.Database.Name = "relay"
	c.Database.UsernameFile = "/x"
	c.Database.PasswordFile = "/y"
	c.Database.CAFile = "/z"
	c.Secrets.GoogleClientIDFile = "/a"
	c.Secrets.GoogleClientSecretFile = "/b"
	c.Secrets.AdminAllowlistFile = "/c"
	c.Secrets.PepperKeyringFile = "/d"
	return c
}

func TestEnabledTransportRequiresCompleteExactPublicAddress(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*config.Config)
		valid  bool
	}{
		{"wss exact TLS host", func(c *config.Config) {
			c.RelayWebSocket.Listen = "/ip4/127.0.0.1/tcp/18082/ws"
			c.RelayWebSocket.PublicAddresses = []string{"/dns4/relay.example.test/tcp/443/tls/ws"}
		}, true},
		{"wrong WSS host", func(c *config.Config) {
			c.RelayWebSocket.Listen = "/ip4/127.0.0.1/tcp/18082/ws"
			c.RelayWebSocket.PublicAddresses = []string{"/dns4/other.example.test/tcp/443/tls/ws"}
		}, false},
		{"plain WS publication", func(c *config.Config) {
			c.RelayWebSocket.Listen = "/ip4/127.0.0.1/tcp/18082/ws"
			c.RelayWebSocket.PublicAddresses = []string{"/dns4/relay.example.test/tcp/443/ws"}
		}, false},
		{"missing WSS address", func(c *config.Config) { c.RelayWebSocket.Listen = "/ip4/127.0.0.1/tcp/18082/ws" }, false},
		{"webrtc direct", func(c *config.Config) {
			c.RelayWebRTC.Listen = "/ip4/127.0.0.1/udp/18083/webrtc-direct"
			c.RelayWebRTC.PublicAddresses = []string{"/dns4/relay.example.test/udp/18083/webrtc-direct"}
		}, true},
		{"webrtc public UDP 443", func(c *config.Config) {
			c.RelayWebRTC.Listen = "/ip4/127.0.0.1/udp/18083/webrtc-direct"
			c.RelayWebRTC.PublicAddresses = []string{"/dns4/relay.example.test/udp/443/webrtc-direct"}
		}, true},
		{"webrtc caller certhash", func(c *config.Config) {
			c.RelayWebRTC.Listen = "/ip4/127.0.0.1/udp/18083/webrtc-direct"
			c.RelayWebRTC.PublicAddresses = []string{"/dns4/relay.example.test/udp/18083/webrtc-direct/certhash/uEiAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}
		}, false},
		{"listener collision", func(c *config.Config) {
			c.RelayWebSocket.Listen = "/ip4/127.0.0.1/tcp/18080/ws"
			c.RelayWebSocket.PublicAddresses = []string{"/dns4/relay.example.test/tcp/443/tls/ws"}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := validTransportConfig()
			tc.change(&c)
			if got := c.Validate() == nil; got != tc.valid {
				t.Fatalf("valid = %v, want %v", got, tc.valid)
			}
		})
	}
}
