package model

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Log index versions accepted by persisted run metadata.
const (
	LogIndexVersion          = 1
	LogIndexVersionSegmented = 2
)

// EntityKind identifies the snapshot changed by a state event.
type EntityKind string

// Entity kinds written by the initial model.
const (
	EntityJob        EntityKind = "job"
	EntityRun        EntityKind = "run"
	EntitySupervisor EntityKind = "supervisor"
)

// JobPhase is the operational lifecycle phase of a job.
type JobPhase string

// Job phases defined by the v1 specification.
const (
	JobPhaseSubmitting JobPhase = "submitting"
	JobPhaseWaiting    JobPhase = "waiting"
	JobPhaseQueued     JobPhase = "queued"
	JobPhaseStarting   JobPhase = "starting"
	JobPhaseRunning    JobPhase = "running"
	JobPhaseBackoff    JobPhase = "backoff"
	JobPhasePaused     JobPhase = "paused"
	JobPhaseStopping   JobPhase = "stopping"
	JobPhaseCompleted  JobPhase = "completed"
)

// RunPhase is the operational lifecycle phase of one target invocation.
type RunPhase string

// Run phases defined by the initial state model.
const (
	RunPhaseStarting  RunPhase = "starting"
	RunPhaseRunning   RunPhase = "running"
	RunPhasePaused    RunPhase = "paused"
	RunPhaseStopping  RunPhase = "stopping"
	RunPhaseCompleted RunPhase = "completed"
)

// JobOutcome is the terminal result of a job.
type JobOutcome string

// Job outcomes defined by the v1 specification.
const (
	// JobOutcomeNone is the nonterminal zero value and is never valid as a
	// persisted completed-job outcome.
	JobOutcomeNone             JobOutcome = ""
	JobOutcomeSuccess          JobOutcome = "success"
	JobOutcomeFailure          JobOutcome = "failure"
	JobOutcomeTimedOut         JobOutcome = "timed_out"
	JobOutcomeCancelled        JobOutcome = "cancelled" //nolint:misspell // The specification defines this persisted spelling.
	JobOutcomeAborted          JobOutcome = "aborted"
	JobOutcomeLost             JobOutcome = "lost"
	JobOutcomeSubmissionFailed JobOutcome = "submission_failed"
)

// RunOutcome is the terminal result of one target invocation.
type RunOutcome string

// Run outcomes defined by the v1 specification.
const (
	RunOutcomeSuccess     RunOutcome = "success"
	RunOutcomeFailure     RunOutcome = "failure"
	RunOutcomeTimedOut    RunOutcome = "timed_out"
	RunOutcomeCancelled   RunOutcome = "cancelled" //nolint:misspell // The specification defines this persisted spelling.
	RunOutcomeStartFailed RunOutcome = "start_failed"
	RunOutcomeLost        RunOutcome = "lost"
)

// EventType is the stable name of a persisted lifecycle event.
type EventType string

// Event types produced by initial-slice transitions.
const (
	EventJobSubmitted       EventType = "job_submitted"
	EventSupervisorClaimed  EventType = "supervisor_claimed"
	EventSupervisorRenewed  EventType = "supervisor_lease_renewed"
	EventRunReserved        EventType = "run_reserved"
	EventProcessStarted     EventType = "process_started"
	EventStartFailed        EventType = "start_failed"
	EventCancellation       EventType = "cancellation_requested"
	EventRunCompleted       EventType = "run_completed"
	EventJobCompleted       EventType = "job_completed"
	EventSupervisorReleased EventType = "supervisor_released"
	EventSubmissionFailed   EventType = "submission_failed"
	EventOwnershipLost      EventType = "ownership_lost"
	EventJobWaiting         EventType = "job_waiting"
	EventJobQueued          EventType = "job_queued"
	EventJobStarting        EventType = "job_starting"
	EventRetryScheduled     EventType = "retry_scheduled"
	EventTimeout            EventType = "timeout_requested"
	EventJobPaused          EventType = "job_paused"
	EventJobResumed         EventType = "job_resumed"
	EventJobAborted         EventType = "job_aborted"
)

