package nexusbridge

import "testing"

func TestConnectionStateTracksCurrentConnection(t *testing.T) {
	state := &ConnectionState{}
	if state.Connected() {
		t.Fatal("new connection state should start disconnected")
	}

	state.SetConnected(true)
	if !state.Connected() {
		t.Fatal("connected state was not recorded")
	}

	state.SetConnected(false)
	if state.Connected() {
		t.Fatal("disconnected state was not recorded")
	}
}

func TestConnectionStateTracksServerEditionAndProtocol(t *testing.T) {
	state := &ConnectionState{}
	state.SetConnectedServer(ServerInfo{
		Edition:         EditionWorkers,
		Version:         "0.1.0-workers",
		ProtocolVersion: "2",
		ContractVersion: "0.9.0",
	})
	if !state.Connected() || !state.IsWorkers() {
		t.Fatalf("expected connected workers state, got connected=%v workers=%v", state.Connected(), state.IsWorkers())
	}
	if got := state.Server(); got.ProtocolVersion != "2" || got.ContractVersion != "0.9.0" || got.Version != "0.1.0-workers" {
		t.Fatalf("server info = %#v", got)
	}

	state.SetConnected(false)
	if state.IsWorkers() || state.Server() != (ServerInfo{}) {
		t.Fatalf("disconnect should clear server info, got %#v", state.Server())
	}
}
