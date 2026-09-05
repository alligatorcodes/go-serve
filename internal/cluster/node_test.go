package cluster

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/go-serve/internal/config"
	"github.com/hashicorp/raft"
)

func TestStateMachineReplicatesAssignments(t *testing.T) {
	machine := &stateMachine{assignments: make(map[string]Assignment)}
	assignment := Assignment{ClientKey: "client-a", NodeID: "node-2", PeerURL: "http://node-2:8080", ExpiresAt: time.Now().Add(time.Hour)}
	payload := []byte(`{"type":"assign","assignment":{"client_key":"client-a","node_id":"node-2","peer_url":"http://node-2:8080","expires_at":"2030-01-01T00:00:00Z"}}`)
	machine.Apply(&raft.Log{Data: payload})
	stored, ok := machine.get(assignment.ClientKey)
	if !ok || stored.NodeID != assignment.NodeID || stored.PeerURL != assignment.PeerURL {
		t.Fatalf("stored assignment = %#v, ok=%v", stored, ok)
	}
}

func TestStateMachineSnapshotRestore(t *testing.T) {
	machine := &stateMachine{assignments: map[string]Assignment{
		"client-a": {ClientKey: "client-a", NodeID: "node-1", ExpiresAt: time.Now().Add(time.Hour)},
	}}
	snapshot, err := machine.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	sink := &memorySink{}
	if err := snapshot.Persist(sink); err != nil {
		t.Fatal(err)
	}
	restored := &stateMachine{}
	if err := restored.Restore(io.NopCloser(strings.NewReader(string(sink.data)))); err != nil {
		t.Fatal(err)
	}
	if _, ok := restored.get("client-a"); !ok {
		t.Fatal("assignment was not restored")
	}
}

func TestAssignmentForIsDeterministic(t *testing.T) {
	node := &Node{config: config.ClusterConfig{
		NodeID:        "node-1",
		AssignmentTTL: time.Minute,
		Peers:         []config.ClusterPeer{{ID: "node-2", PublicURL: "http://node-2:8080"}},
	}}
	first := node.assignmentFor("client-a")
	second := node.assignmentFor("client-a")
	if first.NodeID != second.NodeID || first.PeerURL != second.PeerURL {
		t.Fatalf("assignment changed: first=%#v second=%#v", first, second)
	}
}

func TestHandlerFallsThroughToControlPlane(t *testing.T) {
	node := &Node{}
	next := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusNoContent) })
	response := httptest.NewRecorder()
	node.Handler(next).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("fallback status = %d", response.Code)
	}
}

type memorySink struct{ data []byte }

func (sink *memorySink) Write(value []byte) (int, error) {
	sink.data = append(sink.data, value...)
	return len(value), nil
}
func (sink *memorySink) Close() error  { return nil }
func (sink *memorySink) Cancel() error { return nil }
func (sink *memorySink) ID() string    { return "memory" }