// EffectType describes an external action required after a transition commits.
type EffectType string

// Effects emitted by initial-slice transitions.
const (
	EffectLaunchSupervisor EffectType = "launch_supervisor"
	EffectStartTarget      EffectType = "start_target"
	EffectStopTarget       EffectType = "stop_target"
	EffectPauseTarget      EffectType = "pause_target"
	EffectResumeTarget     EffectType = "resume_target"
)

// StopReason identifies why termination was requested.
type StopReason string

// Initial stop reasons.
const (
	StopReasonCancellation StopReason = "cancellation"
	StopReasonTimeout      StopReason = "timeout"
)

// LogIntegrity describes the relationship between raw streams and their index.
type LogIntegrity string

// Log integrity states.
const (
	LogIntegrityPending LogIntegrity = "pending"
	LogIntegrityValid   LogIntegrity = "valid"
	LogIntegrityPartial LogIntegrity = "partial"
	LogIntegrityCorrupt LogIntegrity = "corrupt"
)

// RecordingHealth describes whether target output was captured successfully.
type RecordingHealth string

// Recording health states.
const (
	RecordingHealthy  RecordingHealth = "healthy"
	RecordingDegraded RecordingHealth = "degraded"
)

// CredentialHash is the SHA-256 digest of a 256-bit launch credential.
type CredentialHash [sha256.Size]byte

// NewCredentialHash validates and hashes one supervisor launch credential.
func NewCredentialHash(credential []byte) (CredentialHash, error) {
	if len(credential) != 32 {
		return CredentialHash{}, invalid("launch credential", "must contain exactly 32 bytes")
	}

	return sha256.Sum256(credential), nil
}

// CredentialHashFromBytes reconstructs a persisted credential digest.
func CredentialHashFromBytes(encoded []byte) (CredentialHash, error) {
	if len(encoded) != sha256.Size {
		return CredentialHash{}, invalid("launch credential hash", "must contain exactly 32 bytes")
	}

	var hash CredentialHash
	copy(hash[:], encoded)

	return hash, nil
}

// Bytes returns a defensive copy of the persisted digest.
func (hash CredentialHash) Bytes() []byte {
	return append([]byte(nil), hash[:]...)
}

// Empty reports whether no launch credential digest is present.
func (hash CredentialHash) Empty() bool {
	return hash == CredentialHash{}
}

// Matches compares the hash against a raw credential in constant time.
func (hash CredentialHash) Matches(credential []byte) bool {
	candidate, err := NewCredentialHash(credential)
	if err != nil {
		return false
	}

	return subtle.ConstantTimeCompare(hash[:], candidate[:]) == 1
}

// ProcessIdentity is the platform adapter's evidence for one exact process.
type ProcessIdentity struct {
	PID        int
	Platform   string
	CreationID string
	BootID     string
	TreeID     string
}

// Validate checks that the identity is stronger than a bare PID.
func (identity ProcessIdentity) Validate() error {
	if identity.PID <= 0 {
		return invalid("process PID", "must be positive")
	}
	if identity.Platform == "" {
		return invalid("process platform", "must not be empty")
	}
	if identity.CreationID == "" {
		return invalid("process creation identity", "must not be empty")
	}
	if identity.BootID == "" {
		return invalid("process boot identity", "must not be empty")
	}

	return nil
}

// CancellationIntent records durable user cancellation intent.
type CancellationIntent struct {
	RequestedAt time.Time
	Reason      StopReason
}

// ExitInfo records factual process-exit observations separately from outcome.
type ExitInfo struct {
	ExitCode       *int
	Signal         string
	PlatformReason string
	ObservedAt     time.Time
}

// Validate checks a factual exit observation.
func (information ExitInfo) Validate() error {
	if information.ObservedAt.IsZero() {
		return invalid("exit observation time", "must not be zero")
	}
	if information.ExitCode != nil && *information.ExitCode < 0 {
		return invalid("exit code", "must not be negative")
	}
	if information.ExitCode == nil && information.Signal == "" && information.PlatformReason == "" {
		return invalid("exit information", "must include a code or termination reason")
	}

	return nil
}

