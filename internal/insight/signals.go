package insight

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

const OperationalRuleVersion = "1"
const MaxOperationalSignals = 20000

type Rule string

const (
	RuleRestarts  Rule = "restarts"
	RuleCrashLoop Rule = "crash-loop"
	RuleOOM       Rule = "oom"
	RuleImagePull Rule = "image-pull"
	RulePending   Rule = "pending"
	RuleEvicted   Rule = "evicted"
)

type SignalState uint8

const (
	SignalNormal SignalState = iota
	SignalCandidate
	SignalActive
	SignalRecovering
	SignalResolved
	SignalSuspended
)

type SignalSeverity uint8

const (
	SignalInfo SignalSeverity = iota
	SignalWarning
	SignalHigh
)

type SuspensionReason uint8

const (
	SuspensionNone SuspensionReason = iota
	SuspensionSourceCoverage
	SuspensionRevisionGap
	SuspensionMissingStatus
	SuspensionCounterReset
	SuspensionInvalidCounter
	SuspensionInvalidTimestamp
	SuspensionBufferLimit
)

type OperationalConfig struct {
	RestartWindow         time.Duration
	RestartWarning        int32
	RestartHigh           int32
	CrashLoopDelay        time.Duration
	CrashLoopObservations int
	ImagePullWarning      time.Duration
	ImagePullHigh         time.Duration
	PendingWarning        time.Duration
	PendingHigh           time.Duration
	Recovery              time.Duration
	OOMQuiet              time.Duration
	EvictionQuiet         time.Duration
	FutureTolerance       time.Duration
	MaxRestartDeltas      int
}

func DefaultOperationalConfig() OperationalConfig {
	return OperationalConfig{RestartWindow: 5 * time.Minute, RestartWarning: 3, RestartHigh: 10, CrashLoopDelay: 30 * time.Second, CrashLoopObservations: 2, ImagePullWarning: 2 * time.Minute, ImagePullHigh: 10 * time.Minute, PendingWarning: 5 * time.Minute, PendingHigh: 15 * time.Minute, Recovery: time.Minute, OOMQuiet: 5 * time.Minute, EvictionQuiet: 5 * time.Minute, FutureTolerance: 30 * time.Second, MaxRestartDeltas: 256}
}

func (c OperationalConfig) Validate() error {
	if c.RestartWindow <= 0 || c.RestartWarning <= 0 || c.RestartHigh < c.RestartWarning || c.CrashLoopDelay <= 0 || c.CrashLoopObservations < 2 || c.ImagePullWarning <= 0 || c.ImagePullHigh < c.ImagePullWarning || c.PendingWarning <= 0 || c.PendingHigh < c.PendingWarning || c.Recovery <= 0 || c.OOMQuiet <= 0 || c.EvictionQuiet <= 0 || c.FutureTolerance < 0 || c.MaxRestartDeltas < 1 {
		return errors.New("invalid operational thresholds")
	}
	return nil
}

type SignalEvidence struct {
	TransitionObservedAt time.Time
	Reason               Reason
	Phase                PodPhase
	NodeName             string
	CreatedAt            time.Time
	EventAt              time.Time
	ReceivedAt           time.Time
	CounterBefore        int32
	CounterAfter         int32
	WindowRestarts       int64
	ExitCode             int32
	Duration             time.Duration
	Observations         int
}

type Signal struct {
	ID              string
	Rule            Rule
	RuleVersion     string
	ConfigHash      string
	Entity          EntityRef
	Container       string
	ContainerKind   ContainerKind
	State           SignalState
	Severity        SignalSeverity
	PeakSeverity    SignalSeverity
	Preexisting     bool
	FirstObservedAt time.Time
	LastObservedAt  time.Time
	ResolvedAt      time.Time
	Coverage        Coverage
	Suspension      SuspensionReason
	Evidence        SignalEvidence
}

type SignalCount struct {
	Rule    Rule
	Total   int
	Omitted int
}

type SignalSnapshot struct {
	TotalSignals   int
	OmittedSignals int
	Counts         []SignalCount
	Source         SourceIdentity
	ConfigHash     string
	RuleVersion    string
	Coverage       Coverage
	Signals        []Signal
}

func operationalHash(value any) string {
	encoded, _ := json.Marshal(value)
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:])
}

func operationalSignalID(key signalKey, config string, episode uint64) string {
	return operationalHash(struct {
		Key     signalKey
		Config  string
		Version string
		Episode uint64
	}{key, config, OperationalRuleVersion, episode})
}
