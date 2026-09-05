package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/alligatorcodes/go-serve/internal/config"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
)

type Assignment struct {
	ClientKey string    `json:"client_key"`
	NodeID    string    `json:"node_id"`
	PeerURL   string    `json:"peer_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Node struct {
	raft       *raft.Raft
	fsm        *stateMachine
	config     config.ClusterConfig
	transport  raft.Transport
	vip        VIPHook
	stop       context.CancelFunc
	leadership <-chan bool
}

type VIPHook interface {
	OnLeader(context.Context) error
	OnFollower(context.Context) error
}

func New(ctx context.Context, cfg config.ClusterConfig) (*Node, error) {
	if !cfg.Enabled {
		return nil, errors.New("cluster is disabled")
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create cluster data directory: %w", err)
	}
	logger := hclog.New(&hclog.LoggerOptions{Name: "go-serve-raft", Output: io.Discard})
	logs, err := raftboltdb.NewBoltStore(filepath.Join(cfg.DataDir, "raft.db"))
	if err != nil {
		return nil, fmt.Errorf("open raft store: %w", err)
	}
	snapshots, err := raft.NewFileSnapshotStoreWithLogger(filepath.Join(cfg.DataDir, "snapshots"), 2, logger)
	if err != nil {
		return nil, fmt.Errorf("open raft snapshots: %w", err)
	}
	advertise, err := net.ResolveTCPAddr("tcp", cfg.AdvertiseAddr)
	if err != nil {
		return nil, fmt.Errorf("resolve raft advertise address: %w", err)
	}
	transport, err := raft.NewTCPTransport(cfg.BindAddr, advertise, 3, 10*time.Second, io.Discard)
	if err != nil {
		return nil, fmt.Errorf("open raft transport: %w", err)
	}
	fsm := &stateMachine{assignments: make(map[string]Assignment)}
	raftConfig := raft.DefaultConfig()
	raftConfig.LocalID = raft.ServerID(cfg.NodeID)
	raftConfig.Logger = logger
	raftNode, err := raft.NewRaft(raftConfig, fsm, logs, logs, snapshots, transport)
	if err != nil {
		return nil, fmt.Errorf("create raft node: %w", err)
	}
	node := &Node{raft: raftNode, fsm: fsm, config: cfg, transport: transport, vip: commandVIPHook{command: cfg.OnLeaderCommand, fallback: cfg.OnFollowerCommand, vip: cfg.VirtualIP, iface: cfg.Interface}}
	workerContext, stop := context.WithCancel(ctx)
	node.stop = stop
	node.leadership = raftNode.LeaderCh()
	if cfg.Bootstrap {
		servers := []raft.Server{{ID: raft.ServerID(cfg.NodeID), Address: raft.ServerAddress(cfg.AdvertiseAddr), Suffrage: raft.Voter}}
		for _, peer := range cfg.Peers {
			servers = append(servers, raft.Server{ID: raft.ServerID(peer.ID), Address: raft.ServerAddress(peer.Address), Suffrage: raft.Voter})
		}
		if err := raftNode.BootstrapCluster(raft.Configuration{Servers: servers}).Error(); err != nil && err != raft.ErrCantBootstrap {
			return nil, fmt.Errorf("bootstrap raft cluster: %w", err)
		}
	}
	go node.leadershipLoop(workerContext)
	return node, nil
}

func (n *Node) Close() error {
	if n.stop != nil {
		n.stop()
	}
	if n.raft != nil {
		return n.raft.Shutdown().Error()
	}
	return nil
}

func (n *Node) IsLeader() bool          { return n.raft.State() == raft.Leader }
func (n *Node) LeaderAddress() string   { return string(n.raft.Leader()) }
func (n *Node) Leadership() <-chan bool { return n.leadership }

func (n *Node) Join(ctx context.Context, peer config.ClusterPeer) error {
	future := n.raft.AddVoter(raft.ServerID(peer.ID), raft.ServerAddress(peer.Address), 0, 10*time.Second)
	return future.Error()
}

func (n *Node) Assign(ctx context.Context, assignment Assignment) error {
	if !n.IsLeader() {
		return fmt.Errorf("cluster leader is %q", n.LeaderAddress())
	}
	if assignment.ExpiresAt.IsZero() {
		assignment.ExpiresAt = time.Now().Add(n.config.AssignmentTTL)
	}
	command, err := json.Marshal(command{Type: "assign", Assignment: assignment})
	if err != nil {
		return err
	}
	return n.raft.Apply(command, n.config.ForwardTimeout).Error()
}

func (n *Node) Assignment(clientKey string) (Assignment, bool) {
	assignment, ok := n.fsm.get(clientKey)
	if !ok || assignment.ExpiresAt.Before(time.Now()) {
		return Assignment{}, false
	}
	return assignment, true
}

func (n *Node) Proxy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Go-Serve-Forwarded") == "1" || !n.IsLeader() && n.LeaderAddress() == "" {
			next.ServeHTTP(w, r)
			return
		}
		key := clientKey(r)
		assignment, ok := n.Assignment(key)
		if !ok && n.IsLeader() {
			assignment = n.assignmentFor(key)
			if err := n.Assign(context.Background(), assignment); err != nil {
				next.ServeHTTP(w, r)
				return
			}
		}
		if assignment.NodeID == "" || assignment.NodeID == n.config.NodeID || assignment.PeerURL == "" {
			next.ServeHTTP(w, r)
			return
		}
		if err := n.forward(w, r, assignment.PeerURL); err != nil {
			http.Error(w, "cluster peer unavailable", http.StatusBadGateway)
		}
	})
}

func (n *Node) assignmentFor(key string) Assignment {
	members := []struct{ id, publicURL string }{{n.config.NodeID, ""}}
	for _, peer := range n.config.Peers {
		members = append(members, struct{ id, publicURL string }{peer.ID, peer.PublicURL})
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(key))
	member := members[hash.Sum32()%uint32(len(members))]
	return Assignment{ClientKey: key, NodeID: member.id, PeerURL: member.publicURL, ExpiresAt: time.Now().Add(n.config.AssignmentTTL)}
}

func clientKey(r *http.Request) string {
	if value := r.Header.Get("X-Go-Serve-Client-Key"); value != "" {
		return value
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func (n *Node) forward(w http.ResponseWriter, r *http.Request, peer string) error {
	base, err := url.Parse(peer)
	if err != nil {
		return err
	}
	target := *r.URL
	target.Scheme, target.Host = base.Scheme, base.Host
	request := r.Clone(r.Context())
	request.URL = &target
	request.Header.Set("X-Go-Serve-Forwarded", "1")
	response, err := (&http.Client{Timeout: n.config.ForwardTimeout}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	for key, values := range response.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	_, err = io.Copy(w, response.Body)
	return err
}

func (n *Node) Handler(next http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/cluster/", n.clusterHandler(next))
	mux.Handle("/", next)
	return mux
}

func (n *Node) clusterHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cluster/health" {
			writeJSON(w, http.StatusOK, map[string]any{"node_id": n.config.NodeID, "leader": n.IsLeader(), "leader_address": n.LeaderAddress()})
			return
		}
		if r.URL.Path == "/cluster/assignment" {
			key := r.URL.Query().Get("client_key")
			assignment, ok := n.Assignment(key)
			if !ok || assignment.ExpiresAt.Before(time.Now()) {
				http.NotFound(w, r)
				return
			}
			writeJSON(w, http.StatusOK, assignment)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (n *Node) leadershipLoop(ctx context.Context) {
	previous := false
	for {
		select {
		case <-ctx.Done():
			return
		case leader := <-n.leadership:
			if leader == previous {
				continue
			}
			previous = leader
			if n.vip == nil {
				continue
			}
			if leader {
				_ = n.vip.OnLeader(ctx)
			} else {
				_ = n.vip.OnFollower(ctx)
			}
		}
	}
}

type command struct {
	Type       string     `json:"type"`
	Assignment Assignment `json:"assignment"`
}
type stateMachine struct {
	mu          sync.RWMutex
	assignments map[string]Assignment
}

func (s *stateMachine) Apply(log *raft.Log) interface{} {
	var command command
	if json.Unmarshal(log.Data, &command) != nil || command.Type != "assign" {
		return nil
	}
	s.mu.Lock()
	s.assignments[command.Assignment.ClientKey] = command.Assignment
	s.mu.Unlock()
	return nil
}
func (s *stateMachine) Snapshot() (raft.FSMSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copy := make(map[string]Assignment, len(s.assignments))
	for key, value := range s.assignments {
		copy[key] = value
	}
	return snapshot{assignments: copy}, nil
}
func (s *stateMachine) Restore(reader io.ReadCloser) error {
	defer reader.Close()
	var assignments map[string]Assignment
	if err := json.NewDecoder(reader).Decode(&assignments); err != nil {
		return err
	}
	s.mu.Lock()
	s.assignments = assignments
	s.mu.Unlock()
	return nil
}
func (s *stateMachine) get(key string) (Assignment, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.assignments[key]
	return value, ok
}

type snapshot struct{ assignments map[string]Assignment }

func (s snapshot) Persist(sink raft.SnapshotSink) error {
	if err := json.NewEncoder(sink).Encode(s.assignments); err != nil {
		_ = sink.Cancel()
		return err
	}
	return sink.Close()
}
func (s snapshot) Release() {}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type commandVIPHook struct{ command, fallback, vip, iface string }

func (h commandVIPHook) OnLeader(ctx context.Context) error   { return h.run(ctx, h.command) }
func (h commandVIPHook) OnFollower(ctx context.Context) error { return h.run(ctx, h.fallback) }
func (h commandVIPHook) run(ctx context.Context, command string) error {
	if command == "" {
		return nil
	}
	return exec.CommandContext(ctx, "/bin/sh", "-c", command, "go-serve-vip", h.vip, h.iface).Run()
}

var _ = filepath.Separator