// LogMetadata locates raw streams and records their final integrity state.
type LogMetadata struct {
	StdoutPath      string
	StderrPath      string
	IndexPath       string
	IndexVersion    int
	StdoutSize      int64
	StderrSize      int64
	Integrity       LogIntegrity
	RecordingHealth RecordingHealth
	DiagnosticCode  string
	PrunedAt        *time.Time
	PrunedFiles     uint64
	PrunedBytes     uint64
}

// Available reports whether the run's captured logs remain in the filesystem.
func (metadata LogMetadata) Available() bool {
	return metadata.PrunedAt == nil
}

// Validate checks log metadata and path containment prerequisites.
func (metadata LogMetadata) Validate() error {
	paths := []string{metadata.StdoutPath, metadata.StderrPath, metadata.IndexPath}
	if err := validateLogPaths(paths); err != nil {
		return err
	}
	if metadata.IndexVersion != LogIndexVersion && metadata.IndexVersion != LogIndexVersionSegmented {
		return invalid("log index version", fmt.Sprintf(
			"must be %d or %d",
			LogIndexVersion,
			LogIndexVersionSegmented,
		))
	}
	if metadata.StdoutSize < 0 || metadata.StderrSize < 0 {
		return invalid("log stream size", "must not be negative")
	}
	if !metadata.Integrity.Valid() {
		return invalid("log integrity", "is unknown")
	}
	if !metadata.RecordingHealth.Valid() {
		return invalid("recording health", "is unknown")
	}

	return validateLogPruning(metadata)
}

func validateLogPaths(paths []string) error {
	for _, path := range paths {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return invalid("log path", "must be a clean absolute path")
		}
		if strings.ContainsRune(path, '\x00') {
			return invalid("log path", "must not contain NUL")
		}
	}
	if paths[0] == paths[1] || paths[0] == paths[2] || paths[1] == paths[2] {
		return invalid("log paths", "must be distinct")
	}

	return nil
}

func validateLogPruning(metadata LogMetadata) error {
	if metadata.PrunedAt == nil {
		if metadata.PrunedFiles != 0 || metadata.PrunedBytes != 0 {
			return invalid("log pruning metadata", "removed counts require a prune time")
		}
	} else if metadata.PrunedAt.IsZero() {
		return invalid("log prune time", "must not be zero")
	}

	return nil
}

// JobState is the current transactional snapshot of one job.
type JobState struct {
	ID                   JobID
	Spec                 JobSpec
	Phase                JobPhase
	Outcome              JobOutcome
	Revision             uint64
	SubmittedAt          time.Time
	ClaimedAt            *time.Time
	StartedAt            *time.Time
	CompletedAt          *time.Time
	ActiveRunID          RunID
	SupervisorID         SupervisorID
	LaunchCredentialHash CredentialHash
	ClaimDeadline        *time.Time
	Cancellation         *CancellationIntent
	LastDiagnosticCode   string
}

// Validate checks the persisted job snapshot invariants.
func (state JobState) Validate() error {
	if !state.ID.Valid() {
		return invalid("job ID", "must be a canonical UUIDv7")
	}
	if err := state.Spec.Validate(); err != nil {
		return fmt.Errorf("validate job specification: %w", err)
	}
	if !state.Phase.Valid() {
		return invalid("job phase", "is unknown")
	}
	if state.Outcome != "" && !state.Outcome.Valid() {
		return invalid("job outcome", "is unknown")
	}
	if state.Revision == 0 {
		return invalid("job revision", "must be positive")
	}
	if state.SubmittedAt.IsZero() {
		return invalid("job submission time", "must not be zero")
	}
	if err := validateOptionalJobIDs(state); err != nil {
		return err
	}
	if err := validateJobPhaseFields(state); err != nil {
		return err
	}

	return validateJobTimes(state)
}

