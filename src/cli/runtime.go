package cli

import (
	"context"

	"github.com/anthropics/agent/agent"
	"github.com/anthropics/agent/api"
	"github.com/anthropics/agent/tools"
)

// RuntimeKind distinguishes local from remote for UI styling.
type RuntimeKind int

const (
	RuntimeLocal  RuntimeKind = iota
	RuntimeRemote             // remote runtimes render tabs in blue
)

// RuntimeStatus represents the current state of a runtime.
type RuntimeStatus int

const (
	StatusReady    RuntimeStatus = iota
	StatusSleeping
	StatusWaking
	StatusError
)

// Runtime abstracts where agent execution happens — in-process, in a container,
// or on a remote VM. The CLI/TUI talks to this interface without knowing which.
type Runtime interface {
	// Kind returns whether this runtime is local or remote.
	Kind() RuntimeKind

	// ID returns a stable identifier for this runtime (e.g. "" for local,
	// "sandbox-abc123" for remote). Persisted in SessionData.RuntimeID.
	ID() string

	// NewSession creates a new agent session on this runtime.
	NewSession(provider api.Provider, cwd string, opts ...agent.Option) (AgentSession, error)

	// RestoreSession restores a session from persisted state on this runtime.
	// Used for both resume-from-disk and migrate-from-other-runtime.
	RestoreSession(provider api.Provider, cwd string, data *SessionData, opts ...agent.Option) (AgentSession, error)

	// Wake brings a sleeping remote runtime back up. No-op for local.
	Wake(ctx context.Context) error

	// Sleep puts a remote runtime to sleep. No-op for local.
	Sleep(ctx context.Context) error

	// Status returns the current runtime state.
	Status() RuntimeStatus

	// SyncTo prepares the runtime's working directory from local git state.
	// Clones repo + checks out branch/commit. No-op for in-process and Docker volume mounts.
	SyncTo(ctx context.Context, repoURL, branch, commit string) error

	// SyncFrom pulls remote working directory changes back to local via git.
	// Remote commits+pushes, returns the branch name. No-op for in-process/volume mounts.
	SyncFrom(ctx context.Context) (branch string, err error)
}

// AgentSession abstracts the per-session agent operations that the CLI/TUI needs.
// For in-process execution this wraps *agent.Agent directly. Remote runtimes may
// proxy calls over SSH, HTTP, or other transports.
type AgentSession interface {
	SendCtx(ctx context.Context, prompt string) error
	Messages() []api.Message
	SetMessages([]api.Message)
	Reset()
	GetPermissionMode() agent.PermissionMode
	SetPermissionMode(agent.PermissionMode)
	SetProvider(api.Provider)
	GetProvider() api.Provider
	AllowedTools() map[string]bool
	SetAllowedTools(map[string]bool)
	AppendHistory(userText, assistantText string)
	Registry() *tools.Registry
	LastResponse() string
	PlanReady() bool
}

// ─── InProcessRuntime (current behavior, no sandbox) ─────────────────────────

// InProcessRuntime runs the agent in the same process as the CLI. This is the
// default runtime that preserves the existing behavior.
type InProcessRuntime struct{}

func (r *InProcessRuntime) Kind() RuntimeKind   { return RuntimeLocal }
func (r *InProcessRuntime) ID() string           { return "" }
func (r *InProcessRuntime) Status() RuntimeStatus { return StatusReady }

func (r *InProcessRuntime) Wake(_ context.Context) error  { return nil }
func (r *InProcessRuntime) Sleep(_ context.Context) error { return nil }

func (r *InProcessRuntime) SyncTo(_ context.Context, _, _, _ string) error { return nil }
func (r *InProcessRuntime) SyncFrom(_ context.Context) (string, error)     { return "", nil }

func (r *InProcessRuntime) NewSession(provider api.Provider, cwd string, opts ...agent.Option) (AgentSession, error) {
	ag := agent.New(provider, cwd, opts...)
	return &inProcessAgentSession{agent: ag}, nil
}

func (r *InProcessRuntime) RestoreSession(provider api.Provider, cwd string, data *SessionData, opts ...agent.Option) (AgentSession, error) {
	ag := agent.New(provider, cwd, opts...)

	// Restore conversation state
	ag.SetMessages(data.Messages)
	ag.SetPermissionMode(parseModeString(data.PermissionMode))

	// Restore allowed tools
	at := make(map[string]bool)
	for _, name := range data.AllowedTools {
		at[name] = true
	}
	ag.SetAllowedTools(at)

	return &inProcessAgentSession{agent: ag}, nil
}

// ─── inProcessAgentSession wraps *agent.Agent to satisfy AgentSession ────────

type inProcessAgentSession struct {
	agent *agent.Agent
}

func (s *inProcessAgentSession) SendCtx(ctx context.Context, prompt string) error {
	return s.agent.SendCtx(ctx, prompt)
}

func (s *inProcessAgentSession) Messages() []api.Message      { return s.agent.Messages() }
func (s *inProcessAgentSession) SetMessages(m []api.Message)   { s.agent.SetMessages(m) }
func (s *inProcessAgentSession) Reset()                        { s.agent.Reset() }
func (s *inProcessAgentSession) GetPermissionMode() agent.PermissionMode {
	return s.agent.GetPermissionMode()
}
func (s *inProcessAgentSession) SetPermissionMode(m agent.PermissionMode) {
	s.agent.SetPermissionMode(m)
}
func (s *inProcessAgentSession) SetProvider(p api.Provider)    { s.agent.SetProvider(p) }
func (s *inProcessAgentSession) GetProvider() api.Provider     { return s.agent.GetProvider() }
func (s *inProcessAgentSession) AllowedTools() map[string]bool { return s.agent.AllowedTools() }
func (s *inProcessAgentSession) SetAllowedTools(m map[string]bool) { s.agent.SetAllowedTools(m) }
func (s *inProcessAgentSession) AppendHistory(u, a string)     { s.agent.AppendHistory(u, a) }
func (s *inProcessAgentSession) Registry() *tools.Registry     { return s.agent.Registry() }
func (s *inProcessAgentSession) LastResponse() string          { return s.agent.LastResponse() }
func (s *inProcessAgentSession) PlanReady() bool               { return s.agent.PlanReady() }
