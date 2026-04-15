package master

import "testing"

func TestValidateServerConfigRequiresPublicForNonLoopback(t *testing.T) {
	err := validateServerConfig(serverConfig{listenAddr: "0.0.0.0:50051"})
	if err == nil {
		t.Fatal("expected non-loopback bind to require public mode")
	}

	err = validateServerConfig(serverConfig{listenAddr: "0.0.0.0:50051", publicMode: true})
	if err != nil {
		t.Fatalf("public non-loopback bind rejected: %v", err)
	}
}

func TestValidateServerConfigAllowsLoopbackDefault(t *testing.T) {
	if err := validateServerConfig(serverConfig{listenAddr: defaultListenAddress}); err != nil {
		t.Fatalf("loopback default rejected: %v", err)
	}
}