// RunState is the current transactional snapshot of one run.
type RunState struct {
	ID                 RunID
	JobID              JobID
	Number             uint64
	Phase              RunPhase
	Outcome            RunOutcome
	Revision           uint64
	ResolvedExecutable string
	Process            *ProcessIdentity
	ReservedAt         time.Time
	StartedAt          *time.Time
	StopRequestedAt    *time.Time
	CompletedAt        *time.Time
	StopReason         StopReason
	Exit               *ExitInfo
	Logs               LogMetadata
	LastDiagnosticCode string
}

// Validate checks the persisted run snapshot invariants.
func (state RunState) Validate() error {
	if !state.ID.Valid() || !state.JobID.Valid() {
		return invalid("run identity", "must contain canonical run and job UUIDv7 values")
	}
	if state.Number == 0 {
		return invalid("run number", "must be positive")
	}
	if !state.Phase.Valid() {
		return invalid("run phase", "is unknown")
	}
	if state.Outcome != "" && !state.Outcome.Valid() {
		return invalid("run outcome", "is unknown")
	}
	if state.Revision == 0 || state.ReservedAt.IsZero() {
		return invalid("run revision and reserve time", "must be present")
	}
	if err := state.Logs.Validate(); err != nil {
		return fmt.Errorf("validate run logs: %w", err)
	}
	if err := validateRunPhaseFields(state); err != nil {
		return err
	}

	return validateRunTimes(state)
}

// SupervisorState is the current ownership and lease snapshot.
type SupervisorState struct {
	ID             SupervisorID
	JobID          JobID
	Revision       uint64
	Process        ProcessIdentity
	ClaimedAt      time.Time
	LeaseRenewedAt time.Time
	LeaseExpiresAt time.Time
	ReleasedAt     *time.Time
}

// Validate checks supervisor identity and lease invariants.
func (state SupervisorState) Validate() error {
	if !state.ID.Valid() || !state.JobID.Valid() {
		return invalid("supervisor identity", "must contain canonical supervisor and job UUIDv7 values")
	}
	if state.Revision == 0 {
		return invalid("supervisor revision", "must be positive")
	}
	if err := state.Process.Validate(); err != nil {
		return fmt.Errorf("validate supervisor process: %w", err)
	}
	if state.ClaimedAt.IsZero() || state.LeaseRenewedAt.IsZero() || state.LeaseExpiresAt.IsZero() {
		return invalid("supervisor lease time", "must be present")
	}
	if state.LeaseRenewedAt.Before(state.ClaimedAt) {
		return invalid("supervisor lease renewal", "must not precede claim")
	}
	if !state.LeaseExpiresAt.After(state.LeaseRenewedAt) {
		return invalid("supervisor lease expiry", "must follow renewal")
	}
	if state.ReleasedAt != nil && state.ReleasedAt.Before(state.ClaimedAt) {
		return invalid("supervisor release time", "must not precede claim")
	}

	return nil
}

// EventDraft is transition evidence awaiting an assigned EventID in the store.
type EventDraft struct {
	JobID        JobID
	RunID        RunID
	SupervisorID SupervisorID
	Entity       EntityKind
	EntityID     string
	Type         EventType
	FromPhase    string
	ToPhase      string
	FromOutcome  string
	ToOutcome    string
	Revision     uint64
	OccurredAt   time.Time
	Details      json.RawMessage
}

// WithID completes an event draft for persistence.
func (draft EventDraft) WithID(id EventID) (StateEvent, error) {
	event := StateEvent{ID: id, EventDraft: draft.clone()}
	if err := event.Validate(); err != nil {
		return StateEvent{}, err
	}

	return event, nil
}

// StateEvent is one immutable append-only lifecycle event.
type StateEvent struct {
	ID EventID
	EventDraft
}

// Validate checks event identity, linkage, and structured details.
func (event StateEvent) Validate() error {
	if !event.ID.Valid() {
		return invalid("event ID", "must be a canonical UUIDv7")
	}
	if err := event.validate(); err != nil {
		return err
	}

	return nil
}

// Effect is an external action to perform only after transition commit.
type Effect struct {
	Type EffectType
}

// TransitionResult contains replacement snapshots, event drafts, and required
// post-commit effects. Nil run or supervisor values mean unchanged/absent.
type TransitionResult struct {
	Job        JobState
	Run        *RunState
	Supervisor *SupervisorState
	Events     []EventDraft
	Effects    []Effect
}

// Valid reports whether the entity kind is defined.
func (kind EntityKind) Valid() bool {
	return slices.Contains([]EntityKind{EntityJob, EntityRun, EntitySupervisor}, kind)
}

// Valid reports whether the phase is defined by the v1 schema.
func (phase JobPhase) Valid() bool {
	return slices.Contains([]JobPhase{
		JobPhaseSubmitting,
		JobPhaseWaiting,
		JobPhaseQueued,
		JobPhaseStarting,
		JobPhaseRunning,
		JobPhaseBackoff,
		JobPhasePaused,
		JobPhaseStopping,
		JobPhaseCompleted,
	}, phase)
}

// Valid reports whether the run phase is defined.
func (phase RunPhase) Valid() bool {
	return slices.Contains([]RunPhase{
		RunPhaseStarting,
		RunPhaseRunning,
		RunPhasePaused,
		RunPhaseStopping,
		RunPhaseCompleted,
	}, phase)
}

// Valid reports whether the job outcome is defined.
func (outcome JobOutcome) Valid() bool {
	return slices.Contains([]JobOutcome{
		JobOutcomeSuccess,
		JobOutcomeFailure,
		JobOutcomeTimedOut,
		JobOutcomeCancelled,
		JobOutcomeAborted,
		JobOutcomeLost,
		JobOutcomeSubmissionFailed,
	}, outcome)
}

// Valid reports whether the run outcome is defined.
func (outcome RunOutcome) Valid() bool {
	return slices.Contains([]RunOutcome{
		RunOutcomeSuccess,
		RunOutcomeFailure,
		RunOutcomeTimedOut,
		RunOutcomeCancelled,
		RunOutcomeStartFailed,
		RunOutcomeLost,
	}, outcome)
}

// Valid reports whether the log integrity state is defined.
func (integrity LogIntegrity) Valid() bool {
	return slices.Contains([]LogIntegrity{
		LogIntegrityPending,
		LogIntegrityValid,
		LogIntegrityPartial,
		LogIntegrityCorrupt,
	}, integrity)
}

// Valid reports whether the recording health state is defined.
func (health RecordingHealth) Valid() bool {
	return health == RecordingHealthy || health == RecordingDegraded
}

func validateOptionalJobIDs(state JobState) error {
	if state.ActiveRunID != "" && !state.ActiveRunID.Valid() {
		return invalid("active run ID", "must be a canonical UUIDv7")
	}
	if state.SupervisorID != "" && !state.SupervisorID.Valid() {
		return invalid("supervisor ID", "must be a canonical UUIDv7")
	}

	return nil
}

func validateJobPhaseFields(state JobState) error {
	validators := []func(JobState) error{
		validateJobTerminalFields,
		validateJobClaimFields,
		validateJobOwnershipFields,
		validateJobCancellationFields,
	}
	for _, validator := range validators {
		if err := validator(state); err != nil {
			return err
		}
	}

	return nil
}

func validateJobTerminalFields(state JobState) error {
	if state.Phase == JobPhaseCompleted {
		if state.Outcome == "" || state.CompletedAt == nil {
			return invalid("completed job", "must have an outcome and completion time")
		}
		if state.ActiveRunID != "" {
			return invalid("completed job", "must not retain an active run")
		}
	} else if state.Outcome != "" || state.CompletedAt != nil {
		return invalid("active job", "must not have a terminal outcome or completion time")
	}

	return nil
}

func validateJobClaimFields(state JobState) error {
	if state.Phase == JobPhaseSubmitting {
		if state.LaunchCredentialHash.Empty() || state.ClaimDeadline == nil {
			return invalid("submitting job", "must have a launch credential and claim deadline")
		}
		if state.SupervisorID != "" || state.ClaimedAt != nil {
			return invalid("submitting job", "must not have a supervisor claim")
		}
	} else if !state.LaunchCredentialHash.Empty() || state.ClaimDeadline != nil {
		return invalid("claimed job", "must not retain launch credentials")
	}

	return nil
}

func validateJobOwnershipFields(state JobState) error {
	if state.Phase == JobPhaseRunning {
		if state.ActiveRunID == "" {
			return invalid("active job", "must identify its active run")
		}
	}
	if state.Phase == JobPhaseStopping && state.ActiveRunID == "" && state.Cancellation == nil {
		return invalid("stopping job", "requires an active run or cancellation intent")
	}
	if state.Phase == JobPhaseStarting || state.Phase == JobPhaseRunning || state.Phase == JobPhaseStopping {
		if state.SupervisorID == "" || state.ClaimedAt == nil {
			return invalid("owned job", "must identify its supervisor and claim time")
		}
	}
	if state.Phase == JobPhaseRunning && state.StartedAt == nil {
		return invalid("running job", "must have a start time")
	}

	return nil
}

func validateJobCancellationFields(state JobState) error {
	if state.Cancellation == nil {
		return nil
	}
	if state.Cancellation.RequestedAt.IsZero() ||
		(state.Cancellation.Reason != StopReasonCancellation && state.Cancellation.Reason != StopReasonTimeout) {
		return invalid("stop intent", "must have a request time and supported reason")
	}
	validState := state.Phase == JobPhaseStopping ||
		state.Phase == JobPhaseCompleted &&
			(state.Outcome == JobOutcomeCancelled || state.Outcome == JobOutcomeTimedOut || state.Outcome == JobOutcomeLost)
	if !validState {
		return invalid("stop intent", "requires a stopping or corresponding terminal job")
	}

	return nil
}

func validateJobTimes(state JobState) error {
	times := []*time.Time{state.ClaimedAt, state.StartedAt, state.CompletedAt}
	for _, candidate := range times {
		if candidate != nil && candidate.Before(state.SubmittedAt) {
			return invalid("job timestamp", "must not precede submission")
		}
	}
	if state.ClaimDeadline != nil && !state.ClaimDeadline.After(state.SubmittedAt) {
		return invalid("claim deadline", "must follow submission")
	}
	if state.ClaimedAt != nil && state.StartedAt != nil && state.StartedAt.Before(*state.ClaimedAt) {
		return invalid("job start time", "must not precede supervisor claim")
	}
	if state.StartedAt != nil && state.CompletedAt != nil && state.CompletedAt.Before(*state.StartedAt) {
		return invalid("job completion time", "must not precede start")
	}

	return nil
}

func validateRunPhaseFields(state RunState) error {
	validators := []func(RunState) error{
		validateRunTerminalFields,
		validateRunProcessFields,
		validateRunStopFields,
		validateRunExitFields,
	}
	for _, validator := range validators {
		if err := validator(state); err != nil {
			return err
		}
	}

	return nil
}

func validateRunTerminalFields(state RunState) error {
	if state.Phase == RunPhaseCompleted {
		if state.Outcome == "" || state.CompletedAt == nil {
			return invalid("completed run", "must have an outcome and completion time")
		}
		if state.Logs.Integrity == LogIntegrityPending {
			return invalid("completed run logs", "must have a final integrity state")
		}
	} else if state.Outcome != "" || state.CompletedAt != nil {
		return invalid("active run", "must not have a terminal outcome or completion time")
	}

	return nil
}

func validateRunProcessFields(state RunState) error {
	if state.Phase == RunPhaseRunning || state.Phase == RunPhasePaused {
		if state.Process == nil || state.StartedAt == nil || state.ResolvedExecutable == "" {
			return invalid("active run", "must have process identity, executable, and start time")
		}
	}
	if state.Phase == RunPhaseStopping && state.Process != nil &&
		(state.StartedAt == nil || state.ResolvedExecutable == "") {
		return invalid("stopping run", "a published process requires its executable and start time")
	}
	if state.Process != nil {
		if err := state.Process.Validate(); err != nil {
			return err
		}
	}

	return nil
}

func validateRunStopFields(state RunState) error {
	if state.Phase == RunPhaseStopping && (state.StopRequestedAt == nil || state.StopReason == "") {
		return invalid("stopping run", "must have durable stop intent")
	}

	return nil
}

func validateRunExitFields(state RunState) error {
	if state.Exit != nil {
		if err := state.Exit.Validate(); err != nil {
			return err
		}
		if state.Phase != RunPhaseCompleted {
			return invalid("run exit information", "is valid only for a completed run")
		}
	}
	mayOmitExit := state.Outcome == RunOutcomeStartFailed ||
		state.Outcome == RunOutcomeLost ||
		(state.Outcome == RunOutcomeCancelled || state.Outcome == RunOutcomeTimedOut) && state.Process == nil
	if state.Phase == RunPhaseCompleted && !mayOmitExit && state.Exit == nil {
		return invalid("completed run", "must have factual exit information")
	}
	if err := validateOutcomeExit(state.Outcome, state.Exit); err != nil {
		return err
	}

	return nil
}

func validateOutcomeExit(outcome RunOutcome, information *ExitInfo) error {
	if information == nil || outcome == "" {
		return nil
	}
	if outcome == RunOutcomeSuccess && information.ExitCode == nil {
		return invalid("successful run exit", "must contain an exit code")
	}

	return nil
}

func validateRunTimes(state RunState) error {
	times := []*time.Time{state.StartedAt, state.StopRequestedAt, state.CompletedAt}
	for _, candidate := range times {
		if candidate != nil && candidate.Before(state.ReservedAt) {
			return invalid("run timestamp", "must not precede reservation")
		}
	}
	if state.StartedAt != nil && state.CompletedAt != nil && state.CompletedAt.Before(*state.StartedAt) {
		return invalid("run completion time", "must not precede start")
	}
	if state.Exit != nil && state.CompletedAt != nil && state.Exit.ObservedAt.After(*state.CompletedAt) {
		return invalid("exit observation time", "must not follow run completion")
	}
	if state.Logs.PrunedAt != nil {
		if state.CompletedAt == nil {
			return invalid("log prune time", "requires a completed run")
		}
		if state.Logs.PrunedAt.Before(*state.CompletedAt) {
			return invalid("log prune time", "must not precede run completion")
		}
	}

	return nil
}

func (draft EventDraft) validate() error {
	if !draft.JobID.Valid() {
		return invalid("event job ID", "must be a canonical UUIDv7")
	}
	if draft.Entity == "" || !draft.Entity.Valid() || draft.EntityID == "" || draft.Type == "" {
		return invalid("event identity", "must include entity and event type")
	}
	if draft.Revision == 0 || draft.OccurredAt.IsZero() {
		return invalid("event revision and time", "must be present")
	}
	if draft.RunID != "" && !draft.RunID.Valid() {
		return invalid("event run ID", "must be a canonical UUIDv7")
	}
	if draft.SupervisorID != "" && !draft.SupervisorID.Valid() {
		return invalid("event supervisor ID", "must be a canonical UUIDv7")
	}
	if len(draft.Details) != 0 && !json.Valid(draft.Details) {
		return invalid("event details", "must contain valid JSON")
	}
	if err := validateEventEntity(draft); err != nil {
		return err
	}

	return nil
}

func validateEventEntity(draft EventDraft) error {
	switch draft.Entity {
	case EntityJob:
		if draft.EntityID != draft.JobID.String() || draft.RunID != "" || draft.SupervisorID != "" {
			return invalid("job event linkage", "must identify only its job")
		}
	case EntityRun:
		if draft.RunID == "" || draft.EntityID != draft.RunID.String() || draft.SupervisorID != "" {
			return invalid("run event linkage", "must identify its job and run")
		}
	case EntitySupervisor:
		if draft.SupervisorID == "" || draft.EntityID != draft.SupervisorID.String() || draft.RunID != "" {
			return invalid("supervisor event linkage", "must identify its job and supervisor")
		}
	default:
		return invalid("event entity", "is unknown")
	}

	return nil
}

func (draft EventDraft) clone() EventDraft {
	draft.Details = append(json.RawMessage(nil), draft.Details...)

	return draft
}
